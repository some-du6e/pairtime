// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package home

import (
	"io/fs"
	"os"
	"testing"
)

// checkOwnerOnly checks that a written file has mode 0600. WriteFile and
// WritePrivate both give it that mode where the OS has modes.
func checkOwnerOnly(t *testing.T, name string, _ bool) {
	t.Helper()
	checkMode(t, name, 0o600)
}

// checkPrivateDir checks that a directory Create made has mode 0700.
func checkPrivateDir(t *testing.T, dir string) {
	t.Helper()
	checkMode(t, dir, 0o700)
}

// checkPrivateFile checks that a file has mode 0600.
func checkPrivateFile(t *testing.T, name string) {
	t.Helper()
	checkMode(t, name, 0o600)
}

func checkMode(t *testing.T, name string, want fs.FileMode) {
	t.Helper()
	fi, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != want {
		t.Errorf("%s has mode %v, want %v", name, perm, want)
	}
}

// permissions describes who may open path, to compare before and after.
func permissions(t *testing.T, path string) string {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm().String()
}
