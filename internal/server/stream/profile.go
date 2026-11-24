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

// Profiles is what profile topics need from public profiles (WP-SV9).
type Profiles interface {
	// Resolve returns the account whose public profile name names, and
	// false for a name no public profile has, a private profile's among
	// them.
	Resolve(ctx context.Context, name string) (account string, ok bool, err error)
	// Totals returns what the account's profile topic carries as of now,
	// PublicTotals, and false once its profile isn't public, which ends
	// the topic's streams. WP-SV9 calls Changed when a profile's
	// visibility changes, so the next hour asks.
	Totals(ctx context.Context, account string, now time.Time) (ProfileTotals, bool, error)
}

// ProfileTotals is a profile event's data: what DESIGN 8.3 lets a public
// profile show of the account's time, its daily totals, with no project
// and nothing finer than a day.
type ProfileTotals struct {
	// Days are the days with credited time among the last PublicDays,
	// oldest first.
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

// PublicDays is how many local days a profile event covers, today
// included.
const PublicDays = dashboard.MonthDays

// PublicTotals returns what a profile topic carries for account as of now:
// the credited seconds of each of its last PublicDays local days that has
// any, counting only final minutes, since public pages show no pending one
// (DESIGN 6.2 D1), and only levels A and B, since level C never reaches
// public totals (D11). The agent's time on its own doesn't count. Nothing
// in it names a project or says when in a day the time was.
func PublicTotals(ctx context.Context, q rollup.Querier, account string, now time.Time) (ProfileTotals, error) {
	zone, _, err := rollup.Zone(ctx, q, account)
	if err != nil {
		return ProfileTotals{}, err
	}
	// The window steps back by whole dates at noon UTC, as the
	// dashboard's does, which no daylight saving time can move.
	y, m, d := now.In(zone).Date()
	today := time.Date(y, m, d, 12, 0, 0, 0, time.UTC)
	from := today.AddDate(0, 0, -(PublicDays - 1)).Format(dashboard.DateLayout)
	rows, err := rollup.Read(ctx, q, account, from, today.Format(dashboard.DateLayout))
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

// profileState is a profile topic's publishing: whether its account
// changed since the last hour, what its last profile event said, and the
// hour it went out.
type profileState struct {
	dirty bool
	last  []byte
	hour  time.Time
}

// hourly runs the profile pass at the top of every hour, UTC, until Close.
// The hours are fixed, never an hour after a change, so when a profile's
// event comes says nothing of when in the hour its owner worked.
func (h *Hub) hourly() {
	defer h.wg.Done()
	for {
		now := time.Now()
		next := now.Truncate(time.Hour).Add(time.Hour)
		if !h.sleep(next.Sub(now)) {
			return
		}
		h.profilePass(next)
	}
}

// profilePass publishes hour's profile events: one for each profile topic
// whose account changed since the last pass and whose totals then differ
// from its last event's. A profile that isn't public any more has its
// streams ended and its topic dropped.
func (h *Hub) profilePass(hour time.Time) {
	h.mu.Lock()
	var due []*topic
	for _, t := range h.topics {
		if t.profile != nil && t.profile.dirty {
			t.profile.dirty = false
			due = append(due, t)
		}
	}
	h.mu.Unlock()
	for _, t := range due {
		select {
		case h.reads <- struct{}{}:
		case <-h.done:
			return
		}
		ctx, cancel := context.WithTimeout(h.ctx, readTimeout)
		totals, public, err := h.o.Profiles.Totals(ctx, t.account, hour)
		cancel()
		<-h.reads
		var data []byte
		if err == nil && public {
			data, err = json.Marshal(totals)
		}

		h.mu.Lock()
		switch {
		case h.topics[t.key] != t:
		case err != nil:
			// The next hour tries again.
			t.profile.dirty = true
			h.o.Logger.Warn("reading a profile's totals for its stream", "user", t.account, "err", err)
		case !public:
			for s := range t.subs {
				h.kickLocked(s)
			}
			delete(h.topics, t.key)
		case bytes.Equal(data, t.profile.last):
		case !t.profile.hour.IsZero() && hour.Sub(t.profile.hour) < time.Hour:
			// One pass an hour can't get here; the rule is the topic's,
			// whatever runs the pass.
			t.profile.dirty = true
		default:
			h.publishLocked(t, "profile", data, hour)
			t.profile.last, t.profile.hour = data, hour
		}
		h.mu.Unlock()
	}
}
