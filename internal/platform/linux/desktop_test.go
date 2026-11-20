// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package linux

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/some-du6e/pairtime/pkg/event"
)

func TestDetectSession(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want Session
	}{
		{"XDG_SESSION_TYPE wayland is Wayland", map[string]string{"XDG_SESSION_TYPE": "wayland", "DISPLAY": ":0"}, SessionWayland},
		{"XDG_SESSION_TYPE x11 with a display is X11", map[string]string{"XDG_SESSION_TYPE": "x11", "DISPLAY": ":0"}, SessionX11},
		{"XDG_SESSION_TYPE x11 without a display is no session", map[string]string{"XDG_SESSION_TYPE": "x11"}, SessionNone},
		{"x11 beats a stray WAYLAND_DISPLAY", map[string]string{"XDG_SESSION_TYPE": "x11", "DISPLAY": ":0", "WAYLAND_DISPLAY": "wayland-0"}, SessionX11},
		{"startx from a console is X11", map[string]string{"XDG_SESSION_TYPE": "tty", "DISPLAY": ":0"}, SessionX11},
		{"a compositor from a console is Wayland, even with XWayland's DISPLAY", map[string]string{"XDG_SESSION_TYPE": "tty", "WAYLAND_DISPLAY": "wayland-1", "DISPLAY": ":1"}, SessionWayland},
		{"without XDG_SESSION_TYPE, WAYLAND_DISPLAY is Wayland", map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"}, SessionWayland},
		{"without XDG_SESSION_TYPE, DISPLAY is X11", map[string]string{"DISPLAY": ":0"}, SessionX11},
		{"a console with no display is no session", map[string]string{"XDG_SESSION_TYPE": "tty"}, SessionNone},
		{"an empty environment is no session", map[string]string{}, SessionNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectSession(func(k string) string { return tc.env[k] }); got != tc.want {
				t.Errorf("session %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPayloads(t *testing.T) {
	cases := []struct {
		name string
		got  event.Payload
		want event.Payload
	}{
		{
			name: "an idle sample is os.presence in seconds, with no key-only idle time",
			got:  Idle{Any: 12345 * time.Millisecond}.Payload(),
			want: &event.OSPresence{IdleAnyS: 12.345},
		},
		{
			name: "a sample while locked says so",
			got:  Idle{Any: time.Minute, Locked: true}.Payload(),
			want: &event.OSPresence{IdleAnyS: 60, Locked: true},
		},
		{
			name: "no focused window is app class none",
			got:  Focus{Window: 0}.Payload(func(int) event.AppClass { return event.AppTerminal }),
			want: &event.OSFocus{AppClass: event.AppNone},
		},
		{
			name: "a window whose process is unknown is other, never a guess",
			got:  Focus{Window: 9}.Payload(func(int) event.AppClass { return event.AppTerminal }),
			want: &event.OSFocus{AppClass: event.AppOther},
		},
		{
			name: "the classifier names the focused process's class",
			got:  Focus{Window: 9, PID: 42}.Payload(func(int) event.AppClass { return event.AppTerminal }),
			want: &event.OSFocus{AppClass: event.AppTerminal},
		},
		{
			name: "without a classifier every app is other",
			got:  Focus{Window: 9, PID: 42}.Payload(nil),
			want: &event.OSFocus{AppClass: event.AppOther},
		},
		{
			name: "a classifier with no answer gives other",
			got:  Focus{Window: 9, PID: 42}.Payload(func(int) event.AppClass { return "" }),
			want: &event.OSFocus{AppClass: event.AppOther},
		},
		{
			name: "a suspend is os.power suspend",
			got:  Power{Op: event.PowerSuspend}.Payload(),
			want: &event.OSPower{Op: event.PowerSuspend},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.got.Validate(); err != nil {
				t.Fatalf("payload doesn't validate: %v", err)
			}
			if !samePayload(tc.got, tc.want) {
				t.Errorf("payload %+v, want %+v", tc.got, tc.want)
			}
		})
	}
}

func samePayload(a, b event.Payload) bool {
	switch a := a.(type) {
	case *event.OSPresence:
		b, ok := b.(*event.OSPresence)
		return ok && a.IdleAnyS == b.IdleAnyS && a.Locked == b.Locked && a.IdleKeysS == nil && b.IdleKeysS == nil
	case *event.OSFocus:
		b, ok := b.(*event.OSFocus)
		return ok && a.AppClass == b.AppClass && a.AppHash == b.AppHash && len(a.HostedSessions) == 0 && a.FocusProject == b.FocusProject
	case *event.OSPower:
		b, ok := b.(*event.OSPower)
		return ok && *a == *b
	}
	return false
}

// TestOpenWithoutSignals opens desktops that give nothing, which needs no
// X server or bus, and checks that each signal is unknown with a reason.
func TestOpenWithoutSignals(t *testing.T) {
	noBus := func(context.Context) (*dbus.Conn, error) { return nil, errors.New("no bus here") }
	cases := []struct {
		name        string
		env         map[string]string
		session     Session
		idleReason  string
		focusReason string
	}{
		{
			name:    "a console has no idle time and no focus",
			env:     map[string]string{"XDG_SESSION_TYPE": "tty"},
			session: SessionNone, idleReason: "no graphical session", focusReason: "no graphical session",
		},
		{
			name:    "Wayland without Mutter has no idle time and no focus signal",
			env:     map[string]string{"XDG_SESSION_TYPE": "wayland", "WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"},
			session: SessionWayland, idleReason: "ext-idle-notify-v1", focusReason: "no focus signal",
		},
		{
			name:    "a display on another host isn't measured",
			env:     map[string]string{"DISPLAY": "remotebox:10.0"},
			session: SessionX11, idleReason: "another host", focusReason: "another host",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := Open(context.Background(), Config{
				Getenv:     func(k string) string { return tc.env[k] },
				SessionBus: noBus,
				SystemBus:  noBus,
			})
			defer d.Close()
			s := d.Status()
			if s.Session != tc.session {
				t.Errorf("session %q, want %q", s.Session, tc.session)
			}
			for _, sig := range []struct {
				what   string
				got    Signal
				reason string
			}{
				{"idle", s.Idle, tc.idleReason},
				{"focus", s.Focus, tc.focusReason},
				{"power", s.Power, "no system bus"},
				{"lock", s.Lock, "no system bus"},
			} {
				if sig.got.Known() || !strings.Contains(sig.got.Unknown, sig.reason) {
					t.Errorf("%s is %+v, want unknown because of %q", sig.what, sig.got, sig.reason)
				}
			}
			if _, err := d.Idle(context.Background()); !errors.Is(err, ErrUnknown) {
				t.Errorf("Idle: %v, want ErrUnknown", err)
			}
			if _, err := d.Locked(); !errors.Is(err, ErrLockUnknown) {
				t.Errorf("Locked: %v, want ErrLockUnknown", err)
			}
			if d.Focus() != nil || d.Power() != nil {
				t.Errorf("channels %v and %v, want nil", d.Focus(), d.Power())
			}
		})
	}
}

// TestSilentBuses gives Open and Idle buses that accept the connection and
// never answer, as a wedged dbus-daemon does. Each has to give up.
func TestSilentBuses(t *testing.T) {
	noBus := func(context.Context) (*dbus.Conn, error) { return nil, errors.New("no bus here") }
	cases := []struct {
		name   string
		env    map[string]string
		silent string // the bus that never answers: "session", "system" or, for one idle sample, "mutter"
		within time.Duration
	}{
		{"Open gives up on a session bus that never answers, and Wayland's idle time is unknown", map[string]string{"XDG_SESSION_TYPE": "wayland", "WAYLAND_DISPLAY": "wayland-0"}, "session", openTimeout + time.Second},
		{"Open gives up on a system bus that never answers, and power and lock are unknown", map[string]string{"XDG_SESSION_TYPE": "tty"}, "system", openTimeout + time.Second},
		{"an idle sample that has to reconnect to Mutter gives up within its second", nil, "mutter", sampleTimeout + time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			address := silentBus(t)
			silent := func(ctx context.Context) (*dbus.Conn, error) { return connectBus(ctx, address) }
			start := time.Now()
			if tc.silent == "mutter" {
				m := &mutterIdle{connect: silent}
				defer m.close()
				if _, err := m.idle(context.Background()); err == nil {
					t.Fatal("a sample from a bus that never answered succeeded")
				}
				if took := time.Since(start); took > tc.within {
					t.Errorf("the sample took %v, want under %v", took, tc.within)
				}
				return
			}
			cfg := Config{
				Getenv:     func(k string) string { return tc.env[k] },
				SessionBus: noBus,
				SystemBus:  noBus,
			}
			if tc.silent == "session" {
				cfg.SessionBus = silent
			} else {
				cfg.SystemBus = silent
			}
			d := Open(context.Background(), cfg)
			defer d.Close()
			if took := time.Since(start); took > tc.within {
				t.Errorf("Open took %v, want under %v", took, tc.within)
			}
			s := d.Status()
			if tc.silent == "session" && s.Idle.Known() {
				t.Errorf("idle is %+v, want unknown", s.Idle)
			}
			if tc.silent == "system" && (s.Power.Known() || s.Lock.Known()) {
				t.Errorf("power is %+v and lock %+v, want both unknown", s.Power, s.Lock)
			}
		})
	}
}

// stubIdle is an idle source that always answers a minute.
type stubIdle struct{}

func (stubIdle) idle(context.Context) (time.Duration, error) { return time.Minute, nil }
func (stubIdle) name() string                                { return "stub" }
func (stubIdle) close()                                      {}

func TestIdleLocked(t *testing.T) {
	cases := []struct {
		name   string
		lock   Signal // Status.Lock
		locked bool   // the last LockedHint logind reported
		want   bool
	}{
		{"a sample while the followed session is locked says locked", via(lockVia), true, true},
		{"a sample while the followed session is unlocked says unlocked", via(lockVia), false, false},
		{"a sample once logind is lost says unlocked, not the last LockedHint", unknown("lost the connection to logind"), true, false},
		{"a sample while the next session isn't found yet says unlocked", unknown("the session pairtime followed has ended"), true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &Desktop{idleSrc: stubIdle{}}
			d.setLock(tc.lock, tc.locked)
			i, err := d.Idle(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if i.Locked != tc.want {
				t.Errorf("Idle says locked %v, want %v", i.Locked, tc.want)
			}
			locked, err := d.Locked()
			if tc.lock.Known() {
				if err != nil || locked != tc.want {
					t.Errorf("Locked: %v, %v; want %v", locked, err, tc.want)
				}
			} else if !errors.Is(err, ErrLockUnknown) {
				t.Errorf("Locked: %v, %v; want ErrLockUnknown", locked, err)
			}
		})
	}
}

// openOnFakeX opens a desktop on a fake X display whose window manager has
// winA focused, with no buses.
func openOnFakeX(t *testing.T, ctx context.Context) (*Desktop, *fakeX) {
	t.Helper()
	f, display := newFakeDisplay(t, fakeXConfig{roots: []uint32{testRoot}})
	addTestWindows(f)
	setWM(f, "ewmh", winA)
	noBus := func(context.Context) (*dbus.Conn, error) { return nil, errors.New("no bus here") }
	env := map[string]string{"XDG_SESSION_TYPE": "x11", "DISPLAY": display}
	d := Open(ctx, Config{
		Getenv:     func(k string) string { return env[k] },
		SessionBus: noBus,
		SystemBus:  noBus,
		Hostname:   testHost,
	})
	t.Cleanup(func() { d.Close() })
	if s := d.Status(); s.Idle.Via != screenSaverExtension || s.Focus.Via != focusViaXRes {
		t.Fatalf("status %+v, want idle time and focus from the fake X server", s)
	}
	return d, f
}

// nextReport waits for the next focus report.
func nextReport(t *testing.T, ch <-chan Focus) Focus {
	t.Helper()
	select {
	case f, ok := <-ch:
		if !ok {
			t.Fatal("the focus channel closed")
		}
		return f
	case <-time.After(5 * time.Second):
		t.Fatal("no focus report in 5 s")
	}
	return Focus{}
}

// eventually waits up to 5 s for cond.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("waited 5 s for this: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDesktopFocusOnFakeX(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T, d *Desktop, f *fakeX, cancel context.CancelFunc)
	}{
		{
			name: "ending Open's ctx without Close closes Focus and the X connection behind it",
			run: func(t *testing.T, d *Desktop, f *fakeX, cancel context.CancelFunc) {
				focus := d.Focus()
				if r := nextReport(t, focus); r.Window != winA || r.PID != 4242 {
					t.Fatalf("first report %+v, want winA with pid 4242", r)
				}
				cancel()
				timeout := time.After(5 * time.Second)
				for open := true; open; {
					select {
					case _, open = <-focus:
					case <-timeout:
						t.Fatal("Focus stayed open after ctx ended")
					}
				}
				// The connection Idle uses stays until Close.
				eventually(t, "only the idle connection is left", func() bool {
					_, live := f.connections()
					return live == 1
				})
				if s := d.Status(); s.Focus.Known() {
					t.Errorf("focus is %+v after watching stopped, want unknown", s.Focus)
				}
			},
		},
		{
			name: "focus is unknown while the X server is down and comes back with it, naming the window focused then",
			run: func(t *testing.T, d *Desktop, f *fakeX, cancel context.CancelFunc) {
				focus := d.Focus()
				nextReport(t, focus)
				f.down()
				eventually(t, "focus is unknown", func() bool { return !d.Status().Focus.Known() })
				f.setCardinal(testRoot, "_NET_ACTIVE_WINDOW", atomWindow, winB)
				f.up(t)
				if r := nextReport(t, focus); r.Window != winB || r.PID != 777 {
					t.Fatalf("first report after the restart %+v, want winB with pid 777", r)
				}
				if s := d.Status(); s.Focus.Via != focusViaXRes {
					t.Errorf("focus is %+v after the restart", s.Focus)
				}
				f.setActive(testRoot, winA)
				if r := nextReport(t, focus); r.Window != winA || r.PID != 4242 {
					t.Fatalf("report %+v, want winA with pid 4242 from the new connection", r)
				}
				// Idle reconnects too: its first sample finds the old
				// connection dead.
				f.setIdle(2500)
				var got Idle
				var err error
				for range 2 {
					if got, err = d.Idle(context.Background()); err == nil {
						break
					}
				}
				if err != nil || got.Any != 2500*time.Millisecond {
					t.Errorf("idle after the restart %v, %v; want 2.5s", got.Any, err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			d, f := openOnFakeX(t, ctx)
			tc.run(t, d, f, cancel)
		})
	}
}

func TestIdleAfterClose(t *testing.T) {
	cases := []struct {
		name string
		// start returns a sample, the close that ends it, and a count of
		// the connections it has made.
		start func(t *testing.T) (sample func() error, close func(), dials func() int)
	}{
		{
			name: "an X11 sample after close fails and opens no connection",
			start: func(t *testing.T) (func() error, func(), func() int) {
				f := newFakeX(t, fakeXConfig{roots: []uint32{testRoot}})
				src := &x11Idle{open: func(deadline time.Time) (*xconn, error) { return f.dialBy(0, nil, deadline) }}
				sample := func() error {
					_, err := src.idle(context.Background())
					return err
				}
				return sample, src.close, func() int {
					n, _ := f.connections()
					return n
				}
			},
		},
		{
			name: "a Mutter sample after close fails and connects to no bus",
			start: func(t *testing.T) (func() error, func(), func() int) {
				n := 0
				src := &mutterIdle{connect: func(context.Context) (*dbus.Conn, error) {
					n++
					return nil, errors.New("no bus here")
				}}
				sample := func() error {
					_, err := src.idle(context.Background())
					return err
				}
				return sample, src.close, func() int { return n }
			},
		},
		{
			name: "Desktop.Idle after Close fails and opens no connection",
			start: func(t *testing.T) (func() error, func(), func() int) {
				d, f := openOnFakeX(t, context.Background())
				sample := func() error {
					_, err := d.Idle(context.Background())
					return err
				}
				return sample, func() { d.Close() }, func() int {
					n, _ := f.connections()
					return n
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sample, closeSource, dials := tc.start(t)
			sample() // connects, where there is something to connect to
			closeSource()
			before := dials()
			if err := sample(); !errors.Is(err, net.ErrClosed) {
				t.Errorf("a sample after close: %v, want net.ErrClosed", err)
			}
			if after := dials(); after != before {
				t.Errorf("the sample after close made %d connections", after-before)
			}
		})
	}
}
