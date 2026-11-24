// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"

	"github.com/some-du6e/pairtime/pkg/params"
)

// semver matches the MAJOR.MINOR.PATCH scoring versions of DESIGN 6.6.
var semver = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)

// parseVersion splits a MAJOR.MINOR.PATCH version into its numbers.
func parseVersion(s string) ([3]int, bool) {
	m := semver.FindStringSubmatch(s)
	if m == nil {
		return [3]int{}, false
	}
	var v [3]int
	for i := range v {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v, true
}

// Scoring returns the scoring version a freeze records and the Hash of the
// parameter table that version scores with (D-sb1-submissions, Q10):
// configured, pairtime.toml's scoring_version, or builtin, the built-in
// scorer's, when configured is empty, as /docs/evidence shows the version
// the server runs; and the newest table pkg/params embeds whose version is
// at or below it, which for the built-in version is the newest table, the
// one the agent stamps its buckets with.
//
// It refuses a configured version newer than builtin, or, while builtin is
// 0.x, one with another MINOR, because a 0.x MINOR can change a rule
// (DESIGN 6.6) and the server couldn't re-score at it. pairtimed calls it
// at start and doesn't start on its error.
func Scoring(configured, builtin string) (version, paramsHash string, err error) {
	b, ok := parseVersion(builtin)
	if !ok {
		return "", "", fmt.Errorf("submission: the built-in scoring version %q isn't of the form X.Y.Z", builtin)
	}
	v := b
	version = builtin
	if configured != "" {
		c, ok := parseVersion(configured)
		switch {
		case !ok:
			return "", "", fmt.Errorf("scoring_version %q isn't of the form X.Y.Z (DESIGN 6.6)", configured)
		case slices.Compare(c[:], b[:]) > 0:
			return "", "", fmt.Errorf("scoring_version is %s, newer than this release's built-in scorer, %s, so the server couldn't re-score a submission at it; leave scoring_version empty or set a version this release scores (DESIGN 6.6)", configured, builtin)
		case b[0] == 0 && c[1] != b[1]:
			return "", "", fmt.Errorf("scoring_version is %s, and this release's built-in scorer is %s: before 1.0.0 another MINOR can change a rule (DESIGN 6.6), so the server couldn't re-score a submission at it; leave scoring_version empty or set %d.%d.x", configured, builtin, b[0], b[1])
		}
		v, version = c, configured
	}
	var table *params.Table
	for _, t := range params.Tables() {
		tv, ok := parseVersion(t.Version())
		if ok && slices.Compare(tv[:], v[:]) <= 0 {
			table = t
		}
	}
	if table == nil {
		return "", "", fmt.Errorf("scoring version %s is older than every parameter table pkg/params embeds", version)
	}
	return version, table.Hash(), nil
}
