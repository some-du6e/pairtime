// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/internal/adapters/git"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/agent/state"
	"github.com/some-du6e/pairtime/internal/agent/tailers"
	"github.com/some-du6e/pairtime/internal/install"
	"github.com/some-du6e/pairtime/pkg/event"
)

// captureTranscript is the capture's main session's transcript, which no
// hook of this test reports.
var captureTranscript = filepath.Join(captureRun, "transcripts", "home", "pairtime-capture", "claude-home", "projects",
	"-home-capture-pairtime-capture-projects-cli", captureSession+".jsonl")

// TestTranscriptLinesReachTheLedger checks the tailers end to end (WP-AD3):
// a transcript Claude Code writes while the agent runs reaches the ledger
// as records with source and via log, keyed under this installation's
// K_session as its hooks would be, a session.start for a session no hook
// reported among them, a line that can't be read raises the parse-failure
// count without holding up the lines after it, and a restart reads none of
// it again.
func TestTranscriptLinesReachTheLedger(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the capture's paths are Linux's")
	}
	raw, err := os.ReadFile(captureTranscript)
	if err != nil {
		t.Fatal(err)
	}
	r := newHookReplay(t)
	// The capture's paths, the project's folder among them, become the
	// replay's, so the git adapter finds the folder on this machine.
	raw = bytes.ReplaceAll(raw, []byte(captureHome), []byte(r.home))
	lines := bytes.SplitAfter(raw, []byte("\n"))
	if len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	home := filepath.Join(filepath.Dir(r.home), "teen")
	file := filepath.Join(home, ".claude", "projects", "-cli", captureSession+".jsonl")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	// There before the agent's first look, empty. The projects folder
	// counts as listed already, as after a run that found it empty, so
	// whenever that first look comes, which can be after the first lines
	// are written, the file is a new one, read from its start: a root's
	// first listing would take its files up at their end.
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(r.dirs.Data, 0o700); err != nil {
		t.Fatal(err)
	}
	store, _, err := state.Open(r.dirs.StateFile())
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]any{"v": 1, "files": []any{}, "roots": []string{filepath.Join(home, ".claude", "projects")}}
	if err := store.Put("tailers", listed); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	opts := r.options([]keys.Store{})
	opts.Env = &install.Env{GOOS: runtime.GOOS, Home: home}
	a := startAgent(t, opts)
	half := len(lines) / 2
	// Between the halves, a line that isn't JSON and one longer than the
	// tailers read: each is a parse failure, and the lines after them
	// still reach the ledger.
	bad := [][]byte{[]byte("this line isn't JSON\n"), append(bytes.Repeat([]byte("x"), tailers.MaxLine+10), '\n')}
	for _, chunk := range [][][]byte{lines[:half], bad, lines[half:]} {
		f, err := os.OpenFile(file, os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(bytes.Join(chunk, nil)); err != nil {
			t.Fatal(err)
		}
		f.Close()
		time.Sleep(200 * time.Millisecond)
	}
	st := waitStatusFor(t, a, 40*time.Second, func(s statusReply) bool {
		return s.Adapters.Harnesses[event.HarnessClaudeCode].Lines >= uint64(len(lines)+len(bad))
	})
	a.cancel()
	if err := a.waitExit(t); err != nil {
		t.Fatalf("stopping the agent: %v", err)
	}
	counts := st.Adapters.Harnesses[event.HarnessClaudeCode]
	recs := readLedger(t, r.dirs, event.SourceLog)
	_, kSession := deviceKeys(t, r.dirs, []keys.Store{})
	k := adapters.NewKeys(kSession)

	var promptIDs []string
	for _, l := range lines {
		var e struct {
			Type         string  `json:"type"`
			PromptID     string  `json:"promptId"`
			TurnOrigin   *string `json:"turnOrigin"`
			PromptSource *string `json:"promptSource"`
			IsSidechain  bool    `json:"isSidechain"`
		}
		if json.Unmarshal(l, &e) == nil && e.Type == "user" && !e.IsSidechain && (e.TurnOrigin != nil || e.PromptSource != nil) &&
			(e.PromptSource == nil || *e.PromptSource != "queued") {
			promptIDs = append(promptIDs, e.PromptID)
		}
	}
	cases := []struct {
		name string
		ok   func() bool
	}{
		{"every line was read, and the two that can't be were the parse failures", func() bool {
			return counts.Lines == uint64(len(lines)+len(bad)) && counts.LineFailures == uint64(len(bad))
		}},
		{"every record has source and via log, and the session's key under this installation's K_session", func() bool {
			for _, rec := range recs {
				if rec.ev.Via != event.ViaLog || rec.ev.Harness != event.HarnessClaudeCode || rec.ev.SessionKey != k.Session(captureSession) {
					return false
				}
			}
			return len(recs) > 0
		}},
		{"each prompt entry is a prompt.submit keyed by its promptId, as its hook would be", func() bool {
			var got []string
			for _, rec := range recs {
				if p, ok := rec.ev.Data.(*event.PromptSubmit); ok {
					got = append(got, p.TurnKey)
				}
			}
			var want []string
			for _, id := range promptIDs {
				want = append(want, k.Turn(captureSession, id))
			}
			return len(want) == 18 && slices.Equal(got, want)
		}},
		{"a session no hook reported gets one session.start, with no TTY and no parent class", func() bool {
			n := 0
			for _, rec := range recs {
				if s, ok := rec.ev.Data.(*event.SessionStart); ok {
					n++
					if s.HasTTY || s.ParentClass != event.ParentUnknown || s.Entrypoint != event.EntrypointUnknown {
						return false
					}
				}
			}
			return n == 1
		}},
		{"step 6's refused rm is one agent.interrupt", func() bool {
			n := 0
			for _, rec := range recs {
				if _, ok := rec.ev.Data.(*event.AgentInterrupt); ok {
					n++
				}
			}
			return n == 1
		}},
		{"no two records share an event_id", func() bool {
			seen := map[string]bool{}
			for _, rec := range recs {
				if seen[rec.ev.EventID] {
					return false
				}
				seen[rec.ev.EventID] = true
			}
			return true
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok() {
				t.Errorf("counts %+v, %d log records", counts, len(recs))
			}
		})
	}

	t.Run("a restarted agent reads only what was written since", func(t *testing.T) {
		a := startAgent(t, opts)
		more, _ := json.Marshal(map[string]any{"type": "user", "sessionId": captureSession, "promptId": "ffffffff-0000-4000-8000-000000000001",
			"timestamp": "2026-11-20T17:20:00.000Z", "cwd": r.folder(), "version": "2.1.327", "entrypoint": "cli", "isSidechain": false,
			"promptSource": "typed", "turnOrigin": "human", "message": map[string]any{"role": "user", "content": "One more question, please."}})
		f, err := os.OpenFile(file, os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		f.Write(append(more, '\n'))
		f.Close()
		st := waitStatusFor(t, a, 40*time.Second, func(s statusReply) bool {
			return s.Adapters.Harnesses[event.HarnessClaudeCode].Lines >= 1
		})
		a.cancel()
		if err := a.waitExit(t); err != nil {
			t.Fatalf("stopping the agent: %v", err)
		}
		again := readLedger(t, r.dirs, event.SourceLog)
		if n := st.Adapters.Harnesses[event.HarnessClaudeCode].Lines; n != 1 || len(again) != len(recs)+1 {
			t.Errorf("the restarted agent read %d lines, and the ledger has %d log records, %d before it", n, len(again), len(recs))
		}
		if p, ok := again[len(again)-1].ev.Data.(*event.PromptSubmit); !ok || p.TurnKey != k.Turn(captureSession, "ffffffff-0000-4000-8000-000000000001") {
			t.Errorf("the new record is %+v, want the new line's prompt", again[len(again)-1].ev)
		}
	})
}

// waitStatusFor is waitStatus with a deadline of d.
func waitStatusFor(t *testing.T, r *running, d time.Duration, ok func(statusReply) bool) statusReply {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		s := getStatus(t, r)
		if ok(s) {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("the status never got there in %s; last: %+v", d, s.Adapters)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// listedProjects makes the home folder of a replay's teen with Claude
// Code's projects folder counted as listed already, as after a run that
// found it empty, and returns it with the projects folder: whenever the
// agent's first look comes, a transcript written there is a new file, read
// from its start, where a root's first listing would take it up at its
// end.
func listedProjects(t *testing.T, r *hookReplay) (home, projects string) {
	t.Helper()
	home = filepath.Join(filepath.Dir(r.home), "teen")
	projects = filepath.Join(home, ".claude", "projects")
	if err := os.MkdirAll(projects, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(r.dirs.Data, 0o700); err != nil {
		t.Fatal(err)
	}
	store, _, err := state.Open(r.dirs.StateFile())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put("tailers", map[string]any{"v": 1, "files": []any{}, "roots": []string{projects}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	return home, projects
}

// TestTranscriptOfAnIgnoredFolder checks that the transcript of a session in
// a folder a .pairtime-ignore keeps out puts nothing in the ledger (DESIGN
// 8.8): not its lines that name the folder, nor those that name none, as a
// queued message's, nor what its idle markers resolve, as the session.start
// of a session no hook reports and the turn's final answer. A session in a
// folder that counts, read beside it, reaches the ledger.
func TestTranscriptOfAnIgnoredFolder(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the replay's folders are Linux paths")
	}
	r := newHookReplay(t)
	if err := os.WriteFile(filepath.Join(r.folder(), git.IgnoreFile), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(r.home, "projects", "kept")
	if err := os.MkdirAll(kept, 0o700); err != nil {
		t.Fatal(err)
	}
	home, projects := listedProjects(t, r)
	entry := func(m map[string]any) []byte {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return append(b, '\n')
	}
	prompt := func(sid, cwd string, at time.Time) []byte {
		return entry(map[string]any{"type": "user", "sessionId": sid, "promptId": "ffffffff-0000-4000-8000-000000000001",
			"timestamp": at.Format(time.RFC3339Nano), "cwd": cwd, "version": "2.1.327", "entrypoint": "cli", "isSidechain": false,
			"promptSource": "typed", "turnOrigin": "human", "message": map[string]any{"role": "user", "content": "What does this do?"}})
	}
	answer := func(sid, cwd string, at time.Time) []byte {
		return entry(map[string]any{"type": "assistant", "sessionId": sid, "timestamp": at.Format(time.RFC3339Nano), "cwd": cwd,
			"version": "2.1.327", "entrypoint": "cli", "isSidechain": false,
			"message": map[string]any{"id": "msg_1", "role": "assistant", "stop_reason": "end_turn",
				"content": []any{map[string]any{"type": "text", "text": "It prints the answer."}}}})
	}
	const out, in = "11111111-0000-4000-8000-00000000000a", "22222222-0000-4000-8000-00000000000b"
	// Each turn's final answer waits for the file's first idle marker, and
	// each session.start for one at least startGrace after its first entry:
	// the ignored session's comes at that first marker, the kept one's at
	// the next. So by the time the kept session's start is in the ledger,
	// the ignored file's markers have been taken.
	now := time.Now().UTC()
	ignored := slices.Concat(prompt(out, r.folder(), now.Add(-82*time.Second)),
		entry(map[string]any{"type": "queue-operation", "operation": "enqueue", "timestamp": now.Add(-81 * time.Second).Format(time.RFC3339Nano),
			"sessionId": out, "content": "and then the tests, please"}),
		answer(out, r.folder(), now.Add(-80*time.Second)))
	counted := slices.Concat(prompt(in, kept, now.Add(-70*time.Second)), answer(in, kept, now.Add(-69*time.Second)))
	for dir, f := range map[string][]byte{filepath.Join(projects, "-out", out+".jsonl"): ignored,
		filepath.Join(projects, "-kept", in+".jsonl"): counted} {
		if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dir, f, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	opts := r.options([]keys.Store{})
	opts.Env = &install.Env{GOOS: runtime.GOOS, Home: home}
	a := startAgent(t, opts)
	// The kept session's prompt, session.start, final answer and stop.
	st := waitStatusFor(t, a, 90*time.Second, func(s statusReply) bool {
		return s.Adapters.Harnesses[event.HarnessClaudeCode].Records >= 4
	})
	a.cancel()
	if err := a.waitExit(t); err != nil {
		t.Fatalf("stopping the agent: %v", err)
	}
	_, kSession := deviceKeys(t, r.dirs, []keys.Store{})
	k := adapters.NewKeys(kSession)
	var leaked, kinds []event.Kind
	for _, rec := range readLedger(t, r.dirs, event.SourceLog) {
		switch rec.ev.SessionKey {
		case k.Session(out):
			leaked = append(leaked, rec.ev.Kind())
		case k.Session(in):
			kinds = append(kinds, rec.ev.Kind())
		}
	}
	slices.Sort(kinds)
	want := []event.Kind{event.KindAgentOutput, event.KindAgentStop, event.KindPromptSubmit, event.KindSessionStart}
	slices.Sort(want)
	counts := st.Adapters.Harnesses[event.HarnessClaudeCode]
	cases := []struct {
		name string
		ok   bool
	}{
		{"nothing of the ignored folder's session reaches the ledger, its queued message and what its idle markers resolved included",
			len(leaked) == 0},
		{"its three lines count as ignored", counts.Ignored == 3},
		{"the kept folder's session reaches the ledger: its prompt, its start, its final answer and its stop", slices.Equal(kinds, want)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Errorf("the ignored session left %v, the kept one %v; counts %+v", leaked, kinds, counts)
			}
		})
	}
}
