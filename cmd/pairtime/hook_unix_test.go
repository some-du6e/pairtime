// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package main

import (
	"os"
	"testing"
)

// checkUserOnlyDir checks that dir is a directory no one but its user may
// open.
func checkUserOnlyDir(t *testing.T, dir string) {
	t.Helper()
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.IsDir() || fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("%s has mode %v, want a directory only its user may open", dir, fi.Mode())
	}
}
