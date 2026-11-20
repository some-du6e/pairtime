// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !unix && !windows

package notifier

import "time"

// currentZone is Go's time.Local on an OS pairtime doesn't ship for.
func currentZone() *time.Location {
	return time.Local
}
