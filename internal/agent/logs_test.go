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
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/agent/state"
	"github.com/some-du6e/pairtime/internal/install"
	"github.com/some-du6e/pairtime/internal/platform"
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
// reported among them, and a restart reads none of it again.
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
	home, file := listedHome(t, r)
	opts := r.options([]keys.Store{})
	opts.Env = &install.Env{GOOS: runtime.GOOS, Home: home}
	a := startAgent(t, opts)
	half := len(lines) / 2
	for _, chunk := range [][][]byte{lines[:half], lines[half:]} {
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
		return s.Adapters.Harnesses[event.HarnessClaudeCode].Lines >= uint64(len(lines))
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
		{"every line was read, and none was a parse failure", func() bool {
			return counts.Lines == uint64(len(lines)) && counts.LineFailures == 0
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

// listedHome makes the home folder of a replay's teen, with Claude Code's
// projects folder and an empty transcript of the capture's main session in
// it, and the projects folder counted as listed already, as after a run
// that found it empty: whenever the agent's first look comes, which can be
// after the first lines are written, the file is a new one, read from its
// start, where a root's first listing would take its files up at their
// end.
func listedHome(t *testing.T, r *hookReplay) (home, file string) {
	t.Helper()
	home = filepath.Join(filepath.Dir(r.home), "teen")
	file = filepath.Join(home, ".claude", "projects", "-cli", captureSession+".jsonl")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
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
	return home, file
}

// TestTranscriptTiedToItsProcess checks the procs tie (WP-AD3): a session
// no hook reports, whose transcript a real agent reads as it is written,
// is tied to the one claude that can be running it, and its session.start
// is then the process scan's, one record, with the parent and the TTY SM
// 2.2 rule 4 reads, which no transcript states. With two claudes that
// could be it, nothing is tied, and the transcript's session.start stands.
func TestTranscriptTiedToItsProcess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the capture's paths are Linux's")
	}
	now := time.Now().UTC()
	me := platform.UIDOwner(uint32(os.Geteuid()))
	claudeIn := func(pid, shell int) []platform.Proc {
		return []platform.Proc{
			{PID: shell, PPID: 100, Name: "bash", PGID: shell, TTY: platform.TTYAttached, Start: now.Add(-30 * time.Minute), Owner: me},
			{PID: pid, PPID: shell, Name: "claude", PGID: pid, TTY: platform.TTYAttached, Start: now.Add(-5 * time.Minute), Owner: me},
		}
	}
	desktop := []platform.Proc{
		{PID: 1, Name: "systemd", PGID: 1, TTY: platform.TTYNone, Start: now.Add(-time.Hour), Owner: "uid:0"},
		{PID: 100, PPID: 1, Name: "gnome-terminal-server", PGID: 100, TTY: platform.TTYNone, Start: now.Add(-40 * time.Minute), Owner: me},
	}
	cases := []struct {
		name  string
		table []platform.Proc
		// via and parent are the session.start the ledger gets.
		via    event.Via
		parent event.ParentClass
		tty    bool
	}{
		{"one claude: the scan's session.start, with the terminal as its parent and a TTY",
			append(slices.Clone(desktop), claudeIn(111, 110)...), event.ViaOS, event.ParentTerminalApp, true},
		{"two claudes: nothing is tied, and the transcript's session.start stands",
			append(append(slices.Clone(desktop), claudeIn(111, 110)...), claudeIn(121, 120)...), event.ViaLog, event.ParentUnknown, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newHookReplay(t)
			home, file := listedHome(t, r)
			opts := r.options([]keys.Store{})
			opts.Env = &install.Env{GOOS: runtime.GOOS, Home: home}
			opts.Procs = func() ([]platform.Proc, error) { return slices.Clone(tc.table), nil }
			a := startAgent(t, opts)
			// The first prompt states the token 85 s ago, so its
			// session.start waits out the 90 s hook grace; the second, just
			// written, is the fresh line the tie needs. The tailers' idle
			// marker after it lets the session.start go.
			line := func(prompt, text string, at time.Time) []byte {
				b, _ := json.Marshal(map[string]any{"type": "user", "sessionId": captureSession, "promptId": prompt,
					"timestamp": at.Format(time.RFC3339Nano), "cwd": r.folder(), "version": "2.1.327", "entrypoint": "cli",
					"isSidechain": false, "promptSource": "typed", "turnOrigin": "human",
					"message": map[string]any{"role": "user", "content": text}})
				return append(b, '\n')
			}
			body := append(line("ffffffff-0000-4000-8000-000000000001", "What does this do?", time.Now().Add(-85*time.Second)),
				line("ffffffff-0000-4000-8000-000000000002", "And this one?", time.Now())...)
			if err := os.WriteFile(file, body, 0o600); err != nil {
				t.Fatal(err)
			}
			// Two prompts and the session.start.
			waitStatusFor(t, a, 60*time.Second, func(s statusReply) bool {
				return s.Adapters.Harnesses[event.HarnessClaudeCode].Records >= 3
			})
			a.cancel()
			if err := a.waitExit(t); err != nil {
				t.Fatalf("stopping the agent: %v", err)
			}
			_, kSession := deviceKeys(t, r.dirs, []keys.Store{})
			key := adapters.NewKeys(kSession).Session(captureSession)
			var starts []ledgerRecord
			for _, src := range []event.Source{event.SourceLog, event.SourceOS} {
				for _, rec := range readLedger(t, r.dirs, src) {
					if _, ok := rec.ev.Data.(*event.SessionStart); ok && rec.ev.SessionKey == key {
						starts = append(starts, rec)
					}
				}
			}
			if len(starts) != 1 {
				t.Fatalf("%d session.starts for the session, want one", len(starts))
			}
			got := starts[0].ev
			s := got.Data.(*event.SessionStart)
			if got.Via != tc.via || s.ParentClass != tc.parent || s.HasTTY != tc.tty {
				t.Errorf("the session.start is via %s, %+v; want via %s, parent %s, TTY %v", got.Via, s, tc.via, tc.parent, tc.tty)
			}
		})
	}
}

