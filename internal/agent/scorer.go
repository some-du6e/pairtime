// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/ipc"
	"github.com/some-du6e/pairtime/internal/agent/retention"
	"github.com/some-du6e/pairtime/internal/agent/scorer"
	"github.com/some-du6e/pairtime/internal/agent/store"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/params"
)

// newestTable returns the newest parameter table pkg/params embeds, the
// one the agent scores with. The ledger writer, presence and the scorer
// all get it, so presence samples the way the scorer reads samples
// (DESIGN 6.2 D14) and the writer seals every BATCH_S of the same table.
func newestTable() *params.Table {
	tables := params.Tables()
	return tables[len(tables)-1]
}

// zone returns the time zone the agent counts days in: the device's own,
// since SM 5.3's day is the person's local day, or Options.Zone. The
// scorer's rollups, GET /v1/days and a session's date all use it.
func (a *agent) zone() *time.Location {
	if a.loc != nil {
		return a.loc
	}
	return time.Local
}

// startScorer opens the bucket store and makes the scorer loop (WP-AG4).
// startLedger hands the loop every batch the ledger writer seals and
// starts it once the ledger is open. It returns the step that stops the
// loop and closes the store. That step must run after the ledger writer
// closed, so the loop scores the writer's last seal.
func (a *agent) startScorer() func() {
	bs, err := store.OpenBuckets(a.dirs.Buckets())
	if err != nil {
		a.setProblem("scorer", fmt.Sprintf("the agent shows no time, because it can't open its bucket store: %v", err))
		return func() {}
	}
	// The bucket store's sweep goes first, so the scorer writes no day's
	// rollup again from minutes retention is about to drop
	// (store.Buckets.KeptFrom). The retention sweeps go on with it every
	// day (startRetention).
	if p, err := retention.Device(); err == nil {
		if _, err := bs.Drop(retention.From(time.Now(), p.Buckets)); err != nil {
			a.log.Warn("the minute buckets' first sweep", "err", err)
		}
	}
	l, err := scorer.New(scorer.Options{
		Ledger:  a.dirs.Ledger(),
		Params:  a.table,
		State:   a.state,
		Buckets: bs,
		Days:    a.dirs.Days(),
		Zone:    a.zone,
		Problem: func(msg string) { a.setProblem("scorer", msg) },
		Log:     a.log.With("component", "scorer"),
	})
	if err != nil {
		bs.Close()
		a.setProblem("scorer", fmt.Sprintf("the agent shows no time, because its scorer can't start: %v", err))
		return func() {}
	}
	a.buckets, a.scorer = bs, l
	return func() {
		l.Close()
		if err := bs.Close(); err != nil {
			a.log.Warn("closing the bucket store", "err", err)
		}
	}
}

// numbersReply is the part of a tally that display.shadow hides: your
// time, and the numbers that split or qualify it. A tally without one
// leaves every field of it out of its JSON.
type numbersReply struct {
	CreditedS          float64                    `json:"credited_s"`
	PendingS           float64                    `json:"pending_s"`
	CreditedByKind     map[event.Activity]float64 `json:"credited_by_kind"`
	CreditedByEvidence map[event.Evidence]float64 `json:"credited_by_evidence"`
	CreditedByReason   map[event.Reason]float64   `json:"credited_by_reason"`
	OnHoldS            float64                    `json:"on_hold_s,omitempty"`
}

// tallyReply is a project's tally over a span, as the local API gives it
// (store.Tally). While display.shadow is on it holds only what decision
// D-shadow-scope lets every view show: the agent's time on its own and
// SM 4.2's loops, X.reroll, X.rewrite_loop, X.same_error and X.autopilot
// (event.Reason.DarkFlow).
// Your time and its splits are left out, and so is every other uncredited
// code, X.day_cap included. pkg/score gives every second it looks at either
// credit or an uncredited code, so with the rest of the uncredited time the
// hidden total could be worked back out. Views show "calibrating" then
// (DESIGN 6.7 step 2), so nobody learns to steer the rules before they are
// fitted (SM 7.5).
type tallyReply struct {
	*numbersReply
	AgentOnlyS         float64                   `json:"agent_only_s"`
	AgentOnlyByHarness map[event.Harness]float64 `json:"agent_only_by_harness"`
	Uncredited         map[event.Reason]float64  `json:"uncredited"`
}

