// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Made-up project keys in the git adapter's form.
const (
	chip8Key = "g7c41e9a2d05b8f63e1a94c7d2b6f0e85"
	plantKey = "g5be03d7f18a6c92e4b07d5a1c8f3e962"
)

func TestParseProjects(t *testing.T) {
	tests := []struct {
		name         string
		file         string
		want         map[string]Project
		mode         Mode     // Local when ""
		wantProblems []string // a substring of each problem, in order
	}{
		{
			name: "a project's table with online = true and a name shows the name",
			file: "[projects." + chip8Key + "]\nname = \"chip8-emulator\"\nonline = true\n",
			want: map[string]Project{chip8Key: {Name: "chip8-emulator", Online: true}},
		},
		{
			name: "a name is off unless its table says online = true, and its label is kept",
			file: "[projects." + chip8Key + "]\nname = \"chip8-emulator\"\n",
			want: map[string]Project{chip8Key: {Name: "chip8-emulator"}},
		},
		{
			name: "online = false keeps the name off",
			file: "[projects." + chip8Key + "]\nname = \"chip8-emulator\"\nonline = false\n",
			want: map[string]Project{chip8Key: {Name: "chip8-emulator"}},
		},
		{
			name:         "online = true without a name keeps the name off, and says so",
			file:         "[projects." + chip8Key + "]\nonline = true\n",
			want:         map[string]Project{chip8Key: {}},
			wantProblems: []string{"says online = true but has no name"},
		},
		{
			name:         "a label with a space at its end isn't one pairtime can show, so the name stays off",
			file:         "[projects." + chip8Key + "]\nname = \"chip8 \"\nonline = true\n",
			want:         map[string]Project{chip8Key: {}},
			wantProblems: []string{`projects.` + chip8Key + `.name "chip8 " isn't a name pairtime can show`},
		},
		{
			name:         "a label with a bidirectional override isn't one either",
			file:         "[projects." + chip8Key + "]\nname = \"chip8" + string(rune(0x202e)) + "\"\nonline = true\n",
			want:         map[string]Project{chip8Key: {}},
			wantProblems: []string{"isn't a name pairtime can show"},
		},
		{
			name:         "a name that isn't text keeps the name off, and the rest of the file stays in force",
			file:         "mode = \"online\"\n[projects." + chip8Key + "]\nname = 8\nonline = true\n",
			mode:         Online,
			want:         map[string]Project{chip8Key: {}},
			wantProblems: []string{".name isn't text"},
		},
		{
			name:         "online that isn't true or false keeps the name off",
			file:         "[projects." + chip8Key + "]\nname = \"chip8-emulator\"\nonline = \"yes\"\n",
			want:         map[string]Project{chip8Key: {Name: "chip8-emulator"}},
			wantProblems: []string{".online isn't true or false"},
		},
		{
			name:         "an unknown setting in a project's table keeps its name off, since it may be a misspelled online = false",
			file:         "[projects." + chip8Key + "]\nname = \"chip8-emulator\"\nonline = true\nonlin = false\n",
			want:         map[string]Project{chip8Key: {Name: "chip8-emulator"}},
			wantProblems: []string{`the unknown setting "projects.` + chip8Key + `.onlin" was ignored, so the project's name stays off`},
		},
		{
			name:         "a key that isn't a project's key is ignored, and another project's name still shows",
			file:         "[projects.\"chip 8\"]\nname = \"x\"\nonline = true\n[projects." + plantKey + "]\nname = \"plant-sensor\"\nonline = true\n",
			want:         map[string]Project{plantKey: {Name: "plant-sensor", Online: true}},
			wantProblems: []string{`projects."chip 8" isn't a project's key`},
		},
		{
			name:         "a project that isn't a table is ignored",
			file:         "[projects]\n" + chip8Key + " = \"chip8-emulator\"\n",
			wantProblems: []string{"projects." + chip8Key + " isn't a table"},
		},
		{
			name:         "projects that isn't a table of projects shows no name",
			file:         "projects = 5\n",
			wantProblems: []string{"projects isn't a table of projects"},
		},
		{
			name: "an inline table and dotted keys are tables too",
			file: "[projects]\n" + chip8Key + " = { name = \"chip8-emulator\", online = true }\n" + plantKey + ".name = \"plant-sensor\"\n",
			want: map[string]Project{chip8Key: {Name: "chip8-emulator", Online: true}, plantKey: {Name: "plant-sensor"}},
		},
		{
			name:         "a table spelled [Projects.<key>] is an unknown setting and shows no name",
			file:         "[Projects." + chip8Key + "]\nonline = true\n",
			wantProblems: []string{`unknown setting "Projects.` + chip8Key + `"`, `unknown setting "Projects.` + chip8Key + `.online"`},
		},
		{
			name: "the default file shows no name, with no problems",
			file: defaultFile,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, problems := Parse([]byte(tt.file))
			if !reflect.DeepEqual(cfg.Projects, tt.want) {
				t.Errorf("Projects = %+v, want %+v", cfg.Projects, tt.want)
			}
			mode := tt.mode
			if mode == "" {
				mode = Local
			}
			if cfg.Mode != mode || cfg.Notifications.Off {
				t.Errorf("the rest of the file isn't in force: mode %q, notifications off %v", cfg.Mode, cfg.Notifications.Off)
			}
			if len(problems) != len(tt.wantProblems) {
				t.Fatalf("problems = %q, want %d", problems, len(tt.wantProblems))
			}
			for i, want := range tt.wantProblems {
				if !strings.Contains(problems[i], want) {
					t.Errorf("problem %d = %q, want one containing %q", i, problems[i], want)
				}
			}
		})
	}
}

