// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	osclock "github.com/some-du6e/pairtime/internal/platform/clock"
	"github.com/some-du6e/pairtime/pkg/event"
)

// TestReached checks how an event is held to a stretch's start: at or after
// it by the monotonic clock it carries when the start holds that clock too,
// and by its t otherwise, each edge holding its own millisecond, with the
// beginning of time before every event.
func TestReached(t *testing.T) {
	const boot = "b_os"
	m := moment{wall: 10_000, run: 5_000, os: 7_000, boot: boot}
	cases := []struct {
		name string
		at   moment
		e    event.Envelope
		want bool
	}{
		{"an event of another boot whose t is after the start reached it", m, event.Envelope{T: 10_001, BootID: "b_other", Mono: 1}, true},
		{"one whose t is the start's own millisecond counts as in the stretch", m, event.Envelope{T: 10_000, BootID: "b_other", Mono: 1}, true},
		{"one whose t is before the start didn't reach it", m, event.Envelope{T: 9_999, BootID: "b_other", Mono: 1}, false},
		{"an event of this run goes by this run's clock, though a step of the wall clock put its t before", m, event.Envelope{T: 1, BootID: runBoot, Mono: 5_000}, true},
		{"and didn't reach it when its mono is before, though its t is after", m, event.Envelope{T: 20_000, BootID: runBoot, Mono: 4_999}, false},
		{"a hook call of the start's boot goes by its mono", m, event.Envelope{T: 1, BootID: boot, Mono: 7_000}, true},
		{"and didn't reach it when its mono is before, though its t is after", m, event.Envelope{T: 20_000, BootID: boot, Mono: 6_999}, false},
		{"an event with no mono goes by its t", m, event.Envelope{T: 10_000, BootID: boot}, true},
		{"every event reached the beginning of time", moment{}, event.Envelope{T: 1}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.at.reached(c.e); got != c.want {
				t.Errorf("reached(%+v) of %+v = %v, want %v", c.e, c.at, got, c.want)
			}
		})
	}
}

// TestStretchHolds checks which events a stretch holds: those at or after
// its start and at or before its end, by the clocks each edge holds, every
// one after the start while it goes on, every one before the end for the
// chain's first, and a hook call of a boot an edge was recorded in by that
// boot's reading too.
func TestStretchHolds(t *testing.T) {
	const boot, old = "b_os", "b_old"
	from := edge{m: moment{wall: 10_000, os: 7_000, boot: boot}}
	to := edge{m: moment{wall: 20_000, os: 17_000, boot: boot}}
	// An end an earlier run recorded in the boot before, read back in this
	// one (Writer.earlierEdge): this boot's start by its clock, and the old
	// boot's reading beside it.
	traded := edge{m: moment{wall: 20_000, run: 1, os: 1, boot: boot}, other: moment{os: 17_000, boot: old}}
	cases := []struct {
		name string
		s    stretch
		e    event.Envelope
		want bool
	}{
		{"an event between its edges by its t is in it", stretch{from: from, to: to}, event.Envelope{T: 15_000}, true},
		{"so is one at either edge's own millisecond", stretch{from: from, to: to}, event.Envelope{T: 20_000}, true},
		{"an event after its end is not", stretch{from: from, to: to}, event.Envelope{T: 20_001}, false},
		{"nor is one before its start, made while config.toml said online", stretch{from: from, to: to}, event.Envelope{T: 9_999}, false},
		{"a hook call of the edges' boot goes by its mono, though a step of the wall clock put its t after the end",
			stretch{from: from, to: to}, event.Envelope{T: 90_000, BootID: boot, Mono: 16_000}, true},
		{"and isn't in it when its mono is after the end, though its t is before",
			stretch{from: from, to: to}, event.Envelope{T: 15_000, BootID: boot, Mono: 17_001}, false},
		{"one going on holds every event after its start", stretch{from: from, open: true}, event.Envelope{T: 1 << 50}, true},
		{"the chain's first holds every event before its end", stretch{to: to}, event.Envelope{T: 1}, true},
		{"an end an earlier run recorded in another boot holds a hook call of that boot by that boot's clock",
			stretch{to: traded}, event.Envelope{T: 90_000, BootID: old, Mono: 16_000}, true},
		{"and none of this boot's, all made after it", stretch{to: traded}, event.Envelope{T: 15_000, BootID: boot, Mono: 2}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.s.holds(c.e); got != c.want {
				t.Errorf("holds(%+v) = %v, want %v", c.e, got, c.want)
			}
		})
	}
}

// TestWiden checks that a stretch whose edges span less by the wall clock
// than by a monotonic clock both hold, as after a step back of the wall
// clock in it, takes in, by the wall clock, as much before its start and
// after its end as the step, and that one with no such step stays as it
// is. One whose edges share no monotonic clock, as one left open across a
// reboot, can't tell a step, so it holds what the wall clock alone dates
// from the end of the stretch before it on, or from the beginning of time.
func TestWiden(t *testing.T) {
	const boot, old = "b_os", "b_old"
	cases := []struct {
		name     string
		from, to edge
		// prev is the end of the stretch before it, in Unix ms, 0 for none.
		prev int64
		// wantFrom and wantTo are the edges' wall times once widened.
		wantFrom, wantTo int64
	}{
		{"a step back by this run's clock widens both edges by the step",
			edgeAt(moment{wall: 10_000, run: 5_000}), edgeAt(moment{wall: 15_000, run: 20_000}), 4_000, 0, 25_000},
		{"and so does one by the OS's clock of one boot",
			edgeAt(moment{wall: 10_000, os: 5_000, boot: boot}), edgeAt(moment{wall: 15_000, os: 20_000, boot: boot}), 4_000, 0, 25_000},
		{"a step forward changes nothing", edgeAt(moment{wall: 10_000, run: 5_000}), edgeAt(moment{wall: 40_000, run: 20_000}), 4_000, 10_000, 40_000},
		{"a start an earlier run recorded, whose run reading is this run's start, holds what the wall clock dates from the stretch before's end",
			edgeAt(moment{wall: 10_000, run: 1}), edgeAt(moment{wall: 15_000, run: 20_000}), 4_000, 4_000, 15_000},
		{"so do edges of two boots", edgeAt(moment{wall: 10_000, os: 5_000, boot: boot}), edgeAt(moment{wall: 15_000, os: 20_000, boot: "b_other"}), 4_000, 4_000, 15_000},
		{"and a start recorded in the boot before, read back in this one, as a stretch left open across a reboot has",
			edge{m: moment{wall: 10_000, run: 1, os: 1, boot: boot}, other: moment{os: 17_000, boot: old}}, edgeAt(moment{wall: 15_000, run: 20_000, os: 9_000, boot: boot}), 4_000, 4_000, 15_000},
		{"the first stretch takes in what the wall clock dates from the beginning of time",
			edgeAt(moment{wall: 10_000, run: 1}), edgeAt(moment{wall: 15_000, run: 20_000}), 0, 0, 15_000},
		{"a stretch before's end after the start moves nothing", edgeAt(moment{wall: 10_000, run: 1}), edgeAt(moment{wall: 15_000, run: 20_000}), 12_000, 10_000, 15_000},
		{"the chain's first stretch, from the beginning of time, stays there", edge{}, edgeAt(moment{wall: 15_000, run: 20_000}), 0, 0, 15_000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := stretch{from: c.from, to: c.to}
			s.widen(c.prev)
			if s.from.m.wall != c.wantFrom || s.to.m.wall != c.wantTo {
				t.Errorf("widened to %d..%d, want %d..%d", s.from.m.wall, s.to.m.wall, c.wantFrom, c.wantTo)
			}
		})
	}
	holds := []struct {
		name string
		s    stretch
		prev int64
		e    event.Envelope
		want bool
	}{
		{"a widened stretch holds a record its t alone dates before its start by up to the step",
			stretch{from: edgeAt(moment{wall: 10_000, run: 5_000}), to: edgeAt(moment{wall: 15_000, run: 20_000})}, 4_000, event.Envelope{T: 1000}, true},
		{"and one dated after its end by up to the step",
			stretch{from: edgeAt(moment{wall: 10_000, run: 5_000}), to: edgeAt(moment{wall: 15_000, run: 20_000})}, 4_000, event.Envelope{T: 24_000}, true},
		{"but not one dated past that",
			stretch{from: edgeAt(moment{wall: 10_000, run: 5_000}), to: edgeAt(moment{wall: 15_000, run: 20_000})}, 4_000, event.Envelope{T: 25_001}, false},
		{"a stretch left open across a reboot holds a transcript line the clock, skewed back after the reboot, dated before its start",
			stretch{from: edge{m: moment{wall: 10_000, run: 1, os: 1, boot: boot}, other: moment{os: 17_000, boot: old}},
				to: edgeAt(moment{wall: 15_000, run: 20_000, os: 9_000, boot: boot})}, 4_000, event.Envelope{T: 5_000, BootID: runBoot}, true},
		{"but not one dated before the stretch before it ended, which that stretch holds if anything does",
			stretch{from: edge{m: moment{wall: 10_000, run: 1, os: 1, boot: boot}, other: moment{os: 17_000, boot: old}},
				to: edgeAt(moment{wall: 15_000, run: 20_000, os: 9_000, boot: boot})}, 4_000, event.Envelope{T: 3_999, BootID: runBoot}, false},
		{"while a hook call of this boot still goes by its mono: one its boot's clock dates after the stretch's end isn't in it, though its t is",
			stretch{from: edge{m: moment{wall: 10_000, run: 1, os: 1, boot: boot}, other: moment{os: 17_000, boot: old}},
				to: edgeAt(moment{wall: 15_000, run: 20_000, os: 9_000, boot: boot})}, 4_000, event.Envelope{T: 5_000, BootID: boot, Mono: 9_001}, false},
	}
	for _, c := range holds {
		t.Run(c.name, func(t *testing.T) {
			s := c.s
			s.widen(c.prev)
			if got := s.holds(c.e); got != c.want {
				t.Errorf("the widened stretch %+v holds %+v: %v, want %v", s, c.e, got, c.want)
			}
		})
	}
}

