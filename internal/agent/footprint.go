// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import "time"

// footprintReply is what the agent costs the computer so far, as the OS
// reports it for this process: the R8 numbers `pairtime doctor --report`
// shares (DESIGN 5.9, 9.7; TE WP7.9). footprint.yml measures the same
// working set from outside the process, on Windows only.
type footprintReply struct {
	// RSSBytes is the resident set now, the working set on Windows, and
	// PeakRSSBytes the most it has been. Either is absent where the OS
	// doesn't say: macOS gives the peak alone.
	RSSBytes     uint64 `json:"rss_bytes,omitempty"`
	PeakRSSBytes uint64 `json:"peak_rss_bytes,omitempty"`
	// CPUS is the CPU time the agent has used since it started, user and
	// system, in seconds, with that of the processes it started: T3 Code's
	// reader's sqlite3 runs and the git adapter's git, which cost the
	// computer for the agent (selfUsage).
	CPUS float64 `json:"cpu_s"`
}

// footprintNow asks the OS what the agent costs (selfUsage).
func footprintNow() footprintReply {
	rss, peak, cpu := selfUsage()
	return footprintReply{RSSBytes: rss, PeakRSSBytes: peak, CPUS: cpu.Seconds()}
}

// cpuOf adds user and system time.
func cpuOf(user, system time.Duration) time.Duration { return user + system }
