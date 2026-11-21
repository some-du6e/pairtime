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
		mode    Mode
	}{
		{"a file that is TOML parsed", ptr("mode = \"local\"\n"), true, false, Local},
		{"one that says online parsed, with its mode", ptr("mode = \"online\"\n"), true, false, Online},
		{"one with an unknown setting parsed, with its problem", ptr("colour = \"blue\"\n"), true, false, Local},
		{"one that isn't TOML didn't, and is local", ptr("mode = \"onl"), false, false, Local},
		{"one with a value of the wrong type didn't, as Parse ignores it, and is local whatever its mode says", ptr("mode = \"online\"\n[http]\nport = \"8123\"\n"), false, false, Local},
		{"no file counts as parsed, with the defaults", nil, true, true, Local},
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
			if cfg.Mode != tc.mode {
				t.Errorf("mode %q, want %q", cfg.Mode, tc.mode)
			}
		})
	}
}

func ptr(s string) *string { return &s }
