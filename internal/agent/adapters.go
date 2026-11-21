// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters"
	_ "github.com/some-du6e/pairtime/internal/adapters/claudecode" // registers itself
	"github.com/some-du6e/pairtime/internal/agent/hookpath"
	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/sched"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/clock"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/ledger/features"
)

// adaptersKey is the adapters' key in state.json.
const adaptersKey = "adapters"

// adaptersSaveEvery is how often the adapters' memory, the git watcher's
// cursors and the project map go to state.json when they changed. A
// crash loses at most this much of it, and the ledger's own unsealed
// tail is about as long (BATCH_S).
const adaptersSaveEvery = time.Minute

// keyRetry is how long the adapters first wait before they try again to
// derive their keys when the device key couldn't be read for them, as after
// a dismissed keyring prompt, and they wait twice as long after each try
// that fails, up to ledgerRetryMax: each try may ask the person again.
const keyRetry = time.Minute

// maxQueued bounds the calls the worker keeps in memory when it can neither
// parse nor hold them, as while held.jsonl can't be written. The ones past
// it are counted as lost.
const maxQueued = 4096

// adapterHost runs the harness adapters inside the agent (WP-AG7; DESIGN
// 3.3, 3.4). The hook sink hands it every hook call it stored (hooks.go),
// and it does the rest on a goroutine of its own, off the hook's ack path:
// it turns the call into the raw record its harness's adapter reads
// (adapters.HookRecord), has the adapter parse it, and writes each record
// to the ledger with the content its record commits beside the event,
// never in an event field (ledger-v1 10.1, 10.6). A call an adapter can't
// fully read counts as a parse failure, for doctor, and its other records
// are still written (DESIGN 5.3). It also hosts the git adapter (git.go):
// each hooked session's folder becomes a project, the watcher watches its
// repo, and the session's project goes to focus (hookTies.setProject).
//
// Nothing is parsed before the keystore has opened and the ledger writer
// with it. An adapter keys session, turn, subagent and scheduled-prompt
// ids under K_session, file paths and commands under K_feat, and folders
// under K_project, and before the keystore opens the agent has none of
// them; a key made up meanwhile would be one anybody could compute. So
// until then every call waits, first in memory and then in held.jsonl in
// the data directory (home.Dirs.Held), and none is dropped while what is
// held stays under inbox.MaxSpool. The calls are parsed in the order the
// agent took them in once the keys and the writer are there, in this run
// or, after a stop, in the next.
//
// The journal keeps no bodies, so before the journal even records a call's
// id, pend appends the call to held.jsonl.pending with one write and no
// flush, as the journal writes its line: a call the socket acks survives
// the agent being killed before the worker parses or holds it, and one cut
// off before its id is recorded comes again through the spool (inbox.Sink). The worker empties the file
// once every call in it is parsed or held, and the next start parses what a
// killed run left there. A call whose records were written in the moment
// before a kill, ahead of the emptying, is parsed again: twice rather than
// never.
type adapterHost struct {
	a    *agent
	goos string

	mu      sync.Mutex
	queue   []inbox.Delivery // calls stored and not yet taken, oldest first
	keys    *adapterKeys     // from the keystore, once it opened (keyAdapters)
	ks      *keys.Keystore   // the keystore, to derive them again from
	writer  *ledger.Writer   // once the ledger is open (attach)
	closed  bool
	started bool
	stats   map[event.Harness]*harnessStats
	other   harnessStats // calls of a harness with no adapter
	held    int          // calls waiting in held.jsonl
	dropped uint64       // calls lost because held.jsonl was full
	saveDue bool
	// pending is held.jsonl.pending, open once pend has written to it, and
	// pendingSize how much it holds. pend writes it and the worker empties
	// it, both under mu.
	pending     *os.File
	pendingSize int64
	pendingBad  bool // the last write to it failed, and doctor heard so
	// pendingBase is how much of held.jsonl.pending an earlier run left
	// that this run couldn't move to held.jsonl (recoverPending): the file
	// is emptied only back to it, and kept for the next start.
	pendingBase int64
	// unsettled counts the calls pend wrote down that the hook sink hasn't
	// settled yet: the file isn't emptied under them.
	unsettled int
	retrying  bool // a goroutine derives the keys again (retryKeysLocked)

	kick chan struct{}
	stop chan struct{}
	done chan struct{}
	job  *sched.Job // the save job, once started

	// The rest belong to the worker goroutine, and to close once the
	// worker is gone.
	ready    bool  // the adapters and the git host are made
	replayed bool  // held.jsonl was read, and nothing has been held since
	replayAt int64 // how far into the .replay file the calls are parsed
	built    map[event.Harness]adapters.Adapter
	ignored  bool // the call being parsed ran in a folder out of pairtime
	memory   adaptersState
	lastSave []byte // the adapters' memory as last saved

	// git is the git adapter, which prepare makes; nil before, and when it
	// couldn't start. The status reads it too.
	git atomic.Pointer[gitHost]
}

