// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"maps"
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
	"github.com/some-du6e/pairtime/internal/agent/state"
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
		// captured parents (adapters.DeliveryOf), and stamped as they came,
		// which was just now, so a start doesn't drop them as held too long
		// (heldKeep), whenever the capture was made.
		d := adapters.DeliveryOf(event.HarnessClaudeCode, c, adapters.Walked, calls[0].Arrived)
		d.Received.Wall = time.Now().UTC()
		line, err := killed.heldLine(d)
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

// pendingJournal is a journal that keeps, at each Store, what the file name
// held then, and stores every call.
type pendingJournal struct {
	name string
	saw  [][]byte
}

func (j *pendingJournal) Deliver(d inbox.Delivery) error {
	_, err := j.Store(d)
	return err
}

func (j *pendingJournal) Store(inbox.Delivery) (bool, error) {
	b, err := os.ReadFile(j.name)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	j.saw = append(j.saw, b)
	return true, nil
}

// TestPendingLog checks held.jsonl.pending on a host with no worker: a
// call the socket acks is written down before the journal stores it, and
// the file is emptied only once every call in it is parsed or held.
func TestPendingLog(t *testing.T) {
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	self := inbox.Proc{PID: 4242, Name: "pairtime", Start: at}
	// call is a prompt the socket took in just now, as the agent stamps it.
	call := func(t *testing.T, prompt string) inbox.Delivery {
		return inbox.Delivery{Message: hookMessage(t, "UserPromptSubmit", `{"session_id":"s1","prompt":"`+prompt+`"}`, self, at),
			Via: inbox.ViaSocket, Received: clock.Stamp{Wall: time.Now().UTC()}}
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
	// park has d wait for its folder's project, as admit does a call taken
	// in over the socket.
	park := func(h *adapterHost, d inbox.Delivery) {
		h.parked = append(h.parked, hostCall{d: d, live: true})
		h.count(func() { h.parkedN++; h.parkedLive++ })
	}
	// during is a copiedForTest hook that takes d in, as a hook's call that
	// comes while the rewrite copies, and settles it. The rewrite holds no
	// lock there, so the ack mustn't wait for the copy, nor for the disk to
	// flush it.
	during := func(t *testing.T, h *adapterHost, d inbox.Delivery, stored bool) func() {
		return func() {
			acked := make(chan struct{})
			go func() {
				h.pend(d)(stored)
				close(acked)
			}()
			select {
			case <-acked:
			case <-time.After(10 * time.Second):
				t.Error("a call taken in during the rewrite's copy waited for it")
			}
		}
	}
	// lose has h without the file open, as after a rewrite that couldn't
	// open it again.
	lose := func(h *adapterHost) {
		h.mu.Lock()
		h.pending.Close()
		h.pending = nil
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
		{"the hook sink writes a call down before the journal stores it, so it is on disk before the socket acks it", func(t *testing.T, dirs home.Dirs, pending string) {
			h := newHost(dirs)
			j := &pendingJournal{name: pending}
			sink := newHookSink(j, runtime.GOOS, newHookTies(), hookSteps{pend: h.pend})
			d := call(t, "one")
			if err := sink.Deliver(d); err != nil {
				t.Fatal(err)
			}
			if len(j.saw) != 1 || !bytes.Contains(j.saw[0], []byte(d.ID)) {
				t.Errorf("when the journal stored the call, the pending file held %q", j.saw)
			}
			if len(h.queue) != 1 {
				t.Errorf("%d calls went to the worker, want the one the journal stored", len(h.queue))
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
		{"a call that waits for its folder keeps the first line pend wrote of it, and its second copy's line goes with the calls that are done", func(t *testing.T, dirs home.Dirs, pending string) {
			h := newHost(dirs)
			waiting, done := call(t, "waiting"), call(t, "done")
			h.pend(waiting)(true)
			takeQueue(h)
			park(h, waiting)
			h.pend(waiting)(false) // a copy, as the spool's of a call the socket acked too late
			h.pend(done)(true)
			takeQueue(h)
			h.donePending(false)
			if got := read(t, pending); !bytes.Equal(got, lineOf(t, h, waiting)) {
				t.Errorf("the file holds %q, want the waiting call's line, once", got)
			}
		}},
		{"a call done before one that waits goes by a rewrite, during whose copy a call is taken in at once, and the lines then lie where pendingAt says", func(t *testing.T, dirs home.Dirs, pending string) {
			h := newHost(dirs)
			done, waiting, late := call(t, "done"), call(t, "waiting"), call(t, "late")
			h.pend(done)(true)
			h.pend(waiting)(true)
			takeQueue(h)
			park(h, waiting)
			h.pend(waiting)(false) // a copy
			h.copiedForTest = during(t, h, late, true)
			h.donePending(false)
			wl, ll := lineOf(t, h, waiting), lineOf(t, h, late)
			if got := read(t, pending); !bytes.Equal(got, slices.Concat(wl, ll)) {
				t.Errorf("the file holds %q, want the waiting call's line, once, and then the one taken in during the rewrite", got)
			}
			want := map[string][]inbox.Span{waiting.ID: {{Off: 0, Len: int64(len(wl))}}, late.ID: {{Off: int64(len(wl)), Len: int64(len(ll))}}}
			if !maps.EqualFunc(h.pendingAt, want, slices.Equal[[]inbox.Span]) {
				t.Errorf("pendingAt is %v, want %v", h.pendingAt, want)
			}
			takeQueue(h)
			h.donePending(false)
			if got := read(t, pending); !bytes.Equal(got, wl) {
				t.Errorf("once the late call is parsed too, the file holds %q, want the waiting call's line alone", got)
			}
		}},
		{"a copy the journal stores while a rewrite copies keeps its line once the call waits, though the rewrite dropped the first copy, which the journal didn't store", func(t *testing.T, dirs home.Dirs, pending string) {
			h := newHost(dirs)
			done, waiting, x := call(t, "done"), call(t, "waiting"), call(t, "x")
			h.pend(done)(true)
			h.pend(waiting)(true)
			h.pend(x)(false) // its first copy, which the journal failed to store
			takeQueue(h)
			park(h, waiting)
			h.copiedForTest = during(t, h, x, true) // its spooled copy, which the journal stores
			h.donePending(false)
			h.copiedForTest = nil
			takeQueue(h)
			park(h, x)
			h.donePending(false)
			if got, want := read(t, pending), slices.Concat(lineOf(t, h, waiting), lineOf(t, h, x)); !bytes.Equal(got, want) {
				t.Errorf("the file holds %q, want the two waiting calls' lines", got)
			}
		}},
		{"the step after a rewrite that couldn't open the file again opens it, and empties it", func(t *testing.T, dirs home.Dirs, pending string) {
			h := newHost(dirs)
			h.pend(call(t, "one"))(true)
			takeQueue(h)
			lose(h)
			h.donePending(false)
			if got := read(t, pending); len(got) != 0 {
				t.Errorf("the file holds %q", got)
			}
			if h.pending == nil {
				t.Error("the file isn't open for pend")
			}
		}},
		{"the last step after a rewrite that couldn't open the file again deletes it by name, once nothing in it is left to keep", func(t *testing.T, dirs home.Dirs, pending string) {
			h := newHost(dirs)
			h.pend(call(t, "one"))(true)
			takeQueue(h)
			lose(h)
			h.donePending(true)
			if _, err := os.Stat(pending); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("the file is still there (%v)", err)
			}
		}},
		{"a waiting call whose line pend couldn't write holds nothing back, and the lines of the calls that are done still go", func(t *testing.T, dirs home.Dirs, pending string) {
			if runtime.GOOS == "windows" || os.Geteuid() == 0 {
				t.Skip("needs a file this user can't open, which Windows' modes and root don't give")
			}
			h := newHost(dirs)
			done, waiting := call(t, "done"), call(t, "waiting")
			h.pend(done)(true)
			lose(h)
			if err := os.Chmod(pending, 0); err != nil {
				t.Fatal(err)
			}
			h.pend(waiting)(true) // its line isn't written
			if err := os.Chmod(pending, 0o600); err != nil {
				t.Fatal(err)
			}
			takeQueue(h)
			park(h, waiting)
			h.donePending(false)
			if got := read(t, pending); len(got) != 0 {
				t.Errorf("the file holds %q, want nothing: the done call is parsed, and the waiting one has no line there", got)
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
		{"what an earlier run left stays even when the start couldn't look at the file, and only this run's calls are emptied", func(t *testing.T, dirs home.Dirs, pending string) {
			left := lineOf(t, newHost(dirs), call(t, "left"))
			if err := os.WriteFile(pending, left, 0o600); err != nil {
				t.Fatal(err)
			}
			h := newHost(dirs) // no recoverPending, as when its stat of the file failed
			h.pend(call(t, "new"))(true)
			takeQueue(h)
			h.donePending(false)
			h.donePending(true)
			if got := read(t, pending); !bytes.Equal(got, left) {
				t.Errorf("the pending file holds %q, want the earlier run's %q", got, left)
			}
		}},
		{"a move that fails still drops the calls held longer than seven days", func(t *testing.T, dirs home.Dirs, pending string) {
			earlier := newHost(dirs)
			old := call(t, "old")
			old.Received.Wall = time.Now().Add(-heldKeep - time.Hour)
			left := lineOf(t, earlier, call(t, "left"))
			if err := os.WriteFile(pending, slices.Concat(lineOf(t, earlier, old), left), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(dirs.Held(), 0o700); err != nil { // so held.jsonl can't be written
				t.Fatal(err)
			}
			h := newHost(dirs)
			h.recoverPending()
			if got := read(t, pending); !bytes.Equal(bytes.TrimSpace(got), bytes.TrimSpace(left)) {
				t.Errorf("the pending file holds %q, want the newer call alone", got)
			}
			if p := h.a.currentProblems()["adapters.dropped"]; !strings.Contains(p, "1 hook calls") {
				t.Errorf("adapters.dropped is %q", p)
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

// moved is call c as session sid made it in folder dir, renamed name, with
// edit applied to its payload.
func moved(t *testing.T, c adapters.Call, name, dir, sid string, edit func(map[string]any)) adapters.Call {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal(c.Stdin, &p); err != nil {
		t.Fatal(err)
	}
	p["cwd"], p["session_id"] = dir, sid
	if edit != nil {
		edit(p)
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	c.Dir, c.Stdin = name, b
	return c
}

// holdGit puts a git first on PATH that, run in folder dir, waits while
// the file it returns is there, as git walking a big repo's history would,
// and runs the real git otherwise. Removing the file lets it go on.
func holdGit(t *testing.T, dir string) string {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git isn't installed")
	}
	bin := t.TempDir()
	hold := filepath.Join(bin, "hold")
	script := "#!/bin/sh\ncase \"$(pwd -P)\" in\n'" + dir + "' | '" + dir + "'/*)\n" +
		"\twhile [ -e '" + hold + "' ]; do sleep 0.05; done\n\t;;\nesac\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hold, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(hold) })
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return hold
}

// TestFolderStillResolving checks D-park-unresolved in a real agent. While
// the first resolve of one session's folder is held back, that session's
// calls wait, a later one of them in a folder already known waits behind
// them, and another session's calls go on, its agent's commit included,
// which the watcher still takes for the agent's. Once the resolve ends, the
// waiting calls make their records in order, each with its own folder's
// project; at a stop before it ends, they make them without the held
// folder's.
func TestFolderStillResolving(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git isn't installed")
	}
	cases := []struct {
		name    string
		release bool // the held resolve ends before the agent stops
	}{
		{"once the held resolve ends, the waiting calls make their records with their folders' projects", true},
		{"at a stop before it ends, the waiting calls make their records, without the held folder's project", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			capture := readCapture(t)
			r := newHookReplay(t)
			slow, quick := filepath.Join(r.home, "slow"), r.folder()
			for _, dir := range []string{slow, quick} {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				runGit(t, dir, "init", "-q")
				// Each its own first commit, so the two repos' root commits,
				// and so their projects, differ.
				if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(filepath.Base(dir)+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				runGit(t, dir, "add", "README.md")
				runGit(t, dir, "commit", "-q", "-m", "first in "+filepath.Base(dir))
			}
			hold := holdGit(t, slow)

			const waiting = "aaaaaaaa-1111-4222-8333-444444444444" // the session that starts in the slow folder
			// Each call is made as it is sent, so its monotonic reading,
			// counted from base, never goes below the first's.
			base := time.Now().UTC()
			send := func(c adapters.Call) {
				c.Arrived = time.Now().UTC()
				r.send(t, event.HarnessClaudeCode, c, base)
			}
			a := startAgent(t, r.options([]keys.Store{}))
			send(moved(t, capture[0], "f00021", slow, waiting, nil))
			send(moved(t, capture[1], "f00022", quick, waiting, nil))
			send(moved(t, capture[0], "f00023", quick, captureSession, nil))
			waitStatus(t, a, func(s statusReply) bool {
				return s.Adapters.Git.Watched == 1 && s.Adapters.Harnesses[event.HarnessClaudeCode].Calls == 1
			})
			// The other session's agent commits in its folder, and its
			// PostToolUse comes as the command ends.
			if err := os.WriteFile(filepath.Join(quick, "b.txt"), []byte("b\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			runGit(t, quick, "add", "b.txt")
			runGit(t, quick, "commit", "-q", "-m", "by the agent")
			agentSHA := runGit(t, quick, "rev-parse", "HEAD")
			send(moved(t, capture[2], "f00024", quick, captureSession, func(p map[string]any) {
				input, _ := p["tool_input"].(map[string]any)
				input["command"] = `git commit -q -m "by the agent"`
				p["tool_use_id"], p["duration_ms"] = "toolu_test_commit", 4000
			}))
			during := waitStatus(t, a, func(s statusReply) bool {
				return s.Adapters.Git.Changes == 1 && s.Adapters.Harnesses[event.HarnessClaudeCode].Calls == 2
			})
			if c.release {
				os.Remove(hold)
				waitParsed(t, a, 4)
			}
			a.cancel()
			if err := a.waitExit(t); err != nil {
				t.Fatal(err)
			}
			os.Remove(hold)

			_, secret := deviceKeys(t, r.dirs, []keys.Store{})
			k := adapters.NewKeys(secret)
			hooked := readLedger(t, r.dirs, event.SourceHook)
			var mine, other []event.Event // the waiting session's records, and the other's
			firstMine, firstOther := -1, -1
			for i, rec := range hooked {
				switch rec.ev.SessionKey {
				case k.Session(waiting):
					mine = append(mine, rec.ev)
					if firstMine < 0 {
						firstMine = i
					}
				case k.Session(captureSession):
					other = append(other, rec.ev)
					if firstOther < 0 {
						firstOther = i
					}
				}
			}
			var change event.GitChange
			if changes := readLedger(t, r.dirs, event.SourceGit); len(changes) == 1 {
				if g, ok := changes[0].ev.Data.(*event.GitChange); ok {
					change = *g
				}
			}
			slowKey := ""
			if c.release {
				slowKey = projectKeyOf(t, r.dirs, []keys.Store{}, slow)
			}
			quickKey := projectKeyOf(t, r.dirs, []keys.Store{}, quick)
			checks := []struct {
				name string
				ok   bool
			}{
				{"while the resolve was held, the session's two calls waited, the one in a known folder too, and the other session's went on",
					during.Adapters.Waiting == 2},
				{"the agent's commit in the other folder, made meanwhile, is the agent's", change.SHA == agentSHA && change.Actor == event.ActorAgent},
				{"the other session's records carry its folder's project and come first in the ledger",
					len(other) == 2 && other[0].ProjectKey == quickKey && other[1].ProjectKey == quickKey && firstOther < firstMine},
				{"the waiting calls made their records in order, each with its own folder's project, none for a folder still resolving",
					len(mine) == 2 && mine[0].Kind() == event.KindSessionStart && mine[0].ProjectKey == slowKey &&
						mine[1].Kind() == event.KindPromptSubmit && mine[1].ProjectKey == quickKey},
			}
			if c.release {
				checks = append(checks, struct {
					name string
					ok   bool
				}{"the held folder's project is the one its repo gives", strings.HasPrefix(slowKey, "g") && slowKey != quickKey})
			}
			for _, ch := range checks {
				t.Run(ch.name, func(t *testing.T) {
					if !ch.ok {
						t.Errorf("no: waiting %d, change %+v, mine %+v, other %+v, slow %q, quick %q", during.Adapters.Waiting, change, mine, other, slowKey, quickKey)
					}
				})
			}
		})
	}
}

// TestMemorySavedWithItsRecords checks that the adapters' memory goes to
// state.json in the step that wrote the records it goes with, a session's
// start or a stop's scheduled prompts, and not up to a minute later, so a
// kill right after can't lose what the next prompt is read against. The
// agent runs throughout, and state.json is read well inside the minute job.
func TestMemorySavedWithItsRecords(t *testing.T) {
	// The part of the Claude Code adapter's saved memory the cases read.
	type session struct {
		HookStart bool `json:"hook_start"`
		Crons     []struct {
			Key string `json:"key"`
		} `json:"crons"`
	}
	type saved struct {
		Sessions []session `json:"sessions"`
	}
	cases := []struct {
		name    string
		through string // the last call sent, by its directory
		ok      func(m saved) bool
	}{
		{"a session's start from the walk is saved at once", "000001", func(m saved) bool {
			return slices.ContainsFunc(m.Sessions, func(s session) bool { return s.HookStart })
		}},
		{"a stop's scheduled prompt is saved at once, as its key", "000060", func(m saved) bool {
			for _, s := range m.Sessions {
				for _, c := range s.Crons {
					if strings.HasPrefix(c.Key, "c") {
						return true
					}
				}
			}
			return false
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			calls := readCapture(t)
			r := newHookReplay(t)
			started := time.Now()
			a := startAgent(t, r.options([]keys.Store{}))
			sent := uint64(0)
			for _, call := range calls {
				r.send(t, event.HarnessClaudeCode, call, calls[0].Arrived)
				sent++
				if call.Dir == c.through {
					break
				}
			}
			waitParsed(t, a, sent)
			deadline := time.Now().Add(5 * time.Second)
			for {
				var m saved
				if st, _, err := state.Open(r.dirs.StateFile()); err == nil {
					var mem adaptersState
					if ok, err := st.Get(adaptersKey, &mem); ok && err == nil {
						json.Unmarshal(mem.Adapters[event.HarnessClaudeCode], &m)
					}
				}
				if c.ok(m) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("state.json never held it; saved memory %+v", m)
				}
				time.Sleep(50 * time.Millisecond)
			}
			if time.Since(started) >= adaptersSaveEvery {
				t.Skip("the run took past the minute job, which saves too, so the check proves nothing")
			}
			a.cancel()
			if err := a.waitExit(t); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestHookCallsAfterTheWriterStopped checks that once the ledger writer
// has stopped after a failure, a hook call waits for the next start, in
// held.jsonl, rather than being parsed, refused and emptied out of the
// pending log, and that the next start, whose Open recovers the ledger,
// makes its records.
func TestHookCallsAfterTheWriterStopped(t *testing.T) {
	calls := readCapture(t)[:2] // the session's start, and its first prompt
	r := newHookReplay(t)
	opts := r.options([]keys.Store{})
	a := startAgent(t, opts)
	r.send(t, event.HarnessClaudeCode, calls[0], calls[0].Arrived)
	waitParsed(t, a, 1)
	writerFailedForTest.Store(true)
	t.Cleanup(func() { writerFailedForTest.Store(false) })
	r.send(t, event.HarnessClaudeCode, calls[1], calls[0].Arrived)
	during := waitStatus(t, a, func(s statusReply) bool { return s.Adapters.Waiting == 1 && s.Problems["adapters.ledger"] != "" })
	a.cancel()
	if err := a.waitExit(t); err != nil {
		t.Fatal(err)
	}
	held, err := os.ReadFile(r.dirs.Held())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	_, pendingErr := os.Stat(r.dirs.Held() + pendingSuffix)
	writerFailedForTest.Store(false)
	a = startAgent(t, opts)
	waitParsed(t, a, 1)
	a.cancel()
	if err := a.waitExit(t); err != nil {
		t.Fatal(err)
	}
	recs := readLedger(t, r.dirs, event.SourceHook)
	checks := []struct {
		name string
		ok   bool
	}{
		{"while the writer was down the call waited unparsed, and doctor heard why",
			during.Adapters.Waiting == 1 && during.Adapters.Harnesses[event.HarnessClaudeCode].Calls == 1},
		{"the stop left it in held.jsonl, and nothing in the pending log",
			bytes.Count(held, []byte(`"mac":`)) == 1 && errors.Is(pendingErr, fs.ErrNotExist)},
		{"the next start made its record once, after the session's start, via plugin as an earlier run's held call",
			len(recs) == 2 && recs[0].ev.Kind() == event.KindSessionStart && recs[1].ev.Kind() == event.KindPromptSubmit &&
				recs[1].ev.Via == event.ViaPlugin},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Errorf("no: status %+v, problems %v, held %q, pending %v, %d records", during.Adapters, during.Problems, held, pendingErr, len(recs))
			}
		})
	}
}

// TestReplayProblems checks that what doctor hears of held calls stays
// until it is no longer so: a later replay clears only its own problem,
// adapters.replay, never an undeletable file's or a count of lost calls.
func TestReplayProblems(t *testing.T) {
	newHost := func(t *testing.T) *adapterHost {
		dir := t.TempDir()
		return newAdapterHost(&agent{dirs: home.Dirs{Data: dir, Config: dir, Runtime: dir},
			log: slog.New(slog.DiscardHandler), problems: make(map[string]string)})
	}
	cases := []struct {
		name string
		run  func(t *testing.T, h *adapterHost)
	}{
		{"a .replay file that couldn't be deleted keeps adapters.held through the next replay, which stops for the run", func(t *testing.T, h *adapterHost) {
			if os.Geteuid() == 0 || runtime.GOOS == "windows" {
				t.Skip("needs a folder this user can't delete in, which root and Windows' modes don't give")
			}
			replay := h.a.dirs.Held() + ".replay"
			if err := os.WriteFile(replay, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(h.a.dirs.Data, 0o500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(h.a.dirs.Data, 0o700) })
			h.endReplay(replay)
			if !h.replayHeld(false) {
				t.Error("the replay after it isn't done for the run")
			}
			if h.a.currentProblems()["adapters.held"] == "" {
				t.Error("adapters.held is gone")
			}
		}},
		{"lost calls keep adapters.dropped through a replay that finds nothing held", func(t *testing.T, h *adapterHost) {
			h.dropHeld(2, errors.New("held.jsonl is full"))
			if !h.replayHeld(false) {
				t.Error("a replay with nothing held isn't done")
			}
			if !strings.Contains(h.a.currentProblems()["adapters.dropped"], "2 hook calls") {
				t.Errorf("adapters.dropped is %q", h.a.currentProblems()["adapters.dropped"])
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t, newHost(t)) })
	}
}

// TestHeldRetention checks D-held-calls-inbox-class's limits on the hook
// calls held.jsonl, its .replay and its pending log keep in plaintext: a
// start drops the ones held longer than heldKeep, and so does the running
// agent within a minute of their coming due, a ledger that can't open
// without a restore holds no more, and the file takes no call where others
// could read it.
func TestHeldRetention(t *testing.T) {
	now := time.Now().UTC()
	self := inbox.Proc{PID: 4242, Name: "pairtime", Start: now}
	newHost := func(t *testing.T) *adapterHost {
		dir := t.TempDir()
		return newAdapterHost(&agent{dirs: home.Dirs{Data: dir, Config: dir, Runtime: dir},
			log: slog.New(slog.DiscardHandler), problems: make(map[string]string)})
	}
	// call is a prompt the agent took in ago before now, with no stamp of
	// its own when unstamped.
	call := func(t *testing.T, prompt string, ago time.Duration, unstamped bool) inbox.Delivery {
		d := inbox.Delivery{Message: hookMessage(t, "UserPromptSubmit", `{"session_id":"s1","prompt":"`+prompt+`"}`, self, now.Add(-ago)),
			Via: inbox.ViaSocket, Received: clock.Stamp{Wall: now.Add(-ago)}}
		if unstamped {
			d.Received = clock.Stamp{}
		}
		return d
	}
	line := func(t *testing.T, h *adapterHost, d inbox.Delivery) []byte {
		t.Helper()
		b, err := h.heldLine(d)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	write := func(t *testing.T, name string, lines ...[]byte) {
		t.Helper()
		if err := os.WriteFile(name, slices.Concat(lines...), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// prompts lists the prompts of the calls in name, in order, of the ones
	// the cases use.
	prompts := func(t *testing.T, name string) []string {
		t.Helper()
		b, err := os.ReadFile(name)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, l := range bytes.Split(b, []byte("\n")) {
			for _, p := range []string{"old-1", "old-2", "new-1", "new-2", "unstamped"} {
				if bytes.Contains(l, []byte(`"prompt":"`+p+`"`)) {
					out = append(out, p)
				}
			}
		}
		return out
	}
	const week = 7 * 24 * time.Hour
	cases := []struct {
		name string
		run  func(t *testing.T, h *adapterHost)
	}{
		{"a start drops the calls held longer than seven days, keeps the rest in order, and doctor hears how many", func(t *testing.T, h *adapterHost) {
			write(t, h.a.dirs.Held(), line(t, h, call(t, "old-1", week+time.Hour, false)), line(t, h, call(t, "new-1", time.Hour, false)),
				line(t, h, call(t, "old-2", week+time.Minute, false)), line(t, h, call(t, "new-2", week-time.Hour, false)))
			h.expireHeld(now)
			if got := prompts(t, h.a.dirs.Held()); !slices.Equal(got, []string{"new-1", "new-2"}) {
				t.Errorf("held.jsonl holds %q, want new-1 and new-2", got)
			}
			if p := h.a.currentProblems()["adapters.dropped"]; !strings.Contains(p, "2 hook calls") {
				t.Errorf("adapters.dropped is %q", p)
			}
		}},
		{"a held file whose calls are all that old goes", func(t *testing.T, h *adapterHost) {
			write(t, h.a.dirs.Held(), line(t, h, call(t, "old-1", 30*24*time.Hour, false)))
			h.expireHeld(now)
			if _, err := os.Stat(h.a.dirs.Held()); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("held.jsonl is still there (%v)", err)
			}
		}},
		{"a .replay file a crash left is cut the same way", func(t *testing.T, h *adapterHost) {
			replay := h.a.dirs.Held() + ".replay"
			write(t, replay, line(t, h, call(t, "old-1", week+time.Hour, false)), line(t, h, call(t, "new-1", time.Hour, false)))
			h.expireHeld(now)
			if got := prompts(t, replay); !slices.Equal(got, []string{"new-1"}) {
				t.Errorf(".replay holds %q, want new-1", got)
			}
		}},
		{"a line that holds no call, or no stamp to go by, goes as well", func(t *testing.T, h *adapterHost) {
			write(t, h.a.dirs.Held(), []byte("\nnot a call\n"), line(t, h, call(t, "unstamped", time.Hour, true)), line(t, h, call(t, "new-1", time.Hour, false)))
			h.expireHeld(now)
			if got := prompts(t, h.a.dirs.Held()); !slices.Equal(got, []string{"new-1"}) {
				t.Errorf("held.jsonl holds %q, want new-1", got)
			}
		}},
		{"a start with nothing that old leaves held.jsonl as it is", func(t *testing.T, h *adapterHost) {
			want := line(t, h, call(t, "new-1", time.Hour, false))
			write(t, h.a.dirs.Held(), want)
			h.expireHeld(now)
			if got, err := os.ReadFile(h.a.dirs.Held()); err != nil || !bytes.Equal(got, want) {
				t.Errorf("held.jsonl holds %q (%v), want it untouched", got, err)
			}
			if p := h.a.currentProblems()["adapters.dropped"]; p != "" {
				t.Errorf("adapters.dropped is %q", p)
			}
		}},
		{"as the agent runs, expireRun drops a held call at its first look after the call's seventh day, and the status counts it no more", func(t *testing.T, h *adapterHost) {
			write(t, h.a.dirs.Held(), line(t, h, call(t, "new-1", time.Hour, false)), line(t, h, call(t, "new-2", time.Minute, false)))
			h.expireHeld(now)
			h.countHeld()
			h.expireRun(now.Add(week - 2*time.Hour))
			if got := prompts(t, h.a.dirs.Held()); !slices.Equal(got, []string{"new-1", "new-2"}) {
				t.Errorf("before either came due, held.jsonl holds %q, want both", got)
			}
			h.expireRun(now.Add(week - time.Hour + time.Minute))
			if got := prompts(t, h.a.dirs.Held()); !slices.Equal(got, []string{"new-2"}) {
				t.Errorf("held.jsonl holds %q, want new-2 alone", got)
			}
			if p := h.a.currentProblems()["adapters.dropped"]; !strings.Contains(p, "1 hook calls") {
				t.Errorf("adapters.dropped is %q", p)
			}
			if w := h.status().Waiting; w != 1 {
				t.Errorf("the status counts %d calls waiting, want 1", w)
			}
		}},
		{"the minute job has the next step drop a held call that has waited seven days, with no keys or ledger yet", func(t *testing.T, h *adapterHost) {
			if rest := h.hold([]inbox.Delivery{call(t, "old-1", week+time.Minute, false)}); len(rest) != 0 {
				t.Fatalf("hold kept %d calls back", len(rest))
			}
			h.minute()
			h.step(false)
			if _, err := os.Stat(h.a.dirs.Held()); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("held.jsonl is still there (%v)", err)
			}
		}},
		{"a call held as the agent runs comes due by its own stamp", func(t *testing.T, h *adapterHost) {
			if rest := h.hold([]inbox.Delivery{call(t, "new-1", time.Hour, false)}); len(rest) != 0 {
				t.Fatalf("hold kept %d calls back", len(rest))
			}
			h.expireRun(now.Add(week - time.Hour + time.Minute))
			if _, err := os.Stat(h.a.dirs.Held()); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("held.jsonl is still there (%v)", err)
			}
		}},
		{"as the agent runs, a .replay file whose replay has begun waits, and one whose replay hasn't is cut", func(t *testing.T, h *adapterHost) {
			replay := h.a.dirs.Held() + ".replay"
			write(t, replay, line(t, h, call(t, "new-1", time.Hour, false)))
			h.expireHeld(now)
			due := now.Add(week - time.Hour + time.Minute)
			h.replayAt = 1 // read in part, as after a read that failed
			h.expireRun(due)
			if got := prompts(t, replay); !slices.Equal(got, []string{"new-1"}) {
				t.Errorf("with its replay begun, .replay holds %q, want it as it was", got)
			}
			h.replayAt = 0
			h.expireRun(due.Add(heldRetry))
			if _, err := os.Stat(replay); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("with no replay begun, .replay is still there (%v)", err)
			}
		}},
		{"as the agent runs, a .replay file left half read behind a stopped ledger writer is cut, and the status stops counting only its unread calls", func(t *testing.T, h *adapterHost) {
			replay := h.a.dirs.Held() + ".replay"
			first := line(t, h, call(t, "new-1", time.Hour, false))
			write(t, replay, first, line(t, h, call(t, "new-2", 2*time.Hour, false)))
			write(t, h.a.dirs.Held(), line(t, h, call(t, "fresh", 0, false))) // held after the replay began, and not due
			h.expireHeld(now)
			h.countHeld()
			// The replay took the first call in, then the writer stopped, and
			// nothing in this run reads the file again.
			h.replayAt = int64(len(first))
			h.count(func() { h.held-- })
			h.down = true
			h.expireRun(now.Add(week - time.Hour + time.Minute))
			if _, err := os.Stat(replay); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf(".replay is still there (%v)", err)
			}
			if w := h.status().Waiting; w != 1 {
				t.Errorf("the status counts %d calls waiting, want the one in held.jsonl", w)
			}
		}},
		{"a .replay file cut twice behind a stopped ledger writer has the status stop counting each unread call as it goes, the first cut having moved where the replay stopped", func(t *testing.T, h *adapterHost) {
			replay := h.a.dirs.Held() + ".replay"
			first := line(t, h, call(t, "new-1", 2*time.Hour, false))
			write(t, replay, first, line(t, h, call(t, "new-2", time.Hour, false)))
			h.expireHeld(now)
			h.countHeld()
			// The replay took new-1 in, then the writer stopped.
			h.replayAt = int64(len(first))
			h.count(func() { h.held-- })
			h.down = true
			h.expireRun(now.Add(week - 2*time.Hour + time.Minute)) // new-1 goes
			if got := prompts(t, replay); !slices.Equal(got, []string{"new-2"}) {
				t.Errorf(".replay holds %q, want new-2 alone", got)
			}
			if w := h.status().Waiting; w != 1 {
				t.Errorf("after the first cut the status counts %d calls waiting, want new-2's", w)
			}
			h.expireRun(now.Add(week - time.Hour + time.Minute)) // new-2 goes
			if w := h.status().Waiting; w != 0 {
				t.Errorf("after the second cut the status counts %d calls waiting, want none", w)
			}
		}},
		{"a .replay file whose replay stops for the run, as when the ledger writer stops, is cut at the next minute job's look rather than an hour later", func(t *testing.T, h *adapterHost) {
			replay := h.a.dirs.Held() + ".replay"
			b := line(t, h, call(t, "new-1", time.Hour, false))
			write(t, replay, b)
			h.expireHeld(now)
			// Read to its end, with its call waiting for a slow folder.
			h.replayAt, h.replayRead = int64(len(b)), true
			due := now.Add(week - time.Hour + time.Minute)
			// Its replay is under way, so the first look leaves it.
			h.expireRun(due)
			// Then the writer stops, and the call stays in the file.
			h.holdBack(hostCall{replay: true})
			h.expireRun(due.Add(time.Minute))
			if _, err := os.Stat(replay); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf(".replay is still there (%v)", err)
			}
		}},
		{"as the agent runs, a .replay file stuck for the run is cut, and the status, which no longer counted its calls, doesn't change", func(t *testing.T, h *adapterHost) {
			replay := h.a.dirs.Held() + ".replay"
			b := slices.Concat(line(t, h, call(t, "new-1", time.Hour, false)), line(t, h, call(t, "new-2", time.Minute, false)))
			write(t, replay, b)
			if rest := h.hold([]inbox.Delivery{call(t, "held", 0, false)}); len(rest) != 0 { // a call held.jsonl holds for a later replay
				t.Fatalf("hold kept %d calls back", len(rest))
			}
			h.expireHeld(now)
			// The replay read it all, and the file couldn't be deleted.
			h.replayAt, h.replayStuck = int64(len(b)), true
			h.expireRun(now.Add(week - time.Hour + time.Minute))
			if got := prompts(t, replay); !slices.Equal(got, []string{"new-2"}) {
				t.Errorf(".replay holds %q, want new-2 alone", got)
			}
			if w := h.status().Waiting; w != 1 {
				t.Errorf("the status counts %d calls waiting, want the one in held.jsonl", w)
			}
		}},
		{"as the agent runs, what an earlier run left in the pending log goes at the log's next rewrite once due, and this run's lines stay", func(t *testing.T, h *adapterHost) {
			pending := h.a.dirs.Held() + pendingSuffix
			earlier := newAdapterHost(&agent{dirs: h.a.dirs, log: slog.New(slog.DiscardHandler), problems: make(map[string]string)})
			write(t, pending, line(t, earlier, call(t, "old-1", week-time.Hour, false)), line(t, earlier, call(t, "new-1", time.Hour, false)))
			if err := os.Mkdir(h.a.dirs.Held(), 0o700); err != nil { // so the start can't move them to held.jsonl
				t.Fatal(err)
			}
			h.recoverPending()
			mine := call(t, "new-2", 0, false)
			h.pend(mine)(true)
			h.mu.Lock()
			h.queue = nil
			h.mu.Unlock()
			// It waits for its folder's project, so its line stays too.
			h.parked = append(h.parked, hostCall{d: mine, live: true})
			h.count(func() { h.parkedN++; h.parkedLive++ })
			h.donePending(false)
			h.expireRun(now.Add(2 * time.Hour))
			h.donePending(false)
			if got := prompts(t, pending); !slices.Equal(got, []string{"new-1", "new-2"}) {
				t.Errorf("the pending log holds %q, want new-1, which the earlier run left, and new-2, this run's", got)
			}
			if p := h.a.currentProblems()["adapters.dropped"]; !strings.Contains(p, "1 hook calls") {
				t.Errorf("adapters.dropped is %q", p)
			}
		}},
		{"a start deletes what a rewrite cut short left", func(t *testing.T, h *adapterHost) {
			left := filepath.Join(h.a.dirs.Data, heldTmp+"123")
			write(t, left, line(t, h, call(t, "new-1", time.Hour, false)))
			h.expireHeld(now)
			if _, err := os.Stat(left); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("%s is still there (%v)", filepath.Base(left), err)
			}
		}},
		{"once the ledger can't open without a restore, a call is dropped rather than held, and doctor says how to get it back", func(t *testing.T, h *adapterHost) {
			h.giveUp()
			if rest := h.hold([]inbox.Delivery{call(t, "new-1", 0, false)}); len(rest) != 0 {
				t.Errorf("hold kept %d calls back", len(rest))
			}
			if _, err := os.Stat(h.a.dirs.Held()); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("held.jsonl is there (%v)", err)
			}
			if p := h.a.currentProblems()["adapters.held"]; !strings.Contains(p, "pairtime restore") || h.status().Dropped != 1 {
				t.Errorf("adapters.held is %q, dropped %d", p, h.status().Dropped)
			}
		}},
		{"a held.jsonl others could read takes no call", func(t *testing.T, h *adapterHost) {
			if runtime.GOOS == "windows" {
				t.Skip("the data directory's DACL decides on Windows")
			}
			write(t, h.a.dirs.Held())
			if err := os.Chmod(h.a.dirs.Held(), 0o644); err != nil {
				t.Fatal(err)
			}
			if rest := h.hold([]inbox.Delivery{call(t, "new-1", 0, false)}); len(rest) != 1 {
				t.Errorf("hold wrote the call, keeping back %d", len(rest))
			}
			if got := prompts(t, h.a.dirs.Held()); len(got) != 0 {
				t.Errorf("held.jsonl holds %q", got)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t, newHost(t)) })
	}
}

// TestIgnoreCountsAtOnce checks that a .pairtime-ignore made in a session's
// folder after the folder resolved keeps the session's next call out at
// once, by the few stats every call makes (gitHost.project), and not when
// the folder's answer is next looked at again.
func TestIgnoreCountsAtOnce(t *testing.T) {
	calls := readCapture(t)[:2] // the session's start and its first prompt
	r := newHookReplay(t)
	a := startAgent(t, r.options([]keys.Store{}))
	r.send(t, event.HarnessClaudeCode, calls[0], calls[0].Arrived)
	waitParsed(t, a, 1)
	if err := os.WriteFile(filepath.Join(r.folder(), git.IgnoreFile), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	r.send(t, event.HarnessClaudeCode, calls[1], calls[0].Arrived)
	s := waitParsed(t, a, 2)
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
		{"the prompt after the .pairtime-ignore counts as ignored", st.Ignored == 1},
		{"the ledger holds the session's start alone, from before it",
			len(recs) == 1 && recs[0].ev.Kind() == event.KindSessionStart},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Errorf("no: counts %+v, %d records", st, len(recs))
			}
		})
	}
}

// copyData copies the regular files under dir to a new folder, short enough
// for the socket's name, as a kill -9 of the agent leaves them: the kill
// takes the socket, the lock and the port with the process.
func copyData(t *testing.T, dir string) string {
	t.Helper()
	dst, err := os.MkdirTemp("", "pk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dst) })
	if dst, err = filepath.EvalSymlinks(dst); err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(to, 0o700)
		case !d.Type().IsRegular(), rel == "agent.lock", rel == "port.json":
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(to, b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// TestKillDuringSlowResolve checks DESIGN 9.7's "kill -9: no loss, no
// duplicate" while a folder's first resolve runs, which can take minutes
// in a big repo. The file the waiting calls came through then holds them
// alone, one line each, the pending log for calls taken in over the socket
// and the .replay file for calls an earlier run held, so a start from what
// a kill left makes their records once, and none of the calls the ledger
// already has again.
func TestKillDuringSlowResolve(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git isn't installed")
	}
	const waiting = "bbbbbbbb-1111-4222-8333-444444444444" // the session in the slow folder
	cases := []struct {
		name string
		held bool   // an earlier run held the calls, rather than the socket taking them in
		file string // the file the waiting calls are left in, beside held.jsonl
	}{
		{"calls the socket took in wait in the pending log alone, once each though one came twice", false, pendingSuffix},
		{"calls an earlier run held wait in the replay file alone, once each though one was held twice", true, ".replay"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			capture := readCapture(t)
			r := newHookReplay(t)
			slow, quick := filepath.Join(r.home, "slow"), r.folder()
			for _, dir := range []string{slow, quick} {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				runGit(t, dir, "init", "-q")
				// Each its own first commit, so the two repos' projects differ.
				if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(filepath.Base(dir)+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				runGit(t, dir, "add", "README.md")
				runGit(t, dir, "commit", "-q", "-m", "first in "+filepath.Base(dir))
			}
			hold := holdGit(t, slow)
			calls := []adapters.Call{
				moved(t, capture[0], "f00031", quick, captureSession, nil),
				moved(t, capture[0], "f00032", slow, waiting, nil),
				moved(t, capture[1], "f00033", slow, waiting, nil),
				moved(t, capture[1], "f00034", quick, captureSession, nil),
			}
			base := time.Now().UTC()
			if c.held {
				// As an earlier run left them in held.jsonl, stamped as they came.
				if err := os.MkdirAll(r.dirs.Data, 0o700); err != nil {
					t.Fatal(err)
				}
				earlier := newAdapterHost(&agent{dirs: r.dirs, log: slog.New(slog.DiscardHandler), problems: make(map[string]string)})
				var held []byte
				for i, call := range calls {
					call.Arrived = time.Now().UTC()
					d := adapters.DeliveryOf(event.HarnessClaudeCode, call, adapters.Walked, base)
					d.Received.Wall = call.Arrived
					line, err := earlier.heldLine(d)
					if err != nil {
						t.Fatal(err)
					}
					held = append(held, line...)
					if i == 1 {
						// A second line of a waiting call, as a killed run's
						// pending log leaves for a call the hook spooled too:
						// the replay takes it in once.
						held = append(held, line...)
					}
				}
				if err := os.WriteFile(r.dirs.Held(), held, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			a := startAgent(t, r.options([]keys.Store{}))
			if !c.held {
				send := func(call adapters.Call) {
					call.Arrived = time.Now().UTC()
					r.send(t, event.HarnessClaudeCode, call, base)
				}
				// The first, once parsed, says the agent has its keys and its
				// ledger, so the rest go through the pending log.
				send(calls[0])
				waitParsed(t, a, 1)
				for _, call := range calls[1:] {
					send(call)
				}
				// A second copy of a waiting call, as the spool's of one the
				// socket acked too late: the journal has it, and pend writes
				// its line all the same.
				send(calls[1])
			}
			waitStatus(t, a, func(s statusReply) bool {
				return s.Adapters.Harnesses[event.HarnessClaudeCode].Calls == 2 && s.Adapters.Waiting == 2
			})
			// The calls' message ids, as DeliveryOf makes them.
			id := func(dir string) []byte { return []byte(`"id":"` + strings.Repeat("0", 26) + dir + `"`) }
			name := r.dirs.Held() + c.file
			var left []byte
			deadline := time.Now().Add(5 * time.Second)
			for {
				left, _ = os.ReadFile(name)
				if bytes.Count(left, id("f00032")) == 1 && bytes.Count(left, id("f00033")) == 1 &&
					!bytes.Contains(left, id("f00031")) && !bytes.Contains(left, id("f00034")) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("%s never held the waiting calls alone, once each: %q", filepath.Base(name), left)
				}
				time.Sleep(50 * time.Millisecond)
			}
			// The kill: what the data directory holds now, before anything more.
			killed := copyData(t, r.dirs.Data)
			a.cancel()
			if err := a.waitExit(t); err != nil {
				t.Fatal(err)
			}
			os.Remove(hold)

			// The next start, from what the kill left.
			dirs := home.Dirs{Data: killed, Config: killed, Runtime: killed}
			b := startAgent(t, Options{Dirs: dirs, Ports: []int{0}, KeyStores: []keys.Store{}, Log: slog.New(slog.NewTextHandler(r.logs, nil))})
			waitParsed(t, b, 2)
			b.cancel()
			if err := b.waitExit(t); err != nil {
				t.Fatal(err)
			}
			_, secret := deviceKeys(t, dirs, []keys.Store{})
			k := adapters.NewKeys(secret)
			var mine, other []event.Event // the waiting session's records, and the other's
			for _, rec := range readLedger(t, dirs, event.SourceHook) {
				switch rec.ev.SessionKey {
				case k.Session(waiting):
					mine = append(mine, rec.ev)
				case k.Session(captureSession):
					other = append(other, rec.ev)
				}
			}
			kinds := func(evs []event.Event) []event.Kind {
				var out []event.Kind
				for _, e := range evs {
					out = append(out, e.Kind())
				}
				return out
			}
			both := []event.Kind{event.KindSessionStart, event.KindPromptSubmit}
			checks := []struct {
				name string
				ok   bool
			}{
				{"the calls parsed before the kill are in the ledger once, and not parsed again", slices.Equal(kinds(other), both)},
				{"the calls that waited for the slow folder are parsed once by the next start, via plugin as a killed run's calls",
					slices.Equal(kinds(mine), both) && !slices.ContainsFunc(mine, func(e event.Event) bool { return e.Via != event.ViaPlugin })},
			}
			for _, ch := range checks {
				t.Run(ch.name, func(t *testing.T) {
					if !ch.ok {
						t.Errorf("no: waiting session %v, other %v; %s at the kill %q", kinds(mine), kinds(other), filepath.Base(name), left)
					}
				})
			}
		})
	}
}

// TestParkedCallsKeepTheirOrder checks the gate on admit's release (#159,
// #164). A call that doesn't wait lets the parked calls that can go now go
// first, in the order they came. It walks them only once a Track has made a
// folder known, or park has made room in a way that can let a call go. The
// host has no ledger: its writer counts as stopped, so parse holds each call
// back in the order it reaches it (holdBack), and h.holding gives that
// order. The git host is built by hand. A folder marked inflight starts no
// Track, and the cases make it known the way track does, under fmu.
func TestParkedCallsKeepTheirOrder(t *testing.T) {
	at := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	self := inbox.Proc{PID: 4242, Name: "pairtime", Start: at}
	type env struct {
		h      *adapterHost
		g      *gitHost
		x, y   string            // two session folders
		labels map[string]string // each call's label, by its message id
	}
	newEnv := func(t *testing.T) *env {
		dir := t.TempDir()
		a := &agent{dirs: home.Dirs{Data: dir, Config: dir, Runtime: dir}, log: slog.New(slog.DiscardHandler), problems: make(map[string]string)}
		secret := make([]byte, hookpath.SessionSecretLen)
		a.kSession.Store(&secret)
		h := newAdapterHost(a)
		a.adaptOnce.Do(func() { a.adapt = h }) // so track's poke reaches h
		h.keys = &adapterKeys{feat: features.NewKey([features.KeySize]byte{})}
		h.down = true
		g := &gitHost{a: a, tracked: make(map[string]bool), folders: make(map[string]*folder)}
		h.git.Store(g)
		return &env{h: h, g: g, x: filepath.Join(dir, "x"), y: filepath.Join(dir, "y"), labels: make(map[string]string)}
	}
	// call is a prompt of session in folder dir, labelled.
	call := func(t *testing.T, e *env, label, session, dir string) inbox.Delivery {
		t.Helper()
		payload, err := json.Marshal(map[string]string{"session_id": session, "cwd": dir, "hook_event_name": "UserPromptSubmit", "prompt": label})
		if err != nil {
			t.Fatal(err)
		}
		d := inbox.Delivery{Message: hookMessage(t, "UserPromptSubmit", string(payload), self, at),
			Via: inbox.ViaSocket, Received: clock.Stamp{Wall: time.Now().UTC()}}
		e.labels[d.ID] = label
		return d
	}
	admit := func(e *env, ds ...inbox.Delivery) {
		for _, d := range ds {
			e.h.admit(d, false, false)
		}
	}
	// resolving has dir's first Track running, so known starts no other,
	// and resolved has dir known just now, with no Track to start.
	resolving := func(e *env, dir string) { e.g.folders[dir] = &folder{inflight: true} }
	resolved := func(e *env, dir string) { e.g.folders[dir] = &folder{known: true, at: time.Now()} }
	// end has dir's Track end as track does; counted says it moves the
	// count, as only track does.
	end := func(e *env, dir string, counted bool) {
		e.g.fmu.Lock()
		f := e.g.folders[dir]
		f.known, f.inflight, f.at = true, false, time.Now()
		if counted {
			e.g.ended++
		}
		e.g.fmu.Unlock()
	}
	// more parks a call of session C in dir, built by hand, and fill parks
	// them until maxParked calls wait, so the next more makes room.
	more := func(e *env, dir string) {
		e.h.park(hostCall{d: inbox.Delivery{Message: inbox.Message{ID: inbox.NewID()}}, session: "C", dir: dir, live: true})
	}
	fill := func(e *env, dir string) {
		for len(e.h.parked) < maxParked {
			more(e, dir)
		}
	}
	// order is the labels of the labelled calls parse reached, in order.
	order := func(e *env) []string {
		var out []string
		for _, d := range e.h.holding {
			if l, ok := e.labels[d.ID]; ok {
				out = append(out, l)
			}
		}
		return out
	}
	want := func(t *testing.T, e *env, labels ...string) {
		t.Helper()
		if got := order(e); !slices.Equal(got, labels) {
			t.Errorf("parse reached %q, want %q", got, labels)
		}
	}
	cases := []struct {
		name string
		run  func(t *testing.T, e *env)
	}{
		{"a Track that ends between two calls has the next call in its folder let the folder's parked calls go first", func(t *testing.T, e *env) {
			resolving(e, e.x)
			admit(e, call(t, e, "a1", "A", e.x), call(t, e, "a2", "A", e.x))
			end(e, e.x, true)
			admit(e, call(t, e, "b1", "B", e.x))
			want(t, e, "a1", "a2", "b1")
			if len(e.h.parked) != 0 {
				t.Errorf("%d calls still wait", len(e.h.parked))
			}
		}},
		{"a folder that turns known without a counted Track lets the next call go alone, with no walk of the parked calls", func(t *testing.T, e *env) {
			resolving(e, e.x)
			admit(e, call(t, e, "a1", "A", e.x), call(t, e, "a2", "A", e.x))
			end(e, e.x, false)
			admit(e, call(t, e, "b1", "B", e.x))
			want(t, e, "b1")
			if len(e.h.parked) != 2 {
				t.Errorf("%d calls still wait, want a1 and a2", len(e.h.parked))
			}
		}},
		{"at maxParked, making room by parsing the oldest call lets its session's next call go before a later call of another session in that call's folder", func(t *testing.T, e *env) {
			resolving(e, e.y)
			resolved(e, e.x)
			admit(e, call(t, e, "a1", "A", e.y), call(t, e, "a2", "A", e.x))
			fill(e, e.y)
			more(e, e.y)
			if !e.h.freed {
				t.Error("making room for a call that can go set no freed")
			}
			admit(e, call(t, e, "b1", "B", e.x))
			want(t, e, "a1", "a2", "b1")
		}},
		{"at maxParked, making room lets the call being parked go first when it is the session's next, before a later call of another session in its folder", func(t *testing.T, e *env) {
			resolving(e, e.y)
			resolved(e, e.x)
			admit(e, call(t, e, "a1", "A", e.y))
			fill(e, e.y)
			admit(e, call(t, e, "a2", "A", e.x))
			if !e.h.freed {
				t.Error("making room for the call being parked, which can go, set no freed")
			}
			admit(e, call(t, e, "b1", "B", e.x))
			want(t, e, "a1", "a2", "b1")
		}},
		{"at maxParked, making room frees nothing when the session's next call still waits for its folder", func(t *testing.T, e *env) {
			resolving(e, e.y)
			resolved(e, e.x)
			admit(e, call(t, e, "a1", "A", e.y), call(t, e, "a2", "A", e.y))
			fill(e, e.y)
			more(e, e.y)
			if e.h.freed {
				t.Error("making room set freed, though a2 still waits for its folder")
			}
			admit(e, call(t, e, "b1", "B", e.x))
			want(t, e, "a1", "b1")
		}},
		{"at maxParked, making room by parsing a call of no session frees nothing", func(t *testing.T, e *env) {
			resolving(e, e.y)
			e.h.park(hostCall{d: inbox.Delivery{Message: inbox.Message{ID: inbox.NewID()}}, dir: e.y, live: true})
			fill(e, e.y)
			more(e, e.y)
			if e.h.freed {
				t.Error("making room by parsing a call of no session set freed")
			}
		}},
		{"a Track that makes its folder known moves the count, and a later Track of the known folder doesn't", func(t *testing.T, e *env) {
			res, err := git.NewResolver(git.ResolverOptions{Secret: make([]byte, 32)}, git.ProjectMap{})
			if err != nil {
				t.Fatal(err)
			}
			w, err := git.NewWatcher(git.WatcherOptions{Resolver: res, Sink: func(git.Change) {}, NoNotify: true})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { w.Close() })
			e.g.res, e.g.w = res, w
			e.g.ctx, e.g.cancel = context.WithCancel(context.Background())
			t.Cleanup(e.g.cancel)
			// Never made, so each Track fails at its os.Stat, before any git
			// runs, and the folder is known with an error, which due Tracks
			// again at the next call.
			gone := filepath.Join(t.TempDir(), "gone")
			f := &folder{}
			e.g.folders[gone] = f
			for i := range 2 {
				e.g.fmu.Lock()
				f.inflight = true // as startLocked does
				e.g.running++
				e.g.fmu.Unlock()
				e.g.track(gone, f)
				if got := e.g.tracksEnded(); got != 1 {
					t.Errorf("after Track %d of the folder the count is %d, want 1", i+1, got)
				}
			}
			if !f.known || f.err == nil {
				t.Errorf("the folder is known: %v, with error %v; want known, with the Stat's error", f.known, f.err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t, newEnv(t)) })
	}
}
