// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
)

// fakeStore returns what a test gives it and remembers what it was asked.
type fakeStore struct {
	days     []Day
	projects []Project
	err      error
	asked    []string
}

func (f *fakeStore) Days(first, last string) ([]Day, []Project, error) {
	f.asked = []string{first, last}
	return f.days, f.projects, f.err
}

// friday is 10:00 UTC on Friday 2026-11-20.
var friday = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

func dates(days []Day) []string {
	var out []string
	for _, d := range days {
		out = append(out, d.Date)
	}
	return out
}

func TestServeDays(t *testing.T) {
	sample := []Day{
		{Date: "2026-11-17", Projects: []ProjectDay{{Project: "p_portfolio", CreditedS: 600, ByEvidence: ByEvidence{A: 600}}}},
		{Date: "2026-11-19", Projects: []ProjectDay{
			{Project: "p_bot", CreditedS: 300, ByEvidence: ByEvidence{B: 300}},
			{Project: "p_portfolio", CreditedS: 120, PendingS: 60, ByEvidence: ByEvidence{A: 60}},
		}},
	}
	names := []Project{{Key: "p_portfolio", Name: "portfolio"}, {Key: "p_bot", Name: "discord-bot"}}

	tests := []struct {
		name     string
		query    string
		now      time.Time
		loc      *time.Location
		store    *fakeStore
		shadow   bool
		wantCode int
		check    func(t *testing.T, w *httptest.ResponseRecorder, got DaysReply, store *fakeStore)
	}{
		{
			name:  "without parameters it is this week, from Monday to today",
			store: &fakeStore{},
			check: func(t *testing.T, w *httptest.ResponseRecorder, got DaysReply, store *fakeStore) {
				if got.Today != "2026-11-20" || got.From != "2026-11-16" || got.To != "2026-11-20" {
					t.Errorf("today %s, from %s to %s; want 2026-11-20, from 2026-11-16 to 2026-11-20", got.Today, got.From, got.To)
				}
				if !slices.Equal(store.asked, []string{"2026-11-16", "2026-11-20"}) {
					t.Errorf("the store was asked for %v", store.asked)
				}
			},
		},
		{
			name:  "to alone starts on the Monday of its week",
			query: "to=2026-11-15",
			store: &fakeStore{},
			check: func(t *testing.T, w *httptest.ResponseRecorder, got DaysReply, store *fakeStore) {
				if got.From != "2026-11-09" || got.To != "2026-11-15" {
					t.Errorf("from %s to %s; want from Monday 2026-11-09 to Sunday 2026-11-15", got.From, got.To)
				}
			},
		},
		{
			name:  "on a Monday the week is that one day",
			query: "to=2026-11-16",
			store: &fakeStore{},
			check: func(t *testing.T, w *httptest.ResponseRecorder, got DaysReply, store *fakeStore) {
				if !slices.Equal(dates(got.Days), []string{"2026-11-16"}) {
					t.Errorf("days %v, want only 2026-11-16", dates(got.Days))
				}
			},
		},
		{
			name:  "today is the agent's local date, not UTC's",
			now:   time.Date(2026, 11, 20, 23, 30, 0, 0, time.UTC),
			loc:   time.FixedZone("UTC+2", 2*60*60),
			store: &fakeStore{},
			check: func(t *testing.T, w *httptest.ResponseRecorder, got DaysReply, store *fakeStore) {
				if got.Today != "2026-11-21" || got.To != "2026-11-21" {
					t.Errorf("today %s, to %s; want 2026-11-21, the date at UTC+2", got.Today, got.To)
				}
				if !got.AsOf.Equal(time.Date(2026, 11, 20, 23, 30, 0, 0, time.UTC)) {
					t.Errorf("as_of %s, want when the agent answered", got.AsOf)
				}
			},
		},
		{
			name:  "every date in the range has an entry, in order, with {} and [] rather than null for an empty day",
			store: &fakeStore{days: sample, projects: names},
			check: func(t *testing.T, w *httptest.ResponseRecorder, got DaysReply, store *fakeStore) {
				want := []string{"2026-11-16", "2026-11-17", "2026-11-18", "2026-11-19", "2026-11-20"}
				if !slices.Equal(dates(got.Days), want) {
					t.Errorf("days %v, want %v", dates(got.Days), want)
				}
				body := w.Body.String()
				if !strings.Contains(body, `{"date":"2026-11-16","uncredited":{},"projects":[]}`) || strings.Contains(body, "null") {
					t.Errorf("an empty day isn't {} and []: %s", body)
				}
			},
		},
		{
			name:  "a project's empty maps and lists come out as {} and []",
			store: &fakeStore{days: sample, projects: names},
			check: func(t *testing.T, w *httptest.ResponseRecorder, got DaysReply, store *fakeStore) {
				body := w.Body.String()
				for _, want := range []string{`"by_kind":{}`, `"uncredited":{}`, `"agents":[]`} {
					if !strings.Contains(body, want) {
						t.Errorf("the body has no %s: %s", want, body)
					}
				}
			},
		},
		{
			name:  "projects lists each project the days name once, with its name, in the order the days name them",
			store: &fakeStore{days: sample, projects: names},
			check: func(t *testing.T, w *httptest.ResponseRecorder, got DaysReply, store *fakeStore) {
				want := []Project{{Key: "p_portfolio", Name: "portfolio"}, {Key: "p_bot", Name: "discord-bot"}}
				if !slices.Equal(got.Projects, want) {
					t.Errorf("projects %v, want %v", got.Projects, want)
				}
			},
		},
		{
			name:  "a project the store has no name for gets an empty name, never its key",
			store: &fakeStore{days: sample},
			check: func(t *testing.T, w *httptest.ResponseRecorder, got DaysReply, store *fakeStore) {
				for _, p := range got.Projects {
					if p.Name != "" {
						t.Errorf("project %s is named %q, want no name", p.Key, p.Name)
					}
				}
			},
		},
		{
			name:  "projects the days don't name are left out",
			store: &fakeStore{days: sample[:1], projects: names},
			check: func(t *testing.T, w *httptest.ResponseRecorder, got DaysReply, store *fakeStore) {
				if want := []Project{{Key: "p_portfolio", Name: "portfolio"}}; !slices.Equal(got.Projects, want) {
					t.Errorf("projects %v, want %v", got.Projects, want)
				}
			},
		},
		{
			name:  "days the store returns outside the range are left out",
			query: "from=2026-11-18&to=2026-11-18",
			store: &fakeStore{days: sample, projects: names},
			check: func(t *testing.T, w *httptest.ResponseRecorder, got DaysReply, store *fakeStore) {
				if !slices.Equal(dates(got.Days), []string{"2026-11-18"}) || len(got.Days[0].Projects) != 0 || len(got.Projects) != 0 {
					t.Errorf("got %+v, want 2026-11-18 alone and empty", got)
				}
			},
		},
		{
			name:  "a day keeps the numbers the store gave it",
			store: &fakeStore{days: sample, projects: names},
			check: func(t *testing.T, w *httptest.ResponseRecorder, got DaysReply, store *fakeStore) {
				p := got.Days[3].Projects[1]
				if p.Project != "p_portfolio" || p.CreditedS != 120 || p.PendingS != 60 || p.ByEvidence != (ByEvidence{A: 60}) {
					t.Errorf("2026-11-19's second project is %+v", p)
				}
			},
		},
		{
			name:   "shadow is display.shadow",
			store:  &fakeStore{},
			shadow: true,
			check: func(t *testing.T, w *httptest.ResponseRecorder, got DaysReply, store *fakeStore) {
				if !got.Shadow {
					t.Error("shadow is false while display.shadow is on")
				}
			},
		},
		{
			name:  "pending_window_s is PENDING_WINDOW_S from the parameter table, and nothing caches the answer",
			store: &fakeStore{},
			check: func(t *testing.T, w *httptest.ResponseRecorder, got DaysReply, store *fakeStore) {
				if got.PendingWindowS != 900 {
					t.Errorf("pending_window_s %d, want 900", got.PendingWindowS)
				}
				if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Type") != "application/json" {
					t.Errorf("headers %v", w.Header())
				}
			},
		},
		{
			name:     "a range of MaxDays days is served",
			query:    "from=2026-08-21&to=2026-11-20",
			store:    &fakeStore{},
			wantCode: 200,
			check: func(t *testing.T, w *httptest.ResponseRecorder, got DaysReply, store *fakeStore) {
				if len(got.Days) != MaxDays {
					t.Errorf("%d days, want %d", len(got.Days), MaxDays)
				}
			},
		},
		{name: "a range longer than MaxDays days is a 400", query: "from=2026-08-20&to=2026-11-20", store: &fakeStore{}, wantCode: 400},
		{name: "from after to is a 400", query: "from=2026-11-20&to=2026-11-19", store: &fakeStore{}, wantCode: 400},
		{name: "a date without two-digit month and day is a 400", query: "to=2026-11-5", store: &fakeStore{}, wantCode: 400},
		{name: "a word for a date is a 400", query: "from=yesterday", store: &fakeStore{}, wantCode: 400},
		{name: "a parameter given twice is a 400", query: "to=2026-11-19&to=2026-11-20", store: &fakeStore{}, wantCode: 400},
		{
			name:     "a store that can't be read gets a 503 that says so in words",
			store:    &fakeStore{err: errors.New("days/2026-11-19.json is damaged")},
			wantCode: 503,
			check: func(t *testing.T, w *httptest.ResponseRecorder, got DaysReply, store *fakeStore) {
				if !strings.Contains(w.Body.String(), "couldn't read your days: days/2026-11-19.json is damaged") {
					t.Errorf("the body is %q", w.Body.String())
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := tt.now
			if now.IsZero() {
				now = friday
			}
			loc := tt.loc
			if loc == nil {
				loc = time.UTC
			}
			s := New(Options{
				Store:    tt.store,
				Shadow:   func() bool { return tt.shadow },
				Now:      func() time.Time { return now },
				Location: loc,
			})
			r := httptest.NewRequest("GET", "/v1/days?"+tt.query, nil)
			w := httptest.NewRecorder()
			s.ServeDays(w, r)
			want := tt.wantCode
			if want == 0 {
				want = http.StatusOK
			}
			if w.Code != want {
				t.Fatalf("got %d %q, want %d", w.Code, w.Body.String(), want)
			}
			var got DaysReply
			if w.Code == http.StatusOK {
				if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
					t.Fatalf("the body isn't a DaysReply: %v", err)
				}
			}
			if tt.check != nil {
				tt.check(t, w, got, tt.store)
			}
		})
	}
}

