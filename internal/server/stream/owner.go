// SPDX-License-Identifier: AGPL-3.0-or-later

package stream

import (
	"context"
	"encoding/json"
	"time"

	"github.com/some-du6e/pairtime/internal/server/dashboard"
)

// readTimeout bounds one read of a View or of a profile's totals.
const readTimeout = 10 * time.Second

// ownerState is a user topic's publishing: whether its account changed
// since the last read began, whether a read is waiting or running, when
// the last read began, how many reads in a row failed, and what the last
// credited event said.
type ownerState struct {
	dirty, busy bool
	last        time.Time
	failures    int
	prev        *Credited
}

// maxReadRetries is how many times a failed View read is tried again, Gap
// and then twice as long each time, 15 s in all at the default Gap, before
// the topic's streams get resync instead.
const maxReadRetries = 4

// Credited is a credited event's data: the owner's time today and over the
// last 7 days, and each project's, as dashboard.View counts them for the
// page. Every time is seconds credited, final and pending together, or
// split as dashboard.Time does. Projects go by their account-level ids
// (dashboard.Project.ID) and never by a name, and the page knows their
// names (D-owner-names).
//
// SP 6.4 sketched delta_s, today_s and week_s for one project, with
// today's split by activity kind. The rollups merge devices (SM 3.4), so a
// revision's own delta isn't the account's, and a View has no day split
// by kind, so each event carries the account's totals as the page shows
// them instead.
type Credited struct {
	// Day is the account's local date, YYYY-MM-DD (dashboard.View.Today).
	Day string `json:"day"`
	// DeltaS is how much TodayS grew since the topic's last credited
	// event: all of it when that event was of another day, and 0 for the
	// topic's first.
	DeltaS float64 `json:"delta_s"`
	// TodayS and WeekS are the owner's time today and over the last 7
	// days.
	TodayS float64 `json:"today_s"`
	WeekS  float64 `json:"week_s"`
	// Today and Week split them into final and pending, beside the
	// agent's time on its own.
	Today Clocks `json:"today"`
	Week  Clocks `json:"week"`
	// Projects are the projects with time today or in the last 7 days, in
	// the View's order, the most time over 30 days first: at most
	// maxEventProjects of them, since a device decides how many projects
	// it names, and the page reads every number from its own refetch.
	Projects []ProjectClocks `json:"projects"`
}

// maxEventProjects is the most projects a credited event lists.
const maxEventProjects = 32

// Clocks are the owner's time and the agent's time on its own over a span.
type Clocks struct {
	You   dashboard.Time `json:"you"`
	Agent dashboard.Time `json:"agent"`
}

// ProjectClocks are one project's clocks today and over the last 7 days.
type ProjectClocks struct {
	// Project is the project's account-level id.
	Project string `json:"project"`
	Today   Clocks `json:"today"`
	Week    Clocks `json:"week"`
}

// creditedOf returns the credited event of v, after prev, the topic's last
// one or nil.
func creditedOf(v dashboard.View, prev *Credited) Credited {
	c := Credited{Day: v.Today, Week: Clocks{You: v.Week.You, Agent: v.Week.Agent}, Projects: []ProjectClocks{}}
	c.WeekS = v.Week.You.Total()
	today := map[string]dashboard.ProjectTime{}
	if n := len(v.Days); n > 0 && v.Days[n-1].Date == v.Today {
		d := v.Days[n-1]
		c.Today = Clocks{You: d.You, Agent: d.Agent}
		for _, pt := range d.Projects {
			today[pt.Project] = pt
		}
	}
	c.TodayS = c.Today.You.Total()
	week := make(map[string]dashboard.ProjectTime, len(v.Week.Projects))
	for _, pt := range v.Week.Projects {
		week[pt.Project] = pt
	}
	for _, p := range v.Projects {
		td, inToday := today[p.ID]
		wk, inWeek := week[p.ID]
		if !inToday && !inWeek {
			continue
		}
		if len(c.Projects) == maxEventProjects {
			break
		}
		c.Projects = append(c.Projects, ProjectClocks{
			Project: p.ID,
			Today:   Clocks{You: td.You, Agent: td.Agent},
			Week:    Clocks{You: wk.You, Agent: wk.Agent},
		})
	}
	switch {
	case prev == nil:
	case prev.Day == c.Day:
		c.DeltaS = c.TodayS - prev.TodayS
	default:
		c.DeltaS = c.TodayS
	}
	return c
}

