// SPDX-License-Identifier: AGPL-3.0-or-later

package scorer

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/state"
	"github.com/some-du6e/pairtime/internal/agent/store"
	"github.com/some-du6e/pairtime/internal/timelines"
	"github.com/some-du6e/pairtime/pkg/event"
	pkgledger "github.com/some-du6e/pairtime/pkg/ledger"
	"github.com/some-du6e/pairtime/pkg/params"
	"github.com/some-du6e/pairtime/pkg/score"
)

// testKeyID names the test device key's chain.
const testKeyID = "00112233445566778899aabbccddeeff"

// creditFixtures are the fixtures whose totals pkg/score reproduces today
// (pkg/score's minutes_test.go): the others need WP-SC3's timeline across
// projects or WP-SC6's dark flow.
var creditFixtures = []string{"6.1", "6.2", "6.4", "6.6"}

func testTable() *params.Table {
	tables := params.Tables()
	return tables[len(tables)-1]
}

func testKeys(t *testing.T) *pkgledger.Keys {
	t.Helper()
	k, err := pkgledger.DeriveKeys(bytes.Repeat([]byte{0x5a}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// fixtureDevice says where a fixture's key presses come from, as
// pkg/score's tests do: off macOS the OS reports none, so a machine with
// input_keys has the typing module.
func fixtureDevice(f *timelines.Fixture) score.Device {
	sys := f.Expected.OS
	return score.Device{TypingModule: f.Expected.InputKeys && sys != "macos" && sys != "darwin"}
}

func loadFixtures(t *testing.T) []*timelines.Fixture {
	t.Helper()
	fs, err := timelines.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) == 0 {
		t.Fatal("no fixtures under testdata/timelines")
	}
	return fs
}

func fixture(t *testing.T, name string) *timelines.Fixture {
	t.Helper()
	for _, f := range loadFixtures(t) {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no fixture %s", name)
	return nil
}

// rig is a data directory with a ledger, the stores and state.json, as the
// agent keeps them.
type rig struct {
	t     *testing.T
	dir   string
	table *params.Table
	dev   score.Device
	keys  *pkgledger.Keys
	clock atomic.Int64 // what the ledger writer's clock reads, Unix ms
	// snapshot, when set, is called with state.json's bytes and the files
	// in days/ after the event at index snapAt is written.
	snapAt   int
	snapshot func(state []byte, days map[string][]byte)
	// wrap, when set, wraps the bucket store every loop gets; setup, when
	// set, gets every loop before it starts; and each, when set, is called
	// after write writes the event at index i.
	wrap  func(BucketStore) BucketStore
	setup func(*Loop)
	each  func(i int, l *Loop, w *ledger.Writer)

	mu       sync.Mutex
	problems []string // what the loops told doctor, in order
}

func newRig(t *testing.T, dev score.Device) *rig {
	return &rig{t: t, dir: t.TempDir(), table: testTable(), dev: dev, keys: testKeys(t), snapAt: -1}
}

func (r *rig) path(name string) string { return filepath.Join(r.dir, name) }

// open makes a loop over the rig's directory, as the agent does at start.
func (r *rig) open() (*Loop, *store.Buckets) {
	r.t.Helper()
	st, _, err := state.Open(r.path("state.json"))
	if err != nil {
		r.t.Fatal(err)
	}
	bs, err := store.OpenBuckets(r.path("buckets"))
	if err != nil {
		r.t.Fatal(err)
	}
	var b BucketStore = bs
	if r.wrap != nil {
		b = r.wrap(bs)
	}
	l, err := New(Options{Ledger: r.path("ledger"), Params: r.table, Device: r.dev, State: st, Buckets: b,
		Days: r.path("days"), Zone: func() *time.Location { return time.UTC }, Problem: r.problem})
	if err != nil {
		r.t.Fatal(err)
	}
	if r.setup != nil {
		r.setup(l)
	}
	return l, bs
}

func (r *rig) problem(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.problems = append(r.problems, msg)
}

// lastProblem returns what the loops last told doctor, "" for nothing.
func (r *rig) lastProblem() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.problems) == 0 {
		return ""
	}
	return r.problems[len(r.problems)-1]
}

// place returns the last batch of the place state.json holds.
func (r *rig) place() uint64 {
	r.t.Helper()
	data, err := os.ReadFile(r.path("state.json"))
	if err != nil {
		r.t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		r.t.Fatal(err)
	}
	var c cursor
	if raw, ok := keys[StateKey]; ok {
		if err := json.Unmarshal(raw, &c); err != nil {
			r.t.Fatal(err)
		}
	}
	return c.Batch
}

// settle seals what w holds and waits until l has scored it.
func (r *rig) settle(l *Loop, w *ledger.Writer) {
	r.t.Helper()
	if _, _, err := w.Seal(); err != nil {
		r.t.Fatal(err)
	}
	l.Flush()
}

// write writes evs to the rig's ledger through a ledger writer that hands
// every batch it seals to a loop, as the agent does. The writer seals when
// the clock has passed BATCH_S since the last seal, as the agent's
// scheduler has it do, and at once after a prompt or a stop. note, when
// set, says after which events an upload-failure note goes in the ledger.
func (r *rig) write(evs []event.Event, note func(i int) bool) {
	r.t.Helper()
	l, bs := r.open()
	w, err := ledger.Open(ledger.Options{Dir: r.path("ledger"), Keys: r.keys, KeyID: testKeyID, Params: r.table,
		Sealed: l.Sealed, Now: func() time.Time { return time.UnixMilli(r.clock.Load()) }})
	if err != nil {
		r.t.Fatal(err)
	}
	l.Start()
	batch := r.table.Int(params.BATCH_S) * 1000
	var next int64
	for i, ev := range evs {
		r.clock.Store(ev.T)
		if next == 0 {
			next = ev.T + batch
		}
		if ev.T >= next {
			if _, _, err := w.Seal(); err != nil {
				r.t.Fatal(err)
			}
			next = ev.T + batch
		}
		if _, err := w.Write(input(ev)); err != nil {
			r.t.Fatalf("event %d, %s: %v", i, ev.Kind(), err)
		}
		if note != nil && note(i) {
			if _, err := w.UploadFailure(uint64(ev.T), "connect"); err != nil {
				r.t.Fatal(err)
			}
		}
		if i == r.snapAt && r.snapshot != nil {
			r.settle(l, w)
			data, err := os.ReadFile(r.path("state.json"))
			if err != nil {
				r.t.Fatal(err)
			}
			r.snapshot(data, r.dirFiles("days"))
		}
		if r.each != nil {
			r.each(i, l, w)
		}
	}
	if err := w.Close(); err != nil {
		r.t.Fatal(err)
	}
	l.Close()
	if err := bs.Close(); err != nil {
		r.t.Fatal(err)
	}
}

// rescore runs a new loop over what the rig's ledger holds, as the agent
// does when it starts, and returns its status.
func (r *rig) rescore() Status {
	r.t.Helper()
	l, bs := r.open()
	l.Start()
	l.Flush()
	st := l.Status()
	l.Close()
	if err := bs.Close(); err != nil {
		r.t.Fatal(err)
	}
	return st
}

// stored returns the latest revision of every minute the rig's bucket
// store holds from from up to to.
func (r *rig) stored(from, to int64) []store.Bucket {
	r.t.Helper()
	bs, err := store.OpenBuckets(r.path("buckets"))
	if err != nil {
		r.t.Fatal(err)
	}
	defer bs.Close()
	got, err := bs.Range(from, to)
	if err != nil {
		r.t.Fatal(err)
	}
	return got
}

// files returns the bytes of every file under the rig's buckets/ and
// days/, by name.
func (r *rig) files() map[string][]byte {
	r.t.Helper()
	out := map[string][]byte{}
	for _, sub := range []string{"buckets", "days"} {
		for name, data := range r.dirFiles(sub) {
			out[sub+"/"+name] = data
		}
	}
	return out
}

// dirFiles returns the bytes of every file in the rig's directory sub, by
// name.
func (r *rig) dirFiles(sub string) map[string][]byte {
	r.t.Helper()
	out := map[string][]byte{}
	entries, err := os.ReadDir(r.path(sub))
	if err != nil {
		r.t.Fatal(err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(r.path(sub), e.Name()))
		if err != nil {
			r.t.Fatal(err)
		}
		out[e.Name()] = data
	}
	return out
}

// input is ev as the agent hands it to the ledger writer, with a stand-in
// for the text its record commits. The writer derives the low_effort and
// error_sig of a prompt from its text and of a track call from its label,
// so the stand-in is "ok" for a low-effort one, and otherwise, after an
// error line when it has an error signature, prose of the prompt's length
// or "a track label". Bridge and plugin prompts, and a plugin's final
// message, come without their text.
func input(ev event.Event) ledger.Input {
	in := ledger.Input{Event: ev}
	switch p := ev.Data.(type) {
	case *event.PromptSubmit:
		if ev.Via == event.ViaBridge || ev.Via == event.ViaBridgeUnverified || ev.Via == event.ViaPlugin {
			return in
		}
		in.Text = []byte(promptText(p))
	case *event.AgentOutput:
		if p.IsFinal && ev.Via != event.ViaPlugin {
			in.Text = []byte(strings.Repeat("word ", max(p.Words, 1)))
		}
	case *event.CLITrack:
		in.Text = []byte(trackText(p))
	}
	return in
}

// trackText is the stand-in label of a track call, from which the writer
// derives the call's low_effort and error_sig again (input).
func trackText(p *event.CLITrack) string {
	switch {
	case p.LowEffort:
		return "ok"
	case p.ErrorSig != "":
		return "TypeError: x is undefined\na track label"
	}
	return "a track label"
}

func promptText(p *event.PromptSubmit) string {
	if p.LowEffort {
		return "ok"
	}
	var b strings.Builder
	if p.ErrorSig != "" {
		b.WriteString("TypeError: x is undefined\n")
	}
	for b.Len() < max(p.LenChars, 4) {
		b.WriteString("make the page load faster ")
	}
	return b.String()
}

// direct scores evs in one Step, sums what it derives into minute buckets
// the way the loop does, and gives each the state that Step's FinalBefore
// gives it.
func direct(table *params.Table, dev score.Device, device string, evs []event.Event) (map[int64]store.Bucket, int64) {
	_, out := score.Step(score.NewState(table, dev), evs)
	meta := bucketMeta{v: score.Version(), params: table.Hash(), device: device}
	var all score.Derived
	all.Append(out.Final)
	all.Append(out.Pending)
	bs := minuteBuckets(meta, all, evs)
	for m, b := range bs {
		b.State = event.StatePending
		if m+minuteMS <= out.FinalBefore {
			b.State = event.StateFinal
		}
		bs[m] = b
	}
	return bs, out.FinalBefore
}

// content is a bucket's JSON without what depends on how the loop came to
// write it: its rev, the batch that wrote it and its state.
func content(b store.Bucket) string {
	b.Rev, b.Batch, b.State = 0, 0, ""
	j, err := json.Marshal(b)
	if err != nil {
		return err.Error()
	}
	return string(j)
}

// compare reports every minute where the loop's latest revisions got and
// one Step's buckets want differ, at most a dozen of them.
func compare(t *testing.T, got []store.Bucket, want map[int64]store.Bucket) {
	t.Helper()
	errs := 0
	fail := func(format string, args ...any) {
		if errs++; errs <= 12 {
			t.Errorf(format, args...)
		}
	}
	seen := map[int64]bool{}
	for _, g := range got {
		m := g.At()
		seen[m] = true
		w, ok := want[m]
		switch {
		case g.Empty() && ok:
			fail("%s: the loop's latest revision is empty, and one Step gives %s", g.Minute, content(w))
		case g.Empty():
		case !ok:
			fail("%s: the loop has %s, and one Step has no bucket", g.Minute, content(g))
		case content(g) != content(w):
			fail("%s:\nthe loop:  %s\none Step:  %s", g.Minute, content(g), content(w))
		case w.State == event.StateFinal && g.State != event.StateFinal:
			fail("%s is final after one Step and %s in the loop", g.Minute, g.State)
		}
	}
	for _, m := range slices.Sorted(maps.Keys(want)) {
		if !seen[m] {
			fail("%s: one Step gives %s, and the loop has no bucket", want[m].Minute, content(want[m]))
		}
	}
	if errs > 12 {
		t.Errorf("and %d more", errs-12)
	}
}

// span returns from a day before evs to a day after them.
func span(evs []event.Event) (from, to int64) {
	from, to = evs[0].T, evs[0].T
	for _, e := range evs {
		from, to = min(from, e.T), max(to, e.T)
	}
	return from - dayMS, to + dayMS
}

func TestFixturesThroughTheLedger(t *testing.T) {
	for _, f := range loadFixtures(t) {
		t.Run(f.Name+": its events through the ledger writer, scored a Step per sealed batch, give the buckets of one Step over them", func(t *testing.T) {
			t.Parallel()
			r := newRig(t, fixtureDevice(f))
			r.write(f.Events, nil)
			chain, err := ledger.ReadChain(r.path("ledger"))
			if err != nil {
				t.Fatal(err)
			}
			want, _ := direct(r.table, r.dev, chain.DeviceID, f.Events)
			from, to := span(f.Events)
			got := r.stored(from, to)
			compare(t, got, want)
			if !slices.Contains(creditFixtures, f.Name) {
				return
			}
			t.Run("the stored minutes of SM's window sum to SM's totals, and every uncredited second has a project", func(t *testing.T) {
				wfrom, wto := f.Expected.Window.Start.UnixMilli(), f.Expected.Window.End.UnixMilli()
				var halves, agentOnly int64
				var agent float64
				listed := map[event.Harness]bool{}
				byMinute, byProject := map[event.Reason]float64{}, map[event.Reason]float64{}
				for _, b := range got {
					if b.At() < wfrom || b.At() >= wto {
						continue
					}
					halves += b.Halves()
					agent += b.AgentRunningS
					for r, s := range b.Uncredited {
						byMinute[r] += s
					}
					for _, pm := range b.Projects {
						agentOnly += pm.AgentOnlyS
						for h := range pm.Harnesses {
							listed[h] = true
						}
						for r, s := range pm.Uncredited {
							byProject[r] += float64(s)
						}
					}
				}
				tot := f.Expected.Totals
				if float64(halves)/2 != tot.CreditedS {
					t.Errorf("credited %v s, SM says %v s", float64(halves)/2, tot.CreditedS)
				}
				if agent != tot.AgentRunningS {
					t.Errorf("an agent ran %v s, SM says %v s", agent, tot.AgentRunningS)
				}
				if want, ok := tot.Other["agent_only_s"]; ok && float64(agentOnly) != want {
					t.Errorf("agent-only %v s, SM says %v s", agentOnly, want)
				}
				if !maps.Equal(byMinute, byProject) {
					t.Errorf("the minutes leave %v uncredited, and their projects %v", byMinute, byProject)
				}
				harnesses := map[event.Harness]bool{}
				for _, e := range f.Events {
					if isHarness(e.Harness) {
						harnesses[e.Harness] = true
					}
				}
				for h := range listed {
					if !harnesses[h] {
						t.Errorf("the minutes list harness %s, which has no record in the fixture", h)
					}
				}
				if agentOnly > 0 && len(listed) == 0 {
					t.Error("agent-only time with no harness listed")
				}
			})
		})
	}
}

func TestUploadFailureNotes(t *testing.T) {
	f := fixture(t, "6.2")
	tests := []struct {
		name string
		note func(i int) bool
	}{
		{"a note after every fifth event takes record numbers the events skip, and the loop scores on", func(i int) bool { return i%5 == 4 }},
		{"a note after every event leaves every event's seq apart from the next, and the loop scores on", func(int) bool { return true }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, fixtureDevice(f))
			r.write(f.Events, tt.note)
			st := r.rescore()
			if st.Stopped != "" {
				t.Fatalf("the loop stopped: %s", st.Stopped)
			}
			chain, err := ledger.ReadChain(r.path("ledger"))
			if err != nil {
				t.Fatal(err)
			}
			want, _ := direct(r.table, r.dev, chain.DeviceID, f.Events)
			from, to := span(f.Events)
			compare(t, r.stored(from, to), want)
		})
	}
}

