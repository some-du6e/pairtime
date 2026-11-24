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

// notesKey is a second project's key of this computer's, beside chip8Key,
// and farKey one of another computer that shares config.toml.
const (
	notesKey = "g0b2d4f6a8c0e2a4c6e8a0b2d4f6a8c0e"
	farKey   = "g5e4d3c2b1a0f9e8d7c6b5a4f3e2d1c0b"
)

// mineKeys reports this computer's projects' keys in the tests: chip8Key
// and notesKey.
func mineKeys(key string) bool { return key == chip8Key || key == notesKey }

func TestForgetNames(t *testing.T) {
	chip8 := "[projects." + chip8Key + "]\nname = \"chip8-emulator\"\nonline = true\n"
	far := "[projects." + farKey + "]\nname = \"far-away\"\n"
	backslash := string(rune(92))
	tests := []struct {
		name string
		file string // "" means no file
		want string // "" means no file
		left int    // Forgotten.Left
		// removed is Forgotten.Removed: how many of this computer's keys it
		// took out, 0 when the edit refused.
		removed int
		// wantErr is a part of the error, "" for none; the file then stays
		// as it was, and byHand is Forgotten.ByHand.
		wantErr string
		byHand  []string
	}{
		{
			name:    "a table of this computer's goes with every line under it, and the rest stays byte for byte",
			file:    "mode = \"online\"\n\n[server]\nurl = \"https://pairtime.example\"\n\n" + chip8,
			want:    "mode = \"online\"\n\n[server]\nurl = \"https://pairtime.example\"\n\n",
			removed: 1,
		},
		{
			name:    "a table of a key this computer never had stays, and is counted",
			file:    "mode = \"local\"\n\n" + far + "\n" + chip8,
			want:    "mode = \"local\"\n\n" + far + "\n",
			left:    1,
			removed: 1,
		},
		{
			name:    "every table of this computer's goes, and the other settings keep their comments and CRLF line endings",
			file:    "# pairtime settings\r\nmode = \"local\"\r\n\r\n[projects." + chip8Key + "]\r\nname = \"chip8-emulator\"\r\nonline = true\r\n\r\n[projects." + notesKey + "]\r\nname = \"notes\"\r\n\r\n[display]\r\nshadow = false\r\n",
			want:    "# pairtime settings\r\nmode = \"local\"\r\n\r\n[display]\r\nshadow = false\r\n",
			removed: 2,
		},
		{
			name:    "headers with quoted keys or blanks around the dots name the same tables, and go",
			file:    "mode = \"local\"\n\n[\"projects\"." + chip8Key + "]\nname = \"chip8-emulator\"\n\n[ projects . " + notesKey + " ]\nname = \"notes\"\n",
			want:    "mode = \"local\"\n\n",
			removed: 2,
		},
		{
			name:    "dotted keys under projects at the top of the file go, quoted or not",
			file:    "mode = \"local\"\nprojects." + chip8Key + ".name = \"chip8-emulator\"\n\"projects\"." + chip8Key + ".online = true\n\n[display]\nshadow = false\n",
			want:    "mode = \"local\"\n\n[display]\nshadow = false\n",
			removed: 1,
		},
		{
			name: "a dotted key of a key this computer never had stays, and is counted",
			file: "projects." + farKey + ".name = \"far-away\"\nmode = \"local\"\n",
			want: "projects." + farKey + ".name = \"far-away\"\nmode = \"local\"\n",
			left: 1,
		},
		{
			name:    "a [projects] table loses the lines of this computer's keys and keeps another's",
			file:    "[projects]\n" + chip8Key + " = { name = \"chip8-emulator\" }\n" + farKey + " = { name = \"far-away\" }\n",
			want:    "[projects]\n" + farKey + " = { name = \"far-away\" }\n",
			left:    1,
			removed: 1,
		},
		{
			name:    "a table under one of this computer's tables goes with it",
			file:    chip8 + "\n[projects." + chip8Key + ".more]\nnote = \"mine\"\n\n[display]\nshadow = false\n",
			want:    "[display]\nshadow = false\n",
			removed: 1,
		},
		{
			name:    "the comment lines right above a table that goes go with it, and a comment a blank line above stays",
			file:    "mode = \"local\"\n# a blank line follows\n\n# chip8, my emulator\n# for the robotics club\n" + chip8,
			want:    "mode = \"local\"\n# a blank line follows\n\n",
			removed: 1,
		},
		{
			name:    "the comment lines right above a dotted key that goes go with it",
			file:    "mode = \"local\"\n# my emulator\nprojects." + chip8Key + ".name = \"chip8-emulator\"\n",
			want:    "mode = \"local\"\n",
			removed: 1,
		},
		{
			name: "an array of [[projects]] tables names no project's key, and stays",
			file: "mode = \"local\"\n\n[[projects]]\nname = \"chip8-emulator\"\n",
			want: "mode = \"local\"\n\n[[projects]]\nname = \"chip8-emulator\"\n",
		},
		{
			name: "a projects key inside another table isn't the teen's projects, and stays",
			file: "[display]\nshadow = false\nprojects = \"kept\"\n",
			want: "[display]\nshadow = false\nprojects = \"kept\"\n",
		},
		{
			name:    "a byte order mark stays",
			file:    bom + "mode = \"local\"\n\n" + chip8,
			want:    bom + "mode = \"local\"\n\n",
			removed: 1,
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
			name:    "a dotted key of this computer's inside a [projects] table goes, and another key's dotted key there stays",
			file:    "[projects]\n" + chip8Key + ".name = \"chip8-emulator\"\n" + farKey + ".name = \"far-away\"\n",
			want:    "[projects]\n" + farKey + ".name = \"far-away\"\n",
			left:    1,
			removed: 1,
		},
		{
			name:    "a sub-table of this computer's key with no table of its own above it goes",
			file:    "[projects." + chip8Key + ".more]\nnote = \"mine\"\n\n" + far,
			want:    far,
			left:    1,
			removed: 1,
		},
		{
			name:    "an entry of this computer's in [projects] whose array goes on over lines goes with every line of it",
			file:    "[projects]\n" + chip8Key + ".tags = [\n  \"robotics\",\n  \"club\",\n]\n" + farKey + ".name = \"far-away\"\n",
			want:    "[projects]\n" + farKey + ".name = \"far-away\"\n",
			left:    1,
			removed: 1,
		},
		{
			name:    "a line inside another key's inline table that goes on over lines stays, though its key is one of this computer's",
			file:    "[projects]\n" + chip8Key + " = { name = \"chip8-emulator\" }\n" + farKey + " = {\n  " + chip8Key + " = 1\n}\n",
			want:    "[projects]\n" + farKey + " = {\n  " + chip8Key + " = 1\n}\n",
			left:    1,
			removed: 1,
		},
		{
			name:    "an array of tables under one of this computer's tables goes with it",
			file:    chip8 + "\n[[projects." + chip8Key + ".links]]\nurl = \"https://chip8.example\"\n\n[display]\nshadow = false\n",
			want:    "[display]\nshadow = false\n",
			removed: 1,
		},
		{
			name:    "an array of arrays in a table that goes doesn't end its lines early",
			file:    chip8 + "grid = [\n[1, 2],\n[3, 4],\n]\n\n[display]\nshadow = false\n",
			want:    "[display]\nshadow = false\n",
			removed: 1,
		},
		{
			name:    "the comment lines right above the header after a table that goes stay with that header",
			file:    chip8 + "# about the display\n[display]\nshadow = false\n",
			want:    "# about the display\n[display]\nshadow = false\n",
			removed: 1,
		},
		{
			name:    "a comment after the last key of a table that goes, a blank line before the next header's comments, goes with the table",
			file:    chip8 + "# after chip8's last key\n\n# about the display\n[display]\nshadow = false\n",
			want:    "# about the display\n[display]\nshadow = false\n",
			removed: 1,
		},
		{
			name: "two tables that go, with another key's table between them and a table after, each lose the comment lines right above them, and each header that stays keeps its own",
			file: "mode = \"local\"\n# a note, a blank line above chip8's comments\n\n# about chip8\n[projects." + chip8Key + "]\nname = \"chip8-emulator\"\n# after chip8's last key\n\n# about far\n" + far +
				"\n# about notes\n[projects." + notesKey + "]\nname = \"notes\"\n# about the display\n[display]\nshadow = false\n",
			want:    "mode = \"local\"\n# a note, a blank line above chip8's comments\n\n# about far\n" + far + "\n# about the display\n[display]\nshadow = false\n",
			left:    1,
			removed: 2,
		},
		{
			name: "a table of a key this computer never had that spells projects in another case stays, and is counted",
			file: "[Projects." + farKey + "]\nname = \"far-away\"\n",
			want: "[Projects." + farKey + "]\nname = \"far-away\"\n",
			left: 1,
		},
		{
			name:    "a table of this computer's that spells projects in another case is refused, naming its header as the file writes it, comment left out",
			file:    "mode = \"local\"\n\n[Projects." + chip8Key + "] # robotics club\nname = \"chip8-emulator\"\n",
			wantErr: "can't take out line by line",
			byHand:  []string{"[Projects." + chip8Key + "]"},
		},
		{
			name:    "projects in another case beside one in lower case is refused, and nothing goes: both headers are to delete by hand",
			file:    chip8 + "\n[PROJECTS." + notesKey + "]\nname = \"notes\"\n",
			wantErr: "can't take out line by line",
			byHand:  []string{"[projects." + chip8Key + "]", "[PROJECTS." + notesKey + "]"},
		},
		{
			name:    "an inline table at projects that holds one of this computer's keys is refused, as one line holds every key",
			file:    "projects = { " + chip8Key + " = { name = \"chip8-emulator\" } }\nmode = \"local\"\n",
			wantErr: "can't take out line by line",
			byHand:  []string{"projects." + chip8Key},
		},
		{
			name:    "a header that escapes a letter of projects, which the edit doesn't read line by line, is refused, as the decoder still reads it as projects",
			file:    "[\"pro" + backslash + "u006aects\"." + chip8Key + "]\nname = \"chip8-emulator\"\n",
			wantErr: "can't take out line by line",
			byHand:  []string{"projects." + chip8Key},
		},
		{
			name:    "a multi-line string in a file with a table of this computer's is refused, whose lines look like a header",
			file:    "[projects." + chip8Key + "]\nname = \"\"\"chip8\n[server]\n\"\"\"\n",
			wantErr: "multi-line string",
			byHand:  []string{"[projects." + chip8Key + "]"},
		},
		{
			name:    "a refused file names an entry of this computer's under a [Projects] table by its key as written, with the header it is under, and no label or comment",
			file:    "[Projects]\n" + chip8Key + " = { name = \"chip8-emulator\" } # the emulator\n",
			wantErr: "can't take out line by line",
			byHand:  []string{chip8Key + " under [Projects]"},
		},
		{
			name:    "a refused file names a dotted key of this computer's at the top of the file up to its =, without its value or comment",
			file:    "projects." + chip8Key + ".name = \"chip8-emulator\" # the emulator\nnote = \"\"\"\nmine\n\"\"\"\n",
			wantErr: "multi-line string",
			byHand:  []string{"projects." + chip8Key + ".name at the top of the file"},
		},
		{
			name:    "a refused file names an inline table and a dotted key of this computer's under [projects] by the header they are under",
			file:    "[display]\nnote = \"\"\"\nmine\n\"\"\"\n\n[projects]\n" + chip8Key + " = { name = \"chip8-emulator\" } # the emulator\n" + notesKey + ".name = \"notes\"\n",
			wantErr: "multi-line string",
			byHand:  []string{chip8Key + " under [projects]", notesKey + ".name under [projects]"},
		},
		{
			name:    "a refused file names an array of tables under one of this computer's tables by its header too",
			file:    "[display]\nnote = \"\"\"\nmine\n\"\"\"\n\n" + chip8 + "\n[[projects." + chip8Key + ".links]]\nurl = \"https://chip8.example\"\n",
			wantErr: "multi-line string",
			byHand:  []string{"[projects." + chip8Key + "]", "[[projects." + chip8Key + ".links]]"},
		},
		{
			name:    "a table of this computer's that also holds keep_prompt_text, which config.toml reads wherever it is, fails the edit, which names its header by hand once",
			file:    "[projects." + chip8Key + "]\nname = \"chip8-emulator\"\nkeep_prompt_text = false\n",
			wantErr: "without changing other settings, so it didn't; delete [projects." + chip8Key + "] in ",
			byHand:  []string{"[projects." + chip8Key + "]"},
		},
		{
			name: "a multi-line string in a file with no table of this computer's is no reason to refuse",
			file: "[display]\nshadow = false\nnote = \"\"\"a\n[projects." + chip8Key + "]\n\"\"\"\n",
			want: "[display]\nshadow = false\nnote = \"\"\"a\n[projects." + chip8Key + "]\n\"\"\"\n",
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
			f, err := ForgetNames(name, mineKeys)
			want := tt.want
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("error %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("error %v, want one containing %q", err, tt.wantErr)
			case tt.wantErr != "":
				want = tt.file
			}
			if f.Left != tt.left || f.Removed != tt.removed || !slices.Equal(f.ByHand, tt.byHand) {
				t.Errorf("Forgotten = %+v, want Left %d, Removed %d and ByHand %q", f, tt.left, tt.removed, tt.byHand)
			}
			if err != nil && strings.Count(err.Error(), "by hand") != 1 {
				t.Errorf("error %q says what to do by hand %d times, want once", err, strings.Count(err.Error(), "by hand"))
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
			if tt.wantErr != "" {
				return
			}
			if keys, ok := projectKeys(strings.TrimPrefix(string(got), bom)); !ok || keys.count(mineKeys) > 0 {
				t.Errorf("tables of this computer's are left among the file's keys: %v (it parses: %v)", keys, ok)
			}
			_, before := Parse([]byte(tt.file))
			_, after := Parse(got)
			for _, p := range after {
				if !slices.Contains(before, p) {
					t.Errorf("the edit added the problem %q", p)
				}
			}
		})
	}
}

