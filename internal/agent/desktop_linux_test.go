// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package agent

import (
	"testing"

	"github.com/some-du6e/pairtime/internal/platform/linux"
)

func TestLinuxSignals(t *testing.T) {
	tests := []struct {
		name string
		st   linux.Status
		want signalsReply
	}{
		{
			name: "on X11 each signal is named by the desktop's source for it",
			st: linux.Status{
				Session: linux.SessionX11,
				Idle:    linux.Signal{Via: "MIT-SCREEN-SAVER"},
				Focus:   linux.Signal{Via: "_NET_ACTIVE_WINDOW, with each window's process from X-Resource"},
				Power:   linux.Signal{Via: "logind PrepareForSleep"},
				Lock:    linux.Signal{Via: "logind LockedHint"},
			},
			want: signalsReply{
				Session: "x11",
				Idle:    via("MIT-SCREEN-SAVER"),
				Focus:   via("_NET_ACTIVE_WINDOW, with each window's process from X-Resource"),
				Power:   via("logind PrepareForSleep"),
				Lock:    via("logind LockedHint"),
			},
		},
		{
			name: "on GNOME on Wayland idle time comes from Mutter and focus is unknown, with the desktop's reason, which doctor prints (D-design-5.9)",
			st: linux.Status{
				Session: linux.SessionWayland,
				Idle:    linux.Signal{Via: "org.gnome.Mutter.IdleMonitor"},
				Focus:   linux.Signal{Unknown: "Wayland gives other programs no focus signal"},
				Power:   linux.Signal{Via: "logind PrepareForSleep"},
				Lock:    linux.Signal{Unknown: "no session"},
			},
			want: signalsReply{
				Session: "wayland",
				Idle:    via("org.gnome.Mutter.IdleMonitor"),
				Focus:   unknown("Wayland gives other programs no focus signal"),
				Power:   via("logind PrepareForSleep"),
				Lock:    unknown("no session"),
			},
		},
		{
			name: "a signal the desktop marks neither known nor explained is unknown, never claimed",
			st:   linux.Status{Session: linux.SessionNone},
			want: signalsReply{
				Session: "none",
				Idle:    unknown("the desktop doesn't say"),
				Focus:   unknown("the desktop doesn't say"),
				Power:   unknown("the desktop doesn't say"),
				Lock:    unknown("the desktop doesn't say"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := linuxSignals(tt.st); got != tt.want {
				t.Errorf("linuxSignals = %+v, want %+v", got, tt.want)
			}
		})
	}
}
