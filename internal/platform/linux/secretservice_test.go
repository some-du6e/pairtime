// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package linux

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSessionBusAddress(t *testing.T) {
	const session = "DBUS_SESSION_BUS_ADDRESS=unix:abstract=/tmp/dbus-E1c73yNqrG\n"
	tests := []struct {
		name   string
		env    string
		uidErr error             // user.Current's error, when it fails
		files  map[string]string // path to contents
		locked string            // a path in files that can't be read
		want   string
		wantOK bool
	}{
		{
			name:   "DBUS_SESSION_BUS_ADDRESS is used as it is",
			env:    "unix:path=/tmp/dbus-test",
			files:  map[string]string{"/run/user/1000/bus": ""},
			want:   "unix:path=/tmp/dbus-test",
			wantOK: true,
		},
		{
			name:   "with no variable, the socket in user.Current's runtime directory is the bus",
			files:  map[string]string{"/run/user/1000/bus": ""},
			want:   "unix:path=/run/user/1000/bus",
			wantOK: true,
		},
		{
			name:   "autolaunch: counts as no address, as godbus reads it",
			env:    "autolaunch:",
			files:  map[string]string{"/run/user/1000/bus": ""},
			want:   "unix:path=/run/user/1000/bus",
			wantOK: true,
		},
		{
			name:  "another user's socket doesn't count",
			files: map[string]string{"/run/user/1001/bus": ""},
		},
		{
			name: "no variable and no socket means no bus, so nothing starts dbus-launch",
		},
		{
			name:   "when user.Current fails, godbus skips /run/user and would run dbus-launch, so its socket doesn't count",
			uidErr: errors.New("user: Current requires cgo or $USER set in environment"),
			files:  map[string]string{"/run/user/1000/bus": ""},
		},
		{
			name:   "the address in Ubuntu 16.04's dbus-session file counts, as godbus reads it",
			files:  map[string]string{"/run/user/1000/dbus-session": session},
			want:   "unix:abstract=/tmp/dbus-E1c73yNqrG",
			wantOK: true,
		},
		{
			name:  "a dbus-session file without the address line means no bus",
			files: map[string]string{"/run/user/1000/dbus-session": "unix:abstract=/tmp/dbus-E1c73yNqrG\n"},
		},
		{
			name:  "a dbus-session file with an empty address means no bus",
			files: map[string]string{"/run/user/1000/dbus-session": "DBUS_SESSION_BUS_ADDRESS=\n"},
		},
		{
			name:   "a dbus-session file that can't be read means no bus",
			files:  map[string]string{"/run/user/1000/dbus-session": session},
			locked: "/run/user/1000/dbus-session",
		},
		{
			name:  "autolaunch: with no socket means no bus",
			env:   "autolaunch:",
			files: map[string]string{"/run/user/1000/other": ""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := busFinder{
				getenv: func(k string) string {
					if k == "DBUS_SESSION_BUS_ADDRESS" {
						return tt.env
					}
					return ""
				},
				uid: func() (string, error) {
					if tt.uidErr != nil {
						return "", tt.uidErr
					}
					return "1000", nil
				},
				exists: func(name string) bool {
					_, ok := tt.files[name]
					return ok
				},
				readFile: func(name string) ([]byte, error) {
					data, ok := tt.files[name]
					if !ok || name == tt.locked {
						return nil, os.ErrPermission
					}
					return []byte(data), nil
				},
			}
			got, ok := sessionBusAddress(f)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("got (%q, %v), want (%q, %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// TestThisSystemExists checks the real exists against godbus's fileExists,
// which counts every path that stat doesn't say is missing.
func TestThisSystemExists(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"a file that exists counts", file, true},
		{"a path that doesn't exist doesn't count", filepath.Join(dir, "missing"), false},
		{"a path stat fails on for another reason counts, as godbus counts it", filepath.Join(file, "bus"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := thisSystem.exists(tt.path); got != tt.want {
				t.Errorf("exists(%s) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
