// SPDX-License-Identifier: AGPL-3.0-or-later

package home

import "errors"

// ErrLocked means another process, or another Acquire in this one, holds
// the lock.
var ErrLocked = errors.New("the data directory is locked by another agent")

// Lock is the single-instance lock on a data directory: a named mutex on
// Windows and an flock on the file agent.lock elsewhere (DESIGN 3.2; CP
// 2.1). The login item, a hook that finds the agent down and a teen
// double-clicking the binary can all start an agent at once, and only one
// may run. The OS drops the lock when its holder exits, crashes or is
// killed, so a dead agent never blocks the next one.
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
