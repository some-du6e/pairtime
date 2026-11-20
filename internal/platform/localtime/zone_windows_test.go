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

func TestZoneAsksWindowsEachTime(t *testing.T) {
	pacific := windows.Timezoneinformation{Bias: 480, DaylightBias: -60}
	tokyo := windows.Timezoneinformation{Bias: -540}
	type answer struct {
		tzi windows.Timezoneinformation
		rc  uint32
		err error
	}
	tests := []struct {
		name    string
		answers []answer // what GetTimeZoneInformation says at each call
		// wantOffsets are the offsets, in seconds, of the zones the calls
		// return; nil means the call returns time.Local.
		wantOffsets []*int
	}{
		{
			name: "a zone changed between two calls is the zone of the second",
			answers: []answer{
				{tzi: pacific, rc: windows.TIME_ZONE_ID_STANDARD},
				{tzi: tokyo, rc: windows.TIME_ZONE_ID_UNKNOWN},
			},
			wantOffsets: []*int{intp(-8 * 3600), intp(9 * 3600)},
		},
		{
			name:        "a failed call gives time.Local",
			answers:     []answer{{rc: 0xffffffff, err: windows.ERROR_INVALID_PARAMETER}},
			wantOffsets: []*int{nil},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for i, a := range tt.answers {
				query := func(tzi *windows.Timezoneinformation) (uint32, error) {
					*tzi = a.tzi
					return a.rc, a.err
				}
				got := zoneFrom(query)
				switch want := tt.wantOffsets[i]; {
				case want == nil && got != time.Local:
					t.Errorf("call %d: zoneFrom() = %v, want time.Local", i, got)
				case want != nil:
					if _, off := time.Now().In(got).Zone(); off != *want {
						t.Errorf("call %d: zoneFrom()'s offset = %d s, want %d s", i, off, *want)
					}
				}
			}
		})
	}
}

func TestZoneIsTheOneWindowsReportsNow(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "the real GetTimeZoneInformation answers, and Zone's offset is the one it reports"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var tzi windows.Timezoneinformation
			rc, err := windows.GetTimeZoneInformation(&tzi)
			if err != nil {
				t.Fatalf("GetTimeZoneInformation() error = %v", err)
			}
			if _, got := time.Now().In(Zone()).Zone(); got != offset(&tzi, rc) {
				t.Errorf("Zone()'s offset = %d s, GetTimeZoneInformation's = %d s", got, offset(&tzi, rc))
			}
		})
	}
}

func intp(n int) *int { return &n }
