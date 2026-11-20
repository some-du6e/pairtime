// SPDX-License-Identifier: AGPL-3.0-or-later

package darwin

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
)

// timeline records what happened and when, in synctest's fake time, so a
// case can check both the order of handler calls and IOKit answers and how
// long each waited.
type timeline struct {
	mu    sync.Mutex
	start time.Time
	lines []string
}

func newTimeline() *timeline { return &timeline{start: time.Now()} }

func (tl *timeline) add(format string, args ...any) {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	tl.lines = append(tl.lines, fmt.Sprintf("%v ", time.Since(tl.start))+fmt.Sprintf(format, args...))
}

func (tl *timeline) get() []string {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	return slices.Clone(tl.lines)
}

// fakeBackend stands in for the frameworks. A case plays the OS by calling
// the Loop's callbacks, as the darwin backend's IOKit, distributed
// notification and NSWorkspace callbacks do.
type fakeBackend struct {
	tl     *timeline
	st     Status
	onOpen func(l *Loop)
	stopc  chan struct{}
	once   sync.Once
	closed bool
}

func (f *fakeBackend) open(l *Loop) Status {
	if f.onOpen != nil {
		f.onOpen(l)
	}
	return f.st
}

func (f *fakeBackend) run()  { <-f.stopc }
func (f *fakeBackend) stop() { f.once.Do(func() { close(f.stopc) }) }

func (f *fakeBackend) close() { f.closed = true }

func (f *fakeBackend) allowPowerChange(id uintptr) { f.tl.add("ack %d", id) }

// recorder is a Handler that writes each call to the timeline. Power
// blocks for hold[op] first, and Focus waits for gate when it is set.
type recorder struct {
	tl   *timeline
	hold map[event.PowerOp]time.Duration
	gate chan struct{}
}

func (r *recorder) Power(at time.Time, op event.PowerOp) {
	r.tl.add("power %s", op)
	if d := r.hold[op]; d > 0 {
		time.Sleep(d)
		r.tl.add("power %s returned", op)
	}
}

func (r *recorder) Focus(at time.Time, app App) {
	if r.gate != nil {
		<-r.gate
	}
	r.tl.add("focus %d %s", app.PID, app.BundleID)
}

// startLoop runs a Loop over fb and returns it with the step that stops it
// and returns Run's error.
func startLoop(fb *fakeBackend, h Handler) (*Loop, func() error) {
	fb.stopc = make(chan struct{})
	l := NewLoop(h)
	l.newBackend = func() (backend, error) { return fb, nil }
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- l.Run(ctx) }()
	<-l.Ready()
	return l, func() error {
		cancel()
		return <-errc
	}
}

