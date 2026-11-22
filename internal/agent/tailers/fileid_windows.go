// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package tailers

import (
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
