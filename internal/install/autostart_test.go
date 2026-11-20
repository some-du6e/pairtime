// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func autostartGolden(t *testing.T, name string) []byte {
	t.Helper()
	data := readOrNil(t, filepath.Join("testdata", "autostart", name))
	if data == nil {
		t.Fatalf("no testdata/autostart/%s", name)
	}
	return data
}

func TestAutostartEntries(t *testing.T) {
	xdg := t.TempDir()
	tests := []struct {
		name   string
		goos   string
		vars   map[string]string
		target func(home string) Target
		want   func(t *testing.T) []byte
		typ    uint32
	}{
		{
			name:   "Windows starts the agent from the Run value under HKEY_CURRENT_USER",
			goos:   "windows",
			target: func(string) Target { return RegistryValue(`Software\Microsoft\Windows\CurrentVersion\Run`, "pairtime") },
			want: func(*testing.T) []byte {
				return []byte(`"C:\Users\teen\AppData\Local\pairtime\bin\pairtime.exe" agent`)
			},
			typ: RegSZ,
		},
		{
			name: "macOS starts it from a LaunchAgent in ~/Library/LaunchAgents",
			goos: "darwin",
			target: func(home string) Target {
				return File(filepath.Join(home, "Library", "LaunchAgents", "dev.pairtime.agent.plist"))
			},
			want: func(t *testing.T) []byte { return autostartGolden(t, "dev.pairtime.agent.plist") },
		},
		{
			name:   "Linux starts it from an XDG autostart entry in ~/.config/autostart",
			goos:   "linux",
			target: func(home string) Target { return File(filepath.Join(home, ".config", "autostart", "pairtime.desktop")) },
			want:   func(t *testing.T) []byte { return autostartGolden(t, "pairtime.desktop") },
		},
		{
			name:   "XDG_CONFIG_HOME moves the Linux entry",
			goos:   "linux",
			vars:   map[string]string{"XDG_CONFIG_HOME": xdg},
			target: func(string) Target { return File(filepath.Join(xdg, "autostart", "pairtime.desktop")) },
			want:   func(t *testing.T) []byte { return autostartGolden(t, "pairtime.desktop") },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testEnv(t, tt.goos, tt.vars)
			item, err := Autostart(env)
			if err != nil {
				t.Fatal(err)
			}
			if item.ID != ItemAutostart || len(item.Edits) != 1 {
				t.Fatalf("Autostart = %+v, want one edit under ID %q", item, ItemAutostart)
			}
			e := item.Edits[0]
			if got, want := e.Target(), tt.target(env.Home); got != want {
				t.Errorf("the entry is %v, want %v", got, want)
			}
			got, err := e.Apply(Content{})
			if err != nil {
				t.Fatal(err)
			}
			if want := tt.want(t); !bytes.Equal(got.Data, want) || got.Type != tt.typ {
				t.Errorf("the entry holds (type %d)\n%s\nwant (type %d)\n%s", got.Type, got.Data, tt.typ, want)
			}
		})
	}
}

