// SPDX-License-Identifier: AGPL-3.0-or-later

package home

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFile(t *testing.T) {
	tests := []struct {
		name    string
		write   func(name string, data []byte) error
		private bool
		before  string // a file already there; "" for none
	}{
		{name: "WriteFile creates the file, for its owner only where the OS has modes", write: WriteFile},
		{name: "WriteFile replaces a file that is there", write: WriteFile, before: "old"},
		{name: "WritePrivate creates a file only its owner can open", write: WritePrivate, private: true},
		{name: "WritePrivate replaces a file with one only its owner can open", write: WritePrivate, private: true, before: "old"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			name := filepath.Join(dir, "port.json")
			if tt.before != "" {
				if err := os.WriteFile(name, []byte(tt.before), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want := []byte(`{"v":1}` + "\n")
			if err := tt.write(name, want); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("the file holds %q, want %q", got, want)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				var names []string
				for _, e := range entries {
					names = append(names, e.Name())
				}
				t.Errorf("the directory holds %q, want only port.json: a temporary file was left behind", names)
			}
			checkOwnerOnly(t, name, tt.private)
		})
	}
}
