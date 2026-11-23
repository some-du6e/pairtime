// SPDX-License-Identifier: AGPL-3.0-or-later

package home

import (
	"errors"
	"fmt"
	"time"
)

// ErrBusy means another process, or another LockFile in this one, held a
// file's lock for longer than LockFile waits.
var ErrBusy = errors.New("another pairtime process is changing the same file")

// LockFile takes an exclusive lock on the file at name, which it creates,
// and returns the function that drops it: an flock on Unix and a
// LockFileEx lock on Windows, as Acquire takes for the agent. A process
// that changes a file other processes change too, as config.toml, holds
// the lock from its read to its rename, so neither change undoes the
// other. It waits up to wait for another holder to drop the lock, trying
// again every 10 ms, and then returns ErrBusy. The file stays after the
// release, since deleting a lock file races with the next process that
// opens it, and the OS drops the lock when its holder exits, crashes or is
// killed.
func LockFile(name string, wait time.Duration) (func() error, error) {
	deadline := time.Now().Add(wait)
	for {
		release, err := tryLockFile(name)
		if !errors.Is(err, ErrBusy) {
			return release, err
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%w: %s", ErrBusy, name)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
