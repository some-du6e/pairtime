// SPDX-License-Identifier: AGPL-3.0-or-later

package scorer

import (
	"cmp"
	"maps"
	"slices"
	"strings"

	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/params"
)

// held is a record in the window, with the batch it came in.
type held struct {
	ev event.Event
	b  uint64
}

// window is what the loop gives pkg/score at each Step: every record whose
// t is from or later, and the carry, which stands for the records before
// from (see the package doc).
type window struct {
	from  int64
	recs  []held // in the order they came in
	carry carry
	gaps  runGaps
	// ttl is BUCKET_TTL_S, how long after a record of the presence
	// sampler's another record the agent stamps itself may come before the
	// sampler's record has no reach left (stamps, cutBy), and late is
	// PENDING_WINDOW_S - READ_HORIZON_S, pkg/score's rules.late, how long
	// after such a record the newest record must be before the cut takes
	// effect.
	ttl, late int64
}

// runGaps are the longest silences of its agent's records that pkg/score
// lets a turn's run last through (turnBuild.runUntil): native in a session
// that a hook or a log reader covers, BUCKET_TTL_S, and other anywhere
// else, the scorer's reach, RETURN_GAP_MAX_S + READ_HORIZON_S (rules.reach).
// The first longer silence ends the run for good.
type runGaps struct{ native, other int64 }

func gapsOf(t *params.Table) runGaps {
	return runGaps{native: t.Int(params.BUCKET_TTL_S) * 1000,
		other: (t.Int(params.RETURN_GAP_MAX_S) + t.Int(params.READ_HORIZON_S)) * 1000}
}

// of returns the gap of a session that hooks or logs cover, or of another.
func (g runGaps) of(native bool) int64 {
	if native {
		return g.native
	}
	return g.other
}

// add puts ev, which came in batch b, in the window, or in the carry when
// it is dated before the window.
func (w *window) add(ev event.Event, b uint64) {
	if ev.T < w.from {
		w.carry.fold(ev, w.gaps, w.ttl)
		w.carry.prune(w.from)
		return
	}
	w.recs = append(w.recs, held{ev: ev, b: b})
}

// slide moves the window's start to from, folding the records before it
// into the carry, in record order, as a turn's run reads them. A start
// before the current one changes nothing.
func (w *window) slide(from int64) {
	if from <= w.from {
		return
	}
	kept := make([]held, 0, len(w.recs))
	var out []event.Event
	for _, h := range w.recs {
		if h.ev.T < from {
			out = append(out, h.ev)
		} else {
			kept = append(kept, h)
		}
	}
	slices.SortFunc(out, func(a, b event.Event) int { return recordCmp(&a, &b) })
	for _, e := range out {
		w.carry.fold(e, w.gaps, w.ttl)
	}
	w.recs = kept
	w.from = from
	w.carry.prune(from)
}

// carryGraceMS bounds how long before the window a carried record is
// dated. pkg/score derives every slot from its earliest record on, so a
// focus record from three days ago would have every Step derive three
// days. A record older than that is scored as if it came carryGraceMS
// before the window, keeping its place among the others by seq: what it
// says holds until a later record changes it, and no rule derives a
// second at or after FinalBefore from a record's time that far back. A
// turn's run is the exception, which carry.records dates on its own.
const carryGraceMS = 3600 * 1000

// records returns what a Step scores: the carry's records, then the
// window's.
func (w *window) records() []event.Event {
	out := w.carry.records(make([]event.Event, 0, len(w.recs)+16), w.from, w.gaps, w.cutBy(), w.ttl)
	for _, h := range w.recs {
		out = append(out, h.ev)
	}
	return out
}

