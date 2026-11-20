// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package linux

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/some-du6e/pairtime/pkg/event"
)

// fakeLock is a delay lock that records when it is released.
type fakeLock struct {
	id  int
	log *[]string
}

func (l *fakeLock) Close() error {
	*l.log = append(*l.log, fmt.Sprintf("release %d", l.id))
	return nil
}

// trackerRig is a powerTracker whose locks and timers the test controls.
type trackerRig struct {
	t      *powerTracker
	log    []string
	taken  int
	fail   bool     // taking a lock fails
	timers []func() // timeouts armed, in order
	ready  []func() // the Ready of each suspend
	ops    []event.PowerOp
}

func newTrackerRig() *trackerRig {
	r := &trackerRig{}
	r.t = &powerTracker{
		take: func() (io.Closer, error) {
			if r.fail {
				return nil, errors.New("polkit says no")
			}
			r.taken++
			r.log = append(r.log, fmt.Sprintf("take %d", r.taken))
			return &fakeLock{id: r.taken, log: &r.log}, nil
		},
		timeout: 3 * time.Second,
		after:   func(_ time.Duration, f func()) { r.timers = append(r.timers, f) },
	}
	return r
}

// do runs one step: "hold", "suspend", "resume", "ready N" (call the Nth
// suspend's Ready), "timeout N" (fire the Nth timer), "lock", "unlock",
// "start-locked", "start-unlocked", "shutdown" or "fail" (taking locks fails
// from now on).
func (r *trackerRig) do(step string) {
	var n int
	switch {
	case step == "hold":
		r.t.hold()
	case step == "suspend":
		p := r.t.suspend(time.Now())
		r.ops = append(r.ops, p.Op)
		r.ready = append(r.ready, p.Ready)
	case step == "resume":
		r.ops = append(r.ops, r.t.resume(time.Now()).Op)
	case step == "lock" || step == "unlock":
		if p, ok := r.t.lockedHint(step == "lock", time.Now()); ok {
			r.ops = append(r.ops, p.Op)
		}
	case step == "start-locked" || step == "start-unlocked":
		locked := step == "start-locked"
		r.t.initial = &locked
		if p, ok := r.t.start(time.Now()); ok {
			r.ops = append(r.ops, p.Op)
		}
	case step == "shutdown":
		r.t.shutdown()
	case step == "fail":
		r.fail = true
	case sscan(step, "ready %d", &n):
		r.ready[n-1]()
	case sscan(step, "timeout %d", &n):
		r.timers[n-1]()
	default:
		panic("unknown step " + step)
	}
}

func sscan(s, format string, n *int) bool {
	_, err := fmt.Sscanf(s, format, n)
	return err == nil
}

