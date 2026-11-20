// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package home

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// tryLock takes an exclusive flock on f, or returns ErrLocked at once when
// another open file holds one.
func tryLock(f *os.File) error {
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) {
			return ErrLocked
		}
		return &os.PathError{Op: "flock", Path: f.Name(), Err: err}
	}
	return nil
}

// unlock does nothing, because closing the file drops its flock.
func unlock(*os.File) error { return nil }
