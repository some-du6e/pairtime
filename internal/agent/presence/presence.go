// SPDX-License-Identifier: AGPL-3.0-or-later

// Package presence is the agent's presence and power component (DESIGN
// 3.3). It samples the OS's input idle time into os.presence records and
// turns suspend, resume, lock and unlock into os.power records, but only
// while a project context is live: a harness session process is running,
// or a terminal, editor or agent app had focus in the last 600 s. Outside
// one, nothing is sampled or recorded, so pairtime never records general
// computer use (D14).
//
// In a context it samples on every 15-second slot boundary of the wall
// clock (:00, :15, :30, :45), because SM 2.1 decides each slot's input
// from the sample that ends it, and after 600 s with no input and no
// running agent turn, on every minute boundary. Those three numbers are
// SAMPLE_S, CONTEXT_IDLE_S and SLOW_SAMPLE_S from the parameter table
// (pkg/params). Any hook, focus or power event takes a sample at once, so
// the 10 s check before a prompt (SM 2.2 rule 6) always has data. Idle
// APIs return the time since the last input, so a sample after a pause
// reconstructs the gap exactly, and gaps come from wall-clock values,
// never from time.Since (CP 2.2).
//
// The OS sources live under internal/platform. ThisOS picks the ones for
// Windows. On macOS and Linux presence shares one connection to the OS
// with the focus component, so the agent opens it and builds the sources
// with MacOS or LinuxOS (WP-OS9). The focus watcher (WP-OS2), the process
// scanner (WP-OS6) and the hook path (WP-AG3) report to a Sampler through
// FocusAt, HarnessProcesses and Hook.
package presence

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/sched"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/params"
)

// minGap is the least time between an immediate sample and the sample
// before it, unless it shows input since that one. A harness can fire
// several hooks in the same second, and a sample a moment after the last
// that shows the same last input says nothing new. One that shows newer
// input is kept: it may be the only record of the Enter that submitted a
// prompt, which SM 2.2 rule 6 looks for. Periodic and power samples are
// always taken.
const minGap = time.Second

// inputSlop is how much later a reading may place the same input than the
// reading before it did. Windows counts idle time in GetTickCount's steps
// of about 16 ms, so a reading can be a step off, plus the moment between
// the sampler reading the clock and the OS reading its tick.
const inputSlop = 32 * time.Millisecond

// slack keeps a timer that fires a moment early, which the scheduler
// allows, from arming itself again for the boundary it is about to reach.
const slack = time.Second

// timerName is the sampler's job in the agent's scheduler.
const timerName = "presence.sample"

// Idle is one reading of the OS's input idle time.
type Idle struct {
	// Any is the time since the last keyboard or mouse input in this
	// session.
	Any time.Duration
	// Keys is the time since the last key-down the OS saw. It is valid only
	// when HasKeys is set: Windows and Linux can't tell keys from other
	// input without a keyboard listener, which pairtime doesn't run before
	// its builds are signed (DESIGN 7.2 item 5, 11 risk 18). macOS gives it,
	// but its count includes key-downs other processes post, so it says a
	// key event reached the session, not that a person pressed a key
	// (internal/platform/darwin, D-macos-hid-posted).
	Keys    time.Duration
	HasKeys bool
}

// OS is what the sampler needs from the operating system.
type OS struct {
	// Idle reads the input idle time. Nil means this OS has no source yet,
	// so the sampler records nothing and presence stays unknown (SM 2.1);
	// its power watches still run, so suspends and locks still seal.
	Idle func() (Idle, error)
	// Locked reports whether the session is locked. The sampler asks once,
	// at Start, after the lock watch has started, and follows lock and
	// unlock events from then on. Nil means unlocked.
	Locked func() (bool, error)
	// WatchSuspend calls fn on every suspend and resume until the returned
	// stop is called. Nil means none are seen.
	WatchSuspend func(fn func(op event.PowerOp)) (stop func(), err error)
	// WatchLock calls fn on every lock and unlock of the session until the
	// returned stop is called. Nil means none are seen.
	WatchLock func(fn func(op event.PowerOp)) (stop func(), err error)
}

