// SPDX-License-Identifier: AGPL-3.0-or-later

package clock_test

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/clock"
)

func TestNow(t *testing.T) {
	switch runtime.GOOS {
	case platform.Linux, platform.Darwin, platform.Windows:
	default:
		t.Skipf("pairtime reads no monotonic clock on %s", runtime.GOOS)
	}
	tests := []struct {
		name  string
		check func(t *testing.T, a, b clock.Stamp)
	}{
		{"every stamp has a boot id the inbox takes, and a monotonic reading", func(t *testing.T, a, b clock.Stamp) {
			for _, s := range []clock.Stamp{a, b} {
				if !s.HasMono() {
					t.Errorf("stamp %+v has no monotonic reading or no boot id", s)
				}
				m := inbox.Message{ID: inbox.NewID(), Kind: "hook", Sent: s.Wall, Mono: s.Mono, BootID: s.BootID}
				if err := m.Check(); err != nil {
					t.Errorf("the inbox doesn't take the stamp: %v", err)
				}
			}
		}},
		{"the boot id is the same from one stamp to the next", func(t *testing.T, a, b clock.Stamp) {
			if a.BootID != b.BootID {
				t.Errorf("boot ids %q and %q, want one boot", a.BootID, b.BootID)
			}
		}},
		{"the monotonic clock moves on with the wall clock", func(t *testing.T, a, b clock.Stamp) {
			mono := time.Duration(b.Mono-a.Mono) * time.Millisecond
			wall := b.Wall.Sub(a.Wall)
			if mono <= 0 || mono-wall > time.Second || wall-mono > time.Second {
				t.Errorf("the monotonic clock moved %v while the wall clock moved %v", mono, wall)
			}
		}},
		{"the wall time is UTC with no Go monotonic reading", func(t *testing.T, a, _ clock.Stamp) {
			if a.Wall.Location() != time.UTC || strings.Contains(a.Wall.String(), "m=") {
				t.Errorf("wall time %s isn't plain UTC", a.Wall)
			}
		}},
	}
	a := clock.Now()
	time.Sleep(100 * time.Millisecond)
	b := clock.Now()
	t.Logf("stamps %+v and %+v", a, b)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { tt.check(t, a, b) })
	}
}

func TestHasMono(t *testing.T) {
	tests := []struct {
		name string
		s    clock.Stamp
		want bool
	}{
		{"a reading and its boot make a monotonic stamp", clock.Stamp{Mono: 5000, BootID: "b-1"}, true},
		{"a reading without its boot isn't one", clock.Stamp{Mono: 5000}, false},
		{"a boot without a reading isn't one", clock.Stamp{BootID: "b-1"}, false},
		{"a wall time alone isn't one", clock.Stamp{Wall: time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.HasMono(); got != tt.want {
				t.Errorf("HasMono = %v, want %v", got, tt.want)
			}
		})
	}
}
