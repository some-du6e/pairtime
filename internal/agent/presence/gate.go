// SPDX-License-Identifier: AGPL-3.0-or-later

package presence

import (
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
)

// The sampling parameters of DESIGN 3.3, D14 and 6.3. The public parameter
// table (pkg/params, WP-PA1) has them under the names in each comment. The
// sampler scores nothing, so it doesn't read the table.
const (
	// SampleInterval is SAMPLE_S: one sample every 15 s while a project
	// context is live (SM 2.1).
	SampleInterval = 15 * time.Second
	// SlowInterval is SLOW_SAMPLE_S, the cadence once a context has gone
	// ContextIdle with no input and no running turn (D14).
	SlowInterval = 60 * time.Second
	// ContextIdle is CONTEXT_IDLE_S (D14).
	ContextIdle = 600 * time.Second
	// ContextWindow is how long a sign of a project context keeps it live:
	// a terminal, editor or agent app that had focus, or a hook from a
	// harness session (DESIGN 3.3, "in the last 600 s").
	ContextWindow = 600 * time.Second
)

// staleTurn is how long a turn counts as running after its session's last
// hook. A turn ends at its stop, interrupt or session end hook; this only
// covers a hook that never came, because the harness crashed or was killed,
// which would otherwise keep a context live and sampled every 15 s for good.
// It is longer than any tool call a harness runs between hooks in practice,
// and it changes no credit: D14's cadence never does.
const staleTurn = 30 * time.Minute

// gate decides whether a project context is live and how often to sample
// in it (DESIGN 3.3, D14). It reads no clock: every method takes the wall
// clock's time, so tests drive it with a fake one. The zero gate has no
// context.
type gate struct {
	// procs is how many harness session processes the process scanner
	// counted last (WP-OS6).
	procs int
	// focused is set while a terminal, editor or agent app has focus.
	focused bool
	// unfocused is when one last lost focus; zero if none has.
	unfocused time.Time
	// sessions are the harness sessions heard from through hooks, by
	// session key.
	sessions map[string]*session
	// input is the time of the latest input the latest sample showed;
	// zero before the first sample.
	input time.Time
	// turnEnd is when the latest running turn stopped; zero if none has.
	turnEnd time.Time
}

type session struct {
	last time.Time // the session's latest hook
	turn bool      // a prompt was submitted and its turn hasn't stopped
}

// projectApp reports whether focus on an app of class c makes a project
// context: terminals, editors and agent apps (DESIGN 3.3). Browsers and
// chat apps don't, although SM 2.5 credits a lookup in one, because a
// lookup is bracketed by focus on a project app.
func projectApp(c event.AppClass) bool {
	return c == event.AppTerminal || c == event.AppEditor || c == event.AppAgentApp
}

// focus records that an app of class c took focus.
func (g *gate) focus(now time.Time, c event.AppClass) {
	g.settle(now)
	in := projectApp(c)
	if g.focused && !in {
		g.unfocused = now
	}
	g.focused = in
}

// hook records a hook of kind k from the harness session key. A prompt
// starts a turn; a stop or an interrupt ends it; a session end forgets the
// session at once.
func (g *gate) hook(now time.Time, key string, k event.Kind) {
	g.settle(now)
	s := g.sessions[key]
	if k == event.KindSessionEnd {
		if s != nil && s.turn {
			g.turnEnd = now
		}
		delete(g.sessions, key)
		return
	}
	if s == nil {
		if g.sessions == nil {
			g.sessions = make(map[string]*session)
		}
		s = &session{}
		g.sessions[key] = s
	}
	s.last = now
	switch k {
	case event.KindPromptSubmit:
		s.turn = true
	case event.KindAgentStop, event.KindAgentInterrupt:
		if s.turn {
			g.turnEnd = now
		}
		s.turn = false
	}
}

// setProcs records the process scanner's count of harness session
// processes.
func (g *gate) setProcs(n int) {
	g.procs = max(n, 0)
}

// sampled records a sample taken at now that showed idle since the last
// input. The OS's latest reading wins, even when it moves the input back,
// because the OS is the authority on its own input queue.
func (g *gate) sampled(now time.Time, idle time.Duration) {
	g.settle(now)
	g.input = now.Add(-max(idle, 0))
}

// interval returns how often to sample at now: SampleInterval, or
// SlowInterval once ContextIdle has passed with no input and no running
// turn, or 0 when no project context is live, which means nothing may be
// sampled or recorded.
func (g *gate) interval(now time.Time) time.Duration {
	g.settle(now)
	if !g.live(now) {
		return 0
	}
	if g.turnRunning(now) || g.input.IsZero() {
		return SampleInterval
	}
	quiet := g.input
	if g.turnEnd.After(quiet) {
		quiet = g.turnEnd
	}
	if now.Sub(quiet) < ContextIdle {
		return SampleInterval
	}
	return SlowInterval
}

// live reports whether a project context is live at now: a harness session
// process is running, or a terminal, editor or agent app has focus or had
// it in the last ContextWindow (DESIGN 3.3). A session heard from through
// hooks counts as running for ContextWindow after its last hook, and for
// as long as its turn runs.
func (g *gate) live(now time.Time) bool {
	if g.procs > 0 || g.focused || recent(now, g.unfocused, ContextWindow) {
		return true
	}
	for _, s := range g.sessions {
		if recent(now, s.last, ContextWindow) || s.running(now) {
			return true
		}
	}
	return false
}

func (g *gate) turnRunning(now time.Time) bool {
	for _, s := range g.sessions {
		if s.running(now) {
			return true
		}
	}
	return false
}

func (s *session) running(now time.Time) bool {
	return s.turn && recent(now, s.last, staleTurn)
}

// settle brings the gate up to now. It ends turns that went stale, at the
// moment they did, forgets sessions that no longer keep a context live,
// and moves times recorded after now back to now: the wall clock can be
// set back, and a time in the future would keep a context live for as
// long as the clock moved.
func (g *gate) settle(now time.Time) {
	g.unfocused = notAfter(g.unfocused, now)
	g.input = notAfter(g.input, now)
	g.turnEnd = notAfter(g.turnEnd, now)
	for key, s := range g.sessions {
		s.last = notAfter(s.last, now)
		if s.turn && !s.running(now) {
			s.turn = false
			if end := s.last.Add(staleTurn); end.After(g.turnEnd) {
				g.turnEnd = end
			}
		}
		if !recent(now, s.last, ContextWindow) && !s.turn {
			delete(g.sessions, key)
		}
	}
}

// recent reports whether t is set and less than d before now.
func recent(now, t time.Time, d time.Duration) bool {
	return !t.IsZero() && now.Sub(t) < d
}

func notAfter(t, now time.Time) time.Time {
	if t.After(now) {
		return now
	}
	return t
}
