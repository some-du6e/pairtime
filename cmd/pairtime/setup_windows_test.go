// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"golang.org/x/sys/windows"
)

// TestProtectDir checks that a data directory an install script made, with
// the entries of the folder above it, gets the DACL that home gives the
// directories it makes, and that a folder already in it takes that DACL's
// one entry in place of its own (#124).
func TestProtectDir(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		check     string // the folder checked, in the one protectDir protects
		protected bool   // its DACL must be protected
	}{
		{"the folder gets a protected DACL with this user's entry alone", ".", true},
		{"a folder that was in it already inherits that entry, and has no other", "bin", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "pairtime")
			// As install.ps1's New-Item makes them, with the entries of the
			// folder above.
			if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := protectDir(dir); err != nil {
				t.Fatal(err)
			}
			protected, sids := daclOf(t, filepath.Join(dir, tt.check))
			if protected != tt.protected || !slices.Equal(sids, []string{user.User.Sid.String()}) {
				t.Errorf("after protectDir %s's DACL is protected: %v, with entries for %q; want %v, with this user's (%s) alone", tt.check, protected, sids, tt.protected, user.User.Sid.String())
			}
		})
	}
}
