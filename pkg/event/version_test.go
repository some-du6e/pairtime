// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import (
	"regexp"
	"strings"
	"testing"
)

// TestIsVersion checks D-version-grammar's form, and that IsVersion, which
// scans by hand, accepts exactly what VersionPattern and MaxVersionLen do.
func TestIsVersion(t *testing.T) {
	form := regexp.MustCompile(VersionPattern)
	cases := []struct {
		name string
		v    string
		want bool
	}{
		{"three numbers are a version", "2.1.327", true},
		{"a leading v is allowed", "v0.1.0", true},
		{"one number alone is a version", "7", true},
		{"four numbers are a version", "1.2.3.4", true},
		{"five numbers are not", "1.2.3.4.5", false},
		{"a pre-release suffix is allowed", "v0.1.0-alpha.1", true},
		{"a build suffix is allowed", "0.1.0+3dbf6d4", true},
		{"a dev build is v0.0.0-dev", "v0.0.0-dev", true},
		{"a Go pseudo-version is allowed", "v0.0.0-20261119104900-1a2b3c4d5e6f", true},
		{"a bare word such as dev is not", "dev", false},
		{"a file name is not", "main.go", false},
		{"a hostname is not", "karims-mbp.local", false},
		{"a folder name is not", "my-secret-startup", false},
		{"a capital V is not the prefix", "V1.0", false},
		{"v alone is not", "v", false},
		{"a trailing dot is not", "1.", false},
		{"two dots in a row are not", "1..2", false},
		{"a suffix needs a character after its sign", "1.0-", false},
		{"a suffix starts with a letter or digit", "1.0-.x", false},
		{"a suffix has no second plus", "1.0.0-beta+exp", false},
		{"a space is not allowed", "2.1.327 (Claude Code)", false},
		{"a slash is not allowed", "1.0/x", false},
		{"an empty string is not", "", false},
		{"a suffix of 41 characters is allowed", "1-" + strings.Repeat("a", 41), true},
		{"a suffix of 42 characters is not", "1-" + strings.Repeat("a", 42), false},
		{"64 characters are allowed", strings.Repeat("1", 64), true},
		{"65 characters are not", strings.Repeat("1", 65), false},
		{"non-ASCII digits are not", "1.٢", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsVersion(tc.v); got != tc.want {
				t.Errorf("IsVersion(%q) = %v, want %v", tc.v, got, tc.want)
			}
			if got := len(tc.v) <= MaxVersionLen && form.MatchString(tc.v); got != tc.want {
				t.Errorf("VersionPattern matches %q: %v, want %v", tc.v, got, tc.want)
			}
		})
	}
}
