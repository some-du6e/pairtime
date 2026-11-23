// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package tailers

import (
	"errors"
	"io/fs"
	"os"
	"strconv"
	"syscall"
)

// fileID names the file fh has open by its volume's serial number and its
// file index, which a rename keeps and a new file at the same path doesn't
// (CP 3.2). os.Stat's FileInfo doesn't carry them on Windows, so it asks
// the open handle. It is "" when Windows won't say.
func fileID(fh *os.File, _ os.FileInfo) string {
	var d syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(fh.Fd()), &d); err != nil {
		return ""
	}
	return strconv.FormatUint(uint64(d.VolumeSerialNumber), 16) + ":" +
		strconv.FormatUint(uint64(d.FileIndexHigh)<<32|uint64(d.FileIndexLow), 16)
}

// openLog opens a log file to read it, sharing it for reading, writing and
// deletion, so the harness can delete or rename its file while the tailer
// has it open: os.Open shares no deletion. It keeps only a file on disk.
func openLog(path string) (*os.File, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	if kind, err := syscall.GetFileType(h); err != nil || kind != syscall.FILE_TYPE_DISK {
		syscall.CloseHandle(h)
		return nil, &fs.PathError{Op: "open", Path: path, Err: errors.New("not a file on disk")}
	}
	return os.NewFile(uintptr(h), path), nil
}
