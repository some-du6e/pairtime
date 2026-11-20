// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"cmp"
	"slices"
	"strings"

	"github.com/some-du6e/pairtime/pkg/event"
)

// The reading bucket (SM 2.4). Each harness session has one: a FIFO queue
// of what the agent showed, in seconds of reading. Output fills it, a
// person's prompt resets it to R_TURN_BASE_S, and present, focused seconds
// near a human act drain it as SUP or READ (credit.go). Amounts are exact:
// a bucket counts in units of 1 / (READ_WPM * DIFF_LPM) of a second
// (rules.units).

// Fill is what one turn put in its session's reading bucket (SM 2.4), for
// review and for WP-FX4's side-door checks.
type Fill struct {
	// OpenedBy is the event_id of the prompt.submit or cli.track that
	// started the turn. A turn a stop hook continued has none.
	OpenedBy   string    `json:"opened_by,omitempty"`
	SessionKey string    `json:"session_key"`
	TurnKey    string    `json:"turn_key,omitempty"`
	T          int64     `json:"t"`
	Via        event.Via `json:"via,omitempty"`
	// Backed is set when a hook or a transcript record backs the turn: its
	// session reaches pairtime through the harness's own hooks or logs.
	Backed bool `json:"backed"`
	// OutputMS is what the turn's agent output would fill without DESIGN
	// 5.2's side-door cap, and FillMS what the turn filled, its base
	// included, before BUCKET_MAX_S evicted anything. Both are rounded to
	// the millisecond.
	OutputMS int64 `json:"output_ms"`
	FillMS   int64 `json:"fill_ms"`
	// decided is when the fill stops changing.
	decided int64
}

func fillCmp(a, b Fill) int {
	if c := cmp.Compare(a.T, b.T); c != 0 {
		return c
	}
	if c := strings.Compare(a.SessionKey, b.SessionKey); c != 0 {
		return c
	}
	if c := strings.Compare(a.TurnKey, b.TurnKey); c != 0 {
		return c
	}
	return strings.Compare(a.OpenedBy, b.OpenedBy)
}

// entry is one fill waiting in a bucket.
type entry struct {
	t     int64 // when it was filled
	units int64
	prose bool
}

// bucketOp is one change to a session's bucket, in record order: a reset
// to a base, or a fill.
type bucketOp struct {
	t, pos int64
	reset  bool
	units  int64
	prose  bool
}

// bucket is one session's reading bucket while the timeline is swept.
type bucket struct {
	sess    *session
	ops     []bucketOp
	next    int
	entries []entry
	// spans are the times the session's turns ran, in submit order, for SUP
	// against READ.
	spans []span
}

// advance applies every op at or before t and drops entries older than
// BUCKET_TTL_S at t.
func (b *bucket) advance(r rules, t int64) {
	for ; b.next < len(b.ops) && b.ops[b.next].t <= t; b.next++ {
		op := b.ops[b.next]
		b.expire(r, op.t)
		if op.reset {
			b.entries = b.entries[:0]
		}
		if op.units > 0 {
			b.entries = append(b.entries, entry{t: op.t, units: op.units, prose: op.prose})
		}
		// New output past BUCKET_MAX_S evicts the oldest entries, so the
		// bucket holds the most recent output.
		over := b.total(false) - r.units(r.bucketMax)
		for over > 0 && len(b.entries) > 0 {
			take := min(over, b.entries[0].units)
			b.entries[0].units -= take
			over -= take
			if b.entries[0].units == 0 {
				b.entries = b.entries[1:]
			}
		}
	}
	b.expire(r, t)
}

func (b *bucket) expire(r rules, t int64) {
	i := 0
	for i < len(b.entries) && t-b.entries[i].t > r.bucketTTL {
		i++
	}
	b.entries = b.entries[i:]
}

// total is what the bucket holds, or only its prose.
func (b *bucket) total(proseOnly bool) int64 {
	var n int64
	for _, e := range b.entries {
		if !proseOnly || e.prose {
			n += e.units
		}
	}
	return n
}

