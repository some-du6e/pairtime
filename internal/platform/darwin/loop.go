// SPDX-License-Identifier: AGPL-3.0-or-later

package darwin

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
)

// SuspendWait is how long a Loop holds a sleep for the handler's
// Power(suspend) to return before it lets the Mac sleep. macOS waits 30 s
// for the answer and then sleeps anyway (CP 2.2), and sealing a ledger batch
// takes far less than this.
const SuspendWait = 5 * time.Second

// queueLen is how many events can wait for the handler. The OS callbacks
// never wait for room, so a handler this far behind loses events, and
// Status.Dropped counts them.
const queueLen = 64

var (
	// ErrRunning means another Loop is running in this process. The OS
	// callbacks find the Loop through a package variable, so there is one at
	// a time.
	ErrRunning = errors.New("darwin: another Loop is running in this process")
	// errRunTwice means Run was called on a Loop that has run already.
	errRunTwice = errors.New("darwin: Loop.Run called twice")
)

// App is the app that became frontmost, as NSWorkspace reports it. Its
// fields stay on the device (see the package doc).
type App struct {
	// PID is the app's process id, where the walk for the harness sessions
	// it hosts starts (SM 3.2).
	PID int
	// BundleID is the app's bundle identifier, such as
	// com.googlecode.iterm2, or "" for a process without a bundle.
	BundleID string
	// Executable is the path of the app's executable, or "" when
	// NSWorkspace doesn't know it.
	Executable string
}

// Handler gets a Loop's events, one call at a time, in the order macOS
// sent the notifications, on a goroutine the Loop starts, never on the run
// loop's thread. A slow handler delays the events after it, not macOS. at
// is the wall-clock time the notification arrived, with no monotonic
// reading, so gaps computed from it count time asleep (CP 2.2).
type Handler interface {
	// Power reports a suspend, resume, lock or unlock. The Mac doesn't
	// sleep until Power(suspend) returns or SuspendWait has passed, so a
	// handler can seal the open batch there (DESIGN 3.3).
	Power(at time.Time, op event.PowerOp)
	// Focus reports the app that became frontmost. Run calls it once at the
	// start for the app in front then, when NSWorkspace knows it.
	Focus(at time.Time, app App)
}

// Status says which notifications a running Loop receives, for doctor.
type Status struct {
	// MainThread is true when the Loop runs on the process's main thread,
	// where macOS delivers screen lock and app switch notifications.
	MainThread bool
	// Power, Lock and Focus are nil when IOKit sleep and wake messages, the
	// screen lock notifications and NSWorkspace activations are registered,
	// and say what failed otherwise. Lock and Focus being nil doesn't mean
	// their notifications arrive off the main thread.
	Power, Lock, Focus error
	// Dropped counts events lost because the handler fell queueLen behind.
	Dropped uint64
}

// backend is the OS side of a Loop. The darwin one calls the frameworks;
// tests use fakes. A Loop calls open, run and close on one locked thread.
type backend interface {
	// open registers every notification source on the calling thread and
	// says which work. From then on the sources' callbacks call the Loop's
	// powerMessage, lockChanged and activated; open itself may call
	// seedLock and activated.
	open(l *Loop) Status
	// run services the sources until stop is called. It returns at once if
	// stop came first.
	run()
	// stop makes run return. Any goroutine may call it, any number of times.
	stop()
	// close unregisters what open registered.
	close()
	// allowPowerChange answers the IOKit message whose argument was id.
	allowPowerChange(id uintptr)
}

// running is set while a Loop runs; see ErrRunning.
var running atomic.Bool

// Loop delivers macOS power and focus notifications to a Handler. Make one
// with NewLoop and call Run once.
type Loop struct {
	h          Handler
	newBackend func() (backend, error)
	ready      chan struct{}
	readyOnce  sync.Once
	ran        atomic.Bool

	mu     sync.Mutex
	status Status
	b      backend
	q      *queue

	// fmu guards the filters, which OS callbacks update.
	fmu     sync.Mutex
	power   powerFilter
	lastApp App
	haveApp bool
}

