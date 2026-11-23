// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/internal/agent/config"
)

// TestPlanCalibration checks what pairtime install --calibration-volunteer
// offers (planCalibration): the diff of config.toml that the consent
// screen and --dry-run show, or why it offers nothing.
func TestPlanCalibration(t *testing.T) {
	// changed returns the lines diff takes out and puts in.
	changed := func(diff string) (out, in []string) {
		for _, line := range strings.Split(diff, "\n") {
			switch {
			case strings.HasPrefix(line, "---"), strings.HasPrefix(line, "+++"):
			case strings.HasPrefix(line, "-"):
				out = append(out, line)
			case strings.HasPrefix(line, "+"):
				in = append(in, line)
			}
		}
		return out, in
	}
	const agentsFile = "the file the agent writes"
	tests := []struct {
		name string
		// file is config.toml, "" for none, or agentsFile for the default
		// file the agent writes at its first start.
		file        string
		calibrating bool
		check       func(t *testing.T, diff, why string)
	}{
		{"once pairtime's scoring is frozen, install offers nothing and says it has reached 1.0.0, without promising the rules were fitted", agentsFile, false,
			func(t *testing.T, diff, why string) {
				if diff != "" || !strings.Contains(why, "reached version 1.0.0, so its rules are frozen") || strings.Contains(why, "fitted") {
					t.Errorf("diff %q, why %q", diff, why)
				}
			}},
		{"with no config.toml, the diff writes the whole file the agent would, with shadow on", "", true,
			func(t *testing.T, diff, why string) {
				out, in := changed(diff)
				if why != "" || !strings.HasPrefix(diff, "--- /dev/null\n") || len(out) != 0 || !strings.Contains(diff, "\n+shadow = true\n") || len(in) < 10 {
					t.Errorf("why %q, diff:\n%s", why, diff)
				}
			}},
		{"in config.toml as the agent writes it, the diff changes the shadow line alone", agentsFile, true,
			func(t *testing.T, diff, why string) {
				out, in := changed(diff)
				if why != "" || len(out) != 1 || out[0] != "-shadow = false" || len(in) != 1 || in[0] != "+shadow = true" {
					t.Errorf("why %q, diff:\n%s", why, diff)
				}
			}},
		{"a config.toml that says shadow = true already shows no change", "[display]\nshadow = true\n", true,
			func(t *testing.T, diff, why string) {
				if diff != "" || why != "" {
					t.Errorf("diff %q, why %q, want neither", diff, why)
				}
			}},
		{"the diff masks what the teen wrote around the line, a comment and a project's name",
			"[display]\n# my secret plan\nshadow = false\n\n[projects.g7c41e9a2d05b8f63e1a94c7d2b6f0e85]\nname = \"secret-game\"\n", true,
			func(t *testing.T, diff, why string) {
				if why != "" || strings.Contains(diff, "secret") || !strings.Contains(diff, "\n # ***\n") || !strings.Contains(diff, "\n name = \"***\"\n") || !strings.Contains(diff, "\n+shadow = true\n") {
					t.Errorf("why %q, diff:\n%s", why, diff)
				}
			}},
		{"a config.toml that doesn't parse is left out, and why says what to write by hand", "[display\n", true,
			func(t *testing.T, diff, why string) {
				if diff != "" || !strings.Contains(why, "doesn't parse") || !strings.Contains(why, "by hand") || strings.HasPrefix(why, "config:") {
					t.Errorf("diff %q, why %q", diff, why)
				}
			}},
		{"for a config.toml that doesn't parse, why gives the line it breaks at and never the decoder's words, which quote what the teen wrote", "[display]\nshadow = mysecretword\n", true,
			func(t *testing.T, diff, why string) {
				if diff != "" || !strings.Contains(why, "config.toml doesn't parse at line ") || !strings.Contains(why, "so pairtime leaves it alone; set shadow = true under [display]") ||
					strings.Contains(why, "mysecretword") || strings.Contains(why, "toml:") || strings.Contains(why, "expected value") {
					t.Errorf("diff %q, why %q", diff, why)
				}
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := filepath.Join(t.TempDir(), "config.toml")
			switch tt.file {
			case "":
			case agentsFile:
				if err := config.WriteDefault(name); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.WriteFile(name, []byte(tt.file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(name)
			diff, why := planCalibration(name, tt.calibrating)
			tt.check(t, diff, why)
			if after, _ := os.ReadFile(name); string(after) != string(before) {
				t.Errorf("planning changed config.toml from %q to %q", before, after)
			}
		})
	}
}
