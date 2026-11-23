// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/home"
)

func TestPrivacyKeepText(t *testing.T) {
	// An older pairtime's config.toml, without [privacy], ends in
	// [notifications].
	const older = "mode = \"local\"\n\n[notifications]\nenabled = true\nquiet_hours = \"\"\n"
	tests := []struct {
		name     string
		file     string // "" means no config.toml
		args     []string
		wantCode int
		check    func(t *testing.T, cfg config.Config, file, out, errOut string)
	}{
		{
			name: "off turns the switch off in this version's config.toml, which deletes the text kept",
			file: "default", args: []string{"off"},
			check: func(t *testing.T, cfg config.Config, file, out, errOut string) {
				if !cfg.Privacy.TextOff || cfg.Privacy.TextHeld {
					t.Errorf("config.toml gives %+v, want the switch off", cfg.Privacy)
				}
				if !strings.Contains(out, "keep_prompt_text = false now") || !strings.Contains(out, "deletes the text it kept") {
					t.Errorf("printed %q", out)
				}
			},
		},
		{
			name: "off adds [privacy] at the end of an older config.toml, and nothing else changes",
			file: older, args: []string{"off"},
			check: func(t *testing.T, cfg config.Config, file, out, errOut string) {
				if file != older+"\n[privacy]\nkeep_prompt_text = false\n" {
					t.Errorf("config.toml is %q", file)
				}
				if want := (config.Config{Mode: config.Local, Privacy: config.Privacy{TextOff: true}}); cfg != want {
					t.Errorf("config.toml gives %+v, want %+v", cfg, want)
				}
			},
		},
		{
			name: "on turns the switch on again, and says the text deleted stays deleted",
			file: older + "\n[privacy]\nkeep_prompt_text = false\n", args: []string{"on"},
			check: func(t *testing.T, cfg config.Config, file, out, errOut string) {
				if file != older+"\n[privacy]\nkeep_prompt_text = true\n" || !cfg.Privacy.KeepText() {
					t.Errorf("config.toml is %q and gives %+v", file, cfg.Privacy)
				}
				if !strings.Contains(out, "The text it deleted before stays deleted") {
					t.Errorf("printed %q", out)
				}
			},
		},
		{
			name: "on with no config.toml yet writes none, and says pairtime keeps the text, as by default",
			args: []string{"on"},
			check: func(t *testing.T, cfg config.Config, file, out, errOut string) {
				if file != "" || !cfg.Privacy.KeepText() {
					t.Errorf("config.toml is %q", file)
				}
				if !strings.Contains(out, "There's no config.toml, so pairtime keeps the text of your prompts") {
					t.Errorf("printed %q", out)
				}
			},
		},
		{
			name: "with no config.toml yet, it writes the default with the switch off",
			args: []string{"off"},
			check: func(t *testing.T, cfg config.Config, file, out, errOut string) {
				if want := (config.Config{Mode: config.Local, Privacy: config.Privacy{TextOff: true}}); cfg != want {
					t.Errorf("config.toml gives %+v, want %+v", cfg, want)
				}
			},
		},
		{
			name: "a config.toml that doesn't parse stays as it is, and the message says what to write by hand",
			file: "[privacy]\nkeep_prompt_text = true\n[privacy]\nkeep_prompt_text = false\n", args: []string{"off"}, wantCode: 1,
			check: func(t *testing.T, cfg config.Config, file, out, errOut string) {
				if file != "[privacy]\nkeep_prompt_text = true\n[privacy]\nkeep_prompt_text = false\n" {
					t.Errorf("config.toml changed to %q", file)
				}
				if !strings.Contains(errOut, "doesn't parse") || !strings.Contains(errOut, "set keep_prompt_text = false under [privacy]") {
					t.Errorf("printed %q", errOut)
				}
			},
		},
		{
			name: "a setting pairtime doesn't know beside the switch keeps it from deleting anything, which it says, with exit 1",
			file: "[privacy]\nkeep_prompt_text = true\nprompt_days = 7\n", args: []string{"off"}, wantCode: 1,
			check: func(t *testing.T, cfg config.Config, file, out, errOut string) {
				if !strings.Contains(file, "keep_prompt_text = false") {
					t.Errorf("config.toml is %q", file)
				}
				if !strings.Contains(out, "keeps the switch from working") || !strings.Contains(out, `"privacy.prompt_days"`) {
					t.Errorf("printed %q", out)
				}
			},
		},
		{
			name: "anything but off or on is a usage error",
			file: "default", args: []string{"no"}, wantCode: 2,
			check: func(t *testing.T, cfg config.Config, file, out, errOut string) {
				if !cfg.Privacy.KeepText() || !strings.Contains(errOut, "takes off or on") {
					t.Errorf("config.toml gives %+v; printed %q", cfg.Privacy, errOut)
				}
			},
		},
		{
			name: "no argument is a usage error",
			file: "default", wantCode: 2,
			check: func(t *testing.T, cfg config.Config, file, out, errOut string) {
				if !cfg.Privacy.KeepText() {
					t.Errorf("config.toml gives %+v", cfg.Privacy)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv(home.EnvHome, dir)
			dirs, err := home.Default()
			if err != nil {
				t.Fatal(err)
			}
			name := dirs.ConfigFile()
			if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
				t.Fatal(err)
			}
			switch tt.file {
			case "":
			case "default":
				if err := config.WriteDefault(name); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.WriteFile(name, []byte(tt.file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var out, errOut bytes.Buffer
			code := run(append([]string{"privacy", "keep-text"}, tt.args...), &out, &errOut)
			if code != tt.wantCode {
				t.Fatalf("exit %d, want %d; stdout:\n%s\nstderr:\n%s", code, tt.wantCode, out.String(), errOut.String())
			}
			data, err := os.ReadFile(name)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				t.Fatal(err)
			}
			cfg, _ := config.Parse(data)
			tt.check(t, cfg, string(data), out.String(), errOut.String())
		})
	}
}
