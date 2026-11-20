// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package notifier

import (
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestZoneFromTZI(t *testing.T) {
	tests := []struct {
		name string
		id   uint32
		tzi  windows.Timezoneinformation
		want int // seconds east of UTC
	}{
		{
			name: "a zone without daylight saving is Bias alone",
			id:   windows.TIME_ZONE_ID_UNKNOWN,
			tzi:  windows.Timezoneinformation{Bias: -330},
			want: 5*3600 + 30*60,
		},
		{
			name: "StandardBias doesn't count when Windows answers TIME_ZONE_ID_UNKNOWN",
			id:   windows.TIME_ZONE_ID_UNKNOWN,
			tzi:  windows.Timezoneinformation{Bias: 300, StandardBias: 60, DaylightBias: -60},
			want: -5 * 3600,
		},
		{
			name: "standard time adds StandardBias",
			id:   windows.TIME_ZONE_ID_STANDARD,
			tzi:  windows.Timezoneinformation{Bias: 300, StandardBias: 0, DaylightBias: -60},
			want: -5 * 3600,
		},
		{
			name: "daylight time adds DaylightBias",
			id:   windows.TIME_ZONE_ID_DAYLIGHT,
			tzi:  windows.Timezoneinformation{Bias: 300, StandardBias: 0, DaylightBias: -60},
			want: -4 * 3600,
		},
		{
			name: "a zone a quarter hour off the hour keeps its minutes",
			id:   windows.TIME_ZONE_ID_UNKNOWN,
			tzi:  windows.Timezoneinformation{Bias: -345},
			want: 5*3600 + 45*60,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, got := time.Now().In(zoneFromTZI(tt.id, &tt.tzi)).Zone()
			if got != tt.want {
				t.Errorf("zoneFromTZI() is %d s east of UTC, want %d", got, tt.want)
			}
		})
	}
}

func TestCurrentZone(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "while the zone hasn't changed, the zone Windows gives now has the offset of Go's time.Local now"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Now()
			_, want := now.Zone()
			_, got := now.In(currentZone()).Zone()
			if got != want {
				t.Errorf("currentZone() is %d s east of UTC, time.Local %d", got, want)
			}
		})
	}
}
