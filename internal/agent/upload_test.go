// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"log/slog"
	"os"
	"testing"

	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/home"
)

func TestLocalOnlyMode(t *testing.T) {
	reads := []struct {
		name         string
		cfg          config.Config
		local, known bool
	}{
		{"a config.toml that reads mode = \"local\" is local-only", config.Config{Mode: config.Local}, true, true},
		{"one that reads mode = \"online\" isn't", config.Config{Mode: modeOnline}, false, true},
		{"one with no mode is local-only, as its default is", config.Config{}, true, true},
	}
	for _, tc := range reads {
		t.Run(tc.name, func(t *testing.T) {
			var a agent
			a.mode.Store(modeRead(tc.cfg))
			if local, known := a.localOnly(); local != tc.local || known != tc.known {
				t.Errorf("localOnly() = %v, %v; want %v, %v", local, known, tc.local, tc.known)
			}
		})
	}

	// loadConfig as the agent runs it, on files that read and files that
	// don't.
	dir := t.TempDir()
	dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
	newAgent := func() *agent {
		return &agent{dirs: dirs, log: slog.New(slog.DiscardHandler), problems: map[string]string{}}
	}
	write := func(t *testing.T, text string) {
		t.Helper()
		if err := os.WriteFile(dirs.ConfigFile(), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	loads := []struct {
		name         string
		run          func(t *testing.T) *agent
		local, known bool
	}{
		{"before config.toml has read, the mode isn't known", func(t *testing.T) *agent { return newAgent() }, false, false},
		{"a file that reads sets the mode", func(t *testing.T) *agent {
			write(t, "mode = \"local\"\n")
			a := newAgent()
			a.loadConfig(true)
			return a
		}, true, true},
		{"a file that isn't TOML, as after a typo, leaves the mode as it last read", func(t *testing.T) *agent {
			write(t, "mode = \"local\"\n")
			a := newAgent()
			a.loadConfig(true)
			write(t, "mode = \"loc") // an editor halfway through writing it
			a.loadConfig(false)
			return a
		}, true, true},
		{"a file that isn't TOML at the start leaves the mode unknown", func(t *testing.T) *agent {
			write(t, "mode = [")
			a := newAgent()
			a.loadConfig(true)
			return a
		}, false, false},
		{"no config.toml reads as the defaults, which are local-only", func(t *testing.T) *agent {
			if err := os.Remove(dirs.ConfigFile()); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			a := newAgent()
			a.loadConfig(false)
			return a
		}, true, true},
	}
	for _, tc := range loads {
		t.Run(tc.name, func(t *testing.T) {
			a := tc.run(t)
			if local, known := a.localOnly(); local != tc.local || known != tc.known {
				t.Errorf("localOnly() = %v, %v; want %v, %v", local, known, tc.local, tc.known)
			}
		})
	}
}
