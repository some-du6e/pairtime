// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestCheckServerURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string // "" means refused
	}{
		{name: "an https origin is kept", raw: "https://pairtime.karimeltaib.com", want: "https://pairtime.karimeltaib.com"},
		{name: "a trailing slash is dropped", raw: "https://pairtime.example.org/", want: "https://pairtime.example.org"},
		{name: "the scheme and host go to lower case", raw: "HTTPS://Pairtime.Example.ORG", want: "https://pairtime.example.org"},
		{name: "a port is kept", raw: "https://pairtime.example.org:8443", want: "https://pairtime.example.org:8443"},
		{name: "http to localhost is allowed, for local runs", raw: "http://localhost:7300", want: "http://localhost:7300"},
		{name: "http to a loopback address is allowed, for tests", raw: "http://127.0.0.1:9000", want: "http://127.0.0.1:9000"},
		{name: "http to another computer is refused", raw: "http://pairtime.example.org"},
		{name: "a path is refused", raw: "https://example.org/pairtime"},
		{name: "a query is refused", raw: "https://example.org/?token=x"},
		{name: "a user name is refused", raw: "https://teen@example.org"},
		{name: "another scheme is refused", raw: "ftp://example.org"},
		{name: "a bare host name is refused", raw: "pairtime.example.org"},
		{name: "an empty string is refused", raw: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CheckServerURL(tt.raw)
			if (err == nil) != (tt.want != "") || got != tt.want {
				t.Errorf("CheckServerURL(%q) = %q, %v; want %q", tt.raw, got, err, tt.want)
			}
		})
	}
}