func TestAutostartInstallAndUninstall(t *testing.T) {
	tests := []struct {
		name string
		goos string
		// before is what the entry holds before install, "" for nothing.
		before string
		typ    uint32 // the registry type of before, on Windows
		// earlier means before is an entry an earlier pairtime wrote, with
		// its binary somewhere else, which uninstall takes away.
		earlier bool
	}{
		{name: "a Run value pairtime adds is gone after uninstall", goos: "windows"},
		{name: "a Run value of the same name for another program comes back after uninstall, with its type", goos: "windows", before: `"%LOCALAPPDATA%\Other\other.exe" --tray`, typ: RegExpandSZ},
		{name: "a Run value an earlier pairtime wrote for another binary path is gone after uninstall", goos: "windows", before: `"C:\old\pairtime.exe" agent`, typ: RegExpandSZ, earlier: true},
		{name: "a LaunchAgent pairtime adds is gone after uninstall, with the folders made for it", goos: "darwin"},
		{name: "a LaunchAgent file that was there before comes back after uninstall", goos: "darwin", before: "<plist/>\n"},
		{name: "a LaunchAgent an earlier pairtime wrote for another binary path is gone after uninstall", goos: "darwin", before: string(launchAgent("/opt/pairtime/bin/pairtime")), earlier: true},
		{name: "an XDG autostart entry pairtime adds is gone after uninstall, with the folders made for it", goos: "linux"},
		{name: "an XDG autostart entry that was there before comes back after uninstall", goos: "linux", before: "[Desktop Entry]\nHidden=true\n"},
		{name: "an XDG autostart entry an earlier pairtime wrote for another binary path is gone after uninstall", goos: "linux", before: "[Desktop Entry]\nType=Application\nExec=/opt/pairtime/bin/pairtime agent\n", earlier: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testEnv(t, tt.goos, nil)
			in := newInstaller(t)
			reg := in.Registry.(fakeRegistry)
			item, err := Autostart(env)
			if err != nil {
				t.Fatal(err)
			}
			e := item.Edits[0].(ownedEdit)
			read := func() Content {
				t.Helper()
				c, err := in.read(e.target)
				if err != nil {
					t.Fatal(err)
				}
				return c
			}
			if tt.before != "" {
				if e.target.isRegistry() {
					reg.Set(e.target.Key, e.target.Name, tt.before, tt.typ)
				} else {
					writeFile(t, e.target.Path, []byte(tt.before))
				}
			}
			was := read()

			p := install(t, in, item)
			if len(p.Changes) != 1 {
				t.Fatalf("the install plan has %d changes, want 1", len(p.Changes))
			}
			if got := read(); !bytes.Equal(got.Data, e.data) || got.Type != e.typ {
				t.Errorf("after install the entry holds %q (type %d), want %q (type %d)", got.Data, got.Type, e.data, e.typ)
			}
			again, err := in.PlanInstall([]Item{item})
			if err != nil {
				t.Fatalf("second PlanInstall: %v", err)
			}
			if !again.Empty() {
				t.Errorf("a second install plans changes:\n%s", again.Diff())
			}

			uninstall(t, in)
			want := was
			if tt.earlier {
				want = Content{}
			}
			if got := read(); !got.Same(want) {
				t.Errorf("after uninstall the entry holds %+v, want %+v", got, want)
			}
			if tt.goos != "windows" && tt.before == "" {
				top := filepath.Join(env.Home, ".config")
				if tt.goos == "darwin" {
					top = filepath.Join(env.Home, "Library")
				}
				checkGone(t, top, "the folder install made")
			}
			checkGone(t, in.Dir, "pairtime's install directory")
		})
	}
}

func TestRunCommand(t *testing.T) {
	long := `C:\` + strings.Repeat("d", 260-len(`C:\`)-len(`"" agent`)) // exactly 260 with the quotes and " agent"
	tests := []struct {
		name    string
		exe     string
		want    string
		wantErr string
	}{
		{name: "the path is quoted and the agent command follows", exe: `C:\Users\teen\AppData\Local\pairtime\bin\pairtime.exe`,
			want: `"C:\Users\teen\AppData\Local\pairtime\bin\pairtime.exe" agent`},
		{name: "a path with spaces is quoted", exe: `C:\Users\Te En\pairtime.exe`, want: `"C:\Users\Te En\pairtime.exe" agent`},
		{name: "a command of exactly 260 characters is allowed", exe: long, want: `"` + long + `" agent`},
		{name: "a command over the Run key's 260 characters is refused", exe: long + "d", wantErr: "260"},
		{name: "a path with a double quote is refused", exe: `C:\a"b\pairtime.exe`, wantErr: "double quote"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := runCommand(tt.exe)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("runCommand(%q) = %q, %v, want an error saying %q", tt.exe, got, err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("runCommand(%q) = %q, %v, want %q", tt.exe, got, err, tt.want)
			}
		})
	}
}

