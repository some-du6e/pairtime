// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package windows

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

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
		cancel   bool   // give up at once instead of waiting out the lifetime
		post     uint32 // a message another program sends the window during the wait
	}{
		{name: "the icon is up while the balloon shows, and gone with its window after the lifetime", lifetime: 500 * time.Millisecond},
		{name: "a caller that gives up ends the wait well before the lifetime", lifetime: time.Minute, cancel: true},
		{
			// taskkill without /F sends WM_CLOSE to each top-level window.
			// DefWindowProc would destroy the window and its timer, and the
			// loop would then wait forever with the icon still up.
			name:     "a window asked to close ends the wait and outlives it, so the icon still comes down first",
			lifetime: time.Minute,
			post:     balloonWMClose,
		},
		{name: "a window told it is being destroyed ends the wait", lifetime: time.Minute, post: balloonWMDestroy},
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
				if tt.post != 0 {
					// Queued now, so the wait's message loop takes it.
					if r, _, err := balloonAPI.postMessage.Call(uintptr(hwnd), uintptr(tt.post), 0, 0); r == 0 {
						t.Errorf("PostMessageW(0x%04x) failed: %v", tt.post, err)
					}
				}
				start := time.Now()
				err = b.wait(ctx, tt.lifetime)
				waited := time.Since(start)
				cancel()
				if err != nil {
					t.Errorf("wait() error = %v", err)
				}
				if (tt.cancel || tt.post != 0) && waited > 10*time.Second {
					t.Errorf("wait() took %v, so the message or the caller didn't end it", waited)
				}
				if !windows.IsWindow(hwnd) {
					t.Error("the hidden window was gone before close, which must remove the icon first")
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

func TestOnBalloonThread(t *testing.T) {
	errOwn := errors.New("the balloon's own answer")
	tests := []struct {
		name string
		// show is the balloon's work; release ends a show that ignores ctx.
		show   func(ctx context.Context, release <-chan struct{}) error
		cancel bool // ctx is cancelled once show has started
		want   error
	}{
		{
			name:   "the balloon's own answer is returned when it comes first",
			show:   func(context.Context, <-chan struct{}) error { return errOwn },
			cancel: false,
			want:   errOwn,
		},
		{
			name: "a balloon that sees ctx and takes its icon down in time has its own answer returned",
			show: func(ctx context.Context, _ <-chan struct{}) error {
				<-ctx.Done()
				return nil
			},
			cancel: true,
			want:   nil,
		},
		{
			name: "a balloon thread stuck past the cleanup wait is left behind, and ctx's error is returned",
			show: func(_ context.Context, release <-chan struct{}) error {
				<-release
				return nil
			},
			cancel: true,
			want:   context.Canceled,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			release := make(chan struct{})
			defer close(release) // ends a stuck show after the test
			started := make(chan struct{})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.cancel {
				go func() {
					<-started
					cancel()
				}()
			}
			begin := time.Now()
			err := onBalloonThread(ctx, 200*time.Millisecond, func(ctx context.Context) error {
				close(started)
				return tt.show(ctx, release)
			})
			if !errors.Is(err, tt.want) {
				t.Errorf("onBalloonThread() = %v, want %v", err, tt.want)
			}
			if took := time.Since(begin); took > 10*time.Second {
				t.Errorf("onBalloonThread() took %v", took)
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
			if err := ShowBalloon(ctx, "pairtime test", "never shown"); !errors.Is(err, context.Canceled) {
				t.Errorf("ShowBalloon() = %v, want context.Canceled", err)
			}
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
