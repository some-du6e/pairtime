// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux && desktop

package main

import (
	"testing"

	"example.com/licensecheck/gpl"
)

// TestDesktopName is shaped like the desktop tests platform.yml runs
// (AGENTS.md), so its import is seen only with -tags desktop on linux.
func TestDesktopName(t *testing.T) {
	if got := gpl.Name(); got != "gpl" {
		t.Errorf("gpl.Name() = %q, want gpl", got)
	}
}
