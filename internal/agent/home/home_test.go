// SPDX-License-Identifier: AGPL-3.0-or-later

package home

import (
	"os"
	"path/filepath"
	"runtime"
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

func TestCreate(t *testing.T) {
	tests := []struct {
		name string
		dirs func(root string) Dirs
	}{
		{name: "Create makes separate data and config directories", dirs: func(root string) Dirs {
			return Dirs{Data: filepath.Join(root, "data", "pairtime"), Config: filepath.Join(root, "config", "pairtime")}
		}},
		{name: "Create accepts one directory for both", dirs: func(root string) Dirs {
			return Dirs{Data: filepath.Join(root, "pt"), Config: filepath.Join(root, "pt")}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := tt.dirs(t.TempDir())
			if err := d.Create(); err != nil {
				t.Fatal(err)
			}
			for _, dir := range []string{d.Data, d.Config} {
				if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
					t.Errorf("%s isn't a directory after Create: %v", dir, err)
				}
			}
		})
	}
}
