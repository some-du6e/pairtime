// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"slices"
	"time"

	osclock "github.com/some-du6e/pairtime/internal/platform/clock"
	"github.com/some-du6e/pairtime/pkg/event"
)

// The local-only stretches (D-late-local,
// docs/errata/2026-11-23-late-local-records.md). The writer judges an
// event that reaches it late by whether its own time falls in a stretch it
// knows config.toml said local-only, or may have: one it holds is sealed
// local-only while no batch has been sealed online since the stretch
// ended, and dropped once one has (ErrLateLocal), and one no stretch holds
// was made while the file said online, so it is sealed online however late
// it comes. Lookback's records keep their own rule (Writer.lookback).

// maxStretches bounds the stretches the writer keeps (Writer.stretches).
// Past it the two oldest become one, which only makes the time between
// them count as local-only too.
const maxStretches = 64

// lateKept bounds the late records a seal frame names (whyPayload.Late).
const lateKept = 4

// edge is a stretch's start or end as the writer holds events to it: its
// moment, and other, the OS's reading of another boot than the writer's
// that the moment held as an earlier run recorded it, which Writer.earlier
// traded for this boot's start, zero for none. A hook call of that boot
// that reaches the writer in this one, as one held.jsonl kept across a
// reboot, is held to that boot's reading by its mono too, as that boot's
// writer held it. A zero edge starts at the beginning of time.
type edge struct {
	m     moment
	other moment
}

// edgeAt is the edge at m, a moment this run took.
func edgeAt(m moment) edge { return edge{m: m} }

// before reports whether e was made at or before d: by d's moment, or, for
// a hook call of other's boot, by its mono against that boot's reading.
func (d edge) before(e event.Envelope) bool {
	return d.m.before(e) || d.other.os > 0 && e.Mono > 0 && e.BootID == d.other.boot && e.Mono <= d.other.os
}

// reached reports whether e was made at or after d, by the clocks before
// holds it to. Either clock that says so is enough: privacy wins what
// can't be told.
func (d edge) reached(e event.Envelope) bool {
	return d.m.reached(e) || d.other.os > 0 && e.Mono > 0 && e.BootID == d.other.boot && e.Mono >= d.other.os
}

// osOf is the OS's reading d holds as it was taken, and its boot: other's
// when earlier traded it, and zero for none.
func (d edge) osOf() untilOS {
	switch {
	case d.other.os > 0:
		return untilOS{Mono: d.other.os, Boot: d.other.boot}
	case d.m.os > 0:
		return untilOS{Mono: d.m.os, Boot: d.m.boot}
	}
	return untilOS{}
}

// stretch is a local-only stretch the writer knows: from from, the last
// moment the writer knew config.toml said online before it took the file
// as local-only, or the beginning of time for the chain's first, since
// every install starts in local mode, to to, the moment it saw the file
// online again, while open is false. A stretch begins where the writer last
// knew the file said online rather than where it first saw it local-only:
// the switch came between the two, and privacy wins what can't be told.
// Each edge holds its own millisecond, as the moment does (#174). passed
// is set once a batch was sealed online since the stretch ended, after
// which a record of it that comes late is dropped, and dropped counts
// those records (ErrLateLocal).
type stretch struct {
	from, to edge
	open     bool
	passed   bool
	dropped  uint64
}

// holds reports whether e was made in s.
func (s stretch) holds(e event.Envelope) bool {
	return s.from.reached(e) && (s.open || s.to.before(e))
}

// widen moves s's edges apart, by the wall clock, by as far as the wall
// clock stepped back between them, as a monotonic clock both edges hold
// tells: this run's, or the OS's of one boot. An event of the stretch that
// the writer judges by its t alone, as a log record read late, can carry
// any time the wall clock showed in the stretch, and a step back puts some
// of those before its start, or after its end, by the wall clock. When the
// step came isn't known, so the stretch takes in both sides. A step forward
// only spreads those times between the edges. When the edges share no
// monotonic clock, as for a stretch left open across a reboot or one that
// began at a moment an earlier run recorded, nothing tells how far the
// wall clock stepped, so s holds the events judged by the wall clock alone
// from prev on, the end of the stretch before it in Unix ms, or the
// beginning of time, 0, for the first, as the single moment #174 kept held
// every event before it.
func (s *stretch) widen(prev int64) {
	var span int64
	switch {
	case s.from.m.run > 1 && s.to.m.run > 1:
		span = s.to.m.run - s.from.m.run
	case s.from.m.os > 1 && s.to.m.os > 1 && s.from.m.boot == s.to.m.boot:
		// An edge an earlier run recorded holds this boot's start, 1, by
		// its clock (Writer.earlier), which measures nothing.
		span = s.to.m.os - s.from.m.os
	default:
		s.from.m.wall = min(s.from.m.wall, max(prev, 0))
		return
	}
	if back := span - (s.to.m.wall - s.from.m.wall); back > 0 {
		s.from.m.wall -= back
		s.to.m.wall += back
	}
}

