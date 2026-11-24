// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"context"
	"net/http"
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
