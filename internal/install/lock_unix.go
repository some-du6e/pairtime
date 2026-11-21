// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package install

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func lockFile(name string) (func() error, error) {
	// RemoveLockFile deletes the file while it holds the lock, so another
	// process can open the file just before that, lock it just after, and
	// hold a lock on a file no one else can find. A lock is taken again
	// until the file it is on is the one at name.
	for range 10 {
		f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE, newFileMode)
		if err != nil {
			return nil, err
		}
		// An flock belongs to the open file, so a second plan in this
		// process conflicts too.
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

// isAt reports whether f is the file at name.
func isAt(f *os.File, name string) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	ni, err := os.Stat(name)
	return err == nil && os.SameFile(fi, ni)
}

// removeLocked deletes name, the lock file whose lock unlock drops, while
// the lock is still held, so that no other process holds it when it goes.
// Unix deletes a file that is open, and a process that opens the name
// afterwards makes a new one.
func removeLocked(name string, unlock func() error) error {
	err := os.Remove(name)
	return errors.Join(err, unlock())
}
