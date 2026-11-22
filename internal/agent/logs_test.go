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
	home := filepath.Join(filepath.Dir(r.home), "teen")
	file := filepath.Join(home, ".claude", "projects", "-cli", captureSession+".jsonl")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	// There before the agent's first look, empty: it reads it from its
	// start as it grows.
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
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
