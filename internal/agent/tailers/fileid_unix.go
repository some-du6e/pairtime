// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

package tailers

import (
	"os"
	"strconv"
	"syscall"
)

// fileID names the file fh has open by its device and inode, which a
// rename keeps and a new file at the same path doesn't (CP 3.2). It is ""
// where the OS doesn't say.
func fileID(_ *os.File, fi os.FileInfo) string {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return strconv.FormatUint(uint64(st.Dev), 16) + ":" + strconv.FormatUint(uint64(st.Ino), 16)
}
