// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package windows

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// balloonsAloneEnv makes this test binary a process whose only GUI
// threads are balloons' (see balloonsAlone). It holds how many balloons to
// make. TestBalloonsInAProcessWithNoOtherGUIThread sets it.
const balloonsAloneEnv = "PAIRTIME_TEST_BALLOONS_ALONE"

func init() {
	// Package initialization runs on the main thread, and locking it here
	// keeps it for the main goroutine, which makes no window. Otherwise a
	// balloon's goroutine could run there, and when a goroutine locked to
	// the main thread ends, Go parks the thread for good instead of ending
	// it (runtime.mexit), which would leave a GUI thread behind.
	if os.Getenv(balloonsAloneEnv) != "" {
		runtime.LockOSThread()
	}
}

func TestMain(m *testing.M) {
	if n := os.Getenv(balloonsAloneEnv); n != "" {
		os.Exit(balloonsAlone(n))
	}
	os.Exit(m.Run())
}

// balloonsAlone makes n balloons' windows, each on a thread of its own
// that ends before the next one starts, as ShowBalloon's threads do. It
// returns the process's exit code: 0 when every window was made.
func balloonsAlone(n string) int {
	count, err := strconv.Atoi(n)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	for i := range count {
		if err := balloonOnAThreadThatEnds(); err != nil {
			fmt.Fprintf(os.Stderr, "balloon %d of %d: %v\n", i+1, count, err)
			return 1
		}
	}
	return 0
}

// balloonOnAThreadThatEnds makes a balloon's window and closes it, on a
// goroutine locked to its OS thread, which Go ends with the goroutine. It
// returns once that thread is gone, which is when Windows has done its
// cleanup for the thread.
func balloonOnAThreadThatEnds() error {
	type result struct {
		thread windows.Handle
		err    error
	}
	done := make(chan result, 1)
	go func() {
		runtime.LockOSThread() // never unlocked
		thread, err := windows.OpenThread(windows.SYNCHRONIZE, false, windows.GetCurrentThreadId())
		if err != nil {
			done <- result{err: fmt.Errorf("OpenThread: %w", err)}
			return
		}
		b, err := newBalloon()
		if err == nil {
			b.close()
		}
		done <- result{thread: thread, err: err}
	}()
	r := <-done
	if r.thread == 0 {
		return r.err
	}
	defer windows.CloseHandle(r.thread)
	switch ev, err := windows.WaitForSingleObject(r.thread, 10_000); {
	case err != nil:
		return fmt.Errorf("WaitForSingleObject: %w", err)
	case ev != windows.WAIT_OBJECT_0:
		return errors.New("the balloon's thread was still there 10 s after its goroutine ended")
	}
	return r.err
}

// Windows destroys the window classes a process registered when the last
// of the process's GUI threads ends, and a balloon's thread ends with its
// balloon. In a process with no other GUI thread, a class registered only
// once makes every balloon after the first fail with "Cannot find window
// class". This runs in a child process, because in this one other tests'
// threads have called user32, and Go keeps those threads.
func TestBalloonsInAProcessWithNoOtherGUIThread(t *testing.T) {
	tests := []struct {
		name     string
		balloons int
	}{
		{name: "the first balloon gets its window", balloons: 1},
		{name: "balloons made one after another, each once the thread of the one before has ended, all get their windows", balloons: 3},
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(exe, "-test.run=^$")
			cmd.Env = append(os.Environ(), balloonsAloneEnv+"="+strconv.Itoa(tt.balloons))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("a process making %d balloons in turn: %v\n%s", tt.balloons, err, out)
			}
		})
	}
}

// onLockedThread runs fn on a goroutine locked to its OS thread, as
// ShowBalloon does, and waits for it.
func onLockedThread(t *testing.T, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		runtime.LockOSThread()
		fn()
	}()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("the balloon's thread didn't finish in 60 s")
	}
}

