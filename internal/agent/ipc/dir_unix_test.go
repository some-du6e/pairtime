// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package ipc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSecureDir checks the socket directory's guards as this test's user.
// TestOtherUser checks, as root, the one only a second user can: a link
// that another uid made.
func TestSecureDir(t *testing.T) {
	me, err := self()
	if err != nil {
		t.Fatal(err)
	}
	// mkdir makes dir with exactly mode perm, past the umask.
	mkdir := func(t *testing.T, dir string, perm os.FileMode) {
		t.Helper()
		if err := os.Mkdir(dir, perm); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, perm); err != nil {
			t.Fatal(err)
		}
	}
	// mode reads the permission bits of name, following links.
	mode := func(t *testing.T, name string) os.FileMode {
		t.Helper()
		fi, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		return fi.Mode().Perm()
	}
	tests := []struct {
		name string
		run  func(t *testing.T, base string)
	}{
		{
			name: "a missing directory is made with mode 0700",
			run: func(t *testing.T, base string) {
				dir := filepath.Join(base, "run")
				if err := secureDir(dir, me); err != nil {
					t.Fatal(err)
				}
				if m := mode(t, dir); m != 0o700 {
					t.Errorf("the directory has mode %v, want 0700", m)
				}
			},
		},
		{
			name: "a directory of the wanted user's with looser permissions is tightened to 0700",
			run: func(t *testing.T, base string) {
				dir := filepath.Join(base, "run")
				mkdir(t, dir, 0o775)
				if err := secureDir(dir, me); err != nil {
					t.Fatal(err)
				}
				if m := mode(t, dir); m != 0o700 {
					t.Errorf("the directory has mode %v, want 0700", m)
				}
			},
		},
		{
			name: "a symbolic link that the wanted user made is followed, and its target tightened",
			run: func(t *testing.T, base string) {
				target := filepath.Join(base, "target")
				mkdir(t, target, 0o755)
				link := filepath.Join(base, "run")
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
				if err := secureDir(link, me); err != nil {
					t.Fatal(err)
				}
				if m := mode(t, target); m != 0o700 {
					t.Errorf("the link's target has mode %v, want 0700", m)
				}
			},
		},
		{
			name: "a symbolic link that the wanted user didn't make is refused, and its target left alone",
			run: func(t *testing.T, base string) {
				target := filepath.Join(base, "target")
				mkdir(t, target, 0o755)
				link := filepath.Join(base, "run")
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
				err := secureDir(link, otherUser(t))
				if err == nil {
					t.Fatal("secureDir followed a link that the wanted user didn't make")
				}
				if !strings.Contains(err.Error(), "symbolic link") {
					t.Errorf("secureDir = %v, want it to refuse the link", err)
				}
				if m := mode(t, target); m != 0o755 {
					t.Errorf("the link's target has mode %v now, want it left at 0755", m)
				}
			},
		},
		{
			name: "a file in the directory's place is refused",
			run: func(t *testing.T, base string) {
				name := filepath.Join(base, "run")
				if err := os.WriteFile(name, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := secureDir(name, me); err == nil {
					t.Error("secureDir took a file for the socket's directory")
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.run(t, shortDir(t))
		})
	}
}