func TestPowerTracker(t *testing.T) {
	cases := []struct {
		name    string
		steps   []string
		wantLog []string // lock takes and releases, in order
		wantOps []event.PowerOp
	}{
		{
			name:    "a suspend holds the delay lock until its Ready",
			steps:   []string{"hold", "suspend", "ready 1"},
			wantLog: []string{"take 1", "release 1"},
			wantOps: []event.PowerOp{event.PowerSuspend},
		},
		{
			name:    "Ready releases the lock only once",
			steps:   []string{"hold", "suspend", "ready 1", "ready 1", "timeout 1"},
			wantLog: []string{"take 1", "release 1"},
			wantOps: []event.PowerOp{event.PowerSuspend},
		},
		{
			name:    "the timeout releases the lock when Ready doesn't come",
			steps:   []string{"hold", "suspend", "timeout 1"},
			wantLog: []string{"take 1", "release 1"},
			wantOps: []event.PowerOp{event.PowerSuspend},
		},
		{
			name:    "a resume takes the lock for the next sleep",
			steps:   []string{"hold", "suspend", "ready 1", "resume", "suspend", "ready 2"},
			wantLog: []string{"take 1", "release 1", "take 2", "release 2"},
			wantOps: []event.PowerOp{event.PowerSuspend, event.PowerResume, event.PowerSuspend},
		},
		{
			name:    "a resume releases a lock Ready never released before taking the next",
			steps:   []string{"hold", "suspend", "resume"},
			wantLog: []string{"take 1", "release 1", "take 2"},
			wantOps: []event.PowerOp{event.PowerSuspend, event.PowerResume},
		},
		{
			name:    "a late Ready from the last sleep leaves the new lock held",
			steps:   []string{"hold", "suspend", "resume", "ready 1", "timeout 1"},
			wantLog: []string{"take 1", "release 1", "take 2"},
			wantOps: []event.PowerOp{event.PowerSuspend, event.PowerResume},
		},
		{
			name:    "a resume with a lock still held takes no second one",
			steps:   []string{"hold", "resume"},
			wantLog: []string{"take 1"},
			wantOps: []event.PowerOp{event.PowerResume},
		},
		{
			name:    "a suspend without a lock is still reported",
			steps:   []string{"fail", "hold", "suspend", "ready 1"},
			wantLog: nil,
			wantOps: []event.PowerOp{event.PowerSuspend},
		},
		{
			name:    "shutdown releases the held lock, so a stopped agent never delays sleep",
			steps:   []string{"hold", "shutdown"},
			wantLog: []string{"take 1", "release 1"},
		},
		{
			name:    "shutdown during a suspend releases that suspend's lock",
			steps:   []string{"hold", "suspend", "shutdown", "ready 1"},
			wantLog: []string{"take 1", "release 1"},
			wantOps: []event.PowerOp{event.PowerSuspend},
		},
		{
			name:    "LockedHint going true then false is a lock then an unlock",
			steps:   []string{"start-unlocked", "lock", "unlock"},
			wantOps: []event.PowerOp{event.PowerLock, event.PowerUnlock},
		},
		{
			name:    "a repeated LockedHint value is no event",
			steps:   []string{"start-unlocked", "lock", "lock", "unlock", "unlock"},
			wantOps: []event.PowerOp{event.PowerLock, event.PowerUnlock},
		},
		{
			name:    "a session locked at start begins with a lock",
			steps:   []string{"start-locked", "unlock"},
			wantOps: []event.PowerOp{event.PowerLock, event.PowerUnlock},
		},
		{
			name:    "a session unlocked at start begins with nothing",
			steps:   []string{"start-unlocked"},
			wantOps: nil,
		},
		{
			name:    "the first LockedHint change without a start reading counts from unlocked",
			steps:   []string{"unlock", "lock"},
			wantOps: []event.PowerOp{event.PowerLock},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTrackerRig()
			for _, s := range tc.steps {
				r.do(s)
			}
			if !slices.Equal(r.log, tc.wantLog) {
				t.Errorf("locks %q, want %q", r.log, tc.wantLog)
			}
			if !slices.Equal(r.ops, tc.wantOps) {
				t.Errorf("events %q, want %q", r.ops, tc.wantOps)
			}
		})
	}
}

