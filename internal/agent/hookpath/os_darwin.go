// SPDX-License-Identifier: AGPL-3.0-or-later

package hookpath

import (
	"time"

	"golang.org/x/sys/unix"

	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/darwin"
)

func readOS() ([]platform.Proc, error) { return darwin.Processes() }

// monoNow reads CLOCK_MONOTONIC, which on macOS goes on counting while the
// Mac sleeps (clock_gettime(3)), and the UUID the kernel makes for each
// boot.
func monoNow() (int64, string) {
	boot, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil {
		return 0, ""
	}
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts); err != nil {
		return 0, ""
	}
	return ts.Nano() / int64(time.Millisecond), boot
}