// cutBy returns the t of the latest record the agent stamped itself
// (stamps) that comes after the carry's records of the presence sampler's
// and before the sampler's next record, and whose cut has taken effect:
// the carry's (carry.Stamped), and the window's that come before the
// window's first record of the sampler's in record order and are late,
// PENDING_WINDOW_S - READ_HORIZON_S, before the newest. pkg/score ends a
// record's reach only for such a record dated more than BUCKET_TTL_S after
// it, and only once the newest record is late past it (sampledReaches),
// so a silence of the sampler's longer than Lookback that ends with a
// sample, or a suspend that long, keeps the carried record's reach, and so
// does a cut still waiting, as in one Step over every record. A stamped
// record after the window's first record of the sampler's can end only
// that one's reach or a later one's, which the window holds itself.
func (w *window) cutBy() int64 {
	if len(w.recs) == 0 {
		return w.carry.Stamped
	}
	var first *event.Event
	high := w.recs[0].ev.T
	for i := range w.recs {
		e := &w.recs[i].ev
		high = max(high, e.T)
		if sampler(e) && (first == nil || recordCmp(e, first) < 0) {
			first = e
		}
	}
	var at int64
	if w.carry.Stamped <= high-w.late {
		at = w.carry.Stamped
	}
	for i := range w.recs {
		if e := &w.recs[i].ev; stamps(e) && e.T <= high-w.late && (first == nil || recordCmp(e, first) < 0) {
			at = max(at, e.T)
		}
	}
	return at
}

// sampler reports whether e is a record of the presence sampler's, an
// os.presence or an os.power, as pkg/score's samplerRecord does.
func sampler(e *event.Event) bool {
	switch e.Data.(type) {
	case *event.OSPresence, *event.OSPower:
		return true
	}
	return false
}

// stamps reports whether the agent stamped e's t itself, by a hook, its own
// watchers or an agent app's reader, and e isn't a record of the presence
// sampler's (sampler): the records that, dated more than BUCKET_TTL_S
// after one of the sampler's and before its next, end that record's reach,
// as pkg/score reads them (stampedRecord, sampledReaches; SM 2.1). A log
// reader's or the bridge's times come from transcripts and heartbeats.
func stamps(e *event.Event) bool {
	if sampler(e) {
		return false
	}
	switch e.Via {
	case event.ViaHook, event.ViaOS, event.ViaApp:
		return true
	}
	return false
}

// firstBatch returns the lowest batch number of the window's records, or
// 0 when it holds none.
func (w *window) firstBatch() uint64 {
	var first uint64
	for _, h := range w.recs {
		if first == 0 || h.b < first {
			first = h.b
		}
	}
	return first
}

// carry keeps, of the records before the window, the ones a rule can
// still read after any time span: what holds until a later record of the
// same kind says otherwise. Those are the last focus, power and presence
// records, and the last power record that locked or unlocked the screen,
// which say what was in front, whether the screen was locked and when the
// last input came (SM 2.1, 3.2), and for each harness session its
// start, which says how the session runs (SM 2.2), and its last prompt,
// agent output and stop, which say which turn runs and whether the agent
// asked a question (SM 2.2, 2.6). Each slot keeps the latest record in
// record order (t, then seq, then event id), so folding records in any
// order gives the same carry.
//
// It also keeps what the run of each session's last prompted turn needs,
// since a run has no bound in time (SM 2.6): the turn's latest agent
// record, whether a silence already ended the run, and whether the turn
// stopped. That part follows the records in record order, which the loop
// folds them in.
type carry struct {
	Focus    *event.Event             `json:"focus,omitempty"`
	Power    *event.Event             `json:"power,omitempty"`
	Presence *event.Event             `json:"presence,omitempty"`
	Sessions map[string]*sessionCarry `json:"sessions,omitempty"`
	// Lock is the latest os.power that locked or unlocked the screen. Power
	// keeps only the latest os.power, so a suspend or a resume after a lock
	// with no sample, as a lid close writes, would drop the lock, and a
	// Step over the window would read the slots after the resume unlocked
	// where one Step over every record keeps them locked until the next
	// sample or unlock (SM 2.1). records drops it as it drops Power.
	Lock *event.Event `json:"lock,omitempty"`
	// Stamped is the t of the latest record folded in that the agent
	// stamped itself and isn't the sampler's (stamps). Dated more than
	// BUCKET_TTL_S after the later of the sampler's records the carry
	// holds, Power and Presence, it comes after that one and before the
	// window, so before the sampler's next record, and it ends that one's
	// reach (window.cutBy).
	Stamped int64 `json:"stamped,omitempty"`
	// RestartAt is the t of the sampler's record that started the device
	// sampling again after the latest stretch the carry folded in whose
	// sample's reach a stamped record ended, 0 for none: from it on, and
	// until the next os.presence, pkg/score keeps no gap, lock or suspend
	// from before the stretch (SM 2.1). The restart can be two records, as
	// when the agent writes a suspend and a resume with no sample after
	// them, so it sticks until records dated after it replace the ones
	// before it: carry.records drops every Power, Presence or Lock dated
	// before it, as the window's Steps would otherwise get them back.
	RestartAt int64 `json:"restart_at,omitempty"`
	// V is the carry's format, carryVersion today: 0 in a place an agent
	// before WP-HL1 saved, whose carry has no Stamped, RestartAt or Lock,
	// and 1 in one an agent between WP-HL1's first part and
	// D-sampleless-power saved, whose carry has no Lock
	// (Loop.rebuildCarry).
	V int `json:"v,omitempty"`
}

