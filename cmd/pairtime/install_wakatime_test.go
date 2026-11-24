// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/localhttp"
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

// markFor returns the first line of the config pairtime makes for key:
// pairtime's mark, which names a hash of the key.
func markFor(t *testing.T, key string) string {
	t.Helper()
	made, err := wakacfg.Make(key, 7247)
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := strings.Cut(string(made), "\n")
	return first
}

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
		// skip runs install with --skip wakatime, and proxy with HTTPS_PROXY
		// set to it.
		skip  bool
		proxy string
		// want is how the config starts after install, "" for as it was,
		// with <mark> for pairtime's mark for the setup file's key; line is
		// what pairtime's line starts with.
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
			want: "<mark>\n[settings]\n",
			line: "api_url = http://127.0.0.1:",
			out:  "Made ",
		},
		{
			name:   "with --skip wakatime, install leaves the config as it was",
			config: teenHackatimeConfig,
			skip:   true,
		},
		{
			name:   "with HTTPS_PROXY set, install leaves the teen's config as it was and says why, naming the variable and never its value",
			config: teenHackatimeConfig,
			proxy:  "http://teen:secret@proxy.example:3128",
			out:    "but HTTPS_PROXY is set here, and wakatime-cli started with it would send the line through that proxy too",
		},
		{
			name:   "with a config that sets a proxy, install leaves the config as it was and says why",
			config: teenHackatimeConfig + "proxy = http://proxy.example:3128\n",
			out:    "sets a proxy, and wakatime-cli would send pairtime's line through it too",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHome(t, "real").withEnv(wakatimeEnv, "1")
			if tt.proxy != "" {
				h = h.withEnv("HTTPS_PROXY", tt.proxy)
			}
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
			if !strings.Contains(out, tt.out) {
				t.Errorf("install didn't say %q:\n%s", tt.out, out)
			}
			if strings.Contains(out, "secret") {
				t.Errorf("install printed the proxy's password:\n%s", out)
			}
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
				want := strings.ReplaceAll(tt.want, "<mark>", markFor(t, s.Key))
				rest, ok := strings.CutPrefix(cfg, want)
				if !ok || !strings.HasPrefix(rest, tt.line) || !strings.Contains(rest, s.Key) {
					t.Errorf("after install the config holds\n%s\nwant it to start %q, then %q, with the setup file's key %s", cfg, want, tt.line, s.Key)
				}
				st, err := wakacfg.Inspect([]byte(cfg), s.Key)
				if err != nil || !st.Line {
					t.Errorf("Inspect = %+v, %v, want pairtime's line", st, err)
				}
				if pf, err := localhttp.ReadPortFile(h.dirs.PortFile()); err != nil || pf.Port != st.Port {
					t.Errorf("pairtime's line names port %d, and the agent listens on %d (%v)", st.Port, pf.Port, err)
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

// TestWakaTimeTeensKeyNeverShown runs the real install and uninstall on a
// config pairtime made whose api_key the teen replaced with their own, as
// WakaTime's extension writes one, with no record of pairtime's key left:
// nothing install or uninstall prints, --dry-run included, and no setup
// file holds the teen's key, and both leave the config to the teen, which
// uninstall names as what has to stay, its dry run included.
func TestWakaTimeTeensKeyNeverShown(t *testing.T) {
	if v, ok := runValue(); ok {
		t.Skipf(`this account has a pairtime login item, HKCU\%s\%s = %q, which the test would replace; it runs where there is none, as in CI`, install.RunKey, install.RunValue, v)
	}
	tests := []struct {
		name string
		port int // the port the config's api_url names: not the one install plans, so a change would show its api_key beside it
	}{
		{"install, --dry-run and uninstall show the teen's key in a config pairtime made nowhere, and leave it to the teen", 7250},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			made, err := wakacfg.Make("00000000-0000-4000-8000-00000000a9e7", tt.port)
			if err != nil {
				t.Fatal(err)
			}
			taken := strings.Replace(string(made), "api_key = 00000000-0000-4000-8000-00000000a9e7", "api_key = "+teenHackatimeKey, 1)
			h := newTestHome(t, "real").withEnv(wakatimeEnv, "1")
			h.write(".wakatime.cfg", taken)
			before := h.snapshot()
			shown := func(what, out string) {
				t.Helper()
				if strings.Contains(out, teenHackatimeKey) {
					t.Errorf("%s printed the teen's key:\n%s", what, out)
				}
			}
			shown("install --dry-run", h.mustRun("", "", 0, "install", "--dry-run"))
			out := h.mustRun("", "", 0, "install", "--yes")
			shown("install --yes", out)
			if !strings.Contains(out, "isn't pairtime's any more") {
				t.Errorf("install didn't say why it left the config alone:\n%s", out)
			}
			pid := h.installed(false)
			if s, err := wakacfg.ReadSetup(wakacfg.SetupFile(h.dirs.Data)); err == nil && s.Key == teenHackatimeKey {
				t.Errorf("the setup file holds the teen's key")
			}
			dry := h.mustRun("", "", 0, "uninstall", "--dry-run")
			shown("uninstall --dry-run", dry)
			if !strings.Contains(dry, "pairtime uninstall: this has to stay: ") || !strings.Contains(dry, "point its api_url at your own server, or delete it") {
				t.Errorf("uninstall --dry-run didn't name the config a real uninstall leaves alone:\n%s", dry)
			}
			out = h.mustRun("", "", 1, "uninstall", "--yes", "--delete-data")
			shown("uninstall", out)
			if !strings.Contains(out, "Left alone: ") || !strings.Contains(out, "point its api_url at your own server, or delete it") {
				t.Errorf("uninstall didn't name the config it left alone, with what to change:\n%s", out)
			}
			if got := h.read(".wakatime.cfg"); got != taken {
				t.Errorf("the config is now\n%s\nwant it as the teen left it", got)
			}
			h.uninstalled(before, pid)
		})
	}
}