// prevEnd is the wall time of the end of the stretch before ss[i], in Unix
// ms, and 0 for the first (widen).
func prevEnd(ss []stretch, i int) int64 {
	if i <= 0 || i > len(ss) || ss[i-1].open {
		return 0
	}
	return ss[i-1].to.m.wall
}

// stretchPayload is a stretch as a seal frame records it
// (sealPayload.Stretches): its edges in Unix ms, each with the OS's
// monotonic reading and its boot when the writer read that clock then, and
// no reading of a run's clock, which a restart starts again. From 0 is the
// beginning of time.
type stretchPayload struct {
	From    int64    `json:"from,omitempty"`
	FromOS  *untilOS `json:"from_os,omitempty"`
	To      int64    `json:"to,omitempty"`
	ToOS    *untilOS `json:"to_os,omitempty"`
	Open    bool     `json:"open,omitempty"`
	Passed  bool     `json:"passed,omitempty"`
	Dropped uint64   `json:"dropped,omitempty"`
}

// osPayload is u as a seal frame records it, nil for none.
func osPayload(u untilOS) *untilOS {
	if u.Mono <= 0 || u.Boot == "" {
		return nil
	}
	return &u
}

// stretchPayloads is the stretches as a seal frame records them, every one
// that ended passed when pass is set, as by a batch sealed online. w.mu is
// held.
func (w *Writer) stretchPayloads(pass bool) []stretchPayload {
	out := make([]stretchPayload, len(w.stretches))
	for i, s := range w.stretches {
		p := stretchPayload{From: s.from.m.wall, FromOS: osPayload(s.from.osOf()), Open: s.open,
			Passed: s.passed || pass && !s.open, Dropped: s.dropped}
		if !s.open {
			p.To, p.ToOS = s.to.m.wall, osPayload(s.to.osOf())
		}
		out[i] = p
	}
	return out
}

// stretchesOf reads back the stretches a seal frame recorded, as an earlier
// run's (earlierEdge).
func (w *Writer) stretchesOf(ps []stretchPayload) []stretch {
	out := make([]stretch, 0, len(ps))
	for _, p := range ps {
		s := stretch{from: w.edgeOf(p.From, p.FromOS), open: p.Open, passed: p.Passed, dropped: p.Dropped}
		if !p.Open {
			s.to = w.edgeOf(p.To, p.ToOS)
		}
		out = append(out, s)
	}
	return out
}

// edgeOf is the edge an earlier run recorded at wall, in Unix ms, with the
// OS's reading o, nil for none. A wall of 0 is the beginning of time.
func (w *Writer) edgeOf(wall int64, o *untilOS) edge {
	if wall == 0 && o == nil {
		return edge{}
	}
	m := moment{wall: wall}
	if o != nil && o.Mono > 0 && o.Boot != "" {
		m.os, m.boot = o.Mono, o.Boot
	}
	return w.earlierEdge(m)
}

// earlierEdge is m, a moment an earlier run recorded, as an edge: with the
// bounds of this run and this boot (Writer.earlier), and other, its OS
// reading, when earlier traded it for this boot's start.
func (w *Writer) earlierEdge(m moment) edge {
	d := edge{m: w.earlier(m)}
	if m.os > 0 && d.m.boot != m.boot {
		d.other = moment{os: m.os, boot: m.boot}
	}
	return d
}

