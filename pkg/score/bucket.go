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
// near a human act drain it as SUP or READ (credit.go). In a session an
// agent app's server runs, relayed reading drains it after a stop too
// (relaycredit.go), and draws on at most UNKNOWN_BUCKET_MAX_S of it from
// the session's first fill on, whatever reset came last, while local
// reading there keeps BUCKET_MAX_S (SM 2.4, D-relayed). Amounts are exact:
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
	src   source
}

// source is the evidence behind a bucket entry, when it came through a side
// door (DESIGN 5.2): the best level a second drained from it can have, and
// the label it carries. The zero source is the harness's own records, which
// leave a drained second at the level of the anchor that qualifies it
// (DESIGN 6.4). See docs/errata/2026-11-20-design-6.4-side-door-reading.md.
type source struct {
	level event.Evidence
	label event.Label
}

// weaker returns the source a second drained from entries of both s and o
// has: the lower level, and the first label.
func (s source) weaker(o source) source {
	switch {
	case s.level == "":
		return o
	case o.level == "":
		return s
	}
	out := source{level: lowerLevel(s.level, o.level), label: s.label}
	if out.label == "" {
		out.label = o.label
	}
	return out
}

// bucketOp is one change to a session's bucket, in record order: a reset
// to a base, or a fill.
type bucketOp struct {
	t, pos int64
	reset  bool
	units  int64
	prose  bool
	src    source
}

// bucket is one session's reading bucket while the timeline is swept.
type bucket struct {
	sess    *session
	ops     []bucketOp
	next    int
	entries []entry
	// capped is set, from the first op on and through every reset, for the
	// bucket of a session an agent app's server runs (session.appRun),
	// whose relayed reading draws on at most UNKNOWN_BUCKET_MAX_S of it (SM
	// 2.4). Its entries then hold the newest UNKNOWN_BUCKET_MAX_S, the part
	// relayed reading drains (relayTotal, relayDrain), and held the older
	// rest, while the two together stay within BUCKET_MAX_S. Local reading
	// drains held, then entries, oldest first (total, oldest, drain): the
	// two in that order are what an uncapped bucket's entries would be, so
	// local reading reads, and BUCKET_MAX_S evicts, as without the cap. A
	// second of relayed reading that finds entries empty takes from held
	// instead, as X.bucket_cap, the code for reading that only the 120 s
	// cap kept back (SM 5.2).
	capped bool
	held   []entry
	// evicted is what BUCKET_MAX_S evicted that would still be in the
	// bucket without the cap: it expires and resets as the entries do, and
	// a second of local reading that finds the bucket empty takes from it
	// instead, as X.bucket_cap (SM 5.2).
	evicted []entry
	// spans are the times the session's turns ran, sorted and merged, for
	// SUP against READ, and cur indexes the first of them that hadn't ended
	// at the last time runningAt was asked about.
	spans []span
	cur   int
}

// advance applies every op at or before t and drops entries older than
// BUCKET_TTL_S at t.
func (b *bucket) advance(r rules, t int64) {
	for ; b.next < len(b.ops) && b.ops[b.next].t <= t; b.next++ {
		op := b.ops[b.next]
		b.expire(r, op.t)
		if op.reset {
			b.entries, b.held, b.evicted = b.entries[:0], b.held[:0], b.evicted[:0]
		}
		if op.units > 0 {
			b.entries = append(b.entries, entry{t: op.t, units: op.units, prose: op.prose, src: op.src})
		}
		// New output past BUCKET_MAX_S evicts the oldest entries, so the
		// bucket holds the most recent output. A capped bucket keeps its
		// newest UNKNOWN_BUCKET_MAX_S in entries and the older rest in
		// held, so BUCKET_MAX_S evicts from held first (SM 2.4).
		if b.capped {
			b.held = evict(&b.entries, b.held, unitsIn(b.entries)-r.units(r.unknownBucketMax))
			for _, es := range []*[]entry{&b.held, &b.entries} {
				b.evicted = evict(es, b.evicted, unitsIn(b.held)+unitsIn(b.entries)-r.units(r.bucketMax))
			}
		} else {
			b.evicted = evict(&b.entries, b.evicted, unitsIn(b.entries)-r.units(r.bucketMax))
		}
	}
	b.expire(r, t)
}

