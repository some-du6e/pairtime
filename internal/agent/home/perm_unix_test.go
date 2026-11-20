// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package home

import (
	"os"
	"testing"
)

// checkOwnerOnly checks that a written file has mode 0600. WriteFile and
// WritePrivate both give it that mode where the OS has modes.
func checkOwnerOnly(t *testing.T, name string, _ bool) {
	t.Helper()
	fi, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("%s has mode %v, want -rw-------", name, perm)
	}
}
