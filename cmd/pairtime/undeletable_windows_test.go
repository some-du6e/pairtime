// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import "testing"

// undeletable keeps the file at path from being deleted until the test
// ends, as a program that has it open does on Windows (holdOpen).
func undeletable(t *testing.T, path string) {
	t.Helper()
	holdOpen(t, path)
}