// ThisOS returns the sources of the OS the agent runs on.
func ThisOS() OS {
	return thisOS()
}

// Record is one os.presence or os.power event, before the agent gives it
// an envelope.
type Record struct {
	// At is when it was taken, from time.Now: its wall clock goes in the
	// event's t, and its monotonic reading can go in mono.
	At time.Time
	// Data is an *event.OSPresence or an *event.OSPower.
	Data event.Payload
}

// Options configure a Sampler.
type Options struct {
	// Sched runs the sampler's timer. It must be set.
	Sched *sched.Scheduler
	OS    OS
	// Params is the parameter table SAMPLE_S, SLOW_SAMPLE_S and
	// CONTEXT_IDLE_S come from, which should be the one the scorer runs
	// with. Nil means the newest table pkg/params embeds, until the agent
	// picks a scoring version.
	Params *params.Table
	// Record gets every record, in order. It runs on the goroutine or
	// thread that took the record, while the sampler is locked, so it must
	// return quickly and must not call the Sampler. Nil drops records.
	Record func(Record)
	// Seal, when set, is called after every suspend and lock, whether or
	// not a project context is live and whether or not the OS gives idle
	// time, so the ledger writer can seal a batch (DESIGN 3.3, 3.6). For a
	// suspend it runs before the machine sleeps: on Windows on the thread
	// the event comes on, which gets about two seconds, on macOS inside the
	// Loop's Power call, and on Linux before logind's delay lock is let go.
	Seal func(op event.PowerOp)
	// Problem gets a description of what is wrong, for doctor and the
	// status endpoint, each time it changes; "" means nothing is.
	Problem func(msg string)
}

// Sampler is the presence and power component. Make one with New and call
// Start. Its methods are safe for concurrent use.
type Sampler struct {
	opts Options

	mu sync.Mutex
	g  gate
	// locked is the session's lock state. lockEvent is set once a lock or
	// unlock has set it, after which the state Start read no longer counts.
	locked    bool
	lockEvent bool
	// watching is set when Start starts the power watches, and started
	// once it has read the lock state and sampling begins.
	watching bool
	started  bool
	stopped  bool
	stops    []func()        // stop the power watches
	watched  map[string]bool // the power watches that run, by source
	timer    *sched.Job      // nil until a context first goes live
	due      time.Time       // when timer fires; zero while it is stopped
	every    time.Duration   // the cadence timer was armed for
	last     time.Time       // the time of the latest sample
	// recAt is the time of the latest record, the sampler's own or one a
	// FocusAt caller wrote, which no later record goes before.
	recAt    time.Time
	samples  uint64
	problems map[string]string
}

// New returns a sampler that does nothing until Start.
func New(opts Options) *Sampler {
	if opts.Sched == nil {
		panic("presence: Options.Sched must be set")
	}
	tab := opts.Params
	if tab == nil {
		tables := params.Tables()
		tab = tables[len(tables)-1]
	}
	return &Sampler{opts: opts, g: gate{cadence: cadenceOf(tab)}, problems: make(map[string]string), watched: make(map[string]bool)}
}

