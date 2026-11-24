// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
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
// prompts keep no text. It reads every appsEvery while T3 runs, every
// appsIdle while it doesn't, and at once when the adapter host is about to
// write a prompt of a session T3's server started, before the prompt's
// text reaches the ledger writer and the vault (prompt). T3 runs, by DESIGN
// 3.3's test, when the agent's last process scan found T3's server
// (t3code.ServerIn, noteServer) and the store a read picked has the -wal
// and -shm files T3 holds open (t3code.Status.Running); the files alone
// don't say so, since a T3 killed while it held them leaves them behind.
//
//   - A prompt.submit's relay and guest are the reader's word alone (DESIGN
//     4.1 E-13), with one exception: every copy of a prompt, from any
//     harness and on any path, a hook's, a spooled call's, a transcript's or
//     lookback's, whose text opens as T3 Code opens a guest's message when
//     it sends it on is marked guest (t3code.OpensAsGuest), since that text
//     carries another person's words. The host drops whatever an adapter or
//     a sender put there, and the component sets them.
//   - It asks about a prompt a hook or a log reader saw, the only kinds a
//     relay record can witness (DESIGN 5.2), of a session whose nearest
//     non-shell ancestor is an agent app's server, parent_class agent_app,
//     as the call's walk or the session's session.start says, and once per
//     turn: the turn's other copy, its transcript's say, takes the guest
//     mark from the answer and no link, so one run claims one record of
//     the turn.
//   - A prompt a guest's run delivered is marked guest, and the ledger
//     writer keeps none of its text. So is every copy of a prompt, asked
//     about or not and past the turn cache, that falls in a running turn
//     the owner steered a guest's message into, from the later of the
//     message's stamp and the turn's start to the turn's end, unless a run
//     claimed it: Claude Code takes the steer as a prompt of its own in the
//     turn's session, which no run of the guest's delivers (t3code.Steer;
//     docs/errata/2026-11-23-t3-reader.md, decision 4). When the reader
//     can't answer, with no sqlite3, a schema it refuses or a read that
//     timed out, the reader's word marks nothing new, and the prompt keeps
//     its text as any prompt does unless it opens as a guest's.
//   - The pairing goes on whichever of the two records comes second. A
//     prompt that pairs with a record already written takes it as its
//     relay; a record the same read found names the prompt, and is written
//     after it. One lock holds each read and the writes of what it found
//     together, so no other read's records come between a prompt and its
//     record. A prompt marked guest takes no relay.
//   - Each record goes through the ledger writer with source log and via
//     app, at the moment the reader read its row, with this run's clock as
//     the agent's own records carry it (ledger.RunClock), and is sealed at
//     once, as a git.change is.
//   - While its reads find T3 running with a store the reader can read
//     (appRuns), presence counts T3 as a project context, as it counts a
//     running harness process, so the minutes a phone message's lead and
//     its reading reach are sampled rather than read as slept through
//     (D-relayed-context). With no other sign of a context, that is a
//     sample a minute, and no os.focus record (presence.Status.AppOnly).
//     The Claude Code T3's server keeps running in a thread between its
//     runs is no other sign: the process scan leaves out a harness process
//     whose parent_class is agent_app (procs.Options.Running), though its
//     hooks and T3's own window in front are signs as any are.
//
// What the reader keeps between runs, its cursor and the rows, runs and
// prompts it watches, all by key, goes to state.json under appsKey: a
// guest's message among them, by its message's key, for a day.

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
// runs under a timeout of its own too (t3code.DefaultTimeout), shorter than
// appsPairWait, so Pair can try a read that failed for a reason that passes,
// a busy store or a slow run, again within it, and the prompt can still
// carry its pairing (t3code.Reader.Pair).
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
	Steers() []t3code.Steer
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
// about: when; whether a guest's words reached it, from a guest's run or a
// running turn the owner steered a guest's message into; and whether a run
// claimed it, which no steered guest's message takes back.
type appAsked struct {
	at      time.Time
	guest   bool
	claimed bool
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
	// job is the reads' timer, which runs every appsEvery while running
	// says the last read found T3 running with a store the reader can read
	// (appRuns), and every appsIdle otherwise. presence last heard the same
	// word.
	job     *sched.Job
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
	// reads without waiting for a read (t3Status). steers are the running
	// turns the reader's last read knew the owner steered a guest's message
	// into (noteSteers), which mark the prompts that fall in them, those
	// the host writes without asking included (steered).
	smu      sync.Mutex
	sessions map[string]time.Time
	turns    map[appTurn]appAsked
	heard    time.Time
	t3       t3code.Status
	t3ok     bool
	steers   []t3code.Steer
	started  bool
	closed   bool

	// server says the agent's last process scan found T3 Code's server
	// running (noteServer), which the reader asks at every read
	// (t3code.Options.Server). It is false until a scan has found one, and
	// after a scan that failed.
	server atomic.Bool

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
		Server: p.server.Load, Table: p.a.table, Zone: p.a.zone(), State: kept.T3Code})
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
	// What state.json kept marks the prompts that come before the first
	// read, as a spooled copy drained at the start does.
	p.noteSteers()
	p.poke()
}

