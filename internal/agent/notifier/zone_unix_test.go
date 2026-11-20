// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package notifier

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tzfile returns a version 1 tzfile (RFC 8536) for a zone that is always
// offset seconds east of UTC, as /etc/localtime holds one.
func tzfile(offset int32, abbr string) []byte {
	var b bytes.Buffer
	b.WriteString("TZif")
	b.WriteByte(0)            // version 1
	b.Write(make([]byte, 15)) // unused
	// isutcnt, isstdcnt, leapcnt, timecnt, typecnt, charcnt
	for _, n := range []uint32{0, 0, 0, 0, 1, uint32(len(abbr) + 1)} {
		binary.Write(&b, binary.BigEndian, n)
	}
	// The one local time type: its offset, isdst and abbreviation index.
	binary.Write(&b, binary.BigEndian, offset)
	b.WriteByte(0)
	b.WriteByte(0)
	b.WriteString(abbr)
	b.WriteByte(0)
	return b.Bytes()
}

// instant is when the zone tests read the clock: 03:30 UTC, which is 19:30
// in Los Angeles and 22:30 in New York in November.
var instant = time.Date(2026, 11, 20, 3, 30, 0, 0, time.UTC)

func offsetAt(loc *time.Location) int {
	_, off := instant.In(loc).Zone()
	return off
}

func TestZoneFrom(t *testing.T) {
	const (
		losAngeles = -8 * 3600
		india      = 5*3600 + 30*60
	)
	tests := []struct {
		name      string
		tz        string // TZ's value, with FILE for the zone file's path
		tzSet     bool
		file      []byte // the zone file; nil means there is none
		want      int    // the offset east of UTC, unless wantLocal
		wantLocal bool   // the zone is Go's own time.Local
	}{
		{name: "without TZ, the zone is the one in the zone file", file: tzfile(losAngeles, "PST"), want: losAngeles},
		{name: "TZ naming a file, as :/etc/localtime does, makes that file the zone", tz: ":FILE", tzSet: true, file: tzfile(india, "IST"), want: india},
		{name: "TZ naming a file without the colon makes that file the zone too", tz: "FILE", tzSet: true, file: tzfile(india, "IST"), want: india},
		{name: "TZ naming a zone keeps Go's own zone, which TZ set when the agent started", tz: "America/New_York", tzSet: true, file: tzfile(losAngeles, "PST"), wantLocal: true},
		{name: "an empty TZ keeps Go's own zone, which is then UTC", tz: "", tzSet: true, file: tzfile(losAngeles, "PST"), wantLocal: true},
		{name: "a zone file that is missing leaves Go's own zone", wantLocal: true},
		{name: "a zone file that isn't a tzfile leaves Go's own zone", file: []byte("not a zone"), wantLocal: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "localtime")
			if tt.file != nil {
				if err := os.WriteFile(file, tt.file, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			lookupEnv := func(key string) (string, bool) {
				if key != "TZ" || !tt.tzSet {
					return "", false
				}
				return strings.ReplaceAll(tt.tz, "FILE", file), true
			}
			// With TZ naming a file, the default zone file must not count.
			defaultFile := file
			if tt.tzSet {
				defaultFile = filepath.Join(t.TempDir(), "missing")
			}
			got := zoneFrom(lookupEnv, defaultFile)
			switch {
			case tt.wantLocal && got != time.Local:
				t.Errorf("zoneFrom() = %v, want time.Local", got)
			case !tt.wantLocal && offsetAt(got) != tt.want:
				t.Errorf("zoneFrom() is %d s east of UTC, want %d", offsetAt(got), tt.want)
			}
		})
	}
}

// The agent runs for days, so a time-zone change while it runs must reach
// quiet hours at the next note (#70).
func TestQuietHoursFollowAZoneChangeWhileTheAgentRuns(t *testing.T) {
	tests := []struct {
		name    string
		before  int32 // the zone file's offset when the agent started
		after   int32 // its offset after the change
		quiet   string
		wantWas bool // quiet hours at instant, before the change
		wantNow bool // and after it
	}{
		{
			name:    "a laptop that moves from Los Angeles to New York is in its 22:00-07:00 quiet hours at 22:30 New York time",
			before:  -8 * 3600,
			after:   -5 * 3600,
			quiet:   "22:00-07:00",
			wantWas: false,
			wantNow: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "localtime")
			unset := func(string) (string, bool) { return "", false }
			q := mustQuiet(tt.quiet)
			for i, c := range []struct {
				offset int32
				want   bool
			}{{tt.before, tt.wantWas}, {tt.after, tt.wantNow}} {
				if err := os.WriteFile(file, tzfile(c.offset, "X"), 0o600); err != nil {
					t.Fatal(err)
				}
				when := instant.In(zoneFrom(unset, file))
				if got := q.Contains(when); got != c.want {
					t.Errorf("read %d: quiet hours %s contain %s = %v, want %v", i+1, q, when.Format("15:04 -0700"), got, c.want)
				}
			}
		})
	}
}