// Start starts the power watches, reads the session's lock state and
// starts sampling. A source that fails is reported through
// Options.Problem, and the sampler carries on without it. Focus, Hook and
// HarnessProcesses may come before Start: the gate keeps what they report,
// and sampling starts with Start.
//
// With no idle source, presence is unknown (SM 2.1), so Start starts the
// power watches and nothing else: nothing is sampled or recorded, but every
// suspend and lock still calls Options.Seal, because DESIGN 3.3 and 3.6
// seal the open batch on each one whether or not idle time is known.
func (s *Sampler) Start() {
	s.mu.Lock()
	if s.watching || s.stopped {
		s.mu.Unlock()
		return
	}
	s.watching = true
	s.mu.Unlock()

	s.watch("suspend", "suspend and resume go unrecorded", s.opts.OS.WatchSuspend)
	s.watch("lock", "lock and unlock go unrecorded", s.opts.OS.WatchLock)
	if s.opts.OS.Idle == nil {
		// started stays false, so Power seals but records nothing, and no
		// sample is ever taken.
		return
	}

	// The lock state is read only now, because Windows tells a new
	// registration nothing about the state it starts in: read before the
	// watch, a lock in between would be lost. From here on every lock and
	// unlock reaches Power, and the latest one is never older than the
	// read, so the read counts only if none has come.
	locked, lockErr := false, error(nil)
	if f := s.opts.OS.Locked; f != nil {
		locked, lockErr = f()
	}
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	if !s.lockEvent {
		s.locked = locked
		if lockErr != nil {
			s.setProblem("lock-state", fmt.Sprintf("couldn't read whether the session is locked, so presence takes it as unlocked until the next lock or unlock: %v", lockErr))
		}
	}
	s.started = true
	s.mu.Unlock()
	// A context that went live before Start gets its first sample now.
	s.update(func(time.Time) {}, false)
}

// watch starts one power watch, or reports why it couldn't.
func (s *Sampler) watch(source, loss string, start func(func(event.PowerOp)) (func(), error)) {
	if start == nil {
		return
	}
	// Not under s.mu: a watch may deliver an event before it returns.
	stop, err := start(s.Power)
	s.mu.Lock()
	if err != nil {
		s.setProblem(source, fmt.Sprintf("%s: %v", loss, err))
		s.mu.Unlock()
		return
	}
	if s.stopped {
		// Stop ran while the watch started. stop may wait for an event
		// that is waiting for s.mu, so it runs unlocked.
		s.mu.Unlock()
		stop()
		return
	}
	s.stops = append(s.stops, stop)
	s.watched[source] = true
	s.mu.Unlock()
}

// Stop stops the timer and the power watches. The sampler records nothing
// after it returns.
func (s *Sampler) Stop() {
	s.mu.Lock()
	s.stopped = true
	if s.timer != nil {
		s.timer.Stop()
	}
	s.due, s.every = time.Time{}, 0
	stops := s.stops
	s.stops = nil
	clear(s.watched)
	s.mu.Unlock()
	for _, stop := range stops {
		stop()
	}
}

// Focus reports that an app of class c took focus. A terminal, editor or
// agent app makes a project context live, and it stays live for
// ContextWindow after one loses focus. A live context takes a sample at
// once.
func (s *Sampler) Focus(c event.AppClass) {
	s.FocusAt(time.Now(), c, nil)
}

// FocusAt is Focus for a caller that records the change itself, as the
// focus component writes an os.focus record of each one (WP-OS2). at is
// when the change was seen, from time.Now. Under the sampler's lock the
// gate hears the change, then write, unless it is nil, gets the time the
// change is dated and whether a project context is live, and then a live
// context takes its sample, dated that same time. So the caller's record
// and the sample share a millisecond, with the sample after the record,
// as the scorer expects of a sample taken at an event
// (testdata/timelines/README.md).
//
// The change is dated at, or at the time of the latest record if that is
// later, so records never go back in time. The sample reads the idle time
// when it is taken and moves it back to the time it is dated, so it still
// says when the last input was. write runs while the sampler is locked,
// so it must return quickly and must not call the Sampler. Once the
// sampler has stopped, write gets false.
func (s *Sampler) FocusAt(at time.Time, c event.AppClass, write func(at time.Time, live bool)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recAt.After(at) {
		at = s.recAt
	}
	if s.stopped {
		if write != nil {
			write(at, false)
		}
		return
	}
	now := time.Now()
	s.g.focus(wall(now), c)
	live := s.g.interval(wall(now)) != 0
	if write != nil {
		write(at, live)
		s.recAt = at
	}
	if !s.started {
		return
	}
	if live {
		s.sampleAt(at, now, true)
	}
	s.arm(now)
}

