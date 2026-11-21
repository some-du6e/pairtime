// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
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
