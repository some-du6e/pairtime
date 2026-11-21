// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"errors"
	"io/fs"
	"os"
)

// LoadParsed is Load, and also reports whether the file parsed, as Parse
// has it: false for a file that can't be read, isn't TOML or has a value
// of the wrong type, which Parse ignores as a whole, so every setting has
// its default and the mode is local. A missing file counts as parsed,
// since the defaults are then the settings. A file that didn't parse keeps
// the computer local-only until it's fixed: a typo can make pairtime more
// private, never less.
func LoadParsed(name string) (cfg Config, problems []string, parsed bool, err error) {
	data, err := os.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil, true, err
	}
	if err != nil {
		return ignored(), nil, false, err
	}
	cfg, problems, parsed = parse(data)
	return cfg, problems, parsed, nil
}
