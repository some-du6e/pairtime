// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"cmp"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/some-du6e/pairtime/pkg/event"
)

// Credit is what SM 2.7's rule gives the seconds of one 15-second slot
// (SM 1.3's credit_slot), within one project per slot: the seconds it
// credits by project, reason and level, the seconds it doesn't by reason
// (SM 5.2), and the seconds an agent ran. Every second of every derived
// slot is one or the other, so Credited and Uncredited add up to 15
// seconds. Choosing between projects is WP-SC3's (SM 3); here a slot's
// project is the one its focus resolves to.
type Credit struct {
	// Slot is the slot's start in Unix milliseconds.
	Slot     int64      `json:"slot"`
	Credited []Credited `json:"credited,omitempty"`
	// Uncredited holds the slot's seconds that nothing credited, by
	// reason.
	Uncredited map[event.Reason]int64 `json:"uncredited,omitempty"`
	// AgentS is how many of the slot's seconds an agent turn was running
	// in, in any project: SM 5.1's agent_running_s.
	AgentS int64 `json:"agent_s"`
	// AgentOnly holds, by project, the seconds a turn of the project was
	// running and the second wasn't credited to the project: SM 2.6's
	// agent-only time, which is never credited.
	AgentOnly map[string]int64 `json:"agent_only,omitempty"`
}

// Credited is a slot's seconds that one project earned under one reason,
// at one evidence level.
type Credited struct {
	ProjectKey string `json:"project_key"`
	// Reason is SM 5.2's credit reason, with event.ReturnedSuffix for a
	// second credited in a returned slot under SM 2.1's return rule.
	Reason event.Reason `json:"reason"`
	// Level is the evidence level of the anchor behind the seconds
	// (DESIGN 6.4): the anchor whose window covers them, the human anchor
	// nearest a reading second within READ_HORIZON_S, or the lower of a
	// lookup's two bracketing anchors.
	Level event.Evidence `json:"level"`
	// InTurn is set when a turn of the project was running in these
	// seconds, which SM 5.4 counts as AI-assisted whatever the reason.
	InTurn bool `json:"in_turn,omitempty"`
	// Seconds is how many seconds of the slot the entry covers, and Halves
	// the half-seconds of credit they earn after weighting: two a second at
	// weight 1. The weights of SM 4.2 and 4.3 are WP-SC6's, so every second
	// is at weight 1 for now.
	Seconds int64 `json:"seconds"`
	Halves  int64 `json:"halves"`
	// AnchorIDs are the event ids of the anchors behind the seconds, so a
	// reviewer can follow each second to the act that opened its window
	// (SM 2.7), and Labels their provenance labels (DESIGN 6.4).
	AnchorIDs []string      `json:"anchor_ids"`
	Labels    []event.Label `json:"labels,omitempty"`
}

// Event returns the entry as pkg/event's credit slot for the slot starting
// at slot.
func (c Credited) Event(slot int64) event.CreditSlot {
	w := 0.0
	switch {
	case c.Seconds > 0 && c.Halves == 2*c.Seconds:
		w = 1
	case c.Seconds > 0 && c.Halves == c.Seconds:
		w = 0.5
	}
	return event.CreditSlot{Slot: slot, ProjectKey: c.ProjectKey, CreditedS: float64(c.Halves) / 2, Weight: w,
		Reason: c.Reason, AnchorIDs: slices.Clone(c.AnchorIDs)}
}

// Halves returns the slot's credited half-seconds.
func (c Credit) Halves() int64 {
	var n int64
	for _, e := range c.Credited {
		n += e.Halves
	}
	return n
}

// The parts of an anchor's windows (SM 2.3).
const (
	partThink  = iota // the lead's fixed allowance, which needs presence only
	partTyping        // the lead's typing part, which needs key presses where the OS reports them
	partAfter         // a command's run and every trail
)

