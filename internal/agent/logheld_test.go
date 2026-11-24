// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/hookpath"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/tailers"
	"github.com/some-du6e/pairtime/pkg/event"
	pkgledger "github.com/some-du6e/pairtime/pkg/ledger"
)

// TestReplayHeld checks that a log line a replay reads again makes no
// second record of what the ledger holds (D-late-local point 5): read
// again in the same run, as a Rewind or a relocate reads a line it had,
// or after a restart or a kill, as a crash re-reads the lines taken since
// state.json's last save, however long after the run before's last seal,
// its record keeps its event_id and the adapter leaves it out, counting
// it; another line of the file still makes its record; and a record sealed
// more than the window before the run before's last seal goes to the
// ledger again.
func TestReplayHeld(t *testing.T) {
	const file = "/home/teen/.claude/projects/-p/s.jsonl"
	// host is an adapter host with the Claude Code adapter, writing to w,
	// of an agent that started at started.
	host := func(dirs home.Dirs, w *ledger.Writer, started time.Time) *adapterHost {
		a := &agent{dirs: dirs, log: slog.New(slog.DiscardHandler), problems: map[string]string{}, hooked: newHookTies(), started: started}
		secret := bytes.Repeat([]byte{7}, hookpath.SessionSecretLen)
		a.kSession.Store(&secret)
		h := newAdapterHost(a)
		h.keys = &adapterKeys{}
		h.attach(w)
		return h
	}
	// line is a typed prompt of a transcript made at made, its prompt id
	// prompt, starting at off in the file. Taken less than startGrace after
	// made, as the first line of the session, it makes only its prompt's
	// record, as its session.start waits for a hook call; taken later, it
	// makes the session.start's record too, at once, of the same event_id in
	// every run (claudecode's resolveStart).
	line := func(t *testing.T, made time.Time, prompt string, off int64) tailers.Line {
		t.Helper()
		b, err := json.Marshal(map[string]any{"type": "user", "sessionId": "77777777-0000-4000-8000-000000000007",
			"promptId": prompt, "timestamp": made.UTC().Format(time.RFC3339Nano), "cwd": "/home/teen/p",
			"version": "2.1.327", "entrypoint": "cli", "isSidechain": false, "promptSource": "typed", "turnOrigin": "human",
			"message": map[string]any{"role": "user", "content": "What does this do?"}})
		if err != nil {
			t.Fatal(err)
		}
		return tailers.Line{Harness: event.HarnessClaudeCode, Bytes: b, Log: adapters.LogLine{File: file, Path: file, Offset: off, End: off + int64(len(b)) + 1}}
	}
	take := func(t *testing.T, h *adapterHost, l tailers.Line) {
		t.Helper()
		if !h.takeLine(tailers.New(tailers.Options{NoNotify: true}), l) {
			t.Fatal("takeLine didn't take the line")
		}
	}
	prompts := func(t *testing.T, dirs home.Dirs) map[string]int {
		t.Helper()
		got := map[string]int{}
		for _, rec := range readLedger(t, dirs, event.SourceLog) {
			if _, ok := rec.ev.Data.(*event.PromptSubmit); ok {
				got[rec.ev.EventID]++
			}
		}
		return got
	}
	// earlier is an earlier run's writer on dirs, its clock at what at
	// holds, in Unix ms.
	earlier := func(t *testing.T, dirs home.Dirs, at *atomic.Int64) *ledger.Writer {
		return optionsWriter(t, dirs, func(o *ledger.Options) { o.Now = func() time.Time { return time.UnixMilli(at.Load()) } })
	}
	const first, second = "ffffffff-0000-4000-8000-000000000071", "ffffffff-0000-4000-8000-000000000072"
	// earlierRun has an earlier run seal the line it returns, the prompt
	// first, made a second before aSealed, in a batch sealed at aSealed, and,
	// unless bSealed is zero, another line of the file, the prompt second,
	// in a batch sealed at bSealed, which is then that run's last seal. The
	// first line is hours old when read, so it makes two records, the
	// session's start and the prompt, in that run and when read again.
	earlierRun := func(t *testing.T, dirs home.Dirs, aSealed, bSealed time.Time) tailers.Line {
		t.Helper()
		var at atomic.Int64
		at.Store(aSealed.Add(-2 * time.Second).UnixMilli())
		w := earlier(t, dirs, &at)
		h := host(dirs, w, aSealed.Add(-2*time.Second))
		l := line(t, aSealed.Add(-time.Second), first, 0)
		at.Store(aSealed.UnixMilli())
		take(t, h, l)
		sealNow(t, w, dirs.Ledger())
		if !bSealed.IsZero() {
			at.Store(bSealed.UnixMilli())
			take(t, h, line(t, bSealed.Add(-time.Second), second, l.Log.End))
			sealNow(t, w, dirs.Ledger())
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return l
	}
	// last is when the earlier run's last seal came, in the cases that seal
	// two batches.
	last := time.Now().Add(-time.Minute)
	cases := []struct {
		name string
		// run takes lines and returns the dirs whose ledger to read, the
		// host whose counts to read, how many prompt records the ledger
		// should hold, of how many event_ids, and how many repeats the host
		// counted.
		run func(t *testing.T, dirs home.Dirs) (home.Dirs, *adapterHost, int, int, uint64)
	}{
		{"a line read again in the same run, as a Rewind or a relocate reads one it had, makes no second record",
			func(t *testing.T, dirs home.Dirs) (home.Dirs, *adapterHost, int, int, uint64) {
				w := optionsWriter(t, dirs, nil)
				h := host(dirs, w, time.Now())
				l := line(t, time.Now().Add(-time.Minute), first, 0)
				take(t, h, l)
				take(t, h, l)
				sealNow(t, w, dirs.Ledger())
				return dirs, h, 1, 1, 1
			}},
		{"another line of the file makes its own record",
			func(t *testing.T, dirs home.Dirs) (home.Dirs, *adapterHost, int, int, uint64) {
				w := optionsWriter(t, dirs, nil)
				h := host(dirs, w, time.Now())
				l := line(t, time.Now().Add(-time.Minute), first, 0)
				take(t, h, l)
				take(t, h, line(t, time.Now().Add(-30*time.Second), second, l.Log.End))
				sealNow(t, w, dirs.Ledger())
				return dirs, h, 2, 2, 0
			}},
		{"a line read again after a restart, as a crash re-reads the lines taken since state.json's last save, makes no second record",
			func(t *testing.T, dirs home.Dirs) (home.Dirs, *adapterHost, int, int, uint64) {
				w := optionsWriter(t, dirs, nil)
				l := line(t, time.Now().Add(-time.Minute), first, 0)
				take(t, host(dirs, w, time.Now()), l)
				if err := w.Close(); err != nil {
					t.Fatal(err)
				}
				w = optionsWriter(t, dirs, nil)
				h := host(dirs, w, time.Now())
				take(t, h, l)
				sealNow(t, w, dirs.Ledger())
				return dirs, h, 1, 1, 1
			}},
		{"so do a line a kill left unsealed, which the next Open seals, and one in the last batch the run before sealed, 72 hours before",
			func(t *testing.T, dirs home.Dirs) (home.Dirs, *adapterHost, int, int, uint64) {
				// The earlier run sealed the line 72 hours back, then took
				// another line just before a kill. Its seals wait at their read
				// of config.toml once armed, so the seal the second line's
				// prompt asks for doesn't come before the copy, as a kill leaves
				// the ledger.
				var at atomic.Int64
				var armed atomic.Bool
				gate := make(chan struct{})
				release := sync.OnceFunc(func() { close(gate) })
				aSealed := time.Now().Add(-72 * time.Hour)
				at.Store(aSealed.Add(-2 * time.Second).UnixMilli())
				w := optionsWriter(t, dirs, func(o *ledger.Options) {
					o.Now = func() time.Time { return time.UnixMilli(at.Load()) }
					o.Online = func() bool {
						if armed.Load() {
							<-gate
						}
						return true
					}
				})
				t.Cleanup(release) // before the writer's Close, which waits for the seal
				h := host(dirs, w, aSealed.Add(-2*time.Second))
				l := line(t, aSealed.Add(-time.Second), first, 0)
				at.Store(aSealed.UnixMilli())
				take(t, h, l)
				sealNow(t, w, dirs.Ledger())
				at.Store(time.Now().UnixMilli())
				armed.Store(true)
				l2 := line(t, time.Now().Add(-time.Minute), second, l.Log.End)
				take(t, h, l2)
				dir := t.TempDir()
				killed := home.Dirs{Data: dir, Config: dir, Runtime: dir}
				copyFiles(t, dirs.Ledger(), killed.Ledger())
				release()
				if _, rows, err := ledger.ReadRows(killed.Ledger(), 0); err != nil || len(rows) != 1 {
					t.Fatalf("the copy holds %d rows (%v); want the one sealed 72 hours back, with the record a kill left unsealed", len(rows), err)
				}
				// The next run takes its start before it opens the ledger, as
				// Run does, and Open seals what the kill left after it.
				started := time.Now()
				w2 := optionsWriter(t, killed, nil)
				h2 := host(killed, w2, started)
				take(t, h2, l)
				take(t, h2, l2)
				sealNow(t, w2, killed.Ledger())
				// The first line, hours old, makes the session's start as well
				// as its prompt, and the ledger holds all three.
				return killed, h2, 2, 2, 3
			}},
		{"a line whose record the ledger dropped as made in a local-only stretch, read again in the same run, is skipped, as neither a repeat nor a second drop",
			func(t *testing.T, dirs home.Dirs) (home.Dirs, *adapterHost, int, int, uint64) {
				// A stretch from the install to 60 s in, and a batch sealed
				// online after it, which passes it.
				t0 := time.Now().Add(-10 * time.Minute)
				var at atomic.Int64
				var local atomic.Bool
				at.Store(t0.UnixMilli())
				local.Store(true)
				w := optionsWriter(t, dirs, func(o *ledger.Options) {
					o.Now = func() time.Time { return time.UnixMilli(at.Load()) }
					o.Online = func() bool { return !local.Load() }
				})
				for _, step := range []struct {
					ms    int64
					local bool
				}{{2000, true}, {60_000, false}, {120_000, false}} {
					local.Store(step.local)
					at.Store(t0.UnixMilli() + step.ms)
					if _, err := w.Write(presenceAt(t0.Add(time.Duration(step.ms-1000) * time.Millisecond))); err != nil {
						t.Fatal(err)
					}
					sealNow(t, w, dirs.Ledger())
				}
				// The line is minutes old, so it makes the session's start
				// as well as the prompt, both made in the stretch.
				h := host(dirs, w, time.Now())
				l := line(t, t0.Add(30*time.Second), first, 0)
				for _, read := range []string{"read", "read again"} {
					take(t, h, l)
					if n := h.statsOf(event.HarnessClaudeCode, true).LateLocal; n != 2 {
						t.Errorf("with the line %s, the adapter counted %d records dropped as made in local-only mode, want 2", read, n)
					}
				}
				return dirs, h, 0, 0, 0
			}},
		{"a line read again 72 hours after the run before's last seal, as after a weekend off, makes no second record",
			func(t *testing.T, dirs home.Dirs) (home.Dirs, *adapterHost, int, int, uint64) {
				l := earlierRun(t, dirs, time.Now().Add(-72*time.Hour), time.Time{})
				w := optionsWriter(t, dirs, nil)
				h := host(dirs, w, time.Now())
				take(t, h, l)
				sealNow(t, w, dirs.Ledger())
				return dirs, h, 1, 1, 2
			}},
		{"a record sealed 47 hours before the run before's last seal is held, so its line read again makes no second record",
			func(t *testing.T, dirs home.Dirs) (home.Dirs, *adapterHost, int, int, uint64) {
				l := earlierRun(t, dirs, last.Add(-47*time.Hour), last)
				w := optionsWriter(t, dirs, nil)
				h := host(dirs, w, time.Now())
				take(t, h, l)
				sealNow(t, w, dirs.Ledger())
				return dirs, h, 2, 2, 2
			}},
		{"one sealed 49 hours before it goes to the ledger again, where the scorer drops it for the one it holds",
			func(t *testing.T, dirs home.Dirs) (home.Dirs, *adapterHost, int, int, uint64) {
				l := earlierRun(t, dirs, last.Add(-49*time.Hour), last)
				w := optionsWriter(t, dirs, nil)
				h := host(dirs, w, time.Now())
				take(t, h, l)
				sealNow(t, w, dirs.Ledger())
				return dirs, h, 3, 2, 0
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			dirs, h, records, ids, repeats := c.run(t, home.Dirs{Data: dir, Config: dir, Runtime: dir})
			got := prompts(t, dirs)
			n := 0
			for _, k := range got {
				n += k
			}
			if n != records || len(got) != ids {
				t.Errorf("the ledger holds %d prompt records of %d event_ids, want %d of %d", n, len(got), records, ids)
			}
			if r := h.statsOf(event.HarnessClaudeCode, true).Repeats; r != repeats {
				t.Errorf("the adapter counted %d repeats, want %d", r, repeats)
			}
		})
	}
}

// TestHeldLogsFrom checks the batches the agent reads what the ledger holds
// from (heldLogsFrom): every batch sealed from heldLogsWindow before the
// anchor, the newest batch an earlier run sealed, by its number, on, and
// those after it, as Open's of what a kill left; no more than that however
// the clocks read, as a start clock behind and a run before whose clock was
// reset to an old year can't widen it; and at most heldLogsMaxBatches up to
// the anchor.
func TestHeldLogsFrom(t *testing.T) {
	t0 := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	// rows is batches 1, 2, ... sealed at t0 plus each of at.
	rows := func(at ...time.Duration) []ledger.Row {
		var out []ledger.Row
		for i, d := range at {
			out = append(out, ledger.Row{BatchRow: pkgledger.BatchRow{B: uint64(i + 1)}, SealedAt: t0.Add(d)})
		}
		return out
	}
	h, d := time.Hour, 24*time.Hour
	// old is an old year, as a run whose clock reset dates its seals.
	old := time.Date(2015, 1, 1, 0, 0, 0, 0, time.UTC).Sub(t0)
	// busy is more batches than heldLogsMaxBatches, a second apart, the last
	// a second before t0.
	var busy []time.Duration
	for i := range heldLogsMaxBatches + 10 {
		busy = append(busy, -time.Duration(heldLogsMaxBatches+10-i)*time.Second)
	}
	// swept is rows whose batches start at 5, as after a sweep.
	swept := func(at ...time.Duration) []ledger.Row {
		out := rows(at...)
		for i := range out {
			out[i].B += 4
		}
		return out
	}
	cases := []struct {
		name   string
		rows   []ledger.Row
		anchor uint64
		want   uint64
	}{
		{"the window counts back from the newest batch an earlier run sealed", rows(-100*h, -60*h, -50*h, -10*h), 4, 3},
		{"and takes in the batches sealed after it, as Open's of what a kill left", rows(-100*h, -10*h, time.Minute), 2, 2},
		{"a batch sealed after the anchor, more than the window later, moves it no later", rows(-80*h, -72*h, time.Second), 2, 1},
		{"a start long after the run before's last seal still reads the window before that seal", rows(-190*h, -180*h, -150*h), 3, 1},
		{"a batch at the window's edge is in it", rows(-58*h, -10*h), 2, 1},
		{"a start clock 30 days behind changes nothing, as the anchor goes by its number", rows(-35*d, -31*d, -29*d, -1*d, -h), 5, 4},
		{"a run before whose clock was reset to an old year stops the walk at the first batch sealed more than the window after its last", rows(-100*h, -50*h, -10*h, old, old+h), 5, 4},
		{"the walk reads at most heldLogsMaxBatches up to the anchor", rows(busy...), heldLogsMaxBatches + 10, 11},
		{"with no batch an earlier run sealed, every batch is this run's", rows(time.Minute, 2*time.Minute), 0, 1},
		{"so it is when a sweep dropped the anchor", swept(-h, time.Minute), 3, 5},
		{"a ledger with no batch gives none", nil, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := heldLogsFrom(c.rows, c.anchor); got != c.want {
				t.Errorf("heldLogsFrom = %d, want %d", got, c.want)
			}
		})
	}
}

