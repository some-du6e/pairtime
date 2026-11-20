// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux && desktop

package linux

// These tests run in platform.yml's Linux job, inside Xvfb with openbox
// and a private session bus (tools/ci/desktop_session.sh). Their oracles
// are the X server's own idle counter, moved by real XTEST input from
// xdotool, the window manager's active window, as xdotool and xprop, two
// independent X clients, read it, and the pid of each xterm the test
// starts.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// openX11Desktop opens the test session's desktop, X11 only: the system
// bus of a CI runner isn't the test's to use.
func openX11Desktop(t *testing.T) *Desktop {
	t.Helper()
	if os.Getenv("DISPLAY") == "" {
		t.Fatal("DISPLAY is unset: run this under tools/ci/desktop_session.sh")
	}
	d := Open(context.Background(), Config{
		SystemBus: func(context.Context) (*dbus.Conn, error) { return nil, errors.New("not in this test") },
	})
	t.Cleanup(func() { d.Close() })
	s := d.Status()
	if s.Session != SessionX11 || s.Idle.Via != screenSaverExtension || s.Focus.Via != focusViaXRes {
		t.Fatalf("status %+v, want X11 with MIT-SCREEN-SAVER idle time, and focus from _NET_ACTIVE_WINDOW with X-Resource pids", s)
	}
	return d
}

func runTool(t *testing.T, name string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestDesktopX11IdleResetsOnInput(t *testing.T) {
	d := openX11Desktop(t)
	cases := []struct {
		name  string
		input []string // the xdotool command that makes input
	}{
		{"a key press resets the X server's idle counter", []string{"key", "shift"}},
		{"a pointer move resets the X server's idle counter", []string{"mousemove_relative", "--", "7", "3"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Let idle time build up past what the input will reset.
			var before Idle
			deadline := time.Now().Add(15 * time.Second)
			for {
				var err error
				if before, err = d.Idle(context.Background()); err != nil {
					t.Fatal(err)
				}
				if before.Any >= 1500*time.Millisecond {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("idle stayed at %v for 15 s with no input", before.Any)
				}
				time.Sleep(100 * time.Millisecond)
			}
			runTool(t, "xdotool", tc.input...)
			// Idle time only grows without input, so any drop is the
			// reset. The server may queue the XTEST event for a moment.
			var after Idle
			for range 20 {
				var err error
				if after, err = d.Idle(context.Background()); err != nil {
					t.Fatal(err)
				}
				if after.Any < time.Second && after.Any < before.Any {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if after.Any >= time.Second || after.Any >= before.Any {
				t.Fatalf("idle %v before the input and %v after; want it reset", before.Any, after.Any)
			}
			if err := after.Payload().Validate(); err != nil {
				t.Errorf("os.presence: %v", err)
			}
		})
	}
}

// startXterm starts an xterm with its own window class and returns its
// process and window.
func startXterm(t *testing.T, class string) (*exec.Cmd, uint32) {
	t.Helper()
	cmd := exec.Command("xterm", "-class", class, "-e", "sleep", "600")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
	out := runTool(t, "xdotool", "search", "--sync", "--limit", "1", "--class", class)
	wid, err := strconv.ParseUint(strings.Fields(out)[0], 10, 32)
	if err != nil {
		t.Fatalf("xdotool search said %q", out)
	}
	return cmd, uint32(wid)
}

// xpropPID is the window's _NET_WM_PID as xprop reads it, or 0.
func xpropPID(t *testing.T, wid uint32) int {
	out := runTool(t, "xprop", "-id", fmt.Sprint(wid), "_NET_WM_PID")
	_, value, ok := strings.Cut(out, "=")
	if !ok {
		return 0 // "_NET_WM_PID:  not found."
	}
	pid, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		t.Fatalf("xprop said %q", out)
	}
	return pid
}

// nextFocus waits for the next report of a focused window. openbox may
// report no window for a moment between two, which is skipped.
func nextFocus(t *testing.T, ch <-chan Focus) Focus {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case f, ok := <-ch:
			if !ok {
				t.Fatal("the focus channel closed")
			}
			if f.Window != 0 {
				return f
			}
		case <-timeout:
			t.Fatal("no focus report in 10 s")
		}
	}
}

func TestDesktopX11FocusFollowsActiveWindow(t *testing.T) {
	cmdA, winA := startXterm(t, "PairtimeFocusA")
	cmdB, winB := startXterm(t, "PairtimeFocusB")
	d := openX11Desktop(t)
	focus := d.Focus()

	// Start from A, whatever had focus before.
	runTool(t, "xdotool", "windowactivate", "--sync", fmt.Sprint(winA))
	for f := nextFocus(t, focus); f.Window != winA; f = nextFocus(t, focus) {
	}

	cases := []struct {
		name string
		win  uint32
		cmd  *exec.Cmd
	}{
		{"activating the second xterm reports its window and process", winB, cmdB},
		{"activating the first xterm again reports its window and process", winA, cmdA},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runTool(t, "xdotool", "windowactivate", "--sync", fmt.Sprint(tc.win))
			got := nextFocus(t, focus)
			active := runTool(t, "xdotool", "getactivewindow")
			if got.Window != tc.win || active != fmt.Sprint(tc.win) {
				t.Fatalf("reported window %d; xdotool activated %d and reads %s as active", got.Window, tc.win, active)
			}
			// xprop is the oracle for the pid, and xterm, which has set
			// _NET_WM_PID since 2005, backs it with its own.
			wantPID := xpropPID(t, tc.win)
			if wantPID != tc.cmd.Process.Pid {
				t.Fatalf("xprop reads _NET_WM_PID %d on the xterm, whose pid is %d", wantPID, tc.cmd.Process.Pid)
			}
			if got.PID != wantPID {
				t.Errorf("pid %d, xprop says %d", got.PID, wantPID)
			}
			t.Logf("window %d, pid %d (xterm %d)", got.Window, got.PID, tc.cmd.Process.Pid)
			if err := got.Payload(nil).Validate(); err != nil {
				t.Errorf("os.focus: %v", err)
			}
		})
	}

	// Two switches made exactly two reports: nothing else is queued.
	select {
	case f := <-focus:
		if f.Window != 0 {
			t.Errorf("an extra focus report: %+v", f)
		}
	case <-time.After(500 * time.Millisecond):
	}
}

func TestDesktopX11FocusPIDFromServer(t *testing.T) {
	cases := []struct {
		name   string
		netPID string // what the test sets the xterm's _NET_WM_PID to
	}{
		{"a window whose _NET_WM_PID names a sandbox's pid, as a Flatpak app's does, reports its process's pid on the host", "2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, win := startXterm(t, "PairtimeSandboxed")
			runTool(t, "xprop", "-id", fmt.Sprint(win), "-f", "_NET_WM_PID", "32c", "-set", "_NET_WM_PID", tc.netPID)
			if got := fmt.Sprint(xpropPID(t, win)); got != tc.netPID {
				t.Fatalf("xprop reads _NET_WM_PID %s after setting it to %s", got, tc.netPID)
			}
			d := openX11Desktop(t)
			focus := d.Focus()
			runTool(t, "xdotool", "windowactivate", "--sync", fmt.Sprint(win))
			f := nextFocus(t, focus)
			for f.Window != win {
				f = nextFocus(t, focus)
			}
			// The xterm's own pid is the oracle: the X server took it from
			// the xterm's socket when it connected.
			if f.PID != cmd.Process.Pid {
				t.Errorf("pid %d, want the xterm's %d rather than its _NET_WM_PID %s", f.PID, cmd.Process.Pid, tc.netPID)
			}
		})
	}
}
