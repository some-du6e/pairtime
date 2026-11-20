// SPDX-License-Identifier: AGPL-3.0-or-later

// Package api serves the agent's tallies of your time (DESIGN 3.9): the
// routes of docs/api/local.openapi.yaml that read the agent's stores, GET
// /v1/days/{date} and GET /v1/sessions/last. `pairtime today`, `recap` and
// `statusline` read them (cmd/pairtime/localapi.go), and so does the local
// dashboard. Each reply is a type here, field for field with the spec,
// which web/tests/smoke.spec.ts holds the running agent to.
//
// The package only reads. The scorer loop (WP-AG4) writes the day rollups
// and work sessions (DESIGN 3.5) and gives them to the Server as a Store;
// until it does, the agent serves Empty, and every day has nothing on it.
package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
)

// DateLayout is how the API writes a local day: YYYY-MM-DD.
const DateLayout = time.DateOnly

// Today is the date GET /v1/days/{date} resolves to the agent's local day.
const Today = "today"

// Store is what the agent's stores give the views. WP-AG4's day rollups
// and work sessions implement it.
type Store interface {
	// Day returns the tally of each project with anything in it on the local
	// day date, a DateLayout date. A day with nothing in it has none.
	Day(date string) ([]DayProject, error)
	// LastSession returns the most recent work session (SM 5.3), or the one
	// still going, or nil before the first.
	LastSession() (*WorkSession, error)
}

// Empty is a Store with nothing in it, which the agent serves until the
// scorer loop's stores exist (WP-AG4).
type Empty struct{}

// Day returns no projects.
func (Empty) Day(string) ([]DayProject, error) { return nil, nil }

// LastSession returns no session.
func (Empty) LastSession() (*WorkSession, error) { return nil, nil }

// Tally is what the agent counts for one project over a span, a local day
// or a work session, from its minute buckets (SM 5.1, 5.3). Seconds may be
// halves.
type Tally struct {
	// CreditedS is the credited seconds: your time, pending minutes
	// included.
	CreditedS float64 `json:"credited_s"`
	// PendingS is the part of CreditedS in buckets that are still pending
	// (DESIGN 3.7), which late records may change.
	PendingS float64 `json:"pending_s"`
	// CreditedByKind and CreditedByEvidence each split CreditedS. Pending
	// seconds count at the level they have so far.
	CreditedByKind     map[event.Activity]float64 `json:"credited_by_kind"`
	CreditedByEvidence map[event.Evidence]float64 `json:"credited_by_evidence"`
	// AgentOnlyS is the seconds a turn in the project ran and nothing
	// credited, whatever their reason code (SM 5.3's agent_only_s): the
	// agent's side of the chess clock (DESIGN 3.8).
	AgentOnlyS float64 `json:"agent_only_s"`
	// AgentOnlyByHarness splits AgentOnlyS by the harness whose turn ran. It
	// names every harness that had a session in the project in the span,
	// with 0 for one that had no agent-only time, so the views can name the
	// agent whose answers were read.
	AgentOnlyByHarness map[event.Harness]float64 `json:"agent_only_by_harness"`
	// Uncredited is the seconds looked at and not credited, by reason
	// (SM 5.2). Agent-only seconds are in it too, under their own codes.
	Uncredited map[event.Reason]float64 `json:"uncredited"`
}

// DayReply is GET /v1/days/{date}: one local day with a tally for each
// project that has anything in it.
type DayReply struct {
	// Date is the day. Asked for "today", the agent says which day that is.
	Date string `json:"date"`
	// Shadow is display.shadow (DESIGN 6.7 step 2): while it is on, every
	// view shows credited totals as calibrating.
	Shadow   bool         `json:"shadow"`
	Projects []DayProject `json:"projects"`
}

// DayProject is one project's tally over a day.
type DayProject struct {
	// Project is the project's key. Name is its display name from the local
	// project map, "" when it has none; it never leaves the device.
	Project string `json:"project"`
	Name    string `json:"name"`
	Tally
}

