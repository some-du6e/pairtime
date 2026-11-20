// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package windows

import (
	"testing"
	"time"
)

// No test here moves focus or makes a window: hosted runners get the
// real hook only to start, read and stop it (JE must-fix 11).

func TestForegroundFrom(t *testing.T) {
	at := time.Date(2026, 11, 20, 14, 0, 0, 0, time.UTC)
	pids := map[uintptr]uint32{0x10: 4242}
	tests := []struct {
		name string
		hwnd uintptr
		want Foreground
	}{
		{"no foreground window is focus pairtime can't name", 0, Foreground{At: at, Unknown: UnknownNoWindow}},
		{"a window that closed before its process was read is focus pairtime can't name", 0x20, Foreground{At: at, Unknown: UnknownNoProcess}},
		{"a window in front names its process", 0x10, Foreground{At: at, Window: 0x10, PID: 4242}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := foregroundFrom(at, tt.hwnd, func(h uintptr) uint32 { return pids[h] })
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestForegroundReport(t *testing.T) {
	a := Foreground{Window: 0x10, PID: 4242}
	b := Foreground{Window: 0x20, PID: 4343}
	away := Foreground{Unknown: UnknownOtherDesktop}
	start := a
	start.Start = true
	tests := []struct {
		name string
		seq  []Foreground
		want int
	}{
		{"one window reported twice is one change", []Foreground{a, a}, 1},
		{"two windows are two changes", []Foreground{a, b, a}, 3},
		{"a window back in front after another desktop had the input is a change", []Foreground{a, away, a}, 3},
		{"another desktop reported twice is one change", []Foreground{a, away, away}, 2},
		{"the report at the start always goes out", []Foreground{start}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := 0
			w := &foregroundWatch{fn: func(Foreground) { n++ }}
			for _, f := range tt.seq {
				w.report(f)
			}
			if n != tt.want {
				t.Errorf("%d reports, want %d", n, tt.want)
			}
		})
	}
}

func TestOnWinEventWithoutAWatch(t *testing.T) {
	tests := []struct {
		name string
		ev   uint32
	}{
		{"a foreground event for a hook no watch owns is dropped", eventSystemForeground},
		{"a desktop switch for a hook no watch owns is dropped", eventSystemDesktopSwitch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if r := onWinEvent(0xdead, tt.ev, 0x10, 0, 0, 0, 0); r != 0 {
				t.Errorf("onWinEvent returned %d, want 0", r)
			}
		})
	}
}

// TestWatchForeground starts the real hook on the runner's desktop, which
// reports the window in front at once, whatever it is, or that there is
// none, and stops it, many times over: each watch has its own thread and
// hooks, and all of them share one callback, which Go never frees.
func TestWatchForeground(t *testing.T) {
	tests := []struct {
		name  string
		times int
	}{
		{"on Windows the hook reports the window in front as it starts, and stop unhooks it, every time", 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for i := range tt.times {
				got := make(chan Foreground, 8)
				stop, err := WatchForeground(func(f Foreground) {
					select {
					case got <- f:
					default:
					}
				})
				if err != nil {
					t.Fatalf("watch %d: %v", i, err)
				}
				select {
				case f := <-got:
					switch {
					case !f.Start:
						t.Errorf("watch %d: the first report isn't marked as the start: %+v", i, f)
					case f.Unknown == "" && (f.Window == 0 || f.PID == 0):
						t.Errorf("watch %d: a known window without a handle or a process: %+v", i, f)
					case f.Unknown != "" && (f.Window != 0 || f.PID != 0):
						t.Errorf("watch %d: an unknown window with a handle or a process: %+v", i, f)
					}
					if i == 0 {
						t.Logf("the runner's foreground: %+v", f)
					}
				case <-time.After(5 * time.Second):
					t.Fatalf("watch %d: no report of the window in front within 5 s", i)
				}
				stop()
				stop() // a second stop does nothing
				foregroundMu.Lock()
				n := len(foregroundWatches)
				foregroundMu.Unlock()
				if n != 0 {
					t.Fatalf("watch %d: %d hooks left after stop", i, n)
				}
			}
		})
	}
}
