// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !linux && !darwin && !windows

package agent

import "time"

// selfUsage says nothing on the OSes pairtime doesn't ship for.
func selfUsage() (rss, peak uint64, cpu time.Duration) { return 0, 0, 0 }

// joinJob has nothing to do on the OSes pairtime doesn't ship for.
func joinJob() {}
