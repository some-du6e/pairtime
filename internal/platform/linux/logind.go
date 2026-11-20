// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package linux

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/some-du6e/pairtime/pkg/event"
)

// systemd-logind's D-Bus API (org.freedesktop.login1(5), systemd 262; CP
// 2.2). elogind, which non-systemd distributions use, exports the same.
const (
	logindName   = "org.freedesktop.login1"
	logindPath   = dbus.ObjectPath("/org/freedesktop/login1")
	managerIface = "org.freedesktop.login1.Manager"
	sessionIface = "org.freedesktop.login1.Session"
	userIface    = "org.freedesktop.login1.User"
	propsIface   = "org.freedesktop.DBus.Properties"
	busName      = "org.freedesktop.DBus"
)

// callWait bounds each call to logind. It answers at once; a bus that
// doesn't is broken, and pairtime goes on without what it asked for.
const callWait = 2 * time.Second

// logind reports suspend and resume from the Manager's PrepareForSleep
// signal, and lock and unlock from the LockedHint property of the user's
// session. Before each sleep it holds a delay lock, so the agent can seal
// the last minutes first (DESIGN 3.3).
//
// The session's Lock and Unlock signals aren't used: they ask a screen
// locker to act, and say nothing about whether one did. Lockers that do
// lock set LockedHint (GNOME Shell and KDE's kscreenlocker do), which
// logind announces with PropertiesChanged. A locker that doesn't leaves
// lock unknown rather than stuck on a lock no unlock ever follows.
type logind struct {
	conn    *dbus.Conn
	session dbus.ObjectPath // "" when the user's session wasn't found
	track   *powerTracker
	sigs    chan *dbus.Signal
	log     *slog.Logger

	// owner is logind's unique bus name: only its signals count. Anyone
	// on the bus can send a signal addressed to this connection, but the
	// bus stamps the true sender on it.
	owner  string
	locked atomic.Bool
}

// openLogind subscribes to logind's signals, finds the session, reads its
// lock state and takes the first delay lock. It fails only when logind
// can't be reached; without a session it still reports suspend and
// resume, and lockErr says why lock state is unknown.
func openLogind(ctx context.Context, conn *dbus.Conn, readyTimeout time.Duration, log *slog.Logger) (l *logind, lockErr error, err error) {
	ctx, cancel := context.WithTimeout(ctx, callWait)
	defer cancel()
	l = &logind{conn: conn, log: log, sigs: make(chan *dbus.Signal, 16)}
	if err := conn.BusObject().CallWithContext(ctx, busName+".GetNameOwner", 0, logindName).Store(&l.owner); err != nil {
		return nil, nil, errors.New("logind isn't on the system bus")
	}
	conn.Signal(l.sigs)
	rules := [][]dbus.MatchOption{
		{
			dbus.WithMatchSender(logindName),
			dbus.WithMatchObjectPath(logindPath),
			dbus.WithMatchInterface(managerIface),
			dbus.WithMatchMember("PrepareForSleep"),
		},
		{
			dbus.WithMatchSender(busName),
			dbus.WithMatchInterface(busName),
			dbus.WithMatchMember("NameOwnerChanged"),
			dbus.WithMatchArg(0, logindName),
		},
	}
	for _, rule := range rules {
		if err := conn.AddMatchSignalContext(ctx, rule...); err != nil {
			return nil, nil, err
		}
	}

	l.track = &powerTracker{take: l.takeDelayLock, timeout: readyTimeout, after: afterFunc, log: log}
	lockErr = l.watchSession(ctx)
	l.track.hold()
	return l, lockErr, nil
}

// watchSession finds the user's session, subscribes to its property
// changes and reads LockedHint. Subscribing first means a change between
// the two isn't lost.
func (l *logind) watchSession(ctx context.Context) error {
	session, err := l.findSession(ctx)
	if err != nil {
		return err
	}
	err = l.conn.AddMatchSignalContext(ctx,
		dbus.WithMatchSender(logindName),
		dbus.WithMatchObjectPath(session),
		dbus.WithMatchInterface(propsIface),
		dbus.WithMatchMember("PropertiesChanged"),
		dbus.WithMatchArg(0, sessionIface),
	)
	if err != nil {
		return err
	}
	l.session = session
	locked, err := l.readLockedHint(ctx)
	if err != nil {
		// Without a first reading, a later change can't be told from
		// the state before it: leave lock unknown throughout.
		l.session = ""
		return err
	}
	// The first reading only sets the state; run reports it as a lock if
	// the session starts out locked.
	l.track.initial = &locked
	l.locked.Store(locked)
	return nil
}

