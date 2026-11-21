// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package install

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIsAt checks how a lock tells that the file it locked is still the
// lock file: a command that tidies up deletes the lock file while it holds
// the lock, and a process that opened the file just before must then take
// the lock again on the new file.
func TestIsAt(t *testing.T) {
	tests := []struct {
		name string
		// then changes what is at the name once the file is open.
		then func(t *testing.T, name string)
		want bool
	}{
		{"the open file is still at its name", func(*testing.T, string) {}, true},
		{"a file whose name was deleted isn't at it", func(t *testing.T, name string) {
			if err := os.Remove(name); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"a file whose name now has a new file isn't at it", func(t *testing.T, name string) {
			if err := os.Remove(name); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(name, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := filepath.Join(t.TempDir(), "x.lock")
			f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			tt.then(t, name)
			if got := isAt(f, name); got != tt.want {
				t.Errorf("isAt = %v, want %v", got, tt.want)
			}
		})
	}
}