// oldest returns the fill time of the oldest entry a drain would take, for
// SM 3.3 rule 4's FIFO across sessions.
func (b *bucket) oldest(proseOnly bool) int64 {
	for _, e := range b.entries {
		if !proseOnly || e.prose {
			return e.t
		}
	}
	return never
}

// drain takes up to u units, oldest first, or from the prose only.
func (b *bucket) drain(u int64, proseOnly bool) {
	kept := b.entries[:0]
	for _, e := range b.entries {
		if u > 0 && (!proseOnly || e.prose) {
			take := min(u, e.units)
			e.units -= take
			u -= take
		}
		if e.units > 0 {
			kept = append(kept, e)
		}
	}
	b.entries = kept
}

// runningAt reports whether a turn of the session was running at t.
func (b *bucket) runningAt(t int64) bool {
	for _, sp := range b.spans {
		if sp.from > t {
			return false
		}
		if t < sp.to {
			return true
		}
	}
	return false
}

// turnFill is the fill of one turn while the buckets are built.
type turnFill struct {
	fill   Fill
	base   int64 // units
	output int64 // units, uncapped
	filled int64 // units of output that went in
}

// add records output of u units for the turn and returns how much of it
// goes in the bucket. Output a plugin or the bridge reports, sideDoor, goes
// in only up to R_TURN_BASE_S for the turn, base included (DESIGN 5.2):
// buildBuckets drops it wherever a hook or a transcript record covers the
// session, so what reaches here has nothing behind it.
func (f *turnFill) add(r rules, u int64, sideDoor bool) int64 {
	f.output += u
	if sideDoor {
		u = min(u, max(0, r.units(r.turnBase)-f.base-f.filled))
	}
	f.filled += u
	return u
}