// TestStretchesFromRows checks the stretches Open rebuilds for a ledger
// whose seal frames recorded none, as an older version's, from the moment
// each row recorded: each rise of it ends a stretch, the first begins at
// the beginning of time, and each later one where the one before it ended,
// as a row records no moment by a monotonic clock where config.toml was
// seen local-only (stretch.widen); and a stretch is passed once a batch
// from the row that first recorded its end on was sealed online.
func TestStretchesFromRows(t *testing.T) {
	type row struct {
		sealed, until int64
		online        bool
	}
	type want struct {
		from, to int64
		passed   bool
	}
	cases := []struct {
		name string
		rows []row
		want []want
	}{
		{"rows that recorded no moment rebuild no stretch",
			[]row{{100, 0, true}, {200, 0, true}}, nil},
		{"the first stretch begins at the beginning of time, and is passed by the online batch that first records its end",
			[]row{{100, 0, false}, {200, 150, true}}, []want{{0, 150, true}}},
		{"a stretch whose end only local-only batches record isn't passed",
			[]row{{100, 0, false}, {200, 150, false}}, []want{{0, 150, false}}},
		{"a later stretch begins where the one before it ended, though batches were sealed online between them",
			[]row{{100, 0, false}, {200, 150, true}, {300, 150, true}, {400, 150, true}, {600, 550, false}, {700, 550, true}},
			[]want{{0, 150, true}, {150, 550, true}}},
		{"and so does one with no batch sealed online between",
			[]row{{100, 0, false}, {200, 150, false}, {600, 550, true}}, []want{{0, 150, true}, {150, 550, true}}},
		{"a stretch whose end only local-only batches record is passed once a later batch is sealed online",
			[]row{{100, 0, false}, {200, 150, false}, {300, 150, false}, {700, 150, true}}, []want{{0, 150, true}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := &Writer{}
			var rows []rowPayload
			for _, r := range c.rows {
				on := r.online
				rows = append(rows, rowPayload{sealPayload: sealPayload{SealedAt: r.sealed, Online: &on, LocalUntil: r.until}})
			}
			var got []want
			for _, s := range w.stretchesFromRows(rows, 0, nil, moment{}) {
				got = append(got, want{s.from.m.wall, s.to.m.wall, s.passed})
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("stretches %+v, want %+v", got, c.want)
			}
		})
	}
}

// TestMergeStretches checks that past maxStretches two stretches become one
// with both's drops, so the list stays bounded and only more of the past
// counts as local-only: of the oldest adjacent pair alike passed or not,
// from the earlier start to the later end by the wall clock, as after a step
// back of the wall clock between them, and, when no pair agrees, the two
// oldest, passed when either is; and, as learned keeps the list in time
// order, a hook call a stretch held by the OS's clock before the merges is
// held after them too.
func TestMergeStretches(t *testing.T) {
	// list is maxStretches+1 stretches, the i-th from 1000*i to 1000*i+500 by
	// the wall clock and the chain's first from the beginning of time,
	// passed as passed says, each with one drop.
	list := func(passed func(i int) bool) []stretch {
		var out []stretch
		for i := range maxStretches + 1 {
			at := int64(i) * 1000
			s := stretch{from: edgeAt(moment{wall: at}), to: edgeAt(moment{wall: at + 500}), passed: passed(i), dropped: 1}
			if i == 0 {
				s.from = edge{}
			}
			out = append(out, s)
		}
		return out
	}
	all := func(int) bool { return true }
	// stepped is list(all) with the second stretch moved back by the wall
	// clock to 300 to 400, before the first's end at 500, as after a step
	// back between the two oldest.
	stepped := func() []stretch {
		l := list(all)
		l[1].from, l[1].to = edgeAt(moment{wall: 300}), edgeAt(moment{wall: 400})
		return l
	}
	// later is list with only the first stretch passed, and the third moved
	// back by the wall clock to 800 to 900, before the second's start at
	// 1000, so the pair that merges starts earlier by the second's start.
	later := func() []stretch {
		l := list(func(i int) bool { return i == 0 })
		l[2].from, l[2].to = edgeAt(moment{wall: 800}), edgeAt(moment{wall: 900})
		return l
	}
	// learnedAfterStep is maxStretches+2 stretches, as learned leaves them, in
	// one boot whose wall clock stepped back 60 s at 45 s by the OS's: the
	// install's, to 10 s, passed; one from 50 s to 60 s by the OS's clock;
	// and one the writer missed, which ended at 40 s by the OS's clock but
	// after 60 s by the wall clock, which the agent's Seen gives after a
	// batch sealed online at 65 s, so passed; then later ones, passed, so two
	// merges join the three. Each has one drop.
	const stepBoot = "b_step"
	stepAt := func(ms int64) moment {
		wall := ms + 1_000_000
		if ms > 45_000 {
			wall -= 60_000
		}
		return moment{wall: wall, os: ms, boot: stepBoot}
	}
	learnedAfterStep := func() []stretch {
		w := &Writer{boot: stepBoot, onlineAt: edgeAt(stepAt(65_000)), onlineRun: stepAt(65_000), stretches: []stretch{
			{to: edgeAt(stepAt(10_000)), passed: true},
			{from: edgeAt(stepAt(50_000)), to: edgeAt(stepAt(60_000)), passed: true},
		}}
		w.learned(edgeAt(stepAt(40_000)), false)
		for i := range maxStretches - 1 {
			at := int64(70_000 + 1000*i)
			w.stretches = append(w.stretches, stretch{from: edgeAt(stepAt(at)), to: edgeAt(stepAt(at + 500)), passed: true})
		}
		for i := range w.stretches {
			w.stretches[i].dropped = 1
		}
		return w.stretches
	}
	// call is a hook call of stepBoot made ms into the boot by its clock.
	call := func(ms int64) event.Envelope {
		return event.Envelope{T: stepAt(ms).wall, Mono: ms, BootID: stepBoot}
	}
	cases := []struct {
		name string
		in   []stretch
		// merged is the index of the stretch the merges make, from and to its
		// edges' wall times, and passed its state.
		merged   int
		from, to int64
		passed   bool
		// held are events a stretch of in holds that one of the list after
		// the merges must hold too.
		held []event.Envelope
	}{
		{"the two oldest, both passed, become one from the first's start to the second's end", list(all), 0, 0, 1500, true, nil},
		{"after a step back of the wall clock between them, the one they become ends where the later end is by the wall clock", stepped(), 0, 0, 500, true, nil},
		{"a passed stretch before unpassed ones stays as it is, and the oldest unpassed pair merges", list(func(i int) bool { return i == 0 }), 1, 1000, 2500, false, nil},
		{"with no adjacent pair alike passed or not, the two oldest merge, passed as either is", list(func(i int) bool { return i%2 == 0 }), 0, 0, 1500, true, nil},
		{"after a step back of the wall clock between their starts, the one they become starts where the earlier start is by the wall clock", later(), 1, 800, 1500, false, nil},
		{"a stretch learned after a step back of the wall clock goes in time order, so the merges keep the later stretch's end by the OS's clock",
			learnedAfterStep(), 0, 0, stepAt(40_000).wall, true, []event.Envelope{call(55_000), call(60_000), call(30_000)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, b := c.in[c.merged], c.in[c.merged+1]
			before := slices.Clone(c.in[:c.merged])
			var heldBefore []bool
			for _, e := range c.held {
				heldBefore = append(heldBefore, slices.ContainsFunc(c.in, func(s stretch) bool { return s.holds(e) }))
			}
			w := &Writer{stretches: slices.Clone(c.in)}
			w.mergeStretches()
			m := w.stretches[c.merged]
			heldAfter := true
			for i, e := range c.held {
				heldAfter = heldAfter && heldBefore[i] && slices.ContainsFunc(w.stretches, func(s stretch) bool { return s.holds(e) })
			}
			merged := uint64(len(c.in) - maxStretches + 1)
			checks := []struct {
				name string
				ok   bool
			}{
				{"the list holds maxStretches stretches", len(w.stretches) == maxStretches},
				{"the stretch the merge makes has the edges and state it should", m.from.m.wall == c.from && m.to.m.wall == c.to && m.passed == c.passed},
				{"it holds what either held by the wall clock", m.holds(event.Envelope{T: a.to.m.wall}) && m.holds(event.Envelope{T: b.to.m.wall}) && m.holds(event.Envelope{T: b.from.m.wall})},
				{"and the drops of each stretch it took in, so no drop is lost", m.dropped == merged && w.lateDropped() == uint64(len(c.in))},
				{"the stretches before it stay as they were", slices.Equal(w.stretches[:c.merged], before)},
				{"what a stretch held by the OS's clock, a stretch holds after the merges", heldAfter},
			}
			for _, ch := range checks {
				t.Run(ch.name, func(t *testing.T) {
					if !ch.ok {
						t.Errorf("no: %d stretches, the merged one %+v", len(w.stretches), m)
					}
				})
			}
		})
	}
}

