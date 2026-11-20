// SPDX-License-Identifier: AGPL-3.0-or-later

package presence

import (
	"math"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
)

// MacPower carries the power events of the agent's darwin.Loop to a
// sampler (WP-OS9). macOS delivers screen lock notifications only on the
// process's main thread, so the Loop runs there, started by cmd/pairtime
// before the agent and stopped after it (internal/platform/darwin). Its
// handler calls Power with every suspend, resume, lock and unlock, and the
// sampler takes them through the watches MacOS gives it. The zero value is
// ready to use. This file builds on every OS, so ci.yml tests it with
// fakes, as it does the darwin package's own logic.
type MacPower struct {
	feed feed
}

// Power is the power half of the Loop's darwin.Handler. It passes op to
// the sampler and returns once the sampler has handled it, which for a
// suspend means once the open batch is sealed: the Loop holds the sleep
// until Power returns or darwin.SuspendWait has passed (DESIGN 3.3). With
// no sampler watching, it drops op. at is when the notification came; the
// sampler dates its record when it takes it, a moment later.
func (p *MacPower) Power(at time.Time, op event.PowerOp) {
	p.feed.pass(op)
}

// MacOS returns macOS's sources (DESIGN 3.3): the idle time, the key-down
// idle time and the lock state from read, which is darwin.Presence in the
// agent, and suspend, resume, lock and unlock from power, which carries the
// agent's darwin.Loop's. With power nil the sampler sees no power events,
// as when no Loop runs.
//
// The key-down idle time goes in idle_keys_s as macOS gives it. It counts
// key-downs that any process allowed to post events sends to the HID
// system state, as well as the person's (internal/platform/darwin, WP-OS3's
// finding), so it can withhold credit but never earns level A on its own.
// The scorer applies that rule; presence only records the value
// (D-macos-hid-posted).
func MacOS(read func() (event.OSPresence, error), power *MacPower) OS {
	o := OS{
		Idle: func() (Idle, error) {
			p, err := read()
			if err != nil {
				return Idle{}, err
			}
			i := Idle{Any: fromSeconds(p.IdleAnyS)}
			if p.IdleKeysS != nil {
				i.Keys, i.HasKeys = fromSeconds(*p.IdleKeysS), true
			}
			return i, nil
		},
		Locked: func() (bool, error) {
			p, err := read()
			return p.Locked, err
		},
	}
	if power != nil {
		power.feed.watches(&o)
	}
	return o
}

// fromSeconds turns an idle time in seconds, as os.presence carries it, back
// into a duration, to the millisecond darwin.Presence rounds it to.
func fromSeconds(s float64) time.Duration {
	if math.IsNaN(s) || s <= 0 {
		return 0
	}
	ms := math.Round(s * 1000)
	if ms >= float64(math.MaxInt64/int64(time.Millisecond)) {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(ms) * time.Millisecond
}