func tallyOf(t store.Tally, shadow bool) tallyReply {
	r := tallyReply{AgentOnlyS: t.AgentOnlyS, AgentOnlyByHarness: orEmpty(t.AgentOnlyByHarness), Uncredited: map[event.Reason]float64{}}
	for code, s := range t.Uncredited {
		if !shadow || code.DarkFlow() {
			r.Uncredited[code] = s
		}
	}
	if !shadow {
		r.numbersReply = &numbersReply{CreditedS: t.CreditedS, PendingS: t.PendingS,
			CreditedByKind: orEmpty(t.CreditedByKind), CreditedByEvidence: orEmpty(t.CreditedByEvidence),
			CreditedByReason: orEmpty(t.CreditedByReason), OnHoldS: t.OnHoldS}
	}
	return r
}

// orEmpty returns m, or an empty map in place of a nil one, so a reply
// says {} rather than null.
func orEmpty[K comparable, V any](m map[K]V) map[K]V {
	if m == nil {
		return map[K]V{}
	}
	return m
}

// dayReply is GET /v1/days/{date}: one local day of this device, with a
// tally for each project that has anything in it (SM 5.3).
type dayReply struct {
	// Date is the day, YYYY-MM-DD; asked for "today", the agent says which
	// day that is in the device's time zone. Zone names the zone, and
	// Start and End are the day's bounds in UTC.
	Date  string    `json:"date"`
	Zone  string    `json:"zone"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	// Shadow is display.shadow.
	Shadow   bool              `json:"shadow"`
	Projects []dayProjectReply `json:"projects"`
}

type dayProjectReply struct {
	Project string `json:"project"`
	Name    string `json:"name"`
	tallyReply
}

// sessionReply is GET /v1/sessions/last: the last work session (SM 5.3),
// or the one still going, and null before the first. The agent gives the
// same JSON to the CLI's sessions/last question over its socket, which
// `pairtime recap` asks (WP-CO0).
type sessionReply struct {
	Shadow  bool               `json:"shadow"`
	Session *sessionTallyReply `json:"session"`
}

type sessionTallyReply struct {
	Project string `json:"project"`
	Name    string `json:"name"`
	// Date is the local day the session started on, YYYY-MM-DD, in the
	// zone the agent counts days in: the day its first credited minute
	// starts in, as every minute belongs to the day it starts in
	// (store.DateOf). It is all a session says of when it was while
	// display.shadow is on.
	Date string `json:"date"`
	// Start is the start of the session's first credited minute and End
	// the end of its last, in UTC. Open is set while the session goes on
	// (sessionOpen). While display.shadow is on all three are left out
	// (D-shadow-scope): with the session's span, its agent time and loops
	// would give away the credited seconds in it, and whether it goes on
	// says whether its last minute counted.
	Start *time.Time `json:"start,omitempty"`
	End   *time.Time `json:"end,omitempty"`
	Open  *bool      `json:"open,omitempty"`
	tallyReply
}

// serveDay answers GET /v1/days/{date}. A date, or today's, comes from
// its rollup in days/, which the scorer rewrites whenever one of the day's
// buckets changes, so the answer is quick enough for a status line. A day
// with no rollup is summed from the bucket store, or is empty.
func (a *agent) serveDay(w http.ResponseWriter, r *http.Request) {
	date := r.PathValue("date")
	loc := a.zone()
	if date == "today" {
		date = time.Now().In(loc).Format(time.DateOnly)
	}
	day, err := a.day(date, loc)
	switch {
	case errors.Is(err, errBadDate):
		http.Error(w, "the date must be YYYY-MM-DD or today", http.StatusBadRequest)
		return
	case err != nil:
		a.log.Warn("GET /v1/days", "date", date, "err", err)
		http.Error(w, "the agent couldn't read that day", http.StatusInternalServerError)
		return
	}
	shadow := a.config().Display.Shadow
	reply := dayReply{Date: day.Date, Zone: day.Zone, Start: day.Start, End: day.End, Shadow: shadow,
		Projects: []dayProjectReply{}}
	for _, p := range day.Projects {
		reply.Projects = append(reply.Projects, dayProjectReply{Project: p.Project, Name: p.Name, tallyReply: tallyOf(p.Tally, shadow)})
	}
	writeJSON(w, http.StatusOK, reply)
}

// errBadDate is a date that isn't YYYY-MM-DD.
var errBadDate = errors.New("not a date")

// day returns the rollup of the local day date in loc: its file in days/,
// or the sum of the bucket store's minutes of the day when there is none.
func (a *agent) day(date string, loc *time.Location) (store.Day, error) {
	start, end, err := store.DayBounds(date, loc)
	if err != nil {
		return store.Day{}, errBadDate
	}
	d, err := store.ReadDay(a.dirs.Days(), date)
	if err == nil {
		return d, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		a.log.Warn("a day's rollup doesn't read, so the agent sums its buckets", "date", date, "err", err)
	}
	var bs []store.Bucket
	if a.buckets != nil {
		if bs, err = a.buckets.Range(start.UnixMilli(), end.UnixMilli()); err != nil {
			return store.Day{}, err
		}
	}
	return store.Rollup(date, loc, bs, a.table.Int(params.DAY_CAP_S))
}

// serveLastSession answers GET /v1/sessions/last.
func (a *agent) serveLastSession(w http.ResponseWriter, r *http.Request) {
	reply, err := a.lastSession(time.Now())
	if err != nil {
		a.log.Warn("GET /v1/sessions/last", "err", err)
		http.Error(w, "the agent couldn't read its buckets", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, reply)
}

// answerQuestion is ipc.Options.Answer: it answers the questions the CLI
// asks over the per-user socket. ipc.AskLastSession, which `pairtime recap`
// asks, gets the JSON that GET /v1/sessions/last gives, display.shadow's
// rule included, so recap and the dashboard show the same session. Any
// other question is ipc.ErrUnknownQuestion.
func (a *agent) answerQuestion(q string) (json.RawMessage, error) {
	if q != ipc.AskLastSession {
		return nil, fmt.Errorf("%w: %q", ipc.ErrUnknownQuestion, q)
	}
	reply, err := a.lastSession(time.Now())
	if err != nil {
		return nil, err
	}
	return json.Marshal(reply)
}

// lastSession returns the last work session at now, as GET
// /v1/sessions/last gives it and the agent answers the CLI's sessions/last
// question.
func (a *agent) lastSession(now time.Time) (sessionReply, error) {
	shadow := a.config().Display.Shadow
	reply := sessionReply{Shadow: shadow}
	if a.buckets == nil {
		return reply, nil
	}
	gap := a.table.Int(params.SESSION_GAP_S) * 1000
	s, err := store.LastSession(a.buckets, now.UnixMilli()+60_000, gap)
	if err != nil || s == nil {
		return reply, err
	}
	var lastBreak int64
	if a.scorer != nil {
		lastBreak = a.scorer.Status().LastBreak
	}
	r := &sessionTallyReply{Project: s.Project, Name: s.Name, Date: store.DateOf(s.Start.UnixMilli(), a.zone()),
		tallyReply: tallyOf(s.Tally, shadow)}
	if !shadow {
		start, end, open := s.Start, s.End, sessionOpen(s, now.UnixMilli(), gap, lastBreak)
		r.Start, r.End, r.Open = &start, &end, &open
	}
	reply.Session = r
	return reply, nil
}

// sessionOpen reports whether work session s goes on at now, in Unix ms:
// gapMS, SESSION_GAP_S, hasn't passed since its last credited minute ended,
// and no lock or suspend came at or after the end of the last slot in
// which anything was credited (DESIGN 3.8). lastBreak is the time of the
// latest lock or suspend, 0 for none. The break is held against when the
// credit stopped, not against the end of its minute: pkg/score credits
// nothing in the slot that holds a lock, so a lock right after the last
// credit lands inside the same minute.
func sessionOpen(s *store.Session, now, gapMS, lastBreak int64) bool {
	return now-s.End.UnixMilli() <= gapMS && lastBreak < s.CreditUntil.UnixMilli()
}
