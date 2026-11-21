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
// call with (internal/platform/clock). A step of the wall clock while an
// event waits moves neither monotonic clock, so an event made before the
// moment by any of the three counts as made before it.
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

// momentAt is at by each clock: the wall clock's at, this run's mono at at,
// which Go's monotonic reading in at gives when it has one, and s, the OS's
// reading taken with it.
func momentAt(at time.Time, s clock.Stamp) moment {
	m := moment{wall: at.UnixMilli(), run: runMono(at)}
	if s.HasMono() {
		m.os, m.boot = s.Mono, s.BootID
	}
	return m
}

// raise moves m on to n by each clock where n is later. An OS reading of
// another boot than m's replaces m's, since only one boot runs at a time
// and n's is the newer.
func (m *moment) raise(n moment) {
	m.wall = max(m.wall, n.wall)
	m.run = max(m.run, n.run)
	switch {
	case n.os == 0:
	case m.boot != n.boot:
		m.os, m.boot = n.os, n.boot
	default:
		m.os = max(m.os, n.os)
	}
}

// before reports whether the event e was made before m by any clock e
// carries that m holds too: its t by the wall clock; its mono when e was
// made in this run, as the agent's own events are; or its mono in m's
// boot, as a hook call's is.
func (m moment) before(e event.Envelope) bool {
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
