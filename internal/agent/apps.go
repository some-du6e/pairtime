// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters"
	"github.com/some-du6e/pairtime/internal/adapters/t3code"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/sched"
	"github.com/some-du6e/pairtime/internal/install"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/pkg/event"
)

// The apps component (DESIGN 3.3) reads the stores of the agent apps that
// drive harnesses for clients elsewhere: T3 Code's, through its reader
// (internal/adapters/t3code; DESIGN 5.1), for the relay.message records SM
// 2.2 rule 9 reads, and for the runs a guest's message started, whose
// prompts keep no text. It reads every appsEvery while T3 runs, which the
// reader tells by the -wal file T3 holds open, every appsIdle while it
// doesn't, and at once when the adapter host is about to write a prompt
// of a session T3's server started, before the prompt's text reaches the
// ledger writer and the vault (prompt):
//
//   - A prompt.submit's relay and guest are the reader's word alone (DESIGN
//     4.1 E-13). The host drops whatever an adapter or a sender put there,
//     and the component sets them from the reader's answer.
//   - It asks about a prompt a hook or a log reader saw, the only kinds a
//     relay record can witness (DESIGN 5.2), of a session whose nearest
//     non-shell ancestor is an agent app's server, parent_class agent_app,
//     as the call's walk or the session's session.start says, and once per
//     turn: the turn's other copy, its transcript's say, takes the guest
//     mark from the answer and no link, so one run claims one record of
//     the turn.
//   - A prompt a guest's run delivered is marked guest, and the ledger
//     writer keeps none of its text. When the reader can't answer, with no
//     sqlite3, a schema it refuses or a read that timed out, nothing is
//     marked, and the prompt keeps its text as any prompt does.
//   - The pairing goes on whichever of the two records comes second. A
//     prompt that pairs with a record already written takes it as its
//     relay; a record the same read found names the prompt, and is written
//     after it. One lock holds each read and the writes of what it found
//     together, so no other read's records come between a prompt and its
//     record.
//   - Each record goes through the ledger writer with source log and via
//     app, at the moment the reader read its row, with this run's clock as
//     the agent's own records carry it (ledger.RunClock), and is sealed at
//     once, as a git.change is.
//   - While its reads find T3 running, presence counts T3 as a project
//     context, as it counts a running harness process, so the minutes a
//     phone message's lead and its reading reach are sampled rather than
//     read as slept through (D-relayed-context).
//
// What the reader keeps between runs, its cursor and the rows, runs and
// prompts it watches, all by key, goes to state.json under appsKey.

// appsKey is the apps component's key in state.json.
const appsKey = "apps"

// How often the apps component reads: appsEvery while an agent app runs
// (DESIGN 3.3), and appsIdle while none does, when a read only finds that
// out. Both are on the scheduler's grid, so the idle reads share the
// minute's wakeup with the process scan.
const (
	appsEvery = 10 * time.Second
	appsIdle  = time.Minute
)

// Bounds on one read: one the apps job starts, and one for a prompt, which
// the adapter host's worker waits for. The reader puts each of its sqlite3
// runs under a timeout of its own too (t3code.DefaultTimeout).
const (
	appsPollWait = 30 * time.Second
	appsPairWait = 10 * time.Second
)

// appsSaveEvery is how often, at most, what the reader keeps goes to
// state.json while it reads, unless a read wrote records or paired a
// prompt: then it goes at once, so the rows it watches outlive a restart.
// The reader's cursor moves with every row T3 writes, an agent's answers
// included, so saving after every read would write state.json every 10 s.
const appsSaveEvery = time.Minute

// maxAppSessions bounds the sessions the component knows an agent app's
// server started, and maxAppTurns the turns it asked about, which it keeps
// for appTurnKeep: the copies of one turn, its hook's and its transcript's,
// come within seconds of each other. Past a bound, it forgets the one it
// heard of least recently.
const (
	maxAppSessions = 1024
	maxAppTurns    = 1024
	appTurnKeep    = 15 * time.Minute
)

// appReader is what the apps component needs of an app's reader,
// *t3code.Reader. Tests give it a fake.
type appReader interface {
	Poll(ctx context.Context) (t3code.Batch, error)
	Pair(ctx context.Context, p t3code.Prompt) (t3code.Pairing, t3code.Batch, error)
	State() t3code.State
	Status() t3code.Status
}