// buildBuckets collects every session's bucket ops (SM 2.4): a reset to
// R_TURN_BASE_S at each prompt a person submitted, at each `pairtime track`
// anchor (DESIGN 5.4) and when the agent picks up a queued message; and a
// fill at each agent.output and agent.tool. Output from automated turns
// fills the bucket and resets nothing. What a plugin or the bridge reports
// fills a turn only up to R_TURN_BASE_S, base included, unless a hook or a
// transcript record backs the session (DESIGN 5.2).
func (d *deriver) buildBuckets() {
	d.buckets = map[string]*bucket{}
	get := func(s *session) *bucket {
		b := d.buckets[s.key]
		if b == nil {
			b = &bucket{sess: s}
			d.buckets[s.key] = b
		}
		return b
	}
	base := d.r.units(d.r.turnBase)
	fills := map[*turnBuild]*turnFill{}
	fillOf := func(tb *turnBuild) *turnFill {
		f := fills[tb]
		if f == nil {
			f = &turnFill{fill: Fill{SessionKey: tb.sess.key, TurnKey: tb.key, T: tb.turn.TSubmit,
				Backed: tb.sess.native, decided: tb.turn.decided()}}
			if tb.primary >= 0 {
				e := d.recs[tb.primary]
				f.fill.OpenedBy, f.fill.Via = e.EventID, e.Via
			}
			fills[tb] = f
		}
		return f
	}
	for _, tb := range d.turns {
		b := get(tb.sess)
		if tb.turn.RunUntil > tb.turn.TSubmit {
			b.spans = append(b.spans, span{tb.turn.TSubmit, tb.turn.RunUntil})
		}
		if tb.primary < 0 || !tb.turn.Origin.Human() {
			continue
		}
		// Each human prompt resets the bucket to its own base when its turn
		// starts: the person has moved on from older output.
		f := fillOf(tb)
		f.base = base
		b.ops = append(b.ops, bucketOp{t: tb.turn.TSubmit, pos: int64(tb.primary), reset: true, units: base})
	}
	for _, a := range d.anchors {
		s := d.sessions[a.SessionKey]
		if s == nil {
			continue
		}
		switch {
		case a.Via == event.ViaTrack:
			// A track anchor starts a turn no record shows the agent's work
			// on, so its fill is the base (DESIGN 5.4).
			pos, _ := d.recIndex(a.ID)
			b := get(s)
			b.ops = append(b.ops, bucketOp{t: a.T, pos: pos, reset: true, units: base})
			d.fills = append(d.fills, Fill{OpenedBy: a.ID, SessionKey: s.key, T: a.T, Via: a.Via,
				FillMS: d.r.unitsMS(base), decided: a.T})
		case a.Kind == event.AnchorQueuedMessage:
			// A queued message resets the bucket when the agent picks it
			// up. The records don't say when that is, so it is when the
			// turn running at the enqueue stops working, or the enqueue
			// itself if none was running.
			t := a.T
			if tb := s.runningAt(a.T); tb != nil && tb.turn.RunUntil > t {
				t = tb.turn.RunUntil
			}
			// At a later time it comes after every record of that
			// millisecond: output that arrived by then is older output.
			pos, _ := d.recIndex(a.ID)
			if t > a.T {
				pos = int64(len(d.recs))
			}
			b := get(s)
			b.ops = append(b.ops, bucketOp{t: t, pos: pos, reset: true, units: base})
		}
	}
	for i, e := range d.recs {
		s := d.sessions[e.SessionKey]
		if s == nil || (s.native && sideDoor(e.Via)) {
			// Where the harness's own paths cover a session, what a side
			// door reports about it is a cross-check.
			continue
		}
		var tb *turnBuild
		var prose, lines int64
		switch p := e.Data.(type) {
		case *event.AgentOutput:
			tb = s.turnByKey(p.TurnKey)
			prose, lines = d.r.proseUnits(int64(p.Words)), d.r.lineUnits(int64(p.CodeLines))
		case *event.AgentTool:
			tb = s.runningAt(e.T)
			n := min(int64(p.VisibleOutputLines), d.r.toolLinesMax)
			if isEdit(p) {
				n += int64(p.LinesAdded + p.LinesRemoved)
			}
			lines = d.r.lineUnits(n)
		default:
			continue
		}
		if prose+lines == 0 {
			continue
		}
		side := sideDoor(e.Via)
		if tb != nil {
			f := fillOf(tb)
			prose = f.add(d.r, prose, side)
			lines = f.add(d.r, lines, side)
		} else if side {
			// Side-door output outside any turn has no turn to fill.
			continue
		}
		b := get(s)
		if prose > 0 {
			b.ops = append(b.ops, bucketOp{t: e.T, pos: int64(i), units: prose, prose: true})
		}
		if lines > 0 {
			b.ops = append(b.ops, bucketOp{t: e.T, pos: int64(i), units: lines})
		}
	}
	for _, b := range d.buckets {
		slices.SortStableFunc(b.ops, func(x, y bucketOp) int {
			if c := cmp.Compare(x.t, y.t); c != 0 {
				return c
			}
			return cmp.Compare(x.pos, y.pos)
		})
	}
	for _, f := range fills {
		f.fill.OutputMS = d.r.unitsMS(f.output)
		f.fill.FillMS = d.r.unitsMS(f.base + f.filled)
		d.fills = append(d.fills, f.fill)
	}
	slices.SortFunc(d.fills, fillCmp)
}

// turnByKey returns the session's turn with turn key k, or nil.
func (s *session) turnByKey(k string) *turnBuild {
	for _, tb := range s.turns {
		if tb.key == k {
			return tb
		}
	}
	return nil
}

// recIndex returns the index of the record with event_id id.
func (d *deriver) recIndex(id string) (int64, bool) {
	if d.byID == nil {
		d.byID = make(map[string]int, len(d.recs))
		for i, e := range d.recs {
			d.byID[e.EventID] = i
		}
	}
	i, ok := d.byID[id]
	return int64(i), ok
}
