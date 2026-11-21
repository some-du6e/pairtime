// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build darwin

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
)

// TestAgentOnMac runs the real `pairtime agent` on macos-latest with a data
// directory of its own and checks WP-OS9's wiring from outside: the
// notification loop runs on the process's main thread with every source
// registered, and once a fake harness process makes a project context live
// the agent records os.presence with the key-down idle time macOS gives.
// Nothing here moves focus, sleeps the runner or locks its screen (JE
// must-fix 11).
func TestAgentOnMac(t *testing.T) {
	if testing.Short() {
		t.Skip("the agent takes up to a minute to see the fake harness")
	}
	startFakeHarness(t)
	ptHome := t.TempDir()
	var out syncBuffer
	cmd := startPairtime(t, ptHome, "agent", &out)
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
		if t.Failed() {
			t.Logf("the agent's output:\n%s", out.String())
		}
	})
	waitForAgent(t, ptHome)
	st := waitStatus(t, ptHome, 30*time.Second, "started its notification loop", func(s runStatus) bool { return s.Signals.Loop != nil })
	// The process scan runs on the minute; after its first run the fake
	// claude has made a project context live, and presence has sampled.
	waitStatus(t, ptHome, 90*time.Second, "run its process scan", func(s runStatus) bool { return s.runs("procs.scan") > 0 })
	time.Sleep(time.Second)
	stopRunAgent(t, ptHome, cmd)
	samples := recorded(t, ptHome, event.KindOSPresence)

	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{
			name: "the notification loop runs on the process's main thread, where macOS delivers screen lock and app switch notifications",
			check: func(t *testing.T) {
				if !st.Signals.Loop.MainThread {
					t.Errorf("signals.loop = %+v, want main_thread true", *st.Signals.Loop)
				}
			},
		},
		{
			name: "every notification source registered, so power, lock and focus each name their source",
			check: func(t *testing.T) {
				for name, sig := range map[string]signalStatus{"power": st.Signals.Power, "lock": st.Signals.Lock, "focus": st.Signals.Focus} {
					if sig.Via == "" || sig.Unknown != "" {
						t.Errorf("signals.%s = %+v, want a source", name, sig)
					}
				}
			},
		},
		{
			name: "idle time comes from CoreGraphics, and the reply doesn't claim its key-downs are the person's alone (D-macos-hid-posted)",
			check: func(t *testing.T) {
				if !strings.Contains(st.Signals.Idle.Via, "CGEventSourceSecondsSinceLastEventType") || !strings.Contains(st.Signals.Idle.Via, "other processes post") {
					t.Errorf("signals.idle = %+v", st.Signals.Idle)
				}
			},
		},
		{
			name: "presence and focus report no problems",
			check: func(t *testing.T) {
				for _, source := range []string{"presence", "focus"} {
					if msg := st.Problems[source]; msg != "" {
						t.Errorf("problems.%s = %q", source, msg)
					}
				}
			},
		},
		{
			name: "once the fake harness makes a project context live, the agent records os.presence with a non-null idle_keys_s",
			check: func(t *testing.T) {
				keys := 0
				for _, ev := range samples {
					p := ev.Data.(*event.OSPresence)
					if err := p.Validate(); err != nil {
						t.Errorf("sample at %d: %v", ev.T, err)
					}
					if p.IdleKeysS != nil {
						keys++
					}
				}
				if len(samples) == 0 || keys == 0 {
					t.Errorf("%d os.presence records, %d with idle_keys_s; want at least one with it", len(samples), keys)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.check)
	}
}