// SessionReply is GET /v1/sessions/last.
type SessionReply struct {
	Shadow bool `json:"shadow"`
	// Session is the most recent work session, or nil before the first.
	Session *WorkSession `json:"session"`
}

// WorkSession is a work session (SM 5.3): credited minutes in one project,
// split where two are more than SESSION_GAP_S apart.
type WorkSession struct {
	Project string    `json:"project"`
	Name    string    `json:"name"`
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	// Open is set while the session goes on: no SESSION_GAP_S without
	// credit since End, and no lock or suspend (DESIGN 3.8).
	Open bool `json:"open"`
	Tally
}

// Options configure a Server.
type Options struct {
	// Store holds the tallies. Nil serves Empty.
	Store Store
	// Shadow reports display.shadow (DESIGN 6.7 step 2). Nil means off.
	Shadow func() bool
	// Now is the wall clock; nil means time.Now. Location is the time zone
	// of local days, the profile's; nil means time.Local.
	Now      func() time.Time
	Location *time.Location
	// Log gets store failures. Nil discards them.
	Log *slog.Logger
}

// Server serves the views' routes. Its methods are safe for concurrent use
// when the Store's are.
type Server struct {
	opts Options
}

// New returns a Server.
func New(opts Options) *Server {
	if opts.Store == nil {
		opts.Store = Empty{}
	}
	if opts.Shadow == nil {
		opts.Shadow = func() bool { return false }
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Location == nil {
		opts.Location = time.Local
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	return &Server{opts: opts}
}

// ServeDay serves GET /v1/days/{date}, where date is YYYY-MM-DD or
// "today", the agent's local date.
func (s *Server) ServeDay(w http.ResponseWriter, r *http.Request) {
	date := r.PathValue("date")
	if date == Today {
		date = s.opts.Now().In(s.opts.Location).Format(DateLayout)
	} else if t, err := time.Parse(DateLayout, date); err != nil || t.Format(DateLayout) != date {
		http.Error(w, fmt.Sprintf("the date is %q, which isn't %s or a date written YYYY-MM-DD", date, Today), http.StatusBadRequest)
		return
	}
	projects, err := s.opts.Store.Day(date)
	if err != nil {
		s.unavailable(w, "day "+date, err)
		return
	}
	// A copy, so nothing here writes into the store's own slice.
	out := make([]DayProject, len(projects))
	for i, p := range projects {
		p.Tally = filled(p.Tally)
		out[i] = p
	}
	writeJSON(w, http.StatusOK, DayReply{Date: date, Shadow: s.opts.Shadow(), Projects: out})
}

// ServeLastSession serves GET /v1/sessions/last.
func (s *Server) ServeLastSession(w http.ResponseWriter, r *http.Request) {
	session, err := s.opts.Store.LastSession()
	if err != nil {
		s.unavailable(w, "the last session", err)
		return
	}
	if session != nil {
		copied := *session
		copied.Tally = filled(copied.Tally)
		session = &copied
	}
	writeJSON(w, http.StatusOK, SessionReply{Shadow: s.opts.Shadow(), Session: session})
}

func (s *Server) unavailable(w http.ResponseWriter, what string, err error) {
	s.opts.Log.Warn("the views couldn't read the agent's stores", "what", what, "err", err)
	http.Error(w, "pairtime couldn't read your time: "+err.Error(), http.StatusServiceUnavailable)
}

// filled gives t's maps as {} rather than null, as the spec has them.
func filled(t Tally) Tally {
	if t.CreditedByKind == nil {
		t.CreditedByKind = map[event.Activity]float64{}
	}
	if t.CreditedByEvidence == nil {
		t.CreditedByEvidence = map[event.Evidence]float64{}
	}
	if t.AgentOnlyByHarness == nil {
		t.AgentOnlyByHarness = map[event.Harness]float64{}
	}
	if t.Uncredited == nil {
		t.Uncredited = map[event.Reason]float64{}
	}
	return t
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
