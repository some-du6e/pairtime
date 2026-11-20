// SPDX-License-Identifier: AGPL-3.0-or-later

package home

import "errors"

// ErrLocked means another process, or another Acquire in this one, holds
// the lock.
var ErrLocked = errors.New("the data directory is locked by another agent")

// lockFile is the file in the data directory whose lock is the agent's. It
// stays on disk after Release: deleting a lock file races with the next
// process that opens it.
const lockFile = "agent.lock"

// Lock is the single-instance lock on a data directory: an exclusive lock
// on the file agent.lock in it, an flock on Unix and a LockFileEx lock on
// Windows, where the agent also holds a named mutex (DESIGN 3.2; CP 2.1).
// The login item, a hook that finds the agent down and a teen
// double-clicking the binary can all start an agent at once, and only one
// may run. Because the lock is on the file, it keeps out every other
// process that uses the directory, whatever Windows session it runs in and
// however it spells the path (#46). The OS drops the lock when its holder
// exits, crashes or is killed, so a dead agent never blocks the next one.
type Lock struct {
	release func() error
}

// Acquire takes the lock on the data directory dir. It returns ErrLocked at
// once, without waiting, when the lock is held.
func Acquire(dir string) (*Lock, error) {
	release, err := acquire(dir)
	if err != nil {
		return nil, err
	}
	return &Lock{release: release}, nil
}

// Release drops the lock.
func (l *Lock) Release() error {
	return l.release()
}