// unreadAt returns the units of the prose the harness's own hooks and logs
// put in b that b holds at t, once its ops up to t are applied, and that
// its ops after t and up to until put in it: SM 2.1's unread prose at a
// returned run's tail end (creditor.readingPart). It applies them to a
// copy of b, so the credit pass's own state stays as it was: the relayed
// reading of a slot's earlier seconds, which waits for the slot's end
// (creditor.relayedRest), sees b without them. A side door's prose has a
// source of its own, and a reset or a line fill is no prose, so neither
// counts.
func (b *bucket) unreadAt(r rules, t, until int64) int64 {
	cp := bucket{ops: b.ops, next: b.next, capped: b.capped, entries: slices.Clone(b.entries),
		held: slices.Clone(b.held), evicted: slices.Clone(b.evicted)}
	cp.advance(r, t)
	var u int64
	for _, es := range [][]entry{cp.held, cp.entries} {
		for _, e := range es {
			if e.prose && e.src == (source{}) {
				u = addUnits(u, e.units)
			}
		}
	}
	for _, op := range cp.ops[cp.next:] {
		if op.t > until {
			break
		}
		if op.prose && op.src == (source{}) {
			u = addUnits(u, op.units)
		}
	}
	return u
}

// evict moves the oldest over units of *from to the end of to, and returns
// to. What it moves keeps its fill time, its prose mark and its source, so
// local reading drains it from held as it would from the bucket. An over
// that isn't positive moves nothing.
func evict(from *[]entry, to []entry, over int64) []entry {
	es := *from
	for over > 0 && len(es) > 0 {
		take := min(over, es[0].units)
		es[0].units -= take
		over -= take
		moved := es[0]
		moved.units = take
		to = append(to, moved)
		if es[0].units == 0 {
			es = es[1:]
		}
	}
	*from = es
	return to
}

// unitsIn sums the units of es.
func unitsIn(es []entry) int64 {
	var n int64
	for _, e := range es {
		n = addUnits(n, e.units)
	}
	return n
}

func (b *bucket) expire(r rules, t int64) {
	for _, es := range []*[]entry{&b.entries, &b.held, &b.evicted} {
		i := 0
		for i < len(*es) && t-(*es)[i].t > r.bucketTTL {
			i++
		}
		*es = (*es)[i:]
	}
}

// takeCapped takes up to u units of what a cap kept from a reading second,
// oldest first, and reports whether there was any: for relayed reading,
// what only UNKNOWN_BUCKET_MAX_S held back, which BUCKET_MAX_S would still
// have let through; for local reading, what BUCKET_MAX_S evicted. A second
// the bucket would have credited but for a cap takes from them, as
// X.bucket_cap (SM 5.2).
func (b *bucket) takeCapped(u int64, relayed bool) bool {
	if relayed {
		return takeFrom(&b.held, u)
	}
	return takeFrom(&b.evicted, u)
}

// takeFrom takes up to u units from *es, oldest first, and reports whether
// it held any.
func takeFrom(es *[]entry, u int64) bool {
	if len(*es) == 0 {
		return false
	}
	for u > 0 && len(*es) > 0 {
		take := min(u, (*es)[0].units)
		(*es)[0].units -= take
		u -= take
		if (*es)[0].units == 0 {
			*es = (*es)[1:]
		}
	}
	return true
}

// total is what local reading can drain, held and entries, or only their
// prose.
func (b *bucket) total(proseOnly bool) int64 {
	var n int64
	for _, es := range [][]entry{b.held, b.entries} {
		for _, e := range es {
			if !proseOnly || e.prose {
				n += e.units
			}
		}
	}
	return n
}

// oldest returns the fill time of the oldest entry a local drain would
// take, for SM 3.3 rule 4's FIFO across sessions.
func (b *bucket) oldest(proseOnly bool) int64 {
	for _, es := range [][]entry{b.held, b.entries} {
		for _, e := range es {
			if !proseOnly || e.prose {
				return e.t
			}
		}
	}
	return never
}

// drain takes up to u units for local reading, oldest first: from held,
// then entries, or from their prose only. It returns the source of what it
// took.
func (b *bucket) drain(u int64, proseOnly bool) source {
	u, src := drainFrom(&b.held, u, proseOnly, source{})
	_, src = drainFrom(&b.entries, u, proseOnly, src)
	return src
}

// relayTotal is what relayed reading can drain: the entries, which in the
// capped bucket of a session an agent app's server runs are its newest
// UNKNOWN_BUCKET_MAX_S (SM 2.4).
func (b *bucket) relayTotal() int64 { return unitsIn(b.entries) }

// relayDrain takes up to u units for relayed reading, oldest first, from
// the entries alone, and returns the source of what it took.
func (b *bucket) relayDrain(u int64) source {
	_, src := drainFrom(&b.entries, u, false, source{})
	return src
}

