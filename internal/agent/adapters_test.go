// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/internal/adapters/git"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/hookpath"
	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/internal/agent/ipc"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/clock"
	"github.com/some-du6e/pairtime/pkg/event"
	pkgledger "github.com/some-du6e/pairtime/pkg/ledger"
	"github.com/some-du6e/pairtime/pkg/ledger/features"
)

// WP-AD1's Claude Code 2.1.327 capture, and the records #115's
// conformance runner holds the Claude Code adapter to for it.
var (
	captureRun  = filepath.Join("..", "..", "testdata", "fixtures", "claude_code", "2.1.327", "linux", "cli")
	expectedRun = filepath.Join("..", "adapters", "claudecode", "fixtures", "2.1.327", "linux", "cli", "expected.jsonl")
)

// captureHome is the folder the capture ran in, which every payload names.
// A replay puts a folder of its own in its place, so the agent's resolver
// finds the session's folder on this machine.
const captureHome = "/home/capture/pairtime-capture"

// captureSession is the capture's first session, in its payloads.
const captureSession = "617e16a3-ddb4-44ca-af68-72cf58ed1a34"

// readCapture reads the capture's hook calls, in the order they came. The
// agent reads their parents' names with this OS's table (hookpath.Of), and
// the capture's are Linux's, so off Linux the test is skipped.
func readCapture(t *testing.T) []adapters.Call {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the capture's process names are Linux's")
	}
	calls, err := adapters.ReadCalls(os.DirFS(captureRun))
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 90 {
		t.Fatalf("the capture has %d calls, want WP-AD1's 90", len(calls))
	}
	return calls
}

// hookReplay hands hook calls to a real agent over its socket as the
// harness's own processes would: the agent's walk of each connection
// (Options.Walk) gives the processes the capture recorded above the hook,
// and the payloads name the replay's folder instead of the capture's.
type hookReplay struct {
	dirs home.Dirs
	home string // stands in for captureHome
	logs *syncBuffer

	mu    sync.Mutex
	chain []platform.Proc // the walk of the call being sent
}

func newHookReplay(t *testing.T) *hookReplay {
	t.Helper()
	// Short, for the socket's name: sun_path is 104 bytes on macOS.
	dir, err := os.MkdirTemp("", "pt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	// The canonical path, as the resolver keys folders, so file paths in
	// the payloads are under the project root it finds.
	if dir, err = filepath.EvalSymlinks(dir); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(dir, "d")
	r := &hookReplay{dirs: home.Dirs{Data: data, Config: data, Runtime: data}, home: filepath.Join(dir, "h"), logs: &syncBuffer{}}
	if err := os.MkdirAll(r.folder(), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("agent log:\n%s", r.logs.String())
		}
	})
	return r
}

// folder is the replay's stand-in for the capture's project folder, the
// cwd of every call.
func (r *hookReplay) folder() string { return filepath.Join(r.home, "projects", "cli") }

// options returns the agent's options for the replay, with the device key
// in stores (an empty list keeps it in a file in the data directory).
func (r *hookReplay) options(stores []keys.Store) Options {
	return Options{Dirs: r.dirs, Ports: []int{0}, KeyStores: stores, Walk: r.walk,
		Log: slog.New(slog.NewTextHandler(r.logs, nil))}
}

func (r *hookReplay) walk(pid int) []platform.Proc {
	r.mu.Lock()
	defer r.mu.Unlock()
	if pid != os.Getpid() {
		return nil
	}
	return slices.Clone(r.chain)
}

// send hands call c of harness h to the agent over its socket and waits
// for the ack. base is the replay's first call, from which the calls'
// monotonic readings count.
func (r *hookReplay) send(t *testing.T, h event.Harness, c adapters.Call, base time.Time) {
	t.Helper()
	c.Stdin = bytes.ReplaceAll(c.Stdin, []byte(captureHome), []byte(r.home))
	d := adapters.DeliveryOf(h, c, adapters.Walked, base)
	// The agent walks from the pid the OS gives for the connection, this
	// process, and the walk counts when its first process is the one the
	// message names first (hookpath.Of).
	m := d.Message
	m.Chain = []inbox.Proc{{PID: os.Getpid(), Name: "pairtime", Start: c.Arrived}}
	walk := slices.Clone(d.Walk)
	walk[0].PID = os.Getpid()
	r.mu.Lock()
	r.chain = walk
	r.mu.Unlock()

	ep, err := ipc.EndpointFor(r.dirs)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := ipc.Dial(ctx, ep)
	if err != nil {
		t.Fatalf("hooks/%s: dialing the agent: %v", c.Dir, err)
	}
	defer conn.Close()
	if err := conn.Send(ctx, m); err != nil {
		t.Fatalf("hooks/%s: the agent didn't ack: %v", c.Dir, err)
	}
}

// waitParsed waits until the agent has parsed n calls of Claude Code's
// since it started, and holds none back.
func waitParsed(t *testing.T, a *running, n uint64) statusReply {
	t.Helper()
	return waitStatus(t, a, func(s statusReply) bool {
		return s.Adapters.Harnesses[event.HarnessClaudeCode].Calls+s.Adapters.Other.Calls >= n && s.Adapters.Waiting == 0
	})
}

// ledgerRecord is an event record read back from the ledger.
type ledgerRecord struct {
	ev   event.Event
	body pkgledger.Body
}

