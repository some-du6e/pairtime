// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix && !darwin

package home

import (
	"fmt"
	"path/filepath"
)

// osDirs follows the XDG Base Directory spec (CP 7): data in
// $XDG_DATA_HOME/pairtime (default ~/.local/share), config.toml in
// $XDG_CONFIG_HOME/pairtime (default ~/.config), and the socket in
// $XDG_RUNTIME_DIR/pairtime, or in the data directory when that variable
// isn't set. The spec says to ignore a relative path in these variables.
func osDirs(getenv func(string) string, userHome string) (Dirs, error) {
	base := func(env string, fallback ...string) (string, error) {
		if v := getenv(env); filepath.IsAbs(v) {
			return v, nil
		}
		if !filepath.IsAbs(userHome) {
			return "", fmt.Errorf("neither %s nor a home directory is set; set %s to choose a directory", env, EnvHome)
		}
		return filepath.Join(append([]string{userHome}, fallback...)...), nil
	}
	dataBase, err := base("XDG_DATA_HOME", ".local", "share")
	if err != nil {
		return Dirs{}, err
	}
	configBase, err := base("XDG_CONFIG_HOME", ".config")
	if err != nil {
		return Dirs{}, err
	}
	data := filepath.Join(dataBase, "pairtime")
	run := data
	if r := getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(r) {
		run = filepath.Join(r, "pairtime")
	}
	return Dirs{Data: data, Config: filepath.Join(configBase, "pairtime"), Runtime: run}, nil
}