// appsState is what state.json keeps of the apps component.
type appsState struct {
	// T3Code is what T3 Code's reader keeps: its cursor and the rows, runs
	// and prompts it watches, by key alone.
	T3Code t3code.State `json:"t3_code"`
	// SessionAt is when the component last heard of a session an agent
	// app's server started, in Unix ms, 0 for never: doctor says when T3
	// Code is there but none of its sessions has reached pairtime in a day
	// (DESIGN 5.1, 5.9), across restarts of the agent.
	SessionAt int64 `json:"session_at,omitempty"`
}

// appTurn names a turn of a session an agent app's server started, by its
// session and turn keys.
type appTurn struct{ session, turn string }

// appAsked is what the component remembers of a turn it asked the reader
// about: when, and whether a guest's run delivered it.
type appAsked struct {
	at    time.Time
	guest bool
}

// appsHost is the apps component.
type appsHost struct {
	a *agent
	// newReader makes T3 Code's reader (attach). Tests replace it.
	newReader func(t3code.Options) (appReader, error)
	// now is the clock: time.Now outside tests.
	now func() time.Time
	// ctx ends the reads at the agent's stop.
	ctx    context.Context
	cancel context.CancelFunc

	// mu holds each read together with the writes of what it found, and
	// guards what follows.
	mu sync.Mutex
	// stores are where T3's store may be, from Locate at the start.
	stores []t3code.Store
	// reader is T3 Code's, and writer the ledger writer, both nil until
	// attach. reader is set holding smu as well. Nothing that holds smu
	// alone calls it: the status reads what the last read left (t3Status).
	reader appReader
	writer *ledger.Writer
	// job is the reads' timer, which runs every appsEvery while fast says
	// the last read read T3's store (appReading), and every appsIdle
	// otherwise.
	job  *sched.Job
	fast bool
	// running says the last read found T3 running, as presence last heard
	// (appRunning).
	running bool
	// off is why the reader didn't read at the last read, for the log.
	off string
	// saved is what state.json last got, at savedAt.
	saved   []byte
	savedAt time.Time
	// written counts the relay.message records the ledger took.
	written uint64

	// smu guards what the component knows of sessions and turns, apart
	// from mu, so the adapter host never waits for a read to find out
	// whether it asks one. sessions are those an agent app's server
	// started, by key, with when they were last heard of; turns are those
	// it asked about. heard is when it last heard of any such session,
	// which state.json keeps (appsState.SessionAt). t3 is the reader's
	// Status as the last read left it, once t3ok, which GET /v1/status
	// reads without waiting for a read (t3Status).
	smu      sync.Mutex
	sessions map[string]time.Time
	turns    map[appTurn]appAsked
	heard    time.Time
	t3       t3code.Status
	t3ok     bool
	started  bool
	closed   bool

	kick chan struct{}
	stop chan struct{}
	done chan struct{}
}