// window is one part of an anchor's windows, [from, to) on whole seconds:
// a second belongs to a window when its start does (SM 1.3: window edges
// are applied to the second).
type window struct {
	from, to int64
	anchor   int
	part     int
}

// second is what pass one and the lookups decided for one second.
type second struct {
	reason event.Reason // "" while uncredited
	proj   string
	level  event.Evidence
	anchor int32 // the anchor behind the credit
	other  int32 // a lookup's second bracketing anchor, or -1
	// label is the side-door label of what a drained second took from the
	// bucket, or "" (DESIGN 6.4).
	label event.Label
	// capped marks a present second of a lookup run past LOOKUP_MAX_S.
	capped bool
}

// secSpan is the half-open interval [from, to) of second indexes.
type secSpan struct{ from, to int }

// running holds the seconds the turns of one project ran, by who started
// them, and all of them together, each as sorted, disjoint spans. Spans,
// unlike an array per second, keep the cost of a project with a running
// turn to its turns, not to the length of the timeline.
type running struct {
	human, auto, noInput, any []secSpan
}

// covers reports whether one of spans, sorted and disjoint, holds second k.
func covers(spans []secSpan, k int) bool {
	i := sort.Search(len(spans), func(i int) bool { return spans[i].to > k })
	return i < len(spans) && spans[i].from <= k
}

// overlaps reports whether one of spans holds a second in [from, to).
func overlaps(spans []secSpan, from, to int) bool {
	i := sort.Search(len(spans), func(i int) bool { return spans[i].to > from })
	return i < len(spans) && spans[i].from < to
}

// merged returns spans sorted, with overlapping and touching ones joined.
func merged(spans []secSpan) []secSpan {
	slices.SortFunc(spans, func(a, b secSpan) int { return cmp.Compare(a.from, b.from) })
	var out []secSpan
	for _, sp := range spans {
		if n := len(out); n > 0 && sp.from <= out[n-1].to {
			out[n-1].to = max(out[n-1].to, sp.to)
			continue
		}
		out = append(out, sp)
	}
	return out
}

// returned is a Return as the credit pass reads it.
type returned struct {
	Return
	end    int64 // the end of the silence: RunEnd, or later inside the slot after it
	anchor int   // the index of the anchor that ended the run, or -1
}

// creditor applies SM 2.7 to every second of the derived slots.
type creditor struct {
	d     *deriver
	r     rules
	slots []Slot
	first int64 // the start of the first slot
	n     int   // seconds, a second's index counting from first
	// secs holds the state of the seconds of live slots only, and base,
	// per slot, the index in secs of its first second, or -1 for a dead
	// slot (deadSlot). Every second of a dead slot is X.absent, so it needs
	// no state, and the memory follows the samples rather than the span
	// the records cover.
	secs []second
	base []int32
	// byProject holds, per project, the indexes of its anchors in time
	// order, for READ_HORIZON_S.
	byProject map[string][]int
	// buckets holds, per project, its sessions' buckets in key order.
	buckets map[string][]*bucket
	runs    map[string]*running
	returns []returned
	// lookups are the browser and chat-app runs with credit in the
	// LOOKUP_BRACKET_S before them, the ones that can become lookups. Step
	// holds FinalBefore back for them until their end bracket is decided.
	lookups []span
}

// ceilSecond returns the start of the first whole second at or after t.
func ceilSecond(t int64) int64 { return -floorSecond(-t) }

// perSlot is the number of seconds in a slot.
const perSlot = int(event.SlotMS / 1000)

// deadSlot reports whether nothing can credit any second of slot s: it is a
// hard break, or the presence sampler wasn't running yet (SM 2.1). Every
// second there is X.absent.
func deadSlot(s *Slot) bool {
	return s.Asleep || s.Locked || s.Present == event.PresentUnknown
}

