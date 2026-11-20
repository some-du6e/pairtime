// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package linux

import "testing"

func TestSessionBusAddress(t *testing.T) {
	const uid = 1000
	tests := []struct {
		name   string
		env    string
		files  []string
		want   string
		wantOK bool
	}{
		{
			name:   "DBUS_SESSION_BUS_ADDRESS is used as it is",
			env:    "unix:path=/tmp/dbus-test",
			files:  []string{"/run/user/1000/bus"},
			want:   "unix:path=/tmp/dbus-test",
			wantOK: true,
		},
		{
			name:   "with no variable, systemd's per-user socket is the bus",
			files:  []string{"/run/user/1000/bus"},
			want:   "unix:path=/run/user/1000/bus",
			wantOK: true,
		},
		{
			name:   "autolaunch: counts as no address, as godbus reads it",
			env:    "autolaunch:",
			files:  []string{"/run/user/1000/bus"},
			want:   "unix:path=/run/user/1000/bus",
			wantOK: true,
		},
		{
			name:  "another user's socket doesn't count",
			files: []string{"/run/user/1001/bus"},
		},
		{
			name: "no variable and no socket means no bus, so nothing starts dbus-launch",
		},
		{
			name:  "autolaunch: with no socket means no bus",
			env:   "autolaunch:",
			files: []string{"/run/user/1000/dbus-session"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(k string) string {
				if k == "DBUS_SESSION_BUS_ADDRESS" {
					return tt.env
				}
				return ""
			}
			exists := func(name string) bool {
				for _, f := range tt.files {
					if f == name {
						return true
					}
				}
				return false
			}
			got, ok := sessionBusAddress(getenv, exists, uid)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("got (%q, %v), want (%q, %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
