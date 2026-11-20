// SPDX-License-Identifier: AGPL-3.0-or-later

package home

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	// An absolute path on every OS, so the PAIRTIME_HOME cases run on all.
	pt := filepath.Join(os.TempDir(), "pairtime-test-home")
	tests := []struct {
		name     string
		goos     string // the OS whose rules the case checks; "" for every OS
		env      map[string]string
		userHome string
		want     Dirs
		wantErr  bool
	}{
		{
			name: "PAIRTIME_HOME holds every directory",
			env:  map[string]string{EnvHome: pt, "XDG_DATA_HOME": "/elsewhere", "LOCALAPPDATA": `C:\elsewhere`},
			want: Dirs{Data: pt, Config: pt, Runtime: pt},
		},
		{
			name:    "a relative PAIRTIME_HOME is refused",
			env:     map[string]string{EnvHome: "pairtime"},
			wantErr: true,
		},
		{
			name: "Windows keeps data in LOCALAPPDATA and config.toml in APPDATA",
			goos: "windows",
			env: map[string]string{
				"LOCALAPPDATA": `C:\Users\teen\AppData\Local`,
				"APPDATA":      `C:\Users\teen\AppData\Roaming`,
			},
			want: Dirs{
				Data:    `C:\Users\teen\AppData\Local\pairtime`,
				Config:  `C:\Users\teen\AppData\Roaming\pairtime`,
				Runtime: `C:\Users\teen\AppData\Local\pairtime`,
			},
		},
		{
			name:    "Windows without LOCALAPPDATA is an error",
			goos:    "windows",
			env:     map[string]string{"APPDATA": `C:\Users\teen\AppData\Roaming`},
			wantErr: true,
		},
		{
			name:     "macOS keeps data and config.toml in Application Support and the socket in TMPDIR",
			goos:     "darwin",
			env:      map[string]string{"TMPDIR": "/var/folders/xy/abc/T/"},
			userHome: "/Users/teen",
			want: Dirs{
				Data:    "/Users/teen/Library/Application Support/pairtime",
				Config:  "/Users/teen/Library/Application Support/pairtime",
				Runtime: "/var/folders/xy/abc/T/pairtime",
			},
		},
		{
			name:     "macOS without TMPDIR keeps the socket with the data",
			goos:     "darwin",
			userHome: "/Users/teen",
			want: Dirs{
				Data:    "/Users/teen/Library/Application Support/pairtime",
				Config:  "/Users/teen/Library/Application Support/pairtime",
				Runtime: "/Users/teen/Library/Application Support/pairtime",
			},
		},
		{
			name:     "Linux uses the XDG defaults under the home directory",
			goos:     "linux",
			env:      map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000"},
			userHome: "/home/teen",
			want: Dirs{
				Data:    "/home/teen/.local/share/pairtime",
				Config:  "/home/teen/.config/pairtime",
				Runtime: "/run/user/1000/pairtime",
			},
		},
		{
			name: "Linux uses XDG_DATA_HOME and XDG_CONFIG_HOME when they are set",
			goos: "linux",
			env: map[string]string{
				"XDG_DATA_HOME":   "/data/teen",
				"XDG_CONFIG_HOME": "/cfg/teen",
				"XDG_RUNTIME_DIR": "/run/user/1000",
			},
			userHome: "/home/teen",
			want: Dirs{
				Data:    "/data/teen/pairtime",
				Config:  "/cfg/teen/pairtime",
				Runtime: "/run/user/1000/pairtime",
			},
		},
		{
			name: "Linux ignores relative XDG paths, as the spec says",
			goos: "linux",
			env: map[string]string{
				"XDG_DATA_HOME":   "data",
				"XDG_CONFIG_HOME": "cfg",
				"XDG_RUNTIME_DIR": "run",
			},
			userHome: "/home/teen",
			want: Dirs{
				Data:    "/home/teen/.local/share/pairtime",
				Config:  "/home/teen/.config/pairtime",
				Runtime: "/home/teen/.local/share/pairtime",
			},
		},
		{
			name:    "Linux with neither XDG paths nor a home directory is an error",
			goos:    "linux",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.goos != "" && tt.goos != runtime.GOOS {
				t.Skipf("checks the %s rules", tt.goos)
			}
			got, err := resolve(func(k string) string { return tt.env[k] }, tt.userHome)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("resolve() = %+v, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("resolve() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestFiles(t *testing.T) {
	d := Dirs{Data: filepath.Join("d"), Config: filepath.Join("c")}
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "config.toml is in the config directory", got: d.ConfigFile(), want: filepath.Join("c", "config.toml")},
		{name: "state.json is in the data directory", got: d.StateFile(), want: filepath.Join("d", "state.json")},
		{name: "port.json is in the data directory", got: d.PortFile(), want: filepath.Join("d", "port.json")},
		{name: "the ledger is in the data directory", got: d.Ledger(), want: filepath.Join("d", "ledger")},
		{name: "the inbox is in the data directory", got: d.Inbox(), want: filepath.Join("d", "inbox")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %s, want %s", tt.got, tt.want)
			}
		})
	}
}

