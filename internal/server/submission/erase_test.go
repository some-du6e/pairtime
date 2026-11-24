// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// TestDeleteAccount checks what the eraser calls (DESIGN 8.9; WP-SV13;
// D-sb1-submissions, Q1): DeleteAccount deletes every row of the
// account's submissions, flags and withdrawn ones included, and nothing
// of another account's.
func TestDeleteAccount(t *testing.T) {
	start, end := t0.Add(-2*day), t0.Add(-day)
	tests := []struct {
		name    string
		account string
		// want are the rows left, as harness.rows counts them.
		want string
	}{
		{
			name:    "deleting u1 leaves u2's submission alone",
			account: u1,
			want:    "submissions 1, submission_projects 1, submission_keys 1, submission_windows 1, submission_events 1, submission_flags 0",
		},
		{
			name:    "deleting u2 leaves both of u1's, the flag included",
			account: u2,
			want:    "submissions 2, submission_projects 2, submission_keys 4, submission_windows 2, submission_events 3, submission_flags 1",
		},
		{
			name:    "deleting an account without submissions deletes nothing",
			account: u3,
			want:    "submissions 3, submission_projects 3, submission_keys 5, submission_windows 3, submission_events 4, submission_flags 1",
		},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				h := newHarness(t, engine)
				h.minute(u1, "d1", start.Add(time.Hour), "p-app")
				h.minute(u1, "d2", start.Add(time.Hour), "p-tool")
				h.create("ptd_laptop", window("hard-mode", "req-1", start, end), http.StatusCreated)
				withdrawn := h.create("ptd_laptop", window("stardance", "req-2", end, t0), http.StatusCreated)
				want(t, h.call("ptd_laptop", "POST", SubmissionsPath+"/"+withdrawn.ID+"/withdraw", ""), http.StatusOK, "")
				theirs := h.create("ptd_other", window("hard-mode", "req-1", start, end), http.StatusCreated)
				if err := h.w.Do(context.Background(), 1, func(ctx context.Context, tx writer.Tx) error {
					return DeleteAccount(ctx, tx, tt.account)
				}); err != nil {
					t.Fatal(err)
				}
				if got := h.rows(); got != tt.want {
					t.Errorf("rows %s, want %s", got, tt.want)
				}
				if _, err := Get(context.Background(), h.db.Read, theirs.ID); (err == nil) != (tt.account != u2) {
					t.Errorf("u2's submission after deleting %s: %v", tt.account, err)
				}
			})
		}
	}
}

// TestDeleteAccountOverlaps checks that deleting an account takes its
// submissions' ids out of the other accounts' repo_overlap flags, deletes
// a flag that then names none and keeps a flag's other names, and that a
// flag is stored under the account of the submission it is on, which the
// eraser deletes and counts flags by (D-sb1-flags-erasure).
func TestDeleteAccountOverlaps(t *testing.T) {
	start, end := t0.Add(-2*day), t0.Add(-day)
	for _, engine := range storetest.Engines() {
		t.Run(string(engine)+"/deleting u2 takes its submission out of u1's flags, deletes the one that named it alone, and leaves no flag naming it", func(t *testing.T) {
			h := newHarness(t, engine)
			alone := h.create("ptd_laptop", window("hard-mode", "req-1", start, end), http.StatusCreated)
			shared := h.create("ptd_laptop", window("stardance", "req-2", end, t0), http.StatusCreated)
			theirs := h.create("ptd_other", window("hard-mode", "req-1", start, end), http.StatusCreated)
			if err := h.w.Do(context.Background(), 3, func(ctx context.Context, tx writer.Tx) error {
				for _, f := range []struct {
					on    string
					names []string
				}{
					{alone.ID, []string{theirs.ID}},
					{shared.ID, []string{"s_third", theirs.ID}},
					{theirs.ID, []string{alone.ID}},
				} {
					if err := setFlag(ctx, tx, f.on, FlagRepoOverlap, overlapDetail{Submissions: f.names}, t0.UnixMilli()); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			for _, c := range []struct{ id, owner string }{{alone.ID, u1}, {theirs.ID, u2}} {
				var owner string
				if err := h.db.Read.QueryRowContext(context.Background(), `SELECT user_id FROM submission_flags WHERE submission_id = $1`, c.id).Scan(&owner); err != nil || owner != c.owner {
					t.Errorf("%s's flag is under %q (%v), want %s, the account the submission is of", c.id, owner, err, c.owner)
				}
			}
			if err := h.w.Do(context.Background(), 4, func(ctx context.Context, tx writer.Tx) error {
				return DeleteAccount(ctx, tx, u2)
			}); err != nil {
				t.Fatal(err)
			}
			if got := h.flagsOf(alone.ID); len(got) != 0 {
				t.Errorf("the flag that named u2's submission alone is %q, want it deleted", got)
			}
			if got, want := h.flagsOf(shared.ID), []string{`repo_overlap: {"submissions":["s_third"]}`}; !slices.Equal(got, want) {
				t.Errorf("the flag that named u2's submission and another is %q, want %q", got, want)
			}
			if n, err := FlagsNaming(context.Background(), h.db.Read, []string{theirs.ID}); err != nil || n != 0 {
				t.Errorf("%d flags (%v) still name u2's submission, want none", n, err)
			}
		})
	}
}
