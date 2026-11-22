// SPDX-License-Identifier: AGPL-3.0-or-later

// The lowest CPU and I/O priority, for the lookback reader (WP-AD3).

package darwin

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// From sys/resource.h.
const (
	prioDarwinProcess = 4      // PRIO_DARWIN_PROCESS
	prioDarwinBG      = 0x1000 // PRIO_DARWIN_BG
)

// Background puts the calling process in the background state:
// setpriority(PRIO_DARWIN_PROCESS, 0, PRIO_DARWIN_BG), with which macOS
// runs every thread of the process at the lowest CPU priority and
// throttles its disk and network I/O (setpriority(2)). The lookback reader
// runs so, since it reads 30 days of transcripts on what may be an old
// laptop's disk (DESIGN 5.9 step 9; TE 4.7).
func Background() error {
	if err := unix.Setpriority(prioDarwinProcess, 0, prioDarwinBG); err != nil {
		return fmt.Errorf("setpriority(PRIO_DARWIN_PROCESS, PRIO_DARWIN_BG): %w", err)
	}
	return nil
}

// InBackground reports whether the calling process is in the background
// state, as getpriority(PRIO_DARWIN_PROCESS, 0) reads it back.
func InBackground() (bool, error) {
	v, err := unix.Getpriority(prioDarwinProcess, 0)
	if err != nil {
		return false, fmt.Errorf("getpriority(PRIO_DARWIN_PROCESS): %w", err)
	}
	return v != 0, nil
}
