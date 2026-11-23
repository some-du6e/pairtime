// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

package tailers

import (
	"errors"
	"io/fs"
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

// openLog opens a log file to read it. It opens without blocking and keeps
// only a regular file, so a FIFO or a device where a log file was looked
// for, as a hook call's transcript_path can name, can't hold the tailer up.
func openLog(path string) (*os.File, error) {
	fh, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	fi, err := fh.Stat()
	if err == nil && !fi.Mode().IsRegular() {
		err = &fs.PathError{Op: "open", Path: path, Err: errors.New("not a regular file")}
	}
	if err != nil {
		fh.Close()
		return nil, err
	}
	return fh, nil
}
