// SPDX-License-Identifier: AGPL-3.0-or-later

package scorer

import (
	"fmt"

	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/pkg/event"
)

// stallMS is how much longer than PENDING_WINDOW_S FinalBefore may stay
// behind the newest record before doctor hears of it. A minute is final
// PENDING_WINDOW_S after it ends (DESIGN 3.7), and Step holds FinalBefore
// back further only for a browser or chat-app run that may be a lookup (SM
// 2.5), which stallNote leaves silent, or, for less than the think part of
// a lead, for an agent app's message that hasn't paired yet (SM 2.3).
const stallMS = 10 * 60 * 1000

// watch tells doctor when FinalBefore lags, after the Step that scored
// batch b: when the newest final minute ends more than stallMS past
// PENDING_WINDOW_S before the newest record, which those minutes stay
// pending for. The newest record is the newest the window holds, but no
// later than b's seal, which the ledger writer dates from the device's
// clock: a record dated hours ahead of it, as a bridge prompt with a
// skewed transcript timestamp can be, waits for the device's clock (Step)
// and is no stall. One dated long before it, as lookback's records are,
// has a FinalBefore of its own time.
func (l *Loop) watch(b ledger.Batch) {
	var high, sampled, focusAt int64
	var focus event.AppClass
	note := func(e *event.Event) {
		switch d := e.Data.(type) {
		case *event.OSPresence, *event.OSPower:
			sampled = max(sampled, e.T)
		case *event.OSFocus:
			if e.T >= focusAt {
				focusAt, focus = e.T, d.AppClass
			}
		}
	}
	// The carry keeps each kind's latest record with its own time, and the
	// window's records are all later than the carry's.
	for _, e := range []*event.Event{l.win.carry.Focus, l.win.carry.Power, l.win.carry.Presence} {
		if e != nil {
			note(e)
		}
	}
	for i := range l.win.recs {
		e := &l.win.recs[i].ev
		high = max(high, e.T)
		note(e)
	}
	msg := ""
	if high > 0 {
		at := min(high, b.SealedAt.UnixMilli())
		msg = stallNote(at, l.cur.FinalBefore, l.pendingMS, l.win.ttl, sampled, focus)
	}
	if msg != l.stallMsg {
		if msg != "" {
			l.log.Warn("the scorer's final minutes lag", "final_before", l.cur.FinalBefore, "batch", b.Row.B)
		}
		l.stallMsg = msg
		l.publish()
	}
}

// stallNote says what doctor hears when the newest final minute, which
// ends at fb, lags at, the newest record, by more than pending plus stallMS,
// and why where the records say: no presence sample at all, which is
// presence unknown on this computer, or no sample for longer than that lag
// allows, which is the samples having stopped while records came: then
// the minutes wait for the next sample, or for ttl, BUCKET_TTL_S, of the
// records the agent stamps itself without one (hooks, its own watchers, an
// agent app's reader; never a log reader's or the bridge's), after which
// pkg/score takes them for a never-sampling device's (SM 2.1). It returns
// "" for no lag, and for a lag while samples come and a browser or chat
// app is in front: SM 2.5 holds the minutes after credit until such a run
// ends, since it may be a lookup, which is the rule working, not a fault,
// and doctor's agent line would call it one. sampled is the newest
// presence sample or power record, 0 for none, and focus the app class of
// the newest os.focus.
func stallNote(at, fb, pending, ttl, sampled int64, focus event.AppClass) string {
	lag := at - fb
	if lag <= pending+stallMS {
		return ""
	}
	msg := fmt.Sprintf("the scorer's newest final minute ends %s before its newest record, where %s is usual, so the minutes since stay pending",
		lasting(lag), lasting(pending))
	switch {
	case sampled == 0:
		msg += ": presence is unknown on this computer, so the scorer has no presence sample to keep time by"
	case at-sampled > pending+stallMS:
		msg += fmt.Sprintf(": no presence sample has come for %s, so they wait for the next one, or for %s of records the agent stamps itself (hooks, its own watchers, an agent app's reader) without one",
			lasting(at-sampled), lasting(ttl))
	case focus == event.AppBrowser || focus == event.AppChatApp:
		return ""
	}
	return msg
}

// lasting says how long ms milliseconds are, in whole minutes, or hours
// and minutes.
func lasting(ms int64) string {
	m := ms / 60_000
	switch {
	case m < 1:
		return "under a minute"
	case m < 60:
		return fmt.Sprintf("%d min", m)
	case m%60 == 0:
		return fmt.Sprintf("%d h", m/60)
	}
	return fmt.Sprintf("%d h %d min", m/60, m%60)
}
