// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
)

// runEnv, when set, makes the test binary run `pairtime` with these
// space-separated arguments instead of its tests, so a test can run the
// real command in a process of its own.
const runEnv = "PAIRTIME_TEST_RUN"

// ownArgs as runEnv's value makes the test binary run `pairtime` with its
// own command-line arguments, as the real binary does. A process it starts
// in turn, as `agent --detach` starts `agent`, inherits the setting and
// runs pairtime too.
const ownArgs = "@args"

// helpers are other jobs a test can give the test binary in a process of
// its own: when the environment variable a helper is filed under is set,
// the binary runs the helper with its value and exits with what it
// returns.
var helpers = map[string]func(value string) int{}

func TestMain(m *testing.M) {
	for env, helper := range helpers {
		if v := os.Getenv(env); v != "" {
			os.Exit(helper(v))
		}
	}
	switch args := os.Getenv(runEnv); args {
	case "":
	case ownArgs:
		os.Exit(start(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
	default:
		os.Exit(run(strings.Fields(args), os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

// waitForAgent waits up to 30 s for the agent using the data directory
// ptHome to write its port.json.
func waitForAgent(t *testing.T, ptHome string) {
	t.Helper()
	portFile := filepath.Join(ptHome, "port.json")
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(portFile); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the agent wrote no port.json within 30 s")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// startedPid returns the process ID that `agent --detach` printed.
func startedPid(t *testing.T, out string) int {
	t.Helper()
	const said = "started the agent, process "
	i := strings.Index(out, said)
	if i < 0 {
		t.Fatalf("agent --detach printed %q, which names no process", out)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(out[i+len(said):]))
	if err != nil {
		t.Fatalf("agent --detach printed %q: %v", out, err)
	}
	return pid
}

// stopAtCleanup kills the process pid when the test ends. Call it after
// t.TempDir, so it runs before the temporary directories go: Windows can't
// delete a directory while the agent has files in it open.
func stopAtCleanup(t *testing.T, pid int) {
	t.Helper()
	p, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("finding the agent, process %d: %v", pid, err)
	}
	t.Cleanup(func() {
		p.Kill()
		// On Windows this waits for the process to end. On Unix a process
		// that isn't this one's child can't be waited for, and that's fine.
		p.Wait()
	})
}

// waitWithin waits up to d for cmd to exit.
func waitWithin(t *testing.T, cmd *exec.Cmd, d time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		cmd.Process.Kill()
		<-done
		t.Fatalf("%s was still running after %v", cmd, d)
		return nil
	}
}

func TestAgentDetach(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"agent --detach starts the agent in a process of its own and returns at once", []string{"agent", "--detach"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ptHome := t.TempDir()
			cmd := exec.Command(exe, tt.args...)
			cmd.Env = append(os.Environ(), runEnv+"="+ownArgs, home.EnvHome+"="+ptHome)
			var out bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &out
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			if err := waitWithin(t, cmd, 30*time.Second); err != nil {
				t.Fatalf("agent --detach ended with %v; output:\n%s", err, out.String())
			}
			pid := startedPid(t, out.String())
			stopAtCleanup(t, pid)
			if pid == cmd.Process.Pid {
				t.Fatalf("agent --detach says it is the agent itself, process %d", pid)
			}
			waitForAgent(t, ptHome)
		})
	}
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
	waitForAgent(t, ptHome)

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
