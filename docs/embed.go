// SPDX-License-Identifier: AGPL-3.0-or-later

// Package docs embeds the files under docs/ that pairtime's code reads, so
// each stays a single copy: a //go:embed pattern can't reach a parent
// directory, so the embedding has to live here.
package docs

import _ "embed"

// RetentionJSON is docs/retention.json: how long pairtime keeps each kind
// of data (DESIGN 4.4). The privacy notice's retention table and
// pairtimed's retention jobs both read it, through package
// internal/server/retention.
//
//go:embed retention.json
var RetentionJSON []byte
