// SPDX-License-Identifier: AGPL-3.0-or-later

package webui

import (
	"io/fs"
	"strings"
	"testing"
)

func TestFS(t *testing.T) {
	tests := []struct {
		name string
		file string
		want string // a substring of the file, compared without case
	}{
		// The placeholder and Vite's build both have an index.html at the
		// root, so this holds for either.
		{name: "index.html is at the root of the UI", file: "index.html", want: "<!doctype html>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := fs.ReadFile(FS(), tt.file)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.ToLower(string(b)), strings.ToLower(tt.want)) {
				t.Errorf("%s doesn't contain %q", tt.file, tt.want)
			}
		})
	}
}