// carryVersion is the carry's format: since WP-HL1 it keeps Stamped and
// RestartAt, and since D-sampleless-power Lock. Loaded from a place an
// older agent saved, a carry without them reads as if no record the agent
// stamps itself, no restart and no lock came before the window, which
// keeps a sample whose reach a folded record ended, or drops a lock still
// open, so open rebuilds them from the ledger first (Loop.rebuildCarry).
const carryVersion = 2

// mark is a record's place in record order, for folding RestartAt again
// (restartAt): its t and seq, which one device's ledger never repeats, and
// whether it is the sampler's record or one the agent stamped itself.
type mark struct {
	t       int64
	seq     uint64
	sampler bool
}

// restartAt sorts marks into record order and returns RestartAt as fold
// leaves it after folding the records they stand for: the t of the last of
// the sampler's records that came after a stamped record dated more than
// ttl after the sampler's record before it, or 0 for none. A restart at the
// first of the sampler's records in marks isn't seen, as the one before it
// isn't there.
func restartAt(marks []mark, ttl int64) int64 {
	slices.SortFunc(marks, func(a, b mark) int { return cmp.Or(cmp.Compare(a.t, b.t), cmp.Compare(a.seq, b.seq)) })
	var at, stamped int64
	var last *mark // the sampler's latest record so far, as carry.sampled
	for i := range marks {
		m := &marks[i]
		if !m.sampler {
			stamped = max(stamped, m.t)
			continue
		}
		if last != nil && stamped > last.t+ttl {
			at = m.t
		}
		last = m
	}
	return at
}

// sessionCarry is what the carry keeps of one harness session.
type sessionCarry struct {
	Start  *event.Event `json:"start,omitempty"`
	Prompt *event.Event `json:"prompt,omitempty"`
	Output *event.Event `json:"output,omitempty"`
	Stop   *event.Event `json:"stop,omitempty"`
	End    *event.Event `json:"end,omitempty"`
	// Agent is the latest agent record of the turn Prompt started, of any
	// kind: an output, a tool call or a stop. pkg/score runs a turn on from
	// its prompt while its agent's records keep coming, and ends the run
	// for good at the first silence longer than its gap (runGaps). Cut is
	// set once such a silence came, and Stopped once the turn's stop did.
	// Native is set once a record of the session came through a hook or a
	// log reader, which gives its turns the longer gap.
	Agent   *event.Event `json:"agent,omitempty"`
	Cut     bool         `json:"cut,omitempty"`
	Stopped bool         `json:"stopped,omitempty"`
	Native  bool         `json:"native,omitempty"`
	// Last is the t of the session's latest record folded in.
	Last int64 `json:"last"`
}

// carrySessions bounds how many sessions the carry keeps, and
// carrySessionMS how long it keeps one that has had no record. A session
// left open longer than a day without a record starts again from what the
// window holds.
const (
	carrySessions  = 64
	carrySessionMS = 24 * 3600 * 1000
)

// recordCmp orders records as pkg/score does: by t, then seq, then event
// id.
func recordCmp(a, b *event.Event) int {
	if c := cmp.Compare(a.T, b.T); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Seq, b.Seq); c != 0 {
		return c
	}
	return strings.Compare(a.EventID, b.EventID)
}

// sampled returns the later of the carry's records of the sampler's, Power
// and Presence, in record order, or nil for neither.
func (c *carry) sampled() *event.Event {
	switch {
	case c.Power == nil:
		return c.Presence
	case c.Presence == nil || recordCmp(c.Presence, c.Power) < 0:
		return c.Power
	}
	return c.Presence
}

// locks reports whether p locks or unlocks the screen.
func locks(p *event.OSPower) bool {
	return p != nil && (p.Op == event.PowerLock || p.Op == event.PowerUnlock)
}