// stretchesFromRows rebuilds stretches from the moment each row recorded
// (sealPayload.LocalUntil), for rows whose seal frames recorded none, as an
// older version's: each rise of it, from rows[start] on, past last, ends a
// stretch, which it adds to seed. The chain's first begins at the beginning
// of time, and each later one where the one before it ended: a row records
// no moment by a monotonic clock where config.toml was seen local-only, so
// the stretch holds every event judged by the wall clock alone from the
// end before it on, as the single moment #174 kept held every event before
// it (stretch.widen). A stretch is passed once a batch was sealed online
// from the row that first recorded its end on.
func (w *Writer) stretchesFromRows(rows []rowPayload, start int, seed []stretch, last moment) []stretch {
	out := slices.Clone(seed)
	added := len(out)
	var firstRow []int // the row each added stretch's end was first recorded in
	lastOnline := -1
	for k := start; k < len(rows); k++ {
		r := rows[k]
		if r.online() {
			lastOnline = k
		}
		u := r.until()
		if !laterEnd(u, last) {
			continue
		}
		from := edge{}
		if n := len(out); n > 0 {
			from = out[n-1].to
		}
		s := stretch{from: from, to: w.earlierEdge(u)}
		s.widen(prevEnd(out, len(out)))
		out = append(out, s)
		firstRow = append(firstRow, k)
		last = u
	}
	for i := range firstRow {
		out[added+i].passed = lastOnline >= firstRow[i]
	}
	return out
}

// restoreStretches takes back the stretches the ledger recorded, for Open:
// from the newest seal frame a crash left without a row that has them, or
// else the newest row's, or else the base frame's, or, for a ledger whose
// seal frames recorded none, as an older version's, from its rows'
// moments (stretchesFromRows), which the next seal frame then records.
// Rows an older version sealed after the ones it takes them from, as after
// a downgrade, end the stretch going on and add stretches of their own. It
// also takes where the writer last knew config.toml said online: the
// newest stretch end, or the seal of the batch before the newest one
// sealed online after it, whose read of the file came after that seal; and
// when that newest one was sealed.
func (w *Writer) restoreStretches(bf *batchesFile, restored []rowPayload) {
	rows := bf.rows
	if len(restored) > 0 {
		rows = append(slices.Clip(bf.rows), restored...)
	}
	// at is the row that recorded list, -1 for the base frame.
	list, at := bf.stretches, bf.stretchesRow
	for j, rp := range restored {
		if rp.Stretches != nil {
			list, at = rp.Stretches, len(bf.rows)+j
		}
	}
	if list == nil {
		w.stretches = w.stretchesFromRows(rows, 0, nil, moment{})
		w.dirty = len(w.stretches) > 0
		w.mergeStretches()
	} else {
		w.stretches = w.stretchesOf(list)
		// Rows sealed after the one that recorded the list, with none of
		// their own, whose moment rose past the one the list ends at and
		// the one its row recorded, are an older version's, which seals
		// without the list, as after a downgrade: this one records the
		// list whenever its moment moves (raiseUntil). Each rise ends a
		// stretch. The first ends the one the list has going on, if one
		// does, passed once a batch was sealed online from that row on, as
		// the older version may have uploaded; and each later rise ends a
		// stretch of its own (stretchesFromRows).
		end := listEnd(list)
		var recorded moment
		switch {
		case at >= 0 && at < len(rows):
			recorded = rows[at].until()
		case at < 0:
			// The base frame a sweep wrote holds the list, and the moment its
			// row recorded (basePayload.LocalUntil).
			recorded = sealPayload{LocalUntil: bf.base.LocalUntil, LocalUntilOS: bf.base.LocalUntilOS}.until()
		}
		k := at + 1
		for k < len(rows) && !(laterEnd(rows[k].until(), end) && laterEnd(rows[k].until(), recorded)) {
			k++
		}
		if k < len(rows) {
			n := len(w.stretches)
			last := moment{}
			if s := w.goingOn(); s != nil {
				last = rows[k].until()
				s.to, s.open = w.earlierEdge(last), false
				s.widen(prevEnd(w.stretches, n-1))
				for _, r := range rows[k:] {
					s.passed = s.passed || r.online()
				}
				k++
			}
			w.stretches = w.stretchesFromRows(rows, k, w.stretches, last)
			w.dirty = true
			w.mergeStretches()
		}
	}
	if end, ok := w.newestEnd(); ok {
		w.onlineAt = end
	}
	for j := len(rows) - 1; j >= 0; j-- {
		if !rows[j].online() {
			continue
		}
		w.onlineWall = rows[j].SealedAt
		if s := rows[max(j-1, 0)].SealedAt; j > 0 && s > w.onlineAt.m.wall {
			w.onlineAt = w.edgeOf(s, nil)
		}
		break
	}
}

