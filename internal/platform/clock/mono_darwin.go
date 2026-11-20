// SPDX-License-Identifier: AGPL-3.0-or-later

package clock

import (
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

// monoNow reads CLOCK_MONOTONIC, which on macOS goes on counting while the
// Mac sleeps (clock_gettime(3)), and the UUID the kernel makes for each
// boot, kern.bootsessionuuid. A kernel without that sysctl names the boot
// by kern.boottime instead, as hex microseconds.
func monoNow() (int64, string) {
	boot, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil || boot == "" {
		tv, terr := unix.SysctlTimeval("kern.boottime")
		if terr != nil {
			return 0, ""
		}
		boot = "m" + strconv.FormatInt(tv.Nano()/int64(time.Microsecond), 16)
	}
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts); err != nil {
		return 0, ""
	}
	return ts.Nano() / int64(time.Millisecond), boot
}
