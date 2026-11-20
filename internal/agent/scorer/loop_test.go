// SPDX-License-Identifier: AGPL-3.0-or-later

package scorer

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
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
	// snapshot, when set, is called with state.json's bytes after the
	// event at index snapAt is written.
	snapAt   int
	snapshot func([]byte)
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
	l, err := New(Options{Ledger: r.path("ledger"), Params: r.table, Device: r.dev, State: st, Buckets: bs,
		Days: r.path("days"), Zone: func() *time.Location { return time.UTC }})
	if err != nil {
		r.t.Fatal(err)
	}
	return l, bs
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
			if _, _, err := w.Seal(); err != nil {
				r.t.Fatal(err)
			}
			l.Flush()
			data, err := os.ReadFile(r.path("state.json"))
			if err != nil {
				r.t.Fatal(err)
			}
			r.snapshot(data)
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
		entries, err := os.ReadDir(r.path(sub))
		if err != nil {
			r.t.Fatal(err)
		}
		for _, e := range entries {
			data, err := os.ReadFile(filepath.Join(r.path(sub), e.Name()))
			if err != nil {
				r.t.Fatal(err)
			}
			out[sub+"/"+e.Name()] = data
		}
	}
	return out
}

// input is ev as the agent hands it to the ledger writer, with a stand-in
// for the text its record commits. The writer derives a prompt's
// low_effort and error_sig from its text, so the stand-in is "ok" for a
// low-effort prompt, and otherwise prose of the prompt's length, after an
// error line when the prompt has an error signature. Bridge and plugin
// prompts, and a plugin's final message, come without their text.
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
		in.Text = []byte("a track label")
	}
	return in
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
	meta := bucketMeta{v: table.Version(), params: table.Hash(), device: device}
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
		// as it was partway through the run.
		lose func(r *rig, early []byte)
	}{
		{"a restart with state.json in place scores nothing twice", func(*rig, []byte) {}},
		{"deleting state.json rebuilds byte-identical buckets and rollups", func(r *rig, _ []byte) {
			removeAll(r.t, r.path("state.json"))
		}},
		{"deleting state.json, buckets/ and days/ rebuilds byte-identical buckets and rollups", func(r *rig, _ []byte) {
			removeAll(r.t, r.path("state.json"), r.path("buckets"), r.path("days"))
		}},
		{"a crash that kept an early place in state.json and tore the last bucket line loses nothing and writes nothing twice", func(r *rig, early []byte) {
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
			r.snapAt, r.snapshot = len(f.Events)/2, func(b []byte) { early = b }
			r.write(f.Events, nil)
			want := r.files()
			if len(early) == 0 {
				t.Fatal("no state.json from the middle of the run")
			}
			tt.lose(r, early)
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

func TestGapStopsTheLoop(t *testing.T) {
	f := fixture(t, "6.2")
	r := newRig(t, fixtureDevice(f))
	r.write(f.Events, nil)
	l, bs := r.open()
	defer bs.Close()
	l.Start()
	l.Flush()
	before := l.Status()
	if before.Stopped != "" || before.Batch == 0 {
		t.Fatalf("the loop stopped or scored nothing: %+v", before)
	}
	rec := func(s uint64) pkgledger.Record {
		h, err := pkgledger.NewHeader(s, pkgledger.RTypeUploadFailure, "", event.NoSession)
		if err != nil {
			t.Fatal(err)
		}
		return pkgledger.Record{Header: h}
	}
	// Batch b+2, which the ledger's files don't hold, nor b+1.
	next := before.Batch + 2
	l.Sealed(ledger.Batch{Row: pkgledger.BatchRow{B: next, FirstSeq: 1000, N: 1}, Records: []pkgledger.Record{rec(1000)}})
	l.Flush()
	after := l.Status()
	l.Close()
	if !strings.Contains(after.Stopped, "aren't in the ledger") || after.Batch != before.Batch {
		t.Errorf("after a batch past a hole the loop is %+v, want it stopped at batch %d over the hole", after, before.Batch)
	}
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
