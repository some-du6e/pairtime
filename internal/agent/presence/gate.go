// SPDX-License-Identifier: AGPL-3.0-or-later

package presence

import (
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/params"
)

// cadence holds the sampling parameters of DESIGN 3.3, D14 and 6.3, read
// from the public parameter table. The scorer reads CONTEXT_IDLE_S and
// SLOW_SAMPLE_S from the same table to tell a gap that slow sampling
// explains from sleep (SM 2.1's SLEEP_GAP_S), so the sampler has to slow
// down when the table says, and a new table moves both with no code
// (DESIGN 6.6).
type cadence struct {
	// sample is SAMPLE_S: one sample every 15 s while a project context is
	// live (SM 2.1).
	sample time.Duration
	// slow is SLOW_SAMPLE_S, the cadence once a context has gone idle with
	// no input and no running turn (D14).
	slow time.Duration
	// idle is CONTEXT_IDLE_S, how long that takes (D14).
	idle time.Duration
}

// cadenceOf reads the sampling parameters from a parameter table.
func cadenceOf(t *params.Table) cadence {
	sec := func(p params.IntParam) time.Duration { return time.Duration(t.Int(p)) * time.Second }
	return cadence{
		sample: sec(params.SAMPLE_S),
		slow:   sec(params.SLOW_SAMPLE_S),
		idle:   sec(params.CONTEXT_IDLE_S),
	}
}

// ContextWindow is how long a sign of a project context keeps it live: a
// terminal, editor or agent app that had focus, or a hook from a harness
// session (DESIGN 3.3, "in the last 600 s"). DESIGN names no parameter for
// it, so it isn't in the table.
const ContextWindow = 600 * time.Second

// staleTurn is how long a turn counts as running after its session's last
// hook. A turn ends at its stop, interrupt or session end hook; this only
// covers a hook that never came, because the harness crashed or was killed,
// which would otherwise keep a context live and sampled every 15 s for good.
// It is longer than any tool call a harness runs between hooks in practice,
// and a turn it ends only lets sampling slow down (D14).
const staleTurn = 30 * time.Minute

// gate decides whether a project context is live and how often to sample
// in it (DESIGN 3.3, D14). It reads no clock: every method takes the wall
// clock's time, so tests drive it with a fake one. A gate with only its
// cadence set has no context.
type gate struct {
	cadence cadence
	// procs is how many harness session processes the process scanner
	// counted last (WP-OS6).
	procs int
	// apps is how many agent apps that drive harness sessions, such as T3
	// Code's server, the apps component found running last
	// (D-relayed-context). One keeps a context live alone at the slow
	// cadence (appOnly).
	apps int
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
	turn bool      // a turn is running: see hook
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
// starts a turn, and so does a tool call or a permission request: those
// come only inside a turn, and a turn that a stop hook continues has no
// prompt (HS 3.5, SM 6.4), so its first tool call is where it shows. A
// stop or an interrupt ends the turn; a session end forgets the session at
// once.
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
	case event.KindPromptSubmit, event.KindAgentTool, event.KindAgentPermission:
		s.turn = true
	case event.KindAgentStop, event.KindAgentInterrupt:
		if s.turn {
			g.turnEnd = now
		}
		s.turn = false
	}
}

// forget drops session as if it had never been heard from: unlike its end,
// a turn it was running leaves no turnEnd behind, which would keep the fast
// cadence for a while after it.
func (g *gate) forget(now time.Time, key string) {
	g.settle(now)
	delete(g.sessions, key)
}

// setProcs records the process scanner's count of harness session
// processes.
func (g *gate) setProcs(n int) {
	g.procs = max(n, 0)
}

// setApps records the apps component's count of running agent apps that
// drive harness sessions.
func (g *gate) setApps(n int) {
	g.apps = max(n, 0)
}

// sampled records a sample taken at now that showed idle since the last
// input. The OS's latest reading wins, even when it moves the input back,
// because the OS is the authority on its own input queue.
func (g *gate) sampled(now time.Time, idle time.Duration) {
	g.settle(now)
	g.input = now.Add(-max(idle, 0))
}

// interval returns how often to sample at now: SAMPLE_S, or SLOW_SAMPLE_S
// once CONTEXT_IDLE_S has passed with no input and no running turn, or 0
// when no project context is live, which means nothing may be sampled or
// recorded. A project context is live while a sign of work on this
// computer is there (atWork) or an agent app that drives harness sessions
// is running (DESIGN 3.3). An agent app counts for as long as it runs,
// since a message can come from its phone app at any moment, and a
// stretch the message's lead or its reading reaches that went unsampled
// would read as slept through (D-relayed-context); a context only it keeps
// live is sampled every SLOW_SAMPLE_S whatever the input (appOnly).
func (g *gate) interval(now time.Time) time.Duration {
	g.settle(now)
	switch {
	case g.atWork(now):
	case g.apps > 0:
		return g.cadence.slow
	default:
		return 0
	}
	if g.turnRunning(now) || g.input.IsZero() {
		return g.cadence.sample
	}
	quiet := g.input
	if g.turnEnd.After(quiet) {
		quiet = g.turnEnd
	}
	if now.Sub(quiet) < g.cadence.idle {
		return g.cadence.sample
	}
	return g.cadence.slow
}

// atWork reports whether a project context is live at now by a sign of
// work on this computer, which is any sign but a running agent app: a
// harness session process is running, or a terminal, editor or agent app
// has focus or had it in the last ContextWindow (DESIGN 3.3). A session
// heard from through hooks counts as running for ContextWindow after its
// last hook, and for as long as its turn runs.
func (g *gate) atWork(now time.Time) bool {
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

// appOnly reports whether a running agent app alone keeps the context live
// at now, with no sign of work on this computer (atWork): D-relayed-context
// as #225 amended it. Presence then samples once a minute whatever the
// input, and the focus component records nothing about the window in
// front. With T3 Code's own window in front, focus on an agent app is a
// sign of work, and the usual rules apply.
func (g *gate) appOnly(now time.Time) bool {
	return g.apps > 0 && !g.atWork(now)
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