// latest returns whichever of cur and ev comes later in record order.
func latest(cur *event.Event, ev event.Event) *event.Event {
	if cur != nil && recordCmp(cur, &ev) >= 0 {
		return cur
	}
	return &ev
}

// turnKey returns the turn key of a prompt.submit record.
func turnKey(e *event.Event) string {
	if p, ok := e.Data.(*event.PromptSubmit); ok && p != nil {
		return p.TurnKey
	}
	return ""
}

// fold takes ev, a record before the window, into the carry. g are the
// runs' gaps, and ttl is BUCKET_TTL_S.
func (c *carry) fold(ev event.Event, g runGaps, ttl int64) {
	if sampler(&ev) {
		if prev := c.sampled(); prev != nil && recordCmp(prev, &ev) < 0 && c.Stamped > prev.T+ttl {
			c.RestartAt = ev.T
		}
	}
	if stamps(&ev) {
		c.Stamped = max(c.Stamped, ev.T)
	}
	switch d := ev.Data.(type) {
	case *event.OSFocus:
		c.Focus = latest(c.Focus, ev)
	case *event.OSPower:
		c.Power = latest(c.Power, ev)
		if locks(d) {
			c.Lock = latest(c.Lock, ev)
		}
	case *event.OSPresence:
		c.Presence = latest(c.Presence, ev)
	}
	if ev.SessionKey == "" || ev.SessionKey == event.NoSession {
		return
	}
	if c.Sessions == nil {
		c.Sessions = make(map[string]*sessionCarry)
	}
	s := c.Sessions[ev.SessionKey]
	if s == nil {
		s = &sessionCarry{}
		c.Sessions[ev.SessionKey] = s
	}
	s.Last = max(s.Last, ev.T)
	if ev.Via == event.ViaHook || ev.Via == event.ViaLog {
		s.Native = true
	}
	switch p := ev.Data.(type) {
	case *event.SessionStart:
		s.Start = latest(s.Start, ev)
	case *event.PromptSubmit:
		if s.Prompt == nil || recordCmp(&ev, s.Prompt) > 0 {
			if s.Prompt == nil || turnKey(s.Prompt) != p.TurnKey {
				// A new turn, whose run starts at its prompt.
				s.Agent, s.Cut, s.Stopped = nil, false, false
			}
			s.Prompt = latest(nil, ev)
		}
	case *event.AgentOutput:
		s.Output = latest(s.Output, ev)
		s.ran(ev, p.TurnKey, false, g)
	case *event.AgentTool:
		s.ran(ev, "", true, g)
	case *event.AgentStop:
		s.Stop = latest(s.Stop, ev)
		s.ran(ev, p.TurnKey, false, g)
	case *event.SessionEnd:
		s.End = latest(s.End, ev)
	}
}

// ran folds ev, an agent record of the session, into the run of the turn
// Prompt started, as turnBuild.runUntil reads a run: from the prompt on,
// a record within the gap of the one before carries the run on, and the
// first one past it ends the run for good. A tool call belongs to the turn
// running at its time, Prompt's until its stop; an output or a stop to its
// own turn key. The turn's stop ends the turn, after a cut too. Records
// after the stop don't count, and a record folded out of turn, earlier
// than the run's latest, changes nothing.
func (s *sessionCarry) ran(ev event.Event, key string, tool bool, g runGaps) {
	if s.Prompt == nil || s.Stopped || recordCmp(&ev, s.Prompt) < 0 || (!tool && key != turnKey(s.Prompt)) {
		return
	}
	if !s.Cut {
		last := s.Prompt.T
		if s.Agent != nil {
			last = s.Agent.T
		}
		if ev.T-last > g.of(s.Native) {
			s.Cut = true
		} else {
			s.Agent = latest(s.Agent, ev)
		}
	}
	if _, stop := ev.Data.(*event.AgentStop); stop {
		s.Stopped = true
	}
}

// running reports whether the turn Prompt started may still run: it hasn't
// stopped, and the session hasn't ended after it.
func (s *sessionCarry) running() bool {
	return s.Prompt != nil && !s.Stopped && (s.End == nil || recordCmp(s.End, s.Prompt) < 0)
}

// events returns the records the carry keeps of the session, each once.
func (s *sessionCarry) events() []*event.Event {
	var out []*event.Event
	for _, e := range []*event.Event{s.Start, s.Prompt, s.Output, s.Stop, s.End, s.Agent} {
		if e != nil && !slices.ContainsFunc(out, func(o *event.Event) bool { return o.EventID == e.EventID }) {
			out = append(out, e)
		}
	}
	return out
}

