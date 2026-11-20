// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package windows

import (
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestIdleMillis(t *testing.T) {
	tests := []struct {
		name            string
		tick, lastInput uint32
		want            uint32
	}{
		{"idle is the tick minus the last input's tick", 10_000, 4_000, 6_000},
		{"input on the current tick is no idle", 5_000, 5_000, 0},
		{"the difference is right across the tick's wrap to zero", 0x0000_1000, 0xFFFF_F000, 0x2000},
		{"a last input ahead of the tick clamps to zero", 4_000, 4_500, 0},
		{"a last input ahead of the tick across the wrap clamps to zero", 0xFFFF_FFF0, 0x0000_0010, 0},
		{"a last input ahead of the tick by the whole bound clamps to zero", 10_000, 10_000 + maxLeadMillis, 0},
		{"a last input further ahead than the bound reads as a long idle, not as input now", 10_000, 10_000 + maxLeadMillis + 1, 0xFFFF_FFFF - maxLeadMillis},
		{"a last input stamped 24.8 days ahead reads as a long idle", 1_000, 1_000 + 0x7FFF_0000, 0x8001_0000},
		{"24.8 days of idle still read as idle", 0x7FFF_FFFF, 0, 0x7FFF_FFFF},
		{"idle past 24.8 days still reads as idle", 0x8000_0000, 0, 0x8000_0000},
		{"idle up to 49.7 days less the bound still reads as idle", 0xFFFF_FFFF - maxLeadMillis, 0, 0xFFFF_FFFF - maxLeadMillis},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := idleMillis(tt.tick, tt.lastInput); got != tt.want {
				t.Errorf("idleMillis(%#x, %#x) = %#x, want %#x", tt.tick, tt.lastInput, got, tt.want)
			}
		})
	}
}

// TestIdleTime reads the runner's own idle time. Nobody touches a hosted
// runner, so the test assumes nothing about the value beyond what the
// clock allows.
func TestIdleTime(t *testing.T) {
	tests := []struct {
		name  string
		pause time.Duration
	}{
		{"GetLastInputInfo reads an idle time no longer than the uptime, which grows no faster than the clock", time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first, err := IdleTime()
			if err != nil {
				t.Fatal(err)
			}
			begin := time.Now()
			time.Sleep(tt.pause)
			second, err := IdleTime()
			if err != nil {
				t.Fatal(err)
			}
			elapsed := time.Since(begin)
			if first < 0 || second < 0 {
				t.Errorf("idle times %v and %v, want neither negative", first, second)
			}
			// GetTickCount moves in steps of about 16 ms.
			if grew := second - first; grew > elapsed+100*time.Millisecond {
				t.Errorf("idle time grew by %v in %v", grew, elapsed)
			}
			if up := windows.DurationSinceBoot(); second > up+time.Second {
				t.Errorf("idle time %v is longer than the uptime %v", second, up)
			}
		})
	}
}
