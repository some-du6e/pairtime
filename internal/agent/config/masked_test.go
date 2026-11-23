// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"strings"
	"testing"
)

func TestMasked(t *testing.T) {
	tests := []struct {
		name, text, want string
	}{
		{"a comment the teen wrote is masked, and its # stays", "# my secret plan\n", "# ***\n"},
		{"an indented comment keeps its indentation", "  # mine\n", "  # ***\n"},
		{"a string value is masked, and its quotes stay", "name = \"chip8-emulator\"\n", "name = \"***\"\n"},
		{"a literal string is masked too", "name = 'chip8-emulator'\n", "name = '***'\n"},
		{"an empty string stays empty", "label = \"\"\n", "label = \"\"\n"},
		{"a comment after a value is masked, and the value stays", "port = 8123 # my port\n", "port = 8123 # ***\n"},
		{"a # inside a string isn't a comment", "name = \"a#b\" # mine\n", "name = \"***\" # ***\n"},
		{"an escaped quote doesn't end a string", "name = \"a\\\"b\" # mine\n", "name = \"***\" # ***\n"},
		{"a quoted key is masked, and a bare key stays", "\"my key\" = 1\nbare = 2\n", "\"***\" = 1\nbare = 2\n"},
		{"the quoted part of a table header is masked", "[projects.\"my game\"]\n", "[projects.\"***\"]\n"},
		{"a project's name is masked, and its table header, an opaque key, stays",
			"[projects.g7c41e9a2d05b8f63e1a94c7d2b6f0e85]\nname = \"secret-game\"\nonline = true\n",
			"[projects.g7c41e9a2d05b8f63e1a94c7d2b6f0e85]\nname = \"***\"\nonline = true\n"},
		{"an array's strings are masked", "list = [\"a\", 'b']\n", "list = [\"***\", '***']\n"},
		{"table headers, numbers, true, false and dates stay", "[display]\nshadow = true\nport = 7247\nwhen = 2026-12-01T00:00:00Z\n", "[display]\nshadow = true\nport = 7247\nwhen = 2026-12-01T00:00:00Z\n"},
		{"lines of pairtime's default config.toml stay, their comments and strings too",
			"# The port of pairtime's web server, which listens on 127.0.0.1 only.\nmode = \"local\"\n",
			"# The port of pairtime's web server, which listens on 127.0.0.1 only.\nmode = \"local\"\n"},
		{"the mode line pairtime login writes stays", "mode = \"online\"\n", "mode = \"online\"\n"},
		{"a server's address is masked, since the teen may have written it", "url = \"https://pairtime.example.org\"\n", "url = \"***\"\n"},
		{"CRLF line endings stay", "# mine\r\nname = \"x\"\r\n", "# ***\r\nname = \"***\"\r\n"},
		{"a last line without a line ending keeps none", "# mine", "# ***"},
		{"a string that isn't closed is masked to the end of its line", "name = \"open\n", "name = \"***\n"},
		{"in a file with a multi-line string every line is masked whole", "a = \"\"\"\nsecret\n\"\"\"\nshadow = true\n", "***\n***\n***\n***\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(Masked([]byte(tt.text))); got != tt.want {
				t.Errorf("Masked(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}

	t.Run("no file stays no file, and an empty one stays empty", func(t *testing.T) {
		if got := Masked(nil); got != nil {
			t.Errorf("Masked(nil) = %q, want nil", got)
		}
		if got := Masked([]byte{}); got == nil || len(got) != 0 {
			t.Errorf("Masked of an empty file = %#v, want an empty file", got)
		}
	})

	t.Run("pairtime's default config.toml shows as it is", func(t *testing.T) {
		if got := string(Masked([]byte(defaultFile))); got != defaultFile {
			t.Errorf("Masked changed the default file:\n%s", got)
		}
	})

	t.Run("every line stays a line, so a diff of two masked files lines up as the files do", func(t *testing.T) {
		text := "# a\nname = \"x\" # b\n[projects.\"c\"]\n\nkey = 'd'\r\nlast"
		if got := Masked([]byte(text)); strings.Count(string(got), "\n") != strings.Count(text, "\n") {
			t.Errorf("Masked(%q) = %q, with another number of lines", text, got)
		}
	})
}
