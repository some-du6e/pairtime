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
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/hookpath"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/tailers"
	"github.com/some-du6e/pairtime/internal/install"
	"github.com/some-du6e/pairtime/pkg/event"
	pkgledger "github.com/some-du6e/pairtime/pkg/ledger"
)

// inProcessLookback starts lookback's reader in the test's own process,
// over pipes, trimming with the Claude Code adapter as `pairtime lookback`
// does, but lowering no priority. started counts the readers it started.
func inProcessLookback(started *atomic.Int32) func() (tailers.Child, error) {
	open := make(chan struct{})
	close(open)
	return gatedLookback(started, open)
}

// gatedLookback is inProcessLookback with readers that read nothing until
// open is closed, as a reader the OS holds back at its low priority.
func gatedLookback(started *atomic.Int32, open <-chan struct{}) func() (tailers.Child, error) {
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
		killed := make(chan struct{})
		go func() {
			err := errors.New("killed")
			select {
			case <-open:
				err = tailers.ServeLookback(inR, outW, trim)
			case <-killed:
			}
			outW.CloseWithError(err)
			inR.Close()
			done <- err
		}()
		var once, kill sync.Once
		var werr error
		return tailers.Child{In: inW, Out: outR,
			Wait: func() error { once.Do(func() { werr = <-done }); return werr },
			Kill: func() {
				kill.Do(func() { close(killed) })
				outR.CloseWithError(errors.New("killed"))
				inW.Close()
			}}, nil
	}
}

// TestLookbackReachesTheLedger checks lookback end to end (WP-AD3): the
// transcripts in the projects folder when the agent first lists it go
// through lookback's reader, and the records of what they say happened in
// LookbackWindow before then, and before the ledger began, reach the
// ledger as the tailers' own lines would. A session whose first entry, the
// one that states its entrypoint, lies before LookbackWindow keeps its
// session.start, at its first record inside it, for SM 2.2 rules 4 and 5.
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
	// A session an SDK app started 31 days ago, whose first entry states
	// its entrypoint, and which it prompted again yesterday.
	const sdk = "33333333-0000-4000-8000-000000000003"
	sdkPrompt := func(id string, at time.Time) []byte {
		b, _ := json.Marshal(map[string]any{"type": "user", "sessionId": sdk, "promptId": id,
			"timestamp": at.Format(time.RFC3339Nano), "cwd": r.folder(), "version": "2.1.327", "entrypoint": "sdk-cli", "isSidechain": false,
			"promptSource": "sdk", "turnOrigin": "sdk", "message": map[string]any{"role": "user", "content": "Summarize the logs."}})
		return append(b, '\n')
	}
	yesterday := now.Add(-24 * time.Hour)
	sdkFile := slices.Concat(sdkPrompt("ffffffff-0000-4000-8000-000000000011", now.Add(-tailers.LookbackWindow-24*time.Hour)),
		sdkPrompt("ffffffff-0000-4000-8000-000000000012", yesterday))
	if err := os.WriteFile(filepath.Join(dir, sdk+".jsonl"), sdkFile, 0o600); err != nil {
		t.Fatal(err)
	}

	var started atomic.Int32
	opts := r.options([]keys.Store{})
	opts.Env = &install.Env{GOOS: runtime.GOOS, Home: home}
	opts.Lookback = inProcessLookback(&started)
	a := startAgent(t, opts)
	st := waitStatusFor(t, a, 60*time.Second, func(s statusReply) bool {
		lb := s.Adapters.Lookback
		return lb != nil && lb.Files == 4 && lb.FilesDone == 4 && !lb.Running
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
	// kinds lists session s's records in ledger order: s for a
	// session.start with its entrypoint, p for a prompt.submit, each with
	// its time.
	kinds := func(s string) (out []string, at []int64) {
		for _, rec := range recs {
			if rec.ev.SessionKey != k.Session(s) {
				continue
			}
			switch d := rec.ev.Data.(type) {
			case *event.SessionStart:
				out = append(out, "s "+string(d.Entrypoint))
			case *event.PromptSubmit:
				out = append(out, "p")
			default:
				continue
			}
			at = append(at, rec.ev.T)
		}
		return out, at
	}
	sdkKinds, sdkAt := kinds(sdk)
	olderKinds, _ := kinds(older)
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
		{"a session wholly before LookbackWindow gets no session.start either", len(olderKinds) == 0},
		{"a session whose first entry lies before LookbackWindow keeps its session.start, at its first record inside it",
			slices.Equal(sdkKinds, []string{"s " + string(event.EntrypointSDK), "p"}) && sdkAt[0] == sdkAt[1] &&
				sdkAt[1] == yesterday.UnixMilli()},
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
		{"one reader read all four files, and the status says so", started.Load() == 1 && lb.Bytes == lb.BytesDone && lb.Failures == 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Errorf("lookback %+v from %d readers, %d log records; the SDK session's %v at %v", lb, started.Load(), len(recs),
					sdkKinds, sdkAt)
			}
		})
	}

	t.Run("a restarted agent's status shows what lookback read before, with no reader of its own", func(t *testing.T) {
		a := startAgent(t, opts)
		st := waitStatusFor(t, a, 15*time.Second, func(s statusReply) bool {
			lb := s.Adapters.Lookback
			return lb != nil && lb.Files == 4 && lb.FilesDone == 4
		})
		a.cancel()
		if err := a.waitExit(t); err != nil {
			t.Fatalf("stopping the agent: %v", err)
		}
		if lb := st.Adapters.Lookback; lb.Running || lb.Bytes != lb.BytesDone || started.Load() != 1 {
			t.Errorf("lookback %+v from %d readers, want the first run's, read through, with no reader since", lb, started.Load())
		}
	})
}

