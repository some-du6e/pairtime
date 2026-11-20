// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var version string

// Version returns the scoring_version of this package, the v of every
// minute bucket and rollup it writes (SM 7.6). It comes from pkg/score/VERSION,
// which a PR that changes the scorer's output on the regression corpus
// bumps, together with the goldens (DESIGN 6.6).
func Version() string { return strings.TrimSpace(version) }