// adapterKeys are the keys the adapters make records with, derived from
// the device key once the keystore opens (keyAdapters). K_session comes
// from the hook sink's own (agent.sessionSecret), so a session's records
// and the hosted_sessions focus lists name it alike.
type adapterKeys struct {
	feat    *features.Key // K_feat, as the ledger keys paths, commands and error signatures
	project []byte        // K_project, the git adapter's project secret
}

// harnessStats counts what one harness's calls made, for doctor and the
// dashboard's list of adapters (DESIGN 5.3).
type harnessStats struct {
	Calls   uint64 `json:"calls"`
	Records uint64 `json:"records"`
	// ParseFailures counts calls the adapter couldn't fully read: unknown
	// events and fields, payloads that aren't JSON, calls cut short.
	ParseFailures uint64 `json:"parse_failures"`
	// WriteFailures counts records the ledger writer refused.
	WriteFailures uint64 `json:"write_failures,omitempty"`
	// Ignored counts calls that ran in a folder out of pairtime, which
	// make no record (DESIGN 8.8).
	Ignored uint64 `json:"ignored,omitempty"`
	// LastRecord is the t of the newest record written, in Unix ms.
	LastRecord int64 `json:"last_record,omitempty"`
	// LastFailure says what the last failure was. It names events and
	// fields, never their values (adapters.ParseError).
	LastFailure string `json:"last_failure,omitempty"`
}

// adaptersReply is the adapters' part of GET /v1/status.
type adaptersReply struct {
	Harnesses map[event.Harness]harnessStats `json:"harnesses"`
	// Other counts calls of a harness pairtime has no adapter for.
	Other harnessStats `json:"other"`
	// Waiting counts the calls taken in that wait for the keystore and
	// the ledger, in memory and in held.jsonl.
	Waiting int `json:"waiting"`
	// Dropped counts calls lost because held.jsonl was full.
	Dropped uint64 `json:"dropped,omitempty"`
	// Git says what the git adapter watches.
	Git gitReply `json:"git"`
}

// adaptersState is what state.json keeps of the adapters: the memory of
// each adapter that has some (adapters.Stateful), by harness. Keyed names
// the K_session its keyed values were made under, so memory from under
// another device key, as after a restore, is left behind rather than
// mixed with keys of this one.
type adaptersState struct {
	Keyed    string                            `json:"keyed"`
	Adapters map[event.Harness]json.RawMessage `json:"adapters,omitempty"`
}

// memoryKey names the K_session a memory was saved under, as an opaque key
// of a fixed id: the session key no harness session can have.
func memoryKey(kSession []byte) string {
	return hookpath.SessionKey(kSession, "pairtime adapters' memory")
}

func newAdapterHost(a *agent) *adapterHost {
	return &adapterHost{
		a:     a,
		goos:  runtime.GOOS,
		stats: make(map[event.Harness]*harnessStats),
		built: make(map[event.Harness]adapters.Adapter),
		kick:  make(chan struct{}, 1),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
}

// adaptersHost returns the agent's adapter host, making it on first use:
// the hook sink, the ledger goroutine and startAdapters all reach it, in
// whichever order they run.
func (a *agent) adaptersHost() *adapterHost {
	a.adaptOnce.Do(func() { a.adapt = newAdapterHost(a) })
	return a.adapt
}

// startAdapters starts the adapters' goroutine and returns the step that
// stops it. That step runs after the socket and the inbox have stopped, so
// no call comes after it, and before the ledger closes: it writes the
// records of every call it still has, or holds them for the next run,
// stops the git watcher, whose held changes go to the ledger, and saves
// the adapters' memory, the watcher's cursors and the project map.
func (a *agent) startAdapters() func() {
	h := a.adaptersHost()
	h.recoverPending()
	h.mu.Lock()
	h.started = true
	h.mu.Unlock()
	go h.run()
	h.job = a.sched.Every("adapters.save", adaptersSaveEvery, func(time.Time) { h.saveSoon() })
	h.poke() // calls held by an earlier run
	return h.close
}

// keyAdapters derives the adapters' keys from the device key, for the
// adapter host, and keeps the keystore to derive them again from.
// openLedger calls it once the keystore has opened. WithDeviceKey reads
// K_dev from the OS's store each time, and when that fails a goroutine of
// the host's tries again (retryKeysLocked).
func (a *agent) keyAdapters(ks *keys.Keystore) {
	h := a.adaptersHost()
	k, err := deriveAdapterKeys(ks)
	h.mu.Lock()
	h.ks = ks
	if err == nil {
		h.keys = k
	}
	h.mu.Unlock()
	if err != nil {
		a.log.Warn("hook calls wait unparsed, because the adapters' keys couldn't be derived", "err", err)
		a.setProblem("adapters.keys", fmt.Sprintf("hook calls wait unparsed, because the device key couldn't be read for the adapters' keys, and the agent tries again: %v", err))
	}
}

// deriveAdapterKeys derives K_project from the device key and takes K_feat
// from the keystore's ledger keys.
func deriveAdapterKeys(ks *keys.Keystore) (*adapterKeys, error) {
	k := &adapterKeys{feat: ks.Keys().Features()}
	err := ks.WithDeviceKey(func(kDev []byte) error {
		var err error
		k.project, err = projectSecret(kDev)
		return err
	})
	if err != nil {
		return nil, err
	}
	return k, nil
}

// attach gives the host the ledger writer, once it is open (startLedger).
func (h *adapterHost) attach(w *ledger.Writer) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.writer = w
	h.mu.Unlock()
	h.poke()
}