// TestCreate checks that Create makes every directory it needs for this
// user only, so a PAIRTIME_HOME at a drive root such as C:\pairtime
// doesn't let other users read the agent's files (#46), and that it leaves
// a directory that is already there alone.
func TestCreate(t *testing.T) {
	// Two names that, under t.TempDir, make a path longer than the 247
	// characters CreateDirectory takes without the \\?\ prefix.
	long1, long2 := strings.Repeat("d", 100), strings.Repeat("e", 100)
	tests := []struct {
		name   string
		before []string // directories under root made before Create, with os.Mkdir
		dirs   func(root string) Dirs
		made   []string // the directories under root that Create makes, each for this user only
		kept   []string // directories under root that were there, whose permissions Create keeps
	}{
		{
			name: "Create makes separate data and config directories, and their parents, for this user only",
			dirs: func(root string) Dirs {
				return Dirs{Data: filepath.Join(root, "data", "pairtime"), Config: filepath.Join(root, "config", "pairtime")}
			},
			made: []string{"data", filepath.Join("data", "pairtime"), "config", filepath.Join("config", "pairtime")},
		},
		{
			name: "Create accepts one directory for both",
			dirs: func(root string) Dirs {
				return Dirs{Data: filepath.Join(root, "pt"), Config: filepath.Join(root, "pt")}
			},
			made: []string{"pt"},
		},
		{
			name:   "Create leaves a directory that is there as it is and makes the rest for this user only",
			before: []string{"pt"},
			dirs: func(root string) Dirs {
				return Dirs{Data: filepath.Join(root, "pt", "data"), Config: filepath.Join(root, "pt")}
			},
			made: []string{filepath.Join("pt", "data")},
			kept: []string{"pt"},
		},
		{
			name: "Create makes a directory whose path is too long for Windows' usual limit",
			dirs: func(root string) Dirs {
				return Dirs{Data: filepath.Join(root, long1, long2), Config: filepath.Join(root, long1, long2)}
			},
			made: []string{long1, filepath.Join(long1, long2)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for _, dir := range tt.before {
				if err := os.Mkdir(filepath.Join(root, dir), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			kept := map[string]string{}
			for _, dir := range tt.kept {
				kept[dir] = permissions(t, filepath.Join(root, dir))
			}
			d := tt.dirs(root)
			if err := d.Create(); err != nil {
				t.Fatal(err)
			}
			for _, dir := range []string{d.Data, d.Config} {
				if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
					t.Errorf("%s isn't a directory after Create: %v", dir, err)
				}
			}
			for _, dir := range tt.made {
				checkPrivateDir(t, filepath.Join(root, dir))
			}
			for dir, before := range kept {
				if after := permissions(t, filepath.Join(root, dir)); after != before {
					t.Errorf("Create changed the permissions of %s, which was there, from %s to %s", dir, before, after)
				}
			}
			// WriteFile's files take the data directory's permissions, so
			// they are for this user only as well.
			if err := WriteFile(d.StateFile(), []byte("{}\n")); err != nil {
				t.Fatal(err)
			}
			checkPrivateFile(t, d.StateFile())
		})
	}
}
