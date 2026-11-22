// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/internal/agent/hookpath"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/agent/tailers"
	"github.com/some-du6e/pairtime/internal/install"
	"github.com/some-du6e/pairtime/pkg/event"
)

// inProcessLookback starts lookback's reader in the test's own process,
// over pipes, trimming with the Claude Code adapter as `pairtime lookback`
// does, but lowering no priority. started counts the readers it started.
func inProcessLookback(started *atomic.Int32) func() (tailers.Child, error) {
	return func() (tailers.Child, error) {
		started.Add(1)
		ad, ok := adapters.Default.New(event.HarnessClaudeCode, adapters.Options{Keys: adapters.NewKeys(make([]byte, hookpath.SessionSecretLen))})
		if !ok {
			return tailers.Child{}, errors.New("no Claude Code adapter")
		}
		lr := ad.(adapters.LogReader)
		trim := func(_ event.Harness, line []byte) ([]byte, error) { return lr.TrimLog(line) }
		inR, inW := io.Pipe()
		outR, outW := io.Pipe()
		done := make(chan error, 1)
		go func() {
			err := tailers.ServeLookback(inR, outW, trim)
			outW.CloseWithError(err)
			inR.Close()
			done <- err
		}()
		var once sync.Once
		var werr error
		return tailers.Child{In: inW, Out: outR,
			Wait: func() error { once.Do(func() { werr = <-done }); return werr },
			Kill: func() { outR.CloseWithError(errors.New("killed")); inW.Close() }}, nil
	}
}

// TestLookbackReachesTheLedger checks lookback end to end (WP-AD3): the
// transcripts in the projects folder when the agent first lists it go
// through lookback's reader, and the records of what they say happened in
// LookbackWindow before then, and before the ledger began, reach the
// ledger as the tailers' own lines would.
func TestLookbackReachesTheLedger(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the capture's paths are Linux's")
	}
	raw, err := os.ReadFile(captureTranscript)
	if err != nil {
		t.Fatal(err)
	}
	r := newHookReplay(t)
	// The capture's folder becomes the replay's, and its day yesterday, so
	// it lies inside LookbackWindow whenever the test runs.
	raw = bytes.ReplaceAll(raw, []byte(captureHome), []byte(r.home))
	now := time.Now().UTC()
	raw = bytes.ReplaceAll(raw, []byte("2026-11-20T"), []byte(now.Add(-24*time.Hour).Format("2006-01-02")+"T"))
	home := filepath.Join(filepath.Dir(r.home), "teen")
	dir := filepath.Join(home, ".claude", "projects", "-cli")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, captureSession+".jsonl"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	// A prompt stamped after the ledger began, as in a session a hook
	// reported since an agent without lookback started it, and one older
	// than LookbackWindow.
	prompt := func(session string, at time.Time) []byte {
		b, _ := json.Marshal(map[string]any{"type": "user", "sessionId": session, "promptId": "ffffffff-0000-4000-8000-000000000001",
			"timestamp": at.Format(time.RFC3339Nano), "cwd": r.folder(), "version": "2.1.327", "entrypoint": "cli", "isSidechain": false,
			"promptSource": "typed", "turnOrigin": "human", "message": map[string]any{"role": "user", "content": "What does this do?"}})
		return append(b, '\n')
	}
	const later, older = "11111111-0000-4000-8000-000000000001", "22222222-0000-4000-8000-000000000002"
	for s, at := range map[string]time.Time{later: now.Add(time.Hour), older: now.Add(-tailers.LookbackWindow - 24*time.Hour)} {
		if err := os.WriteFile(filepath.Join(dir, s+".jsonl"), prompt(s, at), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var started atomic.Int32
	opts := r.options([]keys.Store{})
	opts.Env = &install.Env{GOOS: runtime.GOOS, Home: home}
	opts.Lookback = inProcessLookback(&started)
	a := startAgent(t, opts)
	st := waitStatusFor(t, a, 60*time.Second, func(s statusReply) bool {
		lb := s.Adapters.Lookback
		return lb != nil && lb.Files == 3 && lb.FilesDone == 3 && !lb.Running
	})
	a.cancel()
	if err := a.waitExit(t); err != nil {
		t.Fatalf("stopping the agent: %v", err)
	}
	recs := readLedger(t, r.dirs, event.SourceLog)
	_, kSession := deviceKeys(t, r.dirs, []keys.Store{})
	k := adapters.NewKeys(kSession)

	var want []string
	for _, l := range bytes.SplitAfter(raw, []byte("\n")) {
		var e struct {
			Type         string  `json:"type"`
			PromptID     string  `json:"promptId"`
			TurnOrigin   *string `json:"turnOrigin"`
			PromptSource *string `json:"promptSource"`
			IsSidechain  bool    `json:"isSidechain"`
		}
		if json.Unmarshal(l, &e) == nil && e.Type == "user" && !e.IsSidechain && (e.TurnOrigin != nil || e.PromptSource != nil) &&
			(e.PromptSource == nil || *e.PromptSource != "queued") {
			want = append(want, k.Turn(captureSession, e.PromptID))
		}
	}
	bySession := func(s string) []*event.PromptSubmit {
		var out []*event.PromptSubmit
		for _, rec := range recs {
			if p, ok := rec.ev.Data.(*event.PromptSubmit); ok && rec.ev.SessionKey == k.Session(s) {
				out = append(out, p)
			}
		}
		return out
	}
	lb := st.Adapters.Lookback
	cases := []struct {
		name string
		ok   bool
	}{
		{"each prompt of the capture's transcript reaches the ledger once, keyed as its hook would be", func() bool {
			var got []string
			for _, p := range bySession(captureSession) {
				got = append(got, p.TurnKey)
			}
			return len(want) == 18 && slices.Equal(got, want)
		}()},
		{"a prompt from after the ledger began is left to the session's hooks", len(bySession(later)) == 0},
		{"a prompt from before LookbackWindow is left out", len(bySession(older)) == 0},
		{"every record is via log, and no two share an event_id", func() bool {
			seen := map[string]bool{}
			for _, rec := range recs {
				if rec.ev.Via != event.ViaLog || seen[rec.ev.EventID] {
					return false
				}
				seen[rec.ev.EventID] = true
			}
			return len(recs) > 0
		}()},
		{"one reader read all three files, and the status says so", started.Load() == 1 && lb.Bytes == lb.BytesDone && lb.Failures == 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Errorf("lookback %+v from %d readers, %d log records", lb, started.Load(), len(recs))
			}
		})
	}
}
