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
	"syscall"
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

// lockVia names the source of lock state, as Status.Lock gives it.
const lockVia = "logind LockedHint"

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
//
// The agent can outlive the session it follows. A systemd user unit stays
// up after a logout, and so does an autostarted agent when logind keeps a
// logged-out session's processes (KillUserProcesses=no), which leaves that
// session closing until the agent exits. So the Manager's SessionNew and
// SessionRemoved make logind look for the session again, and lock state is
// unknown until it has read the next one's LockedHint.
type logind struct {
	conn    *dbus.Conn
	session dbus.ObjectPath // the session followed, or "" while there is none
	track   *powerTracker
	sigs    chan *dbus.Signal
	log     *slog.Logger
	// report is told the lock state whenever it changes: Status.Lock, and
	// LockedHint while that is known.
	report func(lock Signal, locked bool)

	// owner is logind's unique bus name: only its signals count. Anyone
	// on the bus can send a signal addressed to this connection, but the
	// bus stamps the true sender on it.
	owner string
}

// openLogind subscribes to logind's signals, finds the session, reads its
// lock state and takes the first delay lock. It fails only when logind
// can't be reached; without a session it still reports suspend and
// resume, and tells report why lock state is unknown.
func openLogind(ctx context.Context, conn *dbus.Conn, readyTimeout time.Duration, log *slog.Logger, report func(Signal, bool)) (*logind, error) {
	ctx, cancel := context.WithTimeout(ctx, callWait)
	defer cancel()
	l := &logind{conn: conn, log: log, report: report, sigs: make(chan *dbus.Signal, 16)}
	if err := conn.BusObject().CallWithContext(ctx, busName+".GetNameOwner", 0, logindName).Store(&l.owner); err != nil {
		return nil, errors.New("logind isn't on the system bus")
	}
	conn.Signal(l.sigs)
	rules := [][]dbus.MatchOption{
		managerRule("PrepareForSleep"),
		managerRule("SessionNew"),
		managerRule("SessionRemoved"),
		{
			dbus.WithMatchSender(busName),
			dbus.WithMatchInterface(busName),
			dbus.WithMatchMember("NameOwnerChanged"),
			dbus.WithMatchArg(0, logindName),
		},
	}
	for _, rule := range rules {
		if err := conn.AddMatchSignalContext(ctx, rule...); err != nil {
			return nil, err
		}
	}

	l.track = &powerTracker{take: l.takeDelayLock, timeout: readyTimeout, after: afterFunc, log: log}
	if locked, err := l.follow(ctx); err != nil {
		l.report(unknownErr(err), false)
	} else {
		// The first reading only sets the state; run reports it as a
		// lock if the session starts out locked.
		l.track.initial = &locked
		l.report(via(lockVia), locked)
	}
	l.track.hold()
	return l, nil
}

// managerRule matches one of the Manager's signals.
func managerRule(member string) []dbus.MatchOption {
	return []dbus.MatchOption{
		dbus.WithMatchSender(logindName),
		dbus.WithMatchObjectPath(logindPath),
		dbus.WithMatchInterface(managerIface),
		dbus.WithMatchMember(member),
	}
}

// sessionRule matches the changes of session's properties.
func sessionRule(session dbus.ObjectPath) []dbus.MatchOption {
	return []dbus.MatchOption{
		dbus.WithMatchSender(logindName),
		dbus.WithMatchObjectPath(session),
		dbus.WithMatchInterface(propsIface),
		dbus.WithMatchMember("PropertiesChanged"),
		dbus.WithMatchArg(0, sessionIface),
	}
}

// follow finds the session whose lock state counts and watches it.
func (l *logind) follow(ctx context.Context) (bool, error) {
	session, err := l.findSession(ctx)
	if err != nil {
		return false, err
	}
	return l.watch(ctx, session)
}

// watch subscribes to session's property changes and reads its LockedHint.
// Subscribing first means a change between the two isn't lost. Without a
// first reading a later change can't be told from the state before it, so
// then no session is followed.
func (l *logind) watch(ctx context.Context, session dbus.ObjectPath) (bool, error) {
	if err := l.conn.AddMatchSignalContext(ctx, sessionRule(session)...); err != nil {
		return false, err
	}
	l.session = session
	locked, err := l.readLockedHint(ctx)
	if err != nil {
		l.unfollow(ctx)
		return false, err
	}
	return locked, nil
}

// unfollow stops following the session. If RemoveMatch fails, the
// session's signals still come, and handle drops them by their path.
func (l *logind) unfollow(ctx context.Context) {
	if l.session == "" {
		return
	}
	_ = l.conn.RemoveMatchSignalContext(ctx, sessionRule(l.session)...)
	l.session = ""
}

