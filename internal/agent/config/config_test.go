// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name        string
		file        string
		want        Config
		wantProblem string // a substring of the one problem expected; "" for none
	}{
		{name: "the default file parses to the defaults with no problems", file: defaultFile, want: Default()},
		{name: "an empty file gives the defaults", file: "", want: Default()},
		{name: "the defaults are local mode and the default port", file: "", want: Config{Mode: Local, HTTP: HTTP{Port: 0}}},
		{name: "mode local is kept", file: `mode = "local"`, want: Default()},
		{
			name:        "a mode this release lacks falls back to local and is reported",
			file:        `mode = "online"`,
			want:        Default(),
			wantProblem: `no mode "online"`,
		},
		{
			name:        "a file that doesn't parse is ignored as a whole",
			file:        "[http]\nport = 8123\n[unclosed\n",
			want:        Default(),
			wantProblem: "was ignored, so every setting has its default",
		},
		{
			name:        "a value of the wrong type makes the whole file ignored",
			file:        "mode = \"local\"\n[http]\nport = \"eighty\"\n",
			want:        Default(),
			wantProblem: "was ignored, so every setting has its default",
		},
		{
			name:        "an unknown setting is reported and ignored",
			file:        "colour = \"blue\"\n",
			want:        Default(),
			wantProblem: `unknown setting "colour"`,
		},
		{name: "a port from 1024 up is used", file: "[http]\nport = 8123\n", want: Config{Mode: Local, HTTP: HTTP{Port: 8123}}},
		{
			name:        "a port below 1024 keeps the default and is reported",
			file:        "[http]\nport = 80\n",
			want:        Default(),
			wantProblem: "http.port 80",
		},
		{
			name:        "a port above 65535 keeps the default and is reported",
			file:        "[http]\nport = 70000\n",
			want:        Default(),
			wantProblem: "http.port 70000",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, problems := Parse([]byte(tt.file))
			if got != tt.want {
				t.Errorf("Parse() = %+v, want %+v", got, tt.want)
			}
			switch {
			case tt.wantProblem == "" && len(problems) != 0:
				t.Errorf("problems = %q, want none", problems)
			case tt.wantProblem != "" && (len(problems) != 1 || !strings.Contains(problems[0], tt.wantProblem)):
				t.Errorf("problems = %q, want one containing %q", problems, tt.wantProblem)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name         string
		setup        func(name string) error
		wantNotExist bool
	}{
		{name: "a missing file gives the defaults and fs.ErrNotExist", setup: func(string) error { return nil }, wantNotExist: true},
		{name: "the file WriteDefault writes loads as the defaults", setup: WriteDefault},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := filepath.Join(t.TempDir(), "config.toml")
			if err := tt.setup(name); err != nil {
				t.Fatal(err)
			}
			cfg, problems, err := Load(name)
			if got := errors.Is(err, fs.ErrNotExist); got != tt.wantNotExist {
				t.Fatalf("Load() error = %v, want fs.ErrNotExist: %v", err, tt.wantNotExist)
			}
			if !tt.wantNotExist && err != nil {
				t.Fatal(err)
			}
			if cfg != Default() || len(problems) != 0 {
				t.Errorf("Load() = %+v with problems %q, want the defaults and none", cfg, problems)
			}
		})
	}
}
