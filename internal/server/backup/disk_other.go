// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !(linux || darwin)

package backup

// DiskSpace can't tell free space on this system, where pairtimed only
// cross-compiles: a deployment runs it on Linux (DESIGN 10.1).
func DiskSpace(path string) (free, total uint64, ok bool, err error) {
	return 0, 0, false, nil
}
