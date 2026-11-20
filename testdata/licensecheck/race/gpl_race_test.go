// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build race

package main

import (
	"testing"

	"example.com/licensecheck/gpl"
)

// TestRaceName builds only under go test -race, the way nightly.yml runs
// pairtime's tests, so its import is seen only in that listing.
func TestRaceName(t *testing.T) {
	if got := gpl.Name(); got != "gpl" {
		t.Errorf("gpl.Name() = %q, want gpl", got)
	}
}