func TestServerFromFile(t *testing.T) {
	tests := []struct {
		name    string
		file    string // "" means no file
		want    string
		wantErr bool
	}{
		{name: "no file names no server", want: ""},
		{name: "a file without [server] names no server", file: "mode = \"local\"\n", want: ""},
		{name: "an empty url names no server", file: "[server]\nurl = \"\"\n", want: ""},
		{name: "a url comes back as CheckServerURL writes it", file: "[server]\nurl = \"https://Pairtime.Example.org/\"\n", want: "https://pairtime.example.org"},
		{name: "a url CheckServerURL refuses is an error, not the default server", file: "[server]\nurl = \"http://pairtime.example.org\"\n", wantErr: true},
		{name: "a file that doesn't parse is an error, not the default server", file: "[server\nurl = \"https://pairtime.example.org\"\n", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := filepath.Join(t.TempDir(), "config.toml")
			if tt.file != "" {
				if err := os.WriteFile(name, []byte(tt.file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := ServerFromFile(name)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("ServerFromFile = %q, %v; want %q, error %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestSetModeThroughSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles", "pairtime.toml")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("mode = \"local\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "config.toml")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("this system can't make a symlink here: %v", err)
	}
	if err := SetOnline(link, "https://pairtime.example.org"); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		ok   func() bool
	}{
		{name: "config.toml is still a symlink", ok: func() bool {
			fi, err := os.Lstat(link)
			return err == nil && fi.Mode()&os.ModeSymlink != 0
		}},
		{name: "the file it points to is the one switched online", ok: func() bool {
			b, err := os.ReadFile(target)
			return err == nil && strings.HasPrefix(string(b), "mode = \"online\"\n")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok() {
				t.Error("it isn't")
			}
		})
	}
}

func TestSetMode(t *testing.T) {
	const server = "https://pairtime.example.org"
	online := func(name string) error { return SetOnline(name, server) }
	tests := []struct {
		name    string
		file    string // "" means no file
		set     func(name string) error
		want    string
		wantErr string
	}{
		{
			name: "online in the default file changes only the mode and url lines",
			file: defaultFile,
			set:  online,
			want: strings.Replace(strings.Replace(defaultFile, "\nmode = \"local\"\n", "\nmode = \"online\"\n", 1), "\nurl = \"\"\n", "\nurl = \""+server+"\"\n", 1),
		},
		{
			name: "local after online changes only the mode line back, and keeps the server",
			file: strings.Replace(defaultFile, "\nmode = \"local\"\n", "\nmode = \"online\"\n", 1),
			set:  SetLocal,
			want: defaultFile,
		},
		{
			name: "a missing file is written from the default, switched online",
			set:  online,
			want: strings.Replace(strings.Replace(defaultFile, "\nmode = \"local\"\n", "\nmode = \"online\"\n", 1), "\nurl = \"\"\n", "\nurl = \""+server+"\"\n", 1),
		},
		{
			name: "a file without the mode and [server] gets both, and keeps the rest",
			file: "# mine\n[http]\nport = 8123\n",
			set:  online,
			want: "mode = \"online\"\n# mine\n[http]\nport = 8123\n\n[server]\nurl = \"" + server + "\"\n",
		},
		{
			name: "a [server] table without url gets one under its header",
			file: "mode = \"local\"\n\n[server]\n# where to link\n",
			set:  online,
			want: "mode = \"online\"\n\n[server]\nurl = \"" + server + "\"\n# where to link\n",
		},
		{
			name: "CRLF line endings, indentation and other tables stay as they are",
			file: "# a\r\n  mode = \"local\"\r\n[notifications]\r\nenabled = false\r\n[server]\r\n  url = \"https://old.example.org\"\r\n",
			set:  online,
			want: "# a\r\n  mode = \"online\"\r\n[notifications]\r\nenabled = false\r\n[server]\r\n  url = \"" + server + "\"\r\n",
		},
		{
			name: "a comment after the value stays, on the mode line and the url line",
			file: "mode = \"local\"   # set by me\n[server]\nurl = 'https://old.example.org' # my club's\n",
			set:  online,
			want: "mode = \"online\"   # set by me\n[server]\nurl = \"" + server + "\" # my club's\n",
		},
		{
			name: "a url under another table isn't touched",
			file: "mode = \"local\"\n[proxy]\nurl = \"https://proxy.example.org\"\n",
			set:  online,
			want: "mode = \"online\"\n[proxy]\nurl = \"https://proxy.example.org\"\n\n[server]\nurl = \"" + server + "\"\n",
		},
		{
			name: "a file that already says so is left alone",
			file: "mode = \"local\"\n",
			set:  SetLocal,
			want: "mode = \"local\"\n",
		},
		{
			name:    "a file that doesn't parse isn't changed, and the error says what to write",
			file:    "mode = \"local\"\n[unclosed\n",
			set:     online,
			want:    "mode = \"local\"\n[unclosed\n",
			wantErr: `set mode = "online" and, under [server], url = "` + server + `"`,
		},
		{
			name:    "a server set as a dotted key isn't rewritten into a second table",
			file:    "mode = \"local\"\nserver.url = \"https://old.example.org\"\n",
			set:     online,
			want:    "mode = \"local\"\nserver.url = \"https://old.example.org\"\n",
			wantErr: "by hand",
		},
		{
			name:    "a server that CheckServerURL refuses isn't written",
			file:    "mode = \"local\"\n",
			set:     func(name string) error { return SetOnline(name, "http://pairtime.example.org") },
			want:    "mode = \"local\"\n",
			wantErr: "uses http",
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
			err := tt.set(name)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("error %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("error %v, want one containing %q", err, tt.wantErr)
			}
			got, rerr := os.ReadFile(name)
			if rerr != nil && tt.file == "" && tt.wantErr != "" {
				return
			}
			if rerr != nil {
				t.Fatal(rerr)
			}
			if string(got) != tt.want {
				t.Errorf("config.toml is\n%q\nwant\n%q", got, tt.want)
			}
			if tt.wantErr == "" {
				_, was := Parse([]byte(tt.file))
				if _, problems := Parse(got); len(problems) > len(was) {
					t.Errorf("the edit added problems: %q, before %q", problems, was)
				}
			}
		})
	}
}

// olderDefault is the config.toml pairtime wrote before [privacy], as
// 0.1.0-alpha.5 writes it: it ends in [notifications].
const olderDefault = `# pairtime settings. The agent reads this file when it starts and again
# whenever you save it.

# Where your time goes. "local" keeps everything on this computer: nothing
# is uploaded, and recaps still work. "online" also uploads the minutes
# pairtime counts, as numbers, to the server you linked with pairtime
# login. pairtime login and pairtime logout change this line.
mode = "local"

[http]
# The port of pairtime's web server, which listens on 127.0.0.1 only.
# 0 means the default, 7247, or a free port near it when that one is taken.
port = 0

[server]
# The pairtime server that pairtime login links this computer to. ""
# means https://pairtime.karimeltaib.com.
url = ""

[display]
# true shows your time as "calibrating" while pairtime's rules are being
# fitted. Views then keep the agent's time, your moments and the loops
# pairtime caught, and leave out everything your time could be worked out
# from. Calibration volunteers have it on until the scoring freeze.
shadow = false

[notifications]
# One quiet notification when a work session ends, to say its recap is
# ready. false turns them off.
enabled = true
# A time of day with no notifications, in this computer's local time, such
# as "22:00-07:00". "" means none.
quiet_hours = ""
`

func TestSetKeepText(t *testing.T) {
	off := func(name string) error { return SetKeepText(name, false) }
	on := func(name string) error { return SetKeepText(name, true) }
	offDefault := strings.Replace(defaultFile, "\nkeep_prompt_text = true\n", "\nkeep_prompt_text = false\n", 1)
	tests := []struct {
		name    string
		file    string // "" means no file
		set     func(name string) error
		want    string
		wantErr string
	}{
		{name: "off in the default file changes only the switch's line", file: defaultFile, set: off, want: offDefault},
		{name: "on after off changes the line back", file: offDefault, set: on, want: defaultFile},
		{name: "a missing file is written from the default, with the switch off", set: off, want: offDefault},
		{
			name: "a file without [privacy], as an older pairtime wrote, gets the table at its end",
			file: olderDefault,
			set:  off,
			want: olderDefault + "\n[privacy]\nkeep_prompt_text = false\n",
		},
		{
			name: "a [privacy] table without the switch gets it under its header",
			file: "mode = \"local\"\n\n[privacy]\n# mine\n",
			set:  off,
			want: "mode = \"local\"\n\n[privacy]\nkeep_prompt_text = false\n# mine\n",
		},
		{
			name: "the table and the switch are found whatever their case, and a comment after the value stays",
			file: "[Privacy]\nKeep_Prompt_Text = true # mine\n",
			set:  off,
			want: "[Privacy]\nKeep_Prompt_Text = false # mine\n",
		},
		{
			name: "CRLF line endings stay, in the table added too",
			file: "mode = \"local\"\r\n[notifications]\r\nenabled = true\r\n",
			set:  off,
			want: "mode = \"local\"\r\n[notifications]\r\nenabled = true\r\n\r\n[privacy]\r\nkeep_prompt_text = false\r\n",
		},
		{
			name: "a switch under another table isn't touched, and [privacy] gets its own",
			file: "[notifications]\nkeep_prompt_text = true\n",
			set:  off,
			want: "[notifications]\nkeep_prompt_text = true\n\n[privacy]\nkeep_prompt_text = false\n",
		},
		{name: "a file that already says so is left alone", file: "[privacy]\nkeep_prompt_text = false\n", set: off, want: "[privacy]\nkeep_prompt_text = false\n"},
		{
			name:    "a file with [privacy] twice, as the two lines added to this version's file give, isn't changed, and the error says what to write",
			file:    defaultFile + "[privacy]\nkeep_prompt_text = false\n",
			set:     off,
			want:    defaultFile + "[privacy]\nkeep_prompt_text = false\n",
			wantErr: "set keep_prompt_text = false under [privacy]",
		},
		{
			name:    "a switch set as a dotted key isn't rewritten into a second table",
			file:    "privacy.keep_prompt_text = true\n",
			set:     off,
			want:    "privacy.keep_prompt_text = true\n",
			wantErr: "by hand",
		},
		{
			name:    "a switch written twice in different case isn't changed, since pairtime can't tell which one counts",
			file:    "[privacy]\nkeep_prompt_text = true\nKeep_Prompt_Text = true\n",
			set:     off,
			want:    "[privacy]\nkeep_prompt_text = true\nKeep_Prompt_Text = true\n",
			wantErr: "by hand",
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
			err := tt.set(name)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("error %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("error %v, want one containing %q", err, tt.wantErr)
			}
			got, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("config.toml is\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

// TestKeepTextEdits applies each edit that README.md and the Privacy page
// give for turning "keep prompt text" off, pairtime privacy keep-text off
// and the one by hand, to this version's default config.toml and to an
// older one: each turns the switch off, and changes nothing else.
func TestKeepTextEdits(t *testing.T) {
	// The two lines both documents give, for a file with no [privacy] line.
	const lines = "[privacy]\nkeep_prompt_text = false\n"
	command := func(t *testing.T, name string) {
		if err := SetKeepText(name, false); err != nil {
			t.Fatal(err)
		}
	}
	// "Set keep_prompt_text = false under [privacy]; if the file has no
	// [privacy] line, add these two lines at the end."
	byHand := func(t *testing.T, name string) {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if strings.Contains(text, "\n[privacy]\n") {
			text = strings.Replace(text, "\nkeep_prompt_text = true\n", "\nkeep_prompt_text = false\n", 1)
		} else {
			text += lines
		}
		if err := os.WriteFile(name, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name string
		file string
		edit func(t *testing.T, name string)
	}{
		{name: "pairtime privacy keep-text off turns the switch off in this version's default file", file: defaultFile, edit: command},
		{name: "pairtime privacy keep-text off turns the switch off in an older file, which ends in [notifications]", file: olderDefault, edit: command},
		{name: "keep_prompt_text = false under [privacy], by hand, turns the switch off in this version's default file", file: defaultFile, edit: byHand},
		{name: "the two lines added at the end by hand turn the switch off in an older file", file: olderDefault, edit: byHand},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(name, []byte(tt.file), 0o600); err != nil {
				t.Fatal(err)
			}
			tt.edit(t, name)
			data, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			got, problems := Parse(data)
			if want := (Config{Mode: Local, Privacy: Privacy{TextOff: true}}); got != want || len(problems) != 0 {
				t.Errorf("Parse() = %+v with problems %q, want %+v and none", got, problems, want)
			}
		})
	}

	docs := []struct {
		name  string
		file  string
		block *regexp.Regexp // the two lines, as the document shows them
	}{
		{name: "README.md names the command first, then the edit by hand with these two lines", file: "../../../README.md", block: regexp.MustCompile("(?s)```toml\n(.*?)```")},
		{name: "the Privacy page names the command first, then the edit by hand with these two lines", file: "../../../web/src/local/pages/Privacy.svelte", block: regexp.MustCompile(`(?s)data-testid="keep-off"><code>(.*?)</code>`)},
	}
	for _, d := range docs {
		t.Run(d.name, func(t *testing.T) {
			data, err := os.ReadFile(d.file)
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			m := d.block.FindStringSubmatchIndex(text)
			if m == nil {
				t.Fatalf("%s shows no lines to add", d.file)
			}
			if got := strings.TrimSuffix(text[m[2]:m[3]], "\n") + "\n"; got != lines {
				t.Errorf("%s shows %q, want %q", d.file, got, lines)
			}
			at := strings.Index(text, "pairtime privacy keep-text off")
			if at < 0 || at > m[0] {
				t.Fatalf("%s doesn't name pairtime privacy keep-text off before the lines", d.file)
			}
			// What it says between them, as words, without markup.
			said := strings.Join(strings.Fields(strings.NewReplacer("<code>", "", "</code>", "", "`", "").Replace(text[at:m[0]])), " ")
			for _, want := range []string{"set keep_prompt_text = false under [privacy]", "if the file has no [privacy] line", "add these two lines at the end"} {
				if !strings.Contains(said, want) {
					t.Errorf("%s doesn't say %q between the command and the lines: %q", d.file, want, said)
				}
			}
		})
	}
}