func newAppsHost(a *agent) *appsHost {
	ctx, cancel := context.WithCancel(context.Background())
	return &appsHost{
		a: a,
		newReader: func(o t3code.Options) (appReader, error) {
			r, err := t3code.New(o)
			if err != nil {
				return nil, err
			}
			return r, nil
		},
		now:      time.Now,
		ctx:      ctx,
		cancel:   cancel,
		sessions: make(map[string]time.Time),
		turns:    make(map[appTurn]appAsked),
		kick:     make(chan struct{}, 1),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// appsHost returns the agent's apps component, making it on first use: Run
// starts it, and the adapter host's worker reaches it, in whichever order
// they run.
func (a *agent) appsHost() *appsHost {
	a.appsOnce.Do(func() { a.apps = newAppsHost(a) })
	return a.apps
}

// startApps starts the apps component and returns the step that stops it.
// That step runs after the adapter host has stopped, whose last step can
// still ask about a prompt, and before the ledger closes. It finds where T3
// Code's store may be on the machine the agent runs on (Options.Env); a T3
// installed later at its default home is read once it runs. The reader
// starts once the adapter host can parse (attach).
func (a *agent) startApps(opts Options) func() {
	p := a.appsHost()
	env := opts.Env
	if env == nil {
		e, err := install.DefaultEnv("")
		if err != nil {
			a.setProblem("apps", fmt.Sprintf("no agent app's messages are read, because the home folder isn't known: %v", err))
			return func() {}
		}
		env = &e
	}
	stores := t3code.Locate(*env)
	p.mu.Lock()
	p.stores = stores
	p.job = a.sched.Every("apps", appsIdle, func(time.Time) { p.poke() })
	p.mu.Unlock()
	p.smu.Lock()
	p.started = true
	p.smu.Unlock()
	go p.run()
	return p.close
}

// attach starts T3 Code's reader once the adapter host can parse
// (adapterHost.prepare), with the ledger writer; K_session, which keys T3's
// ids and, as the hook keys session_id, the Claude Code sessions its runs
// go to; and folder, pairtime's own word on the folder a T3 thread runs in
// (adapterHost.appFolder). It takes up what state.json kept of the reader,
// and has it read at once.
func (p *appsHost) attach(w *ledger.Writer, secret []byte, folder func(string) t3code.Folder) {
	p.smu.Lock()
	closed := p.closed
	p.smu.Unlock()
	if closed {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.reader != nil || len(p.stores) == 0 {
		return
	}
	kept := p.load()
	r, err := p.newReader(t3code.Options{Stores: p.stores, Keys: adapters.NewKeys(secret), Folder: folder,
		Table: p.a.table, Zone: p.a.zone(), State: kept.T3Code})
	if err != nil {
		p.a.log.Warn("T3 Code's messages aren't read, because its reader couldn't start", "err", err)
		p.a.setProblem("apps", fmt.Sprintf("T3 Code's messages aren't read, because its reader couldn't start: %v", err))
		return
	}
	p.smu.Lock()
	p.reader, p.writer = r, w
	if at := time.UnixMilli(kept.SessionAt); kept.SessionAt > 0 && at.After(p.heard) {
		p.heard = at
	}
	p.smu.Unlock()
	p.poke()
}

func (p *appsHost) poke() {
	select {
	case p.kick <- struct{}{}:
	default:
	}
}

// run reads each time the job pokes it, until the stop.
func (p *appsHost) run() {
	defer close(p.done)
	for {
		select {
		case <-p.kick:
			select {
			case <-p.stop: // the stop goes first
				return
			default:
			}
			p.poll()
		case <-p.stop:
			return
		}
	}
}

// close stops the reads, ending the one under way, and saves what the
// reader keeps to state.json.
func (p *appsHost) close() {
	p.smu.Lock()
	p.closed = true
	started := p.started
	p.smu.Unlock()
	p.cancel()
	if started {
		p.mu.Lock()
		job := p.job
		p.mu.Unlock()
		if job != nil {
			job.Stop()
		}
		close(p.stop)
		<-p.done
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.save(true)
}

// poll is a read the job asked for: it writes the records the reader found.
// While the ledger writer is down, it reads nothing, so the rows wait in
// T3's store for the next start rather than be read and refused.
func (p *appsHost) poll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.reader == nil || p.writer.Status().Failed != "" {
		return
	}
	ctx, cancel := context.WithTimeout(p.ctx, appsPollWait)
	b, err := p.reader.Poll(ctx)
	cancel()
	p.record(b)
	p.after(err, len(b.Messages) > 0)
}

// prompt writes prompt.submit ev with write, the adapter host's, and
// reports what write did. It first drops the relay and guest an adapter or
// a sender put on the prompt. Then, for a prompt the reader may know of
// (wants), it asks the reader whether the prompt pairs with a message and
// whether a guest's run delivered it, before the prompt's text reaches the
// ledger writer, and sets them from the answer (DESIGN 5.1). late says the
// record is lookback's, of a time long past. The records the read found go
// to the ledger after the prompt, so a record that names it comes second.
func (p *appsHost) prompt(ev event.Event, late bool, write func(event.Event) bool) bool {
	ps, ok := ev.Data.(*event.PromptSubmit)
	if !ok || ps == nil {
		return write(ev)
	}
	// A copy, so the adapter's record isn't changed.
	cp := *ps
	cp.Relay, cp.Guest = "", false
	ev.Data = &cp
	turn := appTurn{session: ev.SessionKey, turn: cp.TurnKey}
	ask, guest := p.wants(ev, late, turn)
	cp.Guest = guest
	if !ask {
		return write(ev)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.reader == nil {
		return write(ev)
	}
	ctx, cancel := context.WithTimeout(p.ctx, appsPairWait)
	pr, b, err := p.reader.Pair(ctx, t3code.Prompt{SessionKey: ev.SessionKey, T: ev.T, EventID: ev.EventID})
	cancel()
	switch {
	case pr.Guest:
		// A guest's run delivered it, which the reader may know from an
		// earlier read even when this one failed: the mark only costs the
		// prompt credit, and keeps the guest's words off the device.
		cp.Guest = true
		p.guestTurn(turn)
	case pr.Relay != "":
		cp.Relay = pr.Relay
	}
	ok = write(ev)
	p.record(b)
	p.after(err, len(b.Messages) > 0 || pr.Relay != "")
	return ok
}

// wants reports whether to ask the reader about prompt ev, of turn turn,
// and whether a guest's run delivered another copy of the turn, as the
// reader said when it was asked. A prompt is asked about once per turn,
// when a hook or a log reader saw it, it isn't lookback's, which no read
// answers for, and an agent app's server started its session.
func (p *appsHost) wants(ev event.Event, late bool, turn appTurn) (ask, guest bool) {
	p.smu.Lock()
	defer p.smu.Unlock()
	now := p.now()
	p.forgetTurns(now)
	if a, ok := p.turns[turn]; ok && turn.turn != "" {
		return false, a.guest
	}
	if late || ev.Via != event.ViaHook && ev.Via != event.ViaLog {
		return false, false
	}
	if _, ok := p.sessions[ev.SessionKey]; !ok {
		return false, false
	}
	if turn.turn != "" {
		p.turns[turn] = appAsked{at: now}
	}
	return true, false
}

// guestTurn notes that a guest's run delivered turn, so the turn's other
// copies are marked guest too.
func (p *appsHost) guestTurn(turn appTurn) {
	if turn.turn == "" {
		return
	}
	p.smu.Lock()
	defer p.smu.Unlock()
	a := p.turns[turn]
	if a.at.IsZero() {
		a.at = p.now()
	}
	a.guest = true
	p.turns[turn] = a
}

// forgetTurns forgets the turns asked about more than appTurnKeep before
// now, and the oldest past maxAppTurns. p.smu is held.
func (p *appsHost) forgetTurns(now time.Time) {
	for k, a := range p.turns {
		if now.Sub(a.at) > appTurnKeep {
			delete(p.turns, k)
		}
	}
	for len(p.turns) > maxAppTurns {
		var oldest appTurn
		var at time.Time
		for k, a := range p.turns {
			if at.IsZero() || a.at.Before(at) {
				oldest, at = k, a.at
			}
		}
		delete(p.turns, oldest)
	}
}

// noteSession notes what a record or a walk says of session's nearest
// non-shell ancestor: an agent app's server, parent_class agent_app, makes
// session one whose prompts the component asks about; any other class it
// names makes it one whose prompts it doesn't; unknown says nothing, as a
// spooled call's or a log reader's start does, and changes nothing.
func (p *appsHost) noteSession(session string, class event.ParentClass) {
	if session == "" || session == event.NoSession {
		return
	}
	p.smu.Lock()
	defer p.smu.Unlock()
	switch class {
	case event.ParentAgentApp:
		now := p.now()
		p.sessions[session] = now
		if now.After(p.heard) {
			p.heard = now
		}
		for len(p.sessions) > maxAppSessions {
			oldest, at := "", time.Time{}
			for k, t := range p.sessions {
				if oldest == "" || t.Before(at) {
					oldest, at = k, t
				}
			}
			delete(p.sessions, oldest)
		}
	case event.ParentUnknown, "":
	default:
		delete(p.sessions, session)
	}
}

// noteWalk notes what the agent's walk of a hook call of session found
// (adapters.Arrival.Walk), nil for a call whose walk didn't count. Every
// call over the socket of a session T3's server started has one, so the
// mark comes back with its first call after a restart.
func (p *appsHost) noteWalk(session string, w *platform.Lineage) {
	if w != nil {
		p.noteSession(session, w.ParentClass)
	}
}

// noteRecord notes what record ev says of its session's ancestry: a
// session.start's parent_class.
func (p *appsHost) noteRecord(ev event.Event) {
	if st, ok := ev.Data.(*event.SessionStart); ok && st != nil {
		p.noteSession(ev.SessionKey, st.ParentClass)
	}
}

// record writes the relay.message records a read found, then seals them,
// so what state.json says the reader read never runs ahead of the ledger.
// p.mu is held.
func (p *appsHost) record(b t3code.Batch) {
	if len(b.Messages) == 0 || p.writer == nil {
		return
	}
	refused := false
	for _, m := range b.Messages {
		if _, err := p.writer.Write(ledger.Input{Event: relayEvent(m)}); err != nil {
			refused = true
			p.a.log.Warn("the ledger refused a relay.message record", "err", err)
			p.a.setProblem("apps.ledger", fmt.Sprintf("the ledger refused a relay.message record: %v", err))
			continue
		}
		p.written++
	}
	if !refused {
		p.a.setProblem("apps.ledger", "")
	}
	if _, _, err := p.writer.Seal(); err != nil {
		p.a.log.Warn("couldn't seal the relay.message records", "err", err)
	}
}

// relayEvent is the event of relay.message record m: source log and via
// app, at m.SeenAt, when the reader read the row, with this run's clock as
// the agent's own records carry it (ledger.RunClock), bound to the Claude
// Code session T3 started the message's run in, or to none.
func relayEvent(m t3code.Message) event.Event {
	session := m.SessionKey
	if session == "" {
		session = event.NoSession
	}
	harness := m.Harness
	if harness == "" {
		harness = event.HarnessNone
	}
	mono, boot := ledger.RunClock(m.SeenAt)
	d := m.Data
	return event.Event{
		Envelope: event.Envelope{EventID: m.EventID, T: m.SeenAt.UnixMilli(), Mono: mono, BootID: boot,
			Source: event.SourceLog, Via: event.ViaApp, Harness: harness, SessionKey: session,
			ProjectKey: m.ProjectKey},
		Data: &d,
	}
}

// after notes how a read went: the job reads every appsEvery while the
// reader reads T3's store (appReading) and every appsIdle while it can't;
// presence hears whether T3 runs, which keeps a project context live
// (appRunning); the reader's status is kept for GET /v1/status (t3Status);
// the log hears once when the reader stops reading for a reason other than
// T3 not running; and what the reader keeps goes to state.json, at once
// when wrote says the read wrote records or paired a prompt. A read the
// agent's stop cut short changes neither cadence, context nor the status.
// p.mu is held.
func (p *appsHost) after(err error, wrote bool) {
	if fast := appReading(err); p.ctx.Err() == nil && fast != p.fast {
		p.fast = fast
		if p.job != nil {
			every := appsIdle
			if fast {
				every = appsEvery
			}
			p.job.Reset(every)
		}
	}
	if running := appRunning(err); p.ctx.Err() == nil && running != p.running {
		p.running = running
		if p.a.presence != nil {
			n := 0
			if running {
				n = 1
			}
			p.a.presence.AgentApps(n)
		}
	}
	st := p.reader.Status()
	if p.ctx.Err() == nil {
		p.smu.Lock()
		p.t3, p.t3ok = st, true
		p.smu.Unlock()
	}
	if st.Off != p.off {
		p.off = st.Off
		switch st.Off {
		case "", t3code.OffNotRunning, t3code.OffNoStore:
		default:
			// The reader's words name no path (t3code.Status).
			p.a.log.Info("T3 Code's store isn't read", "why", st.Off, "problem", st.Problem)
		}
	}
	p.save(wrote)
}

// appReading reports whether a read that ended with err found T3 running
// with a store the reader reads, or was kept from it by what can pass at
// any moment, a store busy with t3 doctor's compaction or a slow read: the
// job then reads every appsEvery, as DESIGN 3.3 has it while an agent app
// runs, since a row first read more than RELAY_SEEN_LAG_S after its stamp
// witnesses nothing. No store, T3 not running, no sqlite3 on PATH and a
// schema the probe refused wait for appsIdle, which is soon enough to see
// any of them change, and spares a probe of a refused schema every 10 s.
func appReading(err error) bool {
	var se *t3code.SchemaError
	switch {
	case errors.Is(err, t3code.ErrNotRunning), errors.Is(err, t3code.ErrNoStore),
		errors.Is(err, t3code.ErrNoSQLite3), errors.As(err, &se):
		return false
	}
	return true
}

// appRunning reports whether a read that ended with err found T3 running:
// its store held open, which for T3 Code is while its -wal file exists
// (DESIGN 3.3), whether or not the reader could read it. A running agent
// app makes a project context live, as a running harness process does, so
// presence samples the minutes a message from T3's phone or web client
// reaches back into (D-relayed-context).
func appRunning(err error) bool {
	return !errors.Is(err, t3code.ErrNotRunning) && !errors.Is(err, t3code.ErrNoStore)
}

// load reads what state.json kept of the apps component, or nothing.
func (p *appsHost) load() appsState {
	var st appsState
	if p.a.state == nil {
		return st
	}
	if _, err := p.a.state.Get(appsKey, &st); err != nil {
		p.a.log.Warn("T3 Code's reader starts afresh, because state.json's memory of it doesn't read", "err", err)
		return appsState{}
	}
	return st
}

// save puts what the reader keeps in state.json, and writes the file, when
// it changed: at once when force is set, else at most every appsSaveEvery.
// p.mu is held.
func (p *appsHost) save(force bool) {
	if p.reader == nil || p.a.state == nil {
		return
	}
	now := p.now()
	if !force && now.Sub(p.savedAt) < appsSaveEvery {
		return
	}
	b, err := json.Marshal(appsState{T3Code: p.reader.State(), SessionAt: p.heardMS()})
	if err != nil {
		p.a.log.Warn("T3 Code's reader's memory couldn't be saved", "err", err)
		return
	}
	if bytes.Equal(b, p.saved) {
		return
	}
	if err := p.a.state.Put(appsKey, json.RawMessage(b)); err != nil {
		p.a.log.Warn("T3 Code's reader's memory couldn't be saved", "err", err)
		return
	}
	if err := p.a.state.Save(); err != nil {
		p.a.log.Warn("couldn't save state.json", "err", err)
		return
	}
	p.saved, p.savedAt = b, now
}

// heardMS is when the component last heard of a session an agent app's
// server started, in Unix ms, or 0 for never.
func (p *appsHost) heardMS() int64 {
	p.smu.Lock()
	defer p.smu.Unlock()
	if p.heard.IsZero() {
		return 0
	}
	return p.heard.UnixMilli()
}

// t3Status is how T3 Code's reader is doing, for doctor (DESIGN 5.9), as
// its last read left it, with when the component last heard of a session
// an agent app's server started, and false before the reader's first read.
// It never waits for a read, as the reader's own Status does: a read of a
// store that t3 doctor's compaction holds can take seconds, and doctor
// waits for the agent's whole status for only a few.
func (p *appsHost) t3Status() (st t3code.Status, heard time.Time, ok bool) {
	p.smu.Lock()
	defer p.smu.Unlock()
	return p.t3, p.heard, p.t3ok
}

// appsReply is the apps component's part of GET /v1/status: how it reads
// the agent apps whose stores it reads (DESIGN 3.3), for doctor's T3 Code
// line (DESIGN 5.9).
type appsReply struct {
	// T3Code is T3 Code's reader, absent until its first read.
	T3Code *t3Reply `json:"t3_code,omitempty"`
}

// t3Reply is how T3 Code's reader is doing, as its last read left it, and
// what it read today. It names no path.
type t3Reply struct {
	// Reading says the last read worked, and Off why it didn't:
	// t3code.OffNoStore, OffNotRunning, OffNoSQLite3, OffSchema or
	// OffFailed. Problem says more of a refused schema or a failed read.
	Reading bool   `json:"reading"`
	Off     string `json:"off,omitempty"`
	Problem string `json:"problem,omitempty"`
	// Newer says T3's schema is newer than the one the reader was tested
	// against, which it reads anyway.
	Newer bool `json:"newer,omitempty"`
	// Day is the day the counts are for, YYYY-MM-DD in the agent's zone:
	// Witnessed counts the messages T3 says a person sent that the reader
	// recorded, Paired its records that paired with a prompt, and Aged the
	// rows the age test turned away (t3code.Counts).
	Day       string `json:"day"`
	Witnessed int    `json:"witnessed"`
	Paired    int    `json:"paired"`
	Aged      int    `json:"aged"`
	// SessionAt is when a session an agent app's server started last
	// reached the agent, in Unix ms, absent when none has.
	SessionAt int64 `json:"session_at,omitempty"`
}

// reply is the apps component's part of the status. Its counts are
// today's in the agent's zone: when the last read was on another day, as
// just after midnight, it counts none.
func (p *appsHost) reply() appsReply {
	st, heard, ok := p.t3Status()
	if !ok {
		return appsReply{}
	}
	r := &t3Reply{Reading: st.Reading, Off: st.Off, Problem: st.Problem, Newer: st.Newer,
		Day: st.Day, Witnessed: st.Witnessed, Paired: st.Paired, Aged: st.Aged}
	if today := p.now().In(p.a.zone()).Format(time.DateOnly); r.Day != today {
		r.Day, r.Witnessed, r.Paired, r.Aged = today, 0, 0, 0
	}
	if !heard.IsZero() {
		r.SessionAt = heard.UnixMilli()
	}
	return appsReply{T3Code: r}
}
