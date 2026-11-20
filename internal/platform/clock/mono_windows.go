// SPDX-License-Identifier: AGPL-3.0-or-later

package clock

import (
	"strconv"
	"time"
	"unsafe"

	win "golang.org/x/sys/windows"
)

// timeOfDay is SYSTEM_TIMEOFDAY_INFORMATION, which
// NtQuerySystemInformation fills in. Times are FILETIME counts.
type timeOfDay struct {
	BootTime     int64 // when the boot began, moved by every step of the clock since
	CurrentTime  int64
	TimeZoneBias int64
	TimeZoneID   uint32
	Reserved     uint32
	// BootTimeBias is the sum of the clock's steps since the boot, which
	// BootTime has been moved by.
	BootTimeBias  uint64
	SleepTimeBias uint64
}

// monoNow reads GetTickCount64, the milliseconds since the boot, which
// count the time the machine slept, and names the boot by when it began.
// Windows moves the boot time it keeps whenever the wall clock is set, and
// adds each step to BootTimeBias, so BootTime less BootTimeBias stays put
// for the whole boot.
func monoNow() (int64, string) {
	var info timeOfDay
	var n uint32
	err := win.NtQuerySystemInformation(win.SystemTimeOfDayInformation, unsafe.Pointer(&info), uint32(unsafe.Sizeof(info)), &n)
	if err != nil || info.BootTime <= 0 {
		return 0, ""
	}
	boot := uint64(info.BootTime) - info.BootTimeBias
	ms := int64(win.DurationSinceBoot() / time.Millisecond)
	return ms, "w" + strconv.FormatUint(boot, 16)
}
