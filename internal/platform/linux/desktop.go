// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package linux

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/some-du6e/pairtime/pkg/event"
)

// ErrUnknown means the desktop gives no idle time. Status says why.
var ErrUnknown = errors.New("this desktop gives pairtime no idle time")

// ErrLockUnknown means logind can't say whether the session is locked.
// Status.Lock says why.
var ErrLockUnknown = errors.New("this session's lock state is unknown")

// Config configures Open. The zero value reads the real session.
type Config struct {
	// Getenv reads the session's environment. Nil means os.Getenv.
	Getenv func(string) string
	// SessionBus connects to the session bus, for Mutter, before ctx
	// ends. Nil means the bus the environment names, never one started
	// for the purpose.
	SessionBus func(ctx context.Context) (*dbus.Conn, error)
	// SystemBus connects to the bus logind is on, before ctx ends. Nil
	// means the system bus. Tests point it at a private bus with a fake
	// logind.
	SystemBus func(ctx context.Context) (*dbus.Conn, error)
	// Hostname is this machine's name, to tell local X clients from
	// remote ones. Empty means os.Hostname.
	Hostname string
	// ReadyTimeout is how long a suspend waits for Power.Ready before
	// releasing logind's delay lock anyway. Zero means 3 s, under
	// logind's default InhibitDelayMaxSec of 5 s.
	ReadyTimeout time.Duration
	// Log gets a line for each signal that fails. Nil discards them.
	Log *slog.Logger
}

// Status says where each signal comes from on this desktop, or why it is
// unknown. doctor prints it (DESIGN 5.9).
type Status struct {
	Session Session
	Idle    Signal
	Focus   Signal
	// Power covers suspend and resume.
	Power Signal
	Lock  Signal
}

// Signal is one source of events, or the reason there is none.
type Signal struct {
	// Via names the source, such as "MIT-SCREEN-SAVER"; it is empty when
	// the signal is unknown.
	Via string
	// Unknown says why there is no source; it is empty when Via is set.
	Unknown string
}

// Known reports whether the signal has a source.
func (s Signal) Known() bool { return s.Via != "" }

func via(name string) Signal       { return Signal{Via: name} }
func unknown(reason string) Signal { return Signal{Unknown: reason} }
func unknownErr(err error) Signal  { return Signal{Unknown: err.Error()} }

// Idle is one presence reading.
type Idle struct {
	// At is when the reading was taken, with its monotonic clock reading.
	// Gaps between samples come from wall-clock values (CP 2.2), so use
	// At.Round(0) or At.UnixMilli() for them.
	At time.Time
	// Any is the time since the last keyboard or mouse input.
	Any time.Duration
	// Locked is logind's LockedHint for the session: false when the lock
	// state is unknown (Status.Lock).
	Locked bool
}

// Payload returns the os.presence payload for the reading. Linux has no
// key-only idle time without a key counter (SM 1.2), so idle_keys_s is
// null.
func (i Idle) Payload() *event.OSPresence {
	return &event.OSPresence{
		IdleAnyS: float64(i.Any.Milliseconds()) / 1000,
		Locked:   i.Locked,
	}
}

// Focus is a change of the focused window.
type Focus struct {
	At time.Time
	// Window is the X window that has focus, or 0 when none has.
	Window uint32
	// PID is the focused window's process, or 0 when the window doesn't
	// say or belongs to another host.
	PID int
}

// Payload returns the os.focus payload for the change. classify maps the
// focused process to its app class, from the shared app-class table
// (WP-OS6), which also fills hosted_sessions; nil, or a window whose
// process is unknown, gives "other", which is never credited, rather than a
// guess.
func (f Focus) Payload(classify func(pid int) event.AppClass) *event.OSFocus {
	switch {
	case f.Window == 0:
		return &event.OSFocus{AppClass: event.AppNone}
	case f.PID == 0 || classify == nil:
		return &event.OSFocus{AppClass: event.AppOther}
	}
	class := classify(f.PID)
	if class == "" {
		class = event.AppOther
	}
	return &event.OSFocus{AppClass: class}
}

// Power is a suspend, resume, lock or unlock.
type Power struct {
	Op event.PowerOp
	At time.Time
	// Ready lets a suspend go ahead: call it once the ledger batch is
	// sealed (DESIGN 3.3). Until then logind holds the sleep, for at most
	// Config.ReadyTimeout. It is a no-op for other ops, and safe to call
	// more than once and from any goroutine.
	Ready func()
}

