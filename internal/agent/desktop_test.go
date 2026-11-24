// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/focus"
	"github.com/some-du6e/pairtime/internal/agent/presence"
	"github.com/some-du6e/pairtime/internal/agent/sched"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/darwin"
	"github.com/some-du6e/pairtime/pkg/event"
)

// TestFocusLevelC checks GET /v1/status's signals.focus_level_c: set while
// focus is unknown and the agent reads idle time, where pkg/score credits
// the desktop's slots at level C (SM 2.5; WP-SC8), and unset where focus is
// known, or idle time is unknown too and nothing typed there counts.
func TestFocusLevelC(t *testing.T) {
	tests := []struct {
		name        string
		idle, focus signalReply
		want        bool
	}{
		{"focus unknown with idle time read, as on GNOME on Wayland, is level C",
			via("org.gnome.Mutter.IdleMonitor"), unknown("Wayland gives other programs no focus signal"), true},
		{"focus known is no level C",
			via("MIT-SCREEN-SAVER"), via("_NET_ACTIVE_WINDOW"), false},
		{"focus and idle time both unknown is no level C, since nothing typed there counts",
			unknown("no X server and no Mutter on the session bus"), unknown("no X server"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &agent{signals: func() signalsReply { return signalsReply{Idle: tt.idle, Focus: tt.focus} }}
			if got := a.signalsNow().FocusLevelC; got != tt.want {
				t.Errorf("focus_level_c %v, want %v", got, tt.want)
			}
		})
	}
	if (&agent{}).signalsNow().FocusLevelC {
		t.Error("an agent that hasn't opened the OS's sources says focus_level_c")
	}
}

// TestMacLoopSignals checks what GET /v1/status says about a MacLoop that
// can't report the Loop's own Status: one the agent runs without, one
// that hasn't started, and, off macOS, one whose Run failed. The Loop's
// Status itself comes from macOS, so cmd/pairtime's TestAgentOnMac
// (platform.yml's macOS job) checks it.
func TestMacLoopSignals(t *testing.T) {
	tests := []struct {
		name string
		m    *MacLoop
		want string // the reason power, lock and focus are unknown
	}{
		{name: "an agent run without a MacLoop says power, locks and focus go unseen, and why", m: nil, want: noMacLoop},
		{name: "before the Loop has registered its sources they are unknown, as starting", m: NewMacLoop(), want: "the macOS notification loop hasn't started yet"},
	}
	if !onMac() {
		// Only off macOS does Run fail at once. On a Mac it registers
		// IOKit's power source, which works off the main thread too, and
		// serves the run loop until its context is done.
		failed := NewMacLoop()
		if err := failed.Run(context.Background()); err == nil {
			t.Fatal("MacLoop.Run worked off macOS")
		}
		tests = append(tests, struct {
			name string
			m    *MacLoop
			want string
		}{name: "a Loop that can't run says so", m: failed, want: "the macOS notification loop runs on macOS only"})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.m.signals()
			if got.Idle.Via != macIdleVia {
				t.Errorf("idle %+v, want via %q: darwin.Presence needs no loop", got.Idle, macIdleVia)
			}
			for name, sig := range map[string]signalReply{"power": got.Power, "lock": got.Lock, "focus": got.Focus} {
				if sig.Unknown != tt.want || sig.Via != "" {
					t.Errorf("%s %+v, want unknown %q", name, sig, tt.want)
				}
			}
			if got.Loop != nil {
				t.Errorf("loop %+v, want none before the Loop runs", *got.Loop)
			}
		})
	}
}

func onMac() bool { return runtime.GOOS == platform.Darwin }

