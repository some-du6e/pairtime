// SPDX-License-Identifier: AGPL-3.0-or-later

package home

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// tryLockFile takes LockFile's lock on name at once, or returns ErrBusy.
func tryLockFile(name string) (func() error, error) {
	f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	// The lock belongs to the open file, so a second LockFile in this
	// process conflicts as well. Go opens files without handle
	// inheritance, so a child process never holds the lock, and for
	// synchronous I/O, so LockFileEx answers at once.
	err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, allBytes, allBytes, new(windows.Overlapped))
	if err != nil {
		f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrBusy
		}
		return nil, &os.PathError{Op: "LockFileEx", Path: name, Err: err}
	}
	// The closure keeps f reachable, so the garbage collector can't close
	// it, and drop the lock, early. Unlocking before the close makes the
	// lock free at once (unlockFile).
	return func() error { return errors.Join(unlockFile(f), f.Close()) }, nil
}
