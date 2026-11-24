// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// notesKey is a second project's key, beside chip8Key.
const notesKey = "g0b2d4f6a8c0e2a4c6e8a0b2d4f6a8c0e"

func TestForgetNames(t *testing.T) {
	chip8 := "[projects." + chip8Key + "]\nname = \"chip8-emulator\"\nonline = true\n"
	tests := []struct {
		name string
		file string // "" means no file
		want string // "" means no file
		// wantErr is a part of the error, "" for none; the file then stays
		// as it was.
		wantErr string
	}{
		{
			name: "a project's table goes with every line under it, and the rest stays byte for byte",
			file: "mode = \"online\"\n\n[server]\nurl = \"https://pairtime.example\"\n\n" + chip8,
			want: "mode = \"online\"\n\n[server]\nurl = \"https://pairtime.example\"\n\n",
		},
		{
			name: "every project's table goes, and the other settings keep their comments and CRLF line endings",
			file: "# pairtime settings\r\nmode = \"local\"\r\n\r\n[projects." + chip8Key + "]\r\nname = \"chip8-emulator\"\r\nonline = true\r\n\r\n[projects." + notesKey + "]\r\nname = \"notes\"\r\n\r\n[display]\r\nshadow = false\r\n",
			want: "# pairtime settings\r\nmode = \"local\"\r\n\r\n[display]\r\nshadow = false\r\n",
		},
		{
			name: "headers with quoted keys or blanks around the dots name the same tables, and go",
			file: "mode = \"local\"\n\n[\"projects\"." + chip8Key + "]\nname = \"chip8-emulator\"\n\n[ projects . " + notesKey + " ]\nname = \"notes\"\n",
			want: "mode = \"local\"\n\n",
		},
		{
			name: "dotted keys under projects at the top of the file go, quoted or not",
			file: "mode = \"local\"\nprojects." + chip8Key + ".name = \"chip8-emulator\"\n\"projects\"." + chip8Key + ".online = true\n\n[display]\nshadow = false\n",
			want: "mode = \"local\"\n\n[display]\nshadow = false\n",
		},
		{
			name: "an inline table of projects at the top of the file goes",
			file: "projects = { " + chip8Key + " = { name = \"chip8-emulator\", online = true } }\nmode = \"local\"\n",
			want: "mode = \"local\"\n",
		},
		{
			name: "a [projects] table of inline tables goes",
			file: "mode = \"local\"\n\n[projects]\n" + chip8Key + " = { name = \"chip8-emulator\" }\n",
			want: "mode = \"local\"\n\n",
		},
		{
			name: "an array of [[projects]] tables goes, though Parse reads no project in it",
			file: "mode = \"local\"\n\n[[projects]]\nname = \"chip8-emulator\"\n",
			want: "mode = \"local\"\n\n",
		},
		{
			name: "a table under a project's table goes with it",
			file: chip8 + "\n[projects." + chip8Key + ".more]\nnote = \"mine\"\n\n[display]\nshadow = false\n",
			want: "[display]\nshadow = false\n",
		},
		{
			name: "the comment lines right above a table that goes go with it, and a comment a blank line above stays",
			file: "mode = \"local\"\n# a blank line follows\n\n# chip8, my emulator\n# for the robotics club\n" + chip8,
			want: "mode = \"local\"\n# a blank line follows\n\n",
		},
		{
			name: "the comment lines right above a dotted key that goes go with it",
			file: "mode = \"local\"\n# my emulator\nprojects." + chip8Key + ".name = \"chip8-emulator\"\n",
			want: "mode = \"local\"\n",
		},
		{
			name: "a projects key inside another table isn't the teen's projects, and stays",
			file: "[display]\nshadow = false\nprojects = \"kept\"\n",
			want: "[display]\nshadow = false\nprojects = \"kept\"\n",
		},
		{
			name: "a byte order mark stays",
			file: bom + "mode = \"local\"\n\n" + chip8,
			want: bom + "mode = \"local\"\n\n",
		},
		{
			name: "a file with no project is left as it is",
			file: "mode = \"local\"\n",
			want: "mode = \"local\"\n",
		},
		{
			name: "no file stays no file",
		},
		{
			name:    "projects in another case is refused, as it can hold a name Parse doesn't read",
			file:    "mode = \"local\"\n\n[Projects." + chip8Key + "]\nname = \"chip8-emulator\"\n",
			wantErr: "spells projects in another upper and lower case",
		},
		{
			name:    "projects in another case beside one in lower case is refused, and nothing goes",
			file:    chip8 + "\n[PROJECTS." + notesKey + "]\nname = \"notes\"\n",
			wantErr: "spells projects in another upper and lower case",
		},
		{
			name:    "a dotted key that spells projects in another case is refused",
			file:    "Projects." + chip8Key + ".name = \"chip8-emulator\"\nmode = \"local\"\n",
			wantErr: "spells projects in another upper and lower case",
		},
		{
			name:    "a multi-line string in a project's table, whose lines look like a header, is refused",
			file:    "[projects." + chip8Key + "]\nname = \"\"\"chip8\n[server]\n\"\"\"\n",
			wantErr: "multi-line string",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := filepath.Join(t.TempDir(), "config.toml")
			if tt.file != "" {
				if err := os.WriteFile(name, []byte(tt.file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			err := ForgetNames(name)
			want := tt.want
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("error %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("error %v, want one containing %q", err, tt.wantErr)
			case tt.wantErr != "":
				want = tt.file
			}
			got, rerr := os.ReadFile(name)
			switch {
			case want == "" && !errors.Is(rerr, fs.ErrNotExist):
				t.Fatalf("config.toml is there (%v), want no file", rerr)
			case want == "":
				return
			case rerr != nil:
				t.Fatal(rerr)
			}
			if string(got) != want {
				t.Errorf("config.toml is\n%q\nwant\n%q", got, want)
			}
			if cfg, problems := Parse(got); tt.wantErr == "" && cfg.Projects != nil {
				t.Errorf("projects are left: %v", cfg.Projects)
			} else if _, before := Parse([]byte(tt.file)); tt.wantErr == "" {
				for _, p := range problems {
					if !slices.Contains(before, p) {
						t.Errorf("the edit added the problem %q", p)
					}
				}
			}
		})
	}
}