// drainFrom takes up to u units from *es, oldest first, or from their
// prose only. It returns what is left of u, and src weakened by the source
// of what it took.
func drainFrom(es *[]entry, u int64, proseOnly bool, src source) (int64, source) {
	kept := (*es)[:0]
	for _, e := range *es {
		if u > 0 && (!proseOnly || e.prose) {
			take := min(u, e.units)
			e.units -= take
			u -= take
			src = src.weaker(e.src)
		}
		if e.units > 0 {
			kept = append(kept, e)
		}
	}
	*es = kept
	return u, src
}

// runningAt reports whether a turn of the session was running at t. The
// credit pass asks about seconds in order, so t never decreases from one
// call to the next, and each span is passed once.
func (b *bucket) runningAt(t int64) bool {
	for b.cur < len(b.spans) && b.spans[b.cur].to <= t {
		b.cur++
	}
	return b.cur < len(b.spans) && b.spans[b.cur].from <= t
}

// mergeSpans returns spans sorted, with overlapping and touching ones
// joined.
func mergeSpans(spans []span) []span {
	slices.SortFunc(spans, func(a, b span) int { return cmp.Compare(a.from, b.from) })
	var out []span
	for _, sp := range spans {
		if n := len(out); n > 0 && sp.from <= out[n-1].to {
			out[n-1].to = max(out[n-1].to, sp.to)
			continue
		}
		out = append(out, sp)
	}
	return out
}

// turnFill is the fill of one turn while the buckets are built.
type turnFill struct {
	fill Fill
	// opened is set for a turn a prompt opened. Side-door output fills no
	// other turn.
	opened bool
	base   int64 // units
	output int64 // units, uncapped
	filled int64 // units of output that went in
}

// add records output of u units for the turn and returns how much of it
// goes in the bucket. Output a plugin or the bridge reports, sideDoor, goes
// in only up to R_TURN_BASE_S for the turn, base included (DESIGN 5.2), and
// only in a turn a prompt opened: a side door can claim as many turn keys as
// it likes, and a turn per key would fill R_TURN_BASE_S each, up to
// BUCKET_MAX_S. buildBuckets drops side-door output wherever a hook or a
// transcript record covers the session, so what reaches here has nothing
// behind it. An amount that isn't positive adds nothing, so it can't lower
// what the turn has filled and with it the side-door cap.
func (f *turnFill) add(r rules, u int64, sideDoor bool) int64 {
	if u <= 0 {
		return 0
	}
	f.output = addUnits(f.output, u)
	if sideDoor {
		room := int64(0)
		if f.opened {
			room = max(0, r.units(r.turnBase)-f.base-f.filled)
		}
		u = min(u, room)
	}
	f.filled = addUnits(f.filled, u)
	return u
}

