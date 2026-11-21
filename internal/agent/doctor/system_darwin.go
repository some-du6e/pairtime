// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import "golang.org/x/sys/unix"

// OSBuild is macOS's version and build, such as "macOS 15.1 (24B83)".
func OSBuild() string {
	ver, err := unix.Sysctl("kern.osproductversion")
	if err != nil {
		return "macOS"
	}
	if build, err := unix.Sysctl("kern.osversion"); err == nil && build != "" {
		return "macOS " + ver + " (" + build + ")"
	}
	return "macOS " + ver
}

// DiskKind is "unknown" on macOS: whether a disk spins is in IOKit's
// registry, which pairtime doesn't read yet.
func DiskKind(string) string { return "unknown" }
