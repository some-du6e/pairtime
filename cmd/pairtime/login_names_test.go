// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/uploader"
)

func TestLoginNamesOff(t *testing.T) {
	const newServer = "https://new.example.org"
	tests := []struct {
		name     string
		home     projectsHome
		server   string // the server login links to
		wantOut  []string
		wantOn   bool // chip8-emulator's name is still on in config.toml
		wantNote string
	}{
		{
			name:   "a sign-in to another server turns off the names that were on, and lists each with the command that shows it there",
			home:   projectsHome{config: online + chip8Table("chip8-emulator", true), shown: map[string]string{chip8Key: "chip8-emulator"}, codes: serverCodes},
			server: newServer,
			wantOut: []string{
				"\nThis computer showed project names on your dashboard on another sign-in. " + newServer + " never showed them, so pairtime turned them off here, and nothing about them goes there. To show one on your dashboard there:\n",
				"  pairtime projects show-name " + chip8Key + "    shows \"chip8-emulator\"\n",
			},
			wantNote: uploader.NoteSignedIn,
		},
		{
			name:   "a sign-in again to the same server, as the same device, changes nothing",
			home:   projectsHome{config: online + chip8Table("chip8-emulator", true), shown: map[string]string{chip8Key: "chip8-emulator"}},
			server: namesServer,
			wantOn: true,
		},
		{
			name:   "a computer's first link keeps the names turned on before it, which go once the server holds its first upload",
			home:   projectsHome{config: online + chip8Table("chip8-emulator", true)},
			server: newServer,
			wantOn: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := tt.home.write(t)
			var stdout, stderr bytes.Buffer
			namesOff(&stdout, &stderr, h.dirs(), tt.server, namesDevice)
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
			if tt.wantNote == "" {
				return
			}
			n, err := uploader.ReadNames(h.dirs().Data)
			if note := n.Hidden[chip8Key]; err != nil || note.Reason != tt.wantNote || n.Server != tt.server || len(n.Confirmed) != 0 {
				t.Errorf("project-names.json = %+v, %v; want the new sign-in's, with a note of the name turned off", n, err)
			}
		})
	}
}
