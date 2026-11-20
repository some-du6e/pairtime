// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
)

// fakeStore returns what a test gives it and remembers what it was asked.
type fakeStore struct {
	projects []DayProject
	session  *WorkSession
	err      error
	asked    []string
}

func (f *fakeStore) Day(date string) ([]DayProject, error) {
	f.asked = append(f.asked, date)
	return f.projects, f.err
}

func (f *fakeStore) LastSession() (*WorkSession, error) {
	f.asked = append(f.asked, "last session")
	return f.session, f.err
}

// friday is 10:00 UTC on Friday 2026-11-20.
var friday = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

// remindBot is SM 6.2's five minutes before school, as the agent tallies it.
func remindBot() DayProject {
	return DayProject{Project: "p_be4901c04a84", Name: "remind-bot", Tally: Tally{
		CreditedS:          265,
		CreditedByKind:     map[event.Activity]float64{event.ActivityPrompting: 60, event.ActivityHandEdit: 120, event.ActivityReviewing: 50, event.ActivitySupervising: 20, event.ActivityReadingOutput: 15},
		CreditedByEvidence: map[event.Evidence]float64{event.EvidenceB: 265},
		AgentOnlyS:         20,
		AgentOnlyByHarness: map[event.Harness]float64{event.HarnessCursor: 20},
		Uncredited:         map[event.Reason]float64{event.ReasonAbsent: 15, event.ReasonAgentOnly: 20},
	}}
}

// serve sends GET path through a mux arranged as the agent arranges it.
func serve(s *Server, path string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/days/{date}", s.ServeDay)
	mux.HandleFunc("GET /v1/sessions/last", s.ServeLastSession)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	return w
}

