// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package localtime

import (
	"os"
	"strings"
	"time"
)

// Zone returns the time zone the computer is set to now, read from the
// file that gives it at this moment: /etc/localtime, or the file $TZ
// names when it is a path, as in TZ=:/etc/localtime. Linux (timedatectl,
// GNOME's automatic time zone) and macOS replace /etc/localtime when the
// zone changes, and Go reads it, or $TZ's file, only once (go1.26
// time/zoneinfo_unix.go). When $TZ names a zone, such as Asia/Tokyo, or
// is empty, which means UTC, Zone returns time.Local, which already
// follows it: a running process's environment doesn't change. So does a
// file that can't be read or parsed.
func Zone() *time.Location {
	return zoneFrom(os.LookupEnv, os.ReadFile)
}

func zoneFrom(lookupEnv func(string) (string, bool), readFile func(string) ([]byte, error)) *time.Location {
	name := "/etc/localtime"
	if tz, ok := lookupEnv("TZ"); ok {
		// Go's own reading of $TZ: an optional ':', then a path or a name.
		tz = strings.TrimPrefix(tz, ":")
		if !strings.HasPrefix(tz, "/") {
			return time.Local
		}
		name = tz
	}
	data, err := readFile(name)
	if err != nil {
		return time.Local
	}
	loc, err := time.LoadLocationFromTZData("Local", data)
	if err != nil {
		return time.Local
	}
	return loc
}
