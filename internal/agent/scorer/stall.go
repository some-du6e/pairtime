// SPDX-License-Identifier: AGPL-3.0-or-later

package scorer

import (
	"fmt"
	"slices"

	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/pkg/event"
)

// stallMS is how much longer than PENDING_WINDOW_S FinalBefore may stay
// behind the newest record before doctor hears of it. A minute is final
// PENDING_WINDOW_S after it ends (DESIGN 3.7), and Step holds FinalBefore
// back further for a browser or chat-app run that may be a lookup (SM
// 2.5), which stallNote leaves silent; for less than the think part of a
// lead, for an agent app's message that hasn't paired yet (SM 2.3); and,
// for PENDING_WINDOW_S - READ_HORIZON_S after the sampler's record that
// ends a gap longer than BUCKET_TTL_S, such as the resume after a night's
// suspend, at the sample before the gap, in case a record dated inside
// the gap is still on its way (SM 2.1). That hold can keep FinalBefore
// hours behind, so stallNote names it while it lasts, and so is a slot
// whose presence nothing decides, where Step holds FinalBefore until a
// sample does, or for BUCKET_TTL_S plus that allowance at most (SM 2.1,
// score.Output.Undecided).
const stallMS = 10 * 60 * 1000

// sampling is what watch knows of the presence sampler's records, as the
// window and the carry hold them: the newest one's t, 0 for none, and the
// end and length of the latest gap between two of them longer than
// BUCKET_TTL_S that pkg/score holds the clock for, 0 for none.
type sampling struct{ newest, gapEnd, gap int64 }

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
	var high, focusAt int64
	var focus event.AppClass
	var times []int64 // the sampler's records'
	note := func(e *event.Event) {
		switch d := e.Data.(type) {
		case *event.OSPresence, *event.OSPower:
			times = append(times, e.T)
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
	// The latest gap longer than BUCKET_TTL_S is in what the window and
	// the carry hold while pkg/score's hold for it lasts, since FinalBefore
	// stays at the sample before it, and the window starts Lookback before
	// FinalBefore.
	var stamped []int64
	if l.win.carry.Stamped > 0 {
		stamped = append(stamped, l.win.carry.Stamped)
	}
	for i := range l.win.recs {
		if e := &l.win.recs[i].ev; stamps(e) {
			stamped = append(stamped, e.T)
		}
	}
	var s sampling
	if len(times) > 0 {
		s.newest = slices.Max(times)
	}
	s.gapEnd, s.gap = heldGap(times, stamped, l.win.ttl, l.win.late, high)
	msg := ""
	if high > 0 {
		at := min(high, b.SealedAt.UnixMilli())
		msg = stallNote(at, l.cur.FinalBefore, l.pendingMS, l.win.ttl, l.win.late, s, focus, l.undecided)
	}
	if msg != l.stallMsg {
		if msg != "" {
			l.log.Warn("the scorer's final minutes lag", "final_before", l.cur.FinalBefore, "batch", b.Row.B)
		}
		l.stallMsg = msg
		l.publish()
	}
}

// heldGap returns the end and length of the latest gap longer than ttl,
// BUCKET_TTL_S, between two of the sampler's record times in times, for
// which pkg/score holds the clock (reach.held): none of the stamped
// records, the times of records the agent stamps itself, is dated more
// than ttl after the gap's start, before its end, and late,
// PENDING_WINDOW_S - READ_HORIZON_S, before high, the newest record. Such
// a record ended the reach before the gap, and once its cut has taken
// effect nothing holds the clock there. It returns 0, 0 for no such gap.
func heldGap(times, stamped []int64, ttl, late, high int64) (end, length int64) {
	sorted := slices.Clone(times)
	slices.Sort(sorted)
	for i := 1; i < len(sorted); i++ {
		from, to := sorted[i-1], sorted[i]
		if to-from <= ttl || slices.ContainsFunc(stamped, func(t int64) bool {
			return t > from+ttl && t < to && t <= high-late
		}) {
			continue
		}
		end, length = to, to-from
	}
	return end, length
}

// stallNote says what doctor hears when the newest final minute, which
// ends at fb, lags at, the newest record, by more than pending plus stallMS,
// and why where the records say: no presence sample at all, which is
// presence unknown on this computer, or no sample for longer than that lag
// allows, which is the samples having stopped while records came: then
// the minutes wait for the next sample, or for one record the agent stamps
// itself (a hook's, one of its own watchers', an agent app reader's; never
// a log reader's or the bridge's) dated more than ttl, BUCKET_TTL_S, after
// the last sample, and then any record late, PENDING_WINDOW_S -
// READ_HORIZON_S, after that one, after which pkg/score takes them for a
// never-sampling device's (SM 2.1). A sample that ends a gap longer than
// ttl, as the morning's first after a night's suspend, holds the minutes
// since the sample before it until the newest record is late past it (SM
// 2.1's wait for a record dated inside the gap), and the note says so
// while it does. When pkg/score holds FinalBefore at undecided, the start
// of a slot whose presence nothing decides yet (score.Output.Undecided),
// the note says so before any other cause: no minute is final before its
// slots are decided, and such a slot waits for a sample dated within ttl
// after it until the newest record is late past that, when it counts as
// unknown (SM 2.1). It returns "" for no lag,
// and for a lag while samples come and a browser or chat app is in front:
// SM 2.5 holds the minutes after credit until such a run ends, since it
// may be a lookup, which is the rule working, not a fault, and doctor's
// agent line would call it one. s says what the sampler's records show,
// and focus is the app class of the newest os.focus.
func stallNote(at, fb, pending, ttl, late int64, s sampling, focus event.AppClass, undecided int64) string {
	lag := at - fb
	if lag <= pending+stallMS {
		return ""
	}
	msg := fmt.Sprintf("the scorer's newest final minute ends %s before its newest record, where %s is usual, so the minutes since stay pending",
		lasting(lag), lasting(pending))
	switch {
	case undecided > 0 && undecided <= fb:
		msg += fmt.Sprintf(": no presence sample has decided whether you were at the computer from then on, as none came within %s after those minutes, so they wait, and count as unknown once the newest record is %s past them",
			lasting(ttl), lasting(ttl+late))
	case s.newest == 0:
		msg += ": presence is unknown on this computer, so the scorer has no presence sample to keep time by"
	case at-s.newest > pending+stallMS:
		msg += fmt.Sprintf(": no presence sample has come for %s, so they wait for the next one, or for a record the agent stamps itself (a hook's, one of its own watchers', an agent app reader's) dated more than %s after the last one, and then any record %s after that",
			lasting(at-s.newest), lasting(ttl), lasting(late))
	case s.gapEnd > 0 && at-s.gapEnd < late:
		msg += fmt.Sprintf(": a presence sample came after %s without one, so the minutes since wait %s for a record dated in that gap",
			lasting(s.gap), lasting(late))
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
