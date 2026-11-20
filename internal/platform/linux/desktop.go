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

// Config configures Open. The zero value reads the real session.
type Config struct {
	// Getenv reads the session's environment. Nil means os.Getenv.
	Getenv func(string) string
	// SessionBus connects to the session bus, for Mutter. Nil means the
	// bus the environment names, never one started for the purpose.
	SessionBus func() (*dbus.Conn, error)
	// SystemBus connects to the bus logind is on. Nil means the system
	// bus. Tests point it at a private bus with a fake logind.
	SystemBus func() (*dbus.Conn, error)
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
	focus   chan Focus // nil when focus is unknown
	power   chan Power // nil when power events are unknown

	mu     sync.Mutex
	status Status

	cancel  context.CancelFunc
	wg      sync.WaitGroup
	closers []func()
}

// openTimeout bounds each connection Open makes.
const openTimeout = 2 * time.Second

// Open finds the session's signals and starts watching focus and power.
// It doesn't fail: a signal the desktop doesn't give is unknown in Status,
// and the matching method reports ErrUnknown or returns a nil channel.
// Watching stops when ctx ends or Close is called.
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
		sessionBus = func() (*dbus.Conn, error) {
			address, err := sessionBusAddress(getenv)
			if err != nil {
				return nil, err
			}
			return connectBus(address)
		}
	}
	systemBus := cfg.SystemBus
	if systemBus == nil {
		systemBus = func() (*dbus.Conn, error) { return dbus.ConnectSystemBus() }
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
		src := &x11Idle{open: func(timeout time.Duration) (*xconn, error) {
			return openX11(disp, getenv, hostname, timeout)
		}}
		src.mu.Lock()
		err := src.connect(openTimeout)
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
		probe, cancelProbe := context.WithTimeout(ctx, openTimeout)
		_, err := src.idle(probe)
		cancelProbe()
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
		d.status.Focus = unknown("Wayland gives other programs no focus signal, so focus is ambiguous (SM 3.2)")
	default:
		d.status.Focus = unknown("no graphical session")
	}
	d.startPower(ctx, systemBus, readyTimeout, log)
	return d
}

// startFocus starts following _NET_ACTIVE_WINDOW on its own connection.
func (d *Desktop) startFocus(ctx context.Context, disp xDisplay, dispErr error, getenv func(string) string, hostname string, log *slog.Logger) {
	if dispErr != nil {
		d.status.Focus = unknownErr(dispErr)
		return
	}
	c, err := openX11(disp, getenv, hostname, openTimeout)
	if err != nil {
		d.status.Focus = unknownErr(err)
		return
	}
	w, err := newX11Focus(c, hostname, openTimeout)
	if err != nil {
		d.status.Focus = unknownErr(err)
		return
	}
	const name = "_NET_ACTIVE_WINDOW"
	d.status.Focus = via(name)
	if !w.ewmh {
		// Keep listening: a window manager that starts later sets the
		// property, and the first change makes focus known.
		d.status.Focus = unknown("the window manager doesn't keep _NET_ACTIVE_WINDOW")
		w.onEWMH = func() { d.setStatus(func(s *Status) { s.Focus = via(name) }) }
	}
	d.focus = make(chan Focus, 16)
	d.closers = append(d.closers, w.close)
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer close(d.focus)
		err := w.run(ctx, d.focus)
		if ctx.Err() == nil {
			log.Warn("lost the X connection that follows focus", "err", err)
			d.setStatus(func(s *Status) { s.Focus = unknown("lost the X connection") })
		}
	}()
}

// startPower subscribes to logind. The focus goroutine may be running, so
// it sets the status through setStatus.
func (d *Desktop) startPower(ctx context.Context, systemBus func() (*dbus.Conn, error), readyTimeout time.Duration, log *slog.Logger) {
	conn, err := systemBus()
	if err != nil {
		reason := unknown("no system bus, so no logind: suspends show only as gaps between samples")
		d.setStatus(func(s *Status) { s.Power, s.Lock = reason, reason })
		return
	}
	l, lockErr, err := openLogind(conn, readyTimeout, log)
	if err != nil {
		conn.Close()
		reason := unknown(err.Error() + ": suspends show only as gaps between samples")
		d.setStatus(func(s *Status) { s.Power, s.Lock = reason, reason })
		return
	}
	d.logind = l
	lock := via("logind LockedHint")
	if lockErr != nil {
		lock = unknownErr(lockErr)
	}
	d.setStatus(func(s *Status) { s.Power, s.Lock = via("logind PrepareForSleep"), lock })
	d.power = make(chan Power, 8)
	d.closers = append(d.closers, func() { conn.Close() })
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer close(d.power)
		l.run(ctx, d.power)
		if ctx.Err() == nil {
			log.Warn("lost the system bus connection to logind")
			d.setStatus(func(s *Status) {
				s.Power = unknown("lost the connection to logind")
				s.Lock = s.Power
			})
		}
	}()
}

func (d *Desktop) setStatus(f func(*Status)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	f(&d.status)
}

// Status returns where each signal comes from now.
func (d *Desktop) Status() Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.status
}

// Idle reads the time since the last input, with one round trip to the
// X server or Mutter that waits at most 1 s, or until ctx ends. It returns
// ErrUnknown when the desktop gives no idle time; any other error is a
// failed sample, and the next call tries again.
func (d *Desktop) Idle(ctx context.Context) (Idle, error) {
	if d.idleSrc == nil {
		return Idle{}, ErrUnknown
	}
	idle, err := d.idleSrc.idle(ctx)
	if err != nil {
		return Idle{}, err
	}
	i := Idle{At: time.Now(), Any: idle}
	if d.logind != nil {
		i.Locked = d.logind.locked.Load()
	}
	return i, nil
}

// Focus returns the focus changes, starting with the window focused when
// watching began. It is nil when focus is unknown, and closed when
// watching stops.
func (d *Desktop) Focus() <-chan Focus { return d.focus }

// Power returns suspends, resumes, locks and unlocks. It is nil when
// logind isn't reachable, and closed when watching stops. A session that
// is locked when watching begins starts with a lock.
func (d *Desktop) Power() <-chan Power { return d.power }

// Close stops watching, releases logind's delay lock and closes every
// connection. The Focus and Power channels are closed once it returns.
func (d *Desktop) Close() error {
	d.cancel()
	for _, c := range d.closers {
		c()
	}
	d.wg.Wait()
	return nil
}