func TestRebuild(t *testing.T) {
	f := fixture(t, "6.1")
	tests := []struct {
		name string
		// lose removes what a crash or the teen removed, given state.json
		// and days/ as they were partway through the run.
		lose func(r *rig, early []byte, earlyDays map[string][]byte)
	}{
		{"a restart with state.json in place scores nothing twice", func(*rig, []byte, map[string][]byte) {}},
		{"deleting state.json rebuilds byte-identical buckets and rollups", func(r *rig, _ []byte, _ map[string][]byte) {
			removeAll(r.t, r.path("state.json"))
		}},
		{"deleting state.json, buckets/ and days/ rebuilds byte-identical buckets and rollups", func(r *rig, _ []byte, _ map[string][]byte) {
			removeAll(r.t, r.path("state.json"), r.path("buckets"), r.path("days"))
		}},
		{"a crash between a Step's bucket appends and its save, which left state.json and days/ from the middle of the run, leaves no day behind its buckets", func(r *rig, early []byte, earlyDays map[string][]byte) {
			if err := os.WriteFile(r.path("state.json"), early, 0o600); err != nil {
				r.t.Fatal(err)
			}
			removeAll(r.t, r.path("days"))
			if err := os.MkdirAll(r.path("days"), 0o700); err != nil {
				r.t.Fatal(err)
			}
			for name, data := range earlyDays {
				if err := os.WriteFile(filepath.Join(r.path("days"), name), data, 0o600); err != nil {
					r.t.Fatal(err)
				}
			}
		}},
		{"a crash that kept an early place in state.json and tore the last bucket line loses nothing and writes nothing twice", func(r *rig, early []byte, _ map[string][]byte) {
			if err := os.WriteFile(r.path("state.json"), early, 0o600); err != nil {
				r.t.Fatal(err)
			}
			entries, err := os.ReadDir(r.path("buckets"))
			if err != nil || len(entries) == 0 {
				r.t.Fatal("no bucket files", err)
			}
			last := filepath.Join(r.path("buckets"), entries[len(entries)-1].Name())
			fh, err := os.OpenFile(last, os.O_WRONLY|os.O_APPEND, 0)
			if err != nil {
				r.t.Fatal(err)
			}
			fh.WriteString(`{"v":"0.1.0","params":"sha256:`)
			fh.Close()
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, fixtureDevice(f))
			var early []byte
			var earlyDays map[string][]byte
			r.snapAt, r.snapshot = len(f.Events)/2, func(b []byte, days map[string][]byte) { early, earlyDays = b, days }
			r.write(f.Events, nil)
			want := r.files()
			if len(early) == 0 {
				t.Fatal("no state.json from the middle of the run")
			}
			tt.lose(r, early, earlyDays)
			if st := r.rescore(); st.Stopped != "" {
				t.Fatalf("the loop stopped: %s", st.Stopped)
			}
			got := r.files()
			for _, name := range slices.Sorted(maps.Keys(want)) {
				if !bytes.Equal(got[name], want[name]) {
					t.Errorf("%s differs: %d bytes, want %d", name, len(got[name]), len(want[name]))
				}
			}
			for _, name := range slices.Sorted(maps.Keys(got)) {
				if _, ok := want[name]; !ok {
					t.Errorf("%s is new", name)
				}
			}
		})
	}
}

