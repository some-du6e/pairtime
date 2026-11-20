// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/install"
)

// consoleOfEnv, set to a process ID, makes the test binary report on that
// process's console window instead of running its tests (consoleOf).
const consoleOfEnv = "PAIRTIME_TEST_CONSOLE_OF"

func init() { helpers[consoleOfEnv] = consoleOf }

// What consoleOf finds, as its exit status.
const (
	noConsole     = 10 // the process has no console
	noWindow      = 11 // its console has no window
	hiddenWindow  = 12 // its console has a window that isn't visible
	visibleWindow = 13 // its console has a visible window
)

var consoleNames = map[int]string{
	noConsole:     "no console",
	noWindow:      "a console with no window",
	hiddenWindow:  "a console whose window is hidden",
	visibleWindow: "a console with a visible window",
}

// consoleOf attaches to the console of the process whose ID is pid, which
// a process can do only while it has no console of its own, and reports
// whether that console has a window on the screen.
func consoleOf(pid string) int {
	n, err := strconv.ParseUint(pid, 10, 32)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	kernel32.NewProc("FreeConsole").Call()
	if ok, _, err := kernel32.NewProc("AttachConsole").Call(uintptr(n)); ok == 0 {
		if errors.Is(err, windows.ERROR_INVALID_HANDLE) {
			return noConsole
		}
		fmt.Fprintf(os.Stderr, "AttachConsole(%d): %v\n", n, err)
		return 1
	}
	hwnd, _, _ := kernel32.NewProc("GetConsoleWindow").Call()
	switch {
	case hwnd == 0:
		return noWindow
	case !windows.IsWindowVisible(windows.HWND(hwnd)):
		return hiddenWindow
	default:
		return visibleWindow
	}
}

// consoleWindowOf runs consoleOf for the process pid in a new process that
// has no console, and returns what it found.
func consoleWindowOf(t *testing.T, exe string, pid int) int {
	t.Helper()
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), consoleOfEnv+"="+strconv.Itoa(pid))
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || consoleNames[ee.ExitCode()] == "" {
		t.Fatalf("checking the console of process %d: %v\n%s", pid, err, out.String())
	}
	return ee.ExitCode()
}

// TestRunValueStartsTheAgentWithoutAConsoleWindow runs the command the Run
// value holds the way Explorer runs it at login, from a process with no
// console to share, so Windows makes a new console for a console program
// (CREATE_NEW_CONSOLE). The agent it leaves running must have no console
// window: one would stay open, full of the agent's log, for as long as the
// agent runs. The command the Run value held before `agent --detach` shows
// that the check sees such a window.
func TestRunValueStartsTheAgentWithoutAConsoleWindow(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// install only writes entries for a binary called pairtime.exe, the
	// name uninstall knows them by, so the Run value is made for one, and
	// the test binary's path takes its place in the command.
	const stand = `C:\pairtime\bin\pairtime.exe`
	item, err := install.Autostart(install.Env{GOOS: "windows", Home: t.TempDir(), Exe: stand})
	if err != nil {
		t.Fatal(err)
	}
	value, err := item.Edits[0].Apply(install.Content{})
	if err != nil {
		t.Fatal(err)
	}
	cmdline := strings.Replace(string(value.Data), `"`+stand+`"`, `"`+exe+`"`, 1)
	if cmdline == string(value.Data) {
		t.Fatalf("the Run value %q doesn't start with the quoted binary", value.Data)
	}
	tests := []struct {
		name    string
		cmdline string
		// detaches is true when the command starts the agent in another
		// process and exits.
		detaches bool
		want     []int // what consoleOf may find for the agent
	}{
		{
			name:     "the Run value's command leaves the agent running without a console window",
			cmdline:  cmdline,
			detaches: true,
			want:     []int{noConsole, noWindow, hiddenWindow},
		},
		{
			name:    "the agent started without --detach, as the Run value first had it, has a console window the check sees",
			cmdline: `"` + exe + `" agent`,
			want:    []int{visibleWindow},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ptHome := t.TempDir()
			cmd := exec.Command(exe)
			cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: tt.cmdline, CreationFlags: windows.CREATE_NEW_CONSOLE}
			cmd.Env = append(os.Environ(), runEnv+"="+ownArgs, home.EnvHome+"="+ptHome)
			var out bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &out
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			pid := cmd.Process.Pid
			if tt.detaches {
				if err := waitWithin(t, cmd, 30*time.Second); err != nil {
					t.Fatalf("%s ended with %v; output:\n%s", tt.cmdline, err, out.String())
				}
				pid = startedPid(t, out.String())
				stopAtCleanup(t, pid)
			} else {
				t.Cleanup(func() {
					cmd.Process.Kill()
					cmd.Wait()
				})
			}
			waitForAgent(t, ptHome)
			if got := consoleWindowOf(t, exe, pid); !slices.Contains(tt.want, got) {
				t.Errorf("the agent, process %d, has %s", pid, consoleNames[got])
			}
		})
	}
}