// credit runs SM 2.7 over the derived slots: anchor windows, then the
// reading bucket, then lookups (SM 2.5), then a reason for every second
// nothing credited (SM 5.2).
func (d *deriver) credit() {
	slots := d.pres.slots
	if len(slots) == 0 {
		return
	}
	c := &creditor{d: d, r: d.r, slots: slots, first: slots[0].Start, n: len(slots) * perSlot}
	c.base = make([]int32, len(slots))
	live := 0
	for i := range slots {
		c.base[i] = -1
		if !deadSlot(&slots[i]) {
			c.base[i] = int32(live * perSlot)
			live++
		}
	}
	c.secs = make([]second, live*perSlot)
	for k := range c.secs {
		c.secs[k].anchor, c.secs[k].other = -1, -1
	}
	c.index()
	c.anchorsAndBuckets()
	c.lookupRuns()
	d.credits = c.slotCredits()
	d.lookups = c.lookups
}

// state returns the state of second k, or nil if its slot is dead.
func (c *creditor) state(k int) *second {
	b := c.base[k/perSlot]
	if b < 0 {
		return nil
	}
	return &c.secs[int(b)+k%perSlot]
}

// at returns the time of second k.
func (c *creditor) at(k int) int64 { return c.first + int64(k)*1000 }

// sec returns the index of the second holding t, clamped to [0, n].
func (c *creditor) sec(t int64) int {
	k := (t - c.first) / 1000
	return int(max(0, min(k, int64(c.n))))
}

// index builds what the per-second pass looks up: anchors by project,
// buckets by project, running turns, and the returned runs.
func (c *creditor) index() {
	d := c.d
	c.byProject = map[string][]int{}
	for i, a := range d.anchors {
		c.byProject[a.ProjectKey] = append(c.byProject[a.ProjectKey], i)
	}
	c.buckets = map[string][]*bucket{}
	for _, k := range slices.Sorted(maps.Keys(d.buckets)) {
		b := d.buckets[k]
		c.buckets[b.sess.project] = append(c.buckets[b.sess.project], b)
	}
	c.runs = map[string]*running{}
	for _, tb := range d.turns {
		t := tb.turn
		from, to := c.sec(ceilSecond(t.TSubmit)), c.sec(ceilSecond(t.RunUntil))
		if from >= to {
			continue
		}
		r := c.runs[t.ProjectKey]
		if r == nil {
			r = &running{}
			c.runs[t.ProjectKey] = r
		}
		sp := secSpan{from, to}
		switch {
		case t.Origin.Human():
			r.human = append(r.human, sp)
		case t.Origin == event.OriginNoInput:
			r.noInput = append(r.noInput, sp)
		default:
			r.auto = append(r.auto, sp)
		}
		r.any = append(r.any, sp)
	}
	for _, r := range c.runs {
		r.human, r.auto, r.noInput, r.any = merged(r.human), merged(r.auto), merged(r.noInput), merged(r.any)
	}
	ids := map[string]int{}
	for i, a := range d.anchors {
		ids[a.ID] = i
	}
	for _, rt := range d.returns {
		x := returned{Return: rt, end: rt.RunEnd, anchor: -1}
		if i, ok := ids[rt.Anchor]; ok {
			x.anchor = i
		}
		if s := d.pres.slotAt(rt.RunEnd); s != nil && s.Present == event.PresentReturned && s.Then == event.PresentYes {
			x.end = s.PresentUntil
		}
		c.returns = append(c.returns, x)
	}
}

// returnAt returns the returned run whose silence holds t, or nil.
func (c *creditor) returnAt(t int64) *returned {
	i := sort.Search(len(c.returns), func(i int) bool { return c.returns[i].end > t })
	if i < len(c.returns) && c.returns[i].RunStart <= t {
		return &c.returns[i]
	}
	return nil
}

