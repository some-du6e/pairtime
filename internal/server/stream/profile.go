// SPDX-License-Identifier: AGPL-3.0-or-later

package stream

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/some-du6e/pairtime/internal/server/dashboard"
	"github.com/some-du6e/pairtime/internal/server/rollup"
)

// A public profile changes once a day, at 12:00 UTC, and shows a local date
// only once that date has ended in every time zone, by 12:00 UTC on the
// next day, since UTC-12 ends last (D-public-daily; DESIGN 8.3; docs/errata/
// 2026-11-24-public-profile-cadence.md). Its topic follows the page: an
// event at 12:00 UTC when what the page shows changed, never at any other
// time of day, so neither the owner's hours nor their zone's midnight
// shows in when events come. Removals are at once: EndProfile.

// Profiles is what profile topics need from public profiles (WP-SV9).
type Profiles interface {
	// Resolve returns the account whose public profile name names, and
	// false for a name no public profile has, a private profile's among
	// them.
	Resolve(ctx context.Context, name string) (account string, ok bool, err error)
	// Totals returns what the account's public page shows once day's
	// publication, a 12:00 UTC, has passed, PublicTotals or the page's own,
	// and false once the profile isn't public, which ends the topic's
	// streams. The topic compares what it gives for one 12:00 UTC with
	// what it gives for the one before, so it must not depend on whether a
	// stream or a page asked before: what Totals gives for a past 12:00 UTC
	// is what the page showed then.
	Totals(ctx context.Context, account string, publication time.Time) (ProfileTotals, bool, error)
}

// ProfileTotals is a profile event's data: what DESIGN 8.3 lets a public
// profile show of the account's time, its daily totals, with no project
// and nothing finer than a day.
type ProfileTotals struct {
	// Days are the days with credited time among the last PublicDays
	// dates that have ended in every time zone, oldest first.
	Days []ProfileDay `json:"days"`
}

// ProfileDay is one local day's credited time on a public profile.
type ProfileDay struct {
	// Date is YYYY-MM-DD in the profile's time zone, which sets every
	// day's bounds (DESIGN 6.2 D10) and which the profile never shows.
	Date string `json:"date"`
	// CreditedS is the day's credited seconds at level A or B, in minutes
	// that are final.
	CreditedS float64 `json:"credited_s"`
}

// PublicDays is how many local days a profile event covers.
const PublicDays = dashboard.MonthDays

// publication is the hour of the day, UTC, at which public profiles
// change.
const publication = 12 * time.Hour

// lastPublication returns the latest 12:00 UTC at or before t.
func lastPublication(t time.Time) time.Time {
	p := t.UTC().Truncate(24 * time.Hour).Add(publication)
	if p.After(t) {
		p = p.Add(-24 * time.Hour)
	}
	return p
}

// dayNumber numbers the publication at p, a 12:00 UTC, by its date's days
// since 1970-01-01: a profile event's number in its topic, the same for
// everyone who watches it.
func dayNumber(p time.Time) uint64 {
	return uint64(p.Unix() / 86400)
}

// endedEverywhere returns the newest date, YYYY-MM-DD, that has ended in
// every time zone at t. Date D ends last at UTC-12, at 12:00 UTC on D+1, so
// that is the date of t less 36 hours.
func endedEverywhere(t time.Time) string {
	return t.UTC().Add(-36 * time.Hour).Format(dashboard.DateLayout)
}

// PublicTotals returns what a profile topic carries for account at the
// publication of p, a 12:00 UTC: the credited seconds of each of the
// PublicDays dates up to the newest that has ended in every time zone by
// then, for each that has any. It counts only final minutes, since public
// pages show no pending one (DESIGN 6.2 D1), and only levels A and B,
// since level C never reaches public totals (D11). The agent's time on its
// own doesn't count. Nothing in it names a project or says when in a day
// the time was, and its dates don't depend on the account's zone, only on
// p.
//
// It reads the rollups as they are now, so for a past p it gives today's
// numbers for that window, not the ones published at p: a revision a late
// upload makes to a date already shown shows at the next publication, but
// makes no event by itself. WP-SV9's page decides what it showed at each
// publication, and its Profiles.Totals can give exactly that.
func PublicTotals(ctx context.Context, q rollup.Querier, account string, p time.Time) (ProfileTotals, error) {
	last, err := time.Parse(dashboard.DateLayout, endedEverywhere(p))
	if err != nil {
		return ProfileTotals{}, err
	}
	from := last.AddDate(0, 0, -(PublicDays - 1)).Format(dashboard.DateLayout)
	rows, err := rollup.Read(ctx, q, account, from, last.Format(dashboard.DateLayout))
	if err != nil {
		return ProfileTotals{}, err
	}
	return publicDays(rows), nil
}

