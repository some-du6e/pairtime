// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/store/storetest"
)

// page is a list's answer, with each submission's id.
type page struct {
	IDs        []string
	NextCursor *string
}

// list reads one page of GET /api/v1/submissions with query, as as.
func (h *harness) list(as, query string) page {
	h.t.Helper()
	target := SubmissionsPath
	if query != "" {
		target += "?" + query
	}
	rec := h.call(as, "GET", target, "")
	want(h.t, rec, http.StatusOK, "")
	var body struct {
		Submissions []struct {
			ID string `json:"id"`
		} `json:"submissions"`
		NextCursor *string `json:"next_cursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		h.t.Fatal(err)
	}
	p := page{NextCursor: body.NextCursor, IDs: []string{}}
	for _, s := range body.Submissions {
		p.IDs = append(p.IDs, s.ID)
	}
	return p
}

// TestReads checks GET /api/v1/submissions/{id}, the list and GET
// /api/v1/programs (D-sb1-submissions, Q3, Q13 and Q14): the account sees
// its own submissions in the teen view from any of its credentials, a
// program sees the ones addressed to it in the program view, and any other
// id is 404.
func TestReads(t *testing.T) {
	start, end := t0.Add(-2*day), t0.Add(-day)
	tests := []struct {
		name string
		run  func(t *testing.T, h *harness)
	}{
		{
			name: "the account reads its own submission in the teen view from each of its computers and its web session",
			run: func(t *testing.T, h *harness) {
				mine := h.create("ptd_laptop", window("hard-mode", "req-1", start, end), http.StatusCreated)
				for _, as := range []string{"ptd_laptop", "ptd_desktop", "sess:fresh", "sess:plain"} {
					if v := view(t, h.call(as, "GET", SubmissionsPath+"/"+mine.ID, "")); !reflect.DeepEqual(v, mine) {
						t.Errorf("%s reads %+v, want %+v", as, v, mine)
					}
				}
			},
		},
		{
			name: "another account's submission, and an id the server doesn't hold, get 404 from every credential",
			run: func(t *testing.T, h *harness) {
				mine := h.create("ptd_laptop", window("hard-mode", "req-1", start, end), http.StatusCreated)
				for _, c := range []struct{ as, id string }{
					{"ptd_other", mine.ID}, {"sess:fresh2", mine.ID}, {"ptd_laptop", "s-none"}, {"prog:hard-mode", "s-none"}, {"ptd_laptop", "a%2Fb"},
				} {
					want(t, h.call(c.as, "GET", SubmissionsPath+"/"+c.id, ""), http.StatusNotFound, CodeNotFound)
				}
			},
		},
		{
			name: "a program's credential reads the submissions addressed to it in the program view: never its flags, projects, keys, credential, computer or r",
			run: func(t *testing.T, h *harness) {
				h.minute(u1, "d1", start.Add(time.Hour), "p-app")
				h.minute(u1, "d2", start.Add(time.Hour), "p-tool")
				mine := h.create("ptd_laptop", window("hard-mode", "req-1", start, end), http.StatusCreated)
				rec := h.call("prog:hard-mode", "GET", SubmissionsPath+"/"+mine.ID, "")
				want(t, rec, http.StatusOK, "")
				var got map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				var members []string
				for k := range got {
					members = append(members, k)
				}
				slices.Sort(members)
				wantMembers := []string{"challenged_at", "created_at", "decided_at", "eligibility_to_confirm", "external_id", "frozen_at", "id", "metadata_revealed_at",
					"params", "review_url", "revoked_at", "scoring_version", "state", "totals", "updated_at", "user", "windows", "withdrawn_at"}
				if !slices.Equal(members, wantMembers) {
					t.Errorf("the program view has %q, want %q", members, wantMembers)
				}
				var v ProgramView
				if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
					t.Fatal(err)
				}
				wantUser := UserJSON{PairtimeID: u1, HCASub: "sub-u1", SlackID: v.User.SlackID}
				if v.User != wantUser || v.User.SlackID == nil || *v.User.SlackID != "U-u1" || v.ExternalID != nil || v.Totals != nil || v.ReviewURL != mine.ReviewURL {
					t.Errorf("the program view %+v, want u1's ids, no external_id, no totals and %s", v, mine.ReviewURL)
				}
			},
		},
		{
			name: "a program gets 404 for a submission to another program and for one to a program that takes no hours, and a credential that doesn't check out 401",
			run: func(t *testing.T, h *harness) {
				mine := h.create("ptd_laptop", window("hard-mode", "req-1", start, end), http.StatusCreated)
				calibration := h.create("ptd_laptop", window("calibration", "req-2", start, end), http.StatusCreated)
				want(t, h.call("prog:stardance", "GET", SubmissionsPath+"/"+mine.ID, ""), http.StatusNotFound, CodeNotFound)
				want(t, h.call("prog:calibration", "GET", SubmissionsPath+"/"+calibration.ID, ""), http.StatusNotFound, CodeNotFound)
				want(t, h.call("prog:bad", "GET", SubmissionsPath+"/"+mine.ID, ""), http.StatusUnauthorized, "token_invalid")
				if p := h.list("prog:calibration", ""); len(p.IDs) != 0 {
					t.Errorf("calibration's list holds %q, want nothing", p.IDs)
				}
			},
		},
		{
			name: "the account's list holds its own submissions by update time, pages by cursor, and starts at updated_since",
			run: func(t *testing.T, h *harness) {
				var ids []string
				for i, at := range []time.Duration{0, time.Second, 2 * time.Second} {
					h.clock.set(t0.Add(at))
					v := h.create("ptd_laptop", window("hard-mode", []string{"req-1", "req-2", "req-3"}[i], start.Add(time.Duration(i)*time.Hour), start.Add(time.Duration(i+1)*time.Hour)), http.StatusCreated)
					ids = append(ids, v.ID)
				}
				h.create("ptd_other", window("hard-mode", "req-1", start, end), http.StatusCreated)
				first := h.list("ptd_laptop", "limit=2")
				if !slices.Equal(first.IDs, ids[:2]) || first.NextCursor == nil {
					t.Fatalf("the first page %+v, want %q and a cursor", first, ids[:2])
				}
				second := h.list("sess:fresh", "limit=2&cursor="+url.QueryEscape(*first.NextCursor))
				if !slices.Equal(second.IDs, ids[2:]) || second.NextCursor != nil {
					t.Errorf("the second page %+v, want %q and no cursor", second, ids[2:])
				}
				if since := h.list("ptd_desktop", "updated_since="+url.QueryEscape(ts(t0.Add(time.Second)))); !slices.Equal(since.IDs, ids[1:]) {
					t.Errorf("updated_since gave %q, want %q", since.IDs, ids[1:])
				}
				h.clock.set(t0.Add(time.Minute))
				want(t, h.call("ptd_laptop", "POST", SubmissionsPath+"/"+ids[0]+"/withdraw", ""), http.StatusOK, "")
				if all := h.list("ptd_laptop", ""); !slices.Equal(all.IDs, []string{ids[1], ids[2], ids[0]}) {
					t.Errorf("after a withdrawal the list is %q, want the withdrawn one last", all.IDs)
				}
				if theirs := h.list("ptd_other", ""); len(theirs.IDs) != 1 || slices.Contains(ids, theirs.IDs[0]) {
					t.Errorf("u2's list %q, want its own one submission", theirs.IDs)
				}
			},
		},
		{
			name: "a program's list holds only the submissions addressed to it",
			run: func(t *testing.T, h *harness) {
				mine := h.create("ptd_laptop", window("hard-mode", "req-1", start, end), http.StatusCreated)
				h.clock.set(t0.Add(time.Second))
				theirs := h.create("ptd_other", window("hard-mode", "req-1", start, end), http.StatusCreated)
				h.create("ptd_laptop", window("stardance", "req-2", end, t0), http.StatusCreated)
				if p := h.list("prog:hard-mode", ""); !slices.Equal(p.IDs, []string{mine.ID, theirs.ID}) {
					t.Errorf("hard-mode's list %q, want %q", p.IDs, []string{mine.ID, theirs.ID})
				}
			},
		},
		{
			name: "the list refuses a query it doesn't take with 400 query_invalid",
			run: func(t *testing.T, h *harness) {
				for _, q := range []string{"limit=0", "limit=101", "limit=x", "cursor=%25%25", "cursor=" + encodeCursor(Cursor{UpdatedAtMS: 1, ID: "a/b"}), "updated_since=yesterday", "updated_sinc=" + ts(t0), "limit=1&limit=2"} {
					want(t, h.call("ptd_laptop", "GET", SubmissionsPath+"?"+q, ""), http.StatusBadRequest, CodeQueryInvalid)
				}
			},
		},
		{
			name: "GET /api/v1/programs lists, to anyone, the programs that take hours, with the names teens see",
			run: func(t *testing.T, h *harness) {
				rec := h.call("", "GET", ProgramsPath, "")
				want(t, rec, http.StatusOK, "")
				var got struct {
					Programs []ProgramJSON `json:"programs"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if want := []ProgramJSON{{Name: "hard-mode", DisplayName: "Hard Mode"}, {Name: "stardance", DisplayName: "Stardance"}}; !reflect.DeepEqual(got.Programs, want) {
					t.Errorf("programs %+v, want %+v", got.Programs, want)
				}
			},
		},
		{
			name: "a submission to a program the list no longer holds keeps its name, which the view shows in place of a display name, and a retry of its create still gets it, while the program takes no new submissions",
			run: func(t *testing.T, h *harness) {
				mine := h.create("ptd_laptop", window("stardance", "req-1", start, end), http.StatusCreated)
				delete(h.svc.programs, "stardance")
				if v := view(t, h.call("ptd_laptop", "GET", SubmissionsPath+"/"+mine.ID, "")); v.Program != (ProgramJSON{Name: "stardance", DisplayName: "stardance"}) {
					t.Errorf("program %+v, want stardance by its name", v.Program)
				}
				if again := h.create("ptd_laptop", window("stardance", "req-1", start, end), http.StatusOK); again.ID != mine.ID {
					t.Errorf("a retry of the create answered %s, want %s: a retry isn't a new submission", again.ID, mine.ID)
				}
				want(t, h.call("ptd_laptop", "POST", SubmissionsPath, window("stardance", "req-2", end, t0)), http.StatusBadRequest, CodeProgramUnknown)
			},
		},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) { tt.run(t, newHarness(t, engine)) })
		}
	}
}