// pend writes down a hook call the hook sink is about to store, before the
// journal records its id (hookSteps.pend), and returns the step that
// settles it once the journal has answered. Settled as stored, the call
// goes to the worker, which parses it, or holds it while the agent has no
// keys. Settled as not stored, a copy the journal had or one it couldn't
// store, only its line was written, and it goes when the worker empties
// the file. Neither blocks the hook. A call that comes after the host
// stopped, which the agent's stop order rules out, is held for the next
// run.
func (h *adapterHost) pend(d inbox.Delivery) func(stored bool) {
	line, err := heldLine(d)
	h.mu.Lock()
	closed := h.closed
	if !closed {
		if err == nil {
			err = h.writePendingLocked(line)
		}
		h.unsettled++
	}
	was := h.pendingBad
	h.pendingBad = err != nil
	h.mu.Unlock()
	switch {
	case closed:
	case err != nil:
		// The call goes on in memory alone.
		h.a.setProblem("adapters.pending", fmt.Sprintf("hook calls the agent took in can be lost if it is killed before it parses them, because it can't write them down: %v", err))
	case was:
		h.a.setProblem("adapters.pending", "")
	}
	return func(stored bool) {
		if closed {
			if stored {
				h.hold([]inbox.Delivery{d})
			}
			return
		}
		h.mu.Lock()
		h.unsettled--
		if stored {
			h.queue = append(h.queue, d)
		}
		h.mu.Unlock()
		h.poke()
	}
}

// pendingSuffix names held.jsonl.pending, beside held.jsonl.
const pendingSuffix = ".pending"

// writePendingLocked appends a call's line to held.jsonl.pending with one
// write, opening the file on first use. It is opened without O_APPEND,
// which on Windows gives a handle that can't truncate it; pend alone
// writes, so writes land at the end anyway. h.mu is held.
func (h *adapterHost) writePendingLocked(line []byte) error {
	if h.pending == nil {
		f, err := os.OpenFile(h.a.dirs.Held()+pendingSuffix, os.O_WRONLY|os.O_CREATE, 0o600)
		if err != nil {
			return err
		}
		end, err := f.Seek(0, io.SeekEnd)
		if err != nil {
			f.Close()
			return err
		}
		h.pending, h.pendingSize = f, end
	}
	if h.pendingSize+int64(len(line)) > inbox.MaxSpool {
		return errors.New("held.jsonl.pending is full")
	}
	n, err := h.pending.Write(line)
	h.pendingSize += int64(n)
	return err
}

// donePending empties held.jsonl.pending once every call pend wrote to it
// is parsed or held, which an empty queue and no call left to settle say
// under the lock pend writes with: back to what an earlier run left in it,
// pendingBase, which is zero but for a recovery that failed. At the last
// step it closes the file, and deletes it when nothing is left in it.
func (h *adapterHost) donePending(last bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pending == nil {
		return
	}
	idle := len(h.queue) == 0 && h.unsettled == 0
	if idle && h.pendingSize > h.pendingBase {
		if err := h.pending.Truncate(h.pendingBase); err != nil {
			h.a.log.Warn("couldn't empty held.jsonl.pending", "err", err)
			idle = false
		} else if _, err := h.pending.Seek(h.pendingBase, io.SeekStart); err != nil {
			h.a.log.Warn("couldn't empty held.jsonl.pending", "err", err)
			idle = false
		} else {
			h.pendingSize = h.pendingBase
		}
	}
	if last {
		h.pending.Close()
		h.pending = nil
		if idle && h.pendingBase == 0 {
			os.Remove(h.a.dirs.Held() + pendingSuffix)
		}
	}
}

