// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package home

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// tryLockFile takes LockFile's lock on name at once, or returns ErrBusy.
func tryLockFile(name string) (func() error, error) {
	// A lock on a file uninstall deleted meanwhile would be one no other
	// process finds, so the lock is taken again until it is on the file at
	// name, as acquire does.
	for range 10 {
		f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE, 0o600)
		if err != nil {
			return nil, err
		}
		// An flock belongs to the open file, so a second LockFile in this
		// process conflicts as well. Go opens files close-on-exec, so a
		// child process never inherits the lock.
		if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
			f.Close()
			if errors.Is(err, unix.EWOULDBLOCK) {
				return nil, ErrBusy
			}
			return nil, &os.PathError{Op: "flock", Path: name, Err: err}
		}
		if isAt(f, name) {
			// Closing the file drops the lock. The method value keeps f
			// reachable, so the garbage collector can't close it, and drop
			// the lock, early.
			return f.Close, nil
		}
		f.Close()
	}
	return nil, &os.PathError{Op: "flock", Path: name, Err: errors.New("the lock file keeps being deleted")}
}
