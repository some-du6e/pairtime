// SPDX-License-Identifier: AGPL-3.0-or-later

package home

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// allBytes, as both halves of the length, makes the lock cover every byte
// a file can have, from offset 0, as Go's own cmd/go locks its files. No
// one reads agent.lock, so it doesn't matter that the lock keeps readers
// out of it too.
const allBytes = ^uint32(0)

// tryLock takes an exclusive LockFileEx lock on f, or returns ErrLocked at
// once when another open file holds one. The file is opened for
// synchronous I/O, so LockFileEx answers straight away.
func tryLock(f *os.File) error {
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, allBytes, allBytes, new(windows.Overlapped))
	switch {
	case err == nil:
		return nil
	case errors.Is(err, windows.ERROR_LOCK_VIOLATION):
		return ErrLocked
	default:
		return &os.PathError{Op: "LockFileEx", Path: f.Name(), Err: err}
	}
}

// unlock drops f's lock before f is closed. Windows drops a closed file's
// locks as well, but LockFileEx's documentation says how soon depends on
// the system's resources and recommends unlocking explicitly, so the next
// agent finds the lock free at once.
func unlock(f *os.File) error {
	if err := windows.UnlockFileEx(windows.Handle(f.Fd()), 0, allBytes, allBytes, new(windows.Overlapped)); err != nil {
		return &os.PathError{Op: "UnlockFileEx", Path: f.Name(), Err: err}
	}
	return nil
}