// buildBuckets collects every session's bucket ops (SM 2.4): a reset to
// R_TURN_BASE_S at each prompt a person submitted, a relayed one included;
// at each `pairtime track` anchor (DESIGN 5.4) and when the agent picks up
// a queued message; and a fill at each agent.output and agent.tool. Output
// from automated turns fills the bucket and resets nothing. What a plugin
// or the bridge reports fills a turn only up to R_TURN_BASE_S, base
// included, unless a hook or a transcript record backs the session (DESIGN
// 5.2). The bucket of a session an agent app's server runs is capped from
// its first op on, whatever resets it: its relayed reading draws on at
// most UNKNOWN_BUCKET_MAX_S (bucket.capped).
func (d *deriver) buildBuckets() {
	d.buckets = map[string]*bucket{}
	get := func(s *session) *bucket {
		b := d.buckets[s.key]
		if b == nil {
			b = &bucket{sess: s, capped: s.appRun()}
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
				Backed: tb.sess.native, decided: tb.turn.decided()}, opened: tb.primary >= 0}
			if tb.primary >= 0 {
				e := d.recs[tb.primary]
				f.fill.OpenedBy, f.fill.Via = e.EventID, e.Via
			}
			fills[tb] = f
		}
		return f
	}
	anchorOf := map[string]*Anchor{}
	for i := range d.anchors {
		anchorOf[d.anchors[i].ID] = &d.anchors[i]
	}
	// opener is the source of the base of a turn that the act behind anchor
	// a opened: none for the harness's own records, and for a side door the
	// level and label of a itself, so that its base drains no better than
	// the act it watches (docs/errata/2026-11-20-design-6.4-side-door-reading.md).
	opener := func(a *Anchor) source {
		if a == nil || !sideDoor(a.Via) {
			return source{}
		}
		return source{level: a.Level, label: sideDoorLabel(a.Via)}
	}
	for _, tb := range d.turns {
		b := get(tb.sess)
		if tb.turn.RunUntil > tb.turn.TSubmit {
			b.spans = append(b.spans, span{tb.turn.TSubmit, tb.turn.RunUntil})
		}
		if tb.primary < 0 || !tb.turn.Origin.ByPerson() {
			continue
		}
		// Each human prompt resets the bucket to its own base when its turn
		// starts: the person has moved on from older output. A relayed
		// prompt is a person's prompt for its own turn, so it resets the
		// bucket when the agent picks it up (SM 2.2, 2.4). Its anchor is its
		// relay.message, not the prompt, so its base has the zero source,
		// the harness's own records.
		f := fillOf(tb)
		f.base = base
		b.ops = append(b.ops, bucketOp{t: tb.turn.TSubmit, pos: int64(tb.primary), reset: true, units: base,
			src: opener(anchorOf[tb.turn.Prompt])})
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
			b.ops = append(b.ops, bucketOp{t: a.T, pos: pos, reset: true, units: base, src: opener(&a)})
			d.fills = append(d.fills, Fill{OpenedBy: a.ID, SessionKey: s.key, T: a.T, Via: a.Via,
				FillMS: d.r.unitsMS(base), decided: a.T})
		case a.Kind == event.AnchorQueuedMessage:
			// A queued message resets the bucket when the agent picks it
			// up (SM 2.4), and what the agent shows from then on answers
			// it. The records don't say when that is, so it is the
			// session's first agent record after the enqueue, the next
			// point at which an agent takes up queued input, just before
			// what that record shows. With none within rules.reach, which
			// no record may change what was derived beyond, it is the
			// enqueue itself.
			pos, _ := d.recIndex(a.ID)
			t := a.T
			if next := s.agentAfter(int(pos)); next >= 0 && d.recs[next].T-a.T <= d.r.reach {
				t, pos = d.recs[next].T, int64(next)
			}
			b := get(s)
			b.ops = append(b.ops, bucketOp{t: t, pos: pos, reset: true, units: base, src: opener(&a)})
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
			prose, lines = d.r.proseUnits(count(p.Words)), d.r.lineUnits(count(p.CodeLines))
		case *event.AgentTool:
			tb = s.runningAt(e.T)
			n := min(count(p.VisibleOutputLines), d.r.toolLinesMax)
			if isEdit(p) {
				n += addCount(count(p.LinesAdded), count(p.LinesRemoved))
			}
			lines = d.r.lineUnits(n)
		default:
			continue
		}
		if prose+lines == 0 {
			continue
		}
		side := sideDoor(e.Via)
		var src source
		if tb != nil {
			f := fillOf(tb)
			prose = f.add(d.r, prose, side)
			lines = f.add(d.r, lines, side)
		} else if side {
			// Side-door output outside any turn has no turn to fill.
			continue
		}
		if side {
			// Nothing but the side door's word backs what it reports, so a
			// second drained from it is level C and carries its label, even
			// when a hook anchor nearby qualifies the drain.
			src = source{level: event.EvidenceC, label: sideDoorLabel(e.Via)}
		}
		b := get(s)
		if prose > 0 {
			b.ops = append(b.ops, bucketOp{t: e.T, pos: int64(i), units: prose, prose: true, src: src})
		}
		if lines > 0 {
			b.ops = append(b.ops, bucketOp{t: e.T, pos: int64(i), units: lines, src: src})
		}
	}
	for _, b := range d.buckets {
		b.spans = mergeSpans(b.spans)
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

// appRun reports whether an agent app's server runs the session, as T3
// Code's runs Claude Code for the app's clients: a session.start of it has
// parent_class agent_app (SM 1.2, 2.2 rule 4). Only such a session's
// prompts can be relayed, and its relayed reading draws on at most
// UNKNOWN_BUCKET_MAX_S of its bucket (SM 2.4).
func (s *session) appRun() bool {
	for _, st := range s.starts {
		if st.d != nil && st.d.ParentClass == event.ParentAgentApp {
			return true
		}
	}
	return false
}

// turnByKey returns the session's turn with turn key k, or nil. A session
// has one turn per key (buildTurns).
func (s *session) turnByKey(k string) *turnBuild {
	if s.byKey == nil {
		s.byKey = make(map[string]*turnBuild, len(s.turns))
		for _, tb := range s.turns {
			s.byKey[tb.key] = tb
		}
	}
	return s.byKey[k]
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
