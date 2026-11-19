// SPDX-License-Identifier: AGPL-3.0-or-later

package home

import (
	"errors"
	"path/filepath"
)

// osDirs puts data and the socket in %LOCALAPPDATA%\pairtime, and
// config.toml in %APPDATA%\pairtime, which roams with the user (CP 7).
func osDirs(getenv func(string) string, _ string) (Dirs, error) {
	local, roaming := getenv("LOCALAPPDATA"), getenv("APPDATA")
	if !filepath.IsAbs(local) || !filepath.IsAbs(roaming) {
		return Dirs{}, errors.New("LOCALAPPDATA and APPDATA must both be absolute paths; set " + EnvHome + " to choose a directory instead")
	}
	data := filepath.Join(local, "pairtime")
	return Dirs{Data: data, Config: filepath.Join(roaming, "pairtime"), Runtime: data}, nil
}
