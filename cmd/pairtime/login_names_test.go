// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/uploader"
)

func TestLoginNamesOff(t *testing.T) {
	const newServer = "https://new.example.org"
	tests := []struct {
		name    string
		home    projectsHome
		raw     string // project-names.json's bytes, when set, for one that doesn't read
		server  string // the server login links to
		wantOut []string
		wantErr string // what stderr says; then config.toml stays as it was
		// wantFile, with wantErr, says project-names.json then names the
		// new sign-in; else it stays as it was.
		wantFile    bool
		wantOn      bool // chip8-emulator's name is still on in config.toml
		wantNote    string
		wantPending bool // the note says pairtime couldn't turn it off
	}{
		{
			name:   "a sign-in to another server turns off the names that were on, and lists each with the command that shows it there",
			home:   projectsHome{config: online + chip8Table("chip8-emulator", true), shown: map[string]string{chip8Key: "chip8-emulator"}, codes: serverCodes},
			server: newServer,
			wantOut: []string{
				"\nThe project names this computer had on were for another sign-in. " + newServer + " never showed them, so pairtime turned them off here, and nothing about them goes there. To show one on your dashboard there:\n",
				"  pairtime projects show-name " + chip8Key + "    shows \"chip8-emulator\"\n",
			},
			wantNote: uploader.NoteSignedIn,
		},
		{
			name:   "a project-names.json that doesn't read counts as another sign-in's, since pairtime can't tell, and login says so",
			home:   projectsHome{config: online + chip8Table("chip8-emulator", true)},
			raw:    `{"v":1,"server":`,
			server: newServer,
			wantOut: []string{
				"\npairtime couldn't read which sign-in this computer's project names were for, so it turned off the ones that were on, and nothing about them goes to " + newServer + ". To show one on your dashboard there:\n",
				"  pairtime projects show-name " + chip8Key + "    shows \"chip8-emulator\"\n",
			},
			wantNote: uploader.NoteUnreadable,
		},
		{
			name:    "while config.toml doesn't parse, login leaves project-names.json naming the old sign-in, for the agent, and says so",
			home:    projectsHome{config: online + chip8Table("chip8-emulator", true) + "[unclosed\n", shown: map[string]string{chip8Key: "chip8-emulator"}},
			server:  newServer,
			wantErr: "so pairtime left the project names as they are; once it's fixed, the agent turns off the ones that were on for another sign-in before it sends any\n",
		},
		{
			name:     "while config.toml doesn't parse, login still writes the sign-in into a computer's first project-names.json, so a later sign-in elsewhere finds the names were this one's",
			home:     projectsHome{config: online + chip8Table("chip8-emulator", true) + "[unclosed\n"},
			server:   newServer,
			wantErr:  "so pairtime left the project names as they are",
			wantFile: true,
		},
		{
			name:   "a name config.toml has in a form pairtime can't change is listed apart, with what to write there by hand, and isn't sent",
			home:   projectsHome{config: online + "[projects]\n" + chip8Key + " = { name = \"chip8-emulator\", online = true }\n", shown: map[string]string{chip8Key: "chip8-emulator"}},
			server: newServer,
			wantOut: []string{
				"\nThe project names this computer had on were for another sign-in. " + newServer + " never showed them, and nothing about them goes there.\n",
				"pairtime couldn't turn these off in config.toml, which has them in a form it can't change, such as an inline table, so it sends none of them while config.toml has them on. To show one there, write it in config.toml as a table of its own, [projects.<key>] with its name and online = false, and once pairtime projects says pairtime turned it off here, run pairtime projects show-name with its key:\n  " + chip8Key + "    \"chip8-emulator\"\n",
			},
			wantOn:      true,
			wantNote:    uploader.NoteSignedIn,
			wantPending: true,
		},
		{
			name:   "a sign-in again to the same server, as the same device, changes nothing",
			home:   projectsHome{config: online + chip8Table("chip8-emulator", true), shown: map[string]string{chip8Key: "chip8-emulator"}},
			server: namesServer,
			wantOn: true,
		},
		{
			name:   "a computer's first link keeps the names turned on before it, which go once the server holds its first upload, and its file names this sign-in from now on",
			home:   projectsHome{config: online + chip8Table("chip8-emulator", true)},
			server: newServer,
			wantOn: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := tt.home.write(t)
			names := filepath.Join(h.dirs().Data, uploader.NamesFileName)
			if tt.raw != "" {
				if err := os.WriteFile(names, []byte(tt.raw), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(names)
			var stdout, stderr bytes.Buffer
			cfgBefore, _ := os.ReadFile(h.cfg)
			namesOff(&stdout, &stderr, h.dirs(), tt.server, namesDevice)
			if tt.wantErr != "" {
				if !strings.Contains(stderr.String(), tt.wantErr) || stdout.Len() != 0 {
					t.Errorf("login printed %q and %q, want only %q on stderr", stdout.String(), stderr.String(), tt.wantErr)
				}
				if after, _ := os.ReadFile(h.cfg); string(after) != string(cfgBefore) {
					t.Errorf("config.toml changed to %s", after)
				}
				n, err := uploader.ReadNames(h.dirs().Data)
				switch after, _ := os.ReadFile(names); {
				case tt.wantFile && (err != nil || n.Server != tt.server || n.Device != namesDevice):
					t.Errorf("project-names.json = %+v, %v; want it to name the sign-in login made", n, err)
				case !tt.wantFile && string(after) != string(before):
					t.Errorf("project-names.json changed to %s", after)
				}
				return
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr %q", stderr.String())
			}
			at := 0
			for _, w := range tt.wantOut {
				i := strings.Index(stdout.String()[at:], w)
				if i < 0 {
					t.Fatalf("login printed\n%s\nwant, after what came before, %q", stdout.String(), w)
				}
				at += i + len(w)
			}
			if len(tt.wantOut) == 0 && stdout.Len() != 0 {
				t.Errorf("login printed %q, want nothing", stdout.String())
			}
			cfg, _, err := config.Load(h.cfg)
			if err != nil || cfg.Projects[chip8Key] != (config.Project{Name: "chip8-emulator", Online: tt.wantOn}) {
				t.Errorf("config.toml has %+v, %v; want the name on %v with its label kept", cfg.Projects[chip8Key], err, tt.wantOn)
			}
			n, err := uploader.ReadNames(h.dirs().Data)
			if err != nil || n.Server != tt.server || n.Device != namesDevice {
				t.Errorf("project-names.json = %+v, %v; want it to name the sign-in login made", n, err)
			}
			if note := n.Hidden[chip8Key]; tt.wantNote != "" && (note.Reason != tt.wantNote || note.Pending != tt.wantPending || len(n.Confirmed) != 0) {
				t.Errorf("project-names.json = %+v; want the new sign-in's, with a %q note of the name turned off, pending %v", n, tt.wantNote, tt.wantPending)
			}
		})
	}
}