// Hook reports a hook or track message of kind k from the harness session
// with key session (an opaque key, or "" when the message names none). It
// makes a project context live, starts a turn on a prompt, a tool call or a
// permission request, and ends it on a stop, an interrupt or a session end.
// A live context takes a sample at once.
func (s *Sampler) Hook(session string, k event.Kind) {
	s.update(func(now time.Time) { s.g.hook(now, session, k) }, true)
}

// HarnessProcesses reports how many harness session processes are
// running, as the process scanner counted them. Any makes a project
// context live.
func (s *Sampler) HarnessProcesses(n int) {
	s.update(func(time.Time) { s.g.setProcs(n) }, false)
}

// Power reports a suspend, resume, lock or unlock. The OS's watcher calls
// it, and so may anything else that learns of one. In a live context it
// records the event and a sample after it; suspends and locks call
// Options.Seal in any case.
func (s *Sampler) Power(op event.PowerOp) {
	s.mu.Lock()
	if !s.watching || s.stopped {
		// Before Start, the lock state Start reads is the one that counts.
		s.mu.Unlock()
		return
	}
	now := time.Now()
	switch op {
	case event.PowerLock, event.PowerUnlock:
		// The lock state is known from here on, whatever Start read.
		s.locked = op == event.PowerLock
		s.lockEvent = true
		s.setProblem("lock-state", "")
	}
	// While Start is still reading the lock state, an event only sets the
	// state its first sample shows.
	if s.started {
		if s.g.interval(wall(now)) != 0 {
			s.emit(Record{At: now, Data: &event.OSPower{Op: op}})
			s.sample(now, false)
		}
		s.arm(now)
	}
	s.mu.Unlock()
	if s.opts.Seal != nil && (op == event.PowerSuspend || op == event.PowerLock) {
		s.opts.Seal(op)
	}
}

// Status describes the sampler, for doctor and the status endpoint.
type Status struct {
	// Live is set while a project context is live.
	Live bool
	// Every is the current cadence; 0 outside a context.
	Every   time.Duration
	Locked  bool
	Samples uint64
	// Watching names the power watches that run, "suspend" and "lock", in
	// that order. A watch the OS has none of, or that failed to start, is
	// missing, and so is every watch before Start and after Stop.
	Watching []string
	// Problems are what is wrong, by source.
	Problems []string
}

// Status returns the sampler's state.
func (s *Sampler) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	every := s.g.interval(wall(time.Now()))
	return Status{Live: every != 0, Every: every, Locked: s.locked, Samples: s.samples,
		Watching: s.watchingList(), Problems: s.problemList()}
}

// watchingList returns the names of the power watches that run, in the
// order Start starts them.
func (s *Sampler) watchingList() []string {
	var names []string
	for _, name := range []string{"suspend", "lock"} {
		if s.watched[name] {
			names = append(names, name)
		}
	}
	return names
}

// update applies change to the gate, takes a sample at once if a context
// is live and either sampleNow is set or the context has just gone live,
// and re-arms the timer for the cadence that follows.
func (s *Sampler) update(change func(now time.Time), sampleNow bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	now := time.Now()
	wasLive := s.every != 0
	change(wall(now))
	if !s.started {
		return
	}
	if s.g.interval(wall(now)) != 0 && (sampleNow || !wasLive) {
		s.sample(now, true)
	}
	s.arm(now)
}

// tick is the timer's job: a sample on a slot boundary, if the context is
// still live, and the timer armed for the next boundary.
func (s *Sampler) tick(time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.due = time.Time{}
	if !s.started || s.stopped {
		return
	}
	now := time.Now()
	if s.g.interval(wall(now)) != 0 {
		s.sample(now, false)
	}
	s.arm(now)
}

// sample reads the idle time and records an os.presence sample. An
// immediate sample that would repeat the last one is left out (minGap).
func (s *Sampler) sample(now time.Time, immediate bool) {
	s.sampleAt(now, now, immediate)
}