// TestLearnedOrder checks where learned puts a stretch the agent saw end
// that the writer didn't know of, so the list stays in time order for
// mergeStretches: right after the newest stretch that ended before it, by
// the clocks the two share, which after a step back of the wall clock can
// be before a stretch the wall clock dates earlier, and which puts every
// stretch an earlier run recorded before one of this run, whatever the wall
// clock says; before the stretch going on; and first, from the beginning of
// time, when none ended before it. A stretch whose start holds no reading
// of its end's boot holds that boot's hook calls from its start, and one
// whose wall bounds the clocks' order leaves inverted holds what the wall
// clock alone dates from the end before its own by that clock.
func TestLearnedOrder(t *testing.T) {
	const boot = "b_order"
	// at is ms into the boot, whose wall clock stepped back 60 s at 45 s.
	at := func(ms int64) moment {
		wall := ms + 1_000_000
		if ms > 45_000 {
			wall -= 60_000
		}
		return moment{wall: wall, os: ms, boot: boot}
	}
	// ended is a stretch from from to to by the OS's clock, passed, from the
	// beginning of time for from 0.
	ended := func(from, to int64) stretch {
		s := stretch{to: edgeAt(at(to)), passed: true}
		if from > 0 {
			s.from = edgeAt(at(from))
		}
		return s
	}
	// restored is an edge an earlier run recorded at wall in boot b_before,
	// as Open reads it back in boot (Writer.earlierEdge): this run's start
	// and boot's, 1, with b_before's own reading, os, kept.
	restored := func(wall, os int64) edge {
		return edge{m: moment{wall: wall, run: 1, os: 1, boot: boot}, other: moment{os: os, boot: "b_before"}}
	}
	cases := []struct {
		name string
		list []stretch
		// online is where the writer last knew config.toml said online, and
		// end where the stretch the agent saw ended.
		online, end edge
		// want is where the learned stretch goes, and from where it begins
		// by the OS's clock, -1 for the beginning of time; held are events it
		// must hold.
		want int
		from int64
		held []event.Envelope
	}{
		{"one that ended after every stretch goes last, from where the writer last knew the file said online",
			[]stretch{ended(0, 10_000), ended(20_000, 30_000)}, edgeAt(at(35_000)), edgeAt(at(40_000)), 2, 35_000, nil},
		{"one the wall clock dates after the newest stretch, after a step back, but the OS's clock before it, goes right after the newest that ended before it by the OS's clock",
			[]stretch{ended(0, 10_000), ended(50_000, 60_000)}, edgeAt(at(65_000)), edgeAt(at(40_000)), 1, 10_000, nil},
		{"one that ended before the stretch going on goes before it",
			[]stretch{ended(0, 10_000), {from: edgeAt(at(50_000)), open: true}}, edgeAt(at(45_000)), edgeAt(at(40_000)), 1, 10_000, nil},
		{"one that ended before every stretch goes first, from the beginning of time",
			[]stretch{ended(0, 10_000), ended(20_000, 30_000)}, edgeAt(at(35_000)), edgeAt(at(5_000)), 0, -1, nil},
		{"one of this run goes after every stretch an earlier run recorded, though the wall clock reads behind them after a reboot, holds this boot's hook calls from its start, and holds what the wall clock alone dates from the end before its own by that clock",
			[]stretch{{to: restored(1_000_000, 50_000), passed: true}, {from: restored(1_100_000, 60_000), to: restored(1_200_000, 70_000), passed: true}},
			edge{m: moment{wall: 1_205_000, run: 1}}, edgeAt(moment{wall: 1_110_000, run: 5_000, os: 40_000, boot: boot}), 2, 0,
			// A hook call of this boot whose wall time is before every edge's,
			// and a record with no monotonic reading, which the wall clock dates
			// between the first stretch's end and the learned one's.
			[]event.Envelope{{T: 950_000, Mono: 35_000, BootID: boot}, {T: 1_090_000}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := &Writer{boot: boot, onlineAt: c.online, stretches: slices.Clone(c.list)}
			w.learned(c.end, false)
			got := slices.IndexFunc(w.stretches, func(s stretch) bool { return !s.open && s.to.m.os == c.end.m.os })
			ordered := true
			for i := 1; i < len(w.stretches); i++ {
				if prev, s := w.stretches[i-1], w.stretches[i]; prev.open || !s.open && s.to.m.os < prev.to.m.os {
					ordered = false
				}
			}
			from, held := int64(-1), got >= 0
			if got >= 0 {
				if s := w.stretches[got]; s.from != (edge{}) {
					from = s.from.m.os
				}
				for _, e := range c.held {
					held = held && w.stretches[got].holds(e)
				}
			}
			if got != c.want || from != c.from || !ordered || !held || len(w.stretches) != len(c.list)+1 {
				t.Errorf("the learned stretch is at %d, from %d by the OS's clock, holding the events %v, in order %v, of %d; want at %d, from %d, holding them, in order, of %d",
					got, from, held, ordered, len(w.stretches), c.want, c.from, len(c.list)+1)
			}
		})
	}
}

// TestRebuiltStretchesKeepTheCap checks that the stretches Open rebuilds
// from an older version's rows, which recorded none, keep to maxStretches
// too, with the newest end where the newest row put it.
func TestRebuiltStretchesKeepTheCap(t *testing.T) {
	cases := []struct {
		name  string
		rises int
	}{
		{"rows whose moment rose maxStretches times rebuild a stretch for each", maxStretches},
		{"rows whose moment rose more times rebuild maxStretches, the oldest merged", maxStretches + 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Each rise is a batch sealed local-only, then one sealed online
			// that records the stretch's end.
			var rows []rowPayload
			local, online := false, true
			for i := range c.rises {
				at := int64(i+1) * 10_000
				rows = append(rows,
					rowPayload{sealPayload: sealPayload{SealedAt: at - 5000, Online: &local, LocalUntil: at - 10_000}},
					rowPayload{sealPayload: sealPayload{SealedAt: at + 1000, Online: &online, LocalUntil: at}})
			}
			w := &Writer{}
			w.restoreStretches(&batchesFile{base: basePayload{B: 1, FirstSeq: 1}, rows: rows}, nil)
			n := len(w.stretches)
			if want := min(c.rises, maxStretches); n != want || w.stretches[n-1].to.m.wall != int64(c.rises)*10_000 || w.stretches[0].from != (edge{}) {
				t.Errorf("rebuilt %d stretches, the newest ending at %d and the first from %+v; want %d, ending at %d, from the beginning of time",
					n, w.stretches[n-1].to.m.wall, w.stretches[0].from, want, int64(c.rises)*10_000)
			}
		})
	}
}

// TestFoldStretches checks the fold a sweep makes at the start of its day,
// cut (Sweep), as the writer's own stretches and a seal frame's take it
// alike: every stretch that began before cut becomes part of one from the
// beginning of time to cut, or to the latest end of those that ended after
// it, or one that goes on when one of them does, passed when all of them
// are, with all their drops, so no time config.toml changed mode before
// cut stays; those that began from cut on stay as they are; and a fold
// that already reaches cut, as the chain's first stretch ending after it
// does, changes nothing.
func TestFoldStretches(t *testing.T) {
	const cut = int64(100_000)
	type st struct {
		from, to     int64
		open, passed bool
		dropped      uint64
	}
	cases := []struct {
		name    string
		in      []st
		want    []st
		changed bool
	}{
		{"stretches that ended before the cut become one from the beginning of time to the cut, passed, with their drops",
			[]st{{0, 10_000, false, true, 1}, {20_000, 30_000, false, true, 2}, {150_000, 160_000, false, true, 3}},
			[]st{{0, cut, false, true, 3}, {150_000, 160_000, false, true, 3}}, true},
		{"one that began before the cut and ended after it ends the fold, which begins at the beginning of time",
			[]st{{0, 10_000, false, true, 0}, {90_000, 120_000, false, true, 1}}, []st{{0, 120_000, false, true, 1}}, true},
		{"one going on that began before the cut goes on from the beginning of time",
			[]st{{0, 10_000, false, true, 1}, {90_000, 0, true, false, 0}}, []st{{0, 0, true, false, 1}}, true},
		{"one that no batch sealed online passed leaves the fold unpassed",
			[]st{{0, 10_000, false, true, 0}, {20_000, 30_000, false, false, 0}}, []st{{0, cut, false, false, 0}}, true},
		{"a fold the cut has moved past ends at the new cut", []st{{0, 50_000, false, true, 4}}, []st{{0, cut, false, true, 4}}, true},
		{"stretches that began from the cut on stay as they are",
			[]st{{0, 120_000, false, true, 0}, {cut, 130_000, false, true, 0}, {140_000, 0, true, false, 0}},
			[]st{{0, 120_000, false, true, 0}, {cut, 130_000, false, true, 0}, {140_000, 0, true, false, 0}}, false},
		{"a fold that already reaches the cut changes nothing", []st{{0, cut, false, true, 4}}, []st{{0, cut, false, true, 4}}, false},
		{"nor does the chain's first stretch going on", []st{{0, 0, true, false, 0}}, []st{{0, 0, true, false, 0}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var ps []stretchPayload
			w := &Writer{}
			for _, x := range c.in {
				ps = append(ps, stretchPayload{From: x.from, To: x.to, Open: x.open, Passed: x.passed, Dropped: x.dropped})
				s := stretch{open: x.open, passed: x.passed, dropped: x.dropped}
				if x.from != 0 {
					s.from = edgeAt(moment{wall: x.from})
				}
				if !x.open {
					s.to = edgeAt(moment{wall: x.to})
				}
				w.stretches = append(w.stretches, s)
			}
			folded, changed := foldPayloads(ps, cut)
			var gotPayloads []st
			for _, p := range folded {
				if p.FromOS != nil || p.ToOS != nil {
					t.Errorf("the folded payload %+v keeps a reading of the OS's clock", p)
				}
				gotPayloads = append(gotPayloads, st{p.From, p.To, p.Open, p.Passed, p.Dropped})
			}
			w.foldStretches(cut)
			var gotStretches []st
			for _, s := range w.stretches {
				gotStretches = append(gotStretches, st{s.from.m.wall, s.to.m.wall, s.open, s.passed, s.dropped})
			}
			if !slices.Equal(gotPayloads, c.want) || changed != c.changed {
				t.Errorf("a seal frame's stretches fold to %+v, changed %v; want %+v, %v", gotPayloads, changed, c.want, c.changed)
			}
			if !slices.Equal(gotStretches, c.want) || w.dirty != c.changed {
				t.Errorf("the writer's fold to %+v, to be recorded %v; want %+v, %v", gotStretches, w.dirty, c.want, c.changed)
			}
		})
	}
}

// lateRig is a writer whose config.toml mode, clocks and Waiting a test
// sets, with every batch it seals, for the tests of late records below.
type lateRig struct {
	t     *testing.T
	dir   string
	clk   *clock
	file  atomic.Bool // config.toml says online
	waits atomic.Bool
	// held has Waiting say yes while any stretch no batch sealed online has
	// passed is on the list, as for a hook call held whose times the
	// adapters can't read yet (agent's adapterHost.waiting).
	held    atomic.Bool
	sealed  chan Batch
	w       *Writer
	rng     *rand.Rand
	seen    Seen
	batches []Batch
}

// newLateRig opens a rig's writer, config.toml saying online.
func newLateRig(t *testing.T) *lateRig {
	r := &lateRig{t: t, dir: t.TempDir(), clk: newClock(testT), sealed: make(chan Batch, 64), rng: rand.New(rand.NewPCG(41, 42))}
	r.file.Store(true)
	r.open()
	return r
}

// pause gives a seal the writer asked for of its own, as a note's, time to
// run first, as TestNoteMode's "pause" does.
func (r *lateRig) pause() { time.Sleep(200 * time.Millisecond) }

// lateBoot is the boot the rig's OS clock counts in.
const lateBoot = "b_late"

