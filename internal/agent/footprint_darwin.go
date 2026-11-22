// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"syscall"
	"time"
)

// selfUsage reads the agent's peak resident set and its CPU time from
// getrusage, whose ru_maxrss macOS gives in bytes. macOS has no call for
// the resident set now short of Mach's task_info, so that stays unknown.
func selfUsage() (rss, peak uint64, cpu time.Duration) {
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) != nil {
		return 0, 0, 0
	}
	return 0, uint64(ru.Maxrss), cpuOf(time.Duration(ru.Utime.Nano()), time.Duration(ru.Stime.Nano()))
}