func TestProjectTables(t *testing.T) {
	tests := []struct {
		name           string
		file           string // "" means no file
		ofMine, others int
		wantErr        bool
	}{
		{
			name:   "this computer's tables and another's are counted apart, however projects is spelled",
			file:   "[projects." + chip8Key + "]\nname = \"chip8-emulator\"\n\n[Projects." + notesKey + "]\nname = \"notes\"\n\n[projects." + farKey + "]\nname = \"far-away\"\n",
			ofMine: 2, others: 1,
		},
		{
			name: "an inline array of tables at projects names no project's key, and holds none",
			file: "projects = [ { name = \"chip8-emulator\" } ]\nmode = \"local\"\n",
		},
		{
			name: "a file with no project holds none",
			file: "mode = \"local\"\n",
		},
		{
			name: "no file holds none",
		},
		{
			name:    "a file that doesn't parse is an error",
			file:    "mode = [\n",
			wantErr: true,
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
			mine, others, err := ProjectTables(name, mineKeys)
			if (err != nil) != tt.wantErr || mine != tt.ofMine || others != tt.others {
				t.Errorf("ProjectTables = %d, %d, %v, want %d, %d and an error: %v", mine, others, err, tt.ofMine, tt.others, tt.wantErr)
			}
		})
	}
}