// findSession returns the object path of the agent's logind session. An
// agent started by systemd's user manager belongs to no session; then the
// user's display session, the graphical one, is the one whose lock and
// idle state matter.
func (l *logind) findSession(ctx context.Context) (dbus.ObjectPath, error) {
	mgr := l.conn.Object(logindName, logindPath)
	var path dbus.ObjectPath
	err := mgr.CallWithContext(ctx, managerIface+".GetSessionByPID", 0, uint32(os.Getpid())).Store(&path)
	if err == nil && path.IsValid() && path != "/" {
		return path, nil
	}
	var user dbus.ObjectPath
	if err := mgr.CallWithContext(ctx, managerIface+".GetUser", 0, uint32(os.Getuid())).Store(&user); err != nil {
		return "", errors.New("logind knows no session or user for the agent")
	}
	var display dbus.Variant
	err = l.conn.Object(logindName, user).CallWithContext(ctx, propsIface+".Get", 0, userIface, "Display").Store(&display)
	if err != nil {
		return "", errors.New("logind didn't say which session is the user's display session")
	}
	// Display is a struct (so): the session id and its object path, which
	// is "/" when the user has no graphical session.
	fields, ok := display.Value().([]any)
	if ok && len(fields) == 2 {
		if p, ok := fields[1].(dbus.ObjectPath); ok && p.IsValid() && p != "/" {
			return p, nil
		}
	}
	return "", errors.New("the user has no graphical logind session")
}

func (l *logind) readLockedHint(ctx context.Context) (bool, error) {
	var v dbus.Variant
	err := l.conn.Object(logindName, l.session).CallWithContext(ctx, propsIface+".Get", 0, sessionIface, "LockedHint").Store(&v)
	if err != nil {
		return false, errors.New("logind's session has no LockedHint")
	}
	locked, ok := v.Value().(bool)
	if !ok {
		return false, errors.New("logind's LockedHint isn't a boolean")
	}
	return locked, nil
}

// takeDelayLock asks logind to wait for pairtime before the next sleep.
// The lock is a file descriptor: closing it lets the sleep go ahead, and
// logind waits at most InhibitDelayMaxSec (5 s by default) regardless.
func (l *logind) takeDelayLock() (io.Closer, error) {
	if !l.conn.SupportsUnixFDs() {
		return nil, errors.New("the bus connection can't pass file descriptors")
	}
	ctx, cancel := context.WithTimeout(context.Background(), callWait)
	defer cancel()
	var fd dbus.UnixFD
	err := l.conn.Object(logindName, logindPath).CallWithContext(ctx, managerIface+".Inhibit", 0,
		"sleep", "pairtime", "Recording the minutes before sleep", "delay").Store(&fd)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), "logind delay lock"), nil
}

// run turns logind's signals into Power events on out until ctx ends or
// the bus connection closes. Events queue here rather than block, so
// godbus, which delivers signals out of order once a channel fills, always
// finds room.
func (l *logind) run(ctx context.Context, out chan<- Power) {
	defer l.track.shutdown()
	var queue []Power
	if p, ok := l.track.start(time.Now()); ok {
		queue = append(queue, p)
	}
	for {
		var send chan<- Power
		var next Power
		if len(queue) > 0 {
			send, next = out, queue[0]
		}
		select {
		case sig, ok := <-l.sigs:
			if !ok {
				return
			}
			queue = append(queue, l.handle(sig, time.Now())...)
		case send <- next:
			queue = queue[1:]
		case <-ctx.Done():
			return
		}
	}
}

