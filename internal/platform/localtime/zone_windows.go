// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package localtime

import (
	"time"

	"golang.org/x/sys/windows"
)

// Zone returns the time zone the computer is set to now, as
// GetTimeZoneInformation reports it at this moment: a fixed offset, the
// zone's bias plus its standard or daylight bias, whichever is in force.
// Windows changes the answer as soon as the zone changes, by hand or by
// "Set time zone automatically". The offset is right for the time now,
// which is all a caller of Now reads. When the call fails, Zone returns
// time.Local.
func Zone() *time.Location {
	var tzi windows.Timezoneinformation
	rc, err := windows.GetTimeZoneInformation(&tzi)
	if err != nil {
		return time.Local
	}
	return time.FixedZone("", offset(&tzi, rc))
}

// offset returns the offset east of UTC, in seconds, of the zone tzi
// describes, given GetTimeZoneInformation's result rc. Windows counts its
// biases in minutes west of UTC: UTC = local time + bias.
func offset(tzi *windows.Timezoneinformation, rc uint32) int {
	bias := tzi.Bias
	switch rc {
	case windows.TIME_ZONE_ID_STANDARD:
		bias += tzi.StandardBias
	case windows.TIME_ZONE_ID_DAYLIGHT:
		bias += tzi.DaylightBias
	}
	return -int(bias) * 60
}
