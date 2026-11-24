// SPDX-License-Identifier: AGPL-3.0-or-later

package consent

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/install"
)

// hackatimeKey is the teen's Hackatime key in hackatimeConfig, which the
// screen must never show.
const hackatimeKey = "00000000-0000-4000-8000-0000000048c1"

// hackatimeConfig is the ~/.wakatime.cfg hackatime-setup writes (HC 1.5).
const hackatimeConfig = "[settings]\napi_url = https://hackatime.hackclub.com/api/hackatime/v1\napi_key = " + hackatimeKey + "\nheartbeat_rate_limit_seconds = 30\n"

// TestWakaTimeSnapshots checks the whole consent screen with the WakaTime
// card, after the hooks, for a home with hackatime-setup's config and for
// one with none, against testdata/wakatime*.golden (docs/PLAN.md,
// WP-IN3). A screen that differs is printed in base64, as in
// TestScreenSnapshots.
func TestWakaTimeSnapshots(t *testing.T) {
	tests := []struct {
		name   string
		golden string
		config string // ~/.wakatime.cfg, "" for none
	}{
		{"with hackatime-setup's config, a card after the hooks adds pairtime's line, with the teen's values masked in its diff",
			"wakatime.golden", hackatimeConfig},
		{"with no WakaTime config, the card says pairtime makes one for the agent alone, how to get WakaTime's extension, and that Robert's programs read Hackatime",
			"wakatime-new.golden", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, ".claude"), 0o700); err != nil {
				t.Fatal(err)
			}
			if tt.config != "" {
				if err := os.WriteFile(filepath.Join(root, ".wakatime.cfg"), []byte(tt.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			env := install.Env{GOOS: "linux", Home: root, Getenv: func(string) string { return "" }, Exe: teenExe}
			data := filepath.Join(root, "data")
			in := &install.Installer{Dir: filepath.Join(data, "install")}
			diffs := map[string]string{}
			plan := func(it install.Item, err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
				p, err := in.PlanInstall([]install.Item{it})
				if err != nil {
					t.Fatal(err)
				}
				defer p.Close()
				diffs[it.ID] = p.Diff()
			}
			plan(install.Autostart(env))
			plan(install.ClaudeCode(env))
			plan(install.WakaTime(env, install.WakaTimeOptions{
				Data: data, Port: 7247, Now: time.Date(2026, 11, 24, 5, 30, 0, 0, time.UTC),
				Key: "00000000-0000-4000-8000-00000000a9e7",
			}))
			s := New(Input{GOOS: "linux", Binary: teenExe, Harnesses: install.Detect(env), Hook: HookTest{OK: true, Took: 12 * time.Millisecond}, Diffs: diffs, WakaTimeNew: tt.config == ""})
			var b strings.Builder
			if err := s.Render(&b); err != nil {
				t.Fatal(err)
			}
			got := teenPaths(b.String(), root)
			if strings.Contains(got, hackatimeKey) {
				t.Errorf("the screen shows the teen's Hackatime key:\n%s", got)
			}
			want, err := os.ReadFile(filepath.Join("testdata", tt.golden))
			if err != nil || got != string(want) {
				t.Errorf("the screen isn't testdata/%s (%v); it is:\n%s\nin base64:\n%s", tt.golden, err, got, base64.StdEncoding.EncodeToString([]byte(got)))
			}
		})
	}
}

func TestWakaTimeLeftOut(t *testing.T) {
	tests := []struct {
		name, why, want string
	}{
		{name: "a config install won't edit is named with why, and the card stays out", why: "/home/teen/.wakatime.cfg line 2 has a key in quotes", want: "pairtime won't get your editor time from WakaTime's plugins: /home/teen/.wakatime.cfg line 2 has a key in quotes."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New(Input{GOOS: "linux", Binary: teenExe, Diffs: map[string]string{}, Left: map[string]string{install.ItemWakaTime: tt.why}})
			var b strings.Builder
			if err := s.Render(&b); err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(strings.Fields(b.String()), " "); !strings.Contains(got, tt.want) {
				t.Errorf("the screen doesn't say %q:\n%s", tt.want, b.String())
			}
			for _, c := range s.Cards {
				if c.ID == install.ItemWakaTime {
					t.Errorf("the screen offers the WakaTime card it left out")
				}
			}
		})
	}
}
