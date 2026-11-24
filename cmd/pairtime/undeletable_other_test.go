// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// undeletable keeps the file at path from being deleted until the test
// ends: its folder can't be written, so nothing in it can be unlinked. It
// skips the test for root, whom a folder's mode keeps out of nothing.
func undeletable(t *testing.T, path string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("a folder's mode keeps root out of nothing, so this test can't make a file stay")
	}
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	// Registered after t.TempDir's cleanup, so it runs first and the
	// folder can be deleted.
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
}