// windows returns every anchor's windows as whole seconds, by start. A
// window starts no earlier than the anchor can credit (Anchor.from).
func (c *creditor) windows() []window {
	var ws []window
	for i := range c.d.anchors {
		a := &c.d.anchors[i]
		lo := a.from(c.r)
		add := func(from, to int64, part int) {
			if from, to = ceilSecond(max(from, lo)), ceilSecond(to); from < to {
				ws = append(ws, window{from: from, to: to, anchor: i, part: part})
			}
		}
		typing := a.T - a.TypingMS
		add(a.T-a.LeadMS, typing, partThink)
		add(typing, a.T, partTyping)
		add(a.T, a.T+a.WatchMS, partAfter)
		add(a.TrailFrom, a.TrailFrom+a.TrailMS, partAfter)
	}
	slices.SortStableFunc(ws, func(x, y window) int { return cmp.Compare(x.from, y.from) })
	return ws
}

// better reports whether window x beats window y for a second they both
// cover (SM 3.3 rule 3): a lead beats a command's run or a trail; among
// leads, the next anchor at or after the second wins, since time just
// before an act is preparation for it; among runs and trails, the latest
// anchor; then the earlier anchor.
func (c *creditor) better(x, y window) bool {
	xl, yl := x.part != partAfter, y.part != partAfter
	if xl != yl {
		return xl
	}
	tx, ty := c.d.anchors[x.anchor].T, c.d.anchors[y.anchor].T
	switch {
	case tx == ty:
		return x.anchor < y.anchor
	case xl:
		return tx < ty
	}
	return tx > ty
}

// typingOK reports whether a second of an anchor's typing part is realized
// in slot s: where the OS reports key presses, only in a slot with one
// (SM 2.3, 2.7); elsewhere with presence alone, unless the anchor or
// COMPOSE_NEEDS_KEYS asks for key presses everywhere (DESIGN 5.4, 6.3).
func (c *creditor) typingOK(a *Anchor, s *Slot) bool {
	if s.InputKeys != nil {
		return *s.InputKeys
	}
	return !a.TypingNeedsKeys && !c.r.composeNeedsKeys
}

// nearest returns the anchor of project p nearest t within READ_HORIZON_S,
// the earlier on a tie, or -1: the human act that lets a second drain the
// bucket and gives it its level (SM 2.4, DESIGN 6.4). An anchor qualifies
// only seconds it can credit (Anchor.from).
func (c *creditor) nearest(p string, t int64) int {
	idx := c.byProject[p]
	as := c.d.anchors
	i := sort.Search(len(idx), func(i int) bool { return as[idx[i]].T >= t })
	best, dist := -1, c.r.readHorizon+1
	for j := i - 1; j >= 0 && t-as[idx[j]].T < dist; j-- {
		if as[idx[j]].from(c.r) <= t {
			best, dist = idx[j], t-as[idx[j]].T
			break
		}
	}
	for j := i; j < len(idx) && as[idx[j]].T-t < dist; j++ {
		if as[idx[j]].from(c.r) <= t {
			return idx[j]
		}
	}
	return best
}

