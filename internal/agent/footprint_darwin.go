// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"syscall"
	"time"
)

// selfUsage reads the agent's peak resident set and its CPU time from
// getrusage, whose ru_maxrss macOS gives in bytes: its own CPU time, and its
// children's, those it started and waited for, as exec.Cmd waits for T3
// Code's reader's sqlite3 and the git adapter's git. The peak is the
// agent's alone. macOS has no call for the resident set now short of
// Mach's task_info, so that stays unknown.
func selfUsage() (rss, peak uint64, cpu time.Duration) {
	var ru, kids syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) != nil {
		return 0, 0, 0
	}
	cpu = cpuOf(time.Duration(ru.Utime.Nano()), time.Duration(ru.Stime.Nano()))
	if syscall.Getrusage(syscall.RUSAGE_CHILDREN, &kids) == nil {
		cpu += cpuOf(time.Duration(kids.Utime.Nano()), time.Duration(kids.Stime.Nano()))
	}
	return 0, uint64(ru.Maxrss), cpu
}

// joinJob has nothing to do outside Windows: getrusage counts the
// children's CPU time (selfUsage).
func joinJob() {}