// TestWakaTimeReplan checks the WakaTime item install plans again once the
// agent is up, on the port it listens on: it is taken when each file it
// leaves is what the screen's plan leaves with the port swapped, a plan
// that changes nothing included, and refused when anything else changed
// since the screen.
func TestWakaTimeReplan(t *testing.T) {
	const key = "00000000-0000-4000-8000-00000000a9e7"
	line := func(port int) string { return ".+ = " + wakacfg.URL(port) + "|" + key + "\n" }
	tests := []struct {
		name   string
		config string // ~/.wakatime.cfg before install, "" for none
		// screen and agent are the ports the screen planned on and the
		// agent listens on.
		screen, agent int
		// between changes the config after the screen; refused says the
		// new plan is refused.
		between string
		refused bool
		// diff is a part of the new plan's diff, "" for none at all.
		diff string
	}{
		{name: "a first install's line on the agent's port differs from the screen's in the port alone, and is taken", config: teenHackatimeConfig, screen: 7247, agent: 7250, diff: "+" + line(7250)},
		{name: "a config pairtime makes on the agent's port differs from the screen's in the port alone, and is taken", screen: 7247, agent: 7250, diff: "+api_url = " + wakacfg.URL(7250)},
		{name: "a reinstall whose line names the agent's port already, while the screen planned config.toml's, changes nothing and is taken", config: teenHackatimeConfig + "\n[api_urls]\n" + line(7250), screen: 7247, agent: 7250},
		{name: "a reinstall whose line names the screen's port, while the agent listens on another, moves it and is taken", config: teenHackatimeConfig + "\n[api_urls]\n" + line(7247), screen: 7247, agent: 7250, diff: "+" + line(7250)},
		{name: "a config that changed since the screen in more than the port is refused", config: teenHackatimeConfig, screen: 7247, agent: 7250, between: "hostname = mine\n", refused: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			cfg := filepath.Join(home, ".wakatime.cfg")
			if tt.config != "" {
				if err := os.WriteFile(cfg, []byte(tt.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			exe := filepath.Join(home, "bin", "pairtime")
			if runtime.GOOS == "windows" {
				exe += ".exe"
			}
			env := install.Env{GOOS: runtime.GOOS, Home: home, Getenv: func(string) string { return "" }, Exe: exe}
			data := t.TempDir()
			in := &install.Installer{Dir: filepath.Join(data, "install")}
			o := install.WakaTimeOptions{Data: data, Port: tt.screen, Now: time.Date(2026, 11, 24, 5, 30, 0, 0, time.UTC), Key: key}
			screen, err := install.WakaTime(env, o)
			if err != nil {
				t.Fatal(err)
			}
			p, err := in.PlanInstall([]install.Item{screen})
			if err != nil {
				t.Fatal(err)
			}
			shown := p.Planned()
			p.Close()
			if tt.between != "" {
				b, err := os.ReadFile(cfg)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(cfg, append(b, tt.between...), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			_, diff, err := wakaTimeReplan(env, in, o, shown, tt.agent)
			switch {
			case tt.refused:
				if err == nil {
					t.Fatalf("wakaTimeReplan took a plan of a config that changed since the screen:\n%s", diff)
				}
			case err != nil:
				t.Fatalf("wakaTimeReplan: %v", err)
			case tt.diff == "" && diff != "":
				t.Errorf("the new plan's diff is\n%s\nwant none", diff)
			case tt.diff != "" && (!strings.Contains(diff, tt.diff) || strings.Contains(diff, "+"+line(tt.screen))):
				t.Errorf("the new plan's diff is\n%s\nwant it to have %q and nothing on the screen's port", diff, tt.diff)
			}
		})
	}
}
