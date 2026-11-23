// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"cmp"
	"maps"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/some-du6e/pairtime/pkg/event"
)

// The relayed stage of SM 2.7 (D-relayed,
// docs/errata/2026-11-22-relayed-prompts.md). A relayed anchor (SM 2.2
// rule 9) credits its own lead and its own session's relayed reading, and
// nothing else (SM 2.3). Both come after every other candidate, and they
// are realized in a slot of any presence but one the machine slept
// through, whatever its focus or input says, because the person typed and
// read on another device (SM 2.1, 2.3, 2.4). Every second they credit is
// level C, under the anchor's labels, via_relay among them (SM 5.1).

// relaySession is a harness session with relayed anchors. The relayed
// stage's candidates are its anchors' leads and its relayed reading.
type relaySession struct {
	key string
	// project is the project its reading goes to: the session's.
	project string
	// b is its reading bucket, or nil.
	b *bucket
	// spans are its relayed reading, sorted and disjoint, and cur indexes
	// the first of them that hadn't ended at the last second asked about.
	spans []relaySpan
	cur   int
	// sent are its relayed anchors' stamps, sorted, for SM 2.7's rule
	// between two relayed sessions.
	sent []int64
}

// relaySpan is a stretch of SM 2.4's relayed reading, the whole seconds
// [from, to), and the index of the relayed anchor that vouches for it.
type relaySpan struct {
	from, to int64
	anchor   int
}

// relayLead is a relayed anchor's lead, its typing time ending at the
// app's stamp (SM 2.3), as the whole seconds [from, to), and the session
// of its anchor.
type relayLead struct {
	from, to int64
	anchor   int
	rs       *relaySession
}

// indexRelayed builds the relayed stage's candidates: each relayed
// anchor's lead, from the first second it can credit (Anchor.from), and
// each relayed session's reading (relaySpans).
func (c *creditor) indexRelayed() {
	d := c.d
	byKey := map[string]*relaySession{}
	// threads holds the threads of each session's relayed anchors, and
	// byThread each thread's relayed anchors by stamp, as d.anchors has
	// them: a relayed anchor stands at its stamp.
	threads := map[string]map[string]bool{}
	byThread := map[string][]int{}
	for i := range d.anchors {
		a := &d.anchors[i]
		if !a.relayed() {
			continue
		}
		rs := byKey[a.SessionKey]
		if rs == nil {
			rs = &relaySession{key: a.SessionKey, project: a.ProjectKey, b: d.buckets[a.SessionKey]}
			if s := d.sessions[a.SessionKey]; s != nil && s.project != "" {
				rs.project = s.project
			}
			byKey[a.SessionKey], threads[a.SessionKey] = rs, map[string]bool{}
			c.relays = append(c.relays, rs)
		}
		rs.sent = append(rs.sent, a.SentAt)
		threads[a.SessionKey][a.thread] = true
		byThread[a.thread] = append(byThread[a.thread], i)
		if from, to := ceilSecond(max(a.SentAt-a.LeadMS, a.from(c.r))), ceilSecond(a.SentAt); from < to {
			c.leads = append(c.leads, relayLead{from: from, to: to, anchor: i, rs: rs})
		}
	}
	slices.SortFunc(c.relays, func(x, y *relaySession) int { return strings.Compare(x.key, y.key) })
	slices.SortStableFunc(c.leads, func(x, y relayLead) int { return cmp.Compare(x.from, y.from) })
	for _, rs := range c.relays {
		slices.Sort(rs.sent)
		rs.spans = c.relaySpans(rs, slices.Sorted(maps.Keys(threads[rs.key])), byThread)
	}
}