func TestNamesOn(t *testing.T) {
	tests := []struct {
		name string
		file string
		want map[string]string
	}{
		{name: "no project table turns no name on", file: "", want: map[string]string{}},
		{
			name: "only names with online = true are on, each with its label",
			file: "[projects." + chip8Key + "]\nname = \"chip8-emulator\"\nonline = true\n[projects." + plantKey + "]\nname = \"plant-sensor\"\n",
			want: map[string]string{chip8Key: "chip8-emulator"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, _ := Parse([]byte(tt.file))
			if got := cfg.NamesOn(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NamesOn() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNameEdits(t *testing.T) {
	show := func(key, label string) func(string) error {
		return func(name string) error { return ShowName(name, key, label) }
	}
	hide := func(key string) func(string) error {
		return func(name string) error { return HideName(name, key) }
	}
	table := "[projects." + chip8Key + "]\n"
	tests := []struct {
		name    string
		file    string // "" means no file
		edit    func(name string) error
		want    string
		wantErr string
	}{
		{
			name: "showing a name adds the project's table at the end, and changes nothing else",
			file: "mode = \"online\"\n# mine\n[http]\nport = 8123\n",
			edit: show(chip8Key, "chip8-emulator"),
			want: "mode = \"online\"\n# mine\n[http]\nport = 8123\n\n" + table + "name = \"chip8-emulator\"\nonline = true\n",
		},
		{
			name: "showing a name in a missing file writes the default file with the table",
			edit: show(chip8Key, "chip8-emulator"),
			want: defaultFile + "\n" + table + "name = \"chip8-emulator\"\nonline = true\n",
		},
		{
			name: "showing a name rewrites the table's lines and keeps their comments",
			file: table + "name = 'old name'   # mine\nonline = false # for now\n",
			edit: show(chip8Key, "chip8-emulator"),
			want: table + "name = \"chip8-emulator\"   # mine\nonline = true # for now\n",
		},
		{
			name: "showing a name adds what the table lacks just under its header",
			file: table + "# the emulator\n[http]\nport = 8123\n",
			edit: show(chip8Key, "chip8-emulator"),
			want: table + "name = \"chip8-emulator\"\nonline = true\n# the emulator\n[http]\nport = 8123\n",
		},
		{
			name: "a quote and a backslash in a label are escaped",
			file: "",
			edit: show(chip8Key, `say "hi" \o/`),
			want: defaultFile + "\n" + table + `name = "say \"hi\" \\o/"` + "\nonline = true\n",
		},
		{
			name: "a header written with blanks and a quoted key is the project's table",
			file: "[ projects . \"" + chip8Key + "\" ]\nname = \"chip8-emulator\"\n",
			edit: show(chip8Key, "chip8-emulator"),
			want: "[ projects . \"" + chip8Key + "\" ]\nonline = true\nname = \"chip8-emulator\"\n",
		},
		{
			name: "another project's table isn't touched",
			file: "[projects." + plantKey + "]\nname = \"plant-sensor\"\nonline = true\n",
			edit: show(chip8Key, "chip8-emulator"),
			want: "[projects." + plantKey + "]\nname = \"plant-sensor\"\nonline = true\n\n" + table + "name = \"chip8-emulator\"\nonline = true\n",
		},
		{
			name: "CRLF line endings stay",
			file: "mode = \"local\"\r\n" + strings.TrimSuffix(table, "\n") + "\r\nname = \"a\"\r\n",
			edit: show(chip8Key, "chip8-emulator"),
			want: "mode = \"local\"\r\n" + strings.TrimSuffix(table, "\n") + "\r\nonline = true\r\nname = \"chip8-emulator\"\r\n",
		},
		{
			name: "hiding a name sets online = false and keeps its label, so showing it again can keep it",
			file: table + "name = \"chip8-emulator\"\nonline = true # shown\n",
			edit: hide(chip8Key),
			want: table + "name = \"chip8-emulator\"\nonline = false # shown\n",
		},
		{
			name: "hiding the name of a project with no table changes nothing",
			file: "mode = \"online\"\n",
			edit: hide(chip8Key),
			want: "mode = \"online\"\n",
		},
		{
			name:    "a project in an inline table isn't rewritten, and the error says what to write",
			file:    "[projects]\n" + chip8Key + " = { name = \"chip8-emulator\", online = true }\n",
			edit:    hide(chip8Key),
			want:    "[projects]\n" + chip8Key + " = { name = \"chip8-emulator\", online = true }\n",
			wantErr: "set online = false under [projects." + chip8Key + "]",
		},
		{
			name:    "a file that doesn't parse isn't changed",
			file:    "mode = \"local\"\n[unclosed\n",
			edit:    show(chip8Key, "chip8-emulator"),
			want:    "mode = \"local\"\n[unclosed\n",
			wantErr: `set name = "chip8-emulator" and online = true under [projects.` + chip8Key + "]",
		},
		{
			name:    "a label projectname.ValidLabel refuses isn't written",
			file:    "mode = \"local\"\n",
			edit:    show(chip8Key, " chip8"),
			want:    "mode = \"local\"\n",
			wantErr: "isn't a name pairtime can show",
		},
		{
			name:    "a key that isn't a project's key isn't written",
			file:    "mode = \"local\"\n",
			edit:    show("chip 8", "chip8-emulator"),
			want:    "mode = \"local\"\n",
			wantErr: "isn't a project's key",
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
			err := tt.edit(name)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("error %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("error %v, want one containing %q", err, tt.wantErr)
			}
			got, rerr := os.ReadFile(name)
			if rerr != nil {
				t.Fatal(rerr)
			}
			if string(got) != tt.want {
				t.Errorf("config.toml is\n%q\nwant\n%q", got, tt.want)
			}
			if tt.wantErr == "" {
				if _, problems := Parse(got); len(problems) != 0 {
					t.Errorf("the edit left problems: %q", problems)
				}
			}
		})
	}
}
