// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
)

// runEnv, when set, makes the test binary run `pairtime` with these
// space-separated arguments instead of its tests, so a test can run the
// real command in a process of its own.
const runEnv = "PAIRTIME_TEST_RUN"

func TestMain(m *testing.M) {
	if args := os.Getenv(runEnv); args != "" {
		os.Exit(run(strings.Fields(args), os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

// startPairtime starts `pairtime <args>` as a separate process with its
// own PAIRTIME_HOME.
func startPairtime(t *testing.T, ptHome, args string, out io.Writer) *exec.Cmd {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^$")
	cmd.Env = append(os.Environ(), runEnv+"="+args, home.EnvHome+"="+ptHome)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return cmd
}

// TestAgentProcess runs two real agents on one data directory, the way a
// login item and a hook can race to start one.
func TestAgentProcess(t *testing.T) {
	ptHome := t.TempDir()
	var firstOut bytes.Buffer
	first := startPairtime(t, ptHome, "agent", &firstOut)
	// Registered after t.TempDir's cleanup, so it runs first: Windows can't
	// delete the directory while the agent has files in it open.
	t.Cleanup(func() {
		first.Process.Kill()
		first.Wait()
		if t.Failed() {
			t.Logf("the first agent's output:\n%s", firstOut.String())
		}
	})
	portFile := filepath.Join(ptHome, "port.json")
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(portFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the first agent wrote no port.json within 30 s")
		}
		time.Sleep(50 * time.Millisecond)
	}

	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{
			name: "the agent keeps its files in PAIRTIME_HOME",
			check: func(t *testing.T) {
				for _, f := range []string{"port.json", "state.json", "config.toml"} {
					if _, err := os.Stat(filepath.Join(ptHome, f)); err != nil {
						t.Errorf("%s isn't in PAIRTIME_HOME: %v", f, err)
					}
				}
			},
		},
		{
			name: "a second agent on the same data directory exits at once with status 3",
			check: func(t *testing.T) {
				var out bytes.Buffer
				second := startPairtime(t, ptHome, "agent", &out)
				exited := make(chan error, 1)
				go func() { exited <- second.Wait() }()
				select {
				case err := <-exited:
					var ee *exec.ExitError
					if !errors.As(err, &ee) || ee.ExitCode() != exitAlreadyRunning {
						t.Fatalf("the second agent ended with %v, want exit status %d; output:\n%s", err, exitAlreadyRunning, out.String())
					}
					if !strings.Contains(out.String(), "already running") {
						t.Errorf("the second agent said %q, want it to say it is already running", out.String())
					}
				case <-time.After(30 * time.Second):
					second.Process.Kill()
					<-exited
					t.Fatalf("the second agent was still running after 30 s; output:\n%s", out.String())
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.check)
	}
}

func TestRegister(t *testing.T) {
	ok := func([]string, io.Writer, io.Writer) int { return 0 }
	tests := []struct {
		name      string
		c         command
		wantPanic bool
	}{
		{name: "a new lowercase name registers", c: command{name: "test-cmd2", summary: "s", run: ok}},
		{name: "a name already taken panics", c: command{name: "agent", summary: "s", run: ok}, wantPanic: true},
		{name: "a built-in name panics", c: command{name: "help", summary: "s", run: ok}, wantPanic: true},
		{name: "an uppercase name panics", c: command{name: "Agent", summary: "s", run: ok}, wantPanic: true},
		{name: "a name with a space panics", c: command{name: "fixtures scrub", summary: "s", run: ok}, wantPanic: true},
		{name: "a command without a summary panics", c: command{name: "nosummary", run: ok}, wantPanic: true},
		{name: "a command without a run function panics", c: command{name: "norun", summary: "s"}, wantPanic: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				delete(commands, "test-cmd2")
				if got := recover() != nil; got != tt.wantPanic {
					t.Errorf("register panicked: %v, want %v", got, tt.wantPanic)
				}
			}()
			register(tt.c)
		})
	}
}
