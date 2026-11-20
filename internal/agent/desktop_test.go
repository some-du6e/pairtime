// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/focus"
	"github.com/some-du6e/pairtime/internal/agent/presence"
	"github.com/some-du6e/pairtime/internal/agent/sched"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/darwin"
	"github.com/some-du6e/pairtime/pkg/event"
)

// TestMacLoopSignals checks what GET /v1/status says about a MacLoop that
// can't report the Loop's own Status: one the agent runs without, one
// that hasn't started, and one whose Run failed. The Loop's Status itself
// comes from macOS, so the macOS job's TestDesktopAgentOnMac checks it.
func TestMacLoopSignals(t *testing.T) {
	failed := NewMacLoop()
	if err := failed.Run(context.Background()); err == nil && !onMac() {
		t.Fatal("MacLoop.Run worked off macOS")
	}
	tests := []struct {
		name string
		m    *MacLoop
		want string // the reason power, lock and focus are unknown
	}{
		{name: "an agent run without a MacLoop says power, locks and focus go unseen, and why", m: nil, want: noMacLoop},
		{name: "before the Loop has registered its sources they are unknown, as starting", m: NewMacLoop(), want: "the macOS notification loop hasn't started yet"},
	}
	if !onMac() {
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