// TestMacHandler checks that the Loop's handler gives presence the power
// events and focus the activations, the way openOS wires them on macOS.
func TestMacHandler(t *testing.T) {
	m := NewMacLoop()
	h := macHandler{m}
	var seals []event.PowerOp
	s := presence.New(presence.Options{
		Sched: sched.New(),
		OS:    presence.MacOS(func() (event.OSPresence, error) { return event.OSPresence{IdleAnyS: 1}, nil }, &m.power),
		Seal:  func(op event.PowerOp) { seals = append(seals, op) },
	})
	s.Start()
	defer s.Stop()
	var changes []focus.Change
	stop, _ := focus.MacOS(&m.focus, nil).Watch(func(c focus.Change) { changes = append(changes, c) })
	defer stop()

	tests := []struct {
		name  string
		do    func()
		seals []event.PowerOp
		pids  []int
	}{
		{name: "a suspend reaches presence, which seals before the handler returns", do: func() { h.Power(time.Now(), event.PowerSuspend) }, seals: []event.PowerOp{event.PowerSuspend}},
		{name: "a lock reaches presence too", do: func() { h.Power(time.Now(), event.PowerLock) }, seals: []event.PowerOp{event.PowerSuspend, event.PowerLock}},
		{name: "an activation reaches focus with the app's process", do: func() { h.Focus(time.Now(), darwin.App{PID: 4242}) }, seals: []event.PowerOp{event.PowerSuspend, event.PowerLock}, pids: []int{4242}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.do()
			if !slices.Equal(seals, tt.seals) {
				t.Errorf("seals %v, want %v", seals, tt.seals)
			}
			var pids []int
			for _, c := range changes {
				pids = append(pids, c.PID)
			}
			if !slices.Equal(pids, tt.pids) {
				t.Errorf("focus changes for processes %v, want %v", pids, tt.pids)
			}
		})
	}
}

// fakeNotifyLoop stands in for a MacLoop's darwin.Loop, whose Status comes
// from macOS: its Run closes Ready, as the Loop does once its sources are
// registered, then returns err at once, or runs until ctx ends when err is
// nil. Status is what it registered.
type fakeNotifyLoop struct {
	st    darwin.Status
	err   error
	ready chan struct{}
}

func (l *fakeNotifyLoop) Run(ctx context.Context) error {
	close(l.ready)
	if l.err != nil {
		return l.err
	}
	<-ctx.Done()
	return nil
}

func (l *fakeNotifyLoop) Ready() <-chan struct{} { return l.ready }
func (l *fakeNotifyLoop) Status() darwin.Status  { return l.st }