// sampleAt is sample for a sample dated at, which may be a moment before
// now, when the idle time is read: FocusAt dates a focus change's sample
// at the time of the caller's record. The idle times are moved back by
// the difference, so the sample still says when the last input was. An
// input after at shows as idle 0, the most a sample dated at can say.
func (s *Sampler) sampleAt(at, now time.Time, immediate bool) {
	idle, err := s.opts.OS.Idle()
	if err != nil {
		s.setProblem("idle", fmt.Sprintf("couldn't read the input idle time, so presence has gaps: %v", err))
		return
	}
	s.setProblem("idle", "")
	if immediate && s.repeats(now, idle.Any) {
		return
	}
	late := max(now.Sub(at), 0)
	p := &event.OSPresence{IdleAnyS: seconds(idle.Any - late), Locked: s.locked}
	if idle.HasKeys {
		keys := seconds(idle.Keys - late)
		p.IdleKeysS = &keys
	}
	s.last = at
	s.samples++
	s.g.sampled(wall(now), idle.Any)
	s.emit(Record{At: at, Data: p})
}

// repeats reports whether a sample at now that shows idle since the last
// input would repeat the last sample: it comes less than minGap after that
// one and shows no input since, allowing for inputSlop.
func (s *Sampler) repeats(now time.Time, idle time.Duration) bool {
	if s.last.IsZero() {
		return false
	}
	if since := wall(now).Sub(wall(s.last)); since < 0 || since >= minGap {
		return false
	}
	return !wall(now).Add(-max(idle, 0)).After(s.g.input.Add(inputSlop))
}

// arm sets the timer for the next boundary of the cadence the gate wants,
// or stops it outside a context. A timer already armed for that cadence
// is left alone.
func (s *Sampler) arm(now time.Time) {
	now = wall(now)
	every := s.g.interval(now)
	if every == 0 {
		if s.timer != nil && !s.due.IsZero() {
			s.timer.Stop()
		}
		s.due, s.every = time.Time{}, 0
		return
	}
	if every == s.every && !s.due.IsZero() {
		return
	}
	next := boundaryAfter(now.Add(slack), every)
	if s.timer == nil {
		s.timer = s.opts.Sched.After(timerName, next.Sub(now), s.tick)
	} else {
		s.timer.Reset(next.Sub(now))
	}
	s.due, s.every = next, every
}

func (s *Sampler) emit(r Record) {
	if r.At.After(s.recAt) {
		s.recAt = r.At
	}
	if s.opts.Record != nil {
		s.opts.Record(r)
	}
}

// setProblem records a problem from source, or clears it when msg is "",
// and passes the list on when it changed.
func (s *Sampler) setProblem(source, msg string) {
	if s.problems[source] == msg {
		return
	}
	if msg == "" {
		delete(s.problems, source)
	} else {
		s.problems[source] = msg
	}
	if s.opts.Problem != nil {
		s.opts.Problem(strings.Join(s.problemList(), "; "))
	}
}

// problemList returns the problems in the order of their sources' names.
func (s *Sampler) problemList() []string {
	var msgs []string
	for _, source := range slices.Sorted(maps.Keys(s.problems)) {
		msgs = append(msgs, s.problems[source])
	}
	return msgs
}

// boundaryAfter returns the first multiple of every, counted from the Unix
// epoch, after t. For 15 s and 60 s those are SM's slot boundaries.
func boundaryAfter(t time.Time, every time.Duration) time.Time {
	n, e := t.UnixNano(), int64(every)
	k := n / e
	if n < 0 && n%e != 0 {
		k-- // round down, for times before 1970
	}
	return time.Unix(0, (k+1)*e).In(t.Location())
}

// seconds converts an idle time to the seconds os.presence carries, to the
// millisecond the idle APIs give.
func seconds(d time.Duration) float64 {
	return float64(max(d, 0).Milliseconds()) / 1000
}

// wall strips t's monotonic reading, so arithmetic on it uses the wall
// clock, which counts time asleep on every OS (CP 2.2).
func wall(t time.Time) time.Time {
	return t.Round(0)
}
