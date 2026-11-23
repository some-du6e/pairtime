// SPDX-License-Identifier: AGPL-3.0-or-later

package focus

import (
	"sync"
	"time"
)

// settler passes a focus source's changes on, but holds one that may be a
// passing moment for wait, and drops it if another change comes first.
// Windows names no foreground window for a moment while one window loses
// activation and the next hasn't gained it (windows.UnknownNoWindow).
// Recorded at once, that moment would cost a switch two os.focus records:
// the unknown, then the next window's, recorded again even when it is the
// window from before. X11 settles the same way (internal/platform/linux,
// focusSettle). A held change that outlasts wait goes on with its own
// time, so focus is unknown from when the moment began. It builds on every
// OS, so ci.yml tests it.
type settler struct {
	fn   func(Change)
	wait time.Duration
	// after runs f once d has passed, on another goroutine, and returns
	// what cancels that; nil means time.AfterFunc. Tests pass a fake,
	// which must not run f before after returns.
	after func(d time.Duration, f func()) (cancel func() bool)

	mu      sync.Mutex
	gen     uint64 // counts changes, so a held one that a later one replaced stays dropped
	cancel  func() bool
	stopped bool
}

// pass takes change c from the source. A passing c waits; any other goes
// on at once, and either drops a change still waiting.
func (s *settler) pass(c Change, passing bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	s.gen++
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if !passing {
		s.fn(c)
		return
	}
	after := s.after
	if after == nil {
		after = func(d time.Duration, f func()) func() bool { return time.AfterFunc(d, f).Stop }
	}
	gen := s.gen
	s.cancel = after(s.wait, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		// A timer that fires as a later change cancels it finds a newer
		// generation, and so does one that fires after stop.
		if s.stopped || s.gen != gen {
			return
		}
		s.cancel = nil
		s.fn(c)
	})
}

// stop drops a change still waiting. Nothing passes on after it returns.
func (s *settler) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}
