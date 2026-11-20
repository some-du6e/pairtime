// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

package inbox

// busy reports whether err means another process holds the file for a
// moment. Only Windows shuts an open out that way.
func busy(error) bool { return false }