// listEnd is the end of the newest stretch in ps that ended, as a row
// records its moment, and the zero moment when none has.
func listEnd(ps []stretchPayload) moment {
	for i := len(ps) - 1; i >= 0; i-- {
		if !ps[i].Open {
			return payloadEnd(ps[i])
		}
	}
	return moment{}
}

// payloadEnd is the end p records, as a row records its moment, with no
// reading of a run's clock.
func payloadEnd(p stretchPayload) moment {
	m := moment{wall: p.To}
	if o := p.ToOS; o != nil && o.Mono > 0 && o.Boot != "" {
		m.os, m.boot = o.Mono, o.Boot
	}
	return m
}

// laterEnd reports whether u, a moment a row recorded, is a later stretch
// end than last: later by the wall clock, or by the OS's clock of last's
// boot, as after a step back of the wall clock. Only a reading of an
// actual end counts: a writer opened in another boot records its moment
// with this boot's start, 1 ms (Writer.earlier), which ends nothing.
func laterEnd(u, last moment) bool {
	return u.wall > last.wall || u.os > 1 && u.boot == last.boot && u.os > last.os
}

// sawLocal takes config.toml as local-only from now on: a stretch begins,
// unless one goes on, from where the writer last knew the file said
// online, or from the beginning of time for the chain's first. w.mu is
// held.
func (w *Writer) sawLocal() {
	n := len(w.stretches)
	if n > 0 && w.stretches[n-1].open {
		return
	}
	from := w.onlineAt
	if n == 0 {
		from = edge{}
	}
	w.stretches = append(w.stretches, stretch{from: from, open: true})
	w.mergeStretches()
	w.dirty = true
}

// sawOnline ends the stretch that goes on at end, the moment the writer
// took config.toml as online again, which is taken after the read that
// saw it so, and reports whether one did. w.mu is held.
func (w *Writer) sawOnline(end edge) bool {
	n := len(w.stretches)
	if n == 0 || !w.stretches[n-1].open {
		return false
	}
	s := &w.stretches[n-1]
	s.to, s.open = end, false
	s.widen(prevEnd(w.stretches, n-1))
	w.raiseOnline(end)
	w.dirty = true
	return true
}

// learned records a stretch the writer didn't know of that ended at end, as
// the agent saw it end (NoteSeen): from where the writer last knew
// config.toml said online, or from the beginning of time for the chain's
// first. When the writer knew that only after end, by a clock the two
// share, as from a seal's read that came between the switch back and the
// agent's note of it, the stretch begins at the end of the newest one that
// ended before it, or at the beginning of time, so that it holds what was
// made in it. It is passed when a batch was sealed online since end, as far as
// the writer can tell (sealedSince), unless cross is set: end is of a boot
// no clock the writer's batches were sealed by can be ordered with, so
// none counts as sealed after it. w.mu is held.
func (w *Writer) learned(end edge, cross bool) {
	n := len(w.stretches)
	from := w.onlineAt
	if before, shared := clockOrder(from.m, end.m); n == 0 {
		from = edge{}
	} else if shared && !before {
		from = w.endBefore(end)
	}
	if b := end.other.boot; end.other.os > 0 && b != from.osOf().Boot {
		// The end is of a boot the start holds no reading of, one that came
		// after the writer last knew config.toml said online: its hook calls
		// were all made after the stretch began, whatever that boot's wall
		// clock said, so its own reading alone bounds them.
		from.other = moment{os: 1, boot: b}
	}
	s := stretch{from: from, to: end, passed: !cross && w.sealedSince(end)}
	if n > 0 && w.stretches[n-1].open {
		// The stretch going on began after this one ended.
		s.widen(prevEnd(w.stretches, n-1))
		w.stretches = slices.Insert(w.stretches, n-1, s)
	} else {
		s.widen(prevEnd(w.stretches, n))
		w.stretches = append(w.stretches, s)
		w.raiseOnline(end)
	}
	w.mergeStretches()
	w.dirty = true
}