// recoverPending moves what held.jsonl.pending holds, calls a run that was
// killed had acked and not yet parsed or held, to the end of held.jsonl,
// whose replay then parses them after the calls held before them.
// startAdapters calls it before the socket can deliver a call. When it
// can't, the calls stay in the file, which this run then empties only back
// to them (pendingBase), and the next start tries again.
func (h *adapterHost) recoverPending() {
	name := h.a.dirs.Held() + pendingSuffix
	fi, err := os.Stat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err == nil && fi.Size() == 0 {
		os.Remove(name)
		return
	}
	var copied bool
	if err == nil {
		var src *os.File
		if src, err = os.Open(name); err == nil {
			var dst *os.File
			if dst, err = os.OpenFile(h.a.dirs.Held(), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600); err == nil {
				// Each line starts with a newline (heldLine), so the calls
				// stay whole whatever held.jsonl ends with.
				_, err = io.Copy(dst, src)
				if cerr := dst.Close(); err == nil {
					err = cerr
				}
				copied = err == nil
			}
			src.Close()
		}
	}
	if copied {
		// Held now: the file goes, or at least stays empty, so no later
		// start holds its calls again.
		if err = os.Remove(name); err != nil && os.Truncate(name, 0) == nil {
			err = nil
		}
	}
	if err != nil {
		size := int64(0)
		if fi != nil {
			size = fi.Size()
		}
		h.pendingBase = size
		h.a.log.Warn("the hook calls an earlier run left pending couldn't be held", "err", err)
		h.a.setProblem("adapters.recover", fmt.Sprintf("hook calls a run that was killed had taken in wait in held.jsonl.pending, because they couldn't be moved to held.jsonl, and the next start tries again: %v", err))
	}
}

// saveSoon has the worker save state.json at its next step, as after a
// git change, whose cursor is best kept at once.
func (h *adapterHost) saveSoon() {
	h.mu.Lock()
	h.saveDue = true
	h.mu.Unlock()
	h.poke()
}

func (h *adapterHost) poke() {
	select {
	case h.kick <- struct{}{}:
	default:
	}
}

// close stops the worker, which finishes first (finish).
func (h *adapterHost) close() {
	h.mu.Lock()
	h.closed = true
	started := h.started
	h.mu.Unlock()
	if h.job != nil {
		h.job.Stop()
	}
	if started {
		close(h.stop)
		<-h.done
	}
}

func (h *adapterHost) run() {
	defer close(h.done)
	h.countHeld()
	for {
		select {
		case <-h.kick:
			select {
			case <-h.stop: // the stop goes first
				h.finish()
				return
			default:
			}
			h.step(false)
		case <-h.stop:
			h.finish()
			return
		}
	}
}

// finish is the worker's last step, at the agent's stop: it parses what it
// can and holds the rest, empties held.jsonl.pending, stops the git
// watcher, whose held changes go to the ledger first, and saves state.json.
func (h *adapterHost) finish() {
	h.step(true)
	h.donePending(true)
	if g := h.git.Load(); g != nil {
		g.close()
	}
	if h.ready {
		h.save(true)
	}
}

// step parses the calls taken since the last step, after the ones held
// before them, once the adapters can run, and holds them until then. On
// the last step, at the agent's stop, what can't be parsed is held for
// the next run.
func (h *adapterHost) step(last bool) {
	// The batch is taken after prepare, so calls that come while it works
	// go with it.
	ready := h.prepare()
	h.mu.Lock()
	batch := h.queue
	h.queue = nil
	saveDue := h.saveDue
	h.saveDue = false
	held := h.held
	h.mu.Unlock()

	if !ready {
		h.keep(h.hold(batch))
		h.donePending(false)
		return
	}
	if !h.replayed || held > 0 {
		if h.replayed = h.replayHeld(); !h.replayed {
			// The calls held before these come first: these are held
			// behind them, as while the agent has no keys.
			h.keep(h.hold(batch))
			h.donePending(false)
			h.a.setProblem("adapters.replay", "the hook calls held while the ledger wasn't open can't be read yet, and the calls after them wait behind them; the agent's log says why")
			return
		}
		h.a.setProblem("adapters.replay", "")
	}
	for _, d := range batch {
		h.handle(d)
	}
	h.donePending(false)
	if saveDue && !last {
		h.save(false)
	}
}

// prepare makes the adapters' surroundings once the keys and the writer
// are there, and reports whether the adapters can run: the git host, and
// the memory state.json kept of the adapters. It never makes an adapter
// before K_session is known: Keys under a secret anybody has would key
// every session, turn and subagent so a reader could test a guess. It never
// waits on the keyring either: when the keystore opened but reading K_dev
// for these keys failed, a goroutine of its own tries again
// (retryKeysLocked), so a step stays short and a stop never waits for a
// prompt.
func (h *adapterHost) prepare() bool {
	if h.ready {
		return true
	}
	h.mu.Lock()
	k, w := h.keys, h.writer
	keyed := k != nil && len(h.a.sessionSecret()) == hookpath.SessionSecretLen
	if !keyed {
		h.retryKeysLocked()
	}
	h.mu.Unlock()
	if w == nil || !keyed {
		return false
	}
	h.a.setProblem("adapters.keys", "")
	if g, err := newGitHost(h.a, k.project, w); err != nil {
		h.a.setProblem("git", fmt.Sprintf("no commit is recorded and no folder becomes a project, because the git adapter couldn't start: %v", err))
		h.a.log.Warn("the git adapter couldn't start", "err", err)
	} else {
		h.git.Store(g)
	}
	h.memory = h.loadMemory()
	h.ready = true
	return true
}

