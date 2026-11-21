// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package main

import (
	"errors"
	"io/fs"
	"testing"
)

// TestCheckUID checks that install and uninstall refuse to run as root in
// a home directory root doesn't own, as `sudo pairtime install` does on
// macOS, whose sudo keeps HOME (#124).
func TestCheckUID(t *testing.T) {
	owners := map[string]int{"/home/teen": 1000, "/root": 0}
	owner := func(p string) (int, error) {
		if uid, ok := owners[p]; ok {
			return uid, nil
		}
		return 0, fs.ErrNotExist
	}
	tests := []struct {
		name string
		euid int
		home string
		want error
	}{
		{"a user sets pairtime up in their own home directory", 1000, "/home/teen", nil},
		{"root in another user's home directory, as under sudo, is refused", 0, "/home/teen", errRootElsewhere},
		{"root in its own home directory sets up root's own pairtime", 0, "/root", nil},
		{"root in a home directory that isn't there, and no folder above it, is refused", 0, "/home/nobody", errRootElsewhere},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := checkUID(tt.euid, tt.home, owner); !errors.Is(err, tt.want) {
				t.Errorf("checkUID(%d, %q) = %v, want %v", tt.euid, tt.home, err, tt.want)
			}
		})
	}
}
