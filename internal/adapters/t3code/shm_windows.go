// SPDX-License-Identifier: AGPL-3.0-or-later

package t3code

import (
	"os"
	"syscall"
)

// openShared opens the file name to read it, shared with every other open,
// deletes included. os.Open shares no delete on Windows, so a T3 stopping
// while the reader held its -shm couldn't delete it, and would leave it in
// T3's folder.
func openShared(name string) (*os.File, error) {
	p, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	return os.NewFile(uintptr(h), name), nil
}