// TestMacLoopFocusUnknown checks that MacFocus, whose Watch always starts,
// hears when no activation will come: a MacLoop whose activation observer
// didn't register, or whose Loop didn't run, makes focus unknown, and the
// focus component records one os.focus with app_class unknown (SM 2.5).
func TestMacLoopFocusUnknown(t *testing.T) {
	registered := darwin.Status{MainThread: true}
	noObserver := darwin.Status{MainThread: true, Focus: errors.New("NSWorkspace has no notification center")}
	cases := []struct {
		name     string
		loop     *fakeNotifyLoop
		activate bool // an app comes to the front while the Loop runs
		want     event.AppClass
		unknown  string // what the focus component's Status says after
	}{
		{name: "a Loop whose activation observer didn't register gives one os.focus unknown, though the Loop runs on for power and locks",
			loop: &fakeNotifyLoop{st: noObserver}, want: event.AppUnknown, unknown: "NSWorkspace has no notification center"},
		{name: "a Loop whose Run fails gives one os.focus unknown, with the reason GET /v1/status gives",
			loop: &fakeNotifyLoop{err: errors.New("darwin: no notification source works")}, want: event.AppUnknown,
			unknown: "the macOS notification loop didn't start: darwin: no notification source works"},
		{name: "off macOS, where Run fails at once, focus is unknown too",
			loop: &fakeNotifyLoop{err: errors.ErrUnsupported}, want: event.AppUnknown, unknown: "the macOS notification loop runs on macOS only"},
		{name: "a Loop whose sources all registered says nothing of unknown focus, and its activations make the records",
			loop: &fakeNotifyLoop{st: registered}, activate: true, want: event.AppTerminal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.loop.ready = make(chan struct{})
			m := &MacLoop{loop: tc.loop}
			now := time.Now()
			read := func() ([]platform.Proc, error) {
				return []platform.Proc{{PID: 4242, PPID: 1, Name: "zsh", Start: now.Add(-time.Hour), Owner: "uid:501"}}, nil
			}
			var mu sync.Mutex
			var recs []focus.Record
			w := focus.New(focus.Options{OS: focus.MacOS(&m.focus, read), Record: func(r focus.Record) {
				mu.Lock()
				defer mu.Unlock()
				recs = append(recs, r)
			}})
			w.Start()
			defer w.Stop()
			ctx, cancel := context.WithCancel(context.Background())
			ran := make(chan error, 1)
			go func() { ran <- m.Run(ctx) }()
			<-tc.loop.ready
			if tc.activate {
				macHandler{m}.Focus(now, darwin.App{PID: 4242})
			}
			if tc.loop.err == nil {
				// Run is serving the Loop; the observer's report comes after
				// Ready, so wait for the record before stopping it. In the
				// agent the Loop runs until the agent stops, and by then the
				// focus component has stopped too, so a record that waited
				// for Run to return would never be written.
				deadline := time.Now().Add(10 * time.Second)
				for w.Status().Records == 0 && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if w.Status().Records == 0 {
					cancel()
					<-ran
					t.Fatal("no os.focus while the Loop runs; it must not wait for Run to return")
				}
			}
			cancel()
			if err := <-ran; (err != nil) != (tc.loop.err != nil) {
				t.Fatalf("Run returned %v, want %v", err, tc.loop.err)
			}
			// Every change has been delivered by now; wait for the worker.
			deadline := time.Now().Add(10 * time.Second)
			for st := w.Status(); st.Records < 1 && time.Now().Before(deadline); st = w.Status() {
				time.Sleep(time.Millisecond)
			}
			st := w.Status()
			mu.Lock()
			defer mu.Unlock()
			if len(recs) != 1 || recs[0].Data.AppClass != tc.want || st.Changes != 1 {
				var got []event.AppClass
				for _, r := range recs {
					got = append(got, r.Data.AppClass)
				}
				t.Fatalf("records %v from %d changes, want one %s", got, st.Changes, tc.want)
			}
			if err := recs[0].Data.Validate(); err != nil {
				t.Errorf("the record doesn't validate: %v", err)
			}
			if st.Unknown != tc.unknown {
				t.Errorf("Status.Unknown %q, want %q", st.Unknown, tc.unknown)
			}
		})
	}
}

// TestWatchedSignals checks that GET /v1/status names where suspends and
// locks come from only while the sampler watches them, on every OS: the
// OS layer can know logind's or IOKit's source while the agent hears none
// of its events.
func TestWatchedSignals(t *testing.T) {
	watch := func(func(event.PowerOp)) (func(), error) { return func() {}, nil }
	idle := func() (presence.Idle, error) { return presence.Idle{}, nil }
	named := signalsReply{Idle: via("idle source"), Focus: via("focus source"), Power: via("power source"), Lock: via("lock source")}
	tests := []struct {
		name string
		// presence is the sampler's OS, nil for an agent with no sampler.
		presence  *presence.OS
		start     bool
		in        signalsReply
		wantPower signalReply
		wantLock  signalReply
	}{
		{
			name:      "a source the sampler watches stays named",
			presence:  &presence.OS{Idle: idle, WatchSuspend: watch, WatchLock: watch},
			start:     true,
			in:        named,
			wantPower: via("power source"),
			wantLock:  via("lock source"),
		},
		{
			name:      "on a desktop with no idle time power and lock stay named, since the sampler still watches them to seal each suspend and lock (DESIGN 3.3, 3.6)",
			presence:  &presence.OS{WatchSuspend: watch, WatchLock: watch},
			start:     true,
			in:        named,
			wantPower: via("power source"),
			wantLock:  via("lock source"),
		},
		{
			name:      "a source the sampler has no watch on is unknown, though the OS names it",
			presence:  &presence.OS{Idle: idle},
			start:     true,
			in:        named,
			wantPower: unknown(noSuspendWatch),
			wantLock:  unknown(noLockWatch),
		},
		{
			name:      "before the sampler starts nothing is watched, so power and lock are unknown",
			presence:  &presence.OS{Idle: idle, WatchSuspend: watch, WatchLock: watch},
			in:        named,
			wantPower: unknown(noSuspendWatch),
			wantLock:  unknown(noLockWatch),
		},
		{
			name:      "an agent without a sampler hears no suspend or lock",
			in:        named,
			wantPower: unknown(noSuspendWatch),
			wantLock:  unknown(noLockWatch),
		},
		{
			name:      "a signal the OS already calls unknown keeps its own reason",
			presence:  &presence.OS{Idle: idle},
			start:     true,
			in:        signalsReply{Idle: via("idle source"), Focus: via("focus source"), Power: unknown("no logind"), Lock: unknown("no session")},
			wantPower: unknown("no logind"),
			wantLock:  unknown("no session"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &agent{sched: sched.New()}
			if tt.presence != nil {
				a.presence = presence.New(presence.Options{Sched: a.sched, OS: *tt.presence})
				if tt.start {
					a.presence.Start()
				}
				defer a.presence.Stop()
			}
			got := a.watched(tt.in)
			if got.Power != tt.wantPower || got.Lock != tt.wantLock {
				t.Errorf("power %+v, lock %+v; want %+v, %+v", got.Power, got.Lock, tt.wantPower, tt.wantLock)
			}
			if got.Idle != tt.in.Idle || got.Focus != tt.in.Focus {
				t.Errorf("idle %+v, focus %+v; want them as the OS gave them, %+v and %+v", got.Idle, got.Focus, tt.in.Idle, tt.in.Focus)
			}
		})
	}
}

