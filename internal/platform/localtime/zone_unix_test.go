// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package localtime

import (
	"encoding/binary"
	"errors"
	"io/fs"
	"testing"
	"time"
)

// tzif returns a TZif file, version 1, for a zone that is always offset
// seconds east of UTC, which is what /etc/localtime holds.
func tzif(offset int32, abbr string) []byte {
	b := []byte("TZif")
	b = append(b, make([]byte, 16)...) // version 1, then 15 bytes of padding
	// UT/local and standard/wall indicators, leap seconds, transitions,
	// local time types, abbreviation bytes.
	for _, n := range []uint32{0, 0, 0, 0, 1, uint32(len(abbr) + 1)} {
		b = binary.BigEndian.AppendUint32(b, n)
	}
	b = binary.BigEndian.AppendUint32(b, uint32(offset))
	b = append(b, 0, 0) // not daylight time; the abbreviation at index 0
	b = append(b, abbr...)
	return append(b, 0)
}

func TestZoneFollowsTheComputer(t *testing.T) {
	// 12:00 UTC is 04:00 in Los Angeles in November and 21:00 in Tokyo.
	noon := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	pacific, tokyo := tzif(-8*3600, "PST"), tzif(9*3600, "JST")
	tests := []struct {
		name  string
		tz    *string  // $TZ; nil means unset
		files [][]byte // what /etc/localtime holds at each call; nil means unreadable
		// wantHours are the clock hours of noon UTC in the zone each call
		// returns; -1 means the call returns time.Local.
		wantHours []int
	}{
		{name: "with TZ unset, /etc/localtime gives the zone", files: [][]byte{pacific}, wantHours: []int{4}},
		{name: "a zone changed while the agent runs is the zone of the next call", files: [][]byte{pacific, tokyo}, wantHours: []int{4, 21}},
		{name: "TZ set means time.Local, which Go already read from TZ", tz: ptr("Asia/Tokyo"), files: [][]byte{pacific}, wantHours: []int{-1}},
		{name: "TZ set but empty means time.Local, which is UTC", tz: ptr(""), files: [][]byte{pacific}, wantHours: []int{-1}},
		{name: "an /etc/localtime that can't be read gives time.Local", files: [][]byte{nil}, wantHours: []int{-1}},
		{name: "an /etc/localtime that isn't TZif data gives time.Local", files: [][]byte{[]byte("not a zone")}, wantHours: []int{-1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lookupEnv := func(k string) (string, bool) {
				if k != "TZ" || tt.tz == nil {
					return "", false
				}
				return *tt.tz, true
			}
			for i, data := range tt.files {
				readFile := func(name string) ([]byte, error) {
					if name != "/etc/localtime" {
						t.Errorf("read %s, want /etc/localtime", name)
					}
					if data == nil {
						return nil, &fs.PathError{Op: "open", Path: name, Err: errors.ErrUnsupported}
					}
					return data, nil
				}
				got := zoneFrom(lookupEnv, readFile)
				switch want := tt.wantHours[i]; {
				case want < 0 && got != time.Local:
					t.Errorf("call %d: zoneFrom() = %v, want time.Local", i, got)
				case want >= 0 && noon.In(got).Hour() != want:
					t.Errorf("call %d: noon UTC is %s in zoneFrom()'s zone, want %02d:00", i, noon.In(got).Format("15:04 MST"), want)
				}
			}
		})
	}
}

func ptr(s string) *string { return &s }
