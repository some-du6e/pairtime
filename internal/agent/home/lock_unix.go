// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package home

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func acquire(dir string) (func() error, error) {
	name := filepath.Join(dir, lockFile)
	// A command that tidies up deletes the lock file only while it holds
	// the lock (pairtime install's tidy and uninstall), so another process
	// can open the file just before that, lock it just after, and hold a
	// lock on a file no one else can find. The lock is taken again until
	// the file it is on is the one at name.
	for range 10 {
		f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE, 0o600)
		if err != nil {
			return nil, err
		}
		// An flock belongs to the open file, so a second Acquire in this
		// process conflicts as well. Go opens files close-on-exec, so a
		// child process never inherits the lock.
		if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
			f.Close()
			if errors.Is(err, unix.EWOULDBLOCK) {
				return nil, ErrLocked
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
