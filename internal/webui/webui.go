// SPDX-License-Identifier: AGPL-3.0-or-later

// Package webui holds the local dashboard's built files, which the agent
// serves on 127.0.0.1 (DESIGN 3.9).
//
// dist/ is what Vite builds from web/src/local: `npm run build` in web/
// writes it (vite build --mode agent), and web.yml and release.yml run that
// before they build Go (DESIGN 3.12; JE must-fix 8). The repo carries only
// a placeholder dist/index.html, so go build, go test and every Go-only CI
// job work without Node. A build overwrites it; `git checkout --
// internal/webui/dist` puts it back. .gitignore keeps out the assets a
// build writes, so a built index.html committed by mistake fails TestFS in
// ci.yml, which finds the files it loads missing.
//
// The <meta name="pairtime-ui"> in index.html says which one is embedded:
// "placeholder" or "local".
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