// anchorsAndBuckets is pass one of SM 2.7: for each second, the anchor
// windows that cover it, then the reading bucket. An anchor's window is
// realized where the person is present (yes, bridged, tail) and the slot is
// focused on the anchor's project, or, for the anchor that ended a returned
// run, in its think part inside the run (SM 2.1). Anchor windows beat the
// bucket (SM 3.3 rule 2), and the bucket drains only where none is
// realized.
func (c *creditor) anchorsAndBuckets() {
	ws := c.windows()
	var active []int
	next := 0
	oneSecond := c.r.unitsPerS
	for k := 0; k < c.n; k++ {
		s := &c.slots[k/perSlot]
		if c.base[k/perSlot] < 0 {
			// A dead slot credits nothing. Windows and buckets are swept
			// by time, so skipping its seconds changes nothing after it.
			k += perSlot - 1
			continue
		}
		t := c.at(k)
		for ; next < len(ws) && ws[next].from <= t; next++ {
			active = append(active, next)
		}
		kept := active[:0]
		for _, wi := range active {
			if ws[wi].to > t {
				kept = append(kept, wi)
			}
		}
		active = kept
		pres := s.At(t)
		var rt *returned
		if pres == event.PresentReturned {
			rt = c.returnAt(t)
		}
		proj := s.FocusProject
		best, suffix := -1, event.Reason("")
		for _, wi := range active {
			w := ws[wi]
			a := &c.d.anchors[w.anchor]
			if w.part == partTyping && !c.typingOK(a, s) {
				continue
			}
			sfx := event.Reason("")
			switch {
			case present(pres) && proj != "" && proj == a.ProjectKey:
			case rt != nil && rt.anchor == w.anchor && w.part == partThink && t >= a.T-a.TypingMS-c.r.thinkLead:
				sfx = event.ReturnedSuffix
			default:
				continue
			}
			if best < 0 || c.better(w, ws[best]) {
				best, suffix = wi, sfx
			}
		}
		sec := c.state(k)
		if best >= 0 {
			a := &c.d.anchors[ws[best].anchor]
			sec.reason, sec.proj, sec.level, sec.anchor = a.Reason+suffix, a.ProjectKey, a.Level, int32(ws[best].anchor)
			continue
		}
		if proj == "" {
			continue
		}
		proseOnly := false
		switch {
		case present(pres):
		case rt != nil && t >= rt.TailEnd && t < rt.ReadUntil:
			// The reading part of the return credit: only prose drains.
			proseOnly = true
		default:
			continue
		}
		near := c.nearest(proj, t)
		if near < 0 {
			continue
		}
		var pick *bucket
		for _, b := range c.buckets[proj] {
			b.advance(c.r, t)
			if b.total(proseOnly) > 0 && (pick == nil || b.oldest(proseOnly) < pick.oldest(proseOnly)) {
				pick = b
			}
		}
		if pick == nil {
			continue
		}
		// SM 2.7 credits the second when the bucket holds anything and
		// drains up to a second from it.
		src := pick.drain(oneSecond, proseOnly)
		reason := event.ReasonRead
		if pick.runningAt(t) {
			reason = event.ReasonSupervise
		}
		if proseOnly {
			reason += event.ReturnedSuffix
		}
		// The second takes the level of the anchor that qualifies the drain
		// (DESIGN 6.4), but no better than the evidence behind what it
		// drained (docs/errata/2026-11-20-design-6.4-side-door-reading.md).
		a := &c.d.anchors[near]
		level := a.Level
		if src.level != "" {
			level = lowerLevel(level, src.level)
		}
		sec.reason, sec.proj, sec.level, sec.anchor, sec.label = reason, proj, level, int32(near), src.label
	}
}

// lookupRuns applies SM 2.5's lookup rule. A run of browser or chat-app
// slots counts as LOOKUP for project p when a second credited to p ends
// within LOOKUP_BRACKET_S before it, one credited to p starts within
// LOOKUP_BRACKET_S after it, and no turn of p runs during it. Its first
// LOOKUP_MAX_S present seconds are credited and the rest are X.lookup_cap.
// When the brackets fit more than one project, the lower project key wins
// (SM 3.3 rule 6).
func (c *creditor) lookupRuns() {
	for i := 0; i < len(c.slots); {
		if !lookupSlot(&c.slots[i]) {
			i++
			continue
		}
		j := i
		for j < len(c.slots) && lookupSlot(&c.slots[j]) {
			j++
		}
		from, to := i*perSlot, j*perSlot // seconds [from, to)
		i = j
		// A credited second ending LOOKUP_BRACKET_S before the run starts
		// still brackets it, as does one starting LOOKUP_BRACKET_S after it
		// ends.
		bracket := int(c.r.lookupBracket / 1000)
		before := c.brackets(from-1-bracket, from, true)
		if len(before) == 0 {
			// Nothing before the run brackets it, so it is no lookup
			// whatever comes after it, and Step needn't wait for its end.
			continue
		}
		c.lookups = append(c.lookups, span{c.at(from), c.at(to)})
		after := c.brackets(to, to+1+bracket, false)
		var proj string
		for _, p := range slices.Sorted(maps.Keys(before)) {
			if _, ok := after[p]; ok && !c.runsDuring(p, from, to) {
				proj = p
				break
			}
		}
		if proj == "" {
			continue
		}
		// The lookup takes the lower level of its brackets (DESIGN 6.4), as
		// the brackets were credited, so a bracket drained from side-door
		// output passes on its level C and its label.
		b1, b2 := before[proj], after[proj]
		level := lowerLevel(b1.level, b2.level)
		label := b1.label
		if label == "" {
			label = b2.label
		}
		left := c.r.lookupMax / 1000
		for k := from; k < to; k++ {
			sec := c.state(k)
			if sec == nil || !present(c.slots[k/perSlot].At(c.at(k))) {
				continue
			}
			if left == 0 {
				sec.capped = true
				continue
			}
			left--
			sec.reason, sec.proj, sec.level, sec.anchor, sec.other, sec.label = event.ReasonLookup, proj, level, b1.anchor,
				b2.anchor, label
		}
	}
}