// Payload returns the os.power payload for the event.
func (p Power) Payload() *event.OSPower { return &event.OSPower{Op: p.Op} }

// idleSource reads the time since the last input.
type idleSource interface {
	idle(ctx context.Context) (time.Duration, error)
	name() string
	close()
}

// Desktop is the Linux session's presence, focus and power signals. Open
// one per agent.
type Desktop struct {
	idleSrc idleSource // nil when idle time is unknown
	logind  *logind    // nil when logind isn't reachable
	focus   chan Focus // nil when no X11 watcher runs
	power   chan Power // nil when logind isn't reachable

	mu     sync.Mutex
	status Status
	locked bool // LockedHint, while Status.Lock is known

	cancel    context.CancelFunc
	wg        sync.WaitGroup
	closers   []func()
	closeOnce sync.Once
}

// openTimeout bounds each connection Open makes, from the dial to the
// first answer, so that a bus or X server that accepts a connection and
// then says nothing delays Open by seconds, not forever.
const openTimeout = 2 * time.Second

// Open finds the session's signals and starts watching focus and power.
// It doesn't fail: a signal the desktop doesn't give is unknown in Status,
// and Idle reports ErrUnknown, Locked ErrLockUnknown, and Focus or Power a
// nil channel when nothing watches for it. Status is what says whether a
// signal is known: a channel can be open while its signal is unknown (see
// Focus). Watching stops when ctx ends or Close is called: the Focus and
// Power channels close, and so do the connections that fed them.
func Open(ctx context.Context, cfg Config) *Desktop {
	getenv := cfg.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	hostname := cfg.Hostname
	if hostname == "" {
		hostname, _ = os.Hostname()
	}
	sessionBus := cfg.SessionBus
	if sessionBus == nil {
		sessionBus = func(ctx context.Context) (*dbus.Conn, error) {
			address, err := sessionBusAddress(getenv, currentUID, isSocket)
			if err != nil {
				return nil, err
			}
			return connectBus(ctx, address)
		}
	}
	systemBus := cfg.SystemBus
	if systemBus == nil {
		systemBus = func(ctx context.Context) (*dbus.Conn, error) {
			address, err := systemBusAddress(getenv)
			if err != nil {
				return nil, err
			}
			return connectBus(ctx, address)
		}
	}
	readyTimeout := cfg.ReadyTimeout
	if readyTimeout == 0 {
		readyTimeout = 3 * time.Second
	}
	log := cfg.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	ctx, cancel := context.WithCancel(ctx)
	d := &Desktop{cancel: cancel}
	d.status.Session = detectSession(getenv)

	var disp xDisplay
	var dispErr error
	if d.status.Session == SessionX11 {
		disp, dispErr = parseDisplay(getenv("DISPLAY"))
	}

	// Idle time.
	var x11Status Signal
	switch d.status.Session {
	case SessionX11:
		if dispErr != nil {
			x11Status = unknownErr(dispErr)
			break
		}
		src := &x11Idle{open: func(deadline time.Time) (*xconn, error) {
			return openX11(disp, getenv, hostname, deadline)
		}}
		src.mu.Lock()
		err := src.connect(time.Now().Add(openTimeout))
		src.mu.Unlock()
		if err == nil {
			d.idleSrc, x11Status = src, via(src.name())
			break
		}
		x11Status = unknownErr(err)
	case SessionWayland:
		x11Status = unknown("Wayland: X11 idle time would only cover XWayland's clients")
	default:
		x11Status = unknown("no graphical session: DISPLAY and WAYLAND_DISPLAY are unset")
	}
	// GNOME's Mutter answers on Wayland, and on X11 when the X server
	// can't. Not for a display on another host: the session bus there
	// would be that machine's, and its idle time someone else's.
	tryMutter := d.idleSrc == nil &&
		(d.status.Session == SessionWayland || (d.status.Session == SessionX11 && dispErr == nil))
	if !tryMutter {
		d.status.Idle = x11Status
	} else {
		src := &mutterIdle{connect: sessionBus}
		_, err := src.sample(ctx, openTimeout)
		if err == nil {
			d.idleSrc, d.status.Idle = src, via(src.name())
		} else {
			src.close()
			reason := x11Status.Unknown
			if d.status.Session == SessionWayland {
				// KDE and wlroots compositors answer ext-idle-notify-v1
				// over Wayland instead, which pairtime doesn't speak yet.
				reason = "this Wayland compositor has no org.gnome.Mutter.IdleMonitor, and pairtime doesn't read ext-idle-notify-v1 yet"
			}
			d.status.Idle = unknown(reason)
		}
	}
	if d.idleSrc != nil {
		d.closers = append(d.closers, d.idleSrc.close)
	}

	// From here on goroutines run, and the status changes only through
	// setStatus.
	switch d.status.Session {
	case SessionX11:
		d.startFocus(ctx, disp, dispErr, getenv, hostname, log)
	case SessionWayland:
		// SM 3.2 wants these slots ambiguous, so that anchors still
		// count. Recording that takes an os.focus that says focus is
		// unknown, which the event model doesn't have yet, so for now
		// focus is only unknown here.
		d.status.Focus = unknown("Wayland gives other programs no focus signal")
	default:
		d.status.Focus = unknown("no graphical session")
	}
	d.startPower(ctx, systemBus, readyTimeout, log)
	return d
}

