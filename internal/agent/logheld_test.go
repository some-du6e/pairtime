// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/hookpath"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/tailers"
	"github.com/some-du6e/pairtime/pkg/event"
)

// TestReplayHeld checks that a log line a replay reads again makes no
// second record of what the ledger holds (D-late-local point 5): read
// again in the same run, as a Rewind or a relocate reads a line it had,
// or after a restart or a kill, as a crash re-reads the lines taken since
// state.json's last save, its record keeps its event_id and the adapter
// drops it, counting it; another line of the file still makes its record;
// and a record written before the window the agent reads the ledger back
// for goes to the ledger again.
func TestReplayHeld(t *testing.T) {
	const file = "/home/teen/.claude/projects/-p/s.jsonl"
	// host is an adapter host with the Claude Code adapter, writing to w.
	host := func(dirs home.Dirs, w *ledger.Writer) *adapterHost {
		a := &agent{dirs: dirs, log: slog.New(slog.DiscardHandler), problems: map[string]string{}, hooked: newHookTies()}
		secret := bytes.Repeat([]byte{7}, hookpath.SessionSecretLen)
		a.kSession.Store(&secret)
		h := newAdapterHost(a)
		h.keys = &adapterKeys{}
		h.attach(w)
		return h
	}
	// line is a typed prompt of a transcript made at made, its prompt id
	// prompt, starting at off in the file.
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
	const first, second = "ffffffff-0000-4000-8000-000000000071", "ffffffff-0000-4000-8000-000000000072"
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
				h := host(dirs, w)
				l := line(t, time.Now().Add(-time.Minute), first, 0)
				take(t, h, l)
				take(t, h, l)
				sealNow(t, w, dirs.Ledger())
				return dirs, h, 1, 1, 1
			}},
		{"another line of the file makes its own record",
			func(t *testing.T, dirs home.Dirs) (home.Dirs, *adapterHost, int, int, uint64) {
				w := optionsWriter(t, dirs, nil)
				h := host(dirs, w)
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
				take(t, host(dirs, w), l)
				if err := w.Close(); err != nil {
					t.Fatal(err)
				}
				w = optionsWriter(t, dirs, nil)
				h := host(dirs, w)
				take(t, h, l)
				sealNow(t, w, dirs.Ledger())
				return dirs, h, 1, 1, 1
			}},
		{"so does one a kill left unsealed, which the next Open seals",
			func(t *testing.T, dirs home.Dirs) (home.Dirs, *adapterHost, int, int, uint64) {
				w := optionsWriter(t, dirs, nil)
				l := line(t, time.Now().Add(-time.Minute), first, 0)
				take(t, host(dirs, w), l)
				// The ledger as a kill leaves it: the record written, and no seal.
				dir := t.TempDir()
				killed := home.Dirs{Data: dir, Config: dir, Runtime: dir}
				copyFiles(t, dirs.Ledger(), killed.Ledger())
				w2 := optionsWriter(t, killed, nil)
				h := host(killed, w2)
				take(t, h, l)
				sealNow(t, w2, killed.Ledger())
				return killed, h, 1, 1, 1
			}},
		{"a record sealed before the window the agent reads the ledger back for goes to the ledger again, where the scorer drops it for the one it holds",
			func(t *testing.T, dirs home.Dirs) (home.Dirs, *adapterHost, int, int, uint64) {
				// Its batch was sealed three days ago.
				w := optionsWriter(t, dirs, func(o *ledger.Options) { o.Now = func() time.Time { return time.Now().Add(-72 * time.Hour) } })
				l := line(t, time.Now().Add(-73*time.Hour), first, 0)
				take(t, host(dirs, w), l)
				if err := w.Close(); err != nil {
					t.Fatal(err)
				}
				w = optionsWriter(t, dirs, nil)
				h := host(dirs, w)
				take(t, h, l)
				sealNow(t, w, dirs.Ledger())
				return dirs, h, 2, 1, 0
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

// copyFiles copies the files in src to dst, as they are, a writer's open
// ones included.
func copyFiles(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dst, 0o700); err != nil {
		t.Fatal(err)
	}
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
