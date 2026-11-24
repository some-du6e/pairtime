// SPDX-License-Identifier: AGPL-3.0-or-later

package t3code

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// settingsName is T3's settings file, which sits beside its store in the
// userdata folder of the T3 home (apps/server/src/config.ts at T3
// d8f3feff).
const settingsName = "settings.json"

// maxSettings bounds how much of T3's settings ClaudeHomes reads. T3's are
// a few KiB.
const maxSettings = 4 << 20

// claudeHomeVar is the variable that moves Claude Code's whole folder, its
// settings and hooks among them, which T3 sets for a Claude instance that
// has a home of its own (apps/server/src/provider/Drivers/ClaudeHome.ts).
const claudeHomeVar = "CLAUDE_CONFIG_DIR"

// t3Instance is what ClaudeHomes reads of one of T3's provider instances
// (packages/contracts/src/providerInstance.ts): its driver, whether it is
// on, its environment and its driver's config.
type t3Instance struct {
	Driver      string          `json:"driver"`
	Enabled     *bool           `json:"enabled"`
	Environment []t3EnvVar      `json:"environment"`
	Config      json.RawMessage `json:"config"`
}

// t3EnvVar is one variable of an instance's environment. A secret's value
// is stored apart, with an empty value here.
type t3EnvVar struct {
	Name  string          `json:"name"`
	Value json.RawMessage `json:"value"`
}

// t3ClaudeConfig is what ClaudeHomes reads of a Claude instance's config
// (packages/contracts/src/settings.ts, ClaudeSettings): its
// "CLAUDE_CONFIG_DIR path", and the enabled flag older settings kept there.
type t3ClaudeConfig struct {
	Enabled  *bool  `json:"enabled"`
	HomePath string `json:"homePath"`
}

// ClaudeHomes returns the Claude homes, each one once and sorted, that T3
// Code gives the Claude instances it runs Claude Code in when it gives
// them homes of their own, from settings.json beside store: an enabled
// claudeAgent instance's "CLAUDE_CONFIG_DIR path", or else the
// CLAUDE_CONFIG_DIR its environment sets (ClaudeHome.ts at T3 d8f3feff),
// and the claudeAgent entry of the providers map older T3 versions wrote,
// which T3 takes for the instance of that id when there is none
// (serverSettings.ts). A path is resolved as T3CODE_HOME is (resolveHome):
// ~ is home, and a relative one is taken against home. Claude Code reads
// its settings, its hooks among them, from such a home rather than from
// ~/.claude, so pairtime's hooks run there only if that home's
// settings.json holds them (DESIGN 5.1, 5.9). A home T3's server gets from
// its own environment isn't in the file, so it isn't here. No settings.json
// gives none. It reads that file alone, and no error names a path.
func ClaudeHomes(store Store, home string) ([]string, error) {
	b, err := readSettings(filepath.Join(filepath.Dir(store.Path), settingsName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s struct {
		ProviderInstances map[string]json.RawMessage `json:"providerInstances"`
		Providers         map[string]json.RawMessage `json:"providers"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, errors.New("T3 Code's settings aren't JSON that pairtime reads")
	}
	var out []string
	add := func(raw string) {
		if p := resolveHome(raw, home); p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	for _, raw := range s.ProviderInstances {
		var in t3Instance
		// An instance T3 itself can't decode runs nothing.
		if json.Unmarshal(raw, &in) != nil || in.Driver != claudeDriver {
			continue
		}
		var cfg t3ClaudeConfig
		_ = json.Unmarshal(in.Config, &cfg)
		if turnedOff(in.Enabled) || turnedOff(cfg.Enabled) {
			continue
		}
		if h := strings.TrimSpace(cfg.HomePath); h != "" {
			add(h)
			continue
		}
		add(envHome(in.Environment))
	}
	if _, ok := s.ProviderInstances[claudeDriver]; !ok {
		var cfg t3ClaudeConfig
		if raw, ok := s.Providers[claudeDriver]; ok && json.Unmarshal(raw, &cfg) == nil && !turnedOff(cfg.Enabled) {
			add(cfg.HomePath)
		}
	}
	slices.Sort(out)
	return out, nil
}

// turnedOff reports whether an enabled flag turns its instance off. One
// that isn't set leaves it on, as T3 has a Claude instance by default.
func turnedOff(enabled *bool) bool { return enabled != nil && !*enabled }

// envHome is the CLAUDE_CONFIG_DIR an instance's environment sets, the last
// one when it sets it twice, as T3 merges them in order; Windows reads the
// name without regard to case. A secret's empty value, and a value that
// isn't a string, set nothing.
func envHome(vars []t3EnvVar) string {
	home := ""
	for _, v := range vars {
		if !strings.EqualFold(strings.TrimSpace(v.Name), claudeHomeVar) {
			continue
		}
		var s string
		if json.Unmarshal(v.Value, &s) == nil && strings.TrimSpace(s) != "" {
			home = strings.TrimSpace(s)
		}
	}
	return home
}

// readSettings reads T3's settings file name, of at most maxSettings bytes.
// Its errors name no path: one that the file isn't there still matches
// fs.ErrNotExist.
func readSettings(name string) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		var pe *fs.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fs.ErrNotExist
		}
		return nil, errors.New("T3 Code's settings don't open: " + err.Error())
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxSettings+1))
	switch {
	case err != nil:
		return nil, errors.New("T3 Code's settings don't read")
	case len(b) > maxSettings:
		return nil, errors.New("T3 Code's settings are bigger than pairtime reads")
	}
	return b, nil
}