// TestLookbackAfterLoginSealsOnline checks a history lookback's reader
// reads after pairtime login switched config.toml online, in a real agent
// that started local, as every install does (#182): its records are dated
// before the ledger began, while config.toml said local, but the writer
// judges a log reader's record dated before the chain began by when it
// took it in, so its batch is sealed online.
func TestLookbackAfterLoginSealsOnline(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the replay's folders are Linux paths")
	}
	r := newHookReplay(t)
	teen := filepath.Join(filepath.Dir(r.home), "teen")
	dir := filepath.Join(teen, ".claude", "projects", "-cli")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	const sid = "44444444-0000-4000-8000-000000000004"
	b, _ := json.Marshal(map[string]any{"type": "user", "sessionId": sid, "promptId": "ffffffff-0000-4000-8000-000000000041",
		"timestamp": time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339Nano), "cwd": r.folder(), "version": "2.1.327",
		"entrypoint": "cli", "isSidechain": false, "promptSource": "typed", "turnOrigin": "human",
		"message": map[string]any{"role": "user", "content": "What does this do?"}})
	if err := os.WriteFile(filepath.Join(dir, sid+".jsonl"), append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	var started atomic.Int32
	open := make(chan struct{})
	opts := r.options([]keys.Store{})
	opts.Env = &install.Env{GOOS: runtime.GOOS, Home: teen}
	opts.Lookback = gatedLookback(&started, open)
	// The agent writes the default config.toml, which says mode = "local".
	a := startAgent(t, opts)
	waitStatusFor(t, a, 30*time.Second, func(s statusReply) bool { return started.Load() == 1 })
	// pairtime login switches config.toml online, and the reader reads on
	// once the config watch has noted it, past its debounce.
	if err := home.WriteFile(r.dirs.ConfigFile(), []byte("mode = \"online\"\n")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	close(open)
	waitStatusFor(t, a, 60*time.Second, func(s statusReply) bool {
		lb := s.Adapters.Lookback
		return lb != nil && lb.FilesDone == 1 && !lb.Running
	})
	a.cancel()
	if err := a.waitExit(t); err != nil {
		t.Fatalf("stopping the agent: %v", err)
	}
	_, secret := deviceKeys(t, r.dirs, []keys.Store{})
	key := adapters.NewKeys(secret).Session(sid)
	found, online := false, false
	if err := ledger.ReadBatches(r.dirs.Ledger(), 1, func(batch ledger.Batch) error {
		for _, rec := range batch.Records {
			body, err := pkgledger.ParseBody(rec.Body)
			if err != nil {
				return err
			}
			if ev, err := body.Event(); err == nil && ev.Kind() == event.KindPromptSubmit && ev.SessionKey == key {
				found, online = true, batch.Online
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Run("the history's prompt, read after the login, is in a batch sealed online", func(t *testing.T) {
		if !found || !online {
			t.Errorf("the prompt is in the ledger %v, in a batch sealed online %v", found, online)
		}
	})
}
