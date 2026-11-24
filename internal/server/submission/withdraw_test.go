// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// chainTo lists the moves after the freeze that bring a submission to
// state, along DESIGN 7.5's chain.
func chainTo(state State) []State {
	chain := []State{MetadataRevealed, Challenged, ContentRevealed, Checked, InReview, Decided, Revoked}
	for i, s := range chain {
		if s == state {
			return chain[:i+1]
		}
	}
	return nil
}

// TestWithdraw checks POST /api/v1/submissions/{id}/withdraw
// (D-sb1-submissions, Q7): a withdrawal from each state from created to
// content_revealed succeeds and frees the time, one from checked,
// in_review, decided or revoked gets 409 not_withdrawable, and only the
// account withdraws, from a computer or from a web session with a fresh
// login.
func TestWithdraw(t *testing.T) {
	start, end := t0.Add(-2*day), t0.Add(-day)
	type withdrawCase struct {
		name string
		run  func(t *testing.T, h *harness)
	}
	var tests []withdrawCase
	tests = append(tests, withdrawCase{
		name: "a withdrawal from created succeeds",
		run: func(t *testing.T, h *harness) {
			if _, err := h.db.Write.ExecContext(context.Background(), `INSERT INTO submissions (id, user_id, program, takes_hours, external_id, request_sha256, created_by, mode, state, created_at_ms, updated_at_ms)
VALUES ('s-created', 'u1', 'hard-mode', 1, 'program-id', 'sha', 'program', 'window', 'created', 0, 0)`); err != nil {
				t.Fatal(err)
			}
			h.clock.set(t0.Add(time.Minute))
			v := view(t, h.call("ptd_laptop", "POST", SubmissionsPath+"/s-created/withdraw", ""))
			if v.State != Withdrawn || v.WithdrawnAt == nil || !v.WithdrawnAt.Equal(t0.Add(time.Minute)) {
				t.Errorf("state %s, withdrawn_at %v; want withdrawn at %s", v.State, v.WithdrawnAt, t0.Add(time.Minute))
			}
		},
	})
	for _, state := range []State{Frozen, MetadataRevealed, Challenged, ContentRevealed} {
		tests = append(tests, withdrawCase{
			name: fmt.Sprintf("a withdrawal from %s succeeds, keeps the submission with its withdrawal time, and frees its windows", state),
			run: func(t *testing.T, h *harness) {
				first := h.create("ptd_laptop", window("hard-mode", "req-1", start, end), http.StatusCreated)
				h.move(first.ID, chainTo(state)...)
				before := h.rows()
				h.clock.set(t0.Add(time.Minute))
				rec := h.call("ptd_laptop", "POST", SubmissionsPath+"/"+first.ID+"/withdraw", "")
				want(t, rec, http.StatusOK, "")
				v := view(t, rec)
				if v.State != Withdrawn || v.WithdrawnAt == nil || !v.WithdrawnAt.Equal(t0.Add(time.Minute)) || len(v.Windows) != 1 {
					t.Errorf("state %s, withdrawn_at %v, windows %d; want withdrawn at %s with its window", v.State, v.WithdrawnAt, len(v.Windows), t0.Add(time.Minute))
				}
				after := h.rows()
				if strings.Replace(before, fmt.Sprintf("submission_events %d", len(chainTo(state))+1), fmt.Sprintf("submission_events %d", len(chainTo(state))+2), 1) != after {
					t.Errorf("the withdrawal changed the rows from %s to %s; want one more event and nothing deleted", before, after)
				}
				h.create("ptd_laptop", window("stardance", "req-2", start, end), http.StatusCreated)
			},
		})
	}
	for _, state := range []State{Checked, InReview, Decided, Revoked} {
		tests = append(tests, withdrawCase{
			name: fmt.Sprintf("a withdrawal from %s gets 409 not_withdrawable, and the time stays taken", state),
			run: func(t *testing.T, h *harness) {
				first := h.create("ptd_laptop", window("hard-mode", "req-1", start, end), http.StatusCreated)
				h.move(first.ID, chainTo(state)...)
				before := h.rows()
				want(t, h.call("ptd_laptop", "POST", SubmissionsPath+"/"+first.ID+"/withdraw", ""), http.StatusConflict, CodeNotWithdrawable)
				if after := h.rows(); after != before {
					t.Errorf("the refusal changed the rows from %s to %s", before, after)
				}
				if v := view(t, h.call("ptd_laptop", "GET", SubmissionsPath+"/"+first.ID, "")); v.State != state || v.WithdrawnAt != nil {
					t.Errorf("state %s, withdrawn_at %v; want %s and no withdrawal", v.State, v.WithdrawnAt, state)
				}
				want(t, h.call("ptd_laptop", "POST", SubmissionsPath, window("stardance", "req-2", start, end)), http.StatusConflict, CodeAlreadySubmitted)
			},
		})
	}
	tests = append(tests,
		withdrawCase{
			name: "a withdrawal of a submission already withdrawn answers 200 with it unchanged",
			run: func(t *testing.T, h *harness) {
				first := h.create("ptd_laptop", window("hard-mode", "req-1", start, end), http.StatusCreated)
				h.clock.set(t0.Add(time.Minute))
				want(t, h.call("ptd_laptop", "POST", SubmissionsPath+"/"+first.ID+"/withdraw", ""), http.StatusOK, "")
				before := h.rows()
				h.clock.set(t0.Add(time.Hour))
				v := view(t, h.call("ptd_desktop", "POST", SubmissionsPath+"/"+first.ID+"/withdraw", ""))
				if v.State != Withdrawn || !v.WithdrawnAt.Equal(t0.Add(time.Minute)) || !v.UpdatedAt.Equal(t0.Add(time.Minute)) {
					t.Errorf("state %s, withdrawn_at %v, updated_at %s; want the first withdrawal's", v.State, v.WithdrawnAt, v.UpdatedAt)
				}
				if after := h.rows(); after != before {
					t.Errorf("the repeat changed the rows from %s to %s", before, after)
				}
			},
		},
		withdrawCase{
			name: "another account's submission, and an id the server doesn't hold, get 404",
			run: func(t *testing.T, h *harness) {
				mine := h.create("ptd_laptop", window("hard-mode", "req-1", start, end), http.StatusCreated)
				want(t, h.call("ptd_other", "POST", SubmissionsPath+"/"+mine.ID+"/withdraw", ""), http.StatusNotFound, CodeNotFound)
				want(t, h.call("sess:fresh2", "POST", SubmissionsPath+"/"+mine.ID+"/withdraw", ""), http.StatusNotFound, CodeNotFound)
				want(t, h.call("ptd_laptop", "POST", SubmissionsPath+"/s-none/withdraw", ""), http.StatusNotFound, CodeNotFound)
				want(t, h.call("ptd_laptop", "POST", SubmissionsPath+"/not%20an%20id/withdraw", ""), http.StatusNotFound, CodeNotFound)
				if v := view(t, h.call("ptd_laptop", "GET", SubmissionsPath+"/"+mine.ID, "")); v.State != Frozen {
					t.Errorf("state %s, want frozen", v.State)
				}
			},
		},
		withdrawCase{
			name: "a web session withdraws only with a fresh login, from this server's own pages, and a program's credential never does",
			run: func(t *testing.T, h *harness) {
				mine := h.create("ptd_laptop", window("hard-mode", "req-1", start, end), http.StatusCreated)
				path := SubmissionsPath + "/" + mine.ID + "/withdraw"
				want(t, h.call("sess:plain", "POST", path, ""), http.StatusForbidden, CodeFreshLoginRequired)
				want(t, h.call("sess:stale", "POST", path, ""), http.StatusForbidden, CodeFreshLoginRequired)
				r := h.request("sess:fresh", "POST", path, "")
				r.Header.Set("Sec-Fetch-Site", "cross-site")
				want(t, h.serve(r), http.StatusForbidden, CodeCrossOrigin)
				want(t, h.call("prog:hard-mode", "POST", path, ""), http.StatusForbidden, CodeProgramNotAllowed)
				want(t, h.call("sess:fresh", "POST", path, ""), http.StatusOK, "")
			},
		},
		withdrawCase{
			name: "a withdrawal has no body",
			run: func(t *testing.T, h *harness) {
				mine := h.create("ptd_laptop", window("hard-mode", "req-1", start, end), http.StatusCreated)
				want(t, h.call("ptd_laptop", "POST", SubmissionsPath+"/"+mine.ID+"/withdraw", "{}"), http.StatusBadRequest, "body_shape")
			},
		},
		withdrawCase{
			name: "OnWithdraw runs in the withdrawal's op with the withdrawn submission, and its error undoes the withdrawal",
			run: func(t *testing.T, h *harness) {
				var seen []Submission
				h.svc.o.OnWithdraw = func(ctx context.Context, tx writer.Tx, s Submission) error {
					seen = append(seen, s)
					if s.RequestID == "req-fail" {
						return errors.New("draw: the disk is full")
					}
					return nil
				}
				ok := h.create("ptd_laptop", window("hard-mode", "req-1", start, end), http.StatusCreated)
				want(t, h.call("ptd_laptop", "POST", SubmissionsPath+"/"+ok.ID+"/withdraw", ""), http.StatusOK, "")
				if len(seen) != 1 || seen[0].ID != ok.ID || seen[0].State != Withdrawn {
					t.Fatalf("OnWithdraw saw %+v, want %s withdrawn", seen, ok.ID)
				}
				failing := h.create("ptd_laptop", window("hard-mode", "req-fail", start, end), http.StatusCreated)
				want(t, h.call("ptd_laptop", "POST", SubmissionsPath+"/"+failing.ID+"/withdraw", ""), http.StatusServiceUnavailable, "unavailable")
				if v := view(t, h.call("ptd_laptop", "GET", SubmissionsPath+"/"+failing.ID, "")); v.State != Frozen {
					t.Errorf("state %s, want the withdrawal undone", v.State)
				}
			},
		},
	)
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) { tt.run(t, newHarness(t, engine)) })
		}
	}
}
