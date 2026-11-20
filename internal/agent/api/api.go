// SPDX-License-Identifier: AGPL-3.0-or-later

// Package api serves the local dashboard's data (DESIGN 3.9): the routes
// of docs/api/local.openapi.yaml that read the agent's stores. Each reply
// is a type here, field for field with the spec, which web/tests/smoke.spec.ts
// holds the running agent to.
//
// The package only reads. The scorer loop (WP-AG4) writes the day
// rollups (DESIGN 3.5 days/) and gives them to the Server as a Store; until
// it does, the agent serves Empty, and every day has nothing on it.
package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/params"
)

// DateLayout is how the API writes a local day: YYYY-MM-DD.
const DateLayout = time.DateOnly

// MaxDays is the longest range GET /v1/days serves in one answer, about a
// quarter, so one request can't make the agent read a year of rollups.
const MaxDays = 92

// Store is what the agent's stores give the dashboard. WP-AG4's day
// rollups implement it.
type Store interface {
	// Days returns the rollups of the local days from first to last, both
	// included, as DateLayout dates, and the projects they name. A day
	// with nothing on it may be left out, and so may a project's maps and
	// lists that would be empty.
	Days(first, last string) ([]Day, []Project, error)
}

// Empty is a Store with nothing in it, which the agent serves until the
// scorer loop's stores exist (WP-AG4).
type Empty struct{}

// Days returns no days and no projects.
func (Empty) Days(first, last string) ([]Day, []Project, error) { return nil, nil, nil }

// DaysReply is GET /v1/days (the spec's Days).
type DaysReply struct {
	// Today is the agent's local date when it answered.
	Today string `json:"today"`
	// AsOf is when the agent read its stores, in UTC.
	AsOf time.Time `json:"as_of"`
	From string    `json:"from"`
	To   string    `json:"to"`
	// PendingWindowS is PENDING_WINDOW_S: how long after a minute ends
	// it stays pending (DESIGN 3.7, 6.2 D1).
	PendingWindowS int64 `json:"pending_window_s"`
	// Shadow is display.shadow (DESIGN 6.7 step 2): every view shows
	// credited time as "calibrating".
	Shadow   bool      `json:"shadow"`
	Projects []Project `json:"projects"`
	// Days holds one entry per date from From to To, in order.
	Days []Day `json:"days"`
}

// Project is a project the days name.
type Project struct {
	// Key is the project's opaque key (AGENTS.md).
	Key string `json:"key"`
	// Name is what the dashboard calls it: the repository's folder name,
	// or the name the teen gave it. Like everything this API serves, it
	// stays on this computer.
	Name string `json:"name"`
}

// Day is one local day.
type Day struct {
	Date string `json:"date"`
	// Uncredited holds all of the day's seconds that were looked at and
	// not credited, by reason, across projects.
	Uncredited map[event.Reason]float64 `json:"uncredited"`
	Projects   []ProjectDay             `json:"projects"`
}

// ProjectDay is one project's day: the chess clock (DESIGN 3.8) and the
// breakdowns behind it.
type ProjectDay struct {
	// Project is the project's key.
	Project string `json:"project"`
	// CreditedS is the teen's time, pending minutes included.
	CreditedS float64 `json:"credited_s"`
	// PendingS is the part of CreditedS in minutes that are still pending
	// (DESIGN 3.7), whose credit and level can still change.
	PendingS float64 `json:"pending_s"`
	// ByEvidence splits the final part of CreditedS by evidence level;
	// with PendingS it sums to CreditedS.
	ByEvidence ByEvidence `json:"by_evidence"`
	// ByKind splits CreditedS by activity kind (SM 5.1).
	ByKind map[event.Activity]float64 `json:"by_kind"`
	// Uncredited holds the uncredited seconds that belong to this project,
	// by reason.
	Uncredited map[event.Reason]float64 `json:"uncredited"`
	// AgentRunningS is the seconds in which any agent turn of the project
	// was running, and Agents the same by harness.
	AgentRunningS float64     `json:"agent_running_s"`
	Agents        []AgentTime `json:"agents"`
}

// ByEvidence is seconds by evidence level (SM 5.1, DESIGN 6.4).
type ByEvidence struct {
	A float64 `json:"A"`
	B float64 `json:"B"`
	C float64 `json:"C"`
}

// AgentTime is how long one harness's agent ran.
type AgentTime struct {
	Harness  event.Harness `json:"harness"`
	RunningS float64       `json:"running_s"`
}

// Options configure a Server.
type Options struct {
	// Store holds the rollups. Nil serves Empty.
	Store Store
	// Shadow reports display.shadow (DESIGN 6.7 step 2). Nil means off.
	Shadow func() bool
	// PendingWindow is PENDING_WINDOW_S. Zero takes it from the newest
	// parameter table.
	PendingWindow time.Duration
	// Now is the wall clock; nil means time.Now. Location is the time
	// zone of local days; nil means time.Local.
	Now      func() time.Time
	Location *time.Location
	// Log gets store failures. Nil discards them.
	Log *slog.Logger
}

