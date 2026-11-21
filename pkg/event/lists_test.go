// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import (
	"slices"
	"testing"
)

// The exported lists are what other packages' tests cover their own words
// with (internal/coach/text), so a value added here reaches them.
func TestLists(t *testing.T) {
	cases := []struct {
		name string
		got  []string
		want []string
	}{
		{"Harnesses lists every harness in the schema's order, none last", names(Harnesses()), names(harnesses.values)},
		{"Activities lists SM 5.1's eight kinds in order", names(Activities()), []string{
			"prompting", "reading_output", "supervising", "reviewing", "testing", "debugging", "hand_edit", "lookup"}},
		{"CreditReasons lists SM 5.2's credit reasons in order", names(CreditReasons()), []string{
			"P", "ANS", "GO", "Q", "PA", "INT", "E", "NAV", "CMD", "TEST", "G", "SUP", "READ", "LOOKUP"}},
		{"UncreditedReasons lists SM 5.2's uncredited reasons in order", names(UncreditedReasons()), names(uncreditedReasons.values)},
		{"Evidences lists the levels A, B and C", names(Evidences()), []string{"A", "B", "C"}},
		{"SlotLabels lists the side-door labels in a slot's order", names(SlotLabels()), []string{"via_track", "via_plugin", "via_bridge"}},
		{"BucketStates lists every bucket state", names(BucketStates()), names(bucketStates.values)},
		{"Witnesses lists every witness label", names(Witnesses()), names(witnesses.values)},
		{"Actors lists every actor in the schema's order", names(Actors()), names(actors.values)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !slices.Equal(tc.got, tc.want) {
				t.Errorf("got %q\nwant %q", tc.got, tc.want)
			}
		})
	}
	t.Run("a caller that changes a list changes nothing here", func(t *testing.T) {
		h, a, c, u := Harnesses(), Activities(), CreditReasons(), UncreditedReasons()
		e, l, s, w, ac := Evidences(), SlotLabels(), BucketStates(), Witnesses(), Actors()
		h[0], a[0], c[0], u[0] = "x", "x", "x", "x"
		e[0], l[0], s[0], w[0], ac[0] = "x", "x", "x", "x", "x"
		if Harnesses()[0] == "x" || Activities()[0] == "x" || CreditReasons()[0] == "x" || UncreditedReasons()[0] == "x" ||
			Evidences()[0] == "x" || SlotLabels()[0] == "x" || BucketStates()[0] == "x" || Witnesses()[0] == "x" || Actors()[0] == "x" {
			t.Error("a list shares its backing array with the package")
		}
	})
	t.Run("every credit reason has an activity and every uncredited one is uncredited", func(t *testing.T) {
		for _, r := range CreditReasons() {
			if r.Activity() == "" {
				t.Errorf("%s has no activity", r)
			}
		}
		for _, r := range UncreditedReasons() {
			if !r.Uncredited() {
				t.Errorf("%s isn't uncredited", r)
			}
		}
	})
}

func TestDarkFlow(t *testing.T) {
	cases := []struct {
		name string
		r    Reason
		want bool
	}{
		{"X.reroll is a loop (SM 4.2)", ReasonReroll, true},
		{"X.rewrite_loop is a loop", ReasonRewriteLoop, true},
		{"X.same_error is a loop", ReasonSameError, true},
		{"X.autopilot is a loop", ReasonAutopilot, true},
		{"X.noprog weighs seconds and isn't a loop (SM 4.3)", ReasonNoProgress, false},
		{"X.absent isn't a loop", ReasonAbsent, false},
		{"X.agent_only isn't a loop", ReasonAgentOnly, false},
		{"a credit reason isn't a loop", ReasonRead, false},
		{"a code pkg/event doesn't know isn't a loop", Reason("X.unknown"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.r.DarkFlow(); got != tc.want {
				t.Errorf("DarkFlow(%s) = %v, want %v", tc.r, got, tc.want)
			}
		})
	}
}

func names[T ~string](list []T) []string {
	out := make([]string, len(list))
	for i, v := range list {
		out[i] = string(v)
	}
	return out
}