func (p *appsHost) poke() {
	select {
	case p.kick <- struct{}{}:
	default:
	}
}

// noteServer notes what the agent's process scan found of T3 Code's server
// (procs.Options.Listed): procs, its read of the process table on goos, nil
// after a read that failed, and self, the owner whose processes the scan
// reports. T3's server is a process of the app table's app_server role of
// self's, or of any session on Windows, where T3's scheduled service can
// run outside the user's (t3code.ServerIn); a failed scan finds none. When
// the word changes the reader reads at once, so the cadence and presence's
// context follow T3 within a scan.
func (p *appsHost) noteServer(goos string, procs []platform.Proc, self string) {
	found := t3code.ServerIn(goos, procs, self)
	if p.server.Swap(found) != found {
		p.poke()
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
	p.noteSteers()
	p.record(b)
	p.after(err, len(b.Messages) > 0)
}

// prompt writes prompt.submit ev, with text, the prompt as the harness
// delivered it, with write, the adapter host's, and reports what write did.
// It first drops the relay and guest an adapter or a sender put on the
// prompt, and marks guest a prompt whose text opens as T3 Code opens a
// guest's message when it sends it on (t3code.OpensAsGuest), whatever its
// harness, its session or the path it came by. Then, for a prompt the
// reader may know of (wants), it asks the reader whether the prompt pairs
// with a message and whether a guest's run delivered it, before the
// prompt's text reaches the ledger writer, and sets them from the answer
// (DESIGN 5.1). Every copy, asked about or not, is also marked guest when
// it falls in a running turn the owner steered a guest's message into
// (steered) and no run claimed it. A prompt marked guest takes no relay.
// late says the record is lookback's, of a time long past. The records the
// read found go to the ledger after the prompt, so a record that names it
// comes second.
func (p *appsHost) prompt(ev event.Event, text []byte, late bool, write func(event.Event) bool) bool {
	ps, ok := ev.Data.(*event.PromptSubmit)
	if !ok || ps == nil {
		return write(ev)
	}
	// A copy, so the adapter's record isn't changed.
	cp := *ps
	cp.Relay, cp.Guest = "", false
	ev.Data = &cp
	// A guest's words that reach a harness through T3 Code open this way,
	// on whichever path a copy of them comes: one the reader never read,
	// such as one sent while the agent was down, keeps none of them all the
	// same (DESIGN 5.1). An owner who opens a prompt with those words loses
	// that prompt's credit and its text.
	opens := t3code.OpensAsGuest(string(text))
	turn := appTurn{session: ev.SessionKey, turn: cp.TurnKey}
	ask, was := p.wants(ev, late, turn)
	cp.Guest = was.guest || opens
	if !ask {
		// The turn's other copies, and those the reader is never asked
		// about, take a steered guest's mark past the turn's cache, unless
		// a run claimed the turn.
		cp.Guest = cp.Guest || !was.claimed && p.steered(ev)
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
	p.noteSteers()
	// A run claimed the prompt when a guest's run delivered it, or a
	// message's run did: the reader names that message's earlier record, or
	// a record of this read names the prompt.
	claimed := pr.Guest || pr.Relay != "" ||
		slices.ContainsFunc(b.Messages, func(m t3code.Message) bool { return m.Data.Prompt == ev.EventID })
	inSteer := !claimed && p.steered(ev)
	switch {
	case pr.Guest, inSteer:
		// A guest's run delivered it, which the reader may know from an
		// earlier read even when this one failed, or the guest's words
		// reached it as a steer: the mark only costs the prompt credit, and
		// keeps the guest's words off the device.
		cp.Guest = true
	case cp.Guest:
		// Opened as a guest's: no message of the owner's witnesses it.
	case pr.Relay != "":
		cp.Relay = pr.Relay
	}
	// Every mark goes to the turn's other copies, the device test's
	// included, so a copy whose own text doesn't open as a guest's, as one
	// the adapter rebuilt, keeps no more of the guest's words.
	p.noteTurn(turn, cp.Guest, claimed)
	ok = write(ev)
	p.record(b)
	p.after(err, len(b.Messages) > 0 || cp.Relay != "")
	return ok
}

// wants reports whether to ask the reader about prompt ev, of turn turn,
// and what the component noted of the turn when it asked about another of
// its copies: whether a guest's words reached it, and whether a run claimed
// it. A prompt is asked about once per turn, when a hook or a log reader
// saw it, it isn't lookback's, which no read answers for, and an agent
// app's server started its session.
func (p *appsHost) wants(ev event.Event, late bool, turn appTurn) (ask bool, was appAsked) {
	p.smu.Lock()
	defer p.smu.Unlock()
	now := p.now()
	p.forgetTurns(now)
	if a, ok := p.turns[turn]; ok && turn.turn != "" {
		return false, a
	}
	if late || ev.Via != event.ViaHook && ev.Via != event.ViaLog {
		return false, appAsked{}
	}
	if _, ok := p.sessions[ev.SessionKey]; !ok {
		return false, appAsked{}
	}
	if turn.turn != "" {
		p.turns[turn] = appAsked{at: now}
	}
	return true, appAsked{}
}

// noteTurn notes what the asked copy of turn showed: that a guest's words
// reached it (guest), by the reader's answer or by the copy's own opening,
// so the turn's other copies are marked guest too, or that a run claimed it
// (claimed), so no steered guest's message marks them.
func (p *appsHost) noteTurn(turn appTurn, guest, claimed bool) {
	if turn.turn == "" || !guest && !claimed {
		return
	}
	p.smu.Lock()
	defer p.smu.Unlock()
	a := p.turns[turn]
	if a.at.IsZero() {
		a.at = p.now()
	}
	a.guest = a.guest || guest
	a.claimed = a.claimed || claimed
	p.turns[turn] = a
}

// noteSteers takes up the running turns the reader knows the owner steered
// a guest's message into (t3code.Reader.Steers), after a read, or at the
// start from what state.json kept. p.mu is held.
func (p *appsHost) noteSteers() {
	if p.reader == nil {
		return
	}
	steers := p.reader.Steers()
	p.smu.Lock()
	p.steers = steers
	p.smu.Unlock()
}

// steered reports whether prompt ev falls in a running turn the owner
// steered a guest's message into, as the reader's last read said: a prompt
// of one of the turn's sessions, stamped from the later of the message's
// stamp and the turn's start to the turn's end, or after that start while
// the turn runs. Claude Code takes a steer as a prompt of its own in the
// turn's session, which no run of the guest's delivers, so that prompt can
// carry the guest's words though its text doesn't open as a guest's
// (docs/errata/2026-11-23-t3-reader.md, decision 4).
func (p *appsHost) steered(ev event.Event) bool {
	p.smu.Lock()
	defer p.smu.Unlock()
	for _, s := range p.steers {
		if slices.Contains(s.Sessions, ev.SessionKey) && ev.T >= s.From && (s.To == 0 || ev.T <= s.To) {
			return true
		}
	}
	return false
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

// after notes how a read went: while T3 runs with a store the reader can
// read (appRuns) the job reads every appsEvery and presence counts T3 as a
// project context, and otherwise the job reads every appsIdle and presence
// counts none; the reader's status is kept for GET /v1/status (t3Status);
// the log hears once when the reader stops reading for a reason other than
// T3 not running; and what the reader keeps goes to state.json, at once
// when wrote says the read wrote records or paired a prompt. A read the
// agent's stop cut short changes neither cadence, context nor the status.
// p.mu is held.
func (p *appsHost) after(err error, wrote bool) {
	st := p.reader.Status()
	if running := appRuns(err, st); p.ctx.Err() == nil && running != p.running {
		p.running = running
		if p.job != nil {
			every := appsIdle
			if running {
				every = appsEvery
			}
			p.job.Reset(every)
		}
		if p.a.presence != nil {
			n := 0
			if running {
				n = 1
			}
			p.a.presence.AgentApps(n)
		}
	}
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

// appRuns reports whether a read that ended with err and left the reader's
// status st found T3 Code running with a store the reader can read. T3
// runs, by DESIGN 3.3's test, when the agent's last process scan found its
// server and the store the read picked had its -wal and -shm files
// (st.Running), and the reader can read the store when sqlite3 is on PATH
// and the probe passed its schema, with JSON functions to read it with. A
// read kept from the store by what can pass at any moment, a store busy
// with t3 doctor's compaction or a slow read, counts as one that read it.
//
// While T3 runs the job reads every appsEvery, since a row first read more
// than RELAY_SEEN_LAG_S after its stamp witnesses nothing, and presence
// counts T3 as a project context, as a running harness process, so it
// samples the minutes a message from T3's phone or web client reaches back
// into (D-relayed-context). Otherwise the job waits appsIdle, soon enough
// to see any of these change, and sparing a probe of a refused schema every
// 10 s, and T3 makes no context: with no sqlite3 or a schema the reader
// refuses, no relayed credit is possible, and leftover -wal and -shm files
// with no server, which a T3 killed while it held them leaves, are no T3
// running. A T3 run from its source, as node, isn't named in the app table,
// so it gets no project context of its own. Its sessions classify under
// node, as script_runtime, so the agent never asks about their prompts
// (noteSession, wants): its messages are read only at the idle cadence,
// appsIdle, which is as long as RELAY_SEEN_LAG_S, so some age out and the
// rest pair with nothing, and doctor says pairtime doesn't recognize its
// server (t3code.Counts.WithServer).
func appRuns(err error, st t3code.Status) bool {
	var se *t3code.SchemaError
	switch {
	case !st.Running, errors.Is(err, t3code.ErrNotRunning), errors.Is(err, t3code.ErrNoStore),
		errors.Is(err, t3code.ErrNoSQLite3), errors.Is(err, t3code.ErrNoJSON), errors.As(err, &se):
		return false
	}
	return true
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
	// t3code.OffNoStore, OffNotRunning, OffNoSQLite3, OffNoJSON, OffSchema
	// or OffFailed. Problem says more of a refused schema or a failed read.
	Reading bool   `json:"reading"`
	Off     string `json:"off,omitempty"`
	Problem string `json:"problem,omitempty"`
	// Newer says T3's schema is newer than the one the reader was tested
	// against, which it reads anyway.
	Newer bool `json:"newer,omitempty"`
	// Running says T3 Code ran at the last read, by DESIGN 3.3's test: the
	// agent's process scan found its server, and the store had the -wal
	// and -shm files T3 holds open (t3code.Status.Running). Doctor's
	// warning that no session T3 started reached pairtime in a day needs it.
	Running bool `json:"running,omitempty"`
	// NoSQLite3 says the last read found no sqlite3 on the agent's PATH,
	// whether or not T3 ran, so doctor can say how to install it before
	// T3's messages count for nothing.
	NoSQLite3 bool `json:"no_sqlite3,omitempty"`
	// Day is the day the counts are for, YYYY-MM-DD in the agent's zone:
	// Witnessed counts the messages T3 says a person sent that the reader
	// recorded, WithServer those of them it recorded while T3 ran, with a
	// server the app table names, Paired its records that paired with a
	// prompt, Aged the rows the age test turned away, and Ahead the rows it
	// turned away as stamped more than 2 s after the read that decided them
	// (t3code.Counts). Doctor's T3 Code line reads WithServer to tell a T3
	// whose server pairtime doesn't recognize from one whose sessions skip
	// pairtime's hooks.
	Day        string `json:"day"`
	Witnessed  int    `json:"witnessed"`
	WithServer int    `json:"with_server"`
	Paired     int    `json:"paired"`
	Aged       int    `json:"aged"`
	Ahead      int    `json:"ahead"`
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
	r := &t3Reply{Reading: st.Reading, Off: st.Off, Problem: st.Problem, Newer: st.Newer, Running: st.Running,
		NoSQLite3: st.NoSQLite3, Day: st.Day, Witnessed: st.Witnessed, WithServer: st.WithServer, Paired: st.Paired,
		Aged: st.Aged, Ahead: st.Ahead}
	if today := p.now().In(p.a.zone()).Format(time.DateOnly); r.Day != today {
		r.Day, r.Witnessed, r.WithServer, r.Paired, r.Aged, r.Ahead = today, 0, 0, 0, 0, 0
	}
	if !heard.IsZero() {
		r.SessionAt = heard.UnixMilli()
	}
	return appsReply{T3Code: r}
}