// TestTiedStartOf checks the session.start of a session tied to its
// process: the walk's parent and TTY, the log's start kind and permission
// mode, and the entrypoint E-cc-transcript-labels gives, whose labels only
// ever withhold credit.
func TestTiedStartOf(t *testing.T) {
	walk := func(ep event.Entrypoint, parent event.ParentClass) event.SessionStart {
		return event.SessionStart{StartKind: event.StartStartup, Entrypoint: ep, ParentClass: parent, HasTTY: true}
	}
	log := func(ep event.Entrypoint) *event.SessionStart {
		return &event.SessionStart{StartKind: event.StartResume, Entrypoint: ep, ParentClass: event.ParentUnknown, PermissionMode: "plan"}
	}
	cases := []struct {
		name string
		walk event.SessionStart
		log  *event.SessionStart
		want event.Entrypoint
	}{
		{"the transcript's sdk stands under an IDE", walk(event.EntrypointIDE, event.ParentIDE), log(event.EntrypointSDK), event.EntrypointSDK},
		{"the transcript's sdk stands under a desktop app", walk(event.EntrypointDesktop, event.ParentUnknown), log(event.EntrypointSDK), event.EntrypointSDK},
		{"the transcript's cloud stands over a terminal", walk(event.EntrypointUnknown, event.ParentTerminalApp), log(event.EntrypointCloud), event.EntrypointCloud},
		{"a walk's exec stands over the transcript's unknown", walk(event.EntrypointExec, event.ParentScriptRuntime), log(event.EntrypointUnknown), event.EntrypointExec},
		{"a walk's sdk stands over the transcript's cloud", walk(event.EntrypointSDK, event.ParentTerminalApp), log(event.EntrypointCloud), event.EntrypointSDK},
		{"the walk's ide stands over the transcript's unknown", walk(event.EntrypointIDE, event.ParentIDE), log(event.EntrypointUnknown), event.EntrypointIDE},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tiedStartOf(tc.walk, tc.log)
			if got.Entrypoint != tc.want || got.ParentClass != tc.walk.ParentClass || !got.HasTTY ||
				got.StartKind != event.StartResume || got.PermissionMode != "plan" {
				t.Errorf("tiedStartOf = %+v; want entrypoint %s with the walk's parent and TTY and the log's kind and mode", got, tc.want)
			}
		})
	}
}
