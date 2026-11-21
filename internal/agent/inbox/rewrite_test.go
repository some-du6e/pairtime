// SPDX-License-Identifier: AGPL-3.0-or-later

package inbox

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestRewrite checks Rewrite, which keeps some lines of a private file: on
// Windows too, where a file can't be renamed over while it is open, which
// platform.yml's run of this package checks.
func TestRewrite(t *testing.T) {
	const text = "\nkeep 1\n\ndrop 1\n\nkeep 2\n\ndrop 2\n"
	keepKeeps := func(line []byte, _ int64) bool { return bytes.HasPrefix(line, []byte("keep")) }
	tests := []struct {
		name    string
		text    string
		keep    func(line []byte, at int64) bool
		dropped int
		want    string // the file afterwards; "-" for none
	}{
		{"the lines keep drops go, and the rest, blank ones included, stay in order",
			text, keepKeeps, 2, "\nkeep 1\n\n\nkeep 2\n\n"},
		{"a rewrite that drops nothing leaves the file as it was",
			text, func([]byte, int64) bool { return true }, 0, text},
		{"a rewrite that keeps no line deletes the file",
			text, func([]byte, int64) bool { return false }, 4, "-"},
		{"keep sees where each line starts, so a file's first part can stay whatever it holds",
			text, func(line []byte, at int64) bool {
				return at < int64(len("\nkeep 1\n\ndrop 1\n")) || keepKeeps(line, at)
			},
			1, "\nkeep 1\n\ndrop 1\n\nkeep 2\n\n"},
		{"a last line with no newline is a line like the others",
			"keep 1\ndrop 1", keepKeeps, 1, "keep 1\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "data")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(dir, "held.jsonl")
			if err := os.WriteFile(name, []byte(tt.text), 0o600); err != nil {
				t.Fatal(err)
			}
			dropped, err := Rewrite(name, ".held.jsonl.tmp-", tt.keep)
			if err != nil {
				t.Fatalf("Rewrite: %v", err)
			}
			if dropped != tt.dropped {
				t.Errorf("dropped %d lines, want %d", dropped, tt.dropped)
			}
			got, err := os.ReadFile(name)
			switch {
			case tt.want == "-":
				if !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("the file is still there (%v): %q", err, got)
				}
			case err != nil:
				t.Fatal(err)
			case string(got) != tt.want:
				t.Errorf("the file holds %q, want %q", got, tt.want)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if i := slices.IndexFunc(entries, func(e fs.DirEntry) bool { return strings.HasPrefix(e.Name(), ".held.jsonl.tmp-") }); i >= 0 {
				t.Errorf("%s is left beside it", entries[i].Name())
			}
		})
	}
	t.Run("a file that isn't there is an error that says so, and nothing is made", func(t *testing.T) {
		dir := t.TempDir()
		if _, err := Rewrite(filepath.Join(dir, "held.jsonl"), ".held.jsonl.tmp-", keepKeeps); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Rewrite: %v, want a missing file", err)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("the folder holds %d entries", len(entries))
		}
	})
}