// open opens the writer, with what the agent saw as Seen. The OS's clock
// reads the ms since the rig's start, plus 1000, in one boot, as a real
// one goes on across a restart, so the moments the writer takes share it,
// as they share a run's clock outside the tests (stretch.widen). The
// rig's tests only move the clock on.
func (r *lateRig) open() {
	r.w = open(r.t, r.dir, r.clk, func(o *Options) {
		o.Online = r.file.Load
		o.Seen = func() Seen { return r.seen }
		o.Sealed = func(b Batch) { r.sealed <- b }
		o.Waiting = func(before func(event.Envelope) bool) bool {
			return r.waits.Load() || r.held.Load() && before(event.Envelope{})
		}
		o.Clock = func() osclock.Stamp {
			now := r.clk.now()
			return osclock.Stamp{Wall: now, Mono: now.UnixMilli() - testT + 1000, BootID: lateBoot}
		}
	})
}

// at sets the clock to ms after the start.
func (r *lateRig) at(ms int64) { r.clk.set(testT + ms) }

// note has config.toml say online or local-only, and the watch note it.
func (r *lateRig) note(online bool) {
	r.file.Store(online)
	r.w.NoteMode(online)
}

// write writes in, and fails the test on any error.
func (r *lateRig) write(in Input) {
	r.t.Helper()
	if _, err := r.w.Write(in); err != nil {
		r.t.Fatalf("Write: %v", err)
	}
}

// presence is an event made ms after the start, judged by its t alone.
func (r *lateRig) presence(ms int64) Input {
	return sampleInput(r.rng, "os.presence", testT+ms, "", "")
}

// logged is an event of kind that a log reader read made ms after the
// start, with this run's boot_id and mono 0, as the transcript reader
// dates a line it reads late.
func (r *lateRig) logged(kind string, ms int64) Input {
	in := sampleInput(r.rng, kind, testT+ms, "s_1", "p_web")
	in.Event.Source, in.Event.Via = event.SourceLog, event.ViaLog
	in.Event.Mono, in.Event.BootID = 0, runBoot
	return in
}

// seal seals, and keeps the batch sealed, which may be one a seal the
// writer owed sealed first.
func (r *lateRig) seal() Batch {
	r.t.Helper()
	if _, _, err := r.w.Seal(); err != nil {
		r.t.Fatalf("Seal: %v", err)
	}
	return r.take()
}

// take waits for the next batch sealed.
func (r *lateRig) take() Batch {
	r.t.Helper()
	select {
	case b := <-r.sealed:
		r.batches = append(r.batches, b)
		return b
	case <-time.After(10 * time.Second):
		r.t.Fatalf("no batch %d was sealed", len(r.batches)+1)
	}
	return Batch{}
}

// localAfterOnline reports whether any batch r sealed is local-only after
// one sealed online, which would stop the batch roots until the next
// sign-in (ingest-v1 5.3), unless a re-link came between.
func (r *lateRig) localAfterOnline() bool {
	seenOnline := false
	for _, b := range r.batches {
		if b.Online {
			seenOnline = true
		} else if seenOnline {
			return true
		}
	}
	return false
}

// TestAlpha5Replay replays what stopped a linked test computer's uploads on
// 2026-11-23 (docs/errata/2026-11-23-late-local-records.md): an install that starts
// local-only, a link, and a minute after it a Rewind of a transcript the
// tailers took up at its end, which hands the writer that file's last old
// turn's final message and stop again, dated days before the chain began,
// with this run's boot_id and mono 0, beside the live hook records of the
// minute. Lookback's records count by when the writer took them in
// (Writer.lookback), so their batch is online, as after a logout and a
// login too; and the same records dated after the chain began, in the
// install's stretch, are dropped rather than seal a local-only batch after
// the online ones.
func TestAlpha5Replay(t *testing.T) {
	const day = int64(24 * 3600 * 1000)
	cases := []struct {
		name string
		// relink has the computer log out and in again before the replay.
		relink bool
		// made is when the replayed records were made, in ms after the
		// chain began.
		made int64
		// dropped is how many of them Write drops.
		dropped int
	}{
		{"an old turn's final message and stop that a Rewind hands the writer after the link, dated before the chain began, are sealed online with the minute's live records", false, -4 * day, 0},
		{"so are they after a logout and a login", true, -4 * day, 0},
		{"dated in the install's stretch, after the chain began, they are dropped, and the minute's live records are sealed online", false, 5000, 2},
		{"so are they after a logout and a login, as a batch sealed online passed that stretch", true, 5000, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newLateRig(t)
			// Every install starts local-only.
			r.note(false)
			r.write(sampleInput(r.rng, "prompt.submit", testT+1000, "s_1", "p_web"))
			r.take()
			// pairtime login, and the minute's prompts after it.
			r.at(10_000)
			r.note(true)
			r.pause()
			r.at(20_000)
			r.write(sampleInput(r.rng, "prompt.submit", testT+20_000, "s_2", "p_web"))
			r.take()
			if c.relink {
				r.at(30_000)
				r.note(false)
				r.write(r.presence(31_000))
				r.take()
				r.at(40_000)
				r.note(true)
				r.pause()
				r.write(sampleInput(r.rng, "prompt.submit", testT+41_000, "s_2", "p_web"))
				r.take()
			}
			r.at(60_000)
			drops := 0
			var seqs []uint64 // the sequence numbers the replayed records took
			for _, kind := range []string{"agent.final", "agent.stop"} {
				seq, err := r.w.Write(r.logged(kind, c.made))
				switch {
				case errors.Is(err, ErrLateLocal):
					drops++
				case err != nil:
					t.Fatal(err)
				default:
					seqs = append(seqs, seq)
				}
			}
			r.write(sampleInput(r.rng, "agent.tool", testT+60_000, "s_2", "p_web"))
			r.write(sampleInput(r.rng, "agent.stop", testT+61_000, "s_2", "p_web"))
			// The first batch sealed after the replay holds what of it Write
			// took, as nothing else was pending: its own stop asks for that
			// seal, which may come before the live records are written.
			last := r.take()
			held := len(seqs) == 2-c.dropped
			for _, seq := range seqs {
				held = held && seq >= last.Row.FirstSeq && seq < last.Row.FirstSeq+last.Row.N
			}
			if c.dropped > 0 {
				// The live agent.tool and agent.stop alone, as the stop asks for
				// the seal.
				held = held && len(last.Records) == 2
			}
			checks := []struct {
				name string
				ok   bool
			}{
				{"the replay's batch is sealed online", last.Online && last.Why == nil},
				{"no batch is sealed local-only after one sealed online but in a logout's stretch", !r.localAfterOnline() || c.relink && !r.batches[2].Online && r.batches[3].Online && last.Online},
				{"Write drops what the stretch holds", drops == c.dropped && r.w.Status().LateLocal == uint64(c.dropped)},
				{"and the batch sealed after the replay holds the rest of it, and nothing it dropped", held},
			}
			for _, ch := range checks {
				t.Run(ch.name, func(t *testing.T) {
					if !ch.ok {
						t.Errorf("no: %d dropped; batches online %v; the replay took seqs %v, the batch after it %+v with %d records", drops, onlines(r.batches), seqs, last.Row, len(last.Records))
					}
				})
			}
		})
	}
}

// onlines lists whether each batch was sealed online.
func onlines(batches []Batch) []bool {
	var out []bool
	for _, b := range batches {
		out = append(out, b.Online)
	}
	return out
}

// TestLocalWhy checks why a batch sealed local-only says it is, in what
// Sealed gets, in its row and in its seal frame: config.toml said
// local-only as it was sealed, a record came while the writer took the file
// as local-only, something of a stretch was still on its way, or records
// of a stretch came late, named by their kinds and their own times; and a
// batch sealed online says nothing.
func TestLocalWhy(t *testing.T) {
	cases := []struct {
		name string
		run  func(r *lateRig)
		want *LocalWhy
	}{
		{"a batch sealed while config.toml says local-only says so", func(r *lateRig) {
			r.pause()
			r.file.Store(false)
			r.write(r.presence(1000))
			r.seal()
		}, &LocalWhy{Config: true}},
		{"one holding a record written while the watch's note said local-only, sealed once the file says online again, says the record came then",
			func(r *lateRig) {
				r.w.NoteMode(false)
				r.write(r.presence(1000))
				r.take()
			}, &LocalWhy{Noted: true}},
		{"one sealed while something of the stretch was on its way says so", func(r *lateRig) {
			r.note(false)
			r.write(r.presence(1000))
			r.take()
			r.at(5000)
			r.note(true)
			r.pause()
			r.waits.Store(true)
			r.write(r.presence(6000))
			r.seal()
		}, &LocalWhy{Waiting: true}},
		{"one holding records of a stretch that came late names them by their kinds and their own times", func(r *lateRig) {
			r.note(false)
			r.write(r.presence(1000))
			r.take()
			r.at(5000)
			r.note(true)
			r.pause()
			r.write(r.logged("agent.final", 2000))
			r.write(r.logged("agent.stop", 3000))
			r.seal()
		}, &LocalWhy{Late: []LateRecord{{Kind: event.KindAgentOutput, T: time.UnixMilli(testT + 2000).UTC()},
			{Kind: event.KindAgentStop, T: time.UnixMilli(testT + 3000).UTC()}}, LateN: 2}},
		{"one holding more than the seal frame names counts them all", func(r *lateRig) {
			r.note(false)
			r.write(r.presence(1000))
			r.take()
			r.at(9000)
			r.note(true)
			r.pause()
			for i := range lateKept + 2 {
				r.write(r.presence(int64(2000 + i)))
			}
			r.seal()
		}, &LocalWhy{Late: []LateRecord{{Kind: event.KindOSPresence, T: time.UnixMilli(testT + 2000).UTC()},
			{Kind: event.KindOSPresence, T: time.UnixMilli(testT + 2001).UTC()}, {Kind: event.KindOSPresence, T: time.UnixMilli(testT + 2002).UTC()},
			{Kind: event.KindOSPresence, T: time.UnixMilli(testT + 2003).UTC()}}, LateN: lateKept + 2}},
		{"one holding a lookback record a kill left, which Open judges by when it can have come, names it by its own time", func(r *lateRig) {
			r.note(false)
			r.write(r.presence(1000))
			r.take()
			r.at(5000)
			r.note(true)
			r.pause()
			r.write(r.logged("agent.output", -24*3600*1000))
			abandon(r.w)
			r.open()
			r.take()
		}, &LocalWhy{Late: []LateRecord{{Kind: event.KindAgentOutput, T: time.UnixMilli(testT - 24*3600*1000).UTC()}}, LateN: 1}},
		{"a batch sealed online says nothing", func(r *lateRig) {
			r.write(r.presence(1000))
			r.seal()
		}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newLateRig(t)
			c.run(r)
			last := r.batches[len(r.batches)-1]
			_, rows, err := ReadRows(r.dir, last.Row.B)
			if err != nil || len(rows) != 1 {
				t.Fatalf("ReadRows: %d rows, %v", len(rows), err)
			}
			read := readAll(t, r.dir)
			for _, got := range []struct {
				from string
				why  *LocalWhy
			}{{"Sealed", last.Why}, {"the row", rows[0].Why}, {"the seal frame", read[len(read)-1].Why}} {
				if !sameWhy(got.why, c.want) {
					t.Errorf("%s says %+v, want %+v", got.from, got.why, c.want)
				}
			}
		})
	}
}