// lookupSlot reports whether a slot can be part of a lookup run: a browser
// or chat app in front, and no hard break.
func lookupSlot(s *Slot) bool {
	return (s.FocusClass == event.AppBrowser || s.FocusClass == event.AppChatApp) && !s.Asleep && !s.Locked
}

// brackets returns, for each project credited in seconds [from, to), its
// credited second nearest the run: the last one when last is set, for the
// range before the run, else the first. Lookups don't bracket lookups.
func (c *creditor) brackets(from, to int, last bool) map[string]second {
	out := map[string]second{}
	for k := max(from, 0); k < min(to, c.n); k++ {
		s := c.state(k)
		if s == nil || s.reason == "" || s.reason == event.ReasonLookup {
			continue
		}
		if _, ok := out[s.proj]; !ok || last {
			out[s.proj] = *s
		}
	}
	return out
}

// runsDuring reports whether a turn of project p runs in seconds [from,
// to).
func (c *creditor) runsDuring(p string, from, to int) bool {
	r := c.runs[p]
	return r != nil && overlaps(r.any, from, to)
}

// lowerLevel returns the weaker of two evidence levels.
func lowerLevel(a, b event.Evidence) event.Evidence {
	if a > b { // "C" > "B" > "A"
		return a
	}
	return b
}

// uncredited gives a second nothing credited its reason (SM 5.2), the
// first that applies: X.absent without presence, X.lookup_cap past a
// lookup's cap, X.other_focus with focus outside every project's windows,
// X.ambiguous when the focus resolves to no single project, then, while a
// turn of the focused project runs, X.agent_only if a person started it,
// X.automated if automation did and X.no_input if its prompt came with no
// input, and X.no_anchor otherwise. This is the order
// docs/errata/2026-11-19-sm-6.6-uncredited-codes.md proposes.
func (c *creditor) uncredited(k int, s *Slot) event.Reason {
	if s.Asleep || s.Locked || !present(s.At(c.at(k))) {
		return event.ReasonAbsent
	}
	if sec := c.state(k); sec != nil && sec.capped {
		return event.ReasonLookupCap
	}
	switch s.FocusClass {
	case event.AppTerminal, event.AppEditor, event.AppAgentApp:
	default:
		return event.ReasonOtherFocus
	}
	if s.FocusProject == "" {
		return event.ReasonAmbiguous
	}
	if r := c.runs[s.FocusProject]; r != nil {
		switch {
		case covers(r.human, k):
			return event.ReasonAgentOnly
		case covers(r.auto, k):
			return event.ReasonAutomated
		case covers(r.noInput, k):
			return event.ReasonNoInput
		}
	}
	return event.ReasonNoAnchor
}