func TestServeDay(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		now      time.Time
		loc      *time.Location
		store    *fakeStore
		shadow   bool
		wantCode int
		check    func(t *testing.T, w *httptest.ResponseRecorder, got DayReply, store *fakeStore)
	}{
		{
			name:  "today is the agent's local date, not UTC's, and the reply says which day it is",
			path:  "/v1/days/today",
			now:   time.Date(2026, 11, 20, 23, 30, 0, 0, time.UTC),
			loc:   time.FixedZone("UTC+2", 2*60*60),
			store: &fakeStore{},
			check: func(t *testing.T, w *httptest.ResponseRecorder, got DayReply, store *fakeStore) {
				if got.Date != "2026-11-21" || len(store.asked) != 1 || store.asked[0] != "2026-11-21" {
					t.Errorf("date %s, store asked %v; want 2026-11-21, the date at UTC+2", got.Date, store.asked)
				}
			},
		},
		{
			name:  "a date asks the store for that day",
			path:  "/v1/days/2026-11-19",
			store: &fakeStore{},
			check: func(t *testing.T, w *httptest.ResponseRecorder, got DayReply, store *fakeStore) {
				if got.Date != "2026-11-19" || store.asked[0] != "2026-11-19" {
					t.Errorf("date %s, store asked %v", got.Date, store.asked)
				}
			},
		},
		{
			name:  "a day with nothing in it has an empty list, not null",
			path:  "/v1/days/today",
			store: &fakeStore{},
			check: func(t *testing.T, w *httptest.ResponseRecorder, got DayReply, store *fakeStore) {
				if want := `{"date":"2026-11-20","shadow":false,"projects":[]}`; strings.TrimSpace(w.Body.String()) != want {
					t.Errorf("body %s, want %s", w.Body.String(), want)
				}
			},
		},
		{
			name:  "a project's tally comes out flat beside its key and name, with the store's numbers",
			path:  "/v1/days/today",
			store: &fakeStore{projects: []DayProject{remindBot()}},
			check: func(t *testing.T, w *httptest.ResponseRecorder, got DayReply, store *fakeStore) {
				body := w.Body.String()
				for _, want := range []string{
					`"project":"p_be4901c04a84","name":"remind-bot","credited_s":265,"pending_s":0`,
					`"agent_only_s":20`,
					`"agent_only_by_harness":{"cursor":20}`,
					`"uncredited":{"X.absent":15,"X.agent_only":20}`,
				} {
					if !strings.Contains(body, want) {
						t.Errorf("the body has no %s: %s", want, body)
					}
				}
			},
		},
		{
			name: "a harness with a session and no time on its own keeps its 0",
			path: "/v1/days/today",
			store: &fakeStore{projects: []DayProject{{Project: "p", Tally: Tally{
				AgentOnlyByHarness: map[event.Harness]float64{event.HarnessClaudeCode: 0},
			}}}},
			check: func(t *testing.T, w *httptest.ResponseRecorder, got DayReply, store *fakeStore) {
				if !strings.Contains(w.Body.String(), `"agent_only_by_harness":{"claude_code":0}`) {
					t.Errorf("the body lost claude_code's 0: %s", w.Body.String())
				}
			},
		},
		{
			name:  "a project's empty maps come out as {}, and the store's own are left alone",
			path:  "/v1/days/today",
			store: &fakeStore{projects: []DayProject{{Project: "p"}}},
			check: func(t *testing.T, w *httptest.ResponseRecorder, got DayReply, store *fakeStore) {
				body := w.Body.String()
				for _, want := range []string{`"credited_by_kind":{}`, `"credited_by_evidence":{}`, `"agent_only_by_harness":{}`, `"uncredited":{}`} {
					if !strings.Contains(body, want) {
						t.Errorf("the body has no %s: %s", want, body)
					}
				}
				if store.projects[0].CreditedByKind != nil {
					t.Error("the handler wrote into the store's slice")
				}
			},
		},
		{
			name:   "shadow is display.shadow, and nothing caches the answer",
			path:   "/v1/days/today",
			store:  &fakeStore{},
			shadow: true,
			check: func(t *testing.T, w *httptest.ResponseRecorder, got DayReply, store *fakeStore) {
				if !got.Shadow {
					t.Error("shadow is false while display.shadow is on")
				}
				if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Type") != "application/json" {
					t.Errorf("headers %v", w.Header())
				}
			},
		},
		{name: "a date without two-digit month and day is a 400", path: "/v1/days/2026-11-5", store: &fakeStore{}, wantCode: 400},
		{name: "a day the month lacks is a 400", path: "/v1/days/2026-02-30", store: &fakeStore{}, wantCode: 400},
		{name: "a word other than today is a 400", path: "/v1/days/yesterday", store: &fakeStore{}, wantCode: 400},
		{
			name:     "a store that can't be read gets a 503 that says so in words",
			path:     "/v1/days/today",
			store:    &fakeStore{err: errors.New("days/2026-11-20.json is damaged")},
			wantCode: 503,
			check: func(t *testing.T, w *httptest.ResponseRecorder, got DayReply, store *fakeStore) {
				if !strings.Contains(w.Body.String(), "couldn't read your time: days/2026-11-20.json is damaged") {
					t.Errorf("the body is %q", w.Body.String())
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now, loc := tt.now, tt.loc
			if now.IsZero() {
				now = friday
			}
			if loc == nil {
				loc = time.UTC
			}
			s := New(Options{Store: tt.store, Shadow: func() bool { return tt.shadow }, Now: func() time.Time { return now }, Location: loc})
			w := serve(s, tt.path)
			want := tt.wantCode
			if want == 0 {
				want = http.StatusOK
			}
			if w.Code != want {
				t.Fatalf("got %d %q, want %d", w.Code, w.Body.String(), want)
			}
			var got DayReply
			if w.Code == http.StatusOK {
				if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
					t.Fatalf("the body isn't a DayReply: %v", err)
				}
			}
			if tt.check != nil {
				tt.check(t, w, got, tt.store)
			}
		})
	}
}