// TestHeldLogsPrune checks that heldLogs forgets, at most every
// heldLogsPrune, the records this run wrote more than heldLogsWindow before
// now, keeps those within the window at the prune, and keeps what it read
// from the ledger, and the ids the ledger dropped, for the run's life.
func TestHeldLogsPrune(t *testing.T) {
	t0 := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	held := &heldLogs{at: map[[16]byte]int64{idKey("loaded"): heldForRun}, dropped: map[[16]byte]struct{}{}, pruned: t0}
	held.add("old", t0)
	held.drop("dropped", t0)
	// The first prune comes an hour on, with the old ones still in the
	// window.
	held.add("edge", t0.Add(heldLogsPrune))
	keptInWindow := held.has("old") && held.wasDropped("dropped")
	// The next comes heldLogsWindow after that, when they have left it but
	// the record written at the first prune hasn't.
	held.add("new", t0.Add(heldLogsPrune+heldLogsWindow))
	checks := []struct {
		name string
		ok   bool
	}{
		{"a prune keeps what is within the window", keptInWindow},
		{"a later prune forgets a record written before the window", !held.has("old")},
		{"but keeps one written at the window's edge", held.has("edge")},
		{"and one just written", held.has("new")},
		{"and what it read from the ledger, for the run's life", held.has("loaded")},
		{"and one the ledger dropped, for the run's life", held.wasDropped("dropped")},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Errorf("no: holds %d, dropped %d", len(held.at), len(held.dropped))
			}
		})
	}
}

// copyFiles copies the files in src to dst, as they are, a writer's open
// ones included: batches.seg first, then the segments, so a row the copy
// holds has its seal frame in the copy too.
func copyFiles(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dst, 0o700); err != nil {
		t.Fatal(err)
	}
	slices.SortStableFunc(entries, func(a, b os.DirEntry) int {
		switch {
		case a.Name() == "batches.seg" && b.Name() != "batches.seg":
			return -1
		case b.Name() == "batches.seg" && a.Name() != "batches.seg":
			return 1
		}
		return 0
	})
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
