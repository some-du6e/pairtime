// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import (
	"os"
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
		{"a hostname is not", "teens-mbp.local", false},
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

// TestIsTier1Version checks tier 1's narrower form (ingest-v1 2.3.4): every
// version pairtime and the harnesses report today passes, so no released
// client's labels are refused, and a name in the suffix doesn't.
func TestIsTier1Version(t *testing.T) {
	type versionCase struct {
		name string
		v    string
		want bool
	}
	cases := []versionCase{
		{"pairtime's first release passes", "v0.1.0-alpha.1", true},
		{"pairtime's fifth release passes", "v0.1.0-alpha.5", true},
		{"a dev build's v0.0.0-dev passes", "v0.0.0-dev", true},
		{"a Go pseudo-version with no tag before it passes", "v0.0.0-20261123010542-8f0e2c5fba57", true},
		{"a Go pseudo-version after a release passes", "v0.1.1-0.20261123010542-8f0e2c5fba57", true},
		{"a Go pseudo-version after a pre-release passes", "v0.1.0-alpha.5.0.20261123010542-8f0e2c5fba57", true},
		{"a Claude Code version passes", "2.1.327", true},
		{"a later Claude Code version passes", "2.1.399", true},
		{"a build suffix of a short commit hash passes", "0.1.0+3dbf6d4", true},
		{"a nightly with a date and a hash passes", "0.9.0-nightly.20261115.abc1234", true},
		{"a version with no suffix passes", "7", true},
		{"a hostname after the number is refused", "1.0.0-teens-mbp.local", false},
		{"a branch name after the number is refused", "1.4.0-fix-login", false},
		{"a file name in a build suffix is refused", "0.1.0+main.go", false},
		{"a word joined to a number is refused, so the client sends the number alone", "1.0.0-rc1", false},
		{"git describe's g before the hash is refused", "1.0.0-3-g1a2b3c4", false},
		{"a word outside the list is refused", "v0.0.0-ci", false},
		{"a listed word in capitals is refused", "1.0.0-ALPHA", false},
		{"hex digits fewer than 7 are refused", "1.0.0-beef", false},
		{"uppercase hex is refused", "1.0.0+3DBF6D4", false},
		{"an empty identifier is refused", "1.0.0-alpha..1", false},
		{"an empty last identifier is refused", "1.0.0-alpha-", false},
		{"what IsVersion refuses is refused", "dev", false},
	}
	for _, w := range []string{"alpha", "beta", "rc", "pre", "dev", "preview", "nightly", "canary", "next", "build"} {
		cases = append(cases, versionCase{"the word " + w + " passes in a suffix", "1.0.0-" + w + ".1", true})
	}
	// Every harness version testdata/fixtures captured is one the harness
	// reports, so each must pass.
	dirs, err := os.ReadDir("../../testdata/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	fixtures := 0
	for _, h := range dirs {
		if !h.IsDir() {
			continue
		}
		versions, err := os.ReadDir("../../testdata/fixtures/" + h.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range versions {
			if v.IsDir() {
				fixtures++
				cases = append(cases, versionCase{"the captured " + h.Name() + " version " + v.Name() + " passes", v.Name(), true})
			}
		}
	}
	if fixtures == 0 {
		t.Fatal("testdata/fixtures holds no harness versions, so the fixture cases check nothing")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsTier1Version(tc.v); got != tc.want {
				t.Errorf("IsTier1Version(%q) = %v, want %v", tc.v, got, tc.want)
			}
			if IsTier1Version(tc.v) && !IsVersion(tc.v) {
				t.Errorf("IsTier1Version takes %q, which IsVersion doesn't", tc.v)
			}
		})
	}
}