// publicDays sums rollup rows into a profile event's days: final time at
// levels A and B, by day.
func publicDays(rows []rollup.Day) ProfileTotals {
	sums := make(map[string]int64)
	for _, r := range rows {
		if r.Kind == "" {
			// The agent's time on its own.
			continue
		}
		for i, level := range rollup.Levels {
			if level != "C" {
				sums[r.Day] += r.FinalMS[i]
			}
		}
	}
	out := ProfileTotals{Days: []ProfileDay{}}
	for day, ms := range sums {
		if ms > 0 {
			out.Days = append(out.Days, ProfileDay{Date: day, CreditedS: float64(ms) / 1000})
		}
	}
	slices.SortFunc(out.Days, func(a, b ProfileDay) int { return cmp.Compare(a.Date, b.Date) })
	return out
}

// daily runs the profile pass at every 12:00 UTC until Close.
func (h *Hub) daily() {
	defer h.wg.Done()
	for {
		now := time.Now()
		next := lastPublication(now).Add(24 * time.Hour)
		if !h.sleep(next.Sub(now)) {
			return
		}
		h.profilePass(next)
	}
}

// profilePass publishes the events of the publication at p, a 12:00 UTC:
// one for each profile topic whose page changed since the publication
// before, by what Totals gives for the two. It asks for both each time, so
// whether a topic has an event depends on the profile alone, never on
// whether someone watched it yesterday. A profile that isn't public any
// more has its streams ended and its topic dropped.
func (h *Hub) profilePass(p time.Time) {
	h.mu.Lock()
	var due []*topic
	for _, t := range h.topics {
		if isProfile(t) {
			due = append(due, t)
		}
	}
	h.mu.Unlock()
	for _, t := range due {
		if h.ctx.Err() != nil {
			// Close: the topics go with the hub.
			return
		}
		select {
		case h.reads <- struct{}{}:
		case <-h.done:
			return
		}
		ctx, cancel := context.WithTimeout(h.ctx, readTimeout)
		now, public, err := h.o.Profiles.Totals(ctx, t.account, p)
		var before ProfileTotals
		if err == nil && public {
			before, public, err = h.o.Profiles.Totals(ctx, t.account, p.Add(-24*time.Hour))
		}
		cancel()
		<-h.reads
		var data, was []byte
		if err == nil && public {
			if data, err = json.Marshal(now); err == nil {
				was, err = json.Marshal(before)
			}
		}

		h.mu.Lock()
		switch {
		case h.topics[t.key] != t, h.closed:
		case err != nil:
			h.o.Logger.Warn("reading a profile's totals for its stream", "user", t.account, "err", err)
		case !public:
			h.dropLocked(t)
		case bytes.Equal(data, was):
		default:
			h.publishAtLocked(t, dayNumber(p), "profile", data, p)
		}
		h.mu.Unlock()
	}
}

// EndProfile ends at once every stream on account's profile topic and
// drops the topic, as WP-SV9 calls it when the profile turns private:
// removals don't wait for 12:00 UTC (D-public-daily). The streams'
// browsers then reconnect and get 404.
func (h *Hub) EndProfile(account string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if t := h.topics[profileKey+account]; t != nil {
		h.dropLocked(t)
	}
}

// dropLocked ends every stream on t and takes t out of the hub.
func (h *Hub) dropLocked(t *topic) {
	for s := range t.subs {
		h.kickLocked(s)
	}
	delete(h.topics, t.key)
}
