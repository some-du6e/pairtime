// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// TestCreate checks POST /api/v1/submissions on both engines: the freeze a
// create makes at once, idempotency on the account and the request id, the
// credentials that may create, and the refusals of a create's body
// (DESIGN 7.5 steps 1 and 2; D-sb1-submissions, Q4 to Q6, Q12 and Q17).
func TestCreate(t *testing.T) {
	start, end := t0.Add(-2*day), t0.Add(-day)
	tests := []struct {
		name string
		run  func(t *testing.T, h *harness)
	}{
		{
			name: "a computer's signed create freezes at once: 201 with the frozen window, both computers' keys of the project, the scoring version, the parameter hash and SHA-256(r), and no r",
			run: func(t *testing.T, h *harness) {
				rec := h.call("ptd_laptop", "POST", SubmissionsPath, window("hard-mode", "req-1", start, end))
				want(t, rec, http.StatusCreated, "")
				v := view(t, rec)
				if got := rec.Header().Get("Location"); got != SubmissionsPath+"/"+v.ID {
					t.Errorf("Location = %q, want %s/%s", got, SubmissionsPath, v.ID)
				}
				wantProjects := []ProjectJSON{{ID: "p-app", Keys: []KeyJSON{{Device: "d1", ProjectKey: "k_app"}, {Device: "d2", ProjectKey: "k_app_desk"}}}}
				switch {
				case v.State != Frozen || v.Program != (ProgramJSON{Name: "hard-mode", DisplayName: "Hard Mode"}) || !v.TakesHours:
					t.Errorf("state %s, program %+v, takes_hours %v; want frozen, hard-mode as Hard Mode, taking hours", v.State, v.Program, v.TakesHours)
				case v.RequestID == nil || *v.RequestID != "req-1" || v.ExternalID != nil:
					t.Errorf("request_id %v, external_id %v; want req-1 and null", v.RequestID, v.ExternalID)
				case v.CreatedBy != ByDevice || v.Device == nil || *v.Device != "d1" || v.Mode != ModeWindow:
					t.Errorf("created_by %s, device_id %v, mode %s; want device, d1, window", v.CreatedBy, v.Device, v.Mode)
				case !reflect.DeepEqual(v.Projects, wantProjects):
					t.Errorf("projects %+v, want %+v: a key stands for its project, on every computer", v.Projects, wantProjects)
				case !reflect.DeepEqual(windowsOfView(v), [][2]time.Time{{start, end}}):
					t.Errorf("windows %s, want [%s, %s]", spans(windowsOfView(v)), start, end)
				case v.ScoringVersion == nil || *v.ScoringVersion != h.svc.scoringVersion || v.Params == nil || *v.Params != h.svc.params:
					t.Errorf("scoring_version %v, params %v; want %s and %s", v.ScoringVersion, v.Params, h.svc.scoringVersion, h.svc.params)
				case v.RSHA256 == nil || len(*v.RSHA256) != 64 || v.R != "":
					t.Errorf("r_sha256 %v, r %q; want 64 hex digits and no r", v.RSHA256, v.R)
				case v.EligibilityToConfirm:
					t.Error("eligibility_to_confirm is true for an account Hack Club Auth called eligible")
				case v.FrozenAt == nil || !v.FrozenAt.Equal(t0) || !v.CreatedAt.Equal(t0) || !v.UpdatedAt.Equal(t0):
					t.Errorf("times %+v, want created, updated and frozen at %s", v.Times, t0)
				case v.ReviewURL != "https://pairtime.example.org/review/submissions/"+v.ID || v.Totals != nil:
					t.Errorf("review_url %q, totals %v; want the review URL and null", v.ReviewURL, v.Totals)
				}
				events, err := Events(context.Background(), h.db.Read, v.ID)
				if err != nil {
					t.Fatal(err)
				}
				if len(events) != 1 || events[0].From != Created || events[0].To != Frozen || !events[0].At.Equal(t0) {
					t.Errorf("events %+v, want one move from created to frozen at %s", events, t0)
				}
			},
		},
		{
			name: "the freeze records the eligibility note of an account Hack Club Auth didn't call eligible",
			run: func(t *testing.T, h *harness) {
				if v := h.create("ptd_other", window("hard-mode", "req-1", start, end), http.StatusCreated); !v.EligibilityToConfirm {
					t.Error("eligibility_to_confirm is false, want true")
				}
			},
		},
		{
			name: "a retry, the same request under the same request_id, answers 200 with the stored submission and stores nothing new",
			run: func(t *testing.T, h *harness) {
				first := h.create("ptd_laptop", window("hard-mode", "req-1", start, end), http.StatusCreated)
				before := h.rows()
				h.clock.set(t0.Add(time.Minute))
				again := h.create("ptd_laptop", window("hard-mode", "req-1", start, end), http.StatusOK)
				if again.ID != first.ID || *again.RSHA256 != *first.RSHA256 || !again.FrozenAt.Equal(*first.FrozenAt) {
					t.Errorf("the retry answered %+v, want %+v", again, first)
				}
				if after := h.rows(); after != before {
					t.Errorf("the retry changed the rows from %s to %s", before, after)
				}
			},
		},
		{
			name: "a retry answers with the submission in the state it is in now, withdrawn included",
			run: func(t *testing.T, h *harness) {
				first := h.create("ptd_laptop", window("hard-mode", "req-1", start, end), http.StatusCreated)
				want(t, h.call("ptd_laptop", "POST", SubmissionsPath+"/"+first.ID+"/withdraw", ""), http.StatusOK, "")
				if again := h.create("ptd_laptop", window("hard-mode", "req-1", start, end), http.StatusOK); again.ID != first.ID || again.State != Withdrawn {
					t.Errorf("the retry answered %s in state %s, want %s withdrawn", again.ID, again.State, first.ID)
				}
			},
		},
		{
			name: "a retry of a devlog-mode create after another devlog answers with the windows frozen the first time",
			run: func(t *testing.T, h *harness) {
				h.publish(u1, "d1", "k_app", "dl1", t0.Add(-10*day))
				h.publish(u1, "d1", "k_app", "dl2", t0.Add(-8*day))
				first := h.create("sess:fresh", devlogs("hard-mode", "req-1", "p-app"), http.StatusCreated)
				h.publish(u1, "d1", "k_app", "dl3", t0.Add(-6*day))
				again := h.create("sess:fresh", devlogs("hard-mode", "req-1", "p-app"), http.StatusOK)
				if !reflect.DeepEqual(windowsOfView(again), windowsOfView(first)) {
					t.Errorf("the retry answered windows %s, want the frozen %s", spans(windowsOfView(again)), spans(windowsOfView(first)))
				}
			},
		},
		{
			name: "the same request_id with another request gets 409 request_conflict and stores nothing",
			run: func(t *testing.T, h *harness) {
				h.create("ptd_laptop", window("hard-mode", "req-1", start, end), http.StatusCreated)
				before := h.rows()
				for _, body := range []string{
					window("stardance", "req-1", start, end),
					window("hard-mode", "req-1", start, end.Add(-time.Hour)),
					createBody("hard-mode", "req-1", true, []string{"k_app", "k_web"}, start, end),
					createBody("hard-mode", "req-1", false, []string{"p-app"}, start, end),
				} {
					want(t, h.call("ptd_laptop", "POST", SubmissionsPath, body), http.StatusConflict, CodeRequestConflict)
				}
				if after := h.rows(); after != before {
					t.Errorf("the refusals changed the rows from %s to %s", before, after)
				}
			},
		},
		{
			name: "a project's keys named in another order are the same request",
			run: func(t *testing.T, h *harness) {
				first := h.create("ptd_laptop", createBody("hard-mode", "req-1", true, []string{"k_web", "k_app"}, start, end), http.StatusCreated)
				if again := h.create("ptd_laptop", createBody("hard-mode", "req-1", true, []string{"k_app", "k_web"}, start, end), http.StatusOK); again.ID != first.ID {
					t.Errorf("the retry answered %s, want %s", again.ID, first.ID)
				}
			},
		},
		{
			name: "request ids are per account, so another account's request under the same id makes a submission of its own",
			run: func(t *testing.T, h *harness) {
				mine := h.create("ptd_laptop", window("hard-mode", "req-1", start, end), http.StatusCreated)
				theirs := h.create("ptd_other", window("hard-mode", "req-1", start, end), http.StatusCreated)
				if mine.ID == theirs.ID {
					t.Errorf("both accounts got %s", mine.ID)
				}
			},
		},
		{
			name: "a web session with a fresh login made at most 10 minutes before creates, naming projects by id",
			run: func(t *testing.T, h *harness) {
				for i, as := range []string{"sess:fresh", "sess:edge"} {
					body := createBody("hard-mode", fmt.Sprintf("req-%d", i), false, []string{"p-web"}, start.Add(time.Duration(i)*time.Hour), start.Add(time.Duration(i+1)*time.Hour))
					v := h.create(as, body, http.StatusCreated)
					if v.CreatedBy != BySession || v.Device != nil {
						t.Errorf("%s: created_by %s, device_id %v; want session and null", as, v.CreatedBy, v.Device)
					}
				}
			},
		},
		{
			name: "a web session without a fresh login, or with one older than 10 minutes, gets 403 fresh_login_required with the login URL",
			run: func(t *testing.T, h *harness) {
				for _, as := range []string{"sess:plain", "sess:stale"} {
					rec := h.call(as, "POST", SubmissionsPath, createBody("hard-mode", "req-1", false, []string{"p-app"}, start, end))
					want(t, rec, http.StatusForbidden, CodeFreshLoginRequired)
					if e := refusalBody(t, rec); e.LoginURL != "/auth/hca/login?fresh=1" {
						t.Errorf("%s: login_url %q, want /auth/hca/login?fresh=1", as, e.LoginURL)
					}
				}
				if got := h.rows(); !strings.HasPrefix(got, "submissions 0,") {
					t.Errorf("the refusals stored %s", got)
				}
			},
		},
		{
			name: "a web session's create from another site gets 403 cross_origin",
			run: func(t *testing.T, h *harness) {
				r := h.request("sess:fresh", "POST", SubmissionsPath, createBody("hard-mode", "req-1", false, []string{"p-app"}, start, end))
				r.Header.Set("Sec-Fetch-Site", "cross-site")
				want(t, h.serve(r), http.StatusForbidden, CodeCrossOrigin)
			},
		},
		{
			name: "a request with no credential gets 401 not_signed_in, and a session that can't be read 503",
			run: func(t *testing.T, h *harness) {
				rec := h.call("", "POST", SubmissionsPath, window("hard-mode", "req-1", start, end))
				want(t, rec, http.StatusUnauthorized, CodeNotSignedIn)
				if got := rec.Header().Values("WWW-Authenticate"); len(got) != 2 {
					t.Errorf("WWW-Authenticate %q, want the Bearer and Session challenges", got)
				}
				rec = h.call("sess:down", "POST", SubmissionsPath, window("hard-mode", "req-1", start, end))
				want(t, rec, http.StatusServiceUnavailable, "unavailable")
				if got := rec.Header().Get("Retry-After"); got != "5" {
					t.Errorf("Retry-After %q, want 5", got)
				}
			},
		},
		{
			name: "an account that isn't verified gets 403 not_verified, by session or by device, and stores nothing",
			run: func(t *testing.T, h *harness) {
				want(t, h.call("ptd_pending", "POST", SubmissionsPath, createBody("hard-mode", "req-1", true, []string{"k_x"}, start, end)), http.StatusForbidden, CodeNotVerified)
				want(t, h.call("sess:pending", "POST", SubmissionsPath, createBody("hard-mode", "req-2", false, []string{"p-pending"}, start, end)), http.StatusForbidden, CodeNotVerified)
				if got := h.rows(); !strings.HasPrefix(got, "submissions 0,") {
					t.Errorf("the refusals stored %s", got)
				}
			},
		},
		{
			name: "a program the list doesn't hold gets 400 program_unknown",
			run: func(t *testing.T, h *harness) {
				want(t, h.call("ptd_laptop", "POST", SubmissionsPath, window("winter-games", "req-1", start, end)), http.StatusBadRequest, CodeProgramUnknown)
			},
		},
		{
			name: "a program's credential can't create: 403 program_not_allowed",
			run: func(t *testing.T, h *harness) {
				want(t, h.call("prog:hard-mode", "POST", SubmissionsPath, window("hard-mode", "req-1", start, end)), http.StatusForbidden, CodeProgramNotAllowed)
			},
		},
		{
			name: "a project the account doesn't hold, or another computer's key, gets 400 project_unknown",
			run: func(t *testing.T, h *harness) {
				want(t, h.call("sess:fresh", "POST", SubmissionsPath, createBody("hard-mode", "req-1", false, []string{"p-other"}, start, end)), http.StatusBadRequest, CodeProjectUnknown)
				want(t, h.call("ptd_laptop", "POST", SubmissionsPath, createBody("hard-mode", "req-2", true, []string{"k_tool"}, start, end)), http.StatusBadRequest, CodeProjectUnknown)
			},
		},
		{
			name: "a body that isn't one create, a member named twice or in another letter case included, gets 400 with the code of what is wrong",
			run: func(t *testing.T, h *harness) {
				w := `"window":{"start":"` + ts(start) + `","end":"` + ts(end) + `"}`
				many := make([]string, MaxProjects+1)
				for i := range many {
					many[i] = fmt.Sprintf("k_%d", i)
				}
				for _, c := range []struct{ as, body, code string }{
					{"ptd_laptop", `{"program":"hard-mode","project_keys":["k_app"],` + w + `}`, "body_shape"},
					{"ptd_laptop", `{"program":"hard-mode","request_id":"r","projects":["p-app"],"project_keys":["k_app"],` + w + `}`, "body_shape"},
					{"ptd_laptop", `{"program":"hard-mode","request_id":"r",` + w + `}`, "body_shape"},
					{"ptd_laptop", `{"program":"hard-mode","request_id":"r","project_keys":["k_app"],"devlog_windows":true,` + w + `}`, "body_shape"},
					{"ptd_laptop", `{"program":"hard-mode","request_id":"r","project_keys":["k_app"],"devlog_windows":false}`, "body_shape"},
					{"ptd_laptop", `{"program":"hard-mode","request_id":"r","project_keys":["k_app"],"window":{"start":"` + ts(start) + `"}}`, "body_shape"},
					{"ptd_laptop", `{"program":"hard-mode","request_id":"r","project_keys":["k_app"],"window":{"start":"yesterday","end":"` + ts(end) + `"}}`, "body_shape"},
					{"ptd_laptop", `{"program":"hard-mode","request_id":"r","project_keys":["k_app"],"claimed_hours":3,` + w + `}`, "body_shape"},
					{"ptd_laptop", `{"program":"hard-mode","request_id":"r","project_keys":[],` + w + `}`, "body_shape"},
					{"ptd_laptop", `{"Program":"hard-mode","request_id":"r","project_keys":["k_app"],` + w + `}`, "body_shape"},
					{"ptd_laptop", `{"program":"hard-mode","request_id":"r","Program":"calibration","project_keys":["k_app"],` + w + `}`, "body_shape"},
					{"ptd_laptop", `{"program":"hard-mode","request_id":"r","request_id":"s","project_keys":["k_app"],` + w + `}`, "body_shape"},
					{"ptd_laptop", `{"program":"hard-mode","request_id":"r","project_keys":["k_app"],"window":{"Start":"` + ts(start) + `","end":"` + ts(end) + `"}}`, "body_shape"},
					{"ptd_laptop", `{"program":"hard-mode","request_id":"r","project_keys":["k_app"],"window":{"start":"` + ts(start) + `","start":"` + ts(start) + `","end":"` + ts(end) + `"}}`, "body_shape"},
					{"ptd_laptop", `["hard-mode"]`, "body_shape"},
					{"ptd_laptop", `{"program":"hard-mode",`, "json_invalid"},
					{"ptd_laptop", "{\"program\":\"hard-mode\xff\"}", "json_invalid"},
					{"ptd_laptop", `{"program":"hard-mode","request_id":"r/1","project_keys":["k_app"],` + w + `}`, CodeRequestIDInvalid},
					{"ptd_laptop", `{"program":"hard-mode","request_id":"r","project_keys":["k app"],` + w + `}`, CodeProjectInvalid},
					{"ptd_laptop", `{"program":"hard-mode","request_id":"r","project_keys":["k_app","k_app"],` + w + `}`, CodeProjectRepeated},
					{"ptd_laptop", createBody("hard-mode", "r", true, many, start, end), CodeTooManyProjects},
					{"sess:fresh", `{"program":"hard-mode","request_id":"r","project_keys":["k_app"],` + w + `}`, "body_shape"},
				} {
					want(t, h.call(c.as, "POST", SubmissionsPath, c.body), http.StatusBadRequest, c.code)
				}
				if got := h.rows(); !strings.HasPrefix(got, "submissions 0,") {
					t.Errorf("the refusals stored %s", got)
				}
			},
		},
		{
			name: "a window that doesn't start before it ends, ends after the freeze or starts before the 180-day horizon gets 400 window_invalid",
			run: func(t *testing.T, h *harness) {
				for i, w := range [][2]time.Time{
					{end, end},
					{end, start},
					{start, t0.Add(time.Millisecond)},
					{t0.Add(-180*day - time.Millisecond), end},
				} {
					want(t, h.call("ptd_laptop", "POST", SubmissionsPath, window("hard-mode", fmt.Sprintf("req-%d", i), w[0], w[1])), http.StatusBadRequest, CodeWindowInvalid)
				}
				if got := h.rows(); !strings.HasPrefix(got, "submissions 0,") {
					t.Errorf("the refusals stored %s", got)
				}
			},
		},
		{
			name: "a window from exactly the horizon to exactly the freeze is taken",
			run: func(t *testing.T, h *harness) {
				v := h.create("ptd_laptop", window("hard-mode", "req-1", t0.Add(-180*day), t0), http.StatusCreated)
				if got := windowsOfView(v); !reflect.DeepEqual(got, [][2]time.Time{{t0.Add(-180 * day), t0}}) {
					t.Errorf("windows %s", spans(got))
				}
			},
		},
		{
			name: "a devlog-mode create of projects with no devlog windows gets 409 no_devlog_windows, and the client sends one window instead",
			run: func(t *testing.T, h *harness) {
				want(t, h.call("sess:fresh", "POST", SubmissionsPath, devlogs("hard-mode", "req-1", "p-app")), http.StatusConflict, CodeNoDevlogWindows)
				h.publish(u1, "d1", "k_app", "dl1", t0.Add(-day))
				want(t, h.call("sess:fresh", "POST", SubmissionsPath, devlogs("hard-mode", "req-1", "p-app")), http.StatusConflict, CodeNoDevlogWindows)
				h.create("sess:fresh", createBody("hard-mode", "req-2", false, []string{"p-app"}, start, end), http.StatusCreated)
			},
		},
		{
			name: "a submission to calibration, which takes no hours, says so",
			run: func(t *testing.T, h *harness) {
				if v := h.create("ptd_laptop", window("calibration", "req-1", start, end), http.StatusCreated); v.TakesHours || v.Program.DisplayName != "Calibration" {
					t.Errorf("takes_hours %v, program %+v; want false and Calibration", v.TakesHours, v.Program)
				}
			},
		},
		{
			name: "OnFreeze runs in the freeze's op with the frozen submission, and its error refuses the create, which stores nothing",
			run: func(t *testing.T, h *harness) {
				var got []Submission
				h.svc.o.OnFreeze = func(ctx context.Context, tx writer.Tx, s Submission) error {
					got = append(got, s)
					if s.RequestID == "req-fail" {
						return fmt.Errorf("holds: the disk is full")
					}
					return nil
				}
				v := h.create("ptd_laptop", window("hard-mode", "req-1", start, end), http.StatusCreated)
				if len(got) != 1 || got[0].ID != v.ID || got[0].State != Frozen || len(got[0].Windows) != 1 {
					t.Fatalf("OnFreeze saw %+v, want the frozen %s", got, v.ID)
				}
				before := h.rows()
				want(t, h.call("ptd_laptop", "POST", SubmissionsPath, window("stardance", "req-fail", end, end.Add(time.Hour))), http.StatusServiceUnavailable, "unavailable")
				if after := h.rows(); after != before {
					t.Errorf("the refused create changed the rows from %s to %s", before, after)
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

// seed stores n submissions of account directly, made by createdBy and in
// state, each with windows windows of a minute and no keys, so none
// overlaps a create. A withdrawn one has its withdrawal time.
func (h *harness) seed(account string, n int, createdBy string, state State, windows int) {
	h.t.Helper()
	var withdrawn any
	if state == Withdrawn {
		withdrawn = 0
	}
	err := h.w.Do(context.Background(), n*(windows+1), func(ctx context.Context, tx writer.Tx) error {
		for i := range n {
			id := fmt.Sprintf("seed-%s-%s-%d", account, createdBy, i)
			var request, external any = id, nil
			if createdBy == "program" {
				request, external = nil, id
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO submissions (id, user_id, program, takes_hours, external_id, request_id, request_sha256,
created_by, mode, state, created_at_ms, updated_at_ms, frozen_at_ms, withdrawn_at_ms)
VALUES ($1, $2, 'hard-mode', 1, $3, $4, 'seed', $5, 'window', $6, 0, 0, 0, $7)`, id, account, external, request, createdBy, string(state), withdrawn); err != nil {
				return err
			}
			for j := range windows {
				at := t0.Add(-170 * day).Add(time.Duration(j) * time.Minute).UnixMilli()
				if _, err := tx.ExecContext(ctx, `INSERT INTO submission_windows (submission_id, start_ms, end_ms) VALUES ($1, $2, $3)`, id, at, at+60_000); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		h.t.Fatal(err)
	}
}

// TestCaps checks the caps on what one account holds (D-sb1-submissions,
// Q1): 1,000 submissions made with its own credentials, withdrawn ones
// included, and 10,000 frozen windows across all of its submissions. A
// create past either gets 409 limit_reached and stores nothing, while a
// retry of a create the server holds still gets 200.
func TestCaps(t *testing.T) {
	start, end := t0.Add(-2*day), t0.Add(-day)
	tests := []struct {
		name string
		run  func(t *testing.T, h *harness)
	}{
		{
			name: "an account's 1,001st create gets 409 limit_reached and stores nothing, while a retry of a create the server holds still gets 200",
			run: func(t *testing.T, h *harness) {
				held := h.create("ptd_laptop", window("hard-mode", "req-held", start, end), http.StatusCreated)
				h.seed(u1, MaxSubmissions-1, ByDevice, Frozen, 0)
				before := h.rows()
				want(t, h.call("sess:fresh", "POST", SubmissionsPath, createBody("hard-mode", "req-new", false, []string{"p-web"}, start, end)), http.StatusConflict, CodeLimitReached)
				if after := h.rows(); after != before {
					t.Errorf("the refusal changed the rows from %s to %s", before, after)
				}
				if again := h.create("ptd_laptop", window("hard-mode", "req-held", start, end), http.StatusOK); again.ID != held.ID {
					t.Errorf("the retry answered %s, want %s", again.ID, held.ID)
				}
			},
		},
		{
			name: "withdrawn submissions count toward the 1,000",
			run: func(t *testing.T, h *harness) {
				h.seed(u1, MaxSubmissions, BySession, Withdrawn, 0)
				want(t, h.call("ptd_laptop", "POST", SubmissionsPath, window("hard-mode", "req-new", start, end)), http.StatusConflict, CodeLimitReached)
			},
		},
		{
			name: "submissions a program made don't count toward the 1,000, and another account's cap is its own",
			run: func(t *testing.T, h *harness) {
				h.seed(u1, MaxSubmissions-1, ByDevice, Frozen, 0)
				h.seed(u1, 3, "program", Created, 0)
				h.seed(u2, MaxSubmissions, ByDevice, Frozen, 0)
				h.create("ptd_laptop", window("hard-mode", "req-new", start, end), http.StatusCreated)
				want(t, h.call("ptd_laptop", "POST", SubmissionsPath, window("hard-mode", "req-next", end, end.Add(time.Hour))), http.StatusConflict, CodeLimitReached)
			},
		},
		{
			name: "a create that takes the account to 10,000 frozen windows is taken, one past them gets 409 limit_reached and stores nothing, and a retry of the create the server holds still gets 200",
			run: func(t *testing.T, h *harness) {
				h.seed(u1, 1, "program", Frozen, MaxWindows-1)
				first := h.create("ptd_laptop", window("hard-mode", "req-1", start, end), http.StatusCreated)
				before := h.rows()
				want(t, h.call("ptd_laptop", "POST", SubmissionsPath, window("hard-mode", "req-2", end, end.Add(time.Hour))), http.StatusConflict, CodeLimitReached)
				if again := h.create("ptd_laptop", window("hard-mode", "req-1", start, end), http.StatusOK); again.ID != first.ID {
					t.Errorf("the retry answered %s, want %s", again.ID, first.ID)
				}
				if after := h.rows(); after != before {
					t.Errorf("the refusal and the retry changed the rows from %s to %s", before, after)
				}
			},
		},
		{
			name: "a withdrawn submission's windows still count toward the 10,000, since a withdrawal frees their time but not their rows",
			run: func(t *testing.T, h *harness) {
				h.seed(u1, 1, ByDevice, Withdrawn, MaxWindows-1)
				h.create("ptd_laptop", window("hard-mode", "req-1", start, end), http.StatusCreated)
				before := h.rows()
				want(t, h.call("ptd_laptop", "POST", SubmissionsPath, window("hard-mode", "req-2", end, end.Add(time.Hour))), http.StatusConflict, CodeLimitReached)
				if after := h.rows(); after != before {
					t.Errorf("the refusal changed the rows from %s to %s", before, after)
				}
			},
		},
		{
			name: "a devlog-mode create whose list would take the account past 10,000 windows gets 409 limit_reached",
			run: func(t *testing.T, h *harness) {
				h.seed(u1, 1, "program", Frozen, MaxWindows-1)
				for i, at := range []time.Duration{-10 * day, -8 * day, -6 * day} {
					h.publish(u1, "d1", "k_app", fmt.Sprintf("dl%d", i), t0.Add(at))
				}
				want(t, h.call("sess:fresh", "POST", SubmissionsPath, devlogs("hard-mode", "req-1", "p-app")), http.StatusConflict, CodeLimitReached)
			},
		},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) { tt.run(t, newHarness(t, engine)) })
		}
	}
}

// rowsDoer records the rows each op declares to the writer behind it.
type rowsDoer struct {
	Doer
	declared []int
}

func (d *rowsDoer) Do(ctx context.Context, rows int, op writer.Op) error {
	d.declared = append(d.declared, rows)
	return d.Doer.Do(ctx, rows, op)
}

// TestDeclaredRows checks that a create tells the writer at least as many
// rows as it stores, and not many more, which writer.Do needs to keep a
// batch to its bound: a devlog list freezes a row per window, and each of
// a project's keys on every computer takes a row. A retry declares the one
// row it reads.
func TestDeclaredRows(t *testing.T) {
	stored := func(t *testing.T, h *harness) int {
		t.Helper()
		n := 0
		for _, table := range []string{"submissions", "submission_projects", "submission_keys", "submission_windows", "submission_events", "submission_flags"} {
			var c int
			if err := h.db.Read.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM `+table).Scan(&c); err != nil {
				t.Fatal(err)
			}
			n += c
		}
		return n
	}
	tests := []struct {
		name  string
		setup func(h *harness)
		body  string
	}{
		{
			name: "a devlog-mode create whose list freezes 120 windows",
			setup: func(h *harness) {
				for i := 0; i <= 120; i++ {
					h.publish(u1, "d1", "k_app", fmt.Sprintf("dl%d", i), t0.Add(-time.Duration(130-i)*time.Hour))
				}
			},
			body: devlogs("hard-mode", "req-1", "p-app"),
		},
		{
			name:  "a create of one window",
			setup: func(*harness) {},
			body:  window("hard-mode", "req-1", t0.Add(-2*day), t0.Add(-day)),
		},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name+" declares at least the rows it stores and at most four more, and its retry one", func(t *testing.T) {
				d := &rowsDoer{}
				h := newHarness(t, engine, func(o *Options) { d.Doer, o.Writer = o.Writer, d })
				tt.setup(h)
				v := h.create("ptd_laptop", tt.body, http.StatusCreated)
				got, rows := d.declared[len(d.declared)-1], stored(t, h)
				if got < rows || got > rows+4 {
					t.Errorf("the create of %d windows declared %d rows and stored %d", len(v.Windows), got, rows)
				}
				h.create("ptd_laptop", tt.body, http.StatusOK)
				if got := d.declared[len(d.declared)-1]; got != 1 {
					t.Errorf("the retry declared %d rows, want 1", got)
				}
			})
		}
	}
}
