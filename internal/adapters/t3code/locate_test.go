// SPDX-License-Identifier: AGPL-3.0-or-later

package t3code

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/internal/adapters"
)

// unit is a systemd unit as T3's background service writes it
// (bootService.ts renderBootServiceUnit), with home as T3CODE_HOME's value
// as written.
func unit(home string) string {
	return "[Unit]\nDescription=T3 Code server\n\n[Service]\nType=simple\nWorkingDirectory=%h\n" +
		"Environment=T3CODE_HOME=" + home + "\n" +
		"Environment=T3CODE_SERVICE_UNIT=t3code.service\nExecStart=/opt/t3/bin/t3 serve\n\n[Install]\nWantedBy=default.target\n"
}

// plist is a launchd plist as T3's background service writes it
// (renderBootServicePlist), with home as T3CODE_HOME's string as written.
func plist(home string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.t3tools.t3code.service</string>
  <key>ProgramArguments</key>
  <array>
    <string>/opt/t3/bin/t3</string>
    <string>serve</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>/usr/bin:/bin</string>
    <key>T3CODE_HOME</key>
    <string>` + home + `</string>
  </dict>
  <key>RunAtLoad</key>
  <true/>
</dict>
</plist>
`
}

// TestLocate holds Locate to DESIGN 5.1: T3CODE_HOME from the environment,
// else from T3's background service, else <home>/.t3, never a WSL share.
func TestLocate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the cases use Unix paths")
	}
	storeIn := func(home string) string { return filepath.Join(home, "userdata", "statev2.sqlite") }
	cases := []struct {
		name string
		goos string
		// env's values may name the test's home as $HOME.
		env   map[string]string
		files map[string]string // by path under home
		want  func(home string) []Store
	}{
		{name: "with nothing set, the store is in <home>/.t3", goos: "linux",
			want: func(h string) []Store { return []Store{{storeIn(filepath.Join(h, ".t3")), FromDefault}} }},
		{name: "T3CODE_HOME in the environment comes first", goos: "linux", env: map[string]string{"T3CODE_HOME": "/srv/t3"},
			want: func(h string) []Store {
				return []Store{{storeIn("/srv/t3"), FromEnv}, {storeIn(filepath.Join(h, ".t3")), FromDefault}}
			}},
		{name: "T3CODE_HOME is trimmed and its ~ is home, as T3 resolves it", goos: "linux", env: map[string]string{"T3CODE_HOME": "  ~/t3-home "},
			want: func(h string) []Store {
				return []Store{{storeIn(filepath.Join(h, "t3-home")), FromEnv}, {storeIn(filepath.Join(h, ".t3")), FromDefault}}
			}},
		{name: "a T3CODE_HOME of spaces alone names no home", goos: "linux", env: map[string]string{"T3CODE_HOME": "   "},
			want: func(h string) []Store { return []Store{{storeIn(filepath.Join(h, ".t3")), FromDefault}} }},
		{name: "a relative T3CODE_HOME is taken against home, as T3's service runs in it", goos: "linux",
			env: map[string]string{"T3CODE_HOME": "data/t3"},
			want: func(h string) []Store {
				return []Store{{storeIn(filepath.Join(h, "data", "t3")), FromEnv}, {storeIn(filepath.Join(h, ".t3")), FromDefault}}
			}},
		{name: "T3's systemd unit names the background service's home", goos: "linux",
			files: map[string]string{".config/systemd/user/t3code.service": unit("/srv/t3-service")},
			want: func(h string) []Store {
				return []Store{{storeIn("/srv/t3-service"), FromService}, {storeIn(filepath.Join(h, ".t3")), FromDefault}}
			}},
		{name: "a unit's quoted value keeps its spaces, and its %% is a %", goos: "linux",
			files: map[string]string{".config/systemd/user/t3code.service": unit(`"/srv/T3 Code 100%%"`)},
			want: func(h string) []Store {
				return []Store{{storeIn("/srv/T3 Code 100%"), FromService}, {storeIn(filepath.Join(h, ".t3")), FromDefault}}
			}},
		{name: "a unit's %h is home", goos: "linux",
			files: map[string]string{".config/systemd/user/t3code.service": unit("%h/.t3-service")},
			want: func(h string) []Store {
				return []Store{{storeIn(filepath.Join(h, ".t3-service")), FromService}, {storeIn(filepath.Join(h, ".t3")), FromDefault}}
			}},
		{name: "the unit is under XDG_CONFIG_HOME when that is set", goos: "linux",
			env:   map[string]string{"XDG_CONFIG_HOME": "$HOME/xdg"},
			files: map[string]string{"xdg/systemd/user/t3code.service": unit("/srv/t3-service")},
			want: func(h string) []Store {
				return []Store{{storeIn("/srv/t3-service"), FromService}, {storeIn(filepath.Join(h, ".t3")), FromDefault}}
			}},
		{name: "a relative XDG_CONFIG_HOME isn't one, so the unit is under ~/.config", goos: "linux",
			env:   map[string]string{"XDG_CONFIG_HOME": "xdg"},
			files: map[string]string{".config/systemd/user/t3code.service": unit("/srv/t3-service")},
			want: func(h string) []Store {
				return []Store{{storeIn("/srv/t3-service"), FromService}, {storeIn(filepath.Join(h, ".t3")), FromDefault}}
			}},
		{name: "T3's launchd plist names the background service's home on macOS", goos: "darwin",
			files: map[string]string{"Library/LaunchAgents/com.t3tools.t3code.service.plist": plist("/Users/someone/T3 &amp; more")},
			want: func(h string) []Store {
				return []Store{{storeIn("/Users/someone/T3 & more"), FromService}, {storeIn(filepath.Join(h, ".t3")), FromDefault}}
			}},
		{name: "a plist on Linux is not read", goos: "linux",
			files: map[string]string{"Library/LaunchAgents/com.t3tools.t3code.service.plist": plist("/srv/t3-service")},
			want:  func(h string) []Store { return []Store{{storeIn(filepath.Join(h, ".t3")), FromDefault}} }},
		{name: "a home on a WSL distro's share is never read over it", goos: "windows",
			env:  map[string]string{"T3CODE_HOME": `\\wsl$\Ubuntu\home\someone\.t3`},
			want: func(h string) []Store { return []Store{{storeIn(filepath.Join(h, ".t3")), FromDefault}} }},
		{name: "the same home named twice is one store", goos: "linux", env: map[string]string{"T3CODE_HOME": "~/.t3"},
			want: func(h string) []Store { return []Store{{storeIn(filepath.Join(h, ".t3")), FromEnv}} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			for name, data := range tc.files {
				p := filepath.Join(home, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			env := map[string]string{}
			for k, v := range tc.env {
				env[k] = strings.ReplaceAll(v, "$HOME", home)
			}
			got := Locate(adapters.Env{GOOS: tc.goos, Home: home, Getenv: func(k string) string { return env[k] }})
			if want := tc.want(home); !slices.Equal(got, want) {
				t.Errorf("Locate = %+v, want %+v", got, want)
			}
		})
	}
}

// TestWSLShare holds onWSLShare to the forms a WSL distro's share takes.
func TestWSLShare(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{`\\wsl$\Ubuntu\home\someone\.t3`, true},
		{`\\WSL.LOCALHOST\Ubuntu\home\someone\.t3`, true},
		{`//wsl$/Ubuntu/home/someone/.t3`, true},
		{`\\?\UNC\wsl.localhost\Debian\root\.t3`, true},
		{`\\wsl$`, true},
		{`\\wslserver\share\.t3`, false},
		{`C:\Users\someone\.t3`, false},
		{`/home/someone/.t3`, false},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			if got := onWSLShare(tc.path); got != tc.want {
				t.Errorf("onWSLShare(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// TestUnitHome holds unitHome to systemd's reading of Environment= lines.
func TestUnitHome(t *testing.T) {
	cases := []struct {
		name, unit, want string
		ok               bool
	}{
		{"a plain value", "Environment=T3CODE_HOME=/srv/t3\n", "/srv/t3", true},
		{"a quoted value with escapes", `Environment=T3CODE_HOME="/srv/a \"b\" \\c"` + "\n", `/srv/a "b" \c`, true},
		{"one assignment among several on a line", `Environment="A=1 2" T3CODE_HOME=/srv/t3 B=3` + "\n", "/srv/t3", true},
		{"a later assignment wins", "Environment=T3CODE_HOME=/srv/one\nEnvironment=T3CODE_HOME=/srv/two\n", "/srv/two", true},
		{"a specifier the reader doesn't know makes the value unreadable", "Environment=T3CODE_HOME=%t/t3\n", "", false},
		{"no assignment", "Environment=OTHER=1\n", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := unitHome([]byte(tc.unit), "/home/someone")
			if got != tc.want || ok != tc.ok {
				t.Errorf("unitHome = %q, %v; want %q, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}