func TestLoop(t *testing.T) {
	terminal := App{PID: 10, BundleID: "com.apple.Terminal", Executable: "/System/Applications/Utilities/Terminal.app/Contents/MacOS/Terminal"}
	editor := App{PID: 20, BundleID: "com.microsoft.VSCode"}
	tests := []struct {
		name   string
		hold   map[event.PowerOp]time.Duration
		onOpen func(l *Loop)
		play   func(l *Loop)
		want   []string
	}{
		{
			name: "a suspend reaches the handler before macOS gets its answer",
			hold: map[event.PowerOp]time.Duration{event.PowerSuspend: time.Second},
			play: func(l *Loop) { l.powerMessage(time.Now(), msgSystemWillSleep, 7) },
			want: []string{"0s power suspend", "1s power suspend returned", "1s ack 7"},
		},
		{
			name: "a handler that holds a suspend past SuspendWait doesn't hold the sleep",
			hold: map[event.PowerOp]time.Duration{event.PowerSuspend: time.Minute},
			play: func(l *Loop) { l.powerMessage(time.Now(), msgSystemWillSleep, 7) },
			want: []string{"0s power suspend", "5s ack 7", "1m0s power suspend returned"},
		},
		{
			name: "an idle-sleep query is answered at once and reported to no one",
			play: func(l *Loop) { l.powerMessage(time.Now(), msgCanSystemSleep, 3) },
			want: []string{"0s ack 3"},
		},
		{
			name: "wake is one resume, and the messages around it want no answer",
			play: func(l *Loop) {
				l.powerMessage(time.Now(), msgSystemWillPowerOn, 1)
				l.powerMessage(time.Now(), msgSystemHasPoweredOn, 2)
				l.powerMessage(time.Now(), msgSystemWillNotSleep, 3)
				l.powerMessage(time.Now(), msgSystemWillRestart, 4)
			},
			want: []string{"0s power resume"},
		},
		{
			name: "a second sleep with no wake between is answered but not reported again",
			play: func(l *Loop) {
				l.powerMessage(time.Now(), msgSystemWillSleep, 1)
				l.powerMessage(time.Now(), msgSystemWillSleep, 2)
				l.powerMessage(time.Now(), msgSystemHasPoweredOn, 0)
				l.powerMessage(time.Now(), msgSystemWillSleep, 3)
			},
			want: []string{"0s power suspend", "0s ack 1", "0s ack 2", "0s power resume", "0s power suspend", "0s ack 3"},
		},
		{
			name: "lock and unlock are reported once each when the state at the start is unknown",
			play: func(l *Loop) {
				l.lockChanged(time.Now(), true)
				l.lockChanged(time.Now(), true)
				l.lockChanged(time.Now(), false)
				l.lockChanged(time.Now(), false)
			},
			want: []string{"0s power lock", "0s power unlock"},
		},
		{
			name:   "an unlock while the screen was unlocked at the start is not reported",
			onOpen: func(l *Loop) { l.seedLock(false) },
			play: func(l *Loop) {
				l.lockChanged(time.Now(), false)
				l.lockChanged(time.Now(), true)
			},
			want: []string{"0s power lock"},
		},
		{
			name:   "the app in front at the start is the first focus event, and activating it again reports nothing",
			onOpen: func(l *Loop) { l.activated(time.Now(), terminal) },
			play: func(l *Loop) {
				l.activated(time.Now(), terminal)
				l.activated(time.Now(), editor)
				l.activated(time.Now(), terminal)
			},
			want: []string{"0s focus 10 com.apple.Terminal", "0s focus 20 com.microsoft.VSCode", "0s focus 10 com.apple.Terminal"},
		},
		{
			name: "power and focus events reach the handler in the order they came",
			play: func(l *Loop) {
				l.activated(time.Now(), editor)
				l.lockChanged(time.Now(), true)
				l.powerMessage(time.Now(), msgSystemWillSleep, 5)
				l.powerMessage(time.Now(), msgSystemHasPoweredOn, 0)
				l.lockChanged(time.Now(), false)
				l.activated(time.Now(), terminal)
			},
			want: []string{
				"0s focus 20 com.microsoft.VSCode", "0s power lock", "0s power suspend", "0s ack 5",
				"0s power resume", "0s power unlock", "0s focus 10 com.apple.Terminal",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				tl := newTimeline()
				fb := &fakeBackend{tl: tl, onOpen: tt.onOpen}
				l, stop := startLoop(fb, &recorder{tl: tl, hold: tt.hold})
				tt.play(l)
				if err := stop(); err != nil {
					t.Fatalf("Run: %v", err)
				}
				if got := tl.get(); !slices.Equal(got, tt.want) {
					t.Errorf("timeline:\n got %q\nwant %q", got, tt.want)
				}
				if !fb.closed {
					t.Error("Run returned without closing the backend")
				}
			})
		})
	}
}

// TestLoopFullQueue checks that the OS side never waits for a handler that
// has fallen behind.
func TestLoopFullQueue(t *testing.T) {
	tests := []struct {
		name        string
		extra       int // activations beyond what the handler and the queue hold
		wantDropped uint64
	}{
		{name: "a handler queueLen events behind loses the next ones and Status counts them", extra: 3, wantDropped: 3},
		{name: "a queue with room drops nothing", extra: 0, wantDropped: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				tl := newTimeline()
				gate := make(chan struct{})
				l, stop := startLoop(&fakeBackend{tl: tl}, &recorder{tl: tl, gate: gate})
				// The first event is taken by the handler, which then waits at
				// the gate, so the queue holds the next queueLen.
				l.activated(time.Now(), App{PID: 1})
				synctest.Wait()
				for pid := 2; pid < 2+queueLen+tt.extra; pid++ {
					l.activated(time.Now(), App{PID: pid})
				}
				if got := l.Status().Dropped; got != tt.wantDropped {
					t.Errorf("Status().Dropped = %d, want %d", got, tt.wantDropped)
				}
				close(gate)
				if err := stop(); err != nil {
					t.Fatalf("Run: %v", err)
				}
				if got, want := len(tl.get()), 1+queueLen; got != want {
					t.Errorf("the handler got %d events, want %d", got, want)
				}
			})
		})
	}
}