// readLedger reads back the event records of a stopped agent's ledger
// whose source is source, in the order they were written.
func readLedger(t *testing.T, dirs home.Dirs, source event.Source) []ledgerRecord {
	t.Helper()
	var out []ledgerRecord
	err := ledger.ReadBatches(dirs.Ledger(), 1, func(b ledger.Batch) error {
		for _, r := range b.Records {
			body, err := pkgledger.ParseBody(r.Body)
			if err != nil {
				return err
			}
			ev, err := body.Event()
			if err != nil {
				return err
			}
			if ev.Source == source {
				out = append(out, ledgerRecord{ev: ev, body: body})
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading the ledger back: %v", err)
	}
	return out
}

// deviceKeys opens a stopped agent's keystore and returns its ledger keys
// and K_session.
func deviceKeys(t *testing.T, dirs home.Dirs, stores []keys.Store) (*pkgledger.Keys, []byte) {
	t.Helper()
	ks, err := keys.Open(keys.Options{Dir: dirs.Data, Stores: stores})
	if err != nil {
		t.Fatalf("opening the installation's keystore: %v", err)
	}
	var secret []byte
	if err := ks.WithDeviceKey(func(kDev []byte) error {
		var err error
		secret, err = hookpath.SessionSecret(kDev)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return ks.Keys(), secret
}

// projectKeyOf returns the project key a stopped agent's installation gives
// folder: the git adapter's, under the K_project of its device key.
func projectKeyOf(t *testing.T, dirs home.Dirs, stores []keys.Store, folder string) string {
	t.Helper()
	ks, err := keys.Open(keys.Options{Dir: dirs.Data, Stores: stores})
	if err != nil {
		t.Fatalf("opening the installation's keystore: %v", err)
	}
	var secret []byte
	if err := ks.WithDeviceKey(func(kDev []byte) error {
		var err error
		secret, err = git.ProjectSecret(kDev)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	res, err := git.NewResolver(git.ResolverOptions{Secret: secret}, git.ProjectMap{})
	if err != nil {
		t.Fatal(err)
	}
	loc, err := res.Resolve(context.Background(), folder)
	if err != nil {
		t.Fatal(err)
	}
	return loc.Project
}

// checkExpected checks the ledger's hook records against expected.jsonl:
// the same events in the same order once their keys are symbols
// (adapters.Normalize), as the ledger keeps them (Event.Shareable); for
// each record whose line has content, a commitment or features made from
// exactly that content under the device's keys; and the project and file
// keys this installation's keys give, project being its key of the
// capture's folder.
func checkExpected(t *testing.T, got []ledgerRecord, k *pkgledger.Keys, project string) {
	t.Helper()
	raw, err := os.ReadFile(expectedRun)
	if err != nil {
		t.Fatal(err)
	}
	want, err := adapters.ReadLines(raw)
	if err != nil {
		t.Fatal(err)
	}
	var wantRecs, gotRecs []adapters.Replayed
	for _, l := range want {
		var ev event.Event
		if err := json.Unmarshal(l.Event, &ev); err != nil {
			t.Fatalf("expected.jsonl, hooks/%s: %v", l.From, err)
		}
		wantRecs = append(wantRecs, adapters.Replayed{From: l.From, Record: adapters.Record{Event: ev.Shareable()}})
	}
	for i, r := range got {
		from := ""
		if i < len(want) {
			from = want[i].From // the ledger doesn't say which call a record came from
		}
		gotRecs = append(gotRecs, adapters.Replayed{From: from, Record: adapters.Record{Event: r.ev}})
	}
	wantLines, err := adapters.Normalize(wantRecs)
	if err != nil {
		t.Fatal(err)
	}
	gotLines, err := adapters.Normalize(gotRecs)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("the ledger holds expected.jsonl's events, in its order", func(t *testing.T) {
		for _, d := range adapters.Diff(gotLines, wantLines, 20) {
			t.Error(d)
		}
	})
	t.Run("each record commits the content expected.jsonl gives it", func(t *testing.T) {
		fk := k.Features()
		for i := 0; i < min(len(got), len(want)); i++ {
			r, l := got[i], want[i]
			switch r.ev.Kind() {
			case event.KindPromptSubmit, event.KindAgentOutput:
				if l.Text == nil {
					if r.body.C != nil {
						t.Errorf("hooks/%s: a %s record commits text expected.jsonl doesn't give", l.From, r.ev.Kind())
					}
					continue
				}
				text := []byte(*l.Text)
				if r.ev.Kind() == event.KindAgentOutput {
					text = pkgledger.CommittedMessage(text)
				}
				c, err := k.Commit(r.ev.Seq, text)
				if err != nil {
					t.Fatal(err)
				}
				if r.body.C == nil || *r.body.C != c {
					t.Errorf("hooks/%s: the %s record's commitment isn't its text's", l.From, r.ev.Kind())
				}
			case event.KindAgentTool:
				edits := make([]features.Edit, 0, len(l.Edits))
				for _, e := range l.Edits {
					edits = append(edits, features.Edit{Path: e.Path, OldStart: e.OldStart, OldLines: e.OldLines, NewStart: e.NewStart, NewLines: e.NewLines})
				}
				f, err := fk.Tool(edits, l.Command)
				if err != nil {
					t.Fatal(err)
				}
				wantF, err := json.Marshal(f)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(r.body.F, wantF) {
					t.Errorf("hooks/%s: the agent.tool record's features aren't its edits' and command's", l.From)
				}
			}
		}
	})
	t.Run("each record's project, and each edited file, is keyed under this installation's K_project and K_feat", func(t *testing.T) {
		fk := k.Features()
		for i := 0; i < min(len(got), len(want)); i++ {
			r, l := got[i], want[i]
			if r.ev.ProjectKey != project {
				t.Errorf("hooks/%s: project_key %q, want %q", l.From, r.ev.ProjectKey, project)
			}
			tool, ok := r.ev.Data.(*event.AgentTool)
			if !ok || len(l.Edits) == 0 {
				continue
			}
			v, err := fk.Path(l.Edits[0].Path)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(tool.Files, []string{v.String()}) {
				t.Errorf("hooks/%s: files %v, want the edited file's key under K_feat", l.From, tool.Files)
			}
		}
	})
}

// TestHookCallsReachTheLedger drives a real agent with WP-AD1's captured
// Claude Code hook calls over its socket, from under the processes the
// capture recorded, and reads the ledger back: it holds the records
// expected.jsonl gives, with their content committed. Restarting the
// agent where the Claude Code adapter's memory decides a record, before
// the /loop prompts that match the scheduled prompt of the stop before
// them and before the queued message, changes none of them.
func TestHookCallsReachTheLedger(t *testing.T) {
	cases := []struct {
		name     string
		restarts []string // the calls the agent restarts before
	}{
		{"an agent that runs throughout writes expected.jsonl's records", nil},
		{"an agent restarted before the /loop prompts and the queued message writes the same records, so its adapters' memory survives the restarts",
			[]string{"000061", "000076"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			calls := readCapture(t)
			r := newHookReplay(t)
			opts := r.options([]keys.Store{})
			a := startAgent(t, opts)
			sent := uint64(0)
			var failures harnessStats // summed over the runs
			sum := func(s statusReply) {
				st := s.Adapters.Harnesses[event.HarnessClaudeCode]
				failures.ParseFailures += st.ParseFailures
				failures.WriteFailures += st.WriteFailures
			}
			for _, call := range calls {
				if slices.Contains(c.restarts, call.Dir) {
					sum(waitParsed(t, a, sent))
					a.cancel()
					if err := a.waitExit(t); err != nil {
						t.Fatalf("stopping the agent before hooks/%s: %v", call.Dir, err)
					}
					a, sent = startAgent(t, opts), 0
				}
				r.send(t, event.HarnessClaudeCode, call, calls[0].Arrived)
				sent++
			}
			sum(waitParsed(t, a, sent))
			a.cancel()
			if err := a.waitExit(t); err != nil {
				t.Fatalf("stopping the agent: %v", err)
			}
			t.Run("no captured call is a parse failure, in any of the agent's runs", func(t *testing.T) {
				if failures.ParseFailures != 0 || failures.WriteFailures != 0 {
					t.Errorf("counts %+v", failures)
				}
			})
			t.Run("once the agent stops, no call is left pending or held on disk", func(t *testing.T) {
				for _, name := range []string{r.dirs.Held(), r.dirs.Held() + ".replay", r.dirs.Held() + pendingSuffix} {
					if _, err := os.Stat(name); !errors.Is(err, fs.ErrNotExist) {
						t.Errorf("%s is there (%v)", filepath.Base(name), err)
					}
				}
			})
			k, _ := deviceKeys(t, r.dirs, []keys.Store{})
			checkExpected(t, readLedger(t, r.dirs, event.SourceHook), k, projectKeyOf(t, r.dirs, []keys.Store{}, r.folder()))
		})
	}
}

// TestHookCallsBeforeTheKeystore checks that a hook call taken in before
// the keystore opens waits, in this run or through a stop, and is parsed
// once it opens, with every key made under this installation's K_session:
// never under FixtureKeys' public secret, nor under an empty one. A call
// keeps its via hook through held.jsonl only in the run that held it
// (D-spool-via).
func TestHookCallsBeforeTheKeystore(t *testing.T) {
	cases := []struct {
		name string
		stop bool      // the agent stops while the keyring is still locked
		via  event.Via // the via of the calls' records
	}{
		{"calls taken in while the keyring is locked are parsed once it answers, via hook", false, event.ViaHook},
		{"calls held when the agent stops with the keyring locked are parsed by the next run, via plugin", true, event.ViaPlugin},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			calls := readCapture(t)[:2] // the session's start, and its first prompt
			r := newHookReplay(t)
			store := &lockedStore{items: map[string][]byte{}}
			stores := []keys.Store{store}
			opts := r.options(stores)
			opts.LedgerRetry = 20 * time.Millisecond
			// The first run makes K_dev in the store, so keystore.json names
			// it: from then on a locked keyring keeps the keystore shut,
			// where on a first run a key file would stand in for it.
			first := startAgent(t, opts)
			waitChain(t, first, r.dirs)
			first.cancel()
			if err := first.waitExit(t); err != nil {
				t.Fatal(err)
			}
			store.setLocked(true)
			a := startAgent(t, opts)
			for _, call := range calls {
				r.send(t, event.HarnessClaudeCode, call, calls[0].Arrived)
			}
			waiting := waitStatus(t, a, func(s statusReply) bool { return s.Adapters.Waiting == len(calls) && store.refusals() > 0 })
			if c.stop {
				a.cancel()
				if err := a.waitExit(t); err != nil {
					t.Fatal(err)
				}
				store.setLocked(false)
				a = startAgent(t, opts)
			} else {
				store.setLocked(false)
			}
			waitParsed(t, a, uint64(len(calls)))
			a.cancel()
			if err := a.waitExit(t); err != nil {
				t.Fatal(err)
			}
			_, secret := deviceKeys(t, r.dirs, stores)
			recs := readLedger(t, r.dirs, event.SourceHook)
			mine, public := adapters.NewKeys(secret), adapters.FixtureKeys()
			empty := hookpath.SessionKey(nil, captureSession)
			var p event.PromptSubmit
			var prompt string
			for _, rec := range recs {
				if ps, ok := rec.ev.Data.(*event.PromptSubmit); ok {
					p = *ps
				}
			}
			var payload struct {
				PromptID string `json:"prompt_id"`
			}
			if err := json.Unmarshal(calls[1].Stdin, &payload); err != nil {
				t.Fatal(err)
			}
			prompt = payload.PromptID
			_, heldErr := os.Stat(r.dirs.Held())
			_, replayErr := os.Stat(r.dirs.Held() + ".replay")
			checks := []struct {
				name string
				ok   bool
			}{
				{"while the keyring is locked, the calls wait and none is parsed",
					waiting.Adapters.Harnesses[event.HarnessClaudeCode].Calls == 0},
				{"both calls make their records once the keyring answers", len(recs) == 2},
				{"every record's session_key is the session's under this installation's K_session",
					len(recs) == 2 && recs[0].ev.SessionKey == mine.Session(captureSession) && recs[1].ev.SessionKey == mine.Session(captureSession)},
				{"no record's session_key is the one FixtureKeys or an empty secret gives",
					len(recs) == 2 && !slices.ContainsFunc(recs, func(r ledgerRecord) bool {
						return r.ev.SessionKey == public.Session(captureSession) || r.ev.SessionKey == empty
					})},
				{"the prompt's turn_key is its prompt_id's under this installation's K_session, and not FixtureKeys'",
					p.TurnKey == mine.Turn(captureSession, prompt) && p.TurnKey != public.Turn(captureSession, prompt)},
				{"no held call is left on disk, in held.jsonl or the file its replay renames it to",
					errors.Is(heldErr, fs.ErrNotExist) && errors.Is(replayErr, fs.ErrNotExist)},
				{"the records' via is hook in the run that held the calls, and plugin in a later one",
					len(recs) == 2 && !slices.ContainsFunc(recs, func(r ledgerRecord) bool { return r.ev.Via != c.via })},
			}
			for _, ch := range checks {
				t.Run(ch.name, func(t *testing.T) {
					if !ch.ok {
						t.Errorf("no: %d records %+v", len(recs), recs)
					}
				})
			}
		})
	}
}

// TestHookCallsAKilledAgentAcked checks that calls an agent acked and
// hadn't parsed when it was killed, which it wrote to held.jsonl.pending
// before each ack, are parsed by the next start. They come out as plugin
// calls (D-spool-via): the next run can't tell the killed run's lines from
// ones any process of the user wrote, so the via, walk and stamp they name
// count for nothing.
func TestHookCallsAKilledAgentAcked(t *testing.T) {
	calls := readCapture(t)[:2] // the session's start, and its first prompt
	r := newHookReplay(t)
	if err := os.MkdirAll(r.dirs.Data, 0o700); err != nil {
		t.Fatal(err)
	}
	// The killed run's host, with its own key for the lines it wrote.
	killed := newAdapterHost(&agent{dirs: r.dirs, log: slog.New(slog.DiscardHandler), problems: make(map[string]string)})
	var pending []byte
	for _, c := range calls {
		c.Stdin = bytes.ReplaceAll(c.Stdin, []byte(captureHome), []byte(r.home))
		// As the killed run took them in: over the socket, from under the
		// captured parents (adapters.DeliveryOf).
		line, err := killed.heldLine(adapters.DeliveryOf(event.HarnessClaudeCode, c, adapters.Walked, calls[0].Arrived))
		if err != nil {
			t.Fatal(err)
		}
		pending = append(pending, line...)
	}
	if err := os.WriteFile(r.dirs.Held()+pendingSuffix, pending, 0o600); err != nil {
		t.Fatal(err)
	}
	a := startAgent(t, r.options([]keys.Store{}))
	waitParsed(t, a, uint64(len(calls)))
	a.cancel()
	if err := a.waitExit(t); err != nil {
		t.Fatal(err)
	}
	_, secret := deviceKeys(t, r.dirs, []keys.Store{})
	recs := readLedger(t, r.dirs, event.SourceHook)
	_, pendingErr := os.Stat(r.dirs.Held() + pendingSuffix)
	var start event.SessionStart
	if len(recs) > 0 {
		if s, ok := recs[0].ev.Data.(*event.SessionStart); ok {
			start = *s
		}
	}
	checks := []struct {
		name string
		ok   bool
	}{
		{"the next start makes the calls' records, the session's start then its prompt, both via plugin",
			len(recs) == 2 && recs[0].ev.Kind() == event.KindSessionStart && recs[1].ev.Kind() == event.KindPromptSubmit &&
				recs[0].ev.Via == event.ViaPlugin && recs[1].ev.Via == event.ViaPlugin},
		{"the walk the killed run's lines name reaches no record: the session's start has no parent class, entrypoint or terminal",
			len(recs) == 2 && start.ParentClass == event.ParentUnknown && start.Entrypoint == event.EntrypointUnknown && !start.HasTTY},
		{"they are keyed under this installation's K_session",
			len(recs) == 2 && recs[1].ev.SessionKey == adapters.NewKeys(secret).Session(captureSession)},
		{"the pending log is gone once they are parsed", errors.Is(pendingErr, fs.ErrNotExist)},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Errorf("no: %d records %+v, pending log %v", len(recs), recs, pendingErr)
			}
		})
	}
}

// TestPendingLog checks held.jsonl.pending on a host with no worker: a
// call the socket acks is written down before the journal stores it, and
// the file is emptied only once every call in it is parsed or held.
func TestPendingLog(t *testing.T) {
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	self := inbox.Proc{PID: 4242, Name: "pairtime", Start: at}
	call := func(t *testing.T, prompt string) inbox.Delivery {
		return inbox.Delivery{Message: hookMessage(t, "UserPromptSubmit", `{"session_id":"s1","prompt":"`+prompt+`"}`, self, at),
			Via: inbox.ViaSocket}
	}
	newHost := func(dirs home.Dirs) *adapterHost {
		return newAdapterHost(&agent{dirs: dirs, log: slog.New(slog.DiscardHandler), problems: make(map[string]string)})
	}
	// lineOf is d's line as h writes it, under h's own key.
	lineOf := func(t *testing.T, h *adapterHost, d inbox.Delivery) []byte {
		t.Helper()
		b, err := h.heldLine(d)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	read := func(t *testing.T, name string) []byte {
		t.Helper()
		b, err := os.ReadFile(name)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatal(err)
		}
		return b
	}
	takeQueue := func(h *adapterHost) { // as the worker's step does
		h.mu.Lock()
		h.queue = nil
		h.mu.Unlock()
	}
	cases := []struct {
		name string
		run  func(t *testing.T, dirs home.Dirs, pending string)
	}{
		{"a call is written down before the journal stores it, and stays while it waits to be parsed", func(t *testing.T, dirs home.Dirs, pending string) {
			h := newHost(dirs)
			d := call(t, "one")
			settle := h.pend(d)
			if got := read(t, pending); !bytes.Equal(got, lineOf(t, h, d)) {
				t.Errorf("before the journal stored it, the file holds %q", got)
			}
			settle(true)
			h.donePending(false)
			if got := read(t, pending); !bytes.Equal(got, lineOf(t, h, d)) {
				t.Errorf("while the call waits in the queue, the file holds %q", got)
			}
		}},
		{"nothing is emptied under a call written down and not yet settled", func(t *testing.T, dirs home.Dirs, pending string) {
			h := newHost(dirs)
			d := call(t, "one")
			settle := h.pend(d)
			h.donePending(false)
			if got := read(t, pending); !bytes.Equal(got, lineOf(t, h, d)) {
				t.Errorf("the file holds %q", got)
			}
			settle(true)
		}},
		{"once the worker has taken the queue the file is emptied, and the last step deletes it", func(t *testing.T, dirs home.Dirs, pending string) {
			h := newHost(dirs)
			h.pend(call(t, "one"))(true)
			takeQueue(h)
			h.donePending(false)
			if got := read(t, pending); len(got) != 0 {
				t.Errorf("after the worker took the call, the file holds %q", got)
			}
			h.donePending(true)
			if _, err := os.Stat(pending); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("after the last step the file is there (%v)", err)
			}
		}},
		{"a copy the journal had already isn't queued, and its line goes with the next emptying", func(t *testing.T, dirs home.Dirs, pending string) {
			h := newHost(dirs)
			h.pend(call(t, "one"))(false)
			if len(h.queue) != 0 {
				t.Error("a copy the journal had was queued")
			}
			h.donePending(false)
			if got := read(t, pending); len(got) != 0 {
				t.Errorf("the file holds %q", got)
			}
		}},
		{"a start moves the calls a killed run left pending to the end of held.jsonl", func(t *testing.T, dirs home.Dirs, pending string) {
			earlier := newHost(dirs)
			held, left := lineOf(t, earlier, call(t, "held")), lineOf(t, earlier, call(t, "left"))
			if err := os.WriteFile(dirs.Held(), held, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(pending, left, 0o600); err != nil {
				t.Fatal(err)
			}
			newHost(dirs).recoverPending()
			if got, want := read(t, dirs.Held()), slices.Concat(held, left); !bytes.Equal(got, want) {
				t.Errorf("held.jsonl holds %q, want %q", got, want)
			}
			if _, err := os.Stat(pending); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("the pending file is still there (%v)", err)
			}
		}},
		{"when the move fails, this run empties the file only back to what the killed run left, and keeps it", func(t *testing.T, dirs home.Dirs, pending string) {
			left := lineOf(t, newHost(dirs), call(t, "left"))
			if err := os.WriteFile(pending, left, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(dirs.Held(), 0o700); err != nil { // so held.jsonl can't be written
				t.Fatal(err)
			}
			h := newHost(dirs)
			h.recoverPending()
			if h.a.currentProblems()["adapters.recover"] == "" {
				t.Error("no problem says the move failed")
			}
			h.pend(call(t, "new"))(true)
			takeQueue(h)
			h.donePending(false)
			h.donePending(true)
			if got := read(t, pending); !bytes.Equal(got, left) {
				t.Errorf("the pending file holds %q, want the killed run's %q", got, left)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
			c.run(t, dirs, dirs.Held()+pendingSuffix)
		})
	}
}

// TestHeldLines checks what a line of held.jsonl reads back as: the call
// whole with how it came on a line this run wrote, and the call alone, as
// a spooled one reads, on any other (D-spool-via).
func TestHeldLines(t *testing.T) {
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	self := inbox.Proc{PID: 4242, Name: "pairtime", Start: at}
	newHost := func() *adapterHost {
		return newAdapterHost(&agent{log: slog.New(slog.DiscardHandler), problems: make(map[string]string)})
	}
	call := func(t *testing.T, prompt string) inbox.Delivery {
		return inbox.Delivery{
			Message: hookMessage(t, "UserPromptSubmit", `{"session_id":"s1","prompt":"`+prompt+`"}`, self, at),
			Via:     inbox.ViaSocket, PeerPID: 4242,
			Walk: []platform.Proc{{PID: 4242, PPID: 300, Name: "pairtime", Start: at},
				{PID: 300, PPID: 200, Name: "claude", Start: at.Add(-time.Minute)}},
			EventVia: event.ViaHook, Harness: event.HarnessClaudeCode,
			Received: clock.Stamp{Wall: at.Add(time.Second), Mono: 5_000, BootID: "boot-1"},
		}
	}
	lineOf := func(t *testing.T, h *adapterHost, d inbox.Delivery) []byte {
		t.Helper()
		b, err := h.heldLine(d)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	entryOf := func(t *testing.T, line []byte) heldEntry {
		t.Helper()
		var e heldEntry
		if err := json.Unmarshal(line, &e); err != nil {
			t.Fatal(err)
		}
		return e
	}
	// asSpooled checks that got is d's call read as the spool's lines are:
	// via the inbox, with no walk, pid or verdict, and stamped by the read,
	// between from and to.
	asSpooled := func(t *testing.T, got, d inbox.Delivery, from, to time.Time) {
		t.Helper()
		if got.ID != d.ID || !bytes.Equal(got.Body, d.Body) {
			t.Error("the call itself didn't come back whole")
		}
		if got.Via != inbox.ViaInbox || got.Walk != nil || got.PeerPID != 0 || got.EventVia != "" || got.Harness != "" {
			t.Errorf("via %q, walk %v, peer pid %d, event via %q, harness %q: want the inbox's, and none of the rest",
				got.Via, got.Walk, got.PeerPID, got.EventVia, got.Harness)
		}
		if w := got.Received.Wall; w.Before(from) || w.After(to) {
			t.Errorf("stamped %v, want the read's own time, between %v and %v", w, from, to)
		}
	}
	cases := []struct {
		name string
		run  func(t *testing.T, h *adapterHost)
	}{
		{"a line this run wrote keeps its via, the agent's walk, the sender's pid and the agent's stamp", func(t *testing.T, h *adapterHost) {
			d := call(t, "one")
			got, ok := h.readHeld(lineOf(t, h, d))
			if !ok {
				t.Fatal("the line didn't read")
			}
			if got.ID != d.ID || got.Via != d.Via || got.PeerPID != d.PeerPID || got.EventVia != d.EventVia || got.Harness != d.Harness {
				t.Errorf("got %+v, want %+v", got, d)
			}
			if !slices.EqualFunc(got.Walk, d.Walk, func(a, b platform.Proc) bool { return a.ID() == b.ID() && a.Name == b.Name }) {
				t.Errorf("walk %v, want %v", got.Walk, d.Walk)
			}
			if !got.Received.Wall.Equal(d.Received.Wall) || got.Received.Mono != d.Received.Mono || got.Received.BootID != d.Received.BootID {
				t.Errorf("stamp %+v, want %+v", got.Received, d.Received)
			}
		}},
		{"a line an earlier run wrote, under its own key, reads as a spooled call", func(t *testing.T, h *adapterHost) {
			d := call(t, "one")
			line := lineOf(t, newHost(), d)
			from := time.Now()
			got, ok := h.readHeld(line)
			if !ok {
				t.Fatal("the line didn't read")
			}
			asSpooled(t, got, d, from, time.Now())
		}},
		{"a line whose call was changed after it was written reads as a spooled call", func(t *testing.T, h *adapterHost) {
			d := call(t, "one")
			d.Via = inbox.ViaPipe
			line := bytes.Replace(lineOf(t, h, d), []byte(`"via":"pipe"`), []byte(`"via":"socket"`), 1)
			from := time.Now()
			got, ok := h.readHeld(line)
			if !ok {
				t.Fatal("the line didn't read")
			}
			asSpooled(t, got, d, from, time.Now())
		}},
		{"a MAC moved onto another call reads as a spooled call", func(t *testing.T, h *adapterHost) {
			one, two := call(t, "one"), call(t, "two")
			line, err := json.Marshal(heldEntry{Call: entryOf(t, lineOf(t, h, two)).Call, MAC: entryOf(t, lineOf(t, h, one)).MAC})
			if err != nil {
				t.Fatal(err)
			}
			from := time.Now()
			got, ok := h.readHeld(line)
			if !ok {
				t.Fatal("the line didn't read")
			}
			asSpooled(t, got, two, from, time.Now())
		}},
		{"a line written by hand, with no MAC, reads as a spooled call", func(t *testing.T, h *adapterHost) {
			d := call(t, "one")
			line := []byte(`{"call":` + string(entryOf(t, lineOf(t, h, d)).Call) + `}`)
			from := time.Now()
			got, ok := h.readHeld(line)
			if !ok {
				t.Fatal("the line didn't read")
			}
			asSpooled(t, got, d, from, time.Now())
		}},
		{"a line that holds no call is no call", func(t *testing.T, h *adapterHost) {
			for _, line := range []string{`not json`, `{"mac":"00"}`, `{"call":{"message":"x"},"mac":"00"}`, `{"call":null}`} {
				if _, ok := h.readHeld([]byte(line)); ok {
					t.Errorf("%s read as a call", line)
				}
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t, newHost()) })
	}
}

// TestMalformedHookCalls checks that calls the adapters can't read count
// as parse failures, naming no content, and that the calls after them
// still make their records.
func TestMalformedHookCalls(t *testing.T) {
	capture := readCapture(t)
	start, prompt := capture[0], capture[1]
	const secretText = "a prompt that must not reach any status"
	cut := prompt
	cut.Dir, cut.Stdin = "f00001", []byte(`{"session_id":"`+captureSession+`","prompt":"`+secretText)
	var unknown map[string]any
	if err := json.Unmarshal(prompt.Stdin, &unknown); err != nil {
		t.Fatal(err)
	}
	unknown["hook_event_name"] = "Mystery"
	unknown["prompt"] = secretText
	stranger := prompt
	stranger.Dir, stranger.Event = "f00002", "Mystery"
	stranger.Stdin, _ = json.Marshal(unknown)
	other := prompt
	other.Dir = "f00003"
	// A call the adapter reads into: its reason is no reason's name, so the
	// failure is about a value the adapter read, and the call's record
	// still comes, without it.
	end := capture[78] // the first session's own SessionEnd
	if end.Dir != "000079" || end.Event != "SessionEnd" {
		t.Fatalf("hooks/%s is a %s, want the session's SessionEnd at 000079", end.Dir, end.Event)
	}
	var endPayload map[string]any
	if err := json.Unmarshal(end.Stdin, &endPayload); err != nil {
		t.Fatal(err)
	}
	endPayload["reason"] = secretText
	end.Dir = "f00004"
	end.Stdin, _ = json.Marshal(endPayload)

	r := newHookReplay(t)
	a := startAgent(t, r.options([]keys.Store{}))
	sends := []struct {
		h     event.Harness
		call  adapters.Call
		fails bool // a Claude Code parse failure
	}{
		{event.HarnessClaudeCode, start, false},
		{event.HarnessClaudeCode, cut, true},
		{event.HarnessClaudeCode, stranger, true},
		{event.HarnessCursor, other, false},
		{event.HarnessClaudeCode, end, true},
		{event.HarnessClaudeCode, prompt, false},
	}
	// Each failure's words, read as it comes: the status keeps the last.
	var failures []string
	var s statusReply
	for i, sd := range sends {
		r.send(t, sd.h, sd.call, start.Arrived)
		s = waitParsed(t, a, uint64(i+1))
		if sd.fails {
			failures = append(failures, s.Adapters.Harnesses[event.HarnessClaudeCode].LastFailure)
		}
	}
	a.cancel()
	if err := a.waitExit(t); err != nil {
		t.Fatal(err)
	}
	recs := readLedger(t, r.dirs, event.SourceHook)
	st := s.Adapters.Harnesses[event.HarnessClaudeCode]
	checks := []struct {
		name string
		ok   bool
	}{
		{"a payload that isn't JSON, an event pairtime doesn't install and a field with no valid value are Claude Code's parse failures", st.ParseFailures == 3},
		{"a call of a harness with no adapter counts apart from them", s.Adapters.Other.Calls == 1 && s.Adapters.Other.ParseFailures == 1},
		{"each failure says what went wrong without the call's content, the bad reason's included, which the adapter read",
			len(failures) == 3 && strings.Contains(failures[2], "reason") && !slices.ContainsFunc(failures, func(f string) bool {
				return f == "" || strings.Contains(f, secretText)
			})},
		{"the call with the bad value still makes its record, and the agent parses the call after it: start, end and prompt are in the ledger",
			len(recs) == 3 && recs[0].ev.Kind() == event.KindSessionStart && recs[1].ev.Kind() == event.KindSessionEnd &&
				recs[2].ev.Kind() == event.KindPromptSubmit},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Errorf("no: status %+v, failures %q, %d records", s.Adapters, failures, len(recs))
			}
		})
	}
}

// runGit runs git in dir, as a person at the terminal would.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.invalid",
		"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestCommitsInAHookedSession checks the git adapter in the agent: a
// session's folder in a git repo becomes the repo's project, the agent
// watches the repo, and a commit there becomes a git.change record with
// the session's project, the person's or, when the session's bash tool ran
// the command that made it, the agent's.
func TestCommitsInAHookedSession(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git isn't installed")
	}
	capture := readCapture(t)
	r := newHookReplay(t)
	repo := r.folder()
	runGit(t, repo, "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-q", "-m", "first")

	now := time.Now().UTC()
	start := capture[0]
	start.Arrived = now
	var tool map[string]any
	if err := json.Unmarshal(capture[2].Stdin, &tool); err != nil { // a Bash PostToolUse
		t.Fatal(err)
	}

	a := startAgent(t, r.options([]keys.Store{}))
	r.send(t, event.HarnessClaudeCode, start, now)
	waitStatus(t, a, func(s statusReply) bool { return s.Adapters.Git.Watched == 1 })

	// The person commits at the terminal.
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "a.txt")
	runGit(t, repo, "commit", "-q", "-m", "by hand")
	human := runGit(t, repo, "rev-parse", "HEAD")
	waitStatus(t, a, func(s statusReply) bool { return s.Adapters.Git.Changes == 1 })

	// The agent's bash tool commits, and its PostToolUse comes as the
	// command ends.
	if err := os.WriteFile(filepath.Join(repo, "b.txt"), []byte("b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "b.txt")
	runGit(t, repo, "commit", "-q", "-m", "by the agent")
	agentSHA := runGit(t, repo, "rev-parse", "HEAD")
	input, _ := tool["tool_input"].(map[string]any)
	input["command"] = `git commit -q -m "by the agent"`
	tool["tool_use_id"] = "toolu_test_commit"
	tool["duration_ms"] = 4000
	call := capture[2]
	call.Dir, call.Arrived = "f00010", time.Now().UTC()
	call.Stdin, _ = json.Marshal(tool)
	r.send(t, event.HarnessClaudeCode, call, now)
	s := waitStatus(t, a, func(s statusReply) bool { return s.Adapters.Git.Changes == 2 })
	a.cancel()
	if err := a.waitExit(t); err != nil {
		t.Fatal(err)
	}

	hooked := readLedger(t, r.dirs, event.SourceHook)
	changes := readLedger(t, r.dirs, event.SourceGit)
	project := ""
	if len(hooked) > 0 {
		project = hooked[0].ev.ProjectKey
	}
	change := func(i int) (*event.GitChange, event.Event) {
		if i >= len(changes) {
			return &event.GitChange{}, event.Event{}
		}
		g, _ := changes[i].ev.Data.(*event.GitChange)
		if g == nil {
			g = &event.GitChange{}
		}
		return g, changes[i].ev
	}
	g1, e1 := change(0)
	g2, e2 := change(1)
	want := projectKeyOf(t, r.dirs, []keys.Store{}, repo)
	// The commit records the agent kept for the uploader, from every day's
	// file of commits/ (D-commit-records-local).
	var kept []map[string]any
	var raw []byte
	entries, err := os.ReadDir(r.dirs.Commits())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	for _, e := range entries {
		if day, ok := strings.CutSuffix(e.Name(), ".jsonl"); ok {
			kept = append(kept, readCommitLines(t, r.dirs.Commits(), day)...)
			b, err := os.ReadFile(filepath.Join(r.dirs.Commits(), e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			raw = append(raw, b...)
		}
	}
	keptAs := func(sha string, actor event.Actor) bool {
		return slices.ContainsFunc(kept, func(m map[string]any) bool {
			return m["sha"] == sha && m["actor"] == string(actor) && m["project"] == project
		})
	}
	checks := []struct {
		name string
		ok   bool
	}{
		{"the session's records carry the repo's project, keyed from its root commit under this installation's K_project",
			strings.HasPrefix(project, "g") && project == want &&
				!slices.ContainsFunc(hooked, func(r ledgerRecord) bool { return r.ev.ProjectKey != project })},
		{"each commit made one git.change record", len(changes) == 2},
		{"the commit made at the terminal is the person's, in the session's project",
			g1.Op == event.GitCommit && g1.SHA == human && g1.Actor == event.ActorHuman && e1.ProjectKey == project},
		{"the commit the agent's bash tool made is the agent's, in the session's project",
			g2.Op == event.GitCommit && g2.SHA == agentSHA && g2.Actor == event.ActorAgent && e2.ProjectKey == project},
		{"git.change records come via git and belong to no session",
			e1.Via == event.ViaGit && e2.Via == event.ViaGit && e1.SessionKey == event.NoSession && e2.SessionKey == event.NoSession},
		{"commits/ keeps a commit record for each, the person's and the agent's, in the session's project",
			len(kept) == 2 && keptAs(human, event.ActorHuman) && keptAs(agentSHA, event.ActorAgent)},
		{"the commit records hold tier-1 fields only: no message, no file name, no folder",
			!slices.ContainsFunc(kept, func(m map[string]any) bool {
				for k := range m {
					if !slices.Contains(tierOneCommit, k) {
						return true
					}
				}
				return false
			}) && !slices.ContainsFunc([]string{"by hand", "by the agent", "a.txt", "b.txt", repo, filepath.Base(repo)},
				func(text string) bool { return bytes.Contains(raw, []byte(text)) })},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Errorf("no: project %q, changes %+v %+v, status %+v", project, g1, g2, s.Adapters)
			}
		})
	}
}