// NewLoop returns a Loop that delivers to h.
func NewLoop(h Handler) *Loop {
	return &Loop{h: h, newBackend: newBackend, ready: make(chan struct{})}
}

// Ready is closed once Run has registered its sources and Status says
// which work, or once Run has returned without getting that far.
func (l *Loop) Ready() <-chan struct{} {
	return l.ready
}

// Status reports which sources a running Loop has. Before Ready it is the
// zero Status.
func (l *Loop) Status() Status {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.status
	if l.q != nil {
		st.Dropped = l.q.dropped.Load()
	}
	return st
}

// Run registers for sleep, wake, screen lock and app activation
// notifications on the calling goroutine's OS thread and delivers them to
// the handler until ctx is done; then it unregisters, waits for the handler
// to finish the queued events and returns nil. It locks the calling
// goroutine to its OS thread while it runs; see the package doc for why the
// agent calls it from the main goroutine.
//
// Run returns ErrRunning while another Loop runs, errors.ErrUnsupported
// anywhere but macOS, and an error when no source could be registered.
// When only some fail, it runs with the rest, and Status says which.
func (l *Loop) Run(ctx context.Context) error {
	defer l.readyOnce.Do(func() { close(l.ready) })
	if !l.ran.CompareAndSwap(false, true) {
		return errRunTwice
	}
	if !running.CompareAndSwap(false, true) {
		return ErrRunning
	}
	defer running.Store(false)
	b, err := l.newBackend()
	if err != nil {
		return err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	q := newQueue(l.h)
	l.mu.Lock()
	l.b, l.q = b, q
	l.mu.Unlock()
	st := b.open(l)
	l.mu.Lock()
	l.status = st
	l.mu.Unlock()
	l.readyOnce.Do(func() { close(l.ready) })

	if st.Power != nil && st.Lock != nil && st.Focus != nil {
		b.close()
		q.close()
		return fmt.Errorf("darwin: no notification source works: power: %w; lock: %w; focus: %w", st.Power, st.Lock, st.Focus)
	}
	stopOnDone := context.AfterFunc(ctx, b.stop)
	b.run()
	stopOnDone()
	b.close()
	q.close()
	return nil
}

// powerMessage handles one IOKit system power message, from the run loop's
// thread. id is the message's argument, which IOAllowPowerChange takes back.
func (l *Loop) powerMessage(at time.Time, msg uint32, id uintptr) {
	a := decodePower(msg)
	if a.op != "" && l.pass(a.op) {
		d := delivery{at: at, op: a.op}
		if a.ack {
			d.returned = make(chan struct{})
		}
		if l.q.send(d) && d.returned != nil {
			t := time.NewTimer(SuspendWait)
			select {
			case <-d.returned:
			case <-t.C:
			}
			t.Stop()
		}
	}
	if a.ack {
		l.b.allowPowerChange(id)
	}
}

// lockChanged handles a screen lock or unlock notification.
func (l *Loop) lockChanged(at time.Time, locked bool) {
	op := event.PowerUnlock
	if locked {
		op = event.PowerLock
	}
	if l.pass(op) {
		l.q.send(delivery{at: at, op: op})
	}
}

// seedLock records whether the screen was locked when the Loop started, so
// the first notification that repeats that state isn't reported.
func (l *Loop) seedLock(locked bool) {
	l.fmu.Lock()
	defer l.fmu.Unlock()
	l.power.lock = event.PowerUnlock
	if locked {
		l.power.lock = event.PowerLock
	}
}

// activated handles an app activation. Activating the app that is already
// frontmost changes nothing, so it isn't reported.
func (l *Loop) activated(at time.Time, app App) {
	l.fmu.Lock()
	same := l.haveApp && l.lastApp == app
	l.lastApp, l.haveApp = app, true
	l.fmu.Unlock()
	if !same {
		l.q.send(delivery{at: at, app: app, focus: true})
	}
}

func (l *Loop) pass(op event.PowerOp) bool {
	l.fmu.Lock()
	defer l.fmu.Unlock()
	return l.power.pass(op)
}

// IOKit system power messages (IOMessage.h). iokit_common_msg(n) is
// sys_iokit | sub_iokit_common | n, where sys_iokit is 0x38 << 26 and
// sub_iokit_common is 0.
const (
	msgCanSystemSleep     uint32 = 0xE0000270 // kIOMessageCanSystemSleep
	msgSystemWillSleep    uint32 = 0xE0000280 // kIOMessageSystemWillSleep
	msgSystemWillNotSleep uint32 = 0xE0000290 // kIOMessageSystemWillNotSleep
	msgSystemHasPoweredOn uint32 = 0xE0000300 // kIOMessageSystemHasPoweredOn
	msgSystemWillRestart  uint32 = 0xE0000310 // kIOMessageSystemWillRestart
	msgSystemWillPowerOn  uint32 = 0xE0000320 // kIOMessageSystemWillPowerOn
)

// powerAction is what a Loop does with one IOKit message.
type powerAction struct {
	op  event.PowerOp // the os.power op to report, or ""
	ack bool          // whether to answer with IOAllowPowerChange
}

// decodePower maps an IOKit system power message to its action. Only
// kIOMessageCanSystemSleep and kIOMessageSystemWillSleep want an answer
// (QA1340); every other message is information.
func decodePower(msg uint32) powerAction {
	switch msg {
	case msgCanSystemSleep:
		// macOS asks before an idle sleep. pairtime never keeps a Mac awake.
		return powerAction{ack: true}
	case msgSystemWillSleep:
		return powerAction{op: event.PowerSuspend, ack: true}
	case msgSystemHasPoweredOn:
		return powerAction{op: event.PowerResume}
	}
	// msgSystemWillNotSleep follows a vetoed idle-sleep query,
	// msgSystemWillPowerOn comes before msgSystemHasPoweredOn on the same
	// wake, and a restart stops the agent anyway.
	return powerAction{}
}

// powerFilter drops a power op that repeats the state already reported:
// a second suspend with no resume between, or an unlock while unlocked.
type powerFilter struct {
	sleep, lock event.PowerOp // the last op of each pair, or "" for none yet
}

func (f *powerFilter) pass(op event.PowerOp) bool {
	last := &f.sleep
	if op == event.PowerLock || op == event.PowerUnlock {
		last = &f.lock
	}
	if *last == op {
		return false
	}
	*last = op
	return true
}

// delivery is one event on its way to the handler.
type delivery struct {
	at    time.Time
	op    event.PowerOp // for Power
	focus bool          // for Focus, with app
	app   App
	// returned, when set, is closed once the handler has returned.
	returned chan struct{}
}

// queue carries events from the OS callbacks to the handler's goroutine.
type queue struct {
	h       Handler
	ch      chan delivery
	done    chan struct{} // closed when the handler's goroutine exits
	dropped atomic.Uint64

	mu     sync.Mutex // guards closed and sends on ch
	closed bool
}

func newQueue(h Handler) *queue {
	q := &queue{h: h, ch: make(chan delivery, queueLen), done: make(chan struct{})}
	go q.serve()
	return q
}

func (q *queue) serve() {
	defer close(q.done)
	for d := range q.ch {
		q.call(d)
	}
}

func (q *queue) call(d delivery) {
	if d.returned != nil {
		defer close(d.returned)
	}
	if d.focus {
		q.h.Focus(d.at, d.app)
	} else {
		q.h.Power(d.at, d.op)
	}
}

// send queues d without waiting and reports whether it did. An OS callback
// must not wait for the handler, so a full queue drops d and counts it.
func (q *queue) send(d delivery) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	select {
	case q.ch <- d:
		return true
	default:
		q.dropped.Add(1)
		return false
	}
}

// close returns once the handler has had every queued event. Later sends
// are dropped without counting.
func (q *queue) close() {
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		close(q.ch)
	}
	q.mu.Unlock()
	<-q.done
}
