// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"sync"

	"github.com/some-du6e/pairtime/internal/agent/hookpath"
	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/pkg/event"
)

// hookSink is the Sink in front of the journal, which the socket, the pipe
// and the drain all deliver to (WP-AG3). For every hook call it works out
// where the call came from (hookpath.Of), for the journal to record, and
// for a call that came over the socket or the pipe it also:
//
//   - claims the process that the agent's own walk found under the hook,
//     so the process scan doesn't report that process's session as one
//     that fires no hooks (DESIGN 3.3);
//   - ties that process to the session the call names, for the focus
//     component, so the window the process runs under hosts the session
//     (SM 3.2), and has focus look again when the tie is new, or the
//     session starts or ends (WP-OS2);
//   - hands the call to presence with its session key and kind, so a
//     prompt gets the sample SM 2.2 rule 6 reads, and turns set D14's
//     cadence (presence.Sampler.Hook).
//
// It does all of that before the call is stored and acked, while the hook
// is still running, and whether or not storing works: the walk showed that
// the harness fires hooks either way. Only a walk that counts claims or
// ties a process (hookpath.Of), and a walk that doesn't count is dropped
// from the delivery, so the journal records none.
//
// Every call the journal stores, whichever way it came, then goes to the
// adapters (hookSteps.record, adapters.go), which parse it on a goroutine
// of their own and write its records to the ledger. Nothing of that runs
// on the ack's path, and a call the journal had already, the second copy
// of one that came over the socket and through the spool, goes to them
// once.
//
// A spooled call does none of it. Its line proves nothing about which
// process wrote it, so it claims and ties nothing (DESIGN 5.2), and the
// drain that reads it can come long after the call, when a sample says
// nothing about the prompt and a turn started then would be one that
// already ended.
type hookSink struct {
	next  inbox.Sink
	goos  string
	steps hookSteps
	ties  *hookTies
	// key keys harness session ids for presence. It is random for each run
	// of the agent, which is as long as presence keeps a session.
	key []byte
}

// hookSteps are what the hook sink does with a call besides storing it.
// The agent wires them to its components (agent.hookSteps), and tests
// replace them. A nil step is skipped.
type hookSteps struct {
	// claim tells the process scan that host fires hooks
	// (procs.Scanner.ClaimLive).
	claim func(host platform.Proc)
	// presence hears a call with its session's key and kind
	// (presence.Sampler.Hook).
	presence func(session string, k event.Kind)
	// secret returns K_session, which the session keys that focus lists are
	// keyed with, or nil while it isn't known (keySessions).
	secret func() []byte
	// refresh has the focus component look again at the window in front
	// (focus.Watcher.Refresh).
	refresh func()
	// record hands a call the journal stored to the adapters, which parse
	// it later, off the ack's path (adapterHost.add). It must not block.
	// The adapters also set each session's project for focus_project
	// (hookTies.setProject), once the git adapter has resolved its folder.
	record func(d inbox.Delivery)
}

func newHookSink(next inbox.Sink, goos string, ties *hookTies, steps hookSteps) *hookSink {
	key := make([]byte, 32)
	rand.Read(key) // never fails: crypto/rand panics instead
	return &hookSink{next: next, goos: goos, steps: steps, ties: ties, key: key}
}

// hookSteps wires the hook sink to the agent's components: the process
// scan, presence and focus, which startIPC's callers start first.
func (a *agent) hookSteps() hookSteps {
	st := hookSteps{secret: a.sessionSecret, refresh: a.refreshFocus, record: a.adaptersHost().add}
	if a.procs != nil {
		st.claim = a.procs.ClaimLive
	}
	if a.presence != nil {
		st.presence = a.presence.Hook
	}
	return st
}

