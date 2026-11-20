// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux || darwin

package backup

import (
	"fmt"
	"syscall"
)

// DiskSpace returns the bytes an unprivileged user may still write on the
// file system that holds path, and its size; ok is false where it can't
// tell.
func DiskSpace(path string) (free, total uint64, ok bool, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, false, fmt.Errorf("backup: %s: %w", path, err)
	}
	return uint64(st.Bavail) * uint64(st.Bsize), uint64(st.Blocks) * uint64(st.Bsize), true, nil
}