// Changed tells the hub that a transaction that changed account's rollups
// has committed. It never blocks: a user topic with a stream on it, or
// one a stream left less than Window ago, gets a credited event from a
// fresh View at once, or Gap after the last one began. An account with no
// such topic costs nothing. Profile topics need no word: they look at
// every profile they carry at each 12:00 UTC (profile.go).
func (h *Hub) Changed(account string) {
	if account == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	if t := h.liveTopicLocked(userKey+account, time.Now()); t != nil && h.o.Source != nil {
		st := t.owner
		st.dirty = true
		if !st.busy {
			st.busy = true
			h.wg.Add(1)
			go h.publishOwner(t, time.Until(st.last.Add(h.o.Gap)))
		}
	}
}

// publishOwner reads t's View once wait has passed, publishes its credited
// event, and goes on Gap apart while the account keeps changing. It ends
// when nothing changed during its last read, when t is no longer the
// account's topic, or at Close.
func (h *Hub) publishOwner(t *topic, wait time.Duration) {
	defer h.wg.Done()
	for {
		if !h.sleep(wait) {
			return
		}
		select {
		case h.reads <- struct{}{}:
		case <-h.done:
			return
		}
		h.mu.Lock()
		if h.topics[t.key] != t {
			h.mu.Unlock()
			<-h.reads
			return
		}
		st := t.owner
		st.dirty = false
		started := time.Now()
		st.last = started
		// Only this goroutine, the topic's one publisher while busy is
		// set, reads or writes prev, so the event is built and encoded
		// without the lock every stream and Changed share.
		prev := st.prev
		h.mu.Unlock()

		ctx, cancel := context.WithTimeout(h.ctx, readTimeout)
		v, err := h.o.Source.View(ctx, t.account, started)
		cancel()
		<-h.reads
		var c Credited
		var data []byte
		if err == nil {
			c = creditedOf(v, prev)
			data, err = json.Marshal(c)
		}

		h.mu.Lock()
		if h.topics[t.key] != t || h.closed {
			// Gone, or Close canceled the read: nothing to log.
			h.mu.Unlock()
			return
		}
		gap := h.o.Gap
		switch {
		case err == nil:
			st.failures = 0
			h.publishLocked(t, "credited", data, time.Now())
			st.prev = &c
		case st.failures < maxReadRetries:
			// The change stays: the next read comes after a longer wait.
			st.failures++
			st.dirty = true
			gap <<= st.failures - 1
			h.o.Logger.Warn("reading a dashboard for its stream, will try again", "user", t.account, "in", gap.String(), "err", err)
		default:
			// The pages refetch their numbers themselves.
			st.failures = 0
			h.o.Logger.Error("reading a dashboard for its stream failed; its pages resync", "user", t.account, "err", err)
			if r, err := json.Marshal(Resync{Topics: []string{TopicMe}}); err == nil {
				h.publishLocked(t, "resync", r, time.Now())
			}
		}
		if !st.dirty {
			st.busy = false
			h.mu.Unlock()
			return
		}
		wait = time.Until(st.last.Add(gap))
		h.mu.Unlock()
	}
}

// sleep waits d, or reports false at Close.
func (h *Hub) sleep(d time.Duration) bool {
	if d <= 0 {
		select {
		case <-h.done:
			return false
		default:
			return true
		}
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-h.done:
		return false
	}
}
