// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSnapPageDir(t *testing.T) {
	tests := []struct {
		name    string
		handler string   // what xdg-mime prints; "-" for no xdg-mime at all
		snapd   []string // the desktop files snapd installed
		common  []string // the snaps whose ~/snap/<name>/common is there
		want    string   // the snap whose common folder comes back; "" for the data directory
	}{
		{name: "Firefox from a snap reads the page from its own common folder", handler: "firefox_firefox.desktop", snapd: []string{"firefox_firefox.desktop"}, common: []string{"firefox"}, want: "firefox"},
		{name: "Chromium from a snap gets its own folder too", handler: "chromium_chromium.desktop", snapd: []string{"chromium_chromium.desktop"}, common: []string{"chromium"}, want: "chromium"},
		{name: "a browser that isn't a snap reads the data directory", handler: "firefox.desktop", common: []string{"firefox"}},
		{name: "a desktop file snapd didn't install isn't taken for a snap's", handler: "firefox_firefox.desktop", common: []string{"firefox"}},
		{name: "a snap that hasn't run yet has no folder, so the page goes in the data directory", handler: "firefox_firefox.desktop", snapd: []string{"firefox_firefox.desktop"}},
		{name: "a handler name with a path in it is refused", handler: "../x_y.desktop", snapd: []string{"x_y.desktop"}, common: []string{"x"}},
		{name: "a name that isn't a snap's is refused", handler: "Fire_fox.desktop", snapd: []string{"Fire_fox.desktop"}, common: []string{"Fire"}},
		{name: "with no xdg-mime the page goes in the data directory", handler: "-", snapd: []string{"firefox_firefox.desktop"}, common: []string{"firefox"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bin := t.TempDir()
			if tt.handler != "-" {
				script := "#!/bin/sh\necho '" + tt.handler + "'\n"
				if err := os.WriteFile(filepath.Join(bin, "xdg-mime"), []byte(script), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin)
			apps := t.TempDir()
			for _, f := range tt.snapd {
				if err := os.WriteFile(filepath.Join(apps, f), []byte("[Desktop Entry]\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			old := snapDesktopDir
			snapDesktopDir = apps
			t.Cleanup(func() { snapDesktopDir = old })
			userHome := t.TempDir()
			for _, n := range tt.common {
				if err := os.MkdirAll(filepath.Join(userHome, "snap", n, "common"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			want := ""
			if tt.want != "" {
				want = filepath.Join(userHome, "snap", tt.want, "common")
			}
			if got := snapPageDir(ctx, userHome); got != want {
				t.Errorf("snapPageDir = %q, want %q", got, want)
			}
		})
	}
}
