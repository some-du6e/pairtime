// SPDX-License-Identifier: AGPL-3.0-or-later

// Package webui holds the local dashboard's built files, which the agent
// serves on 127.0.0.1 (DESIGN 3.9).
//
// dist/ is what Vite builds from web/ (WP-UI0). The repo carries a
// placeholder dist/index.html, so go build, go test and every Go-only CI
// job work without Node; release.yml and web.yml build the real UI into
// dist/ before they build Go (DESIGN 3.12; JE must-fix 8).
package webui

import (
	"embed"
	"io/fs"
)

//go:embed dist
var dist embed.FS

// FS returns the built UI, with index.html at its root.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		// fs.Sub fails only for an invalid path, and "dist" is valid.
		panic(err)
	}
	return sub
}