// Deliver stores d through the next sink, with what the agent made of a
// hook call. It is safe to call from many goroutines.
func (s *hookSink) Deliver(d inbox.Delivery) error {
	if d.Kind != hookpath.Kind {
		return s.next.Deliver(d)
	}
	b, berr := hookpath.DecodeBody(d.Message)
	p := hookpath.Of(s.goos, d, event.Harness(b.Harness))
	d.EventVia, d.Harness = p.Via, p.Harness
	if !p.Walked {
		// A walk that doesn't count is no walk: none of the call's events
		// can be via = hook, and nothing may read it later (DESIGN 3.4).
		d.Walk = nil
	}
	if d.Via != inbox.ViaInbox {
		var kind event.Kind
		if berr == nil {
			kind = hookpath.PresenceKind(b)
		}
		if p.Host != nil {
			if s.steps.claim != nil {
				s.steps.claim(*p.Host)
			}
			s.tie(*p.Host, b, kind)
		}
		if berr == nil && s.steps.presence != nil {
			s.steps.presence(s.presenceKey(b), kind)
		}
	}
	return s.store(d)
}

// journalStore is the part of the journal that says whether it stored a
// call or had it already (inbox.Journal.Store).
type journalStore interface {
	Store(d inbox.Delivery) (bool, error)
}

// store stores the hook call d through the next sink, and hands it to the
// adapters when the next sink stored it, and hadn't before.
func (s *hookSink) store(d inbox.Delivery) error {
	if s.steps.record == nil {
		return s.next.Deliver(d)
	}
	isNew := true
	var err error
	if j, ok := s.next.(journalStore); ok {
		isNew, err = j.Store(d)
	} else {
		err = s.next.Deliver(d)
	}
	if err == nil && isNew {
		s.steps.record(d)
	}
	return err
}

// tie records that host runs the session the call b names, a call of kind
// k, and has focus look again when that is news: a session the process
// hadn't run before, one that starts, or one that ends.
func (s *hookSink) tie(host platform.Proc, b hookpath.Body, k event.Kind) {
	news := k == event.KindSessionStart || k == event.KindSessionEnd
	if session := s.sessionKey(b); session != "" {
		if k == event.KindSessionEnd {
			s.ties.end(host.ID(), session)
		} else if s.ties.tie(host.ID(), session) {
			news = true
		}
	}
	if news && s.steps.refresh != nil {
		s.steps.refresh()
	}
}

// sessionKey returns the session_key of the session the call b names,
// which focus lists in hosted_sessions and the session's events carry
// (hookpath.SessionKey), or "" when the call names no session or K_session
// isn't known yet.
func (s *hookSink) sessionKey(b hookpath.Body) string {
	if s.steps.secret == nil {
		return ""
	}
	secret := s.steps.secret()
	if secret == nil {
		return ""
	}
	return hookpath.SessionKey(secret, hookpath.SessionID(b))
}

// presenceKey returns the opaque key presence keeps the call's session
// under: the harness's session id, keyed with s.key, or "" when the call
// names none. Presence keeps it in memory only, for this run, so it needs
// no key that outlives the run and can't wait for the keystore; it is a key
// all the same, never the harness's own id (AGENTS.md).
func (s *hookSink) presenceKey(b hookpath.Body) string {
	id := hookpath.SessionID(b)
	if id == "" {
		return ""
	}
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(b.Harness))
	mac.Write([]byte{0})
	mac.Write([]byte(id))
	return "s_" + hex.EncodeToString(mac.Sum(nil)[:8])
}

// hookTies are the processes the hook path saw fire hooks over the socket
// or the pipe, each with the key of the session it runs, for the focus
// component (WP-OS2): the window in front hosts the sessions whose
// processes run under its own (SM 3.2). The process scan reports the
// sessions no hook speaks for (procs.Scanner.Sessions), and these are the
// rest. They live in memory: after a restart a session is tied again by
// its next hook. A nil *hookTies holds none.
type hookTies struct {
	mu       sync.Mutex
	sessions map[platform.ID]string // session key, by process
	projects map[string]string      // project key, by session key
}