// sameWhy reports whether a and b say the same.
func sameWhy(a, b *LocalWhy) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Config == b.Config && a.Noted == b.Noted && a.Waiting == b.Waiting && a.LateN == b.LateN &&
		slices.EqualFunc(a.Late, b.Late, func(x, y LateRecord) bool { return x.Kind == y.Kind && x.T.Equal(y.T) })
}

// TestStretchesKept checks that the stretches outlast what drops the seal
// frame that recorded them: a ledger an older version wrote, whose seal
// frames recorded none, gets them back from its rows' moments; rows an
// older version sealed after the newest that records them, as after a
// downgrade, add the stretches their moments end, and end the stretch that
// list has going on, as a sign-in there does; and a retention sweep that
// drops the row that recorded them keeps them in its base frame, folded at
// its day (Sweep). A late record made in a passed stretch is dropped after
// each, and one made after the second ended is sealed online but where the
// sweep folded it. One made between the stretches is sealed online where
// the writer knows where the second began, and dropped where that was
// rebuilt from rows, which hold no moment where it began, or folded.
func TestStretchesKept(t *testing.T) {
	cases := []struct {
		name string
		// lose leaves the ledger in dir as the case says, the writer closed.
		lose func(t *testing.T, r *lateRig)
		// reopen is when the writer opens again, in ms after the start.
		reopen int64
		// between is whether a record made between the stretches is sealed
		// online, and after whether one made after the second ended is.
		between, after bool
	}{
		{"a ledger whose seal frames recorded no stretches, as an older version's, rebuilds them from its rows",
			func(t *testing.T, r *lateRig) { dropStretches(t, r.dir, 1) }, 70_000, false, true},
		{"rows an older version sealed after the newest row that records stretches, as after a downgrade, add the stretches their moments end",
			func(t *testing.T, r *lateRig) { dropStretches(t, r.dir, 3) }, 70_000, false, true},
		{"rows an older version sealed after a list whose last stretch goes on, as after a downgrade and a sign-in there, end it where they say, and passed it",
			func(t *testing.T, r *lateRig) { dropStretches(t, r.dir, 6) }, 70_000, true, true},
		{"a sweep that drops every row, the one that recorded the stretches with them, keeps them in its base frame, folded into one from the beginning of time to its day",
			func(t *testing.T, r *lateRig) {
				r.open()
				after := dayOf(time.UnixMilli(testT + 2*sweepDayMS))
				if dropped, _, err := r.w.Sweep(after, 0); err != nil || dropped != len(r.batches) {
					t.Fatalf("Sweep dropped %d batches, %v; want %d", dropped, err, len(r.batches))
				}
				if err := r.w.Close(); err != nil {
					t.Fatal(err)
				}
				bf, err := readBatchesFile(filepath.Join(r.dir, batchesName))
				if err != nil {
					t.Fatal(err)
				}
				if cut := cutOf(after); len(bf.stretches) != 1 || bf.stretches[0].From != 0 || bf.stretches[0].FromOS != nil ||
					bf.stretches[0].To != cut || bf.stretches[0].ToOS != nil || !bf.stretches[0].Passed {
					t.Fatalf("the base frame keeps the stretches %+v; want one from the beginning of time to the sweep's day, %d, passed", bf.stretches, cut)
				}
			}, 2*sweepDayMS + 70_000, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newLateRig(t)
			// The install's stretch, three online batches, a second stretch,
			// whose first batch, 5, records it going on, and an online batch
			// after it, 6.
			r.note(false)
			r.write(r.presence(1000))
			r.take()
			r.at(10_000)
			r.note(true)
			r.pause()
			for _, ms := range []int64{20_000, 25_000, 30_000} {
				r.at(ms)
				r.write(r.presence(ms))
				r.seal()
			}
			r.at(40_000)
			r.note(false)
			r.write(r.presence(41_000))
			r.take()
			r.at(50_000)
			r.note(true)
			r.pause()
			r.at(60_000)
			r.write(r.presence(60_000))
			r.seal()
			if err := r.w.Close(); err != nil {
				t.Fatal(err)
			}
			c.lose(t, r)
			r.at(c.reopen)
			r.open()
			// late writes a log reader's record made ms after the start, and
			// reports whether Write dropped it as made in a passed stretch.
			late := func(ms int64) bool {
				t.Helper()
				_, err := r.w.Write(r.logged("agent.output", ms))
				if err != nil && !errors.Is(err, ErrLateLocal) {
					t.Fatal(err)
				}
				return err != nil
			}
			first, second, between, after := late(5000), late(45_000), late(22_000), late(55_000)
			r.write(r.presence(c.reopen + 1000))
			b := r.seal()
			kept := 1
			for _, online := range []bool{c.between, c.after} {
				if online {
					kept++
				}
			}
			checks := []struct {
				name string
				ok   bool
			}{
				{"a late record made in the first stretch, which batches sealed online passed, is dropped", first},
				{"so is one made in the second", second},
				{"one made between the stretches is sealed online where the writer knows where the second began, and dropped where it doesn't", between != c.between},
				{"one made after the second ended, as while an older version uploaded, is sealed online, unless the sweep folded it", after != c.after},
				{"the batch sealed after the reopen is online, with the records Write kept", b.Online && len(b.Records) == kept},
			}
			for _, ch := range checks {
				t.Run(ch.name, func(t *testing.T) {
					if !ch.ok {
						t.Errorf("no: dropped first %v, second %v, between %v, after %v; the batch online %v with %d records, want %d",
							first, second, between, after, b.Online, len(b.Records), kept)
					}
				})
			}
		})
	}
}

// TestUntilMovesRecordStretches checks that a seal frame records the
// stretches whenever the moment it records (Writer.until) moved, though
// they didn't change, as when the agent's Seen gives an end the stretch
// going on holds: a row whose moment rose past the one the list's row
// recorded, with no stretches of its own, is then an older version's
// (restoreStretches), and the next Open doesn't end the stretch going on at
// a row this version sealed later with that moment. A sweep that drops the
// list's row keeps the writer's moment, that one here, in its base frame. A record made in the
// stretch after that moment, which reaches the writer late after the
// restart, is held by it and sealed local-only, whatever config.toml says at
// the restart.
func TestUntilMovesRecordStretches(t *testing.T) {
	cases := []struct {
		name string
		// online is whether config.toml says online at the restart, and
		// sweep whether a sweep that keeps the two newest rows, and drops
		// the rest, comes before it.
		online, sweep bool
	}{
		{"config.toml online at the restart: the stretch goes on until the first read ends it", true, false},
		{"config.toml still local-only at the restart: the stretch goes on", false, false},
		{"a sweep before the restart keeps in its base frame the writer's moment, folded at its day, so the stretch still goes on", true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newLateRig(t)
			// The install's stretch, an online batch, and a second stretch,
			// whose first batch records it going on.
			r.note(false)
			r.write(r.presence(1000))
			r.take()
			r.at(10_000)
			r.note(true)
			r.pause()
			r.at(20_000)
			r.write(r.presence(20_000))
			r.seal()
			r.at(30_000)
			r.note(false)
			r.write(r.presence(31_000))
			r.take()
			// The agent saw a stretch end at 25 s, which the writer can't tell
			// from one before its own local-only take at 30 s, so it ends
			// nothing, though it moves the moment.
			r.w.NoteSeen(Seen{Ended: time.UnixMilli(testT + 25_000)})
			r.at(40_000)
			r.write(r.presence(40_000))
			r.seal()
			// A batch after it with nothing changed records no stretches, and
			// the moment the 40 s one recorded.
			r.at(45_000)
			r.write(r.presence(45_000))
			r.seal()
			if err := r.w.Close(); err != nil {
				t.Fatal(err)
			}
			bf, err := readBatchesFile(filepath.Join(r.dir, batchesName))
			if err != nil {
				t.Fatal(err)
			}
			recorded := bf.stretchesRow == len(bf.rows)-2
			reopen, based := int64(50_000), true
			if c.sweep {
				// The two newest batches stay, for the uploads (floor), as the
				// rest go, with every segment, and the stretches fold.
				r.open()
				after := dayOf(time.UnixMilli(testT + 2*sweepDayMS))
				if dropped, _, err := r.w.Sweep(after, r.batches[len(r.batches)-2].Row.B); err != nil || dropped != len(r.batches)-2 {
					t.Fatalf("Sweep dropped %d batches, %v; want %d", dropped, err, len(r.batches)-2)
				}
				if err := r.w.Close(); err != nil {
					t.Fatal(err)
				}
				swept, err := readBatchesFile(filepath.Join(r.dir, batchesName))
				if err != nil {
					t.Fatal(err)
				}
				based = swept.stretchesRow == -1 && swept.base.LocalUntil == cutOf(after) && len(swept.rows) == 2
				reopen = 3 * sweepDayMS
			}
			r.file.Store(c.online)
			r.at(reopen)
			r.open()
			// A transcript line made at 35 s, while config.toml said
			// local-only, read late.
			r.write(r.logged("agent.output", 35_000))
			r.write(r.presence(reopen + 1000))
			b := r.seal()
			checks := []struct {
				name string
				ok   bool
			}{
				{"the seal frame sealed after the moment moved records the stretches", recorded},
				{"a sweep's base frame keeps them with the writer's moment, folded at its day", based},
				{"the record made in the stretch after that moment is sealed local-only after the restart", !b.Online},
			}
			for _, ch := range checks {
				t.Run(ch.name, func(t *testing.T) {
					if !ch.ok {
						t.Errorf("no: the newest row to record the stretches is %d of %d; the batch after the restart online %v, why %+v",
							bf.stretchesRow, len(bf.rows), b.Online, b.Why)
					}
				})
			}
		})
	}
}

