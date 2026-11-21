// SPDX-License-Identifier: AGPL-3.0-or-later

// Package deploy embeds the deploy bundle's files that pairtime's code
// reads, so each stays a single copy here, next to compose.yml: a
// //go:embed pattern can't reach a parent directory, so the embedding has
// to live in this directory (as docs/embed.go does for docs/).
package deploy

import _ "embed"

// ConfigTemplate is pairtime.toml.tmpl, the text/template `pairtimed
// init` writes config/pairtime.toml from (DESIGN 10.2).
//
//go:embed pairtime.toml.tmpl
var ConfigTemplate string

// LitestreamYAML is litestream.yml. Only tests read it, to check that it
// replicates the database pairtime.toml.tmpl names, to the replica path
// serve's -litestream-replica measures.
//
// compose.yml is embedded in a test file, compose_test.go, because only
// this package's tests read it: pairtimed imports this package, so a file
// embedded here is one the server image is built from, and a pin in
// compose.yml would then build an image (server-image.yml's paths).
//
//go:embed litestream.yml
var LitestreamYAML string