// retryKeysLocked starts, unless one runs, a goroutine that derives the
// keys the adapters lack again from the keystore: K_feat and K_project, and
// K_session for the hook sink and the adapters (keySessions). It waits
// keyRetry first and twice as long after each try that fails, up to
// ledgerRetryMax, gives up when the host stops, and pokes the worker once
// the keys are in. It does nothing before the keystore has opened. h.mu is
// held.
func (h *adapterHost) retryKeysLocked() {
	if h.retrying || h.closed || h.ks == nil {
		return
	}
	h.retrying = true
	ks := h.ks
	go func() {
		defer func() {
			h.mu.Lock()
			h.retrying = false
			h.mu.Unlock()
		}()
		for wait := keyRetry; ; wait = min(2*wait, ledgerRetryMax) {
			t := time.NewTimer(wait)
			select {
			case <-h.stop:
				t.Stop()
				return
			case <-t.C:
			}
			if h.deriveMissing(ks) {
				h.poke()
				return
			}
		}
	}()
}

// deriveMissing derives the keys the adapters lack, and reports whether
// they have them all now.
func (h *adapterHost) deriveMissing(ks *keys.Keystore) bool {
	h.mu.Lock()
	have := h.keys != nil
	h.mu.Unlock()
	if !have {
		k, err := deriveAdapterKeys(ks)
		if err != nil {
			h.a.setProblem("adapters.keys", fmt.Sprintf("hook calls wait unparsed, because the device key couldn't be read for the adapters' keys, and the agent tries again: %v", err))
			return false
		}
		h.mu.Lock()
		h.keys = k
		h.mu.Unlock()
	}
	if len(h.a.sessionSecret()) != hookpath.SessionSecretLen {
		h.a.keySessions(ks)
		if len(h.a.sessionSecret()) != hookpath.SessionSecretLen {
			h.a.setProblem("adapters.keys", "hook calls wait unparsed, because the device key couldn't be read for K_session, and the agent tries again")
			return false
		}
	}
	return true
}

// keep puts calls hold couldn't write back at the head of the queue, where
// held.jsonl.pending keeps them too, for the next step. Past maxQueued the
// newest are counted as lost.
func (h *adapterHost) keep(calls []inbox.Delivery) {
	if len(calls) == 0 {
		return
	}
	h.mu.Lock()
	h.queue = slices.Concat(calls, h.queue)
	lost := 0
	if len(h.queue) > maxQueued {
		lost = len(h.queue) - maxQueued
		h.queue = h.queue[:maxQueued]
	}
	h.mu.Unlock()
	if lost > 0 {
		h.dropHeld(lost, errors.New("held.jsonl can't be written, and too many calls wait in memory"))
	}
}

// loadMemory reads the adapters' memory from state.json, or none.
func (h *adapterHost) loadMemory() adaptersState {
	var st adaptersState
	if h.a.state == nil {
		return st
	}
	ok, err := h.a.state.Get(adaptersKey, &st)
	switch {
	case err != nil:
		h.a.log.Warn("the adapters start without their memory, because state.json's doesn't read", "err", err)
		return adaptersState{}
	case !ok:
		return adaptersState{}
	case st.Keyed != memoryKey(h.a.sessionSecret()):
		// Saved under another device key: its keys mean nothing now.
		h.a.log.Info("the adapters start without their memory, which another device key made")
		return adaptersState{}
	}
	return st
}

// adapter returns harness name's adapter, making it on first use, or nil
// when pairtime has none. Its keys are this installation's: K_session,
// K_feat, and the git adapter's projects.
func (h *adapterHost) adapter(name event.Harness) adapters.Adapter {
	if ad := h.built[name]; ad != nil {
		return ad
	}
	if !slices.Contains(adapters.Default.Harnesses(), name) {
		return nil
	}
	secret := h.a.sessionSecret()
	if len(secret) != hookpath.SessionSecretLen {
		return nil // prepare rules this out
	}
	h.mu.Lock()
	k := h.keys
	h.mu.Unlock()
	ad, ok := adapters.Default.New(name, adapters.Options{
		Keys:     adapters.NewKeys(secret),
		Features: k.feat,
		Project:  h.project,
	})
	if !ok || ad == nil {
		return nil
	}
	if st, ok := ad.(adapters.Stateful); ok {
		if raw := h.memory.Adapters[name]; len(raw) > 0 {
			if err := st.LoadState(raw); err != nil {
				h.a.log.Warn("an adapter starts without its memory", "harness", name, "err", err)
			}
		}
	}
	h.built[name] = ad
	return ad
}

