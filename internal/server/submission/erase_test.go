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

// TestDeleteAccountOverlaps checks what deleting an account does to other
// accounts' flags (D-sb1-flags-erasure). Each case starts with u1's
// submission alone flagged as overlapping u2's, u1's submission shared
// flagged as overlapping u2's and a third account's, and u2's flagged as
// overlapping alone, and then deletes u2.
func TestDeleteAccountOverlaps(t *testing.T) {
	start, end := t0.Add(-2*day), t0.Add(-day)
	type subs struct{ alone, shared, theirs TeenView }
	tests := []struct {
		name string
		// before checks the flags before the delete, and after after it.
		before, after func(t *testing.T, h *harness, s subs)
	}{
		{
			name: "a flag is stored under the account of the submission it is on, which the eraser deletes and counts flags by",
			before: func(t *testing.T, h *harness, s subs) {
				for _, c := range []struct{ id, owner string }{{s.alone.ID, u1}, {s.theirs.ID, u2}} {
					var owner string
					if err := h.db.Read.QueryRowContext(context.Background(), `SELECT user_id FROM submission_flags WHERE submission_id = $1`, c.id).Scan(&owner); err != nil || owner != c.owner {
						t.Errorf("%s's flag is under %q (%v), want %s", c.id, owner, err, c.owner)
					}
				}
			},
		},
		{
			name: "FlagsNaming counts the flag on a submission and the flags that name it",
			before: func(t *testing.T, h *harness, s subs) {
				if n, err := FlagsNaming(context.Background(), h.db.Read, []string{s.theirs.ID}); err != nil || n != 3 {
					t.Errorf("FlagsNaming = %d (%v), want 3: the flag on u2's submission and the two that name it", n, err)
				}
			},
		},
		{
			name: "a flag that named the deleted account's submission alone is deleted",
			after: func(t *testing.T, h *harness, s subs) {
				if got := h.flagsOf(s.alone.ID); len(got) != 0 {
					t.Errorf("the flag is %q, want it deleted", got)
				}
			},
		},
		{
			name: "a flag that named it and another submission keeps the other",
			after: func(t *testing.T, h *harness, s subs) {
				if got, wantFlags := h.flagsOf(s.shared.ID), []string{`repo_overlap: {"submissions":["s_third"]}`}; !slices.Equal(got, wantFlags) {
					t.Errorf("the flag is %q, want %q", got, wantFlags)
				}
			},
		},
		{
			name: "no flag is left on or naming the deleted account's submission",
			after: func(t *testing.T, h *harness, s subs) {
				if n, err := FlagsNaming(context.Background(), h.db.Read, []string{s.theirs.ID}); err != nil || n != 0 {
					t.Errorf("%d flags (%v) are on or name u2's submission, want none", n, err)
				}
			},
		},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				h := newHarness(t, engine)
				s := subs{
					alone:  h.create("ptd_laptop", window("hard-mode", "req-1", start, end), http.StatusCreated),
					shared: h.create("ptd_laptop", window("stardance", "req-2", end, t0), http.StatusCreated),
					theirs: h.create("ptd_other", window("hard-mode", "req-1", start, end), http.StatusCreated),
				}
				if err := h.w.Do(context.Background(), 3, func(ctx context.Context, tx writer.Tx) error {
					for _, f := range []struct {
						on    string
						names []string
					}{
						{s.alone.ID, []string{s.theirs.ID}},
						{s.shared.ID, []string{"s_third", s.theirs.ID}},
						{s.theirs.ID, []string{s.alone.ID}},
					} {
						if err := setFlag(ctx, tx, f.on, FlagRepoOverlap, overlapDetail{Submissions: f.names}, t0.UnixMilli()); err != nil {
							return err
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if tt.before != nil {
					tt.before(t, h, s)
				}
				if err := h.w.Do(context.Background(), 4, func(ctx context.Context, tx writer.Tx) error {
					return DeleteAccount(ctx, tx, u2)
				}); err != nil {
					t.Fatal(err)
				}
				if tt.after != nil {
					tt.after(t, h, s)
				}
			})
		}
	}
}
