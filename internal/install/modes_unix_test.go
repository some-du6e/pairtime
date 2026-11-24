// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package install

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"testing"
)

// TestInstallStateModes checks that what install keeps in its directory is
// the teen's alone (DESIGN 3.5; D-install-retention): the directory and
// backups/ are 0700 and every file in them 0600, a copy of a file anyone
// could read included, since a copy can hold the teen's API keys and
// tokens. The file install edited keeps its own mode.
func TestInstallStateModes(t *testing.T) {
	tests := []struct {
		name string
		// item writes the file the item edits and returns the item and
		// that file's path.
		item func(t *testing.T, h wakaHome) (Item, string)
		// also is a file install keeps beside manifest.json, "" for none.
		also string
	}{
		{
			name: "the copy of a ~/.wakatime.cfg anyone can read, with the teen's Hackatime key, is the teen's alone, and so is everything else install keeps",
			item: func(t *testing.T, h wakaHome) (Item, string) {
				writeFile(t, h.cfg(), []byte("[settings]\napi_url = https://hackatime.hackclub.com/api/hackatime/v1\napi_key = "+hackatimeKey+"\n"))
				return h.item(t, 7247, wakaKey), h.cfg()
			},
			also: "wakatime.json",
		},
		{
			name: "the copy of a Claude Code settings.json anyone can read, with a token in its env block, is the teen's alone",
			item: func(t *testing.T, h wakaHome) (Item, string) {
				settings := ClaudeSettings(h.env)
				writeFile(t, settings, []byte(`{"env": {"ANTHROPIC_API_KEY": "sk-not-a-real-key"}}`+"\n"))
				item, err := ClaudeCode(h.env)
				if err != nil {
					t.Fatal(err)
				}
				return item, settings
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newWakaHome(t, nil)
			item, edited := tt.item(t, h)
			if err := os.Chmod(edited, 0o644); err != nil {
				t.Fatal(err)
			}
			plan, err := h.in.PlanInstall([]Item{item})
			if err != nil {
				t.Fatalf("PlanInstall: %v", err)
			}
			if err := plan.Apply(); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			var names []string
			copies := 0
			err = filepath.WalkDir(h.in.Dir, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				info, err := d.Info()
				if err != nil {
					return err
				}
				rel, err := filepath.Rel(h.in.Dir, p)
				if err != nil {
					return err
				}
				rel = filepath.ToSlash(rel)
				want := fs.FileMode(0o600)
				if d.IsDir() {
					want = 0o700
				}
				if got := info.Mode().Perm(); got != want {
					t.Errorf("install's %s has mode %#o, want %#o", rel, got, want)
				}
				names = append(names, rel)
				if !d.IsDir() && path.Dir(rel) == "backups" {
					copies++
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"manifest.json", tt.also} {
				if name != "" && !slices.Contains(names, name) {
					t.Errorf("install's directory holds %q and no %s, whose mode went unchecked", names, name)
				}
			}
			if copies == 0 {
				t.Errorf("install's directory holds %q and no copy of %s in backups/, so no copy's mode was checked", names, edited)
			}
			info, err := os.Stat(edited)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != 0o644 {
				t.Errorf("install left %s with mode %#o, want its own 0644", edited, got)
			}
		})
	}
}
