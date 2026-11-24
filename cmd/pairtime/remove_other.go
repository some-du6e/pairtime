// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

package main

import (
	"errors"
	"os"
)

// canRemoveRunning is true: a program that runs keeps its file open, and
// deleting the file's name doesn't stop it.
const canRemoveRunning = true

// removesHeldFiles is true: deleting a file that another process has open,
// such as a lock file whose lock it holds, works, so a lock file goes while
// its lock is still held (removeAgentLock).
const removesHeldFiles = true

// removeBinary deletes the binary at path.
func removeBinary(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// removeAfterExit isn't needed off Windows, where removeBinary deletes a
// running binary.
func removeAfterExit(_, _, _ []string) error {
	return errors.New("only Windows keeps a program from deleting itself")
}

// commitBinary renames tmp, the new binary, over dst. A process that runs
// dst goes on running the file it started from.
func commitBinary(tmp, dst string) error { return os.Rename(tmp, dst) }

// oldBinaries returns the copies of the binary that an install moved aside
// in dir, which only Windows needs (commitBinary there).
func oldBinaries(string) []string { return nil }

// placing reports whether an install still running may need p, a copy of
// the binary it moved aside, to put back, which only Windows has.
func placing(string) bool { return false }

// removeOldBinaries deletes the copies of the binary that an install moved
// aside in dir, which only Windows needs.
func removeOldBinaries(string) {}
