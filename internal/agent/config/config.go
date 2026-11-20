// SPDX-License-Identifier: AGPL-3.0-or-later

// Package config reads config.toml, the settings a teen can edit
// (DESIGN 3.5).
//
// A config.toml that doesn't parse is ignored as a whole, and a setting
// with a bad value keeps its default. Every default is the private choice:
// nothing is uploaded, and nothing optional is turned on. So a typo can
// make pairtime more private, never less. Notifications are on by default
// (DESIGN 1.2), so an ignored file, or quiet hours that don't parse, turn
// them off instead: the file may be where the teen turned them off.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/BurntSushi/toml"

	"github.com/some-du6e/pairtime/internal/agent/home"
)

// Mode says whether anything leaves the device (DESIGN 4.2).
type Mode string

// Local uploads nothing: no buckets, no roots, no profile. Recaps and the
// coach still work, and hours logged in this mode are never witnessed, so
// they can't be submitted (DESIGN 4.2). The alpha runs in it (DESIGN 8.6),
// and it is the only mode until signing in exists (WP-SV3).
const Local Mode = "local"

// Config is config.toml.
type Config struct {
	Mode Mode `toml:"mode"`
	HTTP HTTP `toml:"http"`
	// Notifications is [notifications], which Parse reads through
	// notificationsFile.
	Notifications Notifications `toml:"-"`
}

// fileConfig is config.toml as written. Parse checks each value before it
// goes into a Config.
type fileConfig struct {
	Mode          Mode              `toml:"mode"`
	HTTP          HTTP              `toml:"http"`
	Notifications notificationsFile `toml:"notifications"`
}

// HTTP configures the agent's HTTP server on 127.0.0.1.
type HTTP struct {
	// Port is the port to try first. 0 means the default.
	Port int `toml:"port"`
}

// Default is the configuration of a config.toml with nothing in it.
func Default() Config {
	return Config{Mode: Local}
}

// Parse reads the contents of a config.toml. The Config it returns is
// always usable; problems says what was ignored and why.
func Parse(data []byte) (cfg Config, problems []string) {
	cfg = Default()
	var file fileConfig
	md, err := toml.Decode(string(data), &file)
	if err != nil {
		return ignored(), []string{fmt.Sprintf("config.toml was ignored, so every setting has its default and notifications are off: %v", err)}
	}
	// Probably a typo, or a setting from a newer release.
	for _, key := range md.Undecoded() {
		problems = append(problems, fmt.Sprintf("config.toml: the unknown setting %q was ignored", key.String()))
	}
	if md.IsDefined("mode") {
		if file.Mode == Local {
			cfg.Mode = file.Mode
		} else {
			problems = append(problems, fmt.Sprintf("config.toml: this release has no mode %q, so pairtime runs in mode %q", file.Mode, Local))
		}
	}
	if md.IsDefined("http", "port") {
		if p := file.HTTP.Port; p == 0 || (p >= 1024 && p <= 65535) {
			cfg.HTTP.Port = p
		} else {
			problems = append(problems, fmt.Sprintf("config.toml: http.port %d isn't 0 or from 1024 to 65535, so the default port is used", p))
		}
	}
	problems = append(problems, parseNotifications(&cfg, file.Notifications, md.IsDefined)...)
	return cfg, problems
}

// Load reads and parses the file name. When the file is missing it returns
// the defaults and an error that wraps fs.ErrNotExist. When it can't be
// read for another reason, it returns the defaults with notifications off,
// like a file that doesn't parse, and the error.
func Load(name string) (Config, []string, error) {
	data, err := os.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil, err
	}
	if err != nil {
		return ignored(), nil, err
	}
	cfg, problems := Parse(data)
	return cfg, problems, nil
}

// WriteDefault writes the default config.toml, with comments, to name.
func WriteDefault(name string) error {
	return home.WriteFile(name, []byte(defaultFile))
}

// defaultFile is the config.toml that the agent's first start writes.
// Parsing it gives Default() and no problems.
const defaultFile = `# pairtime settings. The agent reads this file when it starts and again
# whenever you save it.

# Where your time goes. "local" keeps everything on this computer: nothing
# is uploaded, and recaps still work. It's the only mode in this release.
mode = "local"

[http]
# The port of pairtime's web server, which listens on 127.0.0.1 only.
# 0 means the default, 7247, or a free port near it when that one is taken.
port = 0

[notifications]
# One quiet notification when a work session ends, to say its recap is
# ready. false turns them off.
enabled = true
# A time of day with no notifications, in this computer's local time, such
# as "22:00-07:00". "" means none.
quiet_hours = ""
`
