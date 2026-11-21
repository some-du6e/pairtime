// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// allBytes, as both halves of the length, makes the lock cover every byte
// a file can have, as internal/agent/home locks agent.lock.
const allBytes = ^uint32(0)

func lockFile(name string) (func() error, error) {
	f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE, newFileMode)
	if err != nil {
		return nil, err
	}
	// The lock belongs to the open file, so a second plan in this process
	// conflicts too.
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
	// lock free at once, which closing alone doesn't promise.
	return func() error {
		err := windows.UnlockFileEx(windows.Handle(f.Fd()), 0, allBytes, allBytes, new(windows.Overlapped))
		if err != nil {
			err = &os.PathError{Op: "UnlockFileEx", Path: name, Err: err}
		}
		return errors.Join(err, f.Close())
	}, nil
}

// removeLocked drops the lock unlock holds, then deletes its file, name.
// Windows doesn't delete a file that a process has open, the lock's holder
// included, so the delete has to wait for the unlock, and it fails, which
// leaves the file, when another install or uninstall has taken the lock
// in between.
func removeLocked(name string, unlock func() error) error {
	if err := unlock(); err != nil {
		return err
	}
	return os.Remove(name)
}