// project is the adapters' project resolver (adapters.Options.Project):
// the git adapter's, which also starts watching the folder's repo. A
// folder out of pairtime marks the call being parsed, whose records the
// host then drops (DESIGN 8.8). Only the worker calls it, from Parse.
func (h *adapterHost) project(dir string) (adapters.Project, bool) {
	g := h.git.Load()
	if g == nil {
		return adapters.Project{}, false
	}
	p, ok, ignored := g.project(dir)
	if ignored {
		h.ignored = true
	}
	return p, ok
}

// handle parses one stored call and writes its records.
func (h *adapterHost) handle(d inbox.Delivery) {
	b, err := hookpath.DecodeBody(d.Message)
	name := event.Harness(b.Harness)
	var ad adapters.Adapter
	if err == nil {
		ad = h.adapter(name)
	}
	st := h.statsOf(name, ad != nil)
	// Counted once the call is done with, so a status that counts it
	// finds its records in the ledger writer.
	defer h.count(func() { st.Calls++ })
	switch {
	case err != nil:
		h.fail(st, "the call's body isn't a hook call's")
		return
	case ad == nil:
		h.fail(st, "pairtime has no adapter for the call's harness")
		return
	}
	raw, err := adapters.HookRecord(ad, h.goos, d)
	if err != nil {
		h.fail(st, err.Error())
		return
	}
	h.ignored = false
	recs, err := ad.Parse(raw)
	if err != nil && !errors.Is(err, adapters.ErrOtherHarness) {
		h.fail(st, err.Error())
	}
	if h.ignored {
		h.count(func() { st.Ignored++ })
		// Its session's window shows no project any more (focus_project).
		for _, r := range recs {
			if k := r.Event.SessionKey; k != "" && k != event.NoSession && h.a.hooked.dropProject(k) {
				h.a.refreshFocus()
			}
		}
		return
	}
	for _, r := range recs {
		h.write(st, r)
	}
}

// write writes one record to the ledger, with its content beside the
// event, and tells focus and the git watcher what it says.
func (h *adapterHost) write(st *harnessStats, r adapters.Record) {
	h.mu.Lock()
	w := h.writer
	h.mu.Unlock()
	ev := r.Event
	if ev.EventID == "" {
		ev.EventID = event.NewEventID(time.UnixMilli(ev.T))
	}
	if _, err := w.Write(ledger.Input{Event: ev, Text: r.Text, Edits: r.Edits, Command: r.Command}); err != nil {
		h.count(func() {
			st.WriteFailures++
			st.LastFailure = clip(fmt.Sprintf("the ledger refused a %s record: %v", ev.Kind(), err))
		})
		h.a.setProblem("adapters.ledger", fmt.Sprintf("the ledger refused a %s record of a hook call: %v", ev.Kind(), err))
		return
	}
	h.count(func() {
		st.Records++
		st.LastRecord = max(st.LastRecord, ev.T)
	})
	// focus_project: the window that hosts the session shows its project.
	if ev.SessionKey != "" && ev.SessionKey != event.NoSession && ev.ProjectKey != "" {
		if h.a.hooked.setProject(ev.SessionKey, ev.ProjectKey) {
			h.a.refreshFocus()
		}
	}
	if t, ok := ev.Data.(*event.AgentTool); ok && r.Command != "" && r.Dir != "" {
		if g := h.git.Load(); g != nil {
			g.noteCommand(r, t)
		}
	}
}

// statsOf returns the counts of harness name, or the ones of calls no
// adapter reads. Only harnesses with an adapter get counts of their own,
// so a sender that makes up names can't grow the map.
func (h *adapterHost) statsOf(name event.Harness, known bool) *harnessStats {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !known {
		return &h.other
	}
	st := h.stats[name]
	if st == nil {
		st = &harnessStats{}
		h.stats[name] = st
	}
	return st
}

// count changes counts under h.mu, which the status reads them under.
func (h *adapterHost) count(fn func()) {
	h.mu.Lock()
	fn()
	h.mu.Unlock()
}

func (h *adapterHost) fail(st *harnessStats, why string) {
	h.count(func() {
		st.ParseFailures++
		st.LastFailure = clip(why)
	})
}

// clip cuts a failure's description to what a status line shows.
func clip(s string) string {
	const most = 300
	if len(s) > most {
		s = strings.ToValidUTF8(s[:most], "")
	}
	return s
}

