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

// removeBinary deletes the binary at path.
func removeBinary(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// removeAfterExit isn't needed off Windows, where removeBinary deletes a
// running binary.
func removeAfterExit(string, []string) error {
	return errors.New("only Windows keeps a program from deleting itself")
}
