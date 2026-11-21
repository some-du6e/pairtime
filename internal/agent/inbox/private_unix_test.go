// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package inbox

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOpenPrivate checks, as this test's user, that OpenPrivate opens a
// file only with the spool's guards: in a folder nobody else may change,
// and only a regular file of this user's, with one name, that nobody else
// may open.
func TestOpenPrivate(t *testing.T) {
	const flags = os.O_WRONLY | os.O_APPEND | os.O_CREATE
	tests := []struct {
		name string
		// setup makes the folder and what is in it, and returns the file
		// to open.
		setup func(t *testing.T, dir string) string
		ok    bool
	}{
		{"a file only this user may open, in a folder only it may change, opens, and is made when missing", func(t *testing.T, dir string) string {
			return filepath.Join(dir, "held.jsonl")
		}, true},
		{"a symbolic link in the file's place is refused", func(t *testing.T, dir string) string {
			target := filepath.Join(t.TempDir(), "target")
			if err := os.WriteFile(target, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(dir, "held.jsonl")
			if err := os.Symlink(target, name); err != nil {
				t.Fatal(err)
			}
			return name
		}, false},
		{"a file others may read is refused", func(t *testing.T, dir string) string {
			name := filepath.Join(dir, "held.jsonl")
			if err := os.WriteFile(name, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(name, 0o644); err != nil {
				t.Fatal(err)
			}
			return name
		}, false},
		{"a file with a second name is refused", func(t *testing.T, dir string) string {
			other := filepath.Join(dir, "notes.txt")
			if err := os.WriteFile(other, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(dir, "held.jsonl")
			if err := os.Link(other, name); err != nil {
				t.Fatal(err)
			}
			return name
		}, false},
		{"a file in a folder others may write in is refused", func(t *testing.T, dir string) string {
			if err := os.Chmod(dir, 0o777); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(dir, "held.jsonl")
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "data")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			name := tt.setup(t, dir)
			f, err := OpenPrivate(name, flags, 0o600)
			if err == nil {
				defer f.Close()
				if _, err := f.Write([]byte("\n")); err != nil {
					t.Fatal(err)
				}
			}
			if (err == nil) != tt.ok {
				t.Fatalf("OpenPrivate: %v, want ok %v", err, tt.ok)
			}
		})
	}
}