func removeAll(t *testing.T, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.RemoveAll(n); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRevisions(t *testing.T) {
	f := fixture(t, "6.1")
	r := newRig(t, fixtureDevice(f))
	r.write(f.Events, nil)
	st := r.rescore()
	entries, err := os.ReadDir(r.path("buckets"))
	if err != nil {
		t.Fatal(err)
	}
	revs := map[int64][]store.Bucket{}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(r.path("buckets"), e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
			var b store.Bucket
			if err := json.Unmarshal([]byte(line), &b); err != nil {
				t.Fatalf("%s: %v", e.Name(), err)
			}
			revs[b.At()] = append(revs[b.At()], b)
		}
	}
	tests := []struct {
		name  string
		check func(m int64, bs []store.Bucket) string
	}{
		{"a minute's revisions are numbered from 0, one at a time", func(_ int64, bs []store.Bucket) string {
			for i, b := range bs {
				if b.Rev != uint32(i) {
					return "revision " + strconv.Itoa(i) + " has rev " + strconv.Itoa(int(b.Rev))
				}
			}
			return ""
		}},
		{"no revision follows a final one", func(_ int64, bs []store.Bucket) string {
			for i, b := range bs {
				if b.State == event.StateFinal && i != len(bs)-1 {
					return "a revision follows the final one"
				}
			}
			return ""
		}},
		{"a minute is final once FinalBefore passes its end, and pending before", func(m int64, bs []store.Bucket) string {
			last := bs[len(bs)-1]
			if final := m+minuteMS <= st.FinalBefore; final != (last.State == event.StateFinal) {
				return "the latest revision is " + string(last.State)
			}
			return ""
		}},
		{"every revision carries pkg/score's VERSION as v and the parameter table's hash as params", func(_ int64, bs []store.Bucket) string {
			for _, b := range bs {
				if b.V != score.Version() || b.Params != testTable().Hash() {
					return "v " + b.V + ", params " + b.Params
				}
			}
			return ""
		}},
		{"every revision says something the one before it didn't", func(_ int64, bs []store.Bucket) string {
			for i := 1; i < len(bs); i++ {
				a, b := bs[i-1], bs[i]
				a.State, b.State = "", ""
				if content(a) == content(b) && bs[i-1].State == bs[i].State {
					return "revision " + strconv.Itoa(i) + " repeats the one before"
				}
			}
			return ""
		}},
	}
	if st.FinalBefore == 0 || len(revs) == 0 {
		t.Fatalf("nothing final (FinalBefore %d) or no buckets (%d minutes)", st.FinalBefore, len(revs))
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, m := range slices.Sorted(maps.Keys(revs)) {
				if msg := tt.check(m, revs[m]); msg != "" {
					t.Errorf("%s: %s", time.UnixMilli(m).UTC().Format(time.RFC3339), msg)
				}
			}
		})
	}
}

