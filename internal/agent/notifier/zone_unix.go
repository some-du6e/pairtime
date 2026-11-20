// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package notifier

import (
	"os"
	"strings"
	"time"
)

// localZoneFile is where Linux and macOS keep the computer's time zone, in
// the tzfile format. Changing the zone replaces it: timedatectl does, and so
// does macOS's automatic time zone when the Mac travels.
const localZoneFile = "/etc/localtime"

// currentZone is the computer's time zone as it is now.
func currentZone() *time.Location {
	return zoneFrom(os.LookupEnv, localZoneFile)
}

// zoneFrom reads the time zone the way Go reads time.Local, but again on
// every call. When TZ names a zone, or is "" for UTC, the zone is
// time.Local: Go read TZ when the process started, and a running process's
// environment doesn't change. When TZ names a file, as ":/etc/localtime"
// does in setups that spare the C library a check of the file, or when TZ
// is unset, the zone is the one in that file, or in localFile, as the file
// is now. A file that can't be read or parsed leaves time.Local.
func zoneFrom(lookupEnv func(string) (string, bool), localFile string) *time.Location {
	name := localFile
	if tz, ok := lookupEnv("TZ"); ok {
		tz = strings.TrimPrefix(tz, ":")
		if !strings.HasPrefix(tz, "/") {
			return time.Local
		}
		name = tz
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return time.Local
	}
	loc, err := time.LoadLocationFromTZData("Local", data)
	if err != nil {
		return time.Local
	}
	return loc
}