// TestComponentSignals checks the signals of an OS whose sources keep no
// status of their own, as on Windows: a power watch that runs is named,
// one that doesn't is unknown, and focus says whether its source runs.
func TestComponentSignals(t *testing.T) {
	watch := func(err error) func(func(event.PowerOp)) (func(), error) {
		return func(func(event.PowerOp)) (func(), error) {
			if err != nil {
				return nil, err
			}
			return func() {}, nil
		}
	}
	tests := []struct {
		name      string
		presence  presence.OS
		focus     focus.OS
		wantPower signalReply
		wantLock  signalReply
		wantFocus signalReply
	}{
		{
			name: "watches and a focus source that run are named by their sources",
			presence: presence.OS{
				Idle:         func() (presence.Idle, error) { return presence.Idle{}, nil },
				WatchSuspend: watch(nil),
				WatchLock:    watch(nil),
			},
			focus:     focus.OS{Via: "SetWinEventHook(EVENT_SYSTEM_FOREGROUND)", Watch: func(func(focus.Change)) (func(), error) { return func() {}, nil }, Read: func() ([]platform.Proc, error) { return nil, nil }},
			wantPower: via("power source"),
			wantLock:  via("lock source"),
			wantFocus: via("SetWinEventHook(EVENT_SYSTEM_FOREGROUND)"),
		},
		{
			name: "a watch that failed to start, and a focus source that didn't, are unknown with the reason",
			presence: presence.OS{
				Idle:         func() (presence.Idle, error) { return presence.Idle{}, nil },
				WatchSuspend: watch(nil),
				WatchLock:    watch(errors.New("WTSRegisterSessionNotification: access denied")),
			},
			focus:     focus.OS{Via: "hook", Watch: func(func(focus.Change)) (func(), error) { return nil, errors.New("no desktop") }, Read: func() ([]platform.Proc, error) { return nil, nil }},
			wantPower: via("power source"),
			wantLock:  unknown("the lock and unlock watch isn't running; problems.presence says why"),
			wantFocus: unknown("the focus source didn't start: no desktop"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &agent{sched: sched.New()}
			a.presence = presence.New(presence.Options{Sched: a.sched, OS: tt.presence})
			a.presence.Start()
			defer a.presence.Stop()
			a.focus = focus.New(focus.Options{OS: tt.focus})
			a.focus.Start()
			defer a.focus.Stop()
			got := a.componentSignals("idle source", "power source", "lock source")
			if got.Idle != via("idle source") || got.Power != tt.wantPower || got.Lock != tt.wantLock || got.Focus != tt.wantFocus {
				t.Errorf("signals %+v, want idle via the idle source, power %+v, lock %+v, focus %+v", got, tt.wantPower, tt.wantLock, tt.wantFocus)
			}
		})
	}
}
