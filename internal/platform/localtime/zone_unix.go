// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package localtime

import (
	"os"
	"time"
)

// Zone returns the time zone the computer is set to now: time.Local when
// $TZ is set, since a running process's environment doesn't change and
// time.Local already follows $TZ, or else what /etc/localtime says at this
// moment. Linux (timedatectl, GNOME's automatic time zone) and macOS
// replace /etc/localtime when the zone changes, which is how Go finds the
// zone at start (go1.26 time/zoneinfo_unix.go). When the file can't be
// read or parsed, Zone returns time.Local.
func Zone() *time.Location {
	return zoneFrom(os.LookupEnv, os.ReadFile)
}

func zoneFrom(lookupEnv func(string) (string, bool), readFile func(string) ([]byte, error)) *time.Location {
	if _, ok := lookupEnv("TZ"); ok {
		return time.Local
	}
	data, err := readFile("/etc/localtime")
	if err != nil {
		return time.Local
	}
	loc, err := time.LoadLocationFromTZData("Local", data)
	if err != nil {
		return time.Local
	}
	return loc
}
