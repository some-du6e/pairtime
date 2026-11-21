// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"errors"
	"io/fs"
	"os"

	"github.com/BurntSushi/toml"
)

// LoadParsed is Load, and also reports whether the file parsed as TOML. Load
// gives every setting its default for a file that doesn't, which says
// nothing of the settings the teen chose, such as the mode: the agent
// keeps the mode it last read then (the uploader's LocalOnly). A missing
// file counts as parsed, since the defaults are then the settings.
func LoadParsed(name string) (cfg Config, problems []string, parsed bool, err error) {
	data, err := os.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil, true, err
	}
	if err != nil {
		return ignored(), nil, false, err
	}
	cfg, problems = Parse(data)
	var probe map[string]any
	_, perr := toml.Decode(string(data), &probe)
	return cfg, problems, perr == nil, nil
}
