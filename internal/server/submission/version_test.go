// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/pages"
	"github.com/some-du6e/pairtime/pkg/params"
)

// TestScoring checks the scoring version and parameter hash a freeze
// records, and the start-up check on scoring_version (D-sb1-submissions,
// Q10): the configured version, or the built-in one when it is empty,
// with the newest embedded table at or below it, and a refusal of a
// version newer than the built-in's or, while the built-in is 0.x, of
// another MINOR.
func TestScoring(t *testing.T) {
	// tableFor is the newest embedded table whose version is at or below
	// version, worked out apart from Scoring.
	tableFor := func(version string) string {
		v, _ := parseVersion(version)
		var hash string
		for _, tb := range params.Tables() {
			tv, _ := parseVersion(tb.Version())
			if slices.Compare(tv[:], v[:]) <= 0 {
				hash = tb.Hash()
			}
		}
		return hash
	}
	tables := params.Tables()
	newest := tables[len(tables)-1]
	tests := []struct {
		name                string
		configured, builtin string
		wantVersion         string
		wantErr             string
	}{
		{name: "an empty scoring_version freezes the built-in version", configured: "", builtin: "0.5.0", wantVersion: "0.5.0"},
		{name: "the built-in version itself is taken", configured: "0.5.0", builtin: "0.5.0", wantVersion: "0.5.0"},
		{name: "before 1.0.0, an older PATCH of the built-in's MINOR is taken", configured: "0.5.1", builtin: "0.5.3", wantVersion: "0.5.1"},
		{name: "a PATCH newer than the built-in's is refused", configured: "0.5.1", builtin: "0.5.0", wantErr: "newer than this release's built-in scorer"},
		{name: "a MINOR newer than the built-in's is refused", configured: "0.6.0", builtin: "0.5.0", wantErr: "newer than this release's built-in scorer"},
		{name: "a MAJOR newer than the built-in's is refused", configured: "1.0.0", builtin: "0.5.0", wantErr: "newer than this release's built-in scorer"},
		{name: "before 1.0.0, an older MINOR is refused, since it can score by another rule", configured: "0.4.0", builtin: "0.5.0", wantErr: "another MINOR"},
		{name: "from 1.0.0 on, an older MINOR is taken", configured: "1.1.0", builtin: "1.2.0", wantVersion: "1.1.0"},
		{name: "a version that isn't X.Y.Z is refused", configured: "0.5", builtin: "0.5.0", wantErr: "isn't of the form X.Y.Z"},
		{name: "a version with a leading zero is refused", configured: "0.05.0", builtin: "0.5.0", wantErr: "isn't of the form X.Y.Z"},
		{name: "a version older than every embedded table is refused", configured: "", builtin: "0.0.1", wantErr: "older than every parameter table"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version, hash, err := Scoring(tt.configured, tt.builtin)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Scoring = %s, %s, %v; want an error with %q", version, hash, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if version != tt.wantVersion || hash != tableFor(tt.wantVersion) || hash == "" {
				t.Errorf("Scoring = %s, %s; want %s and %s", version, hash, tt.wantVersion, tableFor(tt.wantVersion))
			}
		})
	}
	t.Run("the built-in version scores with the newest table, the one the agent stamps its buckets with", func(t *testing.T) {
		if _, hash, err := Scoring("", pages.BuiltinScoringVersion); err != nil || hash != newest.Hash() {
			t.Errorf("Scoring(\"\", %s) = %s, %v; want the newest table's %s (%s)", pages.BuiltinScoringVersion, hash, err, newest.Hash(), newest.Version())
		}
	})
}

// TestHorizon checks that the submission horizon is server.minute_buckets'
// 180 days in docs/retention.json, so the two can't drift
// (D-sb1-submissions, Q12), and that a service takes it from there.
func TestHorizon(t *testing.T) {
	h, err := Horizon()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		got  time.Duration
	}{
		{name: "the horizon is 180 days", got: h},
		{name: "a service without a horizon of its own uses docs/retention.json's", got: newHarness(t, "sqlite").svc.horizon},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != 180*day {
				t.Errorf("horizon = %v, want %v", tt.got, 180*day)
			}
		})
	}
}
