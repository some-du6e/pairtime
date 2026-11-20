// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package localtime

import (
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestOffset(t *testing.T) {
	// Pacific Time as Windows describes it: 480 minutes west of UTC, and
	// 60 fewer in daylight time.
	pacific := windows.Timezoneinformation{Bias: 480, StandardBias: 0, DaylightBias: -60}
	india := windows.Timezoneinformation{Bias: -330}
	tests := []struct {
		name string
		tzi  windows.Timezoneinformation
		rc   uint32
		want int
	}{
		{name: "in standard time the offset is the bias plus the standard bias", tzi: pacific, rc: windows.TIME_ZONE_ID_STANDARD, want: -8 * 3600},
		{name: "in daylight time the offset is the bias plus the daylight bias", tzi: pacific, rc: windows.TIME_ZONE_ID_DAYLIGHT, want: -7 * 3600},
		{name: "a zone without daylight time is the bias alone", tzi: india, rc: windows.TIME_ZONE_ID_UNKNOWN, want: 5*3600 + 30*60},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := offset(&tt.tzi, tt.rc); got != tt.want {
				t.Errorf("offset() = %d s, want %d s", got, tt.want)
			}
		})
	}
}

func TestZoneIsTheOneWindowsReportsNow(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "on a computer whose zone hasn't changed since the test started, Zone agrees with time.Local about now"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Now()
			_, want := now.Zone()
			_, got := now.In(Zone()).Zone()
			if got != want {
				t.Errorf("Zone()'s offset now = %d s, time.Local's = %d s", got, want)
			}
		})
	}
}
