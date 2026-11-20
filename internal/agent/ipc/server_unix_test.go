// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package ipc

import (
	"os"
	"testing"
)

// otherUser is a user this test doesn't run as.
func otherUser(t *testing.T) identity {
	t.Helper()
	return identity{uid: os.Geteuid() + 1}
}

// checkPrivateDir checks that dir has mode 0700.
func checkPrivateDir(t *testing.T, dir string) {
	t.Helper()
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o700 {
		t.Errorf("%s has mode %v, want drwx------", dir, perm)
	}
}