// The smoke test platform.yml runs on windows-latest (docs/PLAN.md,
// WP-OS8): the transient window and icon are created, a real balloon goes
// up, and both are gone afterwards. The shell is the oracle: NIM_MODIFY
// succeeds for an icon it has and fails for one it doesn't.
func TestTransientIconIsCreatedAndRemoved(t *testing.T) {
	tests := []struct {
		name     string
		lifetime time.Duration
		cancel   bool // give up at once instead of waiting out the lifetime
	}{
		{name: "the icon is up while the balloon shows, and gone with its window after the lifetime", lifetime: 500 * time.Millisecond},
		{name: "a caller that gives up ends the wait well before the lifetime", lifetime: time.Minute, cancel: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			onLockedThread(t, func() {
				b, err := newBalloon()
				if err != nil {
					t.Errorf("newBalloon() error = %v", err)
					return
				}
				hwnd := b.hwnd
				if err := b.addIcon(); err != nil {
					b.close()
					t.Errorf("addIcon() error = %v; is Explorer's taskbar running on this runner?", err)
					return
				}
				if !b.iconExists() {
					t.Error("the shell doesn't have the icon that was just added")
				}
				if err := b.show("pairtime test", "This balloon comes from pairtime's CI. It goes away by itself."); err != nil {
					t.Errorf("show() error = %v", err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				if tt.cancel {
					cancel()
				}
				start := time.Now()
				err = b.wait(ctx, tt.lifetime)
				waited := time.Since(start)
				cancel()
				if err != nil {
					t.Errorf("wait() error = %v", err)
				}
				if tt.cancel && waited > 10*time.Second {
					t.Errorf("wait() took %v after the caller gave up", waited)
				}
				b.removeIcon()
				if b.iconExists() {
					t.Error("the shell still has the icon after NIM_DELETE")
				}
				b.close()
				if windows.IsWindow(hwnd) {
					t.Error("the hidden window still exists after close")
				}
			})
		})
	}
}

// The hidden window ends its message loop however it goes, and only its
// own code destroys it.
func TestHiddenWindowEndsItsLoop(t *testing.T) {
	tests := []struct {
		name string
		// do runs on the window's thread before its message loop does.
		do         func(b *balloon)
		wantWindow bool // the window still exists once the loop has ended
	}{
		{
			name: "a WM_CLOSE from outside, as taskkill sends, leaves the window for close to destroy",
			do: func(b *balloon) {
				balloonAPI.postMessage.Call(uintptr(b.hwnd), wmClose, 0, 0)
				balloonAPI.postMessage.Call(uintptr(b.hwnd), balloonWMStop, 0, 0)
			},
			wantWindow: true,
		},
		{
			name: "a window destroyed under the loop ends the loop",
			do: func(b *balloon) {
				balloonAPI.destroyWindow.Call(uintptr(b.hwnd))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			onLockedThread(t, func() {
				b, err := newBalloon()
				if err != nil {
					t.Errorf("newBalloon() error = %v", err)
					return
				}
				defer b.close()
				tt.do(b)
				// Before the fix this never returned, and onLockedThread
				// gave up after 60 s.
				if err := pumpBalloonMessages(); err != nil {
					t.Errorf("pumpBalloonMessages() error = %v", err)
				}
				if got := windows.IsWindow(b.hwnd); got != tt.wantWindow {
					t.Errorf("the window exists after the loop: %v, want %v", got, tt.wantWindow)
				}
			})
		})
	}
}

func TestAwaitBalloon(t *testing.T) {
	failed := errors.New("SetTimer failed")
	tests := []struct {
		name string
		// result is what the balloon's thread sends, after delay; a negative
		// delay means it never finishes.
		result  error
		delay   time.Duration
		cancel  bool // the caller has given up
		cleanup time.Duration
		want    error
		// How long awaitBalloon may take.
		minWait, maxWait time.Duration
	}{
		{name: "the thread's result comes back when it finishes first", result: failed, cleanup: time.Minute, want: failed, maxWait: 5 * time.Second},
		{name: "a thread that finishes during the cleanup wait after the caller gave up has its result returned", result: nil, delay: 50 * time.Millisecond, cancel: true, cleanup: time.Minute, want: nil, maxWait: 5 * time.Second},
		{name: "a thread that never finishes holds a caller who gave up only for the cleanup wait", delay: -1, cancel: true, cleanup: 300 * time.Millisecond, want: context.Canceled, minWait: 200 * time.Millisecond, maxWait: 10 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.cancel {
				cancel()
			}
			done := make(chan error, 1)
			if tt.delay >= 0 {
				time.AfterFunc(tt.delay, func() { done <- tt.result })
			}
			start := time.Now()
			err := awaitBalloon(ctx, done, tt.cleanup)
			waited := time.Since(start)
			if !errors.Is(err, tt.want) || (tt.want == nil && err != nil) {
				t.Errorf("awaitBalloon() = %v, want %v", err, tt.want)
			}
			if waited < tt.minWait || waited > tt.maxWait {
				t.Errorf("awaitBalloon() took %v, want from %v to %v", waited, tt.minWait, tt.maxWait)
			}
		})
	}
}

