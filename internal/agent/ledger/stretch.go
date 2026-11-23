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

// stretchesFromRows rebuilds the stretches of a ledger whose seal frames
// recorded none, as an older version's don't, from the moment each row
// recorded (sealPayload.LocalUntil): each rise of it ends a stretch. The
// first began at the beginning of time, and each later one where the
// writer last knew config.toml said online before it ended: after the
// end before it, and after the seal of the batch before the newest one
// sealed online before that end, since that online batch's read of the
// file came after that seal. A stretch is passed once a batch was sealed
// online from the row that first recorded its end on.
func (w *Writer) stretchesFromRows(rows []rowPayload) []stretch {
	var out []stretch
	var firstRow []int // the row each stretch's end was first recorded in
	var last moment
	lastOnline := -1
	for k, r := range rows {
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
			for j := k - 1; j >= 1; j-- {
				if rows[j].online() && rows[j].SealedAt < u.wall {
					if s := rows[j-1].SealedAt; s > from.m.wall {
						from = w.edgeOf(s, nil)
					}
					break
				}
			}
		}
		out = append(out, stretch{from: from, to: w.earlierEdge(u)})
		firstRow = append(firstRow, k)
		last = u
	}
	for i := range out {
		out[i].passed = lastOnline >= firstRow[i]
	}
	return out
}

// restoreStretches takes back the stretches the ledger recorded, for Open:
// from the newest seal frame a crash left without a row that has them, or
// else the newest row's, or else the base frame's, or, for a ledger whose
// seal frames recorded none, as an older version's, from its rows'
// moments (stretchesFromRows), which the next seal frame then records. It
// also takes where the writer last knew config.toml said online: the
// newest stretch end, or the seal of the batch before the newest one
// sealed online after it, whose read of the file came after that seal; and
// when that newest one was sealed.
func (w *Writer) restoreStretches(bf *batchesFile, restored []rowPayload) {
	rows := bf.rows
	if len(restored) > 0 {
		rows = append(slices.Clip(bf.rows), restored...)
	}
	list := bf.stretches
	for _, rp := range restored {
		if rp.Stretches != nil {
			list = rp.Stretches
		}
	}
	if list != nil {
		w.stretches = w.stretchesOf(list)
	} else {
		w.stretches = w.stretchesFromRows(rows)
		w.dirty = len(w.stretches) > 0
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
	w.raiseOnline(end)
	w.dirty = true
	return true
}

// learned records a stretch the writer didn't know of that ended at end, as
// the agent saw it end (NoteSeen): from where the writer last knew
// config.toml said online, or from the beginning of time for the chain's
// first. It is passed when a batch was sealed online since end, as far as
// the writer can tell (sealedSince), unless cross is set: end is of a boot
// no clock the writer's batches were sealed by can be ordered with, so
// none counts as sealed after it. w.mu is held.
func (w *Writer) learned(end edge, cross bool) {
	n := len(w.stretches)
	from := w.onlineAt
	if n == 0 {
		from = edge{}
	}
	s := stretch{from: from, to: end, passed: !cross && w.sealedSince(end)}
	if n > 0 && w.stretches[n-1].open {
		// The stretch going on began after this one ended.
		w.stretches = slices.Insert(w.stretches, n-1, s)
	} else {
		w.stretches = append(w.stretches, s)
		w.raiseOnline(end)
	}
	w.mergeStretches()
	w.dirty = true
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

// mergeStretches keeps the stretches to maxStretches: the two oldest become
// one, from the first's start to the second's end, with both's drops. w.mu
// is held.
func (w *Writer) mergeStretches() {
	for len(w.stretches) > maxStretches {
		a, b := w.stretches[0], w.stretches[1]
		b.from, b.dropped = a.from, a.dropped+b.dropped
		w.stretches[1] = b
		w.stretches = slices.Delete(w.stretches, 0, 1)
	}
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

// holds is Waiting's before: whether one of f's stretches holds e.
func (f waitFor) holds(e event.Envelope) bool {
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