// TestLearnedStretch checks where a stretch begins that the agent saw end
// and the writer didn't know of (learned): where the writer last knew
// config.toml said online, and, when the writer knew that only after the
// end, as from a seal's read between the switch back and the agent's note
// of it, at the end of the newest stretch that ended before it. Either way
// a record made in it that comes late is dropped once a batch sealed online
// passed it, and one made outside it is sealed online.
func TestLearnedStretch(t *testing.T) {
	cases := []struct {
		name string
		// sealAt is when a batch is sealed online after the install's
		// stretch, endAt when the stretch the agent saw ended, and madeAt
		// when the late record was made, in ms after the start.
		sealAt, endAt, madeAt int64
		// dropped is whether Write drops the late record, or else seals it
		// online.
		dropped bool
	}{
		{"an end before the writer's last online read begins its stretch where the stretch before ended, so a record made in it is dropped", 60_000, 40_000, 35_000, true},
		{"and a record made after that end is sealed online", 60_000, 40_000, 50_000, false},
		{"an end after the writer's last online read begins its stretch at that read, so a record made in it is dropped", 20_000, 40_000, 30_000, true},
		{"and a record made before that read is sealed online", 20_000, 40_000, 15_000, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newLateRig(t)
			// The install's stretch, from the beginning of time to 10 s, and a
			// batch sealed online after it.
			r.note(false)
			r.write(r.presence(1000))
			r.take()
			r.at(10_000)
			r.note(true)
			r.pause()
			r.at(c.sealAt)
			r.write(r.presence(c.sealAt - 1000))
			r.seal()
			// The agent saw a stretch end, by both clocks, which the writer
			// didn't know of; a batch sealed online after it passes it.
			end := time.UnixMilli(testT + c.endAt)
			r.w.NoteSeen(Seen{Ended: end, OS: osclock.Stamp{Wall: end, Mono: c.endAt + 1000, BootID: lateBoot}})
			r.at(70_000)
			r.write(r.presence(70_000))
			r.seal()
			_, err := r.w.Write(r.presence(c.madeAt))
			if err != nil && !errors.Is(err, ErrLateLocal) {
				t.Fatal(err)
			}
			if dropped := err != nil; dropped != c.dropped {
				t.Errorf("Write dropped the record made at %d ms: %v, want %v", c.madeAt, dropped, c.dropped)
			}
			if err == nil {
				// One the writer took is sealed online, as no stretch holds it.
				if b := r.seal(); !b.Online || len(b.Records) != 1 {
					t.Errorf("the batch of the record made at %d ms is online %v with %d records, want online with 1", c.madeAt, b.Online, len(b.Records))
				}
			}
		})
	}
}

// TestFullSweepKeepsStretches checks what a retention sweep that drops every
// row keeps for the next Open: the writer's own stretches, as Open rebuilt
// them from an older version's rows, which recorded none, or read them from
// this version's, and its moment, folded at the sweep's day. With
// them, a stretch end the agent kept from before the sweep ends nothing
// new, so a hook call held at the first seal after a restart keeps no mark,
// and that batch is sealed online after the server's head.
func TestFullSweepKeepsStretches(t *testing.T) {
	cases := []struct {
		name string
		// older has the rows be an older version's, with no stretches.
		older bool
	}{
		{"an older version's chain keeps the stretches Open rebuilt from its rows, and the moment", true},
		{"this version's chain keeps the moment with its stretches", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newLateRig(t)
			// The install's stretch, and a batch sealed online after it.
			r.note(false)
			r.write(r.presence(1000))
			r.take()
			r.at(10_000)
			r.note(true)
			r.pause()
			r.at(20_000)
			r.write(r.presence(20_000))
			r.seal()
			if err := r.w.Close(); err != nil {
				t.Fatal(err)
			}
			if c.older {
				dropStretches(t, r.dir, 1)
			}
			// Open, which rebuilds an older version's stretches, seals
			// nothing before the sweep drops every row.
			r.open()
			after := dayOf(time.UnixMilli(testT + 2*sweepDayMS))
			if dropped, _, err := r.w.Sweep(after, 0); err != nil || dropped != len(r.batches) {
				t.Fatalf("Sweep dropped %d batches, %v; want %d", dropped, err, len(r.batches))
			}
			if err := r.w.Close(); err != nil {
				t.Fatal(err)
			}
			bf, err := readBatchesFile(filepath.Join(r.dir, batchesName))
			if err != nil {
				t.Fatal(err)
			}
			cut := cutOf(after)
			kept := len(bf.rows) == 0 && len(bf.stretches) == 1 && bf.stretches[0].Passed && bf.base.LocalUntil == cut
			// The agent kept the stretch's end from before the sweep, and a
			// hook call held while the keys weren't there is on its way.
			end := time.UnixMilli(testT + 10_000)
			r.seen = Seen{Ended: end, OS: osclock.Stamp{Wall: end, Mono: 11_000, BootID: lateBoot}, Earlier: true}
			r.held.Store(true)
			r.at(3 * sweepDayMS)
			r.open()
			r.write(r.presence(3*sweepDayMS + 1000))
			b := r.seal()
			checks := []struct {
				name string
				ok   bool
			}{
				{"the base frame keeps the stretches, folded, and the moment, folded at the cut", kept},
				{"the first batch after the restart is sealed online, the held call keeping no mark", b.Online},
			}
			for _, ch := range checks {
				t.Run(ch.name, func(t *testing.T) {
					if !ch.ok {
						t.Errorf("no: the base frame keeps %d rows, the stretches %+v and the moment %d (cut %d); the batch online %v, why %+v",
							len(bf.rows), bf.stretches, bf.base.LocalUntil, cut, b.Online, b.Why)
					}
				})
			}
		})
	}
}

// TestSweepFoldsMoments checks that a sweep folds the moment each kept row
// and its base frame record (sealPayload.LocalUntil) at its day, as it folds
// the stretches, so the rows keep no time config.toml changed mode before
// the ledger's 180 days, and the writer's own, so the next seal frame
// records the cut too, even when it drops no row and folds no stretch; and
// that the next Open, holding the kept rows to the base frame's moment,
// ends no stretch at them.
func TestSweepFoldsMoments(t *testing.T) {
	// online is the install's stretch, two batches sealed online after it,
	// and, when open is set, a second stretch, going on. The rows from batch
	// 2 on stay, as unsent (floor), while their moment, 10 s, is days before
	// the sweep's.
	online := func(open bool) func(r *lateRig) (string, uint64) {
		return func(r *lateRig) (string, uint64) {
			r.note(false)
			r.write(r.presence(1000))
			r.take()
			r.at(10_000)
			r.note(true)
			r.pause()
			for _, ms := range []int64{20_000, 25_000} {
				r.at(ms)
				r.write(r.presence(ms))
				r.seal()
			}
			if open {
				r.at(30_000)
				r.note(false)
				r.write(r.presence(31_000))
				r.take()
			}
			return dayOf(time.UnixMilli(testT + 2*sweepDayMS)), 2
		}
	}
	cases := []struct {
		name string
		// build writes the ledger and gives the sweep's day and floor.
		build func(r *lateRig) (string, uint64)
		// open is whether a stretch goes on as the sweep comes.
		open bool
	}{
		{"with every stretch ended, the rows' moments fold to the cut and the fold stays as the sweep left it", online(false), false},
		{"with a stretch going on, the next Open keeps it going on, as no kept row rose past the base frame's moment", online(true), true},
		{"a sweep that drops no row and folds no stretch folds the rows' moments all the same", unsweptMoment, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newLateRig(t)
			after, floor := c.build(r)
			if _, _, err := r.w.Sweep(after, floor); err != nil {
				t.Fatal(err)
			}
			cut := cutOf(after)
			r.w.mu.Lock()
			own := r.w.until
			r.w.mu.Unlock()
			if err := r.w.Close(); err != nil {
				t.Fatal(err)
			}
			bf, err := readBatchesFile(filepath.Join(r.dir, batchesName))
			if err != nil {
				t.Fatal(err)
			}
			folded := len(bf.rows) > 0
			for _, rp := range bf.rows {
				folded = folded && rp.LocalUntil == cut && rp.LocalUntilOS == nil
			}
			r.at(3 * sweepDayMS)
			r.open()
			r.w.mu.Lock()
			restored := slices.Clone(r.w.stretches)
			r.w.mu.Unlock()
			again := len(restored) == 1 && restored[0].from == (edge{}) && restored[0].open == c.open
			checks := []struct {
				name string
				ok   bool
			}{
				{"every kept row's moment is the cut", folded},
				{"so is the base frame's", bf.base.LocalUntil == cut},
				{"and the writer's own, which its next seal frame records", own.wall == cut && own.os == 0},
				{"the next Open keeps the one stretch the fold made, ended or going on as it was", again},
			}
			for _, ch := range checks {
				t.Run(ch.name, func(t *testing.T) {
					if !ch.ok {
						t.Errorf("no: the rows %+v, the base frame's moment %d, the writer's %+v (cut %d); the restored stretches %+v",
							bf.rows, bf.base.LocalUntil, own, cut, restored)
					}
				})
			}
		})
	}
}

// unsweptMoment writes, for TestSweepFoldsMoments and
// TestOldChainFoldsMoments, the install's stretch, going on, whose moment
// the agent's Seen of an end inside it moved to 25 s, which ends nothing, as
// in TestUntilMovesRecordStretches, and one batch, sealed the next day,
// which records that moment. It gives that day, by which a sweep drops no
// row, and folds the stretch, from the beginning of time and going on, to
// itself, and no floor.
func unsweptMoment(r *lateRig) (string, uint64) {
	r.note(false)
	r.at(30_000)
	r.note(false)
	r.w.NoteSeen(Seen{Ended: time.UnixMilli(testT + 25_000)})
	r.at(sweepDayMS + 1000)
	r.write(r.presence(sweepDayMS + 1000))
	r.take()
	return dayOf(time.UnixMilli(testT + sweepDayMS)), 0
}

