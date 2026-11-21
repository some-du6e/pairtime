// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadParsed(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name    string
		text    *string // nil: no file
		parsed  bool
		missing bool
	}{
		{"a file that is TOML parsed", ptr("mode = \"local\"\n"), true, false},
		{"one with an unknown setting parsed, with its problem", ptr("colour = \"blue\"\n"), true, false},
		{"one that isn't TOML didn't", ptr("mode = \"loc"), false, false},
		{"no file counts as parsed, with the defaults", nil, true, true},
	}
	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			name := filepath.Join(dir, "config"+string(rune('a'+i))+".toml")
			if tc.text != nil {
				if err := os.WriteFile(name, []byte(*tc.text), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cfg, _, parsed, err := LoadParsed(name)
			if parsed != tc.parsed {
				t.Errorf("parsed = %v, want %v", parsed, tc.parsed)
			}
			if got := errors.Is(err, fs.ErrNotExist); got != tc.missing {
				t.Errorf("err = %v", err)
			}
			if cfg.Mode != Local {
				t.Errorf("mode %q, want %q", cfg.Mode, Local)
			}
		})
	}
}

func ptr(s string) *string { return &s }