func TestLoopRun(t *testing.T) {
	errNone := errors.New("not registered")
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "Run with no source working returns an error and closes the backend",
			run: func(t *testing.T) {
				fb := &fakeBackend{tl: newTimeline(), st: Status{Power: errNone, Lock: errNone, Focus: errNone}, stopc: make(chan struct{})}
				l := NewLoop(&recorder{tl: fb.tl})
				l.newBackend = func() (backend, error) { return fb, nil }
				err := l.Run(context.Background())
				if !errors.Is(err, errNone) {
					t.Errorf("Run: err = %v, want one wrapping %v", err, errNone)
				}
				if !fb.closed {
					t.Error("the backend wasn't closed")
				}
			},
		},
		{
			name: "Run with some sources working runs, and Status says which failed",
			run: func(t *testing.T) {
				fb := &fakeBackend{tl: newTimeline(), st: Status{Lock: errNone, Focus: errNone}}
				l, stop := startLoop(fb, &recorder{tl: fb.tl})
				if st := l.Status(); st.Power != nil || !errors.Is(st.Lock, errNone) || !errors.Is(st.Focus, errNone) {
					t.Errorf("Status() = %+v, want only Lock and Focus failed", st)
				}
				if err := stop(); err != nil {
					t.Errorf("Run: %v", err)
				}
			},
		},
		{
			name: "a second Loop can't run while one runs",
			run: func(t *testing.T) {
				fb := &fakeBackend{tl: newTimeline()}
				_, stop := startLoop(fb, &recorder{tl: fb.tl})
				other := NewLoop(&recorder{tl: fb.tl})
				other.newBackend = func() (backend, error) { return &fakeBackend{tl: fb.tl, stopc: make(chan struct{})}, nil }
				if err := other.Run(context.Background()); !errors.Is(err, ErrRunning) {
					t.Errorf("second Loop's Run: err = %v, want ErrRunning", err)
				}
				select {
				case <-other.Ready():
				default:
					t.Error("Ready stayed open after Run returned")
				}
				if err := stop(); err != nil {
					t.Errorf("Run: %v", err)
				}
			},
		},
		{
			name: "a Loop runs once",
			run: func(t *testing.T) {
				fb := &fakeBackend{tl: newTimeline()}
				l, stop := startLoop(fb, &recorder{tl: fb.tl})
				if err := stop(); err != nil {
					t.Fatal(err)
				}
				if err := l.Run(context.Background()); !errors.Is(err, errRunTwice) {
					t.Errorf("second Run: err = %v, want errRunTwice", err)
				}
			},
		},
		{
			name: "a Loop whose context is done before Run stops at once",
			run: func(t *testing.T) {
				fb := &fakeBackend{tl: newTimeline(), stopc: make(chan struct{})}
				l := NewLoop(&recorder{tl: fb.tl})
				l.newBackend = func() (backend, error) { return fb, nil }
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				if err := l.Run(ctx); err != nil {
					t.Errorf("Run: %v", err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, tt.run)
		})
	}
}

func TestDecodePower(t *testing.T) {
	tests := []struct {
		name string
		msg  uint32
		want powerAction
	}{
		{name: "kIOMessageSystemWillSleep is a suspend that wants an answer", msg: msgSystemWillSleep, want: powerAction{op: event.PowerSuspend, ack: true}},
		{name: "kIOMessageCanSystemSleep wants an answer and is no event", msg: msgCanSystemSleep, want: powerAction{ack: true}},
		{name: "kIOMessageSystemHasPoweredOn is a resume", msg: msgSystemHasPoweredOn, want: powerAction{op: event.PowerResume}},
		{name: "kIOMessageSystemWillPowerOn is information, since HasPoweredOn follows", msg: msgSystemWillPowerOn, want: powerAction{}},
		{name: "kIOMessageSystemWillNotSleep is information", msg: msgSystemWillNotSleep, want: powerAction{}},
		{name: "an unknown message is ignored", msg: 0xE0000999, want: powerAction{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decodePower(tt.msg); got != tt.want {
				t.Errorf("decodePower(%#x) = %+v, want %+v", tt.msg, got, tt.want)
			}
		})
	}
}

// TestPowerMessageValues pins the IOKit constants to IOMessage.h's
// definition, iokit_common_msg(n) = 0xE0000000 | n.
func TestPowerMessageValues(t *testing.T) {
	sysIOKit := uint32(0x38&0x3f) << 26
	tests := []struct {
		name string
		got  uint32
		n    uint32
	}{
		{name: "kIOMessageCanSystemSleep is iokit_common_msg(0x270)", got: msgCanSystemSleep, n: 0x270},
		{name: "kIOMessageSystemWillSleep is iokit_common_msg(0x280)", got: msgSystemWillSleep, n: 0x280},
		{name: "kIOMessageSystemWillNotSleep is iokit_common_msg(0x290)", got: msgSystemWillNotSleep, n: 0x290},
		{name: "kIOMessageSystemHasPoweredOn is iokit_common_msg(0x300)", got: msgSystemHasPoweredOn, n: 0x300},
		{name: "kIOMessageSystemWillRestart is iokit_common_msg(0x310)", got: msgSystemWillRestart, n: 0x310},
		{name: "kIOMessageSystemWillPowerOn is iokit_common_msg(0x320)", got: msgSystemWillPowerOn, n: 0x320},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if want := sysIOKit | tt.n; tt.got != want {
				t.Errorf("got %#x, want %#x", tt.got, want)
			}
		})
	}
}