func TestQueryUserNotificationState(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "Windows answers with one of the seven QUERY_USER_NOTIFICATION_STATE values"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := QueryUserNotificationState()
			if err != nil {
				t.Fatal(err)
			}
			if s < qunsNotPresent || s > qunsApp {
				t.Errorf("QueryUserNotificationState() = %d, outside the enum", int32(s))
			}
			t.Logf("this session: %s", s)
		})
	}
}

func TestShowBalloonGivesUpOnADoneContext(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "a context that is already done shows nothing and returns its error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			called := false
			if err := ShowBalloon(ctx, "pairtime test", "never shown", func() { called = true }); !errors.Is(err, context.Canceled) {
				t.Errorf("ShowBalloon() = %v, want context.Canceled", err)
			}
			if called {
				t.Error("ShowBalloon called shown for a balloon it never showed")
			}
		})
	}
}

// The notifier counts the gap to the next notification from the moment the
// shell takes a balloon, not from the balloon's end, so ShowBalloon says
// when that is (#86).
func TestBalloonSaysWhenTheShellTookIt(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "shown is called once, as the balloon goes up and before the wait for it to go, which a caller that gives up then ends at once"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			onLockedThread(t, func() {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				calls := 0
				start := time.Now()
				err := showBalloonOnThisThread(ctx, "pairtime test", "This balloon comes from pairtime's CI. It goes away by itself.", time.Minute, func() {
					calls++
					cancel()
				})
				if err != nil {
					t.Errorf("showBalloonOnThisThread() error = %v", err)
				}
				if calls != 1 {
					t.Errorf("shown was called %d times, want once", calls)
				}
				if d := time.Since(start); d > 10*time.Second {
					t.Errorf("the balloon's wait took %v after shown gave up, so shown came after the wait", d)
				}
			})
		})
	}
}

func TestPutBalloonText(t *testing.T) {
	tests := []struct {
		name string
		size int
		in   string
		want []uint16 // the units before the NUL
	}{
		{name: "text that fits is copied whole and ends in NUL", size: 8, in: "hi", want: []uint16{'h', 'i'}},
		{name: "text that doesn't fit is cut to leave room for the NUL", size: 4, in: "abcdef", want: []uint16{'a', 'b', 'c'}},
		{name: "a cut never splits a surrogate pair", size: 4, in: "ab😀", want: []uint16{'a', 'b'}},
		{name: "a surrogate pair that fits is kept", size: 5, in: "ab😀", want: []uint16{'a', 'b', 0xd83d, 0xde00}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := make([]uint16, tt.size)
			for i := range dst {
				dst[i] = 0xffff
			}
			putBalloonText(dst, tt.in)
			n := len(tt.want)
			for i := 0; i < n; i++ {
				if dst[i] != tt.want[i] {
					t.Fatalf("putBalloonText() = %x, want %x then NUL", dst, tt.want)
				}
			}
			if dst[n] != 0 {
				t.Errorf("putBalloonText() = %x, want NUL after %x", dst, tt.want)
			}
		})
	}
}