// status is the adapters' part of GET /v1/status.
func (h *adapterHost) status() adaptersReply {
	h.mu.Lock()
	r := adaptersReply{Harnesses: make(map[event.Harness]harnessStats, len(h.stats)), Other: h.other,
		Waiting: len(h.queue) + h.held, Dropped: h.dropped}
	for name, st := range h.stats {
		r.Harnesses[name] = *st
	}
	h.mu.Unlock()
	if g := h.git.Load(); g != nil {
		r.Git = g.status()
	}
	return r
}

// save puts the adapters' memory in state.json, with the git adapter's
// cursors and project map, and writes the file when any of it changed or
// force is set.
func (h *adapterHost) save(force bool) {
	if h.a.state == nil {
		return
	}
	secret := h.a.sessionSecret()
	st := adaptersState{Keyed: memoryKey(secret), Adapters: maps.Clone(h.memory.Adapters)}
	if st.Adapters == nil {
		st.Adapters = make(map[event.Harness]json.RawMessage)
	}
	for name, ad := range h.built {
		s, ok := ad.(adapters.Stateful)
		if !ok {
			continue
		}
		raw, err := s.SaveState()
		if err != nil {
			h.a.log.Warn("an adapter's memory couldn't be saved", "harness", name, "err", err)
			continue
		}
		st.Adapters[name] = raw
	}
	mem, err := json.Marshal(st)
	if err != nil {
		h.a.log.Warn("the adapters' memory couldn't be saved", "err", err)
		return
	}
	changed := !bytes.Equal(mem, h.lastSave)
	if changed {
		if err := h.a.state.Put(adaptersKey, json.RawMessage(mem)); err != nil {
			h.a.log.Warn("the adapters' memory couldn't be saved", "err", err)
			return
		}
	}
	if g := h.git.Load(); g != nil && g.put() {
		changed = true
	}
	if !changed && !force {
		return
	}
	if err := h.a.state.Save(); err != nil {
		h.a.log.Warn("couldn't save state.json", "err", err)
		return
	}
	h.lastSave = mem
	h.memory = st
}

// heldCall is a stored hook call as held.jsonl keeps it until the
// adapters can read it: the delivery whole, with the agent's own walk and
// stamp, so it parses as it would have when it came.
type heldCall struct {
	Message  json.RawMessage `json:"message"`
	Via      inbox.Via       `json:"via"`
	PeerPID  int             `json:"peer_pid,omitempty"`
	Walk     []platform.Proc `json:"walk,omitempty"`
	EventVia event.Via       `json:"event_via,omitempty"`
	Harness  event.Harness   `json:"harness,omitempty"`
	Received clock.Stamp     `json:"received"`
}

// hold appends calls to held.jsonl, which only this user can read (the
// data directory is theirs alone, home.Dirs.Create), with one write per
// call, and returns the ones it couldn't write, which the caller keeps
// (keep). A call that would take the file past inbox.MaxSpool is dropped,
// counted and reported instead: the ledger has then been shut for days, and
// filling the disk helps nobody.
func (h *adapterHost) hold(calls []inbox.Delivery) []inbox.Delivery {
	if len(calls) == 0 {
		return nil
	}
	name := h.a.dirs.Held()
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		h.a.log.Warn("hook calls that wait for the ledger couldn't be held, and wait in memory", "calls", len(calls), "err", err)
		return calls
	}
	defer f.Close()
	size := int64(0)
	if fi, err := f.Stat(); err == nil {
		size = fi.Size()
	}
	for i, d := range calls {
		line, err := heldLine(d)
		if err != nil {
			h.dropHeld(1, err) // can't happen: a message the agent took in encodes
			continue
		}
		if size+int64(len(line)) > inbox.MaxSpool {
			h.dropHeld(len(calls)-i, errors.New("held.jsonl is full"))
			return nil
		}
		if _, err := f.Write(line); err != nil {
			h.a.log.Warn("hook calls that wait for the ledger couldn't be held, and wait in memory", "calls", len(calls)-i, "err", err)
			return calls[i:]
		}
		size += int64(len(line))
		h.count(func() { h.held++ })
	}
	return nil
}

// heldLine is d as a line of held.jsonl, with a newline on each side.
func heldLine(d inbox.Delivery) ([]byte, error) {
	m, err := d.Message.Encode()
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(heldCall{Message: m, Via: d.Via, PeerPID: d.PeerPID, Walk: d.Walk,
		EventVia: d.EventVia, Harness: d.Harness, Received: d.Received})
	if err != nil {
		return nil, err
	}
	// A newline before it as well as after, as the inbox's spool has: a
	// line a full disk or a power cut left short then can't take the next
	// call with it.
	line := make([]byte, 0, len(b)+2)
	line = append(line, '\n')
	line = append(line, b...)
	return append(line, '\n'), nil
}

