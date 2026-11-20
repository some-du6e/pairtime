// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package windows

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/some-du6e/pairtime/pkg/event"
)

// lockWatchesAloneEnv makes this test binary a process whose only GUI
// threads are lock watches' and those of the goroutines that start and
// stop them (see lockWatchesAlone and main_test.go). It holds how many
// watches to start in turn. TestLockWatchesInAProcessWithNoOtherGUIThread
// sets it.
const lockWatchesAloneEnv = "PAIRTIME_TEST_LOCK_WATCHES_ALONE"

// lockWatchesAlone starts and stops n lock watches in turn, as
// lockWatchOnThreadsThatEnd does. It returns the process's exit code: 0
// when every watch got its window.
func lockWatchesAlone(n string) int {
	count, err := strconv.Atoi(n)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	for i := range count {
		if err := lockWatchOnThreadsThatEnd(); err != nil {
			fmt.Fprintf(os.Stderr, "lock watch %d of %d: %v\n", i+1, count, err)
			return 1
		}
	}
	return 0
}

// lockWatchOnThreadsThatEnd starts a lock watch and stops it from a
// goroutine locked to its OS thread, which Go ends with the goroutine, as
// the window thread of a caller such as WP-OS2's focus hook would. Stopping
// a watch posts to its window, so that thread is a GUI thread too. It
// returns once that thread and the watch's own have ended, which is when
// Windows has done its cleanup for both.
func lockWatchOnThreadsThatEnd() error {
	type result struct {
		threads []windows.Handle
		err     error
	}
	done := make(chan result, 1)
	go func() {
		runtime.LockOSThread() // never unlocked
		var r result
		defer func() { done <- r }()
		self, err := windows.OpenThread(windows.SYNCHRONIZE, false, windows.GetCurrentThreadId())
		if err != nil {
			r.err = fmt.Errorf("OpenThread: %w", err)
			return
		}
		r.threads = append(r.threads, self)
		w, err := startLockWatch(func(event.PowerOp) {})
		if err != nil {
			r.err = err
			return
		}
		// The window belongs to the thread that made it: the watch's own.
		tid, err := windows.GetWindowThreadProcessId(windows.HWND(w.hwnd), nil)
		if err == nil {
			var watch windows.Handle
			if watch, err = windows.OpenThread(windows.SYNCHRONIZE, false, tid); err == nil {
				r.threads = append(r.threads, watch)
			}
		}
		w.stop()
		if err != nil {
			r.err = fmt.Errorf("finding the watch's thread: %w", err)
		}
	}()
	r := <-done
	for _, h := range r.threads {
		ev, err := windows.WaitForSingleObject(h, 10_000)
		windows.CloseHandle(h)
		switch {
		case err != nil:
			return fmt.Errorf("WaitForSingleObject: %w", err)
		case ev != windows.WAIT_OBJECT_0:
			return errors.New("a thread was still there 10 s after its goroutine ended")
		}
	}
	return r.err
}

// Windows destroys the window classes a process registered when the last
// of the process's GUI threads ends, and a lock watch's thread ends with the
// watch. In a process with no other GUI thread, a class registered only
// once made every watch after the first fail with "Cannot find window
// class", as #48 found for the balloon (5219295). This runs in a child
// process, because in this one other tests' threads have called user32,
// and Go keeps those threads.
func TestLockWatchesInAProcessWithNoOtherGUIThread(t *testing.T) {
	tests := []struct {
		name    string
		watches int
	}{
		{name: "the first lock watch gets its window", watches: 1},
		{name: "lock watches started and stopped one after another, each once every thread of the one before has ended, all get their windows", watches: 3},
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(exe, "-test.run=^$")
			cmd.Env = append(os.Environ(), lockWatchesAloneEnv+"="+strconv.Itoa(tt.watches))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("a process starting %d lock watches in turn: %v\n%s", tt.watches, err, out)
			}
		})
	}
}
