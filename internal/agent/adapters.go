// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
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
// cursors and the project map go to state.json when they changed. A call
// whose records the memory goes with, a session's start, a prompt or a
// stop, has it saved at once instead, in the step that wrote them (parse):
// the writer seals a prompt and a stop at once, and by then has each record
// in its segment file, which a kill doesn't lose (ledger.Open seals what a
// crash left unsealed). So the memory a scheduled prompt or a queued
// message is read against is never older than the ledger by more than that
// step, and what a crash can lose of the rest is at most this much.
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

// heldKeep is how long a held hook call is kept: a start drops the ones
// taken in longer ago than this, unparsed (expireHeld; DESIGN 4.4, which
// gives every field a finite retention; D-held-calls-inbox-class).
const heldKeep = 7 * 24 * time.Hour

// maxParked bounds the calls that wait for their folder's project (park).
// Past it the oldest goes on without waiting more, and so without the
// project if its folder still has none.
const maxParked = 4096

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
// A call whose folder's project is still being worked out waits, parked
// in the order the calls came, until the git adapter's Track of the folder
// ends, and the calls of other folders go on (admit; D-park-unresolved).
// So no record goes without its project because a big repo's first
// resolve was slow, and no call waits for the resolver inside Parse, under
// the adapter's lock, while another session's git command needs the
// watcher to hear of it within its hold (git.DefaultHold). A folder whose
// Track gives up, and every folder at the agent's stop, gives its calls no
// project.
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
// or, after a stop, in the next. The held files have inbox/'s protections
// and a bound, since they hold whole payloads in plaintext: a call held
// longer than heldKeep goes at the next start, and none is held once the
// ledger can't open until a person runs pairtime restore
// (D-held-calls-inbox-class; hold, expireHeld).
//
// The journal keeps no bodies, so before the journal even records a call's
// id, pend appends the call to held.jsonl.pending with one write and no
// flush, as the journal writes its line: a call the socket acks survives
// the agent being killed before the worker parses or holds it, and one cut
// off before its id is recorded comes again through the spool (inbox.Sink).
// The worker empties the file once every call in it is parsed or held, and
// the next start parses what a killed run left there. A call whose records
// were written in the moment before a kill, ahead of the emptying, is
// parsed again: twice rather than never.
//
// A held call keeps how it came, its via, the agent's walk of its sender
// and the agent's stamp, only in the run that wrote it down. Any process
// of this user can write to the data directory, so every line carries an
// HMAC under a key the run makes at random and keeps in memory alone
// (heldLine), and a line without a valid one, which is every line an
// earlier run wrote, is read as the spool's lines are (D-spool-via; DESIGN
// 5.2): its events are via plugin, it has no walk, and the replay that
// reads it stamps it (readHeld). So a killed agent's acked calls, and calls
// held through a stop, come out as plugin calls.
type adapterHost struct {
	a    *agent
	goos string
	// lineKey keys the HMAC on each line this run writes to held.jsonl and
	// its pending log (heldLine). It is random, made with the host, and
	// never leaves memory, so no other run and no other process can make a
	// line that verifies.
	lineKey []byte

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
	// pendingBase is how much held.jsonl.pending held when this run first
	// opened it: what an earlier run left that this run couldn't move to
	// held.jsonl (recoverPending). The file is emptied only back to it, and
	// kept for the next start.
	pendingBase   int64
	pendingOpened bool // this run has opened held.jsonl.pending, and taken pendingBase
	// unsettled counts the calls pend wrote down that the hook sink hasn't
	// settled yet: the file isn't emptied under them.
	unsettled int
	retrying  bool // a goroutine derives the keys again (retryKeysLocked)
	// gaveUp says the ledger can't open until a person runs pairtime
	// restore (ledgerGivesUp), so hold keeps no more calls (giveUp).
	gaveUp bool
	// parkedN counts the calls that wait for their folder's project
	// (parked), and parkedLive those of them held.jsonl.pending keeps: the
	// file isn't emptied under them either.
	parkedN, parkedLive int

	kick chan struct{}
	stop chan struct{}
	done chan struct{}
	job  *sched.Job // the save job, once started

	// The rest belong to the worker goroutine, and to close once the
	// worker is gone.
	ready    bool // the adapters and the git host are made
	down     bool // the ledger writer stopped after a failure (writerDown)
	replayed bool // held.jsonl was read, and nothing has been held since
	// The .replay file's calls: replayAt is how far into it they are read,
	// replayRead says they are read to its end, replayTaken counts them,
	// and replayOut counts those still parked, before whose records the file
	// can't go (endReplay). replayStuck says it couldn't be deleted, and
	// stays for the next start.
	replayAt                int64
	replayRead, replayStuck bool
	replayTaken, replayOut  int
	built                   map[event.Harness]adapters.Adapter
	ignored                 bool // the call being parsed ran in a folder out of pairtime
	memory                  adaptersState
	lastSave                []byte // the adapters' memory as last saved
	// parked are the calls that wait for their folder's project, in the
	// order they came, and parkedBy counts them by session, whose later
	// calls wait behind them (park).
	parked   []hostCall
	parkedBy map[string]int
	// holding are the calls holdBack keeps for the next start, which the
	// step holds together (holdBackNow).
	holding []inbox.Delivery

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
	// Waiting counts the calls taken in that wait to be parsed: for the
	// keystore and the ledger, in memory and in held.jsonl, or for their
	// folder's project (adapterHost.park).
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
// over a domain of its own (adapters.Keys.Memory; ledger-v1 3), which no
// session, turn, subagent or scheduled prompt key can equal. It is "" for a
// K_session the agent doesn't have yet.
func memoryKey(kSession []byte) string {
	if len(kSession) != hookpath.SessionSecretLen {
		return ""
	}
	return adapters.NewKeys(kSession).Memory()
}

func newAdapterHost(a *agent) *adapterHost {
	key := make([]byte, 32)
	rand.Read(key) // never fails: crypto/rand panics instead
	return &adapterHost{
		a:        a,
		goos:     runtime.GOOS,
		lineKey:  key,
		stats:    make(map[event.Harness]*harnessStats),
		built:    make(map[event.Harness]adapters.Adapter),
		parkedBy: make(map[string]int),
		kick:     make(chan struct{}, 1),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
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
	line, err := h.heldLine(d)
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
		f, err := inbox.OpenPrivate(h.a.dirs.Held()+pendingSuffix, os.O_WRONLY|os.O_CREATE, 0o600)
		if err != nil {
			return err
		}
		end, err := f.Seek(0, io.SeekEnd)
		if err != nil {
			f.Close()
			return err
		}
		// What the file holds before this run's first write, an earlier
		// run left. recoverPending moved it and emptied the file, or it
		// couldn't move it, or even look at it, and then it stays for the
		// next start. A later open, after keepParkedLocked, finds this
		// run's lines there too, which aren't the earlier run's.
		if !h.pendingOpened {
			h.pendingBase, h.pendingOpened = max(h.pendingBase, end), true
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
// is parsed or held, which an empty queue, no call left to settle and none
// waiting for its folder's project say under the lock pend writes with. It
// empties it back to what an earlier run left in it, pendingBase, which is
// zero but for a recovery that failed. While calls wait for their folder's
// project and nothing else is outstanding, it keeps theirs alone
// (keepParkedLocked). At the last step it closes the file, and deletes it
// when nothing is left in it.
func (h *adapterHost) donePending(last bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pending == nil {
		return
	}
	settled := len(h.queue) == 0 && h.unsettled == 0
	idle := settled && h.parkedLive == 0
	if settled && h.parkedLive > 0 {
		if h.keepParkedLocked(); h.pending == nil {
			return // it couldn't open the file again; the next pend does
		}
	}
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

// keepParkedLocked leaves held.jsonl.pending holding what an earlier run
// left, up to pendingBase, and the lines of the calls that wait for their
// folder's project (parked) and nothing else, once every other call in it
// is parsed or held. A big repo's first walk can take minutes, and a kill
// meanwhile then has the next start parse those calls alone, none that the
// ledger has already (DESIGN 9.7: kill -9 loses nothing and duplicates
// nothing). It rewrites the file only when it holds more than that, and
// through inbox.Rewrite, so a kill at any moment leaves one whole file or
// the other; the file is closed for the rename and opened again after it.
// h.mu is held, so pend waits meanwhile, and only the worker calls it, which
// owns parked.
func (h *adapterHost) keepParkedLocked() {
	want := h.pendingBase
	ids := make(map[string]bool)
	for _, c := range h.parked {
		if !c.live {
			continue
		}
		line, err := h.heldLine(c.d)
		if err != nil {
			return // can't happen: pend wrote the same line
		}
		want += int64(len(line))
		ids[c.d.ID] = true
	}
	if h.pendingSize <= want {
		return // nothing in it but what has to stay
	}
	name := h.a.dirs.Held() + pendingSuffix
	h.pending.Close() // Windows renames nothing over an open file
	h.pending = nil
	base := h.pendingBase
	if _, err := inbox.Rewrite(name, heldTmp, func(line []byte, at int64) bool {
		if at < base {
			return true
		}
		id, ok := heldID(line)
		return ok && ids[id]
	}); err != nil {
		h.a.log.Warn("couldn't drop the parsed calls from held.jsonl.pending", "err", err)
	}
	f, err := inbox.OpenPrivate(name, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		// The next pend opens it again (writePendingLocked).
		h.a.log.Warn("couldn't open held.jsonl.pending again", "err", err)
		return
	}
	end, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		f.Close()
		h.a.log.Warn("couldn't open held.jsonl.pending again", "err", err)
		return
	}
	h.pending, h.pendingSize = f, end
}

// heldID is the id of the message on a line of held.jsonl or its pending
// log, which pend and the journal both key the call by.
func heldID(line []byte) (string, bool) {
	var e struct {
		Call struct {
			Message struct {
				ID string `json:"id"`
			} `json:"message"`
		} `json:"call"`
	}
	if err := json.Unmarshal(line, &e); err != nil || e.Call.Message.ID == "" {
		return "", false
	}
	return e.Call.Message.ID, true
}

// recoverPending moves what held.jsonl.pending holds, calls a run that was
// killed had acked and not yet parsed or held, to the end of held.jsonl,
// whose replay then parses them after the calls held before them.
// startAdapters calls it before the socket can deliver a call. When it
// can't, the calls stay in the file but for those held longer than
// heldKeep, this run empties it only back to them (pendingBase), and the
// next start tries again.
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
		if src, err = inbox.OpenPrivate(name, os.O_RDONLY, 0); err == nil {
			var dst *os.File
			if dst, err = inbox.OpenPrivate(h.a.dirs.Held(), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600); err == nil {
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
		h.a.log.Warn("the hook calls an earlier run left pending couldn't be held", "err", err)
		h.a.setProblem("adapters.recover", fmt.Sprintf("hook calls a run that was killed had taken in wait in held.jsonl.pending, because they couldn't be moved to held.jsonl, and the next start tries again: %v", err))
		// They stay, but no longer than held calls do (heldKeep). Nothing
		// has the file open yet, so it can be rewritten; the run's first
		// pend then takes what is left as what it mustn't empty
		// (writePendingLocked).
		if n := h.expireFile(name, time.Now().Add(-heldKeep)); n > 0 {
			h.dropHeld(n, fmt.Errorf("they were held longer than %v, which is as long as hook calls are kept", heldKeep))
		}
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
	h.expireHeld(time.Now())
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
	if g := h.git.Load(); g != nil {
		g.close()
	}
	if h.ready && !h.down {
		h.save(true)
	}
	// Last: a kill before the save leaves the calls whose memory it holds
	// in held.jsonl.pending for the next start.
	h.donePending(true)
}

// step parses the calls taken since the last step, after the ones held
// before them, once the adapters can run, and holds them until then. The
// calls that wait for their folder's project go first, as their folders'
// projects become known (release). On the last step, at the agent's stop,
// nothing waits for a project any more, and what can't be parsed is held
// for the next run.
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
	if h.writerDown() {
		// Nothing goes to the ledger until the next start's Open recovers
		// it: the calls that wait for their folders, then these, wait for
		// that start in held.jsonl, in the order they came.
		h.release(true)
		h.holding = append(h.holding, batch...)
		h.holdBackNow()
		if !last {
			h.donePending(false)
		}
		return
	}
	h.release(last)
	if !h.replayed || held > 0 || h.replayRead {
		if h.replayed = h.replayHeld(last); !h.replayed {
			// The calls held before these come first: these are held
			// behind them, as while the agent has no keys.
			h.keep(h.hold(batch))
			batch = nil
		}
	}
	for _, d := range batch {
		h.admit(d, false, last)
	}
	h.holdBackNow() // the calls the writer stopped under, if it did
	// Read again: the calls parsed above can have made a save due.
	h.mu.Lock()
	saveDue = saveDue || h.saveDue
	h.saveDue = false
	h.mu.Unlock()
	if saveDue && !last && !h.down {
		h.save(false)
	}
	// Only once the memory they made is saved does held.jsonl.pending let
	// go of the calls, so a kill in between parses them again rather than
	// leave the ledger ahead of state.json (adaptersSaveEvery). The last
	// step leaves it to finish, after its own save.
	if !last {
		h.donePending(false)
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
// the answer the git adapter's last Track of the folder gave, which also
// started watching its repo, and which admit waited for. It never waits
// itself. A folder out of pairtime marks the call being parsed, whose
// records the host then drops (DESIGN 8.8). Only the worker calls it, from
// Parse.
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

// hostCall is a stored hook call on its way to its adapter, as read made
// it ready for Parse.
type hostCall struct {
	d   inbox.Delivery // the call as stored, which holdBack holds again
	st  *harnessStats
	ad  adapters.Adapter
	raw adapters.RawRecord
	// dir is the folder Parse resolves to a project (adapters.Placer), ""
	// for none, and session the harness's own session id, which orders the
	// session's calls. Both stay in memory.
	dir, session string
	// replay says the call came from the .replay file, which keeps it until
	// it is parsed (endReplay), and live that held.jsonl.pending keeps it
	// (donePending).
	replay, live bool
}

// read makes a stored call ready for its adapter: the adapter of its
// harness, the raw record HookRecord makes of it, and its folder and
// session. A call it can't take that far is counted as done, a failure,
// and read reports false.
func (h *adapterHost) read(d inbox.Delivery) (hostCall, bool) {
	b, err := hookpath.DecodeBody(d.Message)
	name := event.Harness(b.Harness)
	var ad adapters.Adapter
	if err == nil {
		ad = h.adapter(name)
	}
	c := hostCall{d: d, st: h.statsOf(name, ad != nil), ad: ad}
	switch {
	case err != nil:
		h.fail(c.st, "the call's body isn't a hook call's")
	case ad == nil:
		h.fail(c.st, "pairtime has no adapter for the call's harness")
	default:
		if c.raw, err = adapters.HookRecord(ad, h.goos, d); err != nil {
			h.fail(c.st, err.Error())
			break
		}
		if p, ok := ad.(adapters.Placer); ok {
			c.dir = p.Folder(c.raw)
		}
		c.session = hookpath.SessionID(b)
		return c, true
	}
	h.count(func() { c.st.Calls++ })
	return hostCall{}, false
}

// admit takes a stored call to its adapter (D-park-unresolved). It parses
// the call at once, unless the project of the call's folder is still being
// worked out (gitHost.known) or an earlier call of its session waits; the
// call then waits too, parked behind them, until release parses it. So the
// calls of other folders go on, and none is parsed without its project
// because a resolve was slow. On the last step nothing waits. replay says
// the call came from the .replay file.
func (h *adapterHost) admit(d inbox.Delivery, replay, last bool) {
	c, ok := h.read(d)
	if !ok {
		return
	}
	c.replay, c.live = replay, !replay
	if !last && (c.session != "" && h.parkedBy[c.session] > 0 || h.waits(c.dir)) {
		h.park(c)
		return
	}
	h.parse(c)
}

// waits reports whether the calls of folder dir wait for its project:
// never for no folder, or with no git adapter, which resolves none.
func (h *adapterHost) waits(dir string) bool {
	g := h.git.Load()
	return dir != "" && g != nil && !g.known(dir)
}

// park puts c behind the calls that wait already. Past maxParked, the
// oldest of those goes on first, without waiting more.
func (h *adapterHost) park(c hostCall) {
	if len(h.parked) >= maxParked {
		first := h.parked[0]
		h.parked[0] = hostCall{}
		h.parked = h.parked[1:]
		h.parse(first)
		h.unparked(first)
	}
	h.parked = append(h.parked, c)
	if c.session != "" {
		h.parkedBy[c.session]++
	}
	if c.replay {
		h.replayOut++
	}
	h.count(func() {
		h.parkedN++
		if c.live {
			h.parkedLive++
		}
	})
}

// unparked counts c, which waited, as gone from the calls that wait.
func (h *adapterHost) unparked(c hostCall) {
	if c.session != "" {
		if h.parkedBy[c.session]--; h.parkedBy[c.session] <= 0 {
			delete(h.parkedBy, c.session)
		}
	}
	if c.replay {
		h.replayOut--
	}
	h.count(func() {
		h.parkedN--
		if c.live {
			h.parkedLive--
		}
	})
}

// release parses the calls that wait and can go now, in the order they
// came: each whose folder's project is known, unless an earlier call of
// its session still waits. On the last step it parses every one, and a
// folder still resolving then gives its calls no project, as one whose
// resolve timed out does.
func (h *adapterHost) release(last bool) {
	if len(h.parked) == 0 {
		return
	}
	held := make(map[string]bool)
	var ready []hostCall
	kept := h.parked[:0]
	for _, c := range h.parked {
		if !last && (c.session != "" && held[c.session] || h.waits(c.dir)) {
			if c.session != "" {
				held[c.session] = true
			}
			kept = append(kept, c)
			continue
		}
		ready = append(ready, c)
	}
	clear(h.parked[len(kept):])
	h.parked = kept
	for _, c := range ready {
		h.parse(c)
		h.unparked(c)
	}
}

// parse has c's adapter parse it, and writes its records. Once the ledger
// writer has stopped after a failure, it holds c for the next start
// instead, before Parse, so the adapter's memory doesn't move on past a
// call whose records the ledger never took (writerDown). A call during
// which the writer stopped is held too, so the records of it that went
// before the stop come out twice, rather than the rest never. A call is
// counted once it is done with, so a status that counts it finds its
// records in the ledger writer.
func (h *adapterHost) parse(c hostCall) {
	if h.writerDown() {
		h.holdBack(c)
		return
	}
	h.ignored = false
	recs, err := c.ad.Parse(c.raw)
	if err != nil && !errors.Is(err, adapters.ErrOtherHarness) {
		h.fail(c.st, err.Error())
	}
	session := hookpath.SessionKey(h.a.sessionSecret(), c.session)
	if h.ignored {
		h.count(func() { c.st.Calls++; c.st.Ignored++ })
		// Its session is out of focus from now on: no window hosts it or
		// shows its project, and presence hears none of its calls, so no
		// os.focus record names it (hookTies.ignore).
		if h.a.hooked.ignore(session) {
			h.a.refreshFocus()
		}
		return
	}
	// A call that does count lets its session back in, as when it moved to
	// a folder that isn't out of pairtime.
	h.a.hooked.unignore(session)
	due, refused := false, false
	for _, r := range recs {
		if !h.write(c.st, r) {
			refused = true
		}
		switch r.Event.Kind() {
		case event.KindSessionStart, event.KindPromptSubmit, event.KindAgentStop:
			due = true
		}
	}
	if refused && h.writerDown() {
		h.holdBack(c)
		return
	}
	h.mu.Lock()
	c.st.Calls++
	// The memory these records go with, a session's start from the walk,
	// a turn's id or a stop's scheduled prompts, goes to state.json with
	// them (adaptersSaveEvery).
	h.saveDue = h.saveDue || due
	h.mu.Unlock()
}

// writerFailedForTest, when set, makes writerDown take the ledger writer
// for one that stopped after a failure, which outside tests only an I/O
// error such as a failed flush does.
var writerFailedForTest atomic.Bool

// writerDown reports whether the ledger writer has stopped after a
// failure: it refuses every record from then on, and only the next
// start's Open recovers it (ledger.Writer.Status). Doctor hears once. The
// adapters then hold every call for that start, and save nothing more to
// state.json, so the next start goes on from the memory, cursors and
// project map that match what the ledger has.
func (h *adapterHost) writerDown() bool {
	if h.down {
		return true
	}
	h.mu.Lock()
	w := h.writer
	h.mu.Unlock()
	if w == nil {
		return false
	}
	failed := w.Status().Failed
	if failed == "" && writerFailedForTest.Load() {
		failed = "a test stopped it"
	}
	if failed == "" {
		return false
	}
	h.down = true
	h.a.log.Warn("hook calls wait for the next start, because the ledger writer stopped after a failure", "failed", failed)
	h.a.setProblem("adapters.ledger", "hook calls wait in held.jsonl for the agent's next start, because the ledger writer stopped after a failure; the agent's log says why")
	return true
}

// holdBack keeps c for the next start, once the writer is down: a call
// held.jsonl.pending keeps goes to held.jsonl, behind the calls held
// before it, and one read from the .replay file stays there, which this run
// no longer deletes. The step holds them all at once, in the order they
// came (holdBackNow), so a write that fails for one can't put a later one
// ahead of it.
func (h *adapterHost) holdBack(c hostCall) {
	if c.replay {
		h.replayStuck = true
		return
	}
	h.holding = append(h.holding, c.d)
}

// holdBackNow holds what holdBack kept, in order, and keeps whatever it
// couldn't write at the head of the queue, still in order (keep).
func (h *adapterHost) holdBackNow() {
	if len(h.holding) == 0 {
		return
	}
	h.keep(h.hold(h.holding))
	h.holding = nil
}

// write writes one record to the ledger, with its content beside the
// event, tells focus and the git watcher what it says, and reports whether
// the ledger took it.
func (h *adapterHost) write(st *harnessStats, r adapters.Record) bool {
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
		return false
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
	return true
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
		Waiting: len(h.queue) + h.held + h.parkedN, Dropped: h.dropped}
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
// stamp, so that in the run that wrote it, it parses as it would have when
// it came. readHeld trusts those only on a line whose MAC verifies.
type heldCall struct {
	Message  json.RawMessage `json:"message"`
	Via      inbox.Via       `json:"via"`
	PeerPID  int             `json:"peer_pid,omitempty"`
	Walk     []platform.Proc `json:"walk,omitempty"`
	EventVia event.Via       `json:"event_via,omitempty"`
	Harness  event.Harness   `json:"harness,omitempty"`
	Received clock.Stamp     `json:"received"`
}

// hold appends calls to held.jsonl, with one write per call, and returns
// the ones it couldn't write, which the caller keeps (keep). The file has
// inbox/'s protections, as its pending log and its .replay do: a folder
// and a file that only this user can reach, opened with O_NOFOLLOW where
// the spool is (inbox.OpenPrivate; D-held-calls-inbox-class). A call that
// would take the file past inbox.MaxSpool is dropped, counted and
// reported instead: the ledger has then been shut for days, and filling the
// disk helps nobody. So is every call once the ledger can't open without
// a person's restore (giveUp): held calls would only pile up, prompts and
// all, with no end in sight. What is held is kept heldKeep at most
// (expireHeld).
func (h *adapterHost) hold(calls []inbox.Delivery) []inbox.Delivery {
	if len(calls) == 0 {
		return nil
	}
	h.mu.Lock()
	gaveUp := h.gaveUp
	h.mu.Unlock()
	if gaveUp {
		h.dropGivenUp(len(calls))
		return nil
	}
	name := h.a.dirs.Held()
	f, err := inbox.OpenPrivate(name, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
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
		line, err := h.heldLine(d)
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

// heldEntry is a line of held.jsonl or its pending log: the call, as the
// JSON of a heldCall, and the HMAC of exactly those bytes under the run's
// lineKey, in hex.
type heldEntry struct {
	Call json.RawMessage `json:"call"`
	MAC  string          `json:"mac"`
}

// heldLine is d as a line of held.jsonl, with a newline on each side. The
// line is written out by hand, so its call's bytes are exactly the ones
// its MAC covers.
func (h *adapterHost) heldLine(d inbox.Delivery) ([]byte, error) {
	m, err := d.Message.Encode()
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(heldCall{Message: m, Via: d.Via, PeerPID: d.PeerPID, Walk: d.Walk,
		EventVia: d.EventVia, Harness: d.Harness, Received: d.Received})
	if err != nil {
		return nil, err
	}
	mac := hex.EncodeToString(h.lineMAC(b))
	// A newline before it as well as after, as the inbox's spool has: a
	// line a full disk or a power cut left short then can't take the next
	// call with it.
	line := make([]byte, 0, len(b)+len(mac)+20)
	line = append(line, "\n{\"call\":"...)
	line = append(line, b...)
	line = append(line, ",\"mac\":\""...)
	line = append(line, mac...)
	return append(line, "\"}\n"...), nil
}

// lineMAC is the HMAC of a held call's JSON under the run's lineKey.
func (h *adapterHost) lineMAC(call []byte) []byte {
	m := hmac.New(sha256.New, h.lineKey)
	m.Write([]byte("pairtime held call v1"))
	m.Write([]byte{0})
	m.Write(call)
	return m.Sum(nil)
}

// giveUp tells the host that the ledger can't open until a person runs
// pairtime restore (ledgerGivesUp): from now on it keeps no call for later,
// and drops each instead (hold).
func (h *adapterHost) giveUp() {
	h.mu.Lock()
	h.gaveUp = true
	h.mu.Unlock()
	h.poke()
}

// dropGivenUp drops n calls that hold would have kept, once the ledger has
// given up, and says how to get it back, under adapters.held.
func (h *adapterHost) dropGivenUp(n int) {
	h.mu.Lock()
	h.dropped += uint64(n)
	dropped := h.dropped
	h.mu.Unlock()
	h.a.log.Warn("hook calls are dropped, because the ledger can't open until pairtime restore runs", "calls", n)
	h.a.setProblem("adapters.held", fmt.Sprintf("hook calls are no longer kept for the ledger, which can't open until you run pairtime restore; %d were dropped", dropped))
}

func (h *adapterHost) dropHeld(n int, err error) {
	h.mu.Lock()
	h.dropped += uint64(n)
	dropped := h.dropped
	h.mu.Unlock()
	h.a.log.Warn("hook calls that wait for the ledger couldn't be held", "calls", n, "err", err)
	h.a.setProblem("adapters.dropped", fmt.Sprintf("%d hook calls that waited for the ledger to open were lost: %v", dropped, err))
}

// replayProblem is what doctor hears while the held calls can't be read.
const replayProblem = "the hook calls held while the ledger wasn't open can't be read yet, and the calls after them wait behind them; the agent's log says why"

// replayHeld takes the calls held.jsonl holds to their adapters, oldest
// first, and reports whether it is done: whether no held call is left
// unread, so that the calls taken in since can go behind them. It renames
// the file first, so calls held meanwhile go to a new one, and a .replay
// file a crash left goes before it. Each call it reads is parsed, or waits
// for its folder's project like any other (admit), and the file is sealed
// and deleted once each of its calls is parsed (endReplay). Until then,
// what is held behind it waits too. A crash during a replay replays the
// file again at the next start, so its records that were sealed by then
// come out twice, never none. A .replay file that is parsed but can't be
// deleted stops the replay for the run: the calls held behind it wait for
// the next start, and calls taken in meanwhile are parsed before them.
func (h *adapterHost) replayHeld(last bool) bool {
	name := h.a.dirs.Held()
	replay := name + ".replay"
	for {
		switch {
		case h.replayStuck:
			return true // parsed, and left for the next start
		case h.replayRead && h.replayOut > 0:
			// Its calls wait for their folders' projects, and what is held
			// behind it waits for them.
			_, err := os.Stat(name)
			return errors.Is(err, fs.ErrNotExist)
		case h.replayRead:
			h.endReplay(replay)
			continue
		}
		if _, err := os.Stat(replay); errors.Is(err, fs.ErrNotExist) {
			if err := os.Rename(name, replay); err != nil {
				if !errors.Is(err, fs.ErrNotExist) {
					h.a.log.Warn("the held hook calls can't be read", "err", err)
					h.a.setProblem("adapters.replay", replayProblem)
					return false
				}
				h.a.setProblem("adapters.replay", "")
				return true // nothing is held
			}
		}
		if !h.readReplay(replay, last) {
			h.a.setProblem("adapters.replay", replayProblem)
			return false // the file stays, and the next step tries again
		}
		h.a.setProblem("adapters.replay", "")
		h.replayRead = true
	}
}

// readReplay reads the held calls in name, from where it got to, and
// admits each, and reports whether it read the whole file. A read that
// fails partway resumes after the last call it took, so none is taken
// twice in a run.
func (h *adapterHost) readReplay(name string, last bool) bool {
	f, err := inbox.OpenPrivate(name, os.O_RDONLY, 0)
	if err != nil {
		h.a.log.Warn("the held hook calls can't be read", "err", err)
		return false
	}
	defer f.Close()
	if _, err := f.Seek(h.replayAt, io.SeekStart); err != nil {
		h.a.log.Warn("the held hook calls can't be read", "err", err)
		return false
	}
	n, bad := 0, 0
	defer func() {
		h.replayTaken += n
		h.count(func() { h.held = max(h.held-n-bad, 0) })
		if n > 0 || bad > 0 {
			h.a.log.Info("read the hook calls held while the ledger wasn't open", "calls", n, "bad_lines", bad)
		}
	}()
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			h.a.log.Warn("reading the held hook calls stopped early", "err", err)
			return false
		}
		if len(bytes.TrimSpace(line)) > 0 {
			if d, ok := h.readHeld(line); ok {
				h.admit(d, true, last)
				n++
			} else {
				bad++
			}
		}
		h.replayAt += int64(len(line))
		if err != nil {
			return true // EOF
		}
	}
}

// endReplay seals the records of the calls read from the .replay file,
// name, once each of them is parsed, and deletes the file. Its records are
// written, and replaying the file again would write them twice, so it goes
// even when the seal fails, and the next seal takes them. One that can't
// be deleted stays for the next start (replayStuck): replayed again in
// this run, it would write its records twice; left, it does at most once
// more.
func (h *adapterHost) endReplay(name string) {
	if h.replayTaken > 0 {
		h.mu.Lock()
		w := h.writer
		h.mu.Unlock()
		if _, _, err := w.Seal(); err != nil {
			h.a.log.Warn("couldn't seal the held hook calls' records", "err", err)
		}
	}
	h.replayRead, h.replayTaken = false, 0
	if err := os.Remove(name); err != nil {
		h.a.log.Warn("couldn't delete the held hook calls once replayed", "err", err)
		h.a.setProblem("adapters.held", fmt.Sprintf("the hook calls held while the ledger wasn't open were parsed, but couldn't be deleted, so the next start parses them again: %v", err))
		h.replayStuck = true
		return
	}
	h.replayAt = 0
}

// heldTmp is the prefix of the temporary file expireFile rewrites a held
// file through, beside it, as home.WriteFile names its own, so a link's
// clearData at uninstall takes one a crash left (pairtimeEntries).
const heldTmp = ".held.jsonl.tmp-"

// expireHeld drops, at a start, the held calls the agent took in longer
// than heldKeep before now, unparsed: from held.jsonl, which holds the
// calls a killed run left pending too by then (recoverPending), and from a
// .replay file a crash left. A line that holds no call, or no time to go
// by, goes as well. It deletes what a rewrite cut short left, first. It runs
// on the worker before its first step, when nothing else writes them.
func (h *adapterHost) expireHeld(now time.Time) {
	if old, err := filepath.Glob(filepath.Join(h.a.dirs.Data, heldTmp+"*")); err == nil {
		for _, name := range old {
			os.Remove(name)
		}
	}
	n := 0
	for _, name := range []string{h.a.dirs.Held() + ".replay", h.a.dirs.Held()} {
		n += h.expireFile(name, now.Add(-heldKeep))
	}
	if n > 0 {
		h.dropHeld(n, fmt.Errorf("they were held longer than %v, which is as long as hook calls are kept", heldKeep))
	}
}

// expireFile drops from name the calls taken in before cutoff, and lines
// that hold no call or no stamp, by rewriting it with the rest, or by
// deleting it when none is left, and returns how many it dropped. A file it
// can't rewrite stays as it is, for the next start to try again. The rewrite
// closes the file before it renames the new one over it, as Windows needs
// (inbox.Rewrite).
func (h *adapterHost) expireFile(name string, cutoff time.Time) int {
	n, err := inbox.Rewrite(name, heldTmp, func(line []byte, _ int64) bool {
		at, ok := heldAt(line)
		return ok && !at.Before(cutoff)
	})
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			h.a.log.Warn("couldn't drop the old held hook calls", "err", err)
		}
		return 0
	}
	return n
}

// heldAt is when the agent took the call on a line of held.jsonl in, by
// the stamp the line gives, which heldKeep counts from: the agent's own,
// or, for a line any process could have written, whatever it says, which
// can keep nothing but that line longer. It reports false for a line that
// holds no call, or no stamp.
func heldAt(line []byte) (time.Time, bool) {
	var e heldEntry
	if err := json.Unmarshal(line, &e); err != nil || len(e.Call) == 0 {
		return time.Time{}, false
	}
	var c heldCall
	if err := json.Unmarshal(e.Call, &c); err != nil || c.Received.Wall.IsZero() {
		return time.Time{}, false
	}
	return c.Received.Wall, true
}

// countHeld counts the calls an earlier run left held, for the status.
func (h *adapterHost) countHeld() {
	n := 0
	for _, name := range []string{h.a.dirs.Held() + ".replay", h.a.dirs.Held()} {
		f, err := inbox.OpenPrivate(name, os.O_RDONLY, 0)
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

// readHeld reads a line of held.jsonl. Only a line this run wrote, whose
// MAC verifies under lineKey, keeps its via, the agent's walk of its
// sender, the sender's pid and the agent's stamp (D-spool-via). Every other
// one is read as a drain reads the spool's lines (inbox.Drainer): via the
// inbox, with no walk or pid, so its events are via plugin and carry no
// lineage, and stamped now, by the replay, so no event of it is timed
// after that (hookpath.Times). That is every line of an earlier run's
// held.jsonl or .replay, and every call a start moved there from a killed
// run's pending log.
func (h *adapterHost) readHeld(line []byte) (inbox.Delivery, bool) {
	var e heldEntry
	if err := json.Unmarshal(line, &e); err != nil || len(e.Call) == 0 {
		return inbox.Delivery{}, false
	}
	var c heldCall
	if err := json.Unmarshal(e.Call, &c); err != nil {
		return inbox.Delivery{}, false
	}
	m, err := inbox.Decode(c.Message)
	if err != nil {
		return inbox.Delivery{}, false
	}
	if mac, err := hex.DecodeString(e.MAC); err == nil && hmac.Equal(mac, h.lineMAC(e.Call)) {
		return inbox.Delivery{Message: m, Via: c.Via, PeerPID: c.PeerPID, Walk: c.Walk,
			EventVia: c.EventVia, Harness: c.Harness, Received: c.Received}, true
	}
	return inbox.Delivery{Message: m, Via: inbox.ViaInbox, Received: clock.Now()}, true
}