// TestOldChainFoldsMoments checks that a sweep folds the moments a chain in
// old/ keeps (sweepChain), as it does the live chain's, even when it drops
// no row of it and folds no stretch.
func TestOldChainFoldsMoments(t *testing.T) {
	r := newLateRig(t)
	after, _ := unsweptMoment(r)
	if err := r.w.Close(); err != nil {
		t.Fatal(err)
	}
	// The chain moves to a ledger whose writer has another key, as the
	// archive of a key the device no longer writes with.
	dir := t.TempDir()
	chain := filepath.Join(dir, oldDir, testKeyID)
	if err := os.MkdirAll(filepath.Dir(chain), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(r.dir, chain); err != nil {
		t.Fatal(err)
	}
	w := open(t, dir, newClock(testT+2*sweepDayMS), otherKey(t))
	if _, _, err := w.Sweep(after, 0); err != nil {
		t.Fatal(err)
	}
	bf, err := readBatchesFile(filepath.Join(chain, batchesName))
	if err != nil {
		t.Fatal(err)
	}
	cut := cutOf(after)
	checks := []struct {
		name string
		ok   bool
	}{
		{"the old chain keeps its one row", len(bf.rows) == 1},
		{"whose moment is the cut", len(bf.rows) == 1 && bf.rows[0].LocalUntil == cut && bf.rows[0].LocalUntilOS == nil},
		{"and so is its base frame's", bf.base.LocalUntil == cut && bf.base.LocalUntilOS == nil},
	}
	for _, ch := range checks {
		t.Run(ch.name, func(t *testing.T) {
			if !ch.ok {
				t.Errorf("no: the rows %+v, the base frame %+v (cut %d)", bf.rows, bf.base, cut)
			}
		})
	}
}

// TestSweepKeepsWhatOpenFound checks that a sweep's base frame keeps the
// writer's own stretches, moment and mark (Sweep), not the list
// batches.seg holds, so what Open found that no seal frame has yet
// outlasts a restart before the next seal, though the fold made the
// moments alike: the stretch going on that rows an older version sealed
// after a downgrade ended at their rise, and the one the agent's kept Seen
// ended, before the sweep's day or after it. The first batch after the
// restart is then sealed online, not local-only after the batches sealed
// online before it, with no stretch learned twice, and it takes the mark
// just when the run that took the end had it due: after the Seen's end,
// which no batch was sealed after, and not after a seal's own read of
// config.toml, which marks no batch.
func TestSweepKeepsWhatOpenFound(t *testing.T) {
	// seenAt is a stretch end the agent saw ms after the start and kept, as
	// state.json does, by both clocks.
	seenAt := func(ms int64) Seen {
		end := time.UnixMilli(testT + ms)
		return Seen{Ended: end, OS: osclock.Stamp{Wall: end, Mono: ms + 1000, BootID: lateBoot}, Earlier: true}
	}
	cases := []struct {
		name string
		// build writes the ledger, closes the writer and opens it again, as
		// the agent starts, before the sweep by after with floor.
		build func(t *testing.T, r *lateRig) (after string, floor uint64)
		// reopen is when the writer opens again after the sweep, in ms after
		// the start, and late the records written then, made before it, of
		// which the batch sealed then keeps kept.
		reopen int64
		late   func(r *lateRig) []Input
		kept   int
		// stretches is how many the writer has after the reopen, none going
		// on, and back whether the batch takes the mark.
		stretches int
		back      bool
	}{
		{"rows an older version sealed after a list with a stretch going on, as after a downgrade: the stretch stays ended and folds into the sweep's day, so a late record made before that day is dropped and the batch is sealed online",
			func(t *testing.T, r *lateRig) (string, uint64) {
				// TestStretchesKept's ledger: the install's stretch, three online
				// batches, a second stretch, whose first batch, 5, records it
				// going on, and an online batch after it, 6, as an older
				// version's row.
				r.note(false)
				r.write(r.presence(1000))
				r.take()
				r.at(10_000)
				r.note(true)
				r.pause()
				for _, ms := range []int64{20_000, 25_000, 30_000} {
					r.at(ms)
					r.write(r.presence(ms))
					r.seal()
				}
				r.at(40_000)
				r.note(false)
				r.write(r.presence(41_000))
				r.take()
				r.at(50_000)
				r.note(true)
				r.pause()
				r.at(60_000)
				r.write(r.presence(60_000))
				r.seal()
				if err := r.w.Close(); err != nil {
					t.Fatal(err)
				}
				dropStretches(t, r.dir, 6)
				// Open ends the stretch going on at batch 6's rise, which no seal
				// frame records, and the sweep keeps batch 6 for the uploads.
				r.open()
				return dayOf(time.UnixMilli(testT + 2*sweepDayMS)), 6
			},
			3 * sweepDayMS, func(r *lateRig) []Input {
				return []Input{r.logged("agent.output", 55_000), r.presence(3*sweepDayMS + 1000)}
			}, 1, 1, false},
		{"a stretch end the agent kept while the ledger was shut, before the sweep's day: the stretch stays ended, so a record made after that day is sealed online, and the batch takes the mark",
			func(t *testing.T, r *lateRig) (string, uint64) {
				// The install's stretch, an online batch, and a second stretch,
				// whose batch, 3, records it going on as the ledger shuts.
				r.note(false)
				r.write(r.presence(1000))
				r.take()
				r.at(10_000)
				r.note(true)
				r.pause()
				r.at(20_000)
				r.write(r.presence(20_000))
				r.seal()
				r.at(30_000)
				r.note(false)
				r.write(r.presence(31_000))
				r.take()
				if err := r.w.Close(); err != nil {
					t.Fatal(err)
				}
				// config.toml is back online, as the agent saw at 40 s, and Open
				// ends the stretch there; the sweep drops every batch.
				r.file.Store(true)
				r.seen = seenAt(40_000)
				r.at(2 * sweepDayMS)
				r.open()
				return dayOf(time.UnixMilli(testT + 2*sweepDayMS)), 0
			},
			3 * sweepDayMS, func(r *lateRig) []Input {
				// One made after the sweep's day and before the restart, while
				// config.toml said online, as a presence sample queued across it.
				return []Input{r.presence(2*sweepDayMS + 1000), r.presence(3*sweepDayMS + 1000)}
			}, 2, 1, true},
		{"a stretch end the agent kept while the ledger was shut, after the sweep's day: the stretch stays ended there, no stretch is learned twice, and the batch takes the mark",
			func(t *testing.T, r *lateRig) (string, uint64) {
				// The install's stretch and an online batch, then, days later,
				// another online batch and a second stretch, whose batch, 4,
				// records it going on as the ledger shuts.
				r.note(false)
				r.write(r.presence(1000))
				r.take()
				r.at(10_000)
				r.note(true)
				r.pause()
				r.at(20_000)
				r.write(r.presence(20_000))
				r.seal()
				r.at(3 * sweepDayMS)
				r.write(r.presence(3 * sweepDayMS))
				r.seal()
				r.at(3*sweepDayMS + 10_000)
				r.note(false)
				r.write(r.presence(3*sweepDayMS + 11_000))
				r.take()
				if err := r.w.Close(); err != nil {
					t.Fatal(err)
				}
				// config.toml is back online, as the agent saw 30 s into that
				// day, and Open ends the stretch there; the sweep drops the first
				// day's batches and keeps the rest.
				r.file.Store(true)
				r.seen = seenAt(3*sweepDayMS + 30_000)
				r.at(4 * sweepDayMS)
				r.open()
				return dayOf(time.UnixMilli(testT + 2*sweepDayMS)), 0
			},
			5 * sweepDayMS, func(r *lateRig) []Input { return []Input{r.presence(5*sweepDayMS + 1000)} }, 1, 2, true},
		{"a stretch a seal's own read of config.toml ended, which marks no batch, marks none after a sweep either",
			func(t *testing.T, r *lateRig) (string, uint64) {
				// The install's stretch and an online batch, then, days later,
				// another online batch and a second stretch, whose batch, 4,
				// records it going on; config.toml is back online with no note,
				// and a seal with nothing pending ends the stretch at its read.
				r.note(false)
				r.write(r.presence(1000))
				r.take()
				r.at(10_000)
				r.note(true)
				r.pause()
				r.at(20_000)
				r.write(r.presence(20_000))
				r.seal()
				r.at(3 * sweepDayMS)
				r.write(r.presence(3 * sweepDayMS))
				r.seal()
				r.at(3*sweepDayMS + 10_000)
				r.note(false)
				r.write(r.presence(3*sweepDayMS + 11_000))
				r.take()
				r.at(3*sweepDayMS + 20_000)
				r.file.Store(true)
				if _, ok, err := r.w.Seal(); ok || err != nil {
					t.Fatalf("Seal sealed a batch %v, %v; want nothing to seal", ok, err)
				}
				return dayOf(time.UnixMilli(testT + 2*sweepDayMS)), 0
			},
			5 * sweepDayMS, func(r *lateRig) []Input { return []Input{r.presence(5*sweepDayMS + 1000)} }, 1, 2, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newLateRig(t)
			after, floor := c.build(t, r)
			if _, _, err := r.w.Sweep(after, floor); err != nil {
				t.Fatal(err)
			}
			// The agent stops before the writer seals again.
			if err := r.w.Close(); err != nil {
				t.Fatal(err)
			}
			r.at(c.reopen)
			r.open()
			r.w.mu.Lock()
			restored := slices.Clone(r.w.stretches)
			r.w.mu.Unlock()
			for _, in := range c.late(r) {
				if _, err := r.w.Write(in); err != nil && !errors.Is(err, ErrLateLocal) {
					t.Fatal(err)
				}
			}
			b := r.seal()
			checks := []struct {
				name string
				ok   bool
			}{
				{"the writer has the stretches it had at the sweep, none going on", len(restored) == c.stretches && !slices.ContainsFunc(restored, func(s stretch) bool { return s.open })},
				{"the first batch after the restart is sealed online, with the records it should keep", b.Online && len(b.Records) == c.kept},
				{"and takes the mark when the end came after the sweep's day", b.Back == c.back},
			}
			for _, ch := range checks {
				t.Run(ch.name, func(t *testing.T) {
					if !ch.ok {
						t.Errorf("no: the stretches %+v; the batch online %v, back %v, with %d records, why %+v",
							restored, b.Online, b.Back, len(b.Records), b.Why)
					}
				})
			}
		})
	}
}