// startFocus follows _NET_ACTIVE_WINDOW on a connection of its own. When
// that connection drops, as when the display manager restarts a crashed X
// server, or the user logs out and back in while the agent runs on, it
// reconnects to the same display, as idle sampling does.
func (d *Desktop) startFocus(ctx context.Context, disp xDisplay, dispErr error, getenv func(string) string, hostname string, log *slog.Logger) {
	if dispErr != nil {
		d.status.Focus = unknownErr(dispErr)
		return
	}
	open := func() (*x11Focus, error) {
		deadline := time.Now().Add(openTimeout)
		c, err := openX11(disp, getenv, hostname, deadline)
		if err != nil {
			return nil, err
		}
		return newX11Focus(c, hostname, deadline)
	}
	w, err := open()
	if err != nil {
		d.status.Focus = unknownErr(err)
		return
	}
	d.watchingFocus(w)
	d.focus = make(chan Focus, 16)
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer close(d.focus)
		attempt := 0
		for w != nil {
			started := time.Now()
			// Ending ctx closes the connection, which ends run's blocked
			// read.
			stop := context.AfterFunc(ctx, w.close)
			err := w.run(ctx, d.focus)
			stop()
			w.close()
			if ctx.Err() != nil {
				break
			}
			if time.Since(started) > time.Minute {
				attempt = 0 // it ran a while, so the waits start over
			}
			log.Warn("lost the X connection that follows focus; reconnecting", "err", err)
			d.setStatus(func(s *Status) { s.Focus = unknown("lost the X connection; reconnecting") })
			w, attempt = d.reconnectFocus(ctx, open, attempt)
		}
		d.setStatus(func(s *Status) { s.Focus = unknown("stopped watching") })
	}()
}

// watchingFocus sets Status.Focus for a new watcher. A watcher that found
// no window manager keeping _NET_ACTIVE_WINDOW goes on listening: one that
// starts later sets the property, and the first change makes focus known.
func (d *Desktop) watchingFocus(w *x11Focus) {
	if w.ewmh {
		d.setStatus(func(s *Status) { s.Focus = via(w.via()) })
		return
	}
	d.setStatus(func(s *Status) { s.Focus = unknown("the window manager doesn't keep _NET_ACTIVE_WINDOW") })
	w.onEWMH = func() { d.setStatus(func(s *Status) { s.Focus = via(w.via()) }) }
}

// focusRetry is how long focus waits before each attempt to reconnect, the
// last wait repeating until one works. A display manager restarts a crashed
// X server within seconds; a new login takes longer.
var focusRetry = []time.Duration{0, time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second}

// reconnectFocus opens a new focus watcher, trying until one opens, the
// first try after focusRetry[attempt]. It returns the watcher, or nil if
// ctx ends first, and the attempt to start from if that watcher's
// connection drops soon too, so that a server that drops every connection
// at once gets the longer waits.
func (d *Desktop) reconnectFocus(ctx context.Context, open func() (*x11Focus, error), attempt int) (*x11Focus, int) {
	for ; ; attempt++ {
		if wait := focusRetry[min(attempt, len(focusRetry)-1)]; wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return nil, attempt
			}
		}
		if ctx.Err() != nil {
			return nil, attempt
		}
		if w, err := open(); err == nil {
			d.watchingFocus(w)
			return w, attempt + 1
		}
	}
}