// TestEmpty checks the store the agent serves until WP-AG4's exists.
func TestEmpty(t *testing.T) {
	tests := []struct {
		name  string
		check func(t *testing.T, got DaysReply)
	}{
		{
			name: "every day of this week is there with nothing on it",
			check: func(t *testing.T, got DaysReply) {
				if len(got.Days) != 5 || len(got.Projects) != 0 {
					t.Errorf("got %+v, want five empty days", got)
				}
				for _, d := range got.Days {
					if len(d.Projects) != 0 || len(d.Uncredited) != 0 {
						t.Errorf("%s isn't empty: %+v", d.Date, d)
					}
				}
			},
		},
		{
			name: "shadow is off when nothing says otherwise",
			check: func(t *testing.T, got DaysReply) {
				if got.Shadow {
					t.Error("shadow is on")
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New(Options{Now: func() time.Time { return friday }, Location: time.UTC})
			w := httptest.NewRecorder()
			s.ServeDays(w, httptest.NewRequest("GET", "/v1/days", nil))
			var got DaysReply
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			tt.check(t, got)
		})
	}
}

// TestJSONNames pins the JSON names the spec gives the enums the replies
// use as map keys and values, so a rename in pkg/event shows up here.
func TestJSONNames(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want string
	}{
		{name: "kinds are SM 5.1's names", v: map[event.Activity]float64{event.ActivityHandEdit: 1}, want: `{"hand_edit":1}`},
		{name: "uncredited reasons keep their X. prefix", v: map[event.Reason]float64{event.ReasonNoProgress: 1}, want: `{"X.noprog":1}`},
		{name: "a harness is pkg/event's name", v: AgentTime{Harness: event.HarnessClaudeCode, RunningS: 2}, want: `{"harness":"claude_code","running_s":2}`},
		{name: "levels are A, B and C", v: ByEvidence{A: 1, B: 2, C: 3}, want: `{"A":1,"B":2,"C":3}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := json.Marshal(tt.v)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != tt.want {
				t.Errorf("got %s, want %s", b, tt.want)
			}
		})
	}
}
