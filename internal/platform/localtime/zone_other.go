// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !unix && !windows

package localtime

import "time"

// Zone returns time.Local on an OS pairtime doesn't ship for.
func Zone() *time.Location {
	return time.Local
}