// prune drops the sessions no later record can need: one whose end is its
// latest record, one with no record for carrySessionMS before from, and
// past carrySessions, the ones whose latest record is oldest.
func (c *carry) prune(from int64) {
	for k, s := range c.Sessions {
		if (s.End != nil && s.End.T >= s.Last) || s.Last < from-carrySessionMS {
			delete(c.Sessions, k)
		}
	}
	if len(c.Sessions) <= carrySessions {
		return
	}
	keys := slices.Collect(maps.Keys(c.Sessions))
	slices.SortFunc(keys, func(a, b string) int {
		if x := cmp.Compare(c.Sessions[a].Last, c.Sessions[b].Last); x != 0 {
			return x
		}
		return strings.Compare(a, b)
	})
	for _, k := range keys[:len(keys)-carrySessions] {
		delete(c.Sessions, k)
	}
}

// records appends the carry's records to out, in a fixed order, as a Step
// with the window starting at from scores them. Most are dated no earlier
// than carryGraceMS before from. A session whose last prompted turn may
// still run is dated so that a new State gives its run what one holding
// every record would at and after from (turnBuild.runUntil):
//
//   - A run that went on into the window keeps its latest agent record's
//     time, and the session's other records go no later than that record
//     and no more than the run's gap before it, so the run carries on from
//     that record into the window, and its first record there ends it when
//     it comes after a longer silence.
//   - A run that a silence ended before the window, or that has had no
//     record for longer than its gap, has every record of its session go
//     longer than any gap before from, so the window's first record of the
//     turn ends the run before from, and no later record starts it again.
//
// The sampler's records, Power and Presence, go by the later of them.
// When its reach ended, both go nowhere: stamped, the latest record the
// agent stamped itself between it and the sampler's next record
// (window.cutBy), is dated more than ttl, BUCKET_TTL_S, after its own time.
// pkg/score takes the slots after such a record for a never-sampling
// device's and lets it keep no clock (SM 2.1), and dated again at from
// minus carryGraceMS it would look like a sample the agent took then; the
// sampler's next record then starts it again, keeping nothing from before.
// The earlier of the two goes with it. The earlier one also goes when it
// is dated before the sampler started again (RestartAt), since pkg/score
// keeps nothing of it past the restart, however many of the sampler's
// records the restart took. Otherwise it stays, since its reach ended at
// the later one, which no stamped record after that changes, and a gap
// from the last sample to the sampler's next record still starts at it.
// Only a cut that has taken effect counts, as in pkg/score, which keeps
// FinalBefore at the sampler's record until then, and also while the
// sampler's next record, more than BUCKET_TTL_S later, is less than
// PENDING_WINDOW_S - READ_HORIZON_S behind the newest (reach.held). A
// record is carried only once FinalBefore is Lookback past it, so the
// carry sees a cut still waiting only from a record that arrives later
// than that allows.
func (c *carry) records(out []event.Event, from int64, g runGaps, stamped, ttl int64) []event.Event {
	grace := from - carryGraceMS
	last := c.sampled()
	for _, e := range []*event.Event{c.Focus, c.Power, c.Presence, c.Lock} {
		if e == nil || e != c.Focus && (stamped > last.T+ttl || e.T < c.RestartAt) {
			continue
		}
		if e == c.Lock && c.Power != nil && c.Power.EventID == e.EventID {
			continue // the latest os.power is the lock itself
		}
		r := *e
		r.T = max(r.T, grace)
		out = append(out, r)
	}
	for _, k := range slices.Sorted(maps.Keys(c.Sessions)) {
		s := c.Sessions[k]
		date := func(t int64) int64 { return max(t, grace) }
		if s.running() {
			gap := g.of(s.Native)
			last := s.Prompt.T
			if s.Agent != nil {
				last = max(last, s.Agent.T)
			}
			if s.Cut || last < from-gap {
				over := from - max(g.native, g.other) - 1
				date = func(int64) int64 { return over }
			} else {
				low := max(grace, last-gap)
				date = func(t int64) int64 { return min(max(t, low), last) }
			}
		}
		for _, e := range s.events() {
			r := *e
			r.T = date(r.T)
			out = append(out, r)
		}
	}
	return out
}
