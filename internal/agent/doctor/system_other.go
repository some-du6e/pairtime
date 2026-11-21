// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !linux && !darwin && !windows

package doctor

import "runtime"

// OSBuild names the OS alone, on the OSes pairtime doesn't ship for.
func OSBuild() string { return runtime.GOOS }

// DiskKind is "unread" on the OSes pairtime doesn't ship for.
func DiskKind(string) string { return "unread" }