// endBefore is the end of the newest stretch that ended before end, by the
// clocks the two share (clockOrder), and the beginning of time when none
// did. w.mu is held.
func (w *Writer) endBefore(end edge) edge {
	for i := len(w.stretches) - 1; i >= 0; i-- {
		if s := w.stretches[i]; !s.open {
			if before, _ := clockOrder(s.to.m, end.m); before {
				return s.to
			}
		}
	}
	return edge{}
}

// clockOrder reports whether a came before b by a monotonic clock both hold
// a reading of that measures something, this run's or the OS's of one boot,
// or else by the wall clock, and whether they share such a monotonic clock.
// A moment an earlier run recorded holds this run's start, 1, by the run's
// clock, and one of another boot that boot's start, 1, by the OS's
// (Writer.earlier), which order nothing.
func clockOrder(a, b moment) (before, shared bool) {
	switch {
	case a.run > 1 && b.run > 1:
		return a.run < b.run, true
	case a.os > 1 && b.os > 1 && a.boot == b.boot:
		return a.os < b.os, true
	}
	return a.wall < b.wall, false
}

// sealedSince reports whether a batch was sealed online after end: in this
// run, by the clocks the two share, or, for an end an earlier run recorded
// or one with no reading of this run's clock, in an earlier one, by the
// wall clock, as its rows say. w.mu is held.
func (w *Writer) sealedSince(end edge) bool {
	if w.onlineRun != (moment{}) && !w.onlineRun.earlierThan(end.m) {
		return true
	}
	return end.m.run <= 1 && w.onlineWall > 0 && w.onlineWall >= end.m.wall
}

// raiseOnline moves onlineAt on to d, a moment the writer knew config.toml
// said online, when it is later. w.mu is held.
func (w *Writer) raiseOnline(d edge) {
	if w.onlineAt == (edge{}) || w.onlineAt.m.earlierThan(d.m) {
		w.onlineAt = d
	}
}

// openHolds reports whether the stretch that goes on, if one does, began at
// or before end, so it holds that moment. w.mu is held.
func (w *Writer) openHolds(end moment) bool {
	n := len(w.stretches)
	return n > 0 && w.stretches[n-1].open && !end.earlierThan(w.stretches[n-1].from.m)
}

// goingOn returns the stretch that goes on, nil when none does. w.mu is
// held.
func (w *Writer) goingOn() *stretch {
	if n := len(w.stretches); n > 0 && w.stretches[n-1].open {
		return &w.stretches[n-1]
	}
	return nil
}

// newestEnd returns the end of the newest stretch that ended, and false
// when none has. w.mu is held.
func (w *Writer) newestEnd() (edge, bool) {
	for i := len(w.stretches) - 1; i >= 0; i-- {
		if !w.stretches[i].open {
			return w.stretches[i].to, true
		}
	}
	return edge{}, false
}

// passes reports whether a stretch that ended isn't passed yet, so a batch
// sealed online passes it. w.mu is held.
func (w *Writer) passes() bool {
	for _, s := range w.stretches {
		if !s.open && !s.passed {
			return true
		}
	}
	return false
}

// passAll marks every stretch that ended as passed: a batch was just sealed
// online after it. w.mu is held.
func (w *Writer) passAll() {
	for i := range w.stretches {
		if s := &w.stretches[i]; !s.open && !s.passed {
			s.passed, w.dirty = true, true
		}
	}
}

// mergeStretches keeps the stretches to maxStretches. Of the oldest
// adjacent pair that ended alike passed or not, as passAll leaves the older
// ones, the two become one with both's drops, from the earlier start to the
// later end by the wall clock, as a step back of the wall clock between
// them can put the second's end before the first's. When no pair agrees,
// the two oldest become one that is passed when either is, which can only
// drop a record of them, never seal one local-only after an online batch.
// Either way only more of the past counts as local-only. w.mu is held.
func (w *Writer) mergeStretches() {
	for len(w.stretches) > maxStretches {
		i := 0
		for j := 0; j+1 < len(w.stretches); j++ {
			if a, b := w.stretches[j], w.stretches[j+1]; !a.open && !b.open && a.passed == b.passed {
				i = j
				break
			}
		}
		a, b := w.stretches[i], w.stretches[i+1]
		m := b
		m.from = a.from
		m.from.m.wall = min(a.from.m.wall, b.from.m.wall)
		m.to.m.wall = max(a.to.m.wall, b.to.m.wall)
		m.passed = a.passed || b.passed
		m.dropped = a.dropped + b.dropped
		w.stretches[i+1] = m
		w.stretches = slices.Delete(w.stretches, i, i+1)
	}
}