// Server serves the dashboard's data routes. Its methods are safe for
// concurrent use.
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
	if opts.PendingWindow == 0 {
		opts.PendingWindow = newestPendingWindow()
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

var newestPendingWindow = sync.OnceValue(func() time.Duration {
	tables := params.Tables()
	return time.Duration(tables[len(tables)-1].Int(params.PENDING_WINDOW_S)) * time.Second
})

// ServeDays serves GET /v1/days?from=&to=. Without parameters it is this
// week, from Monday to today in the agent's time zone; without from, the
// Monday of the week to is in.
func (s *Server) ServeDays(w http.ResponseWriter, r *http.Request) {
	now := s.opts.Now()
	today := now.In(s.opts.Location).Format(DateLayout)
	q := r.URL.Query()
	last, err := dateParam(q["to"], "to", today)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	first, err := dateParam(q["from"], "from", monday(last))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	dates, err := between(first, last)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	stored, projects, err := s.opts.Store.Days(dates[0], dates[len(dates)-1])
	if err != nil {
		s.opts.Log.Warn("the dashboard couldn't read the day rollups", "from", dates[0], "to", dates[len(dates)-1], "err", err)
		http.Error(w, "pairtime couldn't read your days: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	days, projects := fill(dates, stored, projects)
	writeJSON(w, http.StatusOK, DaysReply{
		Today:          today,
		AsOf:           now.UTC(),
		From:           dates[0],
		To:             dates[len(dates)-1],
		PendingWindowS: int64(s.opts.PendingWindow / time.Second),
		Shadow:         s.opts.Shadow(),
		Projects:       projects,
		Days:           days,
	})
}

// dateParam reads a query parameter that is a date, or def when it is
// absent.
func dateParam(values []string, name, def string) (time.Time, error) {
	v := def
	switch len(values) {
	case 0:
	case 1:
		v = values[0]
	default:
		return time.Time{}, fmt.Errorf("%s is given %d times; give it once", name, len(values))
	}
	t, err := time.Parse(DateLayout, v)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s is %q, which isn't a date written YYYY-MM-DD", name, v)
	}
	return t, nil
}

// monday returns the date of the Monday on or before day.
func monday(day time.Time) string {
	back := (int(day.Weekday()) + 6) % 7 // Monday 0, Sunday 6
	return day.AddDate(0, 0, -back).Format(DateLayout)
}

// between lists the dates from first to last, both included.
func between(first, last time.Time) ([]string, error) {
	if last.Before(first) {
		return nil, fmt.Errorf("from %s is after to %s", first.Format(DateLayout), last.Format(DateLayout))
	}
	n := int(last.Sub(first)/(24*time.Hour)) + 1
	if n > MaxDays {
		return nil, fmt.Errorf("from %s to %s is %d days; ask for at most %d at once", first.Format(DateLayout), last.Format(DateLayout), n, MaxDays)
	}
	dates := make([]string, 0, n)
	for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
		dates = append(dates, d.Format(DateLayout))
	}
	return dates, nil
}

// fill gives every date an entry, in order, from what the store returned,
// with empty maps and lists rather than nulls, and lists each project the
// days name once. A project the store has no name for gets an empty one,
// which the dashboard words itself, so it never shows a key.
func fill(dates []string, stored []Day, known []Project) ([]Day, []Project) {
	byDate := make(map[string]Day, len(stored))
	for _, d := range stored {
		byDate[d.Date] = d
	}
	names := make(map[string]string, len(known))
	for _, p := range known {
		if _, ok := names[p.Key]; !ok {
			names[p.Key] = p.Name
		}
	}
	days := make([]Day, 0, len(dates))
	projects := []Project{}
	listed := map[string]bool{}
	for _, date := range dates {
		d := byDate[date]
		d.Date = date
		if d.Uncredited == nil {
			d.Uncredited = map[event.Reason]float64{}
		}
		if d.Projects == nil {
			d.Projects = []ProjectDay{}
		}
		for i := range d.Projects {
			p := &d.Projects[i]
			if p.ByKind == nil {
				p.ByKind = map[event.Activity]float64{}
			}
			if p.Uncredited == nil {
				p.Uncredited = map[event.Reason]float64{}
			}
			if p.Agents == nil {
				p.Agents = []AgentTime{}
			}
			if !listed[p.Project] {
				listed[p.Project] = true
				projects = append(projects, Project{Key: p.Project, Name: names[p.Project]})
			}
		}
		days = append(days, d)
	}
	return days, projects
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
