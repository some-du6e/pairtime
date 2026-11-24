// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"fmt"
	"testing"
)

// TestTransitions is WP-SB1's first check: every legal and illegal move
// between the ten states, against the list of legal moves written out
// here apart from the package's table.
func TestTransitions(t *testing.T) {
	legal := map[[2]State]string{
		{Created, Frozen}:              "the freeze",
		{Frozen, MetadataRevealed}:     "the stored metadata reveal",
		{MetadataRevealed, Challenged}: "the draw",
		{Challenged, ContentRevealed}:  "the content reveal",
		{ContentRevealed, Checked}:     "step 9's checks",
		{Checked, InReview}:            "a reviewer's assignment",
		{InReview, Decided}:            "the outcome",
		{Decided, Decided}:             "a re-decision",
		{Decided, Revoked}:             "a revocation",
		{Created, Withdrawn}:           "a withdrawal before the freeze",
		{Frozen, Withdrawn}:            "a withdrawal after the freeze",
		{MetadataRevealed, Withdrawn}:  "a withdrawal after the metadata reveal",
		{Challenged, Withdrawn}:        "a withdrawal after the draw",
		{ContentRevealed, Withdrawn}:   "a withdrawal after the content reveal",
		{Checked, Withdrawn}:           "a withdrawal after the checks",
		{InReview, Withdrawn}:          "a withdrawal in review",
	}
	if len(States) != 10 {
		t.Fatalf("%d states, want the ten of DESIGN 7.5", len(States))
	}
	for _, from := range States {
		for _, to := range States {
			why, want := legal[[2]State{from, to}]
			name := fmt.Sprintf("%s to %s is illegal", from, to)
			if want {
				name = fmt.Sprintf("%s to %s is legal: %s", from, to, why)
			}
			t.Run(name, func(t *testing.T) {
				if got := Legal(from, to); got != want {
					t.Errorf("Legal(%s, %s) = %v, want %v", from, to, got, want)
				}
			})
		}
	}
}