// relaySpans returns a relayed session's reading (SM 2.4): from each of its
// stops until the next relayed anchor of one of its threads is sent or the
// session's next turn starts, whichever comes first. The next relayed
// anchor must be sent after the stop, within READ_HORIZON_S of it, and must
// pair, the later of its two records, within PENDING_WINDOW_S - 60 s of the
// stop, while the stop's minute is still pending (rules.relayVouch). It
// vouches for the reading. A message sent at or before the stop, as a
// queued one is, vouches for none of the reading after it. A relayed anchor
// that pairs later than the deadline keeps its own lead, but for that stop
// it is no next relayed anchor at all: it neither vouches nor ends the
// reading, so a pairing that comes when the stop's minute may already be
// final changes nothing about the reading after it.
//
// threads are the threads of the session's own relayed anchors, and
// byThread holds each thread's relayed anchors by stamp, in whichever
// session they are.
func (c *creditor) relaySpans(rs *relaySession, threads []string, byThread map[string][]int) []relaySpan {
	d := c.d
	s := d.sessions[rs.key]
	if s == nil || len(s.stops) == 0 {
		return nil
	}
	starts := make([]int64, 0, len(s.turns))
	for _, tb := range s.turns {
		starts = append(starts, tb.turn.TSubmit)
	}
	slices.Sort(starts)
	var out []relaySpan
	for _, st := range s.stops {
		stop := st.t
		v := -1
		for _, th := range threads {
			as := byThread[th]
			j := sort.Search(len(as), func(j int) bool { return d.anchors[as[j]].SentAt > stop })
			for ; j < len(as); j++ {
				a := &d.anchors[as[j]]
				if a.SentAt-stop > c.r.readHorizon {
					break
				}
				if a.paired-stop <= c.r.relayVouch {
					if v < 0 || anchorCmp(*a, d.anchors[v]) < 0 {
						v = as[j]
					}
					break
				}
			}
		}
		if v < 0 {
			continue
		}
		a := &d.anchors[v]
		end := a.SentAt
		if j, _ := slices.BinarySearch(starts, stop); j < len(starts) {
			end = min(end, starts[j])
		}
		from, to := ceilSecond(max(stop, a.from(c.r))), ceilSecond(end)
		switch n := len(out); {
		case from >= to:
		case n > 0 && from < out[n-1].to:
			// Two stops of one turn, as its hook and its transcript report
			// them, read the same output.
			out[n-1].to = max(out[n-1].to, to)
		default:
			out = append(out, relaySpan{from: from, to: to, anchor: v})
		}
	}
	return out
}

// relayLive marks the slots relayed credit reaches, a relayed lead or a
// relayed session's reading, but those the machine slept through: the
// credit pass keeps their seconds' state even where deadSlot says nothing
// else can credit them. It returns nil when there is no relayed credit.
func (c *creditor) relayLive() []bool {
	if len(c.relays) == 0 {
		return nil
	}
	out := make([]bool, len(c.slots))
	mark := func(from, to int64) {
		for k := max(0, (from-c.first)/event.SlotMS); k < int64(len(c.slots)) && c.first+k*event.SlotMS < to; k++ {
			if !c.slots[k].Asleep {
				out[k] = true
			}
		}
	}
	for _, l := range c.leads {
		mark(l.from, l.to)
	}
	for _, rs := range c.relays {
		for _, sp := range rs.spans {
			mark(sp.from, sp.to)
		}
	}
	return out
}

// enterRelaySlot gathers the relayed leads that reach into slot s, for the
// relayed candidates of its seconds (relayedSecond, relayedRest). Slots
// come in time order.
func (c *creditor) enterRelaySlot(s *Slot) {
	if len(c.leads) == 0 {
		return
	}
	for ; c.leadNext < len(c.leads) && c.leads[c.leadNext].from < s.End(); c.leadNext++ {
		c.leadsOpen = append(c.leadsOpen, c.leadNext)
	}
	kept := c.leadsOpen[:0]
	for _, li := range c.leadsOpen {
		if c.leads[li].to > s.Start {
			kept = append(kept, li)
		}
	}
	c.leadsOpen = kept
}

// spanAt returns the session's reading span that holds t, if one does. The
// credit pass asks about each session's seconds in time order, so t never
// decreases from one call to the next.
func (rs *relaySession) spanAt(t int64) (relaySpan, bool) {
	for rs.cur < len(rs.spans) && rs.spans[rs.cur].to <= t {
		rs.cur++
	}
	if rs.cur < len(rs.spans) && rs.spans[rs.cur].from <= t {
		return rs.spans[rs.cur], true
	}
	return relaySpan{}, false
}

// latest returns the stamp of the session's latest relayed anchor at or
// before t, or math.MinInt64 when it has none yet.
func (rs *relaySession) latest(t int64) int64 {
	i := sort.Search(len(rs.sent), func(i int) bool { return rs.sent[i] > t })
	if i == 0 {
		return math.MinInt64
	}
	return rs.sent[i-1]
}

// relayCand is what one relayed session wants of a second: the lead of
// one of its anchors, or, with none, its relayed reading. proj is the
// project the second would go to.
type relayCand struct {
	rs   *relaySession
	lead int // the anchor whose lead holds the second, or -1
	span relaySpan
	proj string
}

