// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package home

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// lockFile is the file in the data directory whose flock is the lock. It
// stays on disk after Release: deleting a lock file races with the next
// process that opens it.
const lockFile = "agent.lock"

func acquire(dir string) (func() error, error) {
	name := filepath.Join(dir, lockFile)
	f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	// An flock belongs to the open file, so a second Acquire in this
	// process conflicts as well. Go opens files close-on-exec, so a child
	// process never inherits the lock.
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, &os.PathError{Op: "flock", Path: name, Err: err}
	}
	// Closing the file drops the lock. The method value keeps f reachable,
	// so the garbage collector can't close it, and drop the lock, early.
	return f.Close, nil
}