// TestSweepMarkLeavesPendingRecords checks that the mark a sweep's base
// frame keeps (basePayload.Back) is due again at Open only from the next
// record on: the running writer can clear the mark after the sweep without
// sealing a batch, as a seal's read that finds config.toml local-only with
// nothing pending does, and a record a kill then leaves pending may have
// been made in that stretch, which no seal frame recorded. The batch Open
// seals it in takes no mark, so the uploader keeps it in the local-only run.
func TestSweepMarkLeavesPendingRecords(t *testing.T) {
	r := newLateRig(t)
	// The install's stretch, whose batch, 1, is sealed local-only, and the
	// switch back, noted, which arms the mark for the next record; no batch
	// is sealed after it.
	r.note(false)
	r.write(r.presence(1000))
	r.take()
	r.at(10_000)
	r.note(true)
	r.pause()
	// Days later a sweep drops batch 1, and its base frame keeps the mark.
	r.at(2*sweepDayMS + 1000)
	if _, _, err := r.w.Sweep(dayOf(time.UnixMilli(testT+2*sweepDayMS)), 0); err != nil {
		t.Fatal(err)
	}
	bf, err := readBatchesFile(filepath.Join(r.dir, batchesName))
	if err != nil {
		t.Fatal(err)
	}
	// config.toml goes local-only, which a seal's read takes with nothing
	// pending: the mark goes, and a stretch begins, in memory alone.
	r.at(2*sweepDayMS + 2000)
	r.file.Store(false)
	if _, ok, err := r.w.Seal(); ok || err != nil {
		t.Fatalf("Seal sealed a batch %v, %v; want nothing to seal", ok, err)
	}
	// A record made in that stretch, and a kill before the seal it asks for:
	// the writer's loop stops first, so nothing seals it.
	w := r.w
	w.stopOnce.Do(func() {
		if w.job != nil {
			w.job.Stop()
		}
		close(w.stop)
	})
	<-w.done
	r.at(2*sweepDayMS + 3000)
	r.write(r.presence(2*sweepDayMS + 3000))
	abandon(w)
	// config.toml is online again at the restart, which the agent never
	// saw go local-only, so its Seen still has the switch back at 10 s.
	r.file.Store(true)
	r.seen = Seen{Ended: time.UnixMilli(testT + 10_000), Earlier: true}
	r.at(2*sweepDayMS + 60_000)
	r.open()
	b := r.take()
	checks := []struct {
		name string
		ok   bool
	}{
		{"the sweep's base frame keeps the mark due", bf.base.Back > 0},
		{"the batch Open seals the record the kill left in holds it", len(b.Records) == 1},
		{"and takes no mark, as the record may be of the stretch the crash erased", !b.Back},
	}
	for _, ch := range checks {
		t.Run(ch.name, func(t *testing.T) {
			if !ch.ok {
				t.Errorf("no: the base frame's mark %d; the batch online %v, back %v, with %d records", bf.base.Back, b.Online, b.Back, len(b.Records))
			}
		})
	}
}

// TestClockOrder checks how clockOrder orders two moments: by a monotonic
// clock both hold past its start, and else, an earlier run's or boot's
// start, which Writer.earlier gives every moment an earlier run recorded,
// before every reading of this run or of that boot past it, whatever the
// wall clock says; and by the wall clock when nothing orders them.
func TestClockOrder(t *testing.T) {
	const boot = "b_now"
	cases := []struct {
		name           string
		a, b           moment
		before, shared bool
	}{
		{"two readings of this run's clock order by it", moment{wall: 2000, run: 500}, moment{wall: 1000, run: 900}, true, true},
		{"two readings of one boot's clock order by it", moment{wall: 2000, os: 500, boot: boot}, moment{wall: 1000, os: 900, boot: boot}, true, true},
		{"an earlier run's end with no OS reading comes before an end of this run whose wall clock reads behind",
			moment{wall: 2000, run: 1}, moment{wall: 1000, run: 900}, true, true},
		{"and an end of this run comes after it", moment{wall: 1000, run: 900}, moment{wall: 2000, run: 1}, false, true},
		{"an earlier run's end of an older boot comes before an earlier run's end of this boot whose wall clock reads behind",
			moment{wall: 2000, run: 1, os: 1, boot: boot}, moment{wall: 1000, run: 1, os: 900, boot: boot}, true, true},
		{"two earlier runs' ends of older boots share no clock and order by the wall clock",
			moment{wall: 2000, run: 1, os: 1, boot: boot}, moment{wall: 1000, run: 1, os: 1, boot: boot}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if before, shared := clockOrder(c.a, c.b); before != c.before || shared != c.shared {
				t.Errorf("clockOrder = %v, %v; want %v, %v", before, shared, c.before, c.shared)
			}
		})
	}
}

// TestDueMarkOutlastsARestart checks that the mark due for the first batch
// after a switch back outlasts a restart when a batch sealed local-only
// first didn't take it, as for a hook call of the stretch still on its
// way, with a sweep between or none: the seal frame records the mark
// (sealPayload.Due), or, with no row left, the sweep's base frame does, and
// the first batch sealed online after the restart takes it (Batch.Back).
func TestDueMarkOutlastsARestart(t *testing.T) {
	cases := []struct {
		name string
		// sweep has a sweep that drops every row come before the restart.
		sweep bool
	}{
		{"with no sweep before the restart, the local-only batch's seal frame keeps the mark", false},
		{"with a sweep that drops every row before the restart, its base frame keeps the mark", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newLateRig(t)
			// The install's stretch, whose batch, 1, is sealed local-only,
			// and the switch back, noted, which arms the mark.
			r.note(false)
			r.write(r.presence(1000))
			r.take()
			r.at(10_000)
			r.note(true)
			r.pause()
			// A hook call of the stretch is still on its way, so the first
			// batch after the switch back is sealed local-only, which leaves
			// the mark due.
			r.held.Store(true)
			r.at(20_000)
			r.write(r.presence(20_000))
			first := r.seal()
			r.held.Store(false)
			reopen := int64(60_000)
			if c.sweep {
				reopen = 2*sweepDayMS + 60_000
				r.at(2 * sweepDayMS)
				if _, _, err := r.w.Sweep(dayOf(time.UnixMilli(testT+2*sweepDayMS)), 0); err != nil {
					t.Fatal(err)
				}
			}
			// The agent stops, and starts again with config.toml online.
			if err := r.w.Close(); err != nil {
				t.Fatal(err)
			}
			r.at(reopen)
			r.open()
			r.write(r.presence(reopen + 1000))
			b := r.seal()
			checks := []struct {
				name string
				ok   bool
			}{
				{"the first batch after the switch back is sealed local-only, for the call still on its way, and takes no mark", !first.Online && !first.Back},
				{"the first batch sealed online after the restart takes the mark", b.Online && b.Back},
			}
			for _, ch := range checks {
				t.Run(ch.name, func(t *testing.T) {
					if !ch.ok {
						t.Errorf("no: the first batch online %v, back %v, why %+v; after the restart online %v, back %v", first.Online, first.Back, first.Why, b.Online, b.Back)
					}
				})
			}
		})
	}
}

// TestSweepLeavesLiveStateToTheSeal checks that a sweep that drops no row,
// folds no stretch and folds no moment leaves batches.seg as it was, though
// the writer holds a stretch end no seal frame recorded yet: recording that
// is the next seal frame's job, not retention's. A restart before that
// seal reads the stretch as going on from the frame that recorded it, and
// the first read that finds config.toml online ends it, later than it
// ended, which only makes more count as local-only.
func TestSweepLeavesLiveStateToTheSeal(t *testing.T) {
	r := newLateRig(t)
	// The install's stretch, going on, whose batch, 1, records it, and the
	// switch back, noted with nothing pending, which no seal frame records.
	r.note(false)
	r.write(r.presence(1000))
	r.take()
	r.at(10_000)
	r.note(true)
	r.pause()
	name := filepath.Join(r.dir, batchesName)
	before, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	// A sweep by the day the chain began drops nothing and folds nothing.
	if _, _, err := r.w.Sweep(dayOf(time.UnixMilli(testT)), 0); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	r.w.mu.Lock()
	live := slices.Clone(r.w.stretches)
	r.w.mu.Unlock()
	// The agent stops before the next seal, and starts again.
	if err := r.w.Close(); err != nil {
		t.Fatal(err)
	}
	r.at(20_000)
	r.open()
	r.w.mu.Lock()
	restored := slices.Clone(r.w.stretches)
	r.w.mu.Unlock()
	checks := []struct {
		name string
		ok   bool
	}{
		{"the writer held the stretch ended at the switch back", len(live) == 1 && !live[0].open && live[0].to.m.wall == testT+10_000},
		{"the sweep left batches.seg as it was", bytes.Equal(before, after)},
		{"after the restart the stretch ends at the first read that finds config.toml online, later than it ended", len(restored) == 1 && !restored[0].open && restored[0].to.m.wall >= testT+20_000},
	}
	for _, ch := range checks {
		t.Run(ch.name, func(t *testing.T) {
			if !ch.ok {
				t.Errorf("no: the live stretches %+v, the restored %+v; batches.seg changed %v", live, restored, !bytes.Equal(before, after))
			}
		})
	}
}

// dropStretches rewrites the rows of dir's batches.seg from batch from on
// without the stretches they record, as an older version's rows, which
// recorded none. The segments keep their seal frames as they are: Open
// rebuilds no row here, so it reads no seal frame's stretches.
func dropStretches(t *testing.T, dir string, from uint64) {
	t.Helper()
	name := filepath.Join(dir, batchesName)
	var out []byte
	for _, fm := range framesOf(t, name) {
		payload := fm.payload
		if fm.kind == kindBatch {
			var m map[string]json.RawMessage
			if err := json.Unmarshal(payload, &m); err != nil {
				t.Fatal(err)
			}
			var rp struct {
				B uint64 `json:"b"`
			}
			if err := json.Unmarshal(payload, &rp); err != nil {
				t.Fatal(err)
			}
			if rp.B >= from {
				delete(m, "stretches")
			}
			var err error
			if payload, err = json.Marshal(m); err != nil {
				t.Fatal(err)
			}
		}
		out = appendFrame(out, fm.kind, payload)
	}
	if err := os.WriteFile(name, out, 0o600); err != nil {
		t.Fatal(err)
	}
}
