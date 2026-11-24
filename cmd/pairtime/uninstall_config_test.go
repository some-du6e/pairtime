// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/home"
)

// TestUninstallConfig checks what uninstall does to a config.toml behind a
// link before it deletes pairtime's data (configNames), then that data as
// it deletes it (clearData): a config.toml a symlink points to loses only
// the tables of the projects this computer knows of, and the link goes;
// that of a linked config folder loses them too, and stays with its lock;
// and one in a folder of pairtime's own is left to go whole with the data
// (D-wipe-project-labels).
func TestUninstallConfig(t *testing.T) {
	const (
		known  = "mode = \"local\"\n\n[projects." + wipeProjectKey + "]\nname = \"chip8-emulator\"\n\n[projects." + farProjectKey + "]\nname = \"far-away\"\n"
		others = "mode = \"local\"\n\n[projects." + farProjectKey + "]\nname = \"far-away\"\n"
	)
	tests := []struct {
		name string
		// link is how config.toml reaches its file: notLinked, linkedFile or
		// linkedFolder; dangling makes linkedFile's link point nowhere.
		link     configLink
		dangling bool
		file     string // what config.toml's file holds before
		mine     bool   // this computer knows of wipeProjectKey
		want     string // what the file holds after, its link gone
		keep     bool   // configNames says to keep config.toml
		gone     bool   // clearData deletes the file
		// out is a part of what configNames prints, "" for nothing, and
		// problem a part of what has to stay, "" for nothing.
		out, problem string
	}{
		{
			name: "a config.toml in a folder of pairtime's own is left for clearData to delete whole, every table with it",
			link: notLinked, file: known, mine: true, gone: true,
		},
		{
			name: "a config.toml that is a symlink loses this computer's tables in the file it points to, another project's table stays byte for byte, and only the link goes",
			link: linkedFile, file: known, mine: true, want: others,
			out: "Took the names you gave the projects this computer knows of out of ",
		},
		{
			name: "with the config folder a link, config.toml and config.lock stay in the folder it leads to, config.toml without this computer's tables, and uninstall says where",
			link: linkedFolder, file: known, mine: true, want: others, keep: true,
			out: "config.toml stays in ",
		},
		{
			name: "a refused edit of a linked folder's config.toml leaves it as it was, names its header to delete by hand there, never its label",
			link: linkedFolder, file: "mode = \"local\"\n\n[Projects." + wipeProjectKey + "] # robotics\nname = \"chip8-emulator\"\n", mine: true,
			want: "mode = \"local\"\n\n[Projects." + wipeProjectKey + "] # robotics\nname = \"chip8-emulator\"\n", keep: true,
			out: "config.toml stays in ", problem: "the names you gave some of the projects this computer knows of (",
		},
		{
			name: "with no project's key this computer knows of, a linked folder's config.toml stays as it is, and nothing has to stay",
			link: linkedFolder, file: known, want: known, keep: true,
			out: "config.toml stays in ",
		},
		{
			name: "a symlinked config.toml whose file is gone loses its link, and what has to stay says pairtime couldn't follow it",
			link: linkedFile, dangling: true, mine: true,
			problem: "a link pairtime can't follow",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			shared := filepath.Join(root, "shared") // the folder another computer may share
			dirs := home.Dirs{Data: filepath.Join(root, "data"), Config: filepath.Join(root, "config")}
			for _, d := range []string{shared, dirs.Data} {
				if err := os.MkdirAll(d, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			file := filepath.Join(shared, "config.toml") // the file config.toml reaches
			if !tt.dangling {
				if err := os.WriteFile(file, []byte(tt.file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			switch tt.link {
			case notLinked:
				if err := os.MkdirAll(dirs.Config, 0o700); err != nil {
					t.Fatal(err)
				}
				file = dirs.ConfigFile()
				if err := os.WriteFile(file, []byte(tt.file), 0o600); err != nil {
					t.Fatal(err)
				}
			case linkedFile:
				if err := os.MkdirAll(dirs.Config, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(file, dirs.ConfigFile()); err != nil {
					t.Skipf("this system can't make a symlink here: %v", err)
				}
			case linkedFolder:
				if err := os.Symlink(shared, dirs.Config); err != nil {
					t.Skipf("this system can't make a symlink here: %v", err)
				}
			}
			mine := keySet{}
			if tt.mine {
				mine[wipeProjectKey] = true
			}
			var out bytes.Buffer
			keep, problem := configNames(&out, dirs, mine)
			if keep != tt.keep || (tt.problem == "") != (problem == "") || !strings.Contains(problem, tt.problem) {
				t.Errorf("configNames = %v, %q, want %v and a problem saying %q", keep, problem, tt.keep, tt.problem)
			}
			if (tt.out == "") != (out.Len() == 0) || !strings.Contains(out.String(), tt.out) || strings.Contains(out.String()+problem, "chip8-emulator") || strings.Contains(out.String()+problem, "far-away") {
				t.Errorf("configNames printed %q, want it to say %q, and no label", out.String(), tt.out)
			}
			var keepHere []string
			if keep {
				keepHere = []string{"config.toml", config.LockName}
			}
			if err := clearData(dirs.Config, keepHere, nil); err != nil {
				t.Fatal(err)
			}
			if tt.link == linkedFile && exists(t, dirs.Config, "config.toml") {
				t.Error("config.toml's link is still there")
			}
			got, err := os.ReadFile(file)
			switch {
			case tt.gone || tt.dangling:
				if err == nil {
					t.Errorf("%s is still there:\n%s", file, got)
				}
			case err != nil:
				t.Fatal(err)
			case string(got) != tt.want:
				t.Errorf("%s holds\n%q\nwant\n%q", file, got, tt.want)
			}
		})
	}
}

// TestConfigFate checks what uninstall's screen and its dry run say of a
// config.toml behind a link, and that they say nothing of one uninstall
// deletes with pairtime's data.
func TestConfigFate(t *testing.T) {
	tests := []struct {
		name  string
		link  configLink
		known bool
		dry   bool
		want  string // a part of the sentence, "" for none
	}{
		{name: "a symlinked config.toml's file loses this computer's names and keeps the rest, and the link goes", link: linkedFile, known: true, want: "uninstall takes out of that file the names you gave the projects this computer knows of, leaves the rest of it, and deletes the link."},
		{name: "the dry run says the same of what it would do", link: linkedFile, known: true, dry: true, want: "uninstall would take out of that file the names you gave the projects this computer knows of, would leave the rest of it, and would delete the link."},
		{name: "a linked config folder's config.toml stays, without this computer's names", link: linkedFolder, known: true, want: "since pairtime's config folder is a link to a folder another computer may share: uninstall takes out of it only the names you gave the projects this computer knows of."},
		{name: "with no project's key this computer knows of, a linked folder's config.toml stays as it is", link: linkedFolder, want: "as it is, since pairtime's config folder is a link to a folder another computer may share."},
		{name: "a config.toml in a folder of pairtime's own goes with the data, so nothing is said of it", link: notLinked, known: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			shared := filepath.Join(root, "shared")
			dirs := home.Dirs{Data: filepath.Join(root, "data"), Config: filepath.Join(root, "config")}
			if err := os.MkdirAll(shared, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(shared, "config.toml"), []byte("mode = \"local\"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			switch tt.link {
			case notLinked:
				if err := os.MkdirAll(dirs.Config, 0o700); err != nil {
					t.Fatal(err)
				}
			case linkedFile:
				if err := os.MkdirAll(dirs.Config, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(shared, "config.toml"), dirs.ConfigFile()); err != nil {
					t.Skipf("this system can't make a symlink here: %v", err)
				}
			case linkedFolder:
				if err := os.Symlink(shared, dirs.Config); err != nil {
					t.Skipf("this system can't make a symlink here: %v", err)
				}
			}
			got := configFate(dirs, tt.known, tt.dry)
			if (tt.want == "") != (got == "") || !strings.Contains(got, tt.want) {
				t.Errorf("configFate = %q, want it to say %q", got, tt.want)
			}
		})
	}
}