func TestServeLastSession(t *testing.T) {
	session := func() *WorkSession {
		return &WorkSession{
			Project: "p_be4901c04a84",
			Name:    "remind-bot",
			Start:   time.Date(2026, 11, 24, 6, 40, 0, 0, time.UTC),
			End:     time.Date(2026, 11, 24, 6, 45, 0, 0, time.UTC),
			Tally:   remindBot().Tally,
		}
	}
	tests := []struct {
		name     string
		store    *fakeStore
		shadow   bool
		wantCode int
		check    func(t *testing.T, body string, store *fakeStore)
	}{
		{
			name:  "before the first work session, session is null",
			store: &fakeStore{},
			check: func(t *testing.T, body string, store *fakeStore) {
				if want := `{"shadow":false,"session":null}`; body != want {
					t.Errorf("body %s, want %s", body, want)
				}
			},
		},
		{
			name:  "a session comes with its project, its span in UTC, whether it goes on, and its tally",
			store: &fakeStore{session: session()},
			check: func(t *testing.T, body string, store *fakeStore) {
				want := `{"shadow":false,"session":{"project":"p_be4901c04a84","name":"remind-bot","start":"2026-11-24T06:40:00Z","end":"2026-11-24T06:45:00Z","open":false,"credited_s":265`
				if !strings.HasPrefix(body, want) {
					t.Errorf("body %s, want it to start %s", body, want)
				}
			},
		},
		{
			name:  "a session's empty maps come out as {}, and the store's own are left alone",
			store: &fakeStore{session: &WorkSession{Project: "p", Open: true}},
			check: func(t *testing.T, body string, store *fakeStore) {
				if !strings.Contains(body, `"open":true`) || !strings.Contains(body, `"uncredited":{}`) {
					t.Errorf("body %s", body)
				}
				if store.session.Uncredited != nil {
					t.Error("the handler wrote into the store's session")
				}
			},
		},
		{
			name:   "shadow is display.shadow",
			store:  &fakeStore{},
			shadow: true,
			check: func(t *testing.T, body string, store *fakeStore) {
				if !strings.HasPrefix(body, `{"shadow":true`) {
					t.Errorf("body %s", body)
				}
			},
		},
		{name: "a store that can't be read gets a 503", store: &fakeStore{err: errors.New("coach/sessions.jsonl is damaged")}, wantCode: 503},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New(Options{Store: tt.store, Shadow: func() bool { return tt.shadow }, Now: func() time.Time { return friday }})
			w := serve(s, "/v1/sessions/last")
			want := tt.wantCode
			if want == 0 {
				want = http.StatusOK
			}
			if w.Code != want {
				t.Fatalf("got %d %q, want %d", w.Code, w.Body.String(), want)
			}
			if tt.check != nil {
				tt.check(t, strings.TrimSpace(w.Body.String()), tt.store)
			}
		})
	}
}

// TestEmpty checks the store the agent serves until WP-AG4's exists.
func TestEmpty(t *testing.T) {
	s := New(Options{Now: func() time.Time { return friday }, Location: time.UTC})
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "today is empty", path: "/v1/days/today", want: `{"date":"2026-11-20","shadow":false,"projects":[]}`},
		{name: "any other day is empty", path: "/v1/days/2026-01-01", want: `{"date":"2026-01-01","shadow":false,"projects":[]}`},
		{name: "there is no session yet", path: "/v1/sessions/last", want: `{"shadow":false,"session":null}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := strings.TrimSpace(serve(s, tt.path).Body.String()); got != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

// TestJSONNames pins the JSON names the spec gives the enums the replies
// use as map keys, so a rename in pkg/event shows up here.
func TestJSONNames(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want string
	}{
		{name: "kinds are SM 5.1's names", v: map[event.Activity]float64{event.ActivityHandEdit: 1}, want: `{"hand_edit":1}`},
		{name: "levels are A, B and C", v: map[event.Evidence]float64{event.EvidenceC: 1}, want: `{"C":1}`},
		{name: "harnesses are pkg/event's names", v: map[event.Harness]float64{event.HarnessClaudeCode: 2}, want: `{"claude_code":2}`},
		{name: "uncredited reasons keep their X. prefix", v: map[event.Reason]float64{event.ReasonNoProgress: 1}, want: `{"X.noprog":1}`},
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