func TestLogindHandle(t *testing.T) {
	const owner = ":1.7"
	session := dbus.ObjectPath("/org/freedesktop/login1/session/_32")
	locked := func(v bool) []any {
		return []any{sessionIface, map[string]dbus.Variant{"LockedHint": dbus.MakeVariant(v)}, []string{}}
	}
	cases := []struct {
		name  string
		sigs  []*dbus.Signal
		want  []event.PowerOp
		owner string // logind's unique name after the signals
	}{
		{
			name: "PrepareForSleep true and false are a suspend and a resume",
			sigs: []*dbus.Signal{
				{Sender: owner, Path: logindPath, Name: managerIface + ".PrepareForSleep", Body: []any{true}},
				{Sender: owner, Path: logindPath, Name: managerIface + ".PrepareForSleep", Body: []any{false}},
			},
			want: []event.PowerOp{event.PowerSuspend, event.PowerResume}, owner: owner,
		},
		{
			name: "a signal from anyone but logind is ignored",
			sigs: []*dbus.Signal{
				{Sender: ":1.99", Path: logindPath, Name: managerIface + ".PrepareForSleep", Body: []any{true}},
				{Sender: ":1.99", Path: session, Name: propsIface + ".PropertiesChanged", Body: locked(true)},
			},
			want: nil, owner: owner,
		},
		{
			name: "PrepareForSleep on another path is ignored",
			sigs: []*dbus.Signal{
				{Sender: owner, Path: "/elsewhere", Name: managerIface + ".PrepareForSleep", Body: []any{true}},
			},
			want: nil, owner: owner,
		},
		{
			name: "a malformed PrepareForSleep is ignored",
			sigs: []*dbus.Signal{
				{Sender: owner, Path: logindPath, Name: managerIface + ".PrepareForSleep", Body: []any{"yes"}},
				{Sender: owner, Path: logindPath, Name: managerIface + ".PrepareForSleep", Body: nil},
			},
			want: nil, owner: owner,
		},
		{
			name: "LockedHint changes on the session are a lock and an unlock",
			sigs: []*dbus.Signal{
				{Sender: owner, Path: session, Name: propsIface + ".PropertiesChanged", Body: locked(true)},
				{Sender: owner, Path: session, Name: propsIface + ".PropertiesChanged", Body: locked(false)},
			},
			want: []event.PowerOp{event.PowerLock, event.PowerUnlock}, owner: owner,
		},
		{
			name: "LockedHint on another session is ignored",
			sigs: []*dbus.Signal{
				{Sender: owner, Path: "/org/freedesktop/login1/session/_33", Name: propsIface + ".PropertiesChanged", Body: locked(true)},
			},
			want: nil, owner: owner,
		},
		{
			name: "other session properties are ignored",
			sigs: []*dbus.Signal{
				{Sender: owner, Path: session, Name: propsIface + ".PropertiesChanged", Body: []any{sessionIface, map[string]dbus.Variant{"IdleHint": dbus.MakeVariant(true)}, []string{}}},
			},
			want: nil, owner: owner,
		},
		{
			name: "after logind restarts, its new name is trusted and its old one isn't",
			sigs: []*dbus.Signal{
				{Sender: busName, Path: "/org/freedesktop/DBus", Name: busName + ".NameOwnerChanged", Body: []any{logindName, owner, ":1.50"}},
				{Sender: owner, Path: logindPath, Name: managerIface + ".PrepareForSleep", Body: []any{true}},
				{Sender: ":1.50", Path: logindPath, Name: managerIface + ".PrepareForSleep", Body: []any{true}},
			},
			want: []event.PowerOp{event.PowerSuspend}, owner: ":1.50",
		},
		{
			name: "a NameOwnerChanged that isn't from the bus changes nothing",
			sigs: []*dbus.Signal{
				{Sender: ":1.99", Path: "/org/freedesktop/DBus", Name: busName + ".NameOwnerChanged", Body: []any{logindName, owner, ":1.99"}},
				{Sender: ":1.99", Path: logindPath, Name: managerIface + ".PrepareForSleep", Body: []any{true}},
			},
			want: nil, owner: owner,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newTrackerRig()
			l := &logind{owner: owner, session: session, track: rig.t}
			var got []event.PowerOp
			for _, sig := range tc.sigs {
				for _, p := range l.handle(sig, time.Now()) {
					got = append(got, p.Op)
				}
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("events %q, want %q", got, tc.want)
			}
			if l.owner != tc.owner {
				t.Errorf("trusted sender %q, want %q", l.owner, tc.owner)
			}
			if want := slices.Contains(got, event.PowerLock) && !slices.Contains(got, event.PowerUnlock); l.locked.Load() != want {
				t.Errorf("locked %v, want %v", l.locked.Load(), want)
			}
		})
	}
}