// handle turns one signal into the events it means.
func (l *logind) handle(sig *dbus.Signal, now time.Time) []Power {
	switch sig.Name {
	case busName + ".NameOwnerChanged":
		// logind restarted. The match rules follow its well-known name;
		// the sender check has to follow its new unique name.
		if sig.Sender == busName && len(sig.Body) == 3 {
			if name, _ := sig.Body[0].(string); name == logindName {
				l.owner, _ = sig.Body[2].(string)
			}
		}
		return nil
	case managerIface + ".PrepareForSleep":
		if sig.Sender != l.owner || sig.Path != logindPath || len(sig.Body) != 1 {
			return nil
		}
		start, ok := sig.Body[0].(bool)
		if !ok {
			return nil
		}
		if start {
			return []Power{l.track.suspend(now)}
		}
		return []Power{l.track.resume(now)}
	case propsIface + ".PropertiesChanged":
		if sig.Sender != l.owner || l.session == "" || sig.Path != l.session || len(sig.Body) != 3 {
			return nil
		}
		if iface, _ := sig.Body[0].(string); iface != sessionIface {
			return nil
		}
		var locked bool
		changed, _ := sig.Body[1].(map[string]dbus.Variant)
		invalidated, _ := sig.Body[2].([]string)
		if v, ok := changed["LockedHint"]; ok {
			if locked, ok = v.Value().(bool); !ok {
				return nil
			}
		} else if slices.Contains(invalidated, "LockedHint") {
			ctx, cancel := context.WithTimeout(context.Background(), callWait)
			defer cancel()
			var err error
			if locked, err = l.readLockedHint(ctx); err != nil {
				return nil
			}
		} else {
			return nil
		}
		l.locked.Store(locked)
		if p, ok := l.track.lockedHint(locked, now); ok {
			return []Power{p}
		}
	}
	return nil
}

// afterFunc is time.AfterFunc, as the tracker uses it.
func afterFunc(d time.Duration, f func()) { time.AfterFunc(d, f) }

func noop() {}

// powerTracker holds the delay lock across sleeps and turns logind's
// signals into Power events. Every method but a Power's Ready runs on the
// logind goroutine.
type powerTracker struct {
	take    func() (io.Closer, error) // takes a new delay lock
	timeout time.Duration             // how long a suspend waits for Ready
	after   func(time.Duration, func())
	log     *slog.Logger

	held    io.Closer // the delay lock for the next sleep, or nil
	pending func()    // the last suspend's Ready, until the resume

	initial   *bool // LockedHint at start, until start reports it
	locked    bool
	lockKnown bool
}

// hold takes a delay lock unless one is held already.
func (t *powerTracker) hold() {
	if t.held != nil {
		return
	}
	lock, err := t.take()
	if err != nil {
		// Without the lock, suspend is still reported; the agent just
		// may not finish sealing before the machine sleeps.
		if t.log != nil {
			t.log.Warn("can't take logind's delay lock before sleep", "err", err)
		}
		return
	}
	t.held = lock
}

// start reports the session as locked if it was locked when the agent
// started. A session that starts unlocked is the normal state and gives
// no event.
func (t *powerTracker) start(at time.Time) (Power, bool) {
	if t.initial == nil {
		return Power{}, false
	}
	locked := *t.initial
	t.initial = nil
	return t.lockedHint(locked, at)
}

// suspend hands the held delay lock to the event's Ready, which releases
// it once. If Ready doesn't come within the timeout, the lock is released
// anyway, so a stuck agent costs a sleep at most that long.
func (t *powerTracker) suspend(at time.Time) Power {
	lock := t.held
	t.held = nil
	var once sync.Once
	ready := func() {
		once.Do(func() {
			if lock != nil {
				lock.Close()
			}
		})
	}
	t.pending = ready
	if lock != nil && t.timeout > 0 {
		t.after(t.timeout, ready)
	}
	return Power{Op: event.PowerSuspend, At: at, Ready: ready}
}

// resume releases the last suspend's lock if Ready never did, and takes
// the lock for the next sleep.
func (t *powerTracker) resume(at time.Time) Power {
	if t.pending != nil {
		t.pending()
		t.pending = nil
	}
	t.hold()
	return Power{Op: event.PowerResume, At: at, Ready: noop}
}

// lockedHint reports a lock or unlock when LockedHint changes.
func (t *powerTracker) lockedHint(locked bool, at time.Time) (Power, bool) {
	first := !t.lockKnown
	if !first && locked == t.locked {
		return Power{}, false
	}
	t.locked, t.lockKnown = locked, true
	if first && !locked {
		return Power{}, false
	}
	op := event.PowerUnlock
	if locked {
		op = event.PowerLock
	}
	return Power{Op: op, At: at, Ready: noop}, true
}

// shutdown releases every lock, so a stopped agent never delays a sleep.
func (t *powerTracker) shutdown() {
	if t.pending != nil {
		t.pending()
		t.pending = nil
	}
	if t.held != nil {
		t.held.Close()
		t.held = nil
	}
}
