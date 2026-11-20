// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux && desktop

package linux

// These tests run on the private session bus desktop_session.sh starts,
// with fakes of Mutter's IdleMonitor and of logind exporting the
// interfaces their documentation gives (org.gnome.Mutter.IdleMonitor.xml;
// org.freedesktop.login1(5)). logind lives on the system bus; here the
// test's private bus stands in for it.

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/prop"
	"github.com/some-du6e/pairtime/pkg/event"
)

func testBus(t *testing.T) *dbus.Conn {
	t.Helper()
	address := os.Getenv("DBUS_SESSION_BUS_ADDRESS")
	if address == "" {
		t.Fatal("DBUS_SESSION_BUS_ADDRESS is unset: run this under tools/ci/desktop_session.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := connectBus(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// own claims a well-known name on conn, as the real service does.
func own(t *testing.T, conn *dbus.Conn, name string) {
	t.Helper()
	reply, err := conn.RequestName(name, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claiming %s: %v, reply %v", name, err, reply)
	}
}

// fakeMutter is GNOME's org.gnome.Mutter.IdleMonitor.
type fakeMutter struct{ ms atomic.Uint64 }

func (m *fakeMutter) GetIdletime() (uint64, *dbus.Error) { return m.ms.Load(), nil }

func TestDesktopMutterIdle(t *testing.T) {
	cases := []struct {
		name    string
		mutter  bool
		samples []uint64 // Mutter's milliseconds at each sample
		leave   bool     // Mutter leaves the bus before the last sample
	}{
		{name: "Mutter's GetIdletime milliseconds are the idle time on Wayland", mutter: true, samples: []uint64{7250}},
		{name: "each sample reads Mutter's counter again", mutter: true, samples: []uint64{40000, 3}},
		{name: "a sample after Mutter leaves the bus fails rather than repeat a value", mutter: true, samples: []uint64{500, 600}, leave: true},
		{name: "without Mutter, idle time on Wayland is unknown", mutter: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &fakeMutter{}
			var service *dbus.Conn
			if tc.mutter {
				service = testBus(t)
				if err := service.Export(m, mutterPath, mutterIface); err != nil {
					t.Fatal(err)
				}
				own(t, service, mutterName)
			}
			env := map[string]string{
				"XDG_SESSION_TYPE":         "wayland",
				"WAYLAND_DISPLAY":          "wayland-test",
				"DBUS_SESSION_BUS_ADDRESS": os.Getenv("DBUS_SESSION_BUS_ADDRESS"),
			}
			d := Open(context.Background(), Config{
				Getenv:    func(k string) string { return env[k] },
				SystemBus: func(context.Context) (*dbus.Conn, error) { return nil, errors.New("not in this test") },
			})
			defer d.Close()
			s := d.Status()
			if s.Focus.Known() || !strings.Contains(s.Focus.Unknown, "no focus signal") {
				t.Errorf("focus on Wayland is %+v, want unknown", s.Focus)
			}
			if !tc.mutter {
				if s.Idle.Known() || !strings.Contains(s.Idle.Unknown, "org.gnome.Mutter.IdleMonitor") {
					t.Errorf("idle is %+v, want unknown for want of Mutter", s.Idle)
				}
				if _, err := d.Idle(context.Background()); !errors.Is(err, ErrUnknown) {
					t.Errorf("Idle: %v, want ErrUnknown", err)
				}
				return
			}
			if s.Idle.Via != mutterName {
				t.Fatalf("idle is %+v, want it from Mutter", s.Idle)
			}
			for i, ms := range tc.samples {
				m.ms.Store(ms)
				if tc.leave && i == len(tc.samples)-1 {
					if _, err := service.ReleaseName(mutterName); err != nil {
						t.Fatal(err)
					}
					if got, err := d.Idle(context.Background()); err == nil {
						t.Fatalf("a sample with Mutter gone gave %v", got.Any)
					}
					return
				}
				got, err := d.Idle(context.Background())
				if err != nil || got.Any != time.Duration(ms)*time.Millisecond {
					t.Fatalf("sample %d: %v, %v; want %d ms", i, got.Any, err, ms)
				}
			}
		})
	}
}

// fakeLogind is systemd-logind's Manager, with sessions for the user and
// the user itself, on its own bus connection. It starts with session "2",
// the user's display session.
type fakeLogind struct {
	conn *dbus.Conn
	user *prop.Properties

	mu         sync.Mutex
	pidSession dbus.ObjectPath // GetSessionByPID's answer; "" for none, as for an agent outside any session
	sessions   map[dbus.ObjectPath]*prop.Properties
	released   []chan struct{} // closed when each delay lock is released
	sent       []*os.File      // the fake's copies of the locks it handed out
	what       []string        // each Inhibit's what and mode
}

const (
	fakeSessionPath = dbus.ObjectPath("/org/freedesktop/login1/session/_32")
	fakeUserPath    = dbus.ObjectPath("/org/freedesktop/login1/user/_1000")
)

// fakeSession is the object path logind gives session id, a single digit:
// a path element can't start with a digit, so "2" is escaped as "_32".
func fakeSession(id string) dbus.ObjectPath {
	return dbus.ObjectPath("/org/freedesktop/login1/session/_3" + id)
}

// fakeDisplay is the user's Display property, a struct (so).
type fakeDisplay struct {
	ID   string
	Path dbus.ObjectPath
}

func (f *fakeLogind) GetSessionByPID(pid uint32) (dbus.ObjectPath, *dbus.Error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pidSession == "" {
		return "", dbus.NewError("org.freedesktop.login1.NoSessionForPID", []any{"PID does not belong to any known session"})
	}
	return f.pidSession, nil
}

func (f *fakeLogind) GetUser(uid uint32) (dbus.ObjectPath, *dbus.Error) {
	return fakeUserPath, nil
}

// Inhibit hands out a pipe's write end as the lock; the read end sees EOF
// once every copy of it is closed, which is the release.
func (f *fakeLogind) Inhibit(what, who, why, mode string) (dbus.UnixFD, *dbus.Error) {
	r, w, err := os.Pipe()
	if err != nil {
		return 0, dbus.MakeFailedError(err)
	}
	done := make(chan struct{})
	go func() {
		io.Copy(io.Discard, r)
		r.Close()
		close(done)
	}()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released = append(f.released, done)
	f.sent = append(f.sent, w)
	f.what = append(f.what, what+" "+mode)
	return dbus.UnixFD(w.Fd()), nil
}

// dropSent closes the fake's own copies of the locks it handed out, once
// the replies carrying them have gone, so only pairtime's copies remain.
func (f *fakeLogind) dropSent() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, w := range f.sent {
		w.Close()
	}
	f.sent = nil
}

func (f *fakeLogind) locks() []chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]chan struct{}(nil), f.released...)
}

