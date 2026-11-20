// SPDX-License-Identifier: AGPL-3.0-or-later

package clock

import (
	"time"

	"golang.org/x/sys/unix"

	"github.com/some-du6e/pairtime/internal/platform/linux"
)

// monoNow reads CLOCK_BOOTTIME, which unlike CLOCK_MONOTONIC counts the
// time the machine was suspended, and the kernel's boot id.
func monoNow() (int64, string) {
	boot, err := linux.BootID()
	if err != nil {
		return 0, ""
	}
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_BOOTTIME, &ts); err != nil {
		return 0, ""
	}
	return ts.Nano() / int64(time.Millisecond), boot
}