// foldStretches folds the writer's stretches at cut, in Unix ms, as a sweep
// by that day folds a seal frame's (foldPayloads): every stretch that
// began before cut becomes part of one from the beginning of time to cut,
// or to the latest end of those that ended after it, or one that goes on
// when one of them does, passed when all of them are, with all their
// drops. The next seal frame records them. w.mu is held.
func (w *Writer) foldStretches(cut int64) {
	fold := stretch{passed: true}
	var rest []stretch
	n, first := 0, -1
	for i, s := range w.stretches {
		if s.from.m.wall >= cut {
			rest = append(rest, s)
			continue
		}
		if n == 0 {
			first = i
		}
		n++
		fold.open = fold.open || s.open
		fold.passed = fold.passed && s.passed
		fold.dropped += s.dropped
		if !s.open && s.to.m.wall > fold.to.m.wall {
			fold.to = s.to
		}
	}
	if n == 0 {
		return
	}
	if s := w.stretches[first]; n == 1 && first == 0 && s.from == (edge{}) && (s.open || s.to.m.wall >= cut) {
		return
	}
	switch {
	case fold.open:
		fold.to, fold.passed = edge{}, false
	case fold.to.m.wall < cut:
		fold.to = edgeAt(moment{wall: cut})
	}
	w.stretches = append([]stretch{fold}, rest...)
	w.dirty = true
}

// lateDropped is how many records Write dropped over the chain's life, as
// the stretches count them. w.mu is held.
func (w *Writer) lateDropped() uint64 {
	var n uint64
	for _, s := range w.stretches {
		n += s.dropped
	}
	return n
}

// judge reports how the stretches hold an event Write takes, made at e or,
// by its sender's stamp, at sent: late when a stretch no batch sealed
// online has passed since it ended holds it, and drop, the stretch, when
// only passed ones do. One the stretch that goes on holds is neither: its
// record's taint, as it comes while the writer takes config.toml as
// local-only, or the stretch's end, holds it (lateLocked). w.mu is held.
func (w *Writer) judge(e event.Envelope, sent osclock.Stamp) (late bool, drop *stretch) {
	times := []event.Envelope{e}
	if !sent.Wall.IsZero() {
		times = append(times, event.Envelope{T: sent.Wall.UnixMilli(), Mono: sent.Mono, BootID: sent.BootID})
	}
	inOpen := false
	for i := range w.stretches {
		s := &w.stretches[i]
		if !holdsAny(*s, times) {
			continue
		}
		switch {
		case s.open:
			inOpen = true
		case !s.passed:
			late = true
		case drop == nil:
			drop = s
		}
	}
	if late || inOpen {
		return late, nil
	}
	return false, drop
}

// holdsAny reports whether s holds any of times.
func holdsAny(s stretch, times []event.Envelope) bool {
	for _, e := range times {
		if s.holds(e) {
			return true
		}
	}
	return false
}

// lateLocked marks late each pending record a stretch that ended holds, by
// the times it keeps (pending), as Write would have: once the writer knows
// a stretch ended, or learns of one it didn't know, what pending holds of
// it is held to it, as what comes after is. A pending record has its
// number, so it stays, passed stretch or not, and its batch is local-only.
// w.mu is held.
func (w *Writer) lateLocked() {
	for i := range w.pending {
		p := &w.pending[i]
		if p.late {
			continue
		}
		times, ok := p.times()
		if !ok {
			continue
		}
		for _, s := range w.stretches {
			if !s.open && holdsAny(s, times) {
				p.late, w.late = true, true
				break
			}
		}
	}
}

// waitFor is what a seal asks Options.Waiting about (Writer.seal): the
// stretches no batch sealed online has passed since they ended, and the one
// going on, ended where the seal's own read of config.toml ends it.
type waitFor []stretch

// waitForLocked is the stretches that ended and no batch sealed online has
// passed. w.mu is held.
func (w *Writer) waitForLocked() waitFor {
	var out waitFor
	for _, s := range w.stretches {
		if !s.open && !s.passed {
			out = append(out, s)
		}
	}
	return out
}