func (f *fakeLogind) sleep(start bool) error {
	return f.conn.Emit(logindPath, managerIface+".PrepareForSleep", start)
}

// addSession exports a session, active, with the LockedHint given, which
// it announces when it changes, as logind does.
func (f *fakeLogind) addSession(t *testing.T, path dbus.ObjectPath, locked bool) {
	t.Helper()
	props, err := prop.Export(f.conn, path, prop.Map{
		sessionIface: {
			"LockedHint": {Value: locked, Emit: prop.EmitTrue},
			"State":      {Value: "active", Emit: prop.EmitFalse},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[path] = props
}

// setSession sets one of a session's properties.
func (f *fakeLogind) setSession(path dbus.ObjectPath, name string, v any) {
	f.mu.Lock()
	props := f.sessions[path]
	f.mu.Unlock()
	props.SetMust(sessionIface, name, v)
}

// do runs one step of a session test: "end ID" ends session ID and leaves
// the user no display session, "login ID" starts session ID and makes it
// the display session, unlocked, or locked with "login ID locked", "close
// ID" leaves session ID closing, as a logout does while the agent runs on
// in it, and "lock ID" and "unlock ID" set session ID's LockedHint.
func (f *fakeLogind) do(t *testing.T, step string) {
	t.Helper()
	words := strings.Fields(step)
	if len(words) < 2 {
		t.Fatalf("unknown step %q", step)
	}
	id := words[1]
	path := fakeSession(id)
	switch words[0] {
	case "end":
		f.user.SetMust(userIface, "Display", fakeDisplay{"", "/"})
		f.mu.Lock()
		delete(f.sessions, path)
		f.mu.Unlock()
		if err := f.conn.Export(nil, path, propsIface); err != nil {
			t.Fatal(err)
		}
		if err := f.conn.Emit(logindPath, managerIface+".SessionRemoved", id, path); err != nil {
			t.Fatal(err)
		}
	case "login":
		f.addSession(t, path, len(words) > 2 && words[2] == "locked")
		f.user.SetMust(userIface, "Display", fakeDisplay{id, path})
		if err := f.conn.Emit(logindPath, managerIface+".SessionNew", id, path); err != nil {
			t.Fatal(err)
		}
	case "close":
		f.setSession(path, "State", "closing")
	case "lock", "unlock":
		f.setSession(path, "LockedHint", words[0] == "lock")
	default:
		t.Fatalf("unknown step %q", step)
	}
}

func startFakeLogind(t *testing.T, locked, noSession bool) *fakeLogind {
	t.Helper()
	f := &fakeLogind{conn: testBus(t), sessions: map[dbus.ObjectPath]*prop.Properties{}}
	if !noSession {
		f.pidSession = fakeSessionPath
	}
	if err := f.conn.Export(f, logindPath, managerIface); err != nil {
		t.Fatal(err)
	}
	f.addSession(t, fakeSessionPath, locked)
	var err error
	f.user, err = prop.Export(f.conn, fakeUserPath, prop.Map{
		userIface: {"Display": {Value: fakeDisplay{"2", fakeSessionPath}, Emit: prop.EmitFalse}},
	})
	if err != nil {
		t.Fatal(err)
	}
	own(t, f.conn, logindName)
	t.Cleanup(func() {
		f.dropSent()
		f.conn.Close()
	})
	return f
}

// openWithLogind opens a desktop whose system bus is the test bus.
func openWithLogind(t *testing.T, readyTimeout time.Duration) *Desktop {
	t.Helper()
	d := Open(context.Background(), Config{
		Getenv: func(string) string { return "" },
		SystemBus: func(ctx context.Context) (*dbus.Conn, error) {
			return connectBus(ctx, os.Getenv("DBUS_SESSION_BUS_ADDRESS"))
		},
		ReadyTimeout: readyTimeout,
	})
	t.Cleanup(func() { d.Close() })
	return d
}

func nextPower(t *testing.T, ch <-chan Power) Power {
	t.Helper()
	select {
	case p, ok := <-ch:
		if !ok {
			t.Fatal("the power channel closed")
		}
		if err := p.Payload().Validate(); err != nil {
			t.Errorf("os.power: %v", err)
		}
		return p
	case <-time.After(10 * time.Second):
		t.Fatal("no power event in 10 s")
	}
	return Power{}
}

func noPower(t *testing.T, ch <-chan Power) {
	t.Helper()
	select {
	case p := <-ch:
		t.Fatalf("an unexpected power event: %+v", p)
	case <-time.After(300 * time.Millisecond):
	}
}

func released(c chan struct{}, within time.Duration) bool {
	select {
	case <-c:
		return true
	case <-time.After(within):
		return false
	}
}

func TestDesktopLogindSleep(t *testing.T) {
	cases := []struct {
		name         string
		readyTimeout time.Duration
		callReady    bool
	}{
		{"a suspend holds logind's delay lock until Ready, and the resume takes the next", time.Minute, true},
		{"a suspend whose Ready never comes lets the sleep go after the timeout", 300 * time.Millisecond, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := startFakeLogind(t, false, false)
			d := openWithLogind(t, tc.readyTimeout)
			f.dropSent()
			if s := d.Status(); s.Power.Via == "" {
				t.Fatalf("power is %+v, want logind", s.Power)
			}
			locks := f.locks()
			if len(locks) != 1 || released(locks[0], 300*time.Millisecond) {
				t.Fatalf("%d delay locks, want 1 held", len(locks))
			}
			f.mu.Lock()
			what := f.what[0]
			f.mu.Unlock()
			if what != "sleep delay" {
				t.Errorf("inhibited %q, want a sleep delay lock", what)
			}

			if err := f.sleep(true); err != nil {
				t.Fatal(err)
			}
			p := nextPower(t, d.Power())
			if p.Op != event.PowerSuspend {
				t.Fatalf("got %q, want suspend", p.Op)
			}
			if tc.callReady {
				if released(locks[0], 300*time.Millisecond) {
					t.Fatal("the delay lock went before Ready")
				}
				p.Ready()
			}
			if !released(locks[0], 5*time.Second) {
				t.Fatal("the delay lock is still held")
			}

			if err := f.sleep(false); err != nil {
				t.Fatal(err)
			}
			if p := nextPower(t, d.Power()); p.Op != event.PowerResume {
				t.Fatalf("got %q, want resume", p.Op)
			}
			f.dropSent()
			locks = f.locks()
			if len(locks) != 2 || released(locks[1], 300*time.Millisecond) {
				t.Fatalf("%d delay locks after the resume, want a second one held", len(locks))
			}
			d.Close()
			if !released(locks[1], 5*time.Second) {
				t.Fatal("Close left the delay lock held")
			}
		})
	}
}

func TestDesktopLogindLock(t *testing.T) {
	cases := []struct {
		name      string
		startLock bool
		noSession bool
		hints     []bool // LockedHint values the session goes through
		want      []event.PowerOp
	}{
		{name: "LockedHint going true then false is a lock then an unlock", hints: []bool{true, false}, want: []event.PowerOp{event.PowerLock, event.PowerUnlock}},
		{name: "a session locked at start begins with a lock", startLock: true, hints: []bool{false}, want: []event.PowerOp{event.PowerLock, event.PowerUnlock}},
		{name: "an agent outside any session follows the user's display session", noSession: true, hints: []bool{true}, want: []event.PowerOp{event.PowerLock}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := startFakeLogind(t, tc.startLock, tc.noSession)
			d := openWithLogind(t, time.Minute)
			f.dropSent()
			if s := d.Status(); s.Lock.Via == "" {
				t.Fatalf("lock is %+v, want logind's LockedHint", s.Lock)
			}
			var got []event.PowerOp
			if tc.startLock {
				got = append(got, nextPower(t, d.Power()).Op)
			}
			for _, locked := range tc.hints {
				f.setSession(fakeSessionPath, "LockedHint", locked)
				got = append(got, nextPower(t, d.Power()).Op)
				if got, err := d.Locked(); err != nil || got != locked {
					t.Errorf("after LockedHint %v, Locked says %v, %v", locked, got, err)
				}
			}
			if strings.Join(opsText(got), " ") != strings.Join(opsText(tc.want), " ") {
				t.Errorf("events %q, want %q", got, tc.want)
			}
			noPower(t, d.Power())
		})
	}
}

func opsText(ops []event.PowerOp) []string {
	s := make([]string, len(ops))
	for i, op := range ops {
		s[i] = string(op)
	}
	return s
}

func TestDesktopLogindSender(t *testing.T) {
	cases := []struct {
		name    string
		unicast bool // addressed to pairtime's connection, past the match rules
	}{
		{"a broadcast PrepareForSleep from a process that isn't logind is ignored", false},
		{"a PrepareForSleep sent straight to pairtime by a process that isn't logind is ignored", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := startFakeLogind(t, false, false)
			d := openWithLogind(t, time.Minute)
			f.dropSent()
			spoof := testBus(t)
			msg := &dbus.Message{
				Type: dbus.TypeSignal,
				Headers: map[dbus.HeaderField]dbus.Variant{
					dbus.FieldPath:      dbus.MakeVariant(logindPath),
					dbus.FieldInterface: dbus.MakeVariant(managerIface),
					dbus.FieldMember:    dbus.MakeVariant("PrepareForSleep"),
					dbus.FieldSignature: dbus.MakeVariant(dbus.SignatureOf(true)),
				},
				Body: []any{true},
			}
			if tc.unicast {
				msg.Headers[dbus.FieldDestination] = dbus.MakeVariant(d.logind.conn.Names()[0])
			}
			if call := spoof.Send(msg, nil); call.Err != nil {
				t.Fatal(call.Err)
			}
			noPower(t, d.Power())
			// logind itself still gets through.
			if err := f.sleep(true); err != nil {
				t.Fatal(err)
			}
			if p := nextPower(t, d.Power()); p.Op != event.PowerSuspend {
				t.Fatalf("got %q, want logind's suspend", p.Op)
			}
		})
	}
}