// creditKey groups a slot's credited seconds.
type creditKey struct {
	proj   string
	reason event.Reason
	level  event.Evidence
	inTurn bool
}

// projSpan is a span of seconds in which a turn of a project ran.
type projSpan struct {
	proj string
	secSpan
}

// slotCredits sums the seconds into one Credit per slot. It sweeps every
// project's running spans along the slots, so the agent time of a slot
// costs what runs in it, not the number of projects.
func (c *creditor) slotCredits() []Credit {
	out := make([]Credit, len(c.slots))
	var spans []projSpan
	for _, p := range slices.Sorted(maps.Keys(c.runs)) {
		for _, sp := range c.runs[p].any {
			spans = append(spans, projSpan{p, sp})
		}
	}
	slices.SortStableFunc(spans, func(a, b projSpan) int { return cmp.Compare(a.from, b.from) })
	var active []projSpan
	next := 0
	none := second{anchor: -1, other: -1} // a dead slot's seconds
	for i := range c.slots {
		s := &c.slots[i]
		lo, hi := i*perSlot, (i+1)*perSlot
		for ; next < len(spans) && spans[next].from < hi; next++ {
			active = append(active, spans[next])
		}
		kept := active[:0]
		for _, a := range active {
			if a.to > lo {
				kept = append(kept, a)
			}
		}
		active = kept
		cr := Credit{Slot: s.Start}
		group := map[creditKey]*Credited{}
		var keys []creditKey
		for k := lo; k < hi; k++ {
			sec := c.state(k)
			if sec == nil {
				sec = &none
			}
			agent, inTurn := false, false
			for _, a := range active {
				if k < a.from || k >= a.to {
					continue
				}
				agent = true
				if a.proj == sec.proj {
					inTurn = true
				} else if a.proj != "" {
					if cr.AgentOnly == nil {
						cr.AgentOnly = map[string]int64{}
					}
					cr.AgentOnly[a.proj]++
				}
			}
			if agent {
				cr.AgentS++
			}
			if sec.reason == "" {
				if cr.Uncredited == nil {
					cr.Uncredited = map[event.Reason]int64{}
				}
				cr.Uncredited[c.uncredited(k, s)]++
				continue
			}
			key := creditKey{proj: sec.proj, reason: sec.reason, level: sec.level, inTurn: inTurn}
			e := group[key]
			if e == nil {
				e = &Credited{ProjectKey: key.proj, Reason: key.reason, Level: key.level, InTurn: key.inTurn}
				group[key] = e
				keys = append(keys, key)
			}
			e.Seconds++
			e.Halves += 2
			for _, ai := range []int32{sec.anchor, sec.other} {
				if ai < 0 {
					continue
				}
				a := &c.d.anchors[ai]
				if !slices.Contains(e.AnchorIDs, a.ID) {
					e.AnchorIDs = append(e.AnchorIDs, a.ID)
				}
				for _, l := range a.Labels {
					if !slices.Contains(e.Labels, l) {
						e.Labels = append(e.Labels, l)
					}
				}
			}
			if sec.label != "" && !slices.Contains(e.Labels, sec.label) {
				e.Labels = append(e.Labels, sec.label)
			}
		}
		slices.SortFunc(keys, func(x, y creditKey) int {
			if c := strings.Compare(x.proj, y.proj); c != 0 {
				return c
			}
			if c := strings.Compare(string(x.reason), string(y.reason)); c != 0 {
				return c
			}
			if c := strings.Compare(string(x.level), string(y.level)); c != 0 {
				return c
			}
			switch {
			case x.inTurn == y.inTurn:
				return 0
			case x.inTurn:
				return 1
			}
			return -1
		})
		for _, key := range keys {
			e := group[key]
			slices.Sort(e.AnchorIDs)
			slices.Sort(e.Labels)
			cr.Credited = append(cr.Credited, *e)
		}
		out[i] = cr
	}
	return out
}