// holds is Waiting's before: whether one of f's stretches holds e. An
// envelope with no times stands for an event whose times the asker can't
// read yet, as the hook calls an earlier run left that the adapters haven't
// counted (agent's adapterHost.waiting): any stretch may hold it, so it is
// held while f has one, and a writer that knows no stretch holds nothing.
func (f waitFor) holds(e event.Envelope) bool {
	if e.T == 0 && e.Mono == 0 {
		return len(f) > 0
	}
	for _, s := range f {
		if s.holds(e) {
			return true
		}
	}
	return false
}

// whyPayload says why a batch was sealed local-only (sealPayload.Why), so
// that doctor can name it when the batch roots stop at it: Options.Online
// said config.toml didn't let the computer upload as it was sealed
// (Config), a record came while the writer took the file as local-only
// (Noted), something made in a stretch was still on its way to the writer
// (Waiting), or it holds records made in a stretch that came after it
// ended, the first lateKept of them by their kinds and their own times,
// and LateN of them in all (Late). It holds no content.
type whyPayload struct {
	Config  bool         `json:"config,omitempty"`
	Noted   bool         `json:"noted,omitempty"`
	Waiting bool         `json:"waiting,omitempty"`
	Late    []lateRecord `json:"late,omitempty"`
	LateN   int          `json:"late_n,omitempty"`
}

// lateRecord is a record whyPayload names: its event's kind, and its own
// time in Unix ms.
type lateRecord struct {
	Kind event.Kind `json:"kind"`
	T    int64      `json:"t"`
}

// LocalWhy is why a batch was sealed local-only, as its seal frame records
// it (Batch.Why, Row.Why), nil for a batch sealed online and for one an
// older version sealed, which recorded none: config.toml said mode =
// "local", had an error, or named a link the writer hadn't had the end of
// yet, as it was sealed (Config); a record came while the writer took the
// file as local-only (Noted); something made in a local-only stretch was
// still on its way to the writer (Waiting); or the batch holds records made
// in a local-only stretch that came after it ended, the first few by their
// kinds and their own times (Late), LateN in all.
type LocalWhy struct {
	Config, Noted, Waiting bool
	Late                   []LateRecord
	LateN                  int
}

// LateRecord is a record LocalWhy names.
type LateRecord struct {
	Kind event.Kind
	T    time.Time
}

// localWhy is p as LocalWhy, nil for nil.
func localWhy(p *whyPayload) *LocalWhy {
	if p == nil {
		return nil
	}
	why := &LocalWhy{Config: p.Config, Noted: p.Noted, Waiting: p.Waiting, LateN: p.LateN}
	for _, r := range p.Late {
		why.Late = append(why.Late, LateRecord{Kind: r.Kind, T: time.UnixMilli(r.T).UTC()})
	}
	return why
}

// whyLocked is why a batch sealed now, with config.toml read as online or
// not and Waiting's answer, is local-only. w.mu is held.
func (w *Writer) whyLocked(online, waiting bool) *whyPayload {
	why := &whyPayload{Config: !online, Noted: w.taint, Waiting: waiting}
	for _, p := range w.pending {
		if !p.late {
			continue
		}
		why.LateN++
		if len(why.Late) < lateKept {
			if r, ok := p.named(); ok {
				why.Late = append(why.Late, r)
			}
		}
	}
	return why
}

// times are the times p is held to (pending): its event's and its sender's
// stamp, as Write took them, or as recovery read them back (envelopeOf),
// and false for a record that holds no event.
func (p pending) times() ([]event.Envelope, bool) {
	e, ok := p.env, p.has
	if !ok {
		e, ok = envelopeOf(p.rec)
	}
	if !ok {
		return nil, false
	}
	times := []event.Envelope{e}
	if !p.sent.Wall.IsZero() {
		times = append(times, event.Envelope{T: p.sent.Wall.UnixMilli(), Mono: p.sent.Mono, BootID: p.sent.BootID})
	}
	return times, true
}

// named is p as whyPayload names it, by its event's own time, false
// for a record that holds no event.
func (p pending) named() (lateRecord, bool) {
	kind, ok := p.rec.Header.RType().EventKind()
	if !ok {
		return lateRecord{}, false
	}
	t := p.t
	if !p.has {
		e, ok := envelopeOf(p.rec)
		if !ok {
			return lateRecord{}, false
		}
		t = e.T
	}
	return lateRecord{Kind: kind, T: t}, true
}
