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
// running agent turn, on every minute boundary. Any hook, focus or power
// event takes a sample at once, so the 10 s check before a prompt (SM 2.2
// rule 6) always has data. Idle APIs return the time since the last
// input, so a sample after a pause reconstructs the gap exactly, and gaps
// come from wall-clock values, never from time.Since (CP 2.2).
//
// The OS sources live under internal/platform; ThisOS picks the ones for
// the OS the agent runs on. The focus watcher (WP-OS2), the process
// scanner (WP-OS6) and the hook path (WP-AG3) report to a Sampler through
// Focus, HarnessProcesses and Hook.
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
)

// minGap is the least time between an immediate sample and the sample
// before it. A harness can fire several hooks in the same second, and a
// second sample a moment after the first says nothing new. Periodic and
// power samples are always taken.
const minGap = time.Second

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
	// Keys is the time since the last key press. It is valid only when
	// HasKeys is set: Windows can't tell keys from other input without a
	// keyboard listener, which pairtime doesn't run before its builds are
	// signed (DESIGN 7.2 item 5, 11 risk 18).
	Keys    time.Duration
	HasKeys bool
}

// OS is what the sampler needs from the operating system.
type OS struct {
	// Idle reads the input idle time. Nil means this OS has no source yet,
	// so the sampler records nothing and presence stays unknown (SM 2.1).
	Idle func() (Idle, error)
	// Locked reports whether the session is locked. The sampler asks once,
	// at Start, and follows lock and unlock events from then on. Nil means
	// unlocked.
	Locked func() (bool, error)
	// WatchPower calls fn on every suspend, resume, lock and unlock until
	// the returned stop is called. Nil means no power events.
	WatchPower func(fn func(op event.PowerOp)) (stop func(), err error)
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
	// Record gets every record, in order. It runs on the goroutine or
	// thread that took the record, while the sampler is locked, so it must
	// return quickly and must not call the Sampler. Nil drops records.
	Record func(Record)
	// Seal, when set, is called after every suspend and lock, whether or
	// not a project context is live, so the ledger writer can seal a
	// batch (DESIGN 3.3, 3.6). For a suspend it runs on the thread Windows
	// delivers the event on, which gets about two seconds before the
	// machine sleeps.
	Seal func(op event.PowerOp)
	// Problem gets a description of what is wrong, for doctor and the
	// status endpoint, each time it changes; "" means nothing is.
	Problem func(msg string)
}

// Sampler is the presence and power component. Make one with New and call
// Start. Its methods are safe for concurrent use.
type Sampler struct {
	opts Options

	mu        sync.Mutex
	g         gate
	locked    bool
	started   bool
	stopped   bool
	stopPower func()
	timer     *sched.Job    // nil until a context first goes live
	due       time.Time     // when timer fires; zero while it is stopped
	every     time.Duration // the cadence timer was armed for
	last      time.Time     // when the latest sample was taken
	samples   uint64
	problems  map[string]string
}

// New returns a sampler that does nothing until Start.
func New(opts Options) *Sampler {
	if opts.Sched == nil {
		panic("presence: Options.Sched must be set")
	}
	return &Sampler{opts: opts, problems: make(map[string]string)}
}

// Start reads the session's lock state and starts watching power events.
// A source that fails is reported through Options.Problem, and the sampler
// carries on without it. With no idle source, Start does nothing.
func (s *Sampler) Start() {
	if s.opts.OS.Idle == nil {
		return
	}
	locked, lockErr := false, error(nil)
	if f := s.opts.OS.Locked; f != nil {
		locked, lockErr = f()
	}
	s.mu.Lock()
	if s.started || s.stopped {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.locked = locked
	if lockErr != nil {
		s.setProblem("lock", fmt.Sprintf("couldn't read whether the session is locked, so presence assumes it isn't until the next lock: %v", lockErr))
	}
	s.mu.Unlock()

	if s.opts.OS.WatchPower == nil {
		return
	}
	// Not under s.mu: a watcher may deliver an event before it returns.
	stop, err := s.opts.OS.WatchPower(s.Power)
	s.mu.Lock()
	if err != nil {
		s.setProblem("power", fmt.Sprintf("suspend, resume, lock and unlock go unrecorded: %v", err))
		s.mu.Unlock()
		return
	}
	if s.stopped {
		// Stop ran while the watcher started. stop may wait for an event
		// that is waiting for s.mu, so it runs unlocked.
		s.mu.Unlock()
		stop()
		return
	}
	s.stopPower = stop
	s.mu.Unlock()
}

// Stop stops the timer and the power watch. The sampler records nothing
// after it returns.
func (s *Sampler) Stop() {
	s.mu.Lock()
	s.stopped = true
	if s.timer != nil {
		s.timer.Stop()
	}
	s.due, s.every = time.Time{}, 0
	stop := s.stopPower
	s.stopPower = nil
	s.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// Focus reports that an app of class c took focus. A terminal, editor or
// agent app makes a project context live, and it stays live for
// ContextWindow after one loses focus. A live context takes a sample at
// once.
func (s *Sampler) Focus(c event.AppClass) {
	s.update(func(now time.Time) { s.g.focus(now, c) }, true)
}

// Hook reports a hook or track message of kind k from the harness session
// with key session (an opaque key, or "" when the message names none). It
// makes a project context live, starts a turn on a prompt and ends it on a
// stop, an interrupt or a session end. A live context takes a sample at
// once.
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
	if !s.started || s.stopped {
		s.mu.Unlock()
		return
	}
	now := time.Now()
	switch op {
	case event.PowerLock:
		s.locked = true
	case event.PowerUnlock:
		s.locked = false
	}
	if s.g.interval(wall(now)) != 0 {
		s.emit(Record{At: now, Data: &event.OSPower{Op: op}})
		s.sample(now, false)
	}
	s.arm(now)
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
	// Problems are what is wrong, by source.
	Problems []string
}

// Status returns the sampler's state.
func (s *Sampler) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	every := s.g.interval(wall(time.Now()))
	return Status{Live: every != 0, Every: every, Locked: s.locked, Samples: s.samples, Problems: s.problemList()}
}

// update applies change to the gate, takes a sample at once if a context
// is live and either sampleNow is set or the context has just gone live,
// and re-arms the timer for the cadence that follows.
func (s *Sampler) update(change func(now time.Time), sampleNow bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || s.stopped {
		return
	}
	now := time.Now()
	wasLive := s.every != 0
	change(wall(now))
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
// immediate sample is skipped when the last one is less than minGap old.
func (s *Sampler) sample(now time.Time, immediate bool) {
	if immediate && !s.last.IsZero() {
		if since := wall(now).Sub(wall(s.last)); since >= 0 && since < minGap {
			return
		}
	}
	idle, err := s.opts.OS.Idle()
	if err != nil {
		s.setProblem("idle", fmt.Sprintf("couldn't read the input idle time, so presence has gaps: %v", err))
		return
	}
	s.setProblem("idle", "")
	p := &event.OSPresence{IdleAnyS: seconds(idle.Any), Locked: s.locked}
	if idle.HasKeys {
		keys := seconds(idle.Keys)
		p.IdleKeysS = &keys
	}
	s.last = now
	s.samples++
	s.g.sampled(wall(now), idle.Any)
	s.emit(Record{At: now, Data: p})
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
