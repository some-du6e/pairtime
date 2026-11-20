// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrBusy means another install or uninstall holds the install lock: one
// in another process, such as the install script and the VS Code extension
// both running `pairtime install` (DESIGN 5.9), or another plan in this one
// that wasn't applied or closed.
var ErrBusy = errors.New("another pairtime install or uninstall is running; let it finish, then try again")

// lockPath is the install lock's file. It sits beside the install
// directory, in pairtime's data directory, so that uninstall can still take
// the install directory away, and it stays there, as the agent's lock file
// does: deleting a lock file races with the next process that opens it.
func (in *Installer) lockPath() string {
	return filepath.Join(filepath.Dir(in.Dir), "install.lock")
}

// lock takes the install lock, an flock on Unix and a LockFileEx lock on
// Windows, and returns the function that drops it. It returns ErrBusy at
// once, without waiting, when the lock is held. The OS drops the lock when
// its holder exits, crashes or is killed.
func (in *Installer) lock() (func() error, error) {
	if err := os.MkdirAll(filepath.Dir(in.lockPath()), newDirMode); err != nil {
		return nil, err
	}
	return lockFile(in.lockPath())
}
