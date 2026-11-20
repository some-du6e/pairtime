// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package notifier

import (
	"time"

	"golang.org/x/sys/windows"
)

// currentZone is the computer's time zone as Windows has it now, read again
// on every call: Windows changes it when the user picks another zone, or
// when "Set time zone automatically" finds the laptop somewhere else. It is
// a fixed zone at the offset in force now, because quiet hours only need
// the clock time now, so the zone's daylight-saving rules needn't be
// copied.
func currentZone() *time.Location {
	var tzi windows.Timezoneinformation
	id, err := windows.GetTimeZoneInformation(&tzi)
	if err != nil {
		return time.Local
	}
	return zoneFromTZI(id, &tzi)
}

// zoneFromTZI is the fixed zone at the offset that GetTimeZoneInformation's
// answer puts in force: Bias, plus StandardBias in standard time or
// DaylightBias in daylight time, all in minutes west of UTC. A zone with no
// daylight saving, or with its automatic adjustment off, answers
// TIME_ZONE_ID_UNKNOWN, and Bias alone applies.
func zoneFromTZI(id uint32, tzi *windows.Timezoneinformation) *time.Location {
	bias := tzi.Bias
	switch id {
	case windows.TIME_ZONE_ID_STANDARD:
		bias += tzi.StandardBias
	case windows.TIME_ZONE_ID_DAYLIGHT:
		bias += tzi.DaylightBias
	}
	return time.FixedZone("Local", -int(bias)*60)
}