// lastRow returns the row of the last batch the rig's ledger holds.
func (r *rig) lastRow() pkgledger.BatchRow {
	r.t.Helper()
	var row pkgledger.BatchRow
	if err := ledger.ReadBatches(r.path("ledger"), 1, func(b ledger.Batch) error {
		row = b.Row
		return nil
	}); err != nil {
		r.t.Fatal(err)
	}
	return row
}

// notes returns batch b of n upload-failure notes numbered from first,
// which hold no event.
func notes(t *testing.T, b, first, n uint64) ledger.Batch {
	t.Helper()
	out := ledger.Batch{Row: pkgledger.BatchRow{B: b, FirstSeq: first, N: n}}
	for s := first; s < first+n; s++ {
		h, err := pkgledger.NewHeader(s, pkgledger.RTypeUploadFailure, "", event.NoSession)
		if err != nil {
			t.Fatal(err)
		}
		out.Records = append(out.Records, pkgledger.Record{Header: h})
	}
	return out
}

func TestGaps(t *testing.T) {
	f := fixture(t, "6.2")
	tests := []struct {
		name string
		// then are the batches the loop gets after 6.2's, whose last batch
		// is b and last record s.
		then func(t *testing.T, b, s uint64) []ledger.Batch
		// at is the batch the loop must be at after them, counted from b,
		// and failed how many batches must have failed.
		at, failed uint64
		// says is what doctor must hear, in part.
		says func(b uint64) string
	}{
		{
			name: "batches the ledger's files don't hold fail alone, and the batch after them counts from its own row",
			then: func(t *testing.T, b, s uint64) []ledger.Batch {
				return []ledger.Batch{notes(t, b+3, s+10, 2), notes(t, b+4, s+12, 1)}
			},
			at: 4, failed: 2,
			says: func(b uint64) string { return fmt.Sprintf("batches %d to %d aren't in the ledger's files", b+1, b+2) },
		},
		{
			name: "a batch whose first record skips numbers fails alone, and the next one counts",
			then: func(t *testing.T, b, s uint64) []ledger.Batch {
				return []ledger.Batch{notes(t, b+1, s+3, 1), notes(t, b+2, s+4, 2)}
			},
			at: 2, failed: 1, says: func(b uint64) string { return fmt.Sprintf("batch %d isn't scored", b+1) },
		},
		{
			name: "a batch that repeats a record number fails alone, and the next one counts",
			then: func(t *testing.T, b, s uint64) []ledger.Batch {
				return []ledger.Batch{notes(t, b+1, s, 2), notes(t, b+2, s+2, 1)}
			},
			at: 2, failed: 1, says: func(b uint64) string { return fmt.Sprintf("batch %d isn't scored", b+1) },
		},
		{
			name: "a batch holding fewer records than its row says fails alone, and the next one counts",
			then: func(t *testing.T, b, s uint64) []ledger.Batch {
				short := notes(t, b+1, s+1, 3)
				short.Records = short.Records[:2]
				return []ledger.Batch{short, notes(t, b+2, s+4, 1)}
			},
			at: 2, failed: 1, says: func(b uint64) string { return fmt.Sprintf("batch %d isn't scored", b+1) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, fixtureDevice(f))
			r.write(f.Events, nil)
			row := r.lastRow()
			l, bs := r.open()
			l.Start()
			l.Flush()
			if st := l.Status(); st.Stopped != "" || st.Batch != row.B {
				t.Fatalf("before the gap the loop is %+v, want it at batch %d", st, row.B)
			}
			for _, b := range tt.then(t, row.B, row.FirstSeq+row.N-1) {
				l.Sealed(b)
			}
			l.Flush()
			after := l.Status()
			l.Close()
			if err := bs.Close(); err != nil {
				t.Fatal(err)
			}
			if after.Stopped != "" {
				t.Fatalf("the loop stopped: %s", after.Stopped)
			}
			if after.Batch != row.B+tt.at || after.Failed != tt.failed {
				t.Errorf("the loop is at batch %d with %d failed, want %d with %d", after.Batch, after.Failed, row.B+tt.at, tt.failed)
			}
			if p, says := r.lastProblem(), tt.says(row.B); !strings.Contains(p, says) {
				t.Errorf("doctor hears %q, want it to say %q", p, says)
			}
			if got := r.place(); got != row.B+tt.at {
				t.Errorf("state.json's place is batch %d, want %d", got, row.B+tt.at)
			}
		})
	}
}

