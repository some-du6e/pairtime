// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/install"
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

// TestMakeHome checks that install gives a data directory an install
// script made, and names with --made, home's DACL before anything is
// written in it, and that it leaves one it isn't told about as it is. The
// agent's socket folder is the data directory on Windows, and the agent
// protects it too once it starts, so only this test, which starts none,
// shows what install does (#124).
func TestMakeHome(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		named     bool // the script names the folders it made with --made
		protected bool // the data directory's DACL is protected afterwards
	}{
		{"a data directory the script made and named gets this user's entry alone", true, true},
		{"one the script didn't name keeps the entries it has", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			local := t.TempDir()
			data := filepath.Join(local, "pairtime")
			dirs := home.Dirs{Data: data, Config: filepath.Join(local, "roaming", "pairtime"), Runtime: data}
			env := install.Env{GOOS: "windows", Home: local, Exe: filepath.Join(data, "bin", "pairtime.exe")}
			// As install.ps1's New-Item makes them, with the entries of the
			// folder above.
			if err := os.MkdirAll(filepath.Dir(env.Exe), 0o700); err != nil {
				t.Fatal(err)
			}
			var made []string
			if tt.named {
				made = []string{data, filepath.Dir(env.Exe)}
			}
			if err := makeHome(env, dirs, nil, made); err != nil {
				t.Fatal(err)
			}
			protected, sids := daclOf(t, data)
			if protected != tt.protected || (tt.protected && !slices.Equal(sids, []string{user.User.Sid.String()})) {
				t.Errorf("after makeHome the data directory's DACL is protected: %v, with entries for %q; want %v", protected, sids, tt.protected)
			}
		})
	}
}
