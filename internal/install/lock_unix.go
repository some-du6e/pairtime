// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package install

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func lockFile(name string) (func() error, error) {
	f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE, newFileMode)
	if err != nil {
		return nil, err
	}
	// An flock belongs to the open file, so a second plan in this process
	// conflicts too.
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, &os.PathError{Op: "flock", Path: name, Err: err}
	}
	// Closing the file drops the lock. The method value keeps f reachable,
	// so the garbage collector can't close it, and drop the lock, early.
	return f.Close, nil
}
