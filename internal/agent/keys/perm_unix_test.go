// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package keys

import (
	"os"
	"path/filepath"
	"testing"
)

// On Windows the same files get a user-only DACL from home.WritePrivate,
// which home's own tests check.

func TestKeyFileIsOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	ks := mustOpen(t, Options{Dir: dir, Stores: []Store{}})
	tests := []struct {
		name string
		path string
		want os.FileMode
	}{
		{"the key file is mode 0600", ks.Status().File, 0o600},
		{"keystore.json is mode 0600", filepath.Join(keysDir(dir), markerName), 0o600},
		{"the keystore's directory is mode 0700", keysDir(dir), 0o700 | os.ModeDir},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fi, err := os.Stat(tt.path)
			if err != nil {
				t.Fatal(err)
			}
			if got := fi.Mode() & (os.ModePerm | os.ModeDir); got != tt.want {
				t.Errorf("%s has mode %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
