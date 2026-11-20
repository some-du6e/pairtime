// SPDX-License-Identifier: AGPL-3.0-or-later

package home

import (
	"errors"
	"path/filepath"
)

// osDirs puts data and config.toml in ~/Library/Application Support/pairtime,
// and the socket in $TMPDIR/pairtime, which macOS makes per user (CP 7).
func osDirs(getenv func(string) string, userHome string) (Dirs, error) {
	if !filepath.IsAbs(userHome) {
		return Dirs{}, errors.New("no home directory; set HOME, or " + EnvHome + " to choose a directory")
	}
	data := filepath.Join(userHome, "Library", "Application Support", "pairtime")
	run := data
	if tmp := getenv("TMPDIR"); filepath.IsAbs(tmp) {
		run = filepath.Join(tmp, "pairtime")
	}
	return Dirs{Data: data, Config: data, Runtime: run}, nil
}