// startPower subscribes to logind. The focus goroutine may be running, so
// it sets the status through setStatus.
func (d *Desktop) startPower(ctx context.Context, systemBus func(context.Context) (*dbus.Conn, error), readyTimeout time.Duration, log *slog.Logger) {
	connectCtx, cancel := context.WithTimeout(ctx, openTimeout)
	conn, err := systemBus(connectCtx)
	cancel()
	if err != nil {
		reason := unknown("no system bus, so no logind: suspends show only as gaps between samples")
		d.setStatus(func(s *Status) { s.Power, s.Lock = reason, reason })
		return
	}
	l, err := openLogind(ctx, conn, readyTimeout, log, d.setLock)
	if err != nil {
		conn.Close()
		reason := unknown(err.Error() + ": suspends show only as gaps between samples")
		d.setStatus(func(s *Status) { s.Power, s.Lock = reason, reason })
		return
	}
	d.logind = l
	d.setStatus(func(s *Status) { s.Power = via("logind PrepareForSleep") })
	d.power = make(chan Power, 8)
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer close(d.power)
		l.run(ctx, d.power)
		// Closed, the connection stops godbus queueing signals that no one
		// reads any more.
		conn.Close()
		reason := unknown("stopped watching")
		if ctx.Err() == nil {
			log.Warn("lost the system bus connection to logind")
			reason = unknown("lost the connection to logind")
		}
		d.setStatus(func(s *Status) { s.Power, s.Lock = reason, reason })
	}()
}

func (d *Desktop) setStatus(f func(*Status)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	f(&d.status)
}

// setLock records the lock state logind reports: Status.Lock, and
// LockedHint while that is known.
func (d *Desktop) setLock(lock Signal, locked bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.status.Lock, d.locked = lock, locked
}

// Status returns where each signal comes from now.
func (d *Desktop) Status() Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.status
}

// Idle reads the time since the last input, with one round trip to the
// X server or Mutter. It gives up after 1 s, or at ctx's deadline if that
// is sooner, reconnecting included, and a Mutter sample also stops when ctx
// is canceled. It returns ErrUnknown when the desktop gives no idle time,
// and net.ErrClosed after Close; any other error is a failed sample, and
// the next call tries again.
func (d *Desktop) Idle(ctx context.Context) (Idle, error) {
	if d.idleSrc == nil {
		return Idle{}, ErrUnknown
	}
	idle, err := d.idleSrc.idle(ctx)
	if err != nil {
		return Idle{}, err
	}
	i := Idle{At: time.Now(), Any: idle}
	if locked, err := d.Locked(); err == nil {
		i.Locked = locked
	}
	return i, nil
}

// Locked reports logind's LockedHint for the session pairtime follows, as
// of the last change logind announced. It returns ErrLockUnknown when that
// isn't known: logind is out of reach, or the session ended and the next
// one isn't found yet.
func (d *Desktop) Locked() (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.status.Lock.Known() {
		return false, ErrLockUnknown
	}
	return d.locked, nil
}

// Focus returns the focus changes. It is nil when no X11 watcher runs, as
// on Wayland or without a display, and closed when watching stops.
//
// A watcher can run while focus is unknown: until a window manager that
// keeps _NET_ACTIVE_WINDOW appears, and while it reconnects after losing
// the X connection. So Status().Focus.Known(), not a nil channel, is what
// says whether focus is known. The first report once it is, and the first
// after each reconnect, names the window focused then.
func (d *Desktop) Focus() <-chan Focus { return d.focus }

// Power returns suspends, resumes, locks and unlocks. It is nil when
// logind isn't reachable, and closed when watching stops. A session that
// is locked when watching begins starts with a lock.
func (d *Desktop) Power() <-chan Power { return d.power }

// Close stops watching, releases logind's delay lock and closes every
// connection. The Focus and Power channels are closed once it returns.
// Closing again does nothing.
func (d *Desktop) Close() error {
	d.closeOnce.Do(func() {
		d.cancel()
		for _, c := range d.closers {
			c()
		}
		d.wg.Wait()
	})
	return nil
}
