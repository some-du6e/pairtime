// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"time"

	osclock "github.com/some-du6e/pairtime/internal/platform/clock"
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
// once config.toml said online. Zero means no bound by that clock. A
// moment an earlier run recorded, read back from disk, holds the bounds of
// this run's start and, when it was recorded in another boot, of this
// boot's (Writer.earlier): every event of this run, and every hook call of
// this boot, was made after it, whatever the wall clock says.
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
func momentAt(at time.Time, s osclock.Stamp) moment {
	m := moment{wall: at.UnixMilli()}
	if at != at.Round(0) { // Round(0) strips the monotonic reading
		m.run = runMono(at)
	}
	if s.HasMono() {
		m.os, m.boot = s.Mono, s.BootID
	}
	return m
}

// raise moves m on to n by each clock where n is later. cur is the boot
// the writer runs in, "" when the OS gave none. A reading of cur replaces
// one of another boot, since only one boot runs at a time and cur is the
// newest. One of another boot than cur replaces m's only when n is later
// by the wall clock and m holds no reading of cur: an older boot's moment,
// as state.json kept it, says nothing of the boots after it. When n is
// later by the wall clock and has no reading of a clock m has, as a moment
// kept by the wall clock alone, m's reading is of an earlier time than the
// moment now is, so it goes, and events of that clock are held to the wall
// clock.
func (m *moment) raise(n moment, cur string) {
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
	case n.os > 0 && (n.boot == cur || later && (m.os == 0 || m.boot != cur)):
		m.os, m.boot = n.os, n.boot
	case n.os == 0 && later:
		m.os, m.boot = 0, ""
	}
}

// earlierThan reports whether m is earlier than n: by this run's clock
// when both hold it, by the OS's when both hold the same boot's, and by the
// wall clock otherwise, as before holds an event to a moment. A zero n has
// nothing m is earlier than.
func (m moment) earlierThan(n moment) bool {
	switch {
	case m.run > 0 && n.run > 0:
		return m.run < n.run
	case m.os > 0 && n.os > 0 && m.boot == n.boot:
		return m.os < n.os
	}
	return m.wall < n.wall
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
