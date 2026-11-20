// SPDX-License-Identifier: AGPL-3.0-or-later

package home

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"testing"
	"time"
)

// holderEnv, set to a directory, turns the test binary into a lock holder:
// it takes the lock on that directory, prints "locked", and holds the lock
// until its stdin closes or it is killed.
const holderEnv = "PAIRTIME_TEST_LOCK_HOLDER"

// holderReportEnv, set to a file name beside holderEnv, is for a holder
// that no test can reach over stdin and stdout, such as one a scheduled
// task starts. It writes "locked <pid>" to that file instead, and holds
// the lock until the file is deleted, for two minutes at most.
const holderReportEnv = "PAIRTIME_TEST_LOCK_REPORT"

func TestMain(m *testing.M) {
	if dir := os.Getenv(holderEnv); dir != "" {
		os.Exit(holdLock(dir, os.Getenv(holderReportEnv)))
	}
	os.Exit(m.Run())
}

// holdLock is the lock holder's main (see holderEnv and holderReportEnv).
func holdLock(dir, report string) int {
	l, err := Acquire(dir)
	if err != nil {
		if report != "" {
			WriteFile(report, []byte("error: "+err.Error()+"\n"))
		}
		fmt.Println("error:", err)
		return 1
	}
	defer l.Release()
	if report == "" {
		fmt.Println("locked")
		io.Copy(io.Discard, os.Stdin)
		return 0
	}
	if err := WriteFile(report, fmt.Appendf(nil, "locked %d\n", os.Getpid())); err != nil {
		fmt.Println("error:", err)
		return 1
	}
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(report); errors.Is(err, fs.ErrNotExist) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return 0
}

func TestLock(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, dir string)
	}{
		{
			name: "a second Acquire of a held lock fails with ErrLocked",
			run: func(t *testing.T, dir string) {
				l := mustAcquire(t, dir)
				defer l.Release()
				if _, err := Acquire(dir); !errors.Is(err, ErrLocked) {
					t.Fatalf("second Acquire: err = %v, want ErrLocked", err)
				}
			},
		},
		{
			name: "Release lets the next Acquire take the lock",
			run: func(t *testing.T, dir string) {
				l := mustAcquire(t, dir)
				if err := l.Release(); err != nil {
					t.Fatal(err)
				}
				mustAcquire(t, dir).Release()
			},
		},
		{
			name: "locks on different data directories are independent",
			run: func(t *testing.T, dir string) {
				l := mustAcquire(t, dir)
				defer l.Release()
				mustAcquire(t, t.TempDir()).Release()
			},
		},
		{
			name: "a lock held by another process makes Acquire fail with ErrLocked",
			run: func(t *testing.T, dir string) {
				h := startHolder(t, dir)
				defer h.stop()
				if _, err := Acquire(dir); !errors.Is(err, ErrLocked) {
					t.Fatalf("Acquire while another process holds the lock: err = %v, want ErrLocked", err)
				}
			},
		},
		{
			name: "the OS drops the lock when its holder is killed",
			run: func(t *testing.T, dir string) {
				h := startHolder(t, dir)
				if err := h.cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				h.cmd.Wait() // the exit status says "killed"; only the exit matters
				// The kernel closes a dead process's handles as it exits;
				// allow it a moment.
				deadline := time.Now().Add(5 * time.Second)
				for {
					l, err := Acquire(dir)
					if err == nil {
						l.Release()
						return
					}
					if !errors.Is(err, ErrLocked) || time.Now().After(deadline) {
						t.Fatalf("Acquire after the holder was killed: %v", err)
					}
					time.Sleep(20 * time.Millisecond)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.run(t, t.TempDir())
		})
	}
}

func mustAcquire(t *testing.T, dir string) *Lock {
	t.Helper()
	l, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire(%s): %v", dir, err)
	}
	return l
}

// holder is a test binary running as a lock holder (see holderEnv).
type holder struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
}

func startHolder(t *testing.T, dir string) *holder {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^$")
	cmd.Env = append(os.Environ(), holderEnv+"="+dir)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "locked\n" {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatalf("the lock holder said %q (%v), want \"locked\"", line, err)
	}
	return &holder{cmd: cmd, stdin: stdin}
}

// stop closes the holder's stdin, so it releases the lock and exits.
func (h *holder) stop() {
	h.stdin.Close()
	h.cmd.Wait()
}
