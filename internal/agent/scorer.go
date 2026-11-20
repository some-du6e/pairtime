// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"fmt"
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

// todayReply is GET /v1/today: this device's time today, by project and
// evidence level (DESIGN 3.8's chess clock; SM 5.3's day), summed from the
// latest revision of each minute's bucket.
type todayReply struct {
	// Date is today in the device's time zone, Zone its name, and Start
	// and End the day's bounds in UTC.
	Date  string    `json:"date"`
	Zone  string    `json:"zone"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	// Shadow is display.shadow. While it is on, the reply leaves out every
	// credited and uncredited number, and keeps the agent's time
	// (DESIGN 6.7).
	Shadow bool `json:"shadow"`
	// CreditedS is your time today, and PendingS the part of it in minutes
	// that are still pending, which later records can change.
	CreditedS *float64 `json:"credited_s,omitempty"`
	PendingS  *float64 `json:"pending_s,omitempty"`
	// ByLevel splits CreditedS by evidence level.
	ByLevel    map[event.Evidence]float64 `json:"by_level,omitempty"`
	Uncredited map[event.Reason]float64   `json:"uncredited,omitempty"`
	// AgentRunningS is how long an agent ran today, in any project.
	AgentRunningS float64        `json:"agent_running_s"`
	Projects      []todayProject `json:"projects"`
	// FinalBefore is the time before which every minute is final; absent
	// before the scorer has scored anything.
	FinalBefore *time.Time `json:"final_before,omitempty"`
	// Minutes counts today's minutes with a bucket, and PendingMinutes
	// the pending ones.
	Minutes        int `json:"minutes"`
	PendingMinutes int `json:"pending_minutes"`
}

// todayProject is one project's part of today.
type todayProject struct {
	ProjectKey string                     `json:"project_key"`
	CreditedS  *float64                   `json:"credited_s,omitempty"`
	PendingS   *float64                   `json:"pending_s,omitempty"`
	ByLevel    map[event.Evidence]float64 `json:"by_level,omitempty"`
	ByKind     map[event.Activity]float64 `json:"by_kind,omitempty"`
	// AgentS is how long a turn of the project ran, and AgentOnlyS the
	// part of that nothing credited you for.
	AgentS     int64 `json:"agent_s"`
	AgentOnlyS int64 `json:"agent_only_s"`
}

func (a *agent) serveToday(w http.ResponseWriter, r *http.Request) {
	reply, err := a.today(time.Now(), time.Local)
	if err != nil {
		a.log.Warn("GET /v1/today", "err", err)
		http.Error(w, "the agent couldn't read today's buckets", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, reply)
}

// today sums the local day of now in loc from the bucket store.
func (a *agent) today(now time.Time, loc *time.Location) (todayReply, error) {
	date := now.In(loc).Format(time.DateOnly)
	start, end, err := store.DayBounds(date, loc)
	if err != nil {
		return todayReply{}, err
	}
	var bs []store.Bucket
	if a.buckets != nil {
		if bs, err = a.buckets.Range(start.UnixMilli(), end.UnixMilli()); err != nil {
			return todayReply{}, err
		}
	}
	day, err := store.Rollup(date, loc, bs, a.table.Int(params.DAY_CAP_S))
	if err != nil {
		return todayReply{}, err
	}
	shadow := a.config().Display.Shadow
	reply := todayReply{Date: day.Date, Zone: day.Zone, Start: day.Start, End: day.End, Shadow: shadow,
		AgentRunningS: day.AgentRunningS, Projects: []todayProject{}, Minutes: day.Minutes,
		PendingMinutes: day.PendingMinutes}
	if a.scorer != nil {
		if fb := a.scorer.Status().FinalBefore; fb > 0 {
			t := time.UnixMilli(fb).UTC()
			reply.FinalBefore = &t
		}
	}
	if !shadow {
		reply.CreditedS, reply.PendingS = &day.CreditedS, &day.PendingS
		reply.ByLevel, reply.Uncredited = day.ByLevel, day.Uncredited
	}
	for _, p := range day.Projects {
		row := todayProject{ProjectKey: p.ProjectKey, AgentS: p.AgentS, AgentOnlyS: p.AgentOnlyS}
		if !shadow {
			credited, pending := p.CreditedS, p.PendingS
			row.CreditedS, row.PendingS = &credited, &pending
			row.ByLevel, row.ByKind = p.ByLevel, p.ByKind
		}
		reply.Projects = append(reply.Projects, row)
	}
	return reply, nil
}
