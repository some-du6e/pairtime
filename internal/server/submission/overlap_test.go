// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/internal/server/store/storetest"
)

// TestOverlapRefusal checks the overlap refusal (D-sb1-submissions, Q2):
// a create whose windows overlap those of another submission of the same
// account that holds one of the same keys, was frozen and not withdrawn,
// and went to a program that takes hours, the same one included, gets 409
// already_submitted naming each one. A withdrawal frees the windows, and a
// decision or a revocation doesn't. Calibration's submissions are never
// refused and cause no refusal.
func TestOverlapRefusal(t *testing.T) {
	a, b, c := t0.Add(-6*day), t0.Add(-4*day), t0.Add(-2*day)
	tests := []struct {
		name string
		run  func(t *testing.T, h *harness)
	}{
		{
			name: "a create to the same program over an overlapping window is refused with 409 already_submitted naming the submission, and stores nothing",
			run: func(t *testing.T, h *harness) {
				first := h.create("ptd_laptop", window("hard-mode", "req-1", a, b), http.StatusCreated)
				before := h.rows()
				rec := h.call("ptd_laptop", "POST", SubmissionsPath, window("hard-mode", "req-2", a.Add(day), c))
				want(t, rec, http.StatusConflict, CodeAlreadySubmitted)
				if got := refusalBody(t, rec).Submissions; !reflect.DeepEqual(got, []RefJSON{{ID: first.ID, Program: "hard-mode"}}) {
					t.Errorf("submissions %+v, want the first, to hard-mode", got)
				}
				if after := h.rows(); after != before {
					t.Errorf("the refusal changed the rows from %s to %s", before, after)
				}
			},
		},
		{
			name: "a create to another program over an overlapping window is refused the same way",
			run: func(t *testing.T, h *harness) {
				first := h.create("ptd_laptop", window("hard-mode", "req-1", a, b), http.StatusCreated)
				rec := h.call("ptd_laptop", "POST", SubmissionsPath, window("stardance", "req-2", a, b))
				want(t, rec, http.StatusConflict, CodeAlreadySubmitted)
				if got := refusalBody(t, rec).Submissions; len(got) != 1 || got[0].ID != first.ID {
					t.Errorf("submissions %+v, want the first", got)
				}
			},
		},
		{
			name: "the other computer's key of the project is refused too, since the freeze holds every computer's keys",
			run: func(t *testing.T, h *harness) {
				h.create("ptd_laptop", window("hard-mode", "req-1", a, b), http.StatusCreated)
				want(t, h.call("ptd_desktop", "POST", SubmissionsPath, createBody("stardance", "req-2", true, []string{"k_app_desk"}, a, b)), http.StatusConflict, CodeAlreadySubmitted)
				want(t, h.call("sess:fresh", "POST", SubmissionsPath, createBody("stardance", "req-3", false, []string{"p-app", "p-web"}, a, b)), http.StatusConflict, CodeAlreadySubmitted)
			},
		},
		{
			name: "windows that only touch at an end don't overlap",
			run: func(t *testing.T, h *harness) {
				h.create("ptd_laptop", window("hard-mode", "req-1", a, b), http.StatusCreated)
				h.create("ptd_laptop", window("hard-mode", "req-2", b, c), http.StatusCreated)
				h.create("ptd_laptop", window("stardance", "req-3", a.Add(-day), a), http.StatusCreated)
			},
		},
		{
			name: "another project of the account, or another account's submission over the same window, is no overlap",
			run: func(t *testing.T, h *harness) {
				h.create("ptd_laptop", window("hard-mode", "req-1", a, b), http.StatusCreated)
				h.create("ptd_laptop", createBody("hard-mode", "req-2", true, []string{"k_web"}, a, b), http.StatusCreated)
				h.create("ptd_other", window("hard-mode", "req-1", a, b), http.StatusCreated)
			},
		},
		{
			name: "a withdrawal frees the withdrawn submission's windows",
			run: func(t *testing.T, h *harness) {
				first := h.create("ptd_laptop", window("hard-mode", "req-1", a, b), http.StatusCreated)
				want(t, h.call("ptd_laptop", "POST", SubmissionsPath+"/"+first.ID+"/withdraw", ""), http.StatusOK, "")
				h.create("ptd_laptop", window("stardance", "req-2", a, b), http.StatusCreated)
			},
		},
		{
			name: "a decision, whatever its outcome, doesn't free the windows, and neither does a revocation",
			run: func(t *testing.T, h *harness) {
				decided := h.create("ptd_laptop", window("hard-mode", "req-1", a, b), http.StatusCreated)
				h.move(decided.ID, MetadataRevealed, Challenged, ContentRevealed, Checked, InReview, Decided)
				want(t, h.call("ptd_laptop", "POST", SubmissionsPath, window("stardance", "req-2", a, b)), http.StatusConflict, CodeAlreadySubmitted)
				h.move(decided.ID, Revoked)
				want(t, h.call("ptd_laptop", "POST", SubmissionsPath, window("stardance", "req-3", a, b)), http.StatusConflict, CodeAlreadySubmitted)
			},
		},
		{
			name: "a submission to calibration, which takes no hours, is never refused",
			run: func(t *testing.T, h *harness) {
				h.create("ptd_laptop", window("hard-mode", "req-1", a, b), http.StatusCreated)
				h.create("ptd_laptop", window("calibration", "req-2", a, b), http.StatusCreated)
			},
		},
		{
			name: "a submission to calibration counts against no other",
			run: func(t *testing.T, h *harness) {
				h.create("ptd_laptop", window("calibration", "req-1", a, b), http.StatusCreated)
				h.create("ptd_laptop", window("hard-mode", "req-2", a, b), http.StatusCreated)
				h.create("ptd_laptop", window("calibration", "req-3", a, b), http.StatusCreated)
			},
		},
		{
			name: "the refusal names every submission the windows overlap",
			run: func(t *testing.T, h *harness) {
				one := h.create("ptd_laptop", window("hard-mode", "req-1", a, b), http.StatusCreated)
				two := h.create("ptd_laptop", createBody("stardance", "req-2", true, []string{"k_web"}, b, c), http.StatusCreated)
				rec := h.call("ptd_laptop", "POST", SubmissionsPath, createBody("hard-mode", "req-3", true, []string{"k_app", "k_web"}, a, c))
				want(t, rec, http.StatusConflict, CodeAlreadySubmitted)
				wantRefs := []RefJSON{{ID: one.ID, Program: "hard-mode"}, {ID: two.ID, Program: "stardance"}}
				slices.SortFunc(wantRefs, func(x, y RefJSON) int { return strings.Compare(x.ID, y.ID) })
				if got := refusalBody(t, rec).Submissions; !reflect.DeepEqual(got, wantRefs) {
					t.Errorf("submissions %+v, want %+v", got, wantRefs)
				}
			},
		},
		{
			name: "a retry of the overlapped submission's own create gets it back, never the refusal",
			run: func(t *testing.T, h *harness) {
				first := h.create("ptd_laptop", window("hard-mode", "req-1", a, b), http.StatusCreated)
				if again := h.create("ptd_laptop", window("hard-mode", "req-1", a, b), http.StatusOK); again.ID != first.ID {
					t.Errorf("the retry answered %s, want %s", again.ID, first.ID)
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