// refollow looks for the session to follow again after one started or
// ended, and moves to it if it isn't the one followed. ended says the
// followed session is gone, so lock state is unknown from the start.
func (l *logind) refollow(now time.Time, ended bool) []Power {
	ctx, cancel := context.WithTimeout(context.Background(), callWait)
	defer cancel()
	if ended {
		l.unfollow(ctx)
		l.report(unknown("the session pairtime followed has ended"), false)
	}
	session, err := l.findSession(ctx)
	if err == nil && session == l.session {
		return nil
	}
	if err != nil && l.session != "" && !l.closing(ctx, l.session) {
		// The session followed goes on, and a lookup that failed is no
		// reason to drop it.
		return nil
	}
	l.unfollow(ctx)
	if err == nil {
		var locked bool
		if locked, err = l.watch(ctx, session); err == nil {
			l.report(via(lockVia), locked)
			if p, ok := l.track.lockedHint(locked, now); ok {
				return []Power{p}
			}
			return nil
		}
	}
	l.report(unknownErr(err), false)
	return nil
}

// findSession returns the object path of the session whose lock state
// counts: the agent's own logind session, unless it is closing because its
// user logged out. An agent started by systemd's user manager belongs to
// no session, and one left in a closing session follows no screen any
// more; for those, the user's display session, the graphical one, is the
// one whose lock and idle state matter.
func (l *logind) findSession(ctx context.Context) (dbus.ObjectPath, error) {
	mgr := l.conn.Object(logindName, logindPath)
	var path dbus.ObjectPath
	err := mgr.CallWithContext(ctx, managerIface+".GetSessionByPID", 0, uint32(os.Getpid())).Store(&path)
	if err == nil && path.IsValid() && path != "/" && !l.closing(ctx, path) {
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

// closing reports whether session's State is "closing": its user logged
// out, and processes such as the agent are left in it. A State that can't
// be read counts as not closing.
func (l *logind) closing(ctx context.Context, session dbus.ObjectPath) bool {
	var v dbus.Variant
	err := l.conn.Object(logindName, session).CallWithContext(ctx, propsIface+".Get", 0, sessionIface, "State").Store(&v)
	state, _ := v.Value().(string)
	return err == nil && state == "closing"
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
	// No context: when a call's context ends, godbus stops tracking it and
	// drops the answer if it comes later, file descriptor and all.
	call := l.conn.Object(logindName, logindPath).Go(managerIface+".Inhibit", 0, make(chan *dbus.Call, 1),
		"sleep", "pairtime", "Recording the minutes before sleep", "delay")
	return awaitDelayLock(call, callWait)
}

// awaitDelayLock waits up to wait for logind's answer to Inhibit. For a
// caller that isn't root, logind answers only once polkit allows the lock,
// which can take seconds while polkitd starts, or right after a resume on
// a slow disk. An answer after the wait is closed as it comes: a lock that
// pairtime held without knowing would make every later sleep wait the
// whole InhibitDelayMaxSec.
func awaitDelayLock(call *dbus.Call, wait time.Duration) (io.Closer, error) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-call.Done:
		return delayLock(call)
	case <-timer.C:
		go func() {
			<-call.Done
			closeFDs(call.Body)
		}()
		return nil, errors.New("logind didn't answer Inhibit in time")
	}
}

// delayLock returns the lock in Inhibit's answer.
func delayLock(call *dbus.Call) (io.Closer, error) {
	var fd dbus.UnixFD
	if err := call.Store(&fd); err != nil {
		closeFDs(call.Body)
		return nil, err
	}
	return os.NewFile(uintptr(fd), "logind delay lock"), nil
}

// closeFDs closes the file descriptors in a message body.
func closeFDs(body []any) {
	for _, v := range body {
		if fd, ok := v.(dbus.UnixFD); ok {
			syscall.Close(int(fd))
		}
	}
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
		l.report(via(lockVia), locked)
		if p, ok := l.track.lockedHint(locked, now); ok {
			return []Power{p}
		}
	case managerIface + ".SessionNew", managerIface + ".SessionRemoved":
		if sig.Sender != l.owner || sig.Path != logindPath || len(sig.Body) != 2 {
			return nil
		}
		path, ok := sig.Body[1].(dbus.ObjectPath)
		if !ok {
			return nil
		}
		ended := sig.Name == managerIface+".SessionRemoved" && path == l.session
		if sig.Name == managerIface+".SessionRemoved" && !ended && l.session != "" {
			return nil // another session ended
		}
		return l.refollow(now, ended)
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
