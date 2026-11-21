// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux && desktop

package main

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/procs"
	"github.com/some-du6e/pairtime/pkg/event"
)

// startWindow starts an X client in the test's display, with env added to
// its environment, and returns its window, which xdotool search finds by
// the match it is given, such as "--class" and the class the client was
// started with.
func startWindow(t *testing.T, match, env []string, args ...string) string {
	t.Helper()
	if _, err := exec.LookPath(args[0]); err != nil {
		t.Fatalf("%s isn't installed: platform.yml installs it with xterm", args[0])
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = append(os.Environ(), env...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
	search := append([]string{"30", "xdotool", "search", "--sync", "--limit", "1"}, match...)
	out, err := exec.Command("timeout", search...).Output()
	if err != nil {
		t.Fatalf("finding the %s window (xdotool search %s): %v", args[0], strings.Join(match, " "), err)
	}
	return strings.TrimSpace(string(out))
}

// xdotool runs xdotool with args.
func xdotool(t *testing.T, args ...string) {
	t.Helper()
	if out, err := exec.Command("xdotool", args...).CombinedOutput(); err != nil {
		t.Fatalf("xdotool %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// TestDesktopAgentOnX11 runs the real `pairtime agent` under Xvfb and
// openbox (tools/ci/desktop_session.sh) and checks WP-OS9's Linux wiring
// from the ledger it writes: the X server's idle counter and the window
// manager's _NET_ACTIVE_WINDOW reach os.presence and os.focus, and the
// window's process, read from the X server and joined to /proc, hosts the
// harness session running in it (SM 3.2). Focus on an xterm, and the fake
// claude, make the project context live (DESIGN 3.3).
//
// One xterm runs the fake claude as its program, as a person runs claude
// in a terminal; the other runs nothing pairtime knows. Two xterms that
// both hosted nothing would make one record, not two, since the focus
// component writes a record only when it says something new (WP-OS2).
func TestDesktopAgentOnX11(t *testing.T) {
	fake := fakeHarness(t)
	claude := startWindow(t, []string{"--class", "PairtimeClaude"}, []string{sleepEnv + "=10m"}, "xterm", "-class", "PairtimeClaude", "-e", fake)
	claudeAt := time.Now() // the fake claude started by now, with its window
	plain := startWindow(t, []string{"--class", "PairtimePlain"}, nil, "xterm", "-class", "PairtimePlain", "-e", "sleep", "600")
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
	st := waitStatus(t, ptHome, 30*time.Second, "found its X11 sources", func(s agentStatus) bool {
		return s.Signals.Idle.Via != "" && s.Signals.Focus.Via != ""
	})

	// Each switch waits for the window manager, and then a moment for
	// the agent, which reads _NET_ACTIVE_WINDOW 50 ms after it changes.
	activate := func(w string) {
		xdotool(t, "windowactivate", "--sync", w)
		time.Sleep(500 * time.Millisecond)
	}
	// The process scan reports a harness process only once it has run
	// for procs.Grace, so the scan a terminal's focus asks for as the
	// agent starts, and a minute scan that comes soon after, can find the
	// fake claude too young. A scan after that tells the agent its
	// session. Focus on its xterm asks for one (procs.kick), but the
	// status reply counts only the minute scan, procs.scan, so the test
	// waits for the next of those, at most procs.Interval away.
	time.Sleep(time.Until(claudeAt.Add(procs.Grace + 2*time.Second)))
	scans := readStatus(t, ptHome).runs("procs.scan")
	activate(claude)
	waitStatus(t, ptHome, 90*time.Second, "scanned the processes again", func(s agentStatus) bool { return s.runs("procs.scan") > scans })
	// Two seconds without input, then a key, then focus moves at once:
	// the sample presence takes for the switch is the first after the
	// key, so it has to show the key.
	time.Sleep(2 * time.Second)
	xdotool(t, "key", "shift")
	keyAt := time.Now()
	activate(plain)
	time.Sleep(time.Second)
	stopAgent(t, ptHome, cmd)
	focus := recorded(t, ptHome, event.KindOSFocus)
	samples := recorded(t, ptHome, event.KindOSPresence)

	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{
			name: "the status reply names the X server's idle counter and _NET_ACTIVE_WINDOW as the sources, for doctor (D-design-5.9)",
			check: func(t *testing.T) {
				if st.Signals.Session != "x11" || !strings.Contains(st.Signals.Idle.Via, "MIT-SCREEN-SAVER") || !strings.Contains(st.Signals.Focus.Via, "_NET_ACTIVE_WINDOW") {
					t.Errorf("signals = %+v", st.Signals)
				}
			},
		},
		{
			name: "xdotool windowactivate between two xterms gives two os.focus records, the first hosting the fake claude's session and the second none",
			check: func(t *testing.T) {
				var got []string
				for _, ev := range focus {
					f := ev.Data.(*event.OSFocus)
					got = append(got, fmt.Sprintf("%s hosting %d", f.AppClass, len(f.HostedSessions)))
				}
				want := []string{"terminal hosting 1", "terminal hosting 0"}
				if len(got) < len(want) || !slices.Equal(got[len(got)-len(want):], want) {
					t.Errorf("os.focus records %q, want them to end with %q", got, want)
				}
			},
		},
		{
			name: "the first os.presence after xdotool key input has idle_any_s under 1 s, and none has idle_keys_s",
			check: func(t *testing.T) {
				var first *event.OSPresence
				for _, ev := range samples {
					p := ev.Data.(*event.OSPresence)
					if p.IdleKeysS != nil {
						t.Errorf("sample at %d has idle_keys_s %v; Linux has none without a key counter", ev.T, *p.IdleKeysS)
					}
					if first == nil && ev.T >= keyAt.UnixMilli() {
						first = p
					}
				}
				switch {
				case first == nil:
					t.Errorf("no os.presence after the key, of %d", len(samples))
				case first.IdleAnyS >= 1:
					t.Errorf("the first os.presence after the key has idle_any_s %v, want under 1", first.IdleAnyS)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.check)
	}
}
