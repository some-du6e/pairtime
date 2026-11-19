// SPDX-License-Identifier: AGPL-3.0-or-later

package release

import (
	"strings"
	"testing"
)

func TestParseVersion(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		prerelease bool
		wantErr    string // "" means it parses and String gives it back
	}{
		{name: "a release parses", in: "v1.2.3"},
		{name: "zero is a valid number", in: "v0.0.0"},
		{name: "an alpha is a prerelease", in: "v0.1.0-alpha.1", prerelease: true},
		{name: "a beta is a prerelease", in: "v0.1.0-beta.2", prerelease: true},
		{name: "any SemVer prerelease parses", in: "v1.0.0-rc.1.x-y-z.0a", prerelease: true},
		{name: "the snapshot form parses", in: "v0.0.0-snapshot.42", prerelease: true},
		{name: "numbers past 64 bits parse", in: "v99999999999999999999.0.0"},
		{name: "the v is required", in: "1.2.3", wantErr: "doesn't start with v"},
		{name: "a capital V is refused", in: "V1.2.3", wantErr: "doesn't start with v"},
		{name: "two parts are refused", in: "v1.2", wantErr: "SemVer"},
		{name: "four parts are refused", in: "v1.2.3.4", wantErr: "SemVer"},
		{name: "a leading zero is refused", in: "v01.2.3", wantErr: "SemVer"},
		{name: "a leading zero in a numeric prerelease part is refused", in: "v1.2.3-alpha.01", wantErr: "SemVer"},
		{name: "an empty prerelease is refused", in: "v1.2.3-", wantErr: "SemVer"},
		{name: "an empty prerelease part is refused", in: "v1.2.3-alpha..1", wantErr: "SemVer"},
		{name: "an underscore is refused", in: "v1.2.3-alpha_1", wantErr: "SemVer"},
		{name: "build metadata is refused", in: "v1.2.3+build.5", wantErr: "build metadata"},
		{name: "build metadata after a prerelease is refused", in: "v1.2.3-alpha.1+exp.sha.5114f85", wantErr: "build metadata"},
		{name: "a trailing newline is refused", in: "v1.2.3\n", wantErr: "SemVer"},
		{name: "surrounding space is refused", in: " v1.2.3", wantErr: "doesn't start with v"},
		{name: "an empty string is refused", in: "", wantErr: "doesn't start with v"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := ParseVersion(tt.in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := v.String(); got != tt.in {
				t.Errorf("String() = %q, want %q", got, tt.in)
			}
			if got := v.Prerelease(); got != tt.prerelease {
				t.Errorf("Prerelease() = %v, want %v", got, tt.prerelease)
			}
		})
	}
}

// TestVersionPrecedence checks the two orderings semver.org gives as
// examples in item 11, plus the snapshot and alpha versions pairtime uses.
func TestVersionPrecedence(t *testing.T) {
	chains := []struct {
		name     string
		versions []string // ascending
	}{
		{name: "major, minor and patch compare numerically (semver.org 11.2)", versions: []string{"v1.0.0", "v2.0.0", "v2.1.0", "v2.1.1"}},
		{name: "prerelease identifiers compare left to right (semver.org 11.4)", versions: []string{
			"v1.0.0-alpha", "v1.0.0-alpha.1", "v1.0.0-alpha.beta", "v1.0.0-beta",
			"v1.0.0-beta.2", "v1.0.0-beta.11", "v1.0.0-rc.1", "v1.0.0",
		}},
		{name: "numbers compare by value, not by text", versions: []string{"v0.9.0", "v0.10.0", "v0.10.10", "v10.0.0", "v99999999999999999999.0.0", "v100000000000000000000.0.0"}},
		{name: "snapshots sort below the first alpha, alphas below betas", versions: []string{
			"v0.0.0-snapshot.2", "v0.0.0-snapshot.10", "v0.0.0", "v0.1.0-alpha.1", "v0.1.0-alpha.2", "v0.1.0-beta.1", "v0.1.0",
		}},
	}
	for _, c := range chains {
		t.Run(c.name, func(t *testing.T) {
			vs := make([]Version, len(c.versions))
			for i, s := range c.versions {
				v, err := ParseVersion(s)
				if err != nil {
					t.Fatal(err)
				}
				vs[i] = v
			}
			for i := range vs {
				for j := range vs {
					want := 0
					if i < j {
						want = -1
					} else if i > j {
						want = 1
					}
					if got := vs[i].Compare(vs[j]); got != want {
						t.Errorf("Compare(%s, %s) = %d, want %d", c.versions[i], c.versions[j], got, want)
					}
				}
			}
		})
	}
}
