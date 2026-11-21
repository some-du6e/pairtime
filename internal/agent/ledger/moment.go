// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"time"

	"github.com/some-du6e/pairtime/internal/platform/clock"
	"github.com/some-du6e/pairtime/pkg/event"
)

// moment is when config.toml was last known, or taken, to say local-only
// (Options.Online, NoteMode): an event made before it may have been made,
// or may have waited, while nothing could upload it, so it counts as made
// in local-only mode (DESIGN 4.2). It holds that time by each clock an
// event's time can be held to: the wall clock, and the monotonic clocks of
// this run, which presence's, focus's and the git watcher's events carry
// (RunClock), and of the OS's boot, which `pairtime hook` stamps each hook
// call with (internal/platform/clock). A step of the wall clock moves
// neither monotonic clock, so an event that carries a reading of one the
// moment holds too is held to it by that reading alone (before): a step
// while it waited can neither hide it, nor make an event made after the
// moment count as made before.
//
// Each bound is exclusive: an event made at the moment itself was made
// once config.toml said online. Zero means no bound by that clock.
type moment struct {
	// wall is in Unix ms.
	wall int64
	// run is RunClock's reading in this run, in ms.
	run int64
	// os is the OS's monotonic clock in boot's count, in ms.
	os   int64
	boot string
}

// momentAt is at by each clock: the wall clock's at, this run's mono at at
// when at has Go's monotonic reading, as time.Now gives it and a time read
// back from disk doesn't, and s, the OS's reading taken with it. A time
// with no monotonic reading gives none of this run's clock, since one made
// from the wall clock would move with its steps.
func momentAt(at time.Time, s clock.Stamp) moment {
	m := moment{wall: at.UnixMilli()}
	if at != at.Round(0) { // Round(0) strips the monotonic reading
		m.run = runMono(at)
	}
	if s.HasMono() {
		m.os, m.boot = s.Mono, s.BootID
	}
	return m
}

// madeBefore reports whether the event e may have been made before m by
// any clock it carries that m holds: by its t, or by its mono. It is wider
// than before, for what may still be on its way to the writer
// (Options.Waiting): every event before counts as made before m counts
// here too, so each holds the mark until it comes, whichever way a step of
// the wall clock went.
func (m moment) madeBefore(e event.Envelope) bool {
	switch {
	case e.T < m.wall:
		return true
	case m.run > 0 && e.BootID == runBoot:
		return e.Mono < m.run
	case m.os > 0 && e.Mono > 0 && e.BootID == m.boot:
		return e.Mono < m.os
	}
	return false
}

// raise moves m on to n by each clock where n is later. An OS reading of
// another boot than m's replaces m's, since only one boot runs at a time
// and n's is the newer. When n is later by the wall clock and has no
// reading of a clock m has, as a moment read back from disk or kept by
// the wall clock alone, m's reading is of an earlier time than the moment
// now is, so it goes, and events of that clock are held to the wall
// clock.
func (m *moment) raise(n moment) {
	later := n.wall > m.wall
	m.wall = max(m.wall, n.wall)
	switch {
	case n.run > 0:
		m.run = max(m.run, n.run)
	case later:
		m.run = 0
	}
	switch {
	case n.os > 0 && m.boot == n.boot:
		m.os = max(m.os, n.os)
	case n.os > 0:
		m.os, m.boot = n.os, n.boot
	case later:
		m.os, m.boot = 0, ""
	}
}

// before reports whether the event e was made before m: by e's mono when
// m holds the clock it counts, this run's for the agent's own events and
// the OS's of m's boot for a hook call, since a monotonic clock is the one
// no step of the wall clock moves either way; and by its t otherwise, as
// for an event of an earlier run or boot.
func (m moment) before(e event.Envelope) bool {
	switch {
	case m.run > 0 && e.BootID == runBoot:
		return e.Mono < m.run
	case m.os > 0 && e.Mono > 0 && e.BootID == m.boot:
		return e.Mono < m.os
	}
	return e.T < m.wall
}