func TestDesktopWithoutLogind(t *testing.T) {
	cases := []struct {
		name string
	}{
		{"without logind on the bus, power and lock are unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := openWithLogind(t, time.Minute)
			s := d.Status()
			for _, sig := range []Signal{s.Power, s.Lock} {
				if sig.Known() || !strings.Contains(sig.Unknown, "logind isn't on the system bus") {
					t.Errorf("status %+v, want power and lock unknown for want of logind", s)
				}
			}
			if d.Power() != nil {
				t.Error("a power channel without logind")
			}
		})
	}
}

// lockState is what Locked says: "locked", "unlocked" or "unknown".
func lockState(d *Desktop) string {
	locked, err := d.Locked()
	switch {
	case err != nil:
		return "unknown"
	case locked:
		return "locked"
	}
	return "unlocked"
}

// waitLock waits for Locked to say want, as lockState spells it.
func waitLock(t *testing.T, d *Desktop, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for lockState(d) != want {
		if time.Now().After(deadline) {
			t.Fatalf("lock is %s, want %s; status %+v", lockState(d), want, d.Status().Lock)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestDesktopLogindSessions(t *testing.T) {
	type step struct {
		do    string        // a fakeLogind.do step
		lock  string        // Locked afterwards, as lockState spells it
		event event.PowerOp // the event the step makes, or "" for none
	}
	cases := []struct {
		name      string
		noSession bool // the agent is in no session, as a systemd user unit
		startLock bool
		steps     []step
	}{
		{
			name:      "when the display session ends, lock is unknown until the next login, whose locks then count",
			noSession: true,
			steps: []step{
				{"end 2", "unknown", ""},
				{"login 3", "unlocked", ""},
				{"lock 3", "locked", event.PowerLock},
				{"unlock 3", "unlocked", event.PowerUnlock},
			},
		},
		{
			name:      "a session that ends locked counts as unlocked once the next login is found",
			noSession: true, startLock: true,
			steps: []step{
				{"end 2", "unknown", ""},
				{"login 3", "unlocked", event.PowerUnlock},
			},
		},
		{
			name: "a login while the agent's own session is closing moves lock to the new session",
			steps: []step{
				{"close 2", "unlocked", ""},
				{"login 3 locked", "locked", event.PowerLock},
				{"unlock 2", "locked", ""},
				{"unlock 3", "unlocked", event.PowerUnlock},
			},
		},
		{
			name: "a login while the agent's own session goes on changes nothing",
			steps: []step{
				{"login 3 locked", "unlocked", ""},
				{"lock 2", "locked", event.PowerLock},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := startFakeLogind(t, tc.startLock, tc.noSession)
			d := openWithLogind(t, time.Minute)
			f.dropSent()
			if tc.startLock {
				if p := nextPower(t, d.Power()); p.Op != event.PowerLock {
					t.Fatalf("got %q, want the lock the session starts with", p.Op)
				}
			}
			for _, s := range tc.steps {
				f.do(t, s.do)
				waitLock(t, d, s.lock)
				if s.event == "" {
					noPower(t, d.Power())
					continue
				}
				if p := nextPower(t, d.Power()); p.Op != s.event {
					t.Fatalf("after %q got %q, want %q", s.do, p.Op, s.event)
				}
			}
			noPower(t, d.Power())
		})
	}
}

func TestDesktopLogindLost(t *testing.T) {
	cases := []struct {
		name string
	}{
		{"after the system bus connection drops, lock is unknown rather than its last LockedHint"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			startFakeLogind(t, true, false)
			d := openWithLogind(t, time.Minute)
			if p := nextPower(t, d.Power()); p.Op != event.PowerLock {
				t.Fatalf("got %q, want the lock the session starts with", p.Op)
			}
			waitLock(t, d, "locked")
			d.logind.conn.Close()
			waitLock(t, d, "unknown")
			if s := d.Status(); s.Power.Known() || s.Lock.Known() {
				t.Errorf("power is %+v and lock %+v, want both unknown", s.Power, s.Lock)
			}
		})
	}
}
