// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/internal/agent/uploader"
)

func TestDescribeNames(t *testing.T) {
	body := `{"delete":["` + folderKey + `"],"set":[{"project":"` + chip8Key + `","label":"chip8-emulator"}]}`
	tests := []struct {
		name    string
		p       uploader.NamesPreview
		want    []string // what it says, each in order
		notWant []string
	}{
		{
			name: "a request that shows one name and stops showing another prints all of its body, byte for byte, then what it does on whose dashboard",
			p: uploader.NamesPreview{JSON: []byte(body), Set: map[string]string{chip8Key: "chip8-emulator"}, Delete: []string{folderKey},
				Codes: map[string]string{chip8Key: "7c41e9"}},
			want: []string{
				"Before its next upload, this computer sends a project names request to\nhttps://pairtime.example.org/api/v1/projects/names, signed with its device key like an upload.\n",
				"This is all of its body, before pairtime compresses it with gzip:\n\n" + body + "\n\n",
				"It shows the name \"chip8-emulator\" for Project 7c41e9, and it stops showing the name of the project " + folderKey + " on your own pairtime dashboard.",
				"Only you see these names there, and\nnothing else about these projects goes with them.\n",
			},
		},
		{
			name:    "a name that would reach the terminal with a control character in it prints a replacement character",
			p:       uploader.NamesPreview{JSON: []byte(`{}`), Set: map[string]string{chip8Key: "chip8\x1b[31m"}},
			want:    []string{"It shows the name \"chip8\uFFFD[31m\" for the project " + chip8Key},
			notWant: []string{"\x1b"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := describeNames(tt.p, "https://pairtime.example.org/")
			at := 0
			for _, w := range tt.want {
				i := strings.Index(out[at:], w)
				if i < 0 {
					t.Fatalf("privacy show said\n%s\nwant, after what came before, %q", out, w)
				}
				at += i + len(w)
			}
			for _, w := range tt.notWant {
				if strings.Contains(out, w) {
					t.Errorf("privacy show said %q", w)
				}
			}
		})
	}
}
