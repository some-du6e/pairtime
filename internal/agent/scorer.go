// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"time"

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
	l, err := scorer.New(scorer.Options{
		Ledger:  a.dirs.Ledger(),
		Params:  a.table,
		State:   a.state,
		Buckets: bs,
		Days:    a.dirs.Days(),
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

// tallyReply is a project's tally over a span, as the local API gives it
// (store.Tally). While display.shadow is on, it leaves out every credited
// number: views show credited time as "calibrating" then, and keep the
// agent's time and the time that wasn't credited (DESIGN 6.7 step 2).
type tallyReply struct {
	CreditedS          *float64                   `json:"credited_s,omitempty"`
	PendingS           *float64                   `json:"pending_s,omitempty"`
	CreditedByKind     map[event.Activity]float64 `json:"credited_by_kind,omitempty"`
	CreditedByEvidence map[event.Evidence]float64 `json:"credited_by_evidence,omitempty"`
	AgentOnlyS         float64                    `json:"agent_only_s"`
	AgentOnlyByHarness map[event.Harness]float64  `json:"agent_only_by_harness"`
	Uncredited         map[event.Reason]float64   `json:"uncredited"`
}

func tallyOf(t store.Tally, shadow bool) tallyReply {
	r := tallyReply{AgentOnlyS: t.AgentOnlyS, AgentOnlyByHarness: t.AgentOnlyByHarness, Uncredited: t.Uncredited}
	if r.AgentOnlyByHarness == nil {
		r.AgentOnlyByHarness = map[event.Harness]float64{}
	}
	if r.Uncredited == nil {
		r.Uncredited = map[event.Reason]float64{}
	}
	if !shadow {
		credited, pending := t.CreditedS, t.PendingS
		r.CreditedS, r.PendingS = &credited, &pending
		r.CreditedByKind, r.CreditedByEvidence = t.CreditedByKind, t.CreditedByEvidence
	}
	return r
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
// or the one still going, and null before the first.
type sessionReply struct {
	Shadow  bool               `json:"shadow"`
	Session *sessionTallyReply `json:"session"`
}

type sessionTallyReply struct {
	Project string    `json:"project"`
	Name    string    `json:"name"`
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	// Open is set while the session goes on: SESSION_GAP_S hasn't passed
	// since End, and no lock or suspend came after it (DESIGN 3.8).
	Open bool `json:"open"`
	tallyReply
}

// serveDay answers GET /v1/days/{date}. A date, or today's, comes from
// its rollup in days/, which the scorer rewrites whenever one of the day's
// buckets changes, so the answer is quick enough for a status line. A day
// with no rollup is summed from the bucket store, or is empty.
func (a *agent) serveDay(w http.ResponseWriter, r *http.Request) {
	date := r.PathValue("date")
	loc := time.Local
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
	shadow := a.config().Display.Shadow
	reply := sessionReply{Shadow: shadow}
	if a.buckets != nil {
		now := time.Now()
		gap := a.table.Int(params.SESSION_GAP_S) * 1000
		s, err := store.LastSession(a.buckets, now.UnixMilli()+60_000, gap)
		if err != nil {
			a.log.Warn("GET /v1/sessions/last", "err", err)
			http.Error(w, "the agent couldn't read its buckets", http.StatusInternalServerError)
			return
		}
		if s != nil {
			var lastBreak int64
			if a.scorer != nil {
				lastBreak = a.scorer.Status().LastBreak
			}
			end := s.End.UnixMilli()
			reply.Session = &sessionTallyReply{Project: s.Project, Name: s.Name, Start: s.Start, End: s.End,
				Open:       now.UnixMilli()-end <= gap && lastBreak < end,
				tallyReply: tallyOf(s.Tally, shadow)}
		}
	}
	writeJSON(w, http.StatusOK, reply)
}
