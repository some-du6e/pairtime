// SPDX-License-Identifier: AGPL-3.0-or-later

package home

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// createTemp makes the temporary file. A private one is created with its
// DACL already in place, so the secret is never in a file that others can
// read, even for a moment.
func createTemp(dir, prefix string, private bool) (*os.File, error) {
	if !private {
		return os.CreateTemp(dir, prefix+"*")
	}
	sa, err := userOnly()
	if err != nil {
		return nil, err
	}
	for range 10 {
		name := filepath.Join(dir, prefix+rand.Text())
		p, err := windows.UTF16PtrFromString(name)
		if err != nil {
			return nil, err
		}
		h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, sa,
			windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if errors.Is(err, windows.ERROR_FILE_EXISTS) {
			continue
		}
		if err != nil {
			return nil, &os.PathError{Op: "CreateFile", Path: name, Err: err}
		}
		return os.NewFile(uintptr(h), name), nil
	}
	return nil, &os.PathError{Op: "CreateFile", Path: dir, Err: windows.ERROR_FILE_EXISTS}
}

// userOnly returns security attributes with a DACL that grants the current
// user full access and nobody else any, protected from inheriting the
// directory's entries.
var userOnly = sync.OnceValues(func() (*windows.SecurityAttributes, error) {
	return userOnlyAttributes("")
})

// userOnlyDir is userOnly for a directory (mkdirAll). Its entry is marked
// for inheritance by files (OI) and directories (CI), so everything made
// inside gets the same one entry and nothing else.
var userOnlyDir = sync.OnceValues(func() (*windows.SecurityAttributes, error) {
	return userOnlyAttributes("OICI")
})

// userOnlyAttributes builds the security attributes of userOnly and
// userOnlyDir, whose one entry has the SDDL ACE flags inherit.
func userOnlyAttributes(inherit string) (*windows.SecurityAttributes, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;" + inherit + ";FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return nil, err
	}
	sa := &windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(*sa))
	return sa, nil
}

// rename retries for a moment while another process, such as a virus
// scanner or the search indexer, has the target open, which makes
// MoveFileEx fail with access denied or a sharing violation.
func rename(from, to string) error {
	var err error
	for i := range 10 {
		if err = os.Rename(from, to); err == nil || !retryable(err) {
			return err
		}
		time.Sleep(time.Duration(i+1) * 10 * time.Millisecond)
	}
	return err
}

func retryable(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}

// syncDir does nothing on Windows, which has no portable way to flush a
// directory. NTFS journals the rename.
func syncDir(string) {}
