// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/internal/install"
	"github.com/some-du6e/pairtime/internal/install/wakacfg"
)

// wakatimeEnv, when set, turns the WakaTime item on in the test binary and
// the processes it starts (offerWakaTime), as WP-AD17 turns it on for a
// release.
const wakatimeEnv = "PAIRTIME_TEST_WAKATIME"

func init() {
	if os.Getenv(wakatimeEnv) != "" {
		offerWakaTime = true
	}
}

// teenHackatimeKey is the teen's Hackatime key in teenHackatimeConfig,
// which install must never print.
const teenHackatimeKey = "00000000-0000-4000-8000-0000000048c1"

// teenHackatimeConfig is the ~/.wakatime.cfg hackatime-setup writes (HC
// 1.5).
const teenHackatimeConfig = "[settings]\napi_url = https://hackatime.hackclub.com/api/hackatime/v1\napi_key = " + teenHackatimeKey + "\nheartbeat_rate_limit_seconds = 30\n"

// anyPort writes every port on 127.0.0.1 in s as <port>.
func anyPort(s string) string {
	return regexp.MustCompile(`127\.0\.0\.1:[0-9]+`).ReplaceAllString(s, "127.0.0.1:<port>")
}

// TestInstallWakaTime runs the real install and uninstall with the
// WakaTime item on, in processes of their own, and checks pairtime's line
// in ~/.wakatime.cfg: install adds it to the teen's config, or makes one,
// and uninstall leaves every file as it was before install, with no agent
// running (docs/PLAN.md, WP-IN3).
func TestInstallWakaTime(t *testing.T) {
	if v, ok := runValue(); ok {
		t.Skipf(`this account has a pairtime login item, HKCU\%s\%s = %q, which the test would replace; it runs where there is none, as in CI`, install.RunKey, install.RunValue, v)
	}
	tests := []struct {
		name string
		// config is ~/.wakatime.cfg before install, "" for none.
		config string
		// skip runs install with --skip wakatime.
		skip bool
		// want is how the config starts after install, "" for as it was;
		// line is what pairtime's line starts with.
		want, line string
		// out is a part of what install prints.
		out string
	}{
		{
			name:   "with hackatime-setup's config, install adds pairtime's line and uninstall gives the file back",
			config: teenHackatimeConfig,
			want:   teenHackatimeConfig + "\n[api_urls]\n",
			line:   ".+ = http://127.0.0.1:",
			out:    "Added pairtime's line to ",
		},
		{
			name: "with no WakaTime config, install makes one for the agent with wakatime-cli's watermark, and uninstall deletes both",
			want: "[settings]\n",
			line: "api_url = http://127.0.0.1:",
			out:  "Made ",
		},
		{
			name:   "with --skip wakatime, install leaves the config as it was",
			config: teenHackatimeConfig,
			skip:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHome(t, "real").withEnv(wakatimeEnv, "1")
			if tt.config != "" {
				h.write(".wakatime.cfg", tt.config)
			}
			before := h.snapshot()
			args := []string{"install", "--yes"}
			if tt.skip {
				args = append(args, "--skip", install.ItemWakaTime)
			}
			out := h.mustRun("", "", 0, args...)
			pid := h.installed(false)
			if strings.Contains(out, teenHackatimeKey) {
				t.Errorf("install printed the teen's Hackatime key:\n%s", out)
			}
			cfg := h.read(".wakatime.cfg")
			s, err := wakacfg.ReadSetup(wakacfg.SetupFile(h.dirs.Data))
			switch {
			case tt.want == "":
				if cfg != tt.config {
					t.Errorf("install changed the config:\n%s", cfg)
				}
				if err == nil {
					t.Errorf("install wrote a WakaTime setup file: %+v", s)
				}
			case err != nil:
				t.Fatalf("the setup file: %v", err)
			default:
				rest, ok := strings.CutPrefix(cfg, tt.want)
				if !ok || !strings.HasPrefix(rest, tt.line) || !strings.Contains(rest, s.Key) {
					t.Errorf("after install the config holds\n%s\nwant it to start %q, then %q, with the setup file's key %s", cfg, tt.want, tt.line, s.Key)
				}
				if st, err := wakacfg.Inspect([]byte(cfg), s.Key); err != nil || !st.Line {
					t.Errorf("Inspect = %+v, %v, want pairtime's line", st, err)
				}
				if !strings.Contains(out, tt.out) {
					t.Errorf("install didn't say %q:\n%s", tt.out, out)
				}
				if tt.config == "" && !strings.Contains(h.read(".wakatime/wakatime-internal.cfg"), "[internal]\nai_logs_last_parsed_at = ") {
					t.Errorf("install didn't seed wakatime-cli's watermark: %q", h.read(".wakatime/wakatime-internal.cfg"))
				}
			}

			// A second install changes nothing but the port, which follows
			// the agent's: another test's agent may have held the default
			// when this one started.
			again := h.mustRun("", "", 0, args...)
			if got := h.read(".wakatime.cfg"); anyPort(got) != anyPort(cfg) {
				t.Errorf("a second install changed the config:\n%s\n(it printed\n%s)", got, again)
			}

			h.mustRun("", "", 0, "uninstall", "--yes", "--delete-data")
			h.uninstalled(before, pid)
		})
	}
}
