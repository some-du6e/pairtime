// SPDX-License-Identifier: AGPL-3.0-or-later

package release

import (
	"cmp"
	"fmt"
	"regexp"
	"strings"
)

// semverRE is the regular expression semver.org's FAQ suggests for
// SemVer 2.0.0, copied verbatim. It matches a version without the "v".
var semverRE = regexp.MustCompile(`^(?P<major>0|[1-9]\d*)\.(?P<minor>0|[1-9]\d*)\.(?P<patch>0|[1-9]\d*)(?:-(?P<prerelease>(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?(?:\+(?P<buildmetadata>[0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$`)

// Version is a pairtime release version: "v" and a SemVer 2.0.0 version
// without build metadata, such as v0.1.0-alpha.1. Release tags, the
// binaries' -X version stamp and the manifest all use this form.
type Version struct {
	major, minor, patch string   // decimal, no leading zeros, any length
	pre                 []string // prerelease identifiers; nil for a release
}

// ParseVersion parses a pairtime version.
func ParseVersion(s string) (Version, error) {
	rest, ok := strings.CutPrefix(s, "v")
	if !ok {
		return Version{}, fmt.Errorf("version %q doesn't start with v", s)
	}
	m := semverRE.FindStringSubmatch(rest)
	if m == nil {
		return Version{}, fmt.Errorf("version %q isn't v and a SemVer 2.0.0 version, like v0.1.0 or v0.1.0-alpha.1", s)
	}
	if m[semverRE.SubexpIndex("buildmetadata")] != "" {
		return Version{}, fmt.Errorf("version %q has build metadata, which pairtime versions don't use", s)
	}
	v := Version{
		major: m[semverRE.SubexpIndex("major")],
		minor: m[semverRE.SubexpIndex("minor")],
		patch: m[semverRE.SubexpIndex("patch")],
	}
	if pre := m[semverRE.SubexpIndex("prerelease")]; pre != "" {
		v.pre = strings.Split(pre, ".")
	}
	return v, nil
}

// String returns the version as ParseVersion reads it.
func (v Version) String() string {
	s := "v" + v.major + "." + v.minor + "." + v.patch
	if len(v.pre) > 0 {
		s += "-" + strings.Join(v.pre, ".")
	}
	return s
}

// Prerelease reports whether v has a prerelease part, as in v0.1.0-alpha.1.
// Such tags publish as GitHub prereleases.
func (v Version) Prerelease() bool { return len(v.pre) > 0 }

// Compare returns -1, 0 or +1 as v has lower, equal or higher precedence
// than w, by SemVer 2.0.0 item 11.
func (v Version) Compare(w Version) int {
	for _, p := range [][2]string{{v.major, w.major}, {v.minor, w.minor}, {v.patch, w.patch}} {
		if c := compareNumeric(p[0], p[1]); c != 0 {
			return c
		}
	}
	switch {
	case len(v.pre) == 0 && len(w.pre) == 0:
		return 0
	case len(v.pre) == 0:
		return 1 // 1.0.0-alpha < 1.0.0
	case len(w.pre) == 0:
		return -1
	}
	for i := 0; i < len(v.pre) && i < len(w.pre); i++ {
		if c := compareIdentifier(v.pre[i], w.pre[i]); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(v.pre), len(w.pre))
}

// compareNumeric compares decimal strings without leading zeros. Comparing
// lengths first means a number of any size compares without overflow.
func compareNumeric(a, b string) int {
	if c := cmp.Compare(len(a), len(b)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

// compareIdentifier compares two prerelease identifiers: numeric ones as
// numbers, the rest in ASCII order, and numeric before non-numeric.
func compareIdentifier(a, b string) int {
	an, bn := isDigits(a), isDigits(b)
	switch {
	case an && bn:
		return compareNumeric(a, b)
	case an:
		return -1
	case bn:
		return 1
	}
	return strings.Compare(a, b)
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}