func TestExecArg(t *testing.T) {
	tests := []struct {
		name    string
		exe     string
		want    string
		wantErr bool
	}{
		{name: "a plain path is written as it is", exe: "/home/teen/.local/bin/pairtime", want: "/home/teen/.local/bin/pairtime"},
		{name: "a path with a space goes in double quotes", exe: "/home/te en/pairtime", want: `"/home/te en/pairtime"`},
		{name: "a backslash in quotes takes four backslashes", exe: `/home/a\b/pairtime`, want: `"/home/a\\\\b/pairtime"`},
		{name: "a dollar sign and a backtick in quotes take two backslashes", exe: "/home/$x/`y/pairtime", want: "\"/home/\\\\$x/\\\\`y/pairtime\""},
		{name: "a double quote in quotes takes two backslashes", exe: `/home/a"b/pairtime`, want: `"/home/a\\"b/pairtime"`},
		{name: "a percent sign is doubled", exe: "/home/100%/pairtime", want: "/home/100%%/pairtime"},
		{name: "a percent sign in a path that needs quotes is refused", exe: "/home/1 00%/pairtime", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := execArg(tt.exe)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("execArg(%q) = %q, %v, want %q (error: %v)", tt.exe, got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestAutostartEntriesCarryOddPaths(t *testing.T) {
	tests := []struct {
		name string
		goos string
		exe  string
		want []string // lines or fragments the entry must hold
	}{
		{"the desktop entry quotes Exec, but not TryExec, for a path with a space", "linux", "/home/te en/.local/bin/pairtime",
			[]string{"Exec=\"/home/te en/.local/bin/pairtime\" agent\n", "TryExec=/home/te en/.local/bin/pairtime\n"}},
		{"the desktop entry escapes a backslash in TryExec as a string value", "linux", `/home/a\b/pairtime`,
			[]string{`TryExec=/home/a\\b/pairtime` + "\n"}},
		{"the LaunchAgent escapes XML's special characters", "darwin", `/Users/a&b <c>/pairtime`,
			[]string{"<string>/Users/a&amp;b &lt;c&gt;/pairtime</string>"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testEnv(t, tt.goos, nil)
			env.Exe = tt.exe
			item, err := Autostart(env)
			if err != nil {
				t.Fatal(err)
			}
			got, err := item.Edits[0].Apply(Content{})
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range tt.want {
				if !bytes.Contains(got.Data, []byte(w)) {
					t.Errorf("the entry\n%s\ndoesn't hold %q", got.Data, w)
				}
			}
		})
	}
}

func TestEnvCheck(t *testing.T) {
	home := t.TempDir()
	tests := []struct {
		name string
		goos string
		home string
		exe  string
		ok   bool
	}{
		{"an absolute Linux path is a binary path", "linux", home, "/home/teen/.local/bin/pairtime", true},
		{"a drive-letter path is a Windows binary path", "windows", home, `C:\Users\teen\pairtime.exe`, true},
		{"a UNC path is a Windows binary path", "windows", home, `\\server\share\pairtime.exe`, true},
		{"a Unix path isn't a Windows binary path", "windows", home, "/home/teen/pairtime", false},
		{"a Windows path isn't a Linux binary path", "linux", home, `C:\pairtime.exe`, false},
		{"a relative path isn't a binary path", "linux", home, "bin/pairtime", false},
		{"a path with a newline is refused", "linux", home, "/home/teen/pair\ntime", false},
		{"a path that isn't UTF-8 is refused", "linux", home, "/home/teen/\xff/pairtime", false},
		{"no binary path is refused", "linux", home, "", false},
		{"a relative home directory is refused", "linux", "teen", "/home/teen/.local/bin/pairtime", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Env{GOOS: tt.goos, Home: tt.home, Exe: tt.exe}.check()
			if (err == nil) != tt.ok {
				t.Errorf("check() = %v, want ok: %v", err, tt.ok)
			}
		})
	}
	if _, err := Autostart(Env{GOOS: "plan9", Home: home, Exe: "/bin/pairtime"}); err == nil {
		t.Error("Autostart planned an entry for an OS without one")
	}
}