func TestNoPlaceStartsAtTheFirstBatchTheLedgerHolds(t *testing.T) {
	// 6.4 runs across midnight UTC, so its ledger has two segments.
	f := fixture(t, "6.4")
	r := newRig(t, fixtureDevice(f))
	r.write(f.Events, nil)
	var segs []string
	entries, err := os.ReadDir(r.path("ledger"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if day, ok := strings.CutSuffix(e.Name(), ".seg"); ok {
			if _, err := time.Parse(time.DateOnly, day); err == nil {
				segs = append(segs, e.Name())
			}
		}
	}
	slices.Sort(segs)
	if len(segs) < 2 {
		t.Fatalf("the ledger has segments %q, want two days of them", segs)
	}
	// Retention deletes the oldest segment, and state.json is lost.
	removeAll(t, filepath.Join(r.path("ledger"), segs[0]), r.path("state.json"), r.path("buckets"), r.path("days"))
	first, err := ledger.FirstHeld(r.path("ledger"), 1)
	if err != nil || first <= 1 {
		t.Fatalf("FirstHeld = %d, %v; want a batch after the first", first, err)
	}
	st := r.rescore()
	if st.Stopped != "" || st.Failed != 0 {
		t.Fatalf("the loop is %+v, want it scoring with nothing failed", st)
	}
	chain, err := ledger.ReadChain(r.path("ledger"))
	if err != nil {
		t.Fatal(err)
	}
	var evs []event.Event
	if err := ledger.ReadBatches(r.path("ledger"), first, func(b ledger.Batch) error {
		for _, rec := range b.Records {
			if ev, ok, err := eventOf(rec, chain.DeviceID); err == nil && ok {
				evs = append(evs, ev)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Run("the buckets are those of one Step over the batches the ledger holds", func(t *testing.T) {
		want, _ := direct(r.table, r.dev, chain.DeviceID, evs)
		from, to := span(f.Events)
		compare(t, r.stored(from, to), want)
	})
}

func TestCheckBatch(t *testing.T) {
	rec := func(t *testing.T, s uint64) pkgledger.Record {
		h, err := pkgledger.NewHeader(s, pkgledger.RTypeUploadFailure, "", event.NoSession)
		if err != nil {
			t.Fatal(err)
		}
		return pkgledger.Record{Header: h}
	}
	batch := func(t *testing.T, b, first, n uint64, seqs ...uint64) ledger.Batch {
		out := ledger.Batch{Row: pkgledger.BatchRow{B: b, FirstSeq: first, N: n}}
		for _, s := range seqs {
			out.Records = append(out.Records, rec(t, s))
		}
		return out
	}
	tests := []struct {
		name         string
		b            func(t *testing.T) ledger.Batch
		lastB, lastS uint64
		ok           bool
	}{
		{"the batch after the last, numbered on from the last record, passes", func(t *testing.T) ledger.Batch { return batch(t, 4, 11, 3, 11, 12, 13) }, 3, 10, true},
		{"the first batch starts at record 1", func(t *testing.T) ledger.Batch { return batch(t, 1, 1, 1, 1) }, 0, 0, true},
		{"a batch that skips a batch number is a gap", func(t *testing.T) ledger.Batch { return batch(t, 5, 11, 1, 11) }, 3, 10, false},
		{"a batch that repeats a batch number is a gap", func(t *testing.T) ledger.Batch { return batch(t, 3, 11, 1, 11) }, 3, 10, false},
		{"a batch that skips a record number is a gap", func(t *testing.T) ledger.Batch { return batch(t, 4, 12, 1, 12) }, 3, 10, false},
		{"a batch that repeats a record number is a gap", func(t *testing.T) ledger.Batch { return batch(t, 4, 10, 1, 10) }, 3, 10, false},
		{"a record numbered out of turn inside a batch is a gap", func(t *testing.T) ledger.Batch { return batch(t, 4, 11, 3, 11, 13, 12) }, 3, 10, false},
		{"a batch holding fewer records than its row says is a gap", func(t *testing.T) ledger.Batch { return batch(t, 4, 11, 3, 11, 12) }, 3, 10, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkBatch(tt.b(t), tt.lastB, tt.lastS)
			if (err == nil) != tt.ok {
				t.Errorf("checkBatch = %v, want ok %v", err, tt.ok)
			}
		})
	}
}

// faulty is a bucket store whose writes, or whose flushes, fail while it
// is told to.
type faulty struct {
	BucketStore
	puts, flushes atomic.Bool
}

func (f *faulty) Put(b store.Bucket) (store.Bucket, bool, error) {
	if f.puts.Load() {
		return store.Bucket{}, false, errors.New("injected: no space left on device")
	}
	return f.BucketStore.Put(b)
}

func (f *faulty) Sync() error {
	if f.flushes.Load() {
		return errors.New("injected: input/output error")
	}
	return f.BucketStore.Sync()
}

// Forget flushes the days it drops, so it fails as Sync does.
func (f *faulty) Forget(before int64) error {
	if f.flushes.Load() {
		return errors.New("injected: input/output error")
	}
	return f.BucketStore.Forget(before)
}

func TestWriteFailures(t *testing.T) {
	f := fixture(t, "6.1")
	clean := newRig(t, fixtureDevice(f))
	clean.write(f.Events, nil)
	from, to := span(f.Events)
	want := map[int64]store.Bucket{}
	for _, b := range clean.stored(from, to) {
		want[b.At()] = b
	}
	wantDays := clean.dirFiles("days")
	n := len(f.Events)
	tests := []struct {
		name string
		// fail turns the failure on or off.
		fail func(fs *faulty, days *atomic.Bool, on bool)
		// held says the place in state.json must not move while it lasts.
		held bool
		// untilStop keeps the failure on until the loop stops, and has a
		// new loop over the same files make it good.
		untilStop bool
	}{
		{
			name: "a bucket write that fails is written again at the next save, the place waits for it, and the problem lasts until then",
			fail: func(fs *faulty, _ *atomic.Bool, on bool) { fs.puts.Store(on) },
			held: true,
		},
		{
			name: "a flush that fails is tried again at the next save, the place waits for it, and the problem lasts until then",
			fail: func(fs *faulty, _ *atomic.Bool, on bool) { fs.flushes.Store(on) },
			held: true,
		},
		{
			name: "a day rollup that fails to write is written again at the next save, and the problem lasts until then",
			fail: func(_ *faulty, days *atomic.Bool, on bool) { days.Store(on) },
		},
		{
			name:      "a day rollup that fails to write until the agent stops is written when it starts again",
			fail:      func(_ *faulty, days *atomic.Bool, on bool) { days.Store(on) },
			untilStop: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, fixtureDevice(f))
			fs := &faulty{}
			var days atomic.Bool
			r.wrap = func(b BucketStore) BucketStore {
				fs.BucketStore = b
				return fs
			}
			r.setup = func(l *Loop) {
				l.writeDayFile = func(dir string, d store.Day) error {
					if days.Load() {
						return errors.New("injected: the rename outlasted its retries")
					}
					return store.WriteDay(dir, d)
				}
			}
			on, off := n/3, 2*n/3
			var placed uint64
			r.each = func(i int, l *Loop, w *ledger.Writer) {
				switch {
				case i == on:
					r.settle(l, w)
					placed = r.place()
					tt.fail(fs, &days, true)
				case i == off && !tt.untilStop:
					r.settle(l, w)
					if r.lastProblem() == "" {
						t.Error("doctor heard nothing while the writes failed")
					}
					if got := r.place(); tt.held && got != placed {
						t.Errorf("the place moved from batch %d to %d while the writes failed", placed, got)
					}
					tt.fail(fs, &days, false)
				}
			}
			r.write(f.Events, nil)
			if tt.untilStop {
				if r.lastProblem() == "" {
					t.Error("doctor heard nothing while the writes failed")
				}
				tt.fail(fs, &days, false)
				if st := r.rescore(); st.Stopped != "" {
					t.Fatalf("the loop stopped: %s", st.Stopped)
				}
			} else if p := r.lastProblem(); p != "" {
				t.Errorf("doctor still hears %q after the writes work again", p)
			}
			// The two runs' ledgers are two devices.
			chain, err := ledger.ReadChain(r.path("ledger"))
			if err != nil {
				t.Fatal(err)
			}
			mine := map[int64]store.Bucket{}
			for m, b := range want {
				b.Device = chain.DeviceID
				mine[m] = b
			}
			compare(t, r.stored(from, to), mine)
			got := r.dirFiles("days")
			for _, name := range slices.Sorted(maps.Keys(wantDays)) {
				if !bytes.Equal(got[name], wantDays[name]) {
					t.Errorf("days/%s isn't what a run with no failure writes", name)
				}
			}
		})
	}
}

// hookTurn returns a Claude Code turn the harness's hooks alone report,
// as WP-AD2 delivers one before WP-AD3 reads its transcript: 6.4's
// session start a minute before at, its prompt at at, and a tool call at
// at plus each of tools, in Unix ms, with no stop. Nobody is at the
// computer, and the agent samples presence once a minute, as it does
// after CONTEXT_IDLE_S without input (DESIGN 6.2 D14), so no sample gap is
// a sleep. Without samples the computer would be one whose agent never
// samples, scored with SM 5.2's codes for a slot whose presence is
// unknown, and not the desktop these turns run on.
func hookTurn(t *testing.T, at int64, tools []int64) []event.Event {
	t.Helper()
	var start, prompt, tool, sample *event.Event
	evs := fixture(t, "6.4").Events
	for i := range evs {
		e := &evs[i]
		switch e.Data.(type) {
		case *event.SessionStart:
			start = cmpOr(start, e)
		case *event.PromptSubmit:
			prompt = cmpOr(prompt, e)
		case *event.AgentTool:
			if e.Via == event.ViaHook {
				tool = cmpOr(tool, e)
			}
		case *event.OSPresence:
			sample = cmpOr(sample, e)
		}
	}
	if start == nil || prompt == nil || tool == nil || sample == nil {
		t.Fatal("6.4 has no session start, prompt, hooked tool call or presence sample")
	}
	n := 0
	clone := func(tmpl *event.Event, ts int64) event.Event {
		n++
		e := *tmpl
		e.EventID = fmt.Sprintf("01a1fb1e-5318-78a1-a781-%012x", n)
		e.Mono += ts - e.T
		e.T = ts
		return e
	}
	out := []event.Event{clone(start, at-60_000), clone(prompt, at)}
	for _, d := range tools {
		e := clone(tool, at+d)
		p := *tool.Data.(*event.AgentTool)
		p.TStart, p.TEnd = e.T-800, e.T
		e.Data = &p
		out = append(out, e)
	}
	end := at + slices.Max(tools) + 60_000
	for ts := at - 55_000; ts <= end; ts += 60_000 {
		e := clone(sample, ts)
		idle := float64(600 + (ts-at+55_000)/1000)
		e.Data = &event.OSPresence{IdleAnyS: idle, IdleKeysS: &idle}
		out = append(out, e)
	}
	slices.SortStableFunc(out, func(a, b event.Event) int { return cmp.Compare(a.T, b.T) })
	return out
}

// cmpOr returns cur, or e when cur is nil.
func cmpOr(cur, e *event.Event) *event.Event {
	if cur != nil {
		return cur
	}
	return e
}

func TestLongHookedTurns(t *testing.T) {
	const minute = 60_000
	every := func(from, to int64) []int64 {
		var out []int64
		for d := from; d <= to; d += 10 * minute {
			out = append(out, d)
		}
		return out
	}
	tests := []struct {
		name  string
		tools []int64 // after the prompt, in ms
	}{
		{
			name:  "a hooked turn quiet for 90 minutes after six hours of tool calls runs on through the window's start, as one Step over every record has it",
			tools: append(every(10*minute, 360*minute), every(450*minute, 840*minute)...),
		},
		{
			name:  "a hooked turn quiet for three hours ends two hours into the silence for good, as one Step over every record has it",
			tools: append(every(10*minute, 120*minute), every(300*minute, 780*minute)...),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			at := fixture(t, "6.4").Events[0].T
			evs := hookTurn(t, at, tt.tools)
			r := newRig(t, score.Device{})
			r.write(evs, nil)
			chain, err := ledger.ReadChain(r.path("ledger"))
			if err != nil {
				t.Fatal(err)
			}
			want, _ := direct(r.table, r.dev, chain.DeviceID, evs)
			if len(want) == 0 {
				t.Fatal("one Step over the turn gives no bucket")
			}
			from, to := span(evs)
			compare(t, r.stored(from, to), want)
		})
	}
}

func TestHoldFor(t *testing.T) {
	const h = 3600 * 1000
	from := int64(30 * h)
	turn := func(submit, runUntil int64) score.Turn { return score.Turn{TSubmit: submit, RunUntil: runUntil} }
	tests := []struct {
		name  string
		turns []score.Turn
		want  int64
	}{
		{"a turn that ran past the window's start holds it at the turn's submit", []score.Turn{turn(20*h, 31*h)}, 20 * h},
		{"a turn whose run ended at or before the window's start holds nothing", []score.Turn{turn(20*h, 30*h)}, from},
		{"a turn submitted inside the window holds nothing", []score.Turn{turn(30*h+1, 32*h)}, from},
		{"a turn submitted more than holdMaxMS before the window's start holds nothing", []score.Turn{turn(from-holdMaxMS-1, 31*h)}, from},
		{"a turn submitted holdMaxMS before the window's start still holds it", []score.Turn{turn(from-holdMaxMS, 31*h)}, from - holdMaxMS},
		{"of two turns that ran past it, the earlier submit holds the window's start", []score.Turn{turn(25*h, 31*h), turn(22*h, 30*h+1)}, 22 * h},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := holdFor(tt.turns, from); got != tt.want {
				t.Errorf("holdFor = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestFollower(t *testing.T) {
	short := func(t *testing.T, b, first, n uint64) ledger.Batch {
		out := notes(t, b, first, n)
		out.Records = out.Records[:n-1]
		return out
	}
	tests := []struct {
		name    string
		batches func(t *testing.T) []ledger.Batch
		scored  []bool
	}{
		{"batches that follow on are all scored", func(t *testing.T) []ledger.Batch {
			return []ledger.Batch{notes(t, 3, 10, 1), notes(t, 4, 11, 2), notes(t, 5, 13, 1)}
		}, []bool{true, true, true}},
		{"the first batch read starts the count, whatever its number", func(t *testing.T) []ledger.Batch {
			return []ledger.Batch{notes(t, 7, 40, 2)}
		}, []bool{true}},
		{"a batch whose first record skips numbers isn't scored, and the next one follows its row", func(t *testing.T) []ledger.Batch {
			return []ledger.Batch{notes(t, 3, 10, 1), notes(t, 4, 14, 1), notes(t, 5, 15, 1)}
		}, []bool{true, false, true}},
		{"a batch that repeats a record number isn't scored, and the next one follows its row", func(t *testing.T) []ledger.Batch {
			return []ledger.Batch{notes(t, 3, 10, 2), notes(t, 4, 11, 1), notes(t, 5, 12, 1)}
		}, []bool{true, false, true}},
		{"a batch holding fewer records than its row says isn't scored", func(t *testing.T) []ledger.Batch {
			return []ledger.Batch{notes(t, 3, 10, 1), short(t, 4, 11, 3), notes(t, 5, 14, 1)}
		}, []bool{true, false, true}},
		{"a batch after batches the files don't hold starts the count again from its own row", func(t *testing.T) []ledger.Batch {
			return []ledger.Batch{notes(t, 3, 10, 1), notes(t, 6, 30, 1), notes(t, 7, 31, 1)}
		}, []bool{true, true, true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var f follower
			var got []bool
			for _, b := range tt.batches(t) {
				got = append(got, f.next(b))
			}
			if !slices.Equal(got, tt.scored) {
				t.Errorf("scored %v, want %v", got, tt.scored)
			}
		})
	}
}