// relayWant returns what relayed session rs wants of the second at t in a
// slot resolved as at, in the pass now names (attribution.relayNow): of its
// anchors' leads that hold t, the one whose anchor is sent soonest after
// it, since a lead beats reading and the next anchor's lead beats a later
// one's (SM 3.3 rules 2 and 3); else, in one of its reading spans, its
// bucket's reading, when the bucket holds anything at t. A candidate of a
// project the slot doesn't admit wants nothing (attribution.relayAdmits).
func (c *creditor) relayWant(rs *relaySession, t int64, at *attribution, now bool) (relayCand, bool) {
	x := relayCand{rs: rs, lead: -1}
	for _, li := range c.leadsOpen {
		l := &c.leads[li]
		if l.rs != rs || t < l.from || t >= l.to {
			continue
		}
		a := &c.d.anchors[l.anchor]
		if !at.relayAdmits(a.ProjectKey, now) {
			continue
		}
		if x.lead < 0 || anchorCmp(*a, c.d.anchors[x.lead]) < 0 {
			x.lead, x.proj = l.anchor, a.ProjectKey
		}
	}
	if x.lead >= 0 {
		return x, true
	}
	if rs.b == nil || !at.relayAdmits(rs.project, now) {
		return x, false
	}
	sp, ok := rs.spanAt(t)
	if !ok {
		return x, false
	}
	rs.b.advance(c.r, t)
	if rs.b.total(false) == 0 {
		return x, false
	}
	x.span, x.proj = sp, rs.project
	return x, true
}

// relayBefore reports whether relayed candidate x takes the second at t
// from y, a candidate of another session (SM 2.7): the session whose next
// relayed anchor's lead holds the second; when both do, the one whose
// anchor is sent soonest after it; when neither does, the one whose latest
// relayed anchor is the most recent, a session with none yet counting as
// the oldest; then the lower project key, and the lower session key.
func (c *creditor) relayBefore(x, y relayCand, t int64) bool {
	xl, yl := x.lead >= 0, y.lead >= 0
	switch {
	case xl != yl:
		return xl
	case xl:
		if a, b := c.d.anchors[x.lead].SentAt, c.d.anchors[y.lead].SentAt; a != b {
			return a < b
		}
	default:
		if a, b := x.rs.latest(t), y.rs.latest(t); a != b {
			return a > b
		}
	}
	if x.proj != y.proj {
		return x.proj < y.proj
	}
	return x.rs.key < y.rs.key
}

// relayedSecond weighs the relayed candidates of second k, at t, which
// nothing else credited, in a slot resolved as at and in the pass now
// names (attribution.relayNow), and credits the winner (relayBefore): its
// anchor's lead, under its anchor's reason, or its session's relayed
// reading, as READ, never SUP, draining up to a second from the bucket
// (SM 2.4). Either is level C, and the anchor behind it, the lead's own or
// the one that vouches for the reading, gives its labels, via_relay among
// them. With no winner, the second is X.bucket_cap if it is in the reading
// of a session whose bucket only UNKNOWN_BUCKET_MAX_S emptied, and it
// takes a second of what that cap held back from each such session, as
// relayed.py's Replay does (SM 5.2, D-relayed's reading 3).
func (c *creditor) relayedSecond(k int, t int64, at *attribution, now bool) {
	var best relayCand
	found := false
	for _, rs := range c.relays {
		if x, ok := c.relayWant(rs, t, at, now); ok && (!found || c.relayBefore(x, best, t)) {
			best, found = x, true
		}
	}
	sec := c.state(k)
	if !found {
		for _, rs := range c.relays {
			if rs.b == nil || !at.relayAdmits(rs.project, now) {
				continue
			}
			if _, ok := rs.spanAt(t); ok && rs.b.total(false) == 0 && rs.b.takeCapped(c.r.unitsPerS, true) {
				sec.bucketCap = true
			}
		}
		return
	}
	if best.lead >= 0 {
		a := &c.d.anchors[best.lead]
		sec.reason, sec.proj, sec.level, sec.anchor = a.Reason, a.ProjectKey, a.Level, int32(best.lead)
	} else {
		src := best.rs.b.drain(c.r.unitsPerS, false)
		sec.reason, sec.proj, sec.level, sec.anchor, sec.label = event.ReasonRead, best.proj, event.EvidenceC,
			int32(best.span.anchor), src.label
	}
	sec.relayed = true
	if at.won == "" {
		at.won = sec.proj
	}
}

// relayedRest weighs, once every second of slot i has had its other
// candidates, the relayed candidates that wait for the slot's end
// (attribution.relayNow): those of a project other than the one the slot
// resolves to, and all of them in an ambiguous slot. They take, in order,
// the seconds nothing else credited, and only in the project of whatever
// credited a second of the slot first.
func (c *creditor) relayedRest(i int, at *attribution) {
	if len(c.relays) == 0 || (at.proj == "" && !at.ambiguous) {
		return
	}
	for k := i * perSlot; k < (i+1)*perSlot; k++ {
		if sec := c.state(k); sec != nil && sec.reason == "" {
			c.relayedSecond(k, c.at(k), at, false)
		}
	}
}
