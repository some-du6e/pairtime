// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package linux

import (
	"errors"
	"slices"
	"testing"
)

func TestSessionBusAddress(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		// uidErr is os/user.Current's error; without one the uid is 1000.
		uidErr  error
		sockets []string
		files   []string // that exist and aren't sockets
		want    string   // "" means no bus
	}{
		{
			name:    "DBUS_SESSION_BUS_ADDRESS is used as it is",
			env:     map[string]string{"DBUS_SESSION_BUS_ADDRESS": "unix:path=/tmp/dbus-test"},
			sockets: []string{"/run/user/1000/bus"},
			want:    "unix:path=/tmp/dbus-test",
		},
		{
			name:    "DBUS_SESSION_BUS_ADDRESS is used even when os/user can't say who this is",
			env:     map[string]string{"DBUS_SESSION_BUS_ADDRESS": "unix:path=/tmp/dbus-test"},
			uidErr:  errors.New("user: Current requires cgo or $USER set in environment"),
			sockets: []string{"/run/user/1000/bus"},
			want:    "unix:path=/tmp/dbus-test",
		},
		{
			name:    "with no variable, systemd's per-user socket is the bus",
			sockets: []string{"/run/user/1000/bus"},
			want:    "unix:path=/run/user/1000/bus",
		},
		{
			name:    "autolaunch: counts as no address, as godbus reads it",
			env:     map[string]string{"DBUS_SESSION_BUS_ADDRESS": "autolaunch:"},
			sockets: []string{"/run/user/1000/bus"},
			want:    "unix:path=/run/user/1000/bus",
		},
		{
			name:    "when os/user can't say who this is there is no bus, as godbus then looks in no /run/user directory",
			uidErr:  errors.New("user: Current requires cgo or $HOME set in environment"),
			sockets: []string{"/run/user/1000/bus"},
		},
		{
			name:    "another user's socket doesn't count",
			sockets: []string{"/run/user/1001/bus"},
		},
		{
			name:    "a socket in XDG_RUNTIME_DIR alone doesn't count, because godbus doesn't look there",
			env:     map[string]string{"XDG_RUNTIME_DIR": "/tmp/xdg-runtime"},
			sockets: []string{"/tmp/xdg-runtime/bus"},
		},
		{
			name:  "a bus file that isn't a socket doesn't count",
			files: []string{"/run/user/1000/bus"},
		},
		{
			name: "no variable and no socket means no bus, so nothing starts dbus-launch",
		},
		{
			name:  "autolaunch: with only Ubuntu 16.04's dbus-session file means no bus",
			env:   map[string]string{"DBUS_SESSION_BUS_ADDRESS": "autolaunch:"},
			files: []string{"/run/user/1000/dbus-session"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(k string) string { return tt.env[k] }
			uid := func() (string, error) {
				if tt.uidErr != nil {
					return "", tt.uidErr
				}
				return "1000", nil
			}
			isSocket := func(name string) bool { return slices.Contains(tt.sockets, name) }
			got, err := sessionBusAddress(getenv, uid, isSocket)
			if tt.want == "" {
				if err == nil {
					t.Fatalf("got %q, want no bus", got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("got (%q, %v), want %q", got, err, tt.want)
			}
		})
	}
}
