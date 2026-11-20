// SPDX-License-Identifier: AGPL-3.0-or-later

package darwin

import (
	"errors"
	"math"

	"github.com/some-du6e/pairtime/pkg/event"
)

// CoreGraphics event types (CGEventTypes.h).
const (
	// cgAnyInput is kCGAnyInputEventType, defined as ~0.
	cgAnyInput uint32 = 0xFFFFFFFF
	// cgKeyDown is kCGEventKeyDown.
	cgKeyDown uint32 = 10
)

// ErrNoIdle means neither CoreGraphics nor IOKit gave an idle time, so the
// sample is missing and presence is unknown (SM 2.1), rather than a guess.
var ErrNoIdle = errors.New("darwin: neither CoreGraphics nor IOKit reported an idle time")

// presenceSource is what a presence sample reads. The darwin one calls the
// frameworks; tests use fakes.
type presenceSource interface {
	// cgIdle returns CGEventSourceSecondsSinceLastEventType for the HID
	// system state and eventType, and false when CoreGraphics isn't loaded.
	cgIdle(eventType uint32) (float64, bool)
	// hidIdle returns IOHIDSystem's HIDIdleTime in nanoseconds, and false
	// when IOKit has no such property.
	hidIdle() (int64, bool)
	// session returns whether the screen is locked, and false when there is
	// no session dictionary, as outside a GUI login.
	session() (locked bool, ok bool)
}

// Presence takes one os.presence sample (SM 1.2): seconds since any input,
// seconds since a key press when CoreGraphics can tell, and whether the
// screen is locked. It returns ErrNoIdle when the OS gives no idle time,
// and errors.ErrUnsupported anywhere but macOS. Any goroutine may call it.
//
// locked is false when the session dictionary is missing; the idle times
// are what the OS reports either way.
func Presence() (event.OSPresence, error) {
	src, err := osPresenceSource()
	if err != nil {
		return event.OSPresence{}, err
	}
	return samplePresence(src)
}

// samplePresence takes one os.presence sample from src.
func samplePresence(src presenceSource) (event.OSPresence, error) {
	// Key-down first. Any input includes key presses, so read in this order
	// the key-down idle time can come out short of the any-input one only by
	// the time between the two calls. Read the other way round, a key press
	// between them would make it the smaller one by any amount.
	keys, keysOK := src.cgIdle(cgKeyDown)
	anyS, cgOK := src.cgIdle(cgAnyInput)
	cgOK = cgOK && validIdle(anyS)
	anyOK := cgOK
	if !anyOK {
		if ns, ok := src.hidIdle(); ok && ns >= 0 {
			anyS, anyOK = float64(ns)/1e9, true
		}
	}
	if !anyOK {
		return event.OSPresence{}, ErrNoIdle
	}
	p := event.OSPresence{IdleAnyS: roundMS(anyS)}
	// A key-down time is kept only when CoreGraphics also gave a sound
	// any-input time: a call that got one wrong can't be trusted with the
	// other. It is raised to the any-input time when the order of the calls
	// left it a little short, because a key press is input too.
	if cgOK && keysOK && validIdle(keys) {
		k := roundMS(max(keys, anyS))
		p.IdleKeysS = &k
	}
	if locked, ok := src.session(); ok {
		p.Locked = locked
	}
	return p, nil
}

// validIdle reports whether v can be an idle time: finite and not negative.
// A negative idle time would claim input in the future, so it is treated as
// no answer rather than clamped to "input just now".
func validIdle(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0
}

// roundMS rounds seconds to the millisecond, which is the precision of every
// other time in an event (SM 1.1). Values too large to scale are kept as
// they are.
func roundMS(v float64) float64 {
	if v >= 1e12 {
		return v
	}
	return math.Round(v*1000) / 1000
}