func newHookTies() *hookTies {
	return &hookTies{sessions: make(map[platform.ID]string), projects: make(map[string]string)}
}

// tie records that the process id runs session, and reports whether that
// is new.
func (t *hookTies) tie(id platform.ID, session string) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sessions[id] == session {
		return false
	}
	t.sessions[id] = session
	return true
}

// end records that session has ended in the process id.
func (t *hookTies) end(id platform.ID, session string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sessions[id] == session {
		delete(t.sessions, id)
		t.forgetLocked(session)
	}
}

// setProject records the project session works in, and reports whether
// that is news. A session no process runs is left alone: its project would
// stay forever, since only a tie's end or prune forgets one, and focus
// hosts tied sessions only. The adapters call it for every record with a
// project, after the hook sink tied the call's process.
func (t *hookTies) setProject(session, project string) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.projects[session] == project || !t.runsLocked(session) {
		return false
	}
	t.projects[session] = project
	return true
}

// runsLocked reports whether a process runs session. t.mu must be held.
func (t *hookTies) runsLocked(session string) bool {
	for _, s := range t.sessions {
		if s == session {
			return true
		}
	}
	return false
}

// projectOf returns the project key of session, or "" when it isn't known
// (focus.Options.ProjectOf).
func (t *hookTies) projectOf(session string) string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.projects[session]
}

// merged returns scan, the sessions the process scan found by process,
// with the ties added (focus.Options.Sessions). A hook's tie wins over the
// scan's report of the same process, which the hook's claim withdraws
// anyway.
func (t *hookTies) merged(scan map[platform.ID]string) map[platform.ID]string {
	if t == nil {
		return scan
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.sessions) == 0 {
		return scan
	}
	out := make(map[platform.ID]string, len(scan)+len(t.sessions))
	maps.Copy(out, scan)
	maps.Copy(out, t.sessions)
	return out
}

// prune drops the ties of processes that claimed says are gone: the
// process scan forgets a claim once its process has left the table
// (procs.Scanner.Claimed), and focus wants a process that has exited out of
// its map (focus.Options.Sessions).
func (t *hookTies) prune(claimed func(platform.ID) bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, session := range t.sessions {
		if !claimed(id) {
			delete(t.sessions, id)
			t.forgetLocked(session)
		}
	}
}

// forgetLocked drops session's project once no process runs the session.
// t.mu must be held.
func (t *hookTies) forgetLocked(session string) {
	if !t.runsLocked(session) {
		delete(t.projects, session)
	}
}

// keySessions derives K_session from the device key and keeps it for the
// hook sink, which keys the session keys focus lists with it. openLedger
// calls it once the keystore has opened. Until then, and if it fails, no
// hook ties a process to its session.
func (a *agent) keySessions(ks *keys.Keystore) {
	var secret []byte
	err := ks.WithDeviceKey(func(kDev []byte) error {
		var err error
		secret, err = hookpath.SessionSecret(kDev)
		return err
	})
	if err != nil {
		a.log.Warn("hooked sessions stay out of os.focus, because K_session couldn't be derived", "err", err)
		return
	}
	a.kSession.Store(&secret)
}

// sessionSecret returns K_session, or nil until keySessions has derived it.
func (a *agent) sessionSecret() []byte {
	if k := a.kSession.Load(); k != nil {
		return *k
	}
	return nil
}

// walkPeer reads the parent chain of a connection's peer, for the socket
// and the pipe to hand on with each of its messages (ipc.Options.Walk). A
// table it can't read gives no walk, so the calls on that connection count
// as plugin calls.
func (a *agent) walkPeer(pid int) []platform.Proc {
	chain, err := hookpath.ReadChain(pid)
	if err != nil {
		a.log.Warn("couldn't read the parent chain of a hook", "pid", pid, "err", err)
		return nil
	}
	return chain
}
