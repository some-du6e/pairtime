// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// TestExportAndDeleteAccount checks what the eraser and the privacy hub's
// export call (DESIGN 8.9; WP-SV13; D-sb1-submissions, Q1): Export reads
// every submission of the account with its moves, r only once its draw is
// stored, and no flag, and DeleteAccount deletes every row of the
// account's submissions and nothing of another account's.
func TestExportAndDeleteAccount(t *testing.T) {
	start, end := t0.Add(-2*day), t0.Add(-day)
	// two makes two submissions of u1, one withdrawn and one with a
	// concurrent_devices flag, and one of u2.
	two := func(h *harness) (flagged, withdrawn, theirs TeenView) {
		h.minute(u1, "d1", start.Add(time.Hour), "p-app")
		h.minute(u1, "d2", start.Add(time.Hour), "p-tool")
		flagged = h.create("ptd_laptop", window("hard-mode", "req-1", start, end), http.StatusCreated)
		h.clock.set(t0.Add(time.Second))
		withdrawn = h.create("ptd_laptop", window("stardance", "req-2", end, t0), http.StatusCreated)
		want(h.t, h.call("ptd_laptop", "POST", SubmissionsPath+"/"+withdrawn.ID+"/withdraw", ""), http.StatusOK, "")
		theirs = h.create("ptd_other", window("hard-mode", "req-1", start, end), http.StatusCreated)
		return flagged, withdrawn, theirs
	}
	tests := []struct {
		name string
		run  func(t *testing.T, h *harness)
	}{
		{
			name: "the export holds each of the account's submissions in the teen view with its moves, and no flag, review URL or other account's submission",
			run: func(t *testing.T, h *harness) {
				flagged, withdrawn, theirs := two(h)
				got, err := Export(context.Background(), h.db.Read, u1)
				if err != nil {
					t.Fatal(err)
				}
				if len(got.Submissions) != 2 || got.Submissions[0].ID != flagged.ID || got.Submissions[1].ID != withdrawn.ID {
					t.Fatalf("the export holds %+v, want %s and %s", got.Submissions, flagged.ID, withdrawn.ID)
				}
				if e := got.Submissions[1].Events; len(e) != 2 || e[0].To != Frozen || e[1].To != Withdrawn {
					t.Errorf("the withdrawn submission's moves %+v, want the freeze and the withdrawal", e)
				}
				b, err := json.Marshal(got)
				if err != nil {
					t.Fatal(err)
				}
				for _, s := range []string{FlagConcurrentDevices, "review_url", theirs.ID} {
					if strings.Contains(string(b), s) {
						t.Errorf("the export holds %q: %s", s, b)
					}
				}
				if r := h.storedR(flagged.ID); leaks(string(b), r) {
					t.Errorf("the export holds r before the draw: %s", b)
				}
			},
		},
		{
			name: "the export holds r once the draw is stored",
			run: func(t *testing.T, h *harness) {
				flagged, _, _ := two(h)
				h.move(flagged.ID, MetadataRevealed, Challenged)
				got, err := Export(context.Background(), h.db.Read, u1)
				if err != nil {
					t.Fatal(err)
				}
				if r := h.storedR(flagged.ID); got.Submissions[0].R != hex.EncodeToString(r) {
					t.Errorf("the export's r %q, want %x", got.Submissions[0].R, r)
				}
			},
		},
		{
			name: "an account without submissions exports an empty list",
			run: func(t *testing.T, h *harness) {
				got, err := Export(context.Background(), h.db.Read, u3)
				if err != nil {
					t.Fatal(err)
				}
				if b, _ := json.Marshal(got); string(b) != `{"submissions":[]}` {
					t.Errorf("the export is %s, want no submissions", b)
				}
			},
		},
		{
			name: "DeleteAccount deletes every row of the account's submissions, flags included, and none of another account's",
			run: func(t *testing.T, h *harness) {
				_, _, theirs := two(h)
				if err := h.w.Do(context.Background(), 1, func(ctx context.Context, tx writer.Tx) error {
					return DeleteAccount(ctx, tx, u1)
				}); err != nil {
					t.Fatal(err)
				}
				if got, want := h.rows(), "submissions 1, submission_projects 1, submission_keys 1, submission_windows 1, submission_events 1, submission_flags 0"; got != want {
					t.Errorf("rows %s, want %s: u2's submission alone", got, want)
				}
				if _, err := Get(context.Background(), h.db.Read, theirs.ID); err != nil {
					t.Errorf("u2's submission: %v", err)
				}
				if got, err := Export(context.Background(), h.db.Read, u1); err != nil || len(got.Submissions) != 0 {
					t.Errorf("u1's export after the deletion: %+v, %v", got, err)
				}
			},
		},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) { tt.run(t, newHarness(t, engine)) })
		}
	}
}