func (h *adapterHost) dropHeld(n int, err error) {
	h.mu.Lock()
	h.dropped += uint64(n)
	dropped := h.dropped
	h.mu.Unlock()
	h.a.log.Warn("hook calls that wait for the ledger couldn't be held", "calls", n, "err", err)
	h.a.setProblem("adapters.dropped", fmt.Sprintf("%d hook calls that waited for the ledger to open were lost: %v", dropped, err))
}

// replayHeld parses the calls held.jsonl holds, oldest first, then seals
// their records and deletes the file, and reports whether it is done. It
// renames the file first, so calls held meanwhile go to a new one, and a
// .replay file a crash left goes before it. A crash during a replay
// replays the file again at the next start, so its records that were
// sealed by then come out twice, never none.
func (h *adapterHost) replayHeld() bool {
	name := h.a.dirs.Held()
	replay := name + ".replay"
	for {
		if _, err := os.Stat(replay); errors.Is(err, fs.ErrNotExist) {
			if err := os.Rename(name, replay); err != nil {
				if !errors.Is(err, fs.ErrNotExist) {
					h.a.log.Warn("the held hook calls can't be read", "err", err)
					return false
				}
				return true // nothing is held
			}
		}
		read, gone := h.replayFile(replay)
		switch {
		case !read:
			return false // the file stays, and the next step tries again
		case !gone:
			return true // parsed, and left for the next start
		}
	}
}

// replayFile parses the held calls in name, seals their records and
// deletes the file. It reports whether it read the whole file, and whether
// the file is gone.
func (h *adapterHost) replayFile(name string) (read, gone bool) {
	f, err := os.Open(name)
	if err != nil {
		h.a.log.Warn("the held hook calls can't be read", "err", err)
		return false, false
	}
	// A read that failed partway resumes after the last call it parsed,
	// so none is parsed twice in a run.
	if _, err := f.Seek(h.replayAt, io.SeekStart); err != nil {
		h.a.log.Warn("the held hook calls can't be read", "err", err)
		f.Close()
		return false, false
	}
	n, bad := 0, 0
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			h.a.log.Warn("reading the held hook calls stopped early", "err", err)
			f.Close()
			h.count(func() { h.held = max(h.held-n-bad, 0) })
			return false, false
		}
		if len(bytes.TrimSpace(line)) > 0 {
			if d, ok := readHeld(line); ok {
				h.handle(d)
				n++
			} else {
				bad++
			}
		}
		h.replayAt += int64(len(line))
		if err != nil {
			break // EOF
		}
	}
	f.Close()
	h.mu.Lock()
	w := h.writer
	h.held = max(h.held-n-bad, 0)
	h.mu.Unlock()
	if n > 0 {
		// Their records are written; replaying the file again would write
		// them twice, so it goes even when the seal fails, and the next
		// seal takes them.
		if _, _, err := w.Seal(); err != nil {
			h.a.log.Warn("couldn't seal the held hook calls' records", "err", err)
		}
	}
	if n > 0 || bad > 0 {
		h.a.log.Info("parsed the hook calls held while the ledger wasn't open", "calls", n, "bad_lines", bad)
	}
	if err := os.Remove(name); err != nil {
		// Replayed again, it would write its records twice in this run;
		// left for the next start, it does at most once more.
		h.a.log.Warn("couldn't delete the held hook calls once replayed", "err", err)
		h.a.setProblem("adapters.held", fmt.Sprintf("the hook calls held while the ledger wasn't open were parsed, but couldn't be deleted, so the next start parses them again: %v", err))
		return true, false
	}
	h.replayAt = 0
	return true, true
}

// countHeld counts the calls an earlier run left held, for the status.
func (h *adapterHost) countHeld() {
	n := 0
	for _, name := range []string{h.a.dirs.Held() + ".replay", h.a.dirs.Held()} {
		f, err := os.Open(name)
		if err != nil {
			continue
		}
		r := bufio.NewReader(f)
		for {
			line, err := r.ReadBytes('\n')
			if len(bytes.TrimSpace(line)) > 0 {
				n++
			}
			if err != nil {
				break
			}
		}
		f.Close()
	}
	h.count(func() { h.held += n })
}

// readHeld reads a line of held.jsonl.
func readHeld(line []byte) (inbox.Delivery, bool) {
	var c heldCall
	if err := json.Unmarshal(line, &c); err != nil {
		return inbox.Delivery{}, false
	}
	m, err := inbox.Decode(c.Message)
	if err != nil {
		return inbox.Delivery{}, false
	}
	return inbox.Delivery{Message: m, Via: c.Via, PeerPID: c.PeerPID, Walk: c.Walk,
		EventVia: c.EventVia, Harness: c.Harness, Received: c.Received}, true
}
