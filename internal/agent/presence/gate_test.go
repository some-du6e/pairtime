// SPDX-License-Identifier: AGPL-3.0-or-later

package presence

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
)

// step is one moment of a gate test: at a time after the test's origin,
// an input to the gate, or a check of the cadence it wants then, or both.
type step struct {
	at    time.Duration
	do    func(g *gate, now time.Time)
	check bool          // check the interval at this moment
	want  time.Duration // the interval wanted: 0 means no context
}

func focus(c event.AppClass) func(*gate, time.Time) {
	return func(g *gate, now time.Time) { g.focus(now, c) }
}

func hook(session string, k event.Kind) func(*gate, time.Time) {
	return func(g *gate, now time.Time) { g.hook(now, session, k) }
}

// forgot is the agent forgetting session, as it does for one whose calls
// ran in a folder out of pairtime (Sampler.Forget).
func forgot(session string) func(*gate, time.Time) {
	return func(g *gate, now time.Time) { g.forget(now, session) }
}

func procs(n int) func(*gate, time.Time) {
	return func(g *gate, _ time.Time) { g.setProcs(n) }
}

// apps is the apps component's count of running agent apps that drive
// harness sessions (Sampler.AgentApps).
func apps(n int) func(*gate, time.Time) {
	return func(g *gate, _ time.Time) { g.setApps(n) }
}

// sampled is a sample at the step's moment that shows idle since the last
// input.
func sampled(idle time.Duration) func(*gate, time.Time) {
	return func(g *gate, now time.Time) { g.sampled(now, idle) }
}

func want(at, interval time.Duration) step {
	return step{at: at, check: true, want: interval}
}

// toolCalls returns a tool hook from session key at from and then every
// every until to, each followed halfway to the next by a check that the
// interval is want.
func toolCalls(key string, from, to, every, want time.Duration) []step {
	var steps []step
	for t := from; t <= to; t += every {
		steps = append(steps, step{at: t, do: hook(key, event.KindAgentTool)}, step{at: t + every/2, check: true, want: want})
	}
	return steps
}

// s returns d seconds, rounded to the nanosecond, so s(23.2) is exactly
// 23.2 s.
func s(d float64) time.Duration { return time.Duration(math.Round(d * float64(time.Second))) }

// TestGate checks D14's rule text and DESIGN 3.3's definition of a project
// context, step by step on a fake wall clock, with table 0.1.0's 15 s, 60 s
// and 600 s.
func TestGate(t *testing.T) {
	d14 := cadenceOf(table010(t))
	fast, slow := d14.sample, d14.slow
	tests := []struct {
		name  string
		steps []step
	}{
		{
			name:  "with no signal there is no context and nothing is sampled",
			steps: []step{want(0, 0), want(s(3600), 0)},
		},
		{
			name:  "a focused terminal is a project context, sampled every 15 s",
			steps: []step{{at: 0, do: focus(event.AppTerminal)}, want(0, fast), want(s(7200), fast)},
		},
		{
			name:  "a focused editor is a project context",
			steps: []step{{at: 0, do: focus(event.AppEditor)}, want(0, fast)},
		},
		{
			name:  "a focused agent app is a project context",
			steps: []step{{at: 0, do: focus(event.AppAgentApp)}, want(0, fast)},
		},
		{
			name: "a focused browser, chat app, other app or no window is no context",
			steps: []step{
				{at: 0, do: focus(event.AppBrowser)}, want(0, 0),
				{at: s(1), do: focus(event.AppChatApp)}, want(s(1), 0),
				{at: s(2), do: focus(event.AppOther)}, want(s(2), 0),
				{at: s(3), do: focus(event.AppNone)}, want(s(3), 0),
			},
		},
		{
			name: "the context outlives the terminal's focus by 600 s",
			steps: []step{
				{at: 0, do: focus(event.AppTerminal)},
				{at: s(10), do: focus(event.AppBrowser)},
				want(s(609.999), fast),
				want(s(610), 0),
			},
		},
		{
			name: "focus back on the terminal starts the 600 s again",
			steps: []step{
				{at: 0, do: focus(event.AppTerminal)},
				{at: s(10), do: focus(event.AppBrowser)},
				{at: s(300), do: focus(event.AppEditor)},
				{at: s(400), do: focus(event.AppChatApp)},
				want(s(999), fast),
				want(s(1000), 0),
			},
		},
		{
			name: "moving between two browsers doesn't restart the 600 s",
			steps: []step{
				{at: 0, do: focus(event.AppTerminal)},
				{at: s(10), do: focus(event.AppBrowser)},
				{at: s(300), do: focus(event.AppChatApp)},
				want(s(610), 0),
			},
		},
		{
			name: "a running harness process is a project context for as long as it runs",
			steps: []step{
				{at: 0, do: procs(1)}, want(s(5000), fast),
				{at: s(5000), do: procs(0)}, want(s(5000), 0),
			},
		},
		{
			name: "a running agent app that drives sessions keeps a project context live for as long as it runs, as a harness process does, sampled every 60 s with no other sign of one (D-relayed-context)",
			steps: []step{
				{at: 0, do: apps(1)}, want(0, slow), want(s(5000), slow),
				{at: s(5000), do: apps(0)}, want(s(5000), 0),
			},
		},
		{
			name: "a context only a running agent app keeps live is sampled every 60 s whatever the input (D-relayed-context, as #225 amended it)",
			steps: []step{
				{at: 0, do: apps(1)},
				{at: 0, do: sampled(0)},
				want(0, slow),
				want(s(599), slow),
				{at: s(700), do: sampled(s(5))},
				want(s(700), slow),
			},
		},
		{
			name: "where focus is unknown, a context an agent app alone keeps live samples at D14's cadence, every SAMPLE_S until CONTEXT_IDLE_S without input, and records no os.focus (WP-SC8, D-relayed-sleep)",
			steps: []step{
				{at: 0, do: apps(1)},
				{at: 0, do: focus(event.AppUnknown)},
				{at: 0, do: sampled(0)},
				want(0, fast),
				want(s(599), fast),
				want(s(600), slow),
				{at: s(700), do: sampled(0)},
				want(s(700), fast),
				// A window report that isn't unknown ends it: the app alone
				// samples every SLOW_SAMPLE_S again, whatever the input.
				{at: s(800), do: focus(event.AppBrowser)},
				want(s(800), slow),
			},
		},
		{
			// The input at 500 s would keep SAMPLE_S under D14 until 1100 s,
			// so the cadence at 700 s and 2000 s is the agent app's.
			name: "an agent app alone samples every SLOW_SAMPLE_S whatever the input once ContextWindow has passed since the last project app's focus and hook",
			steps: []step{
				{at: 0, do: focus(event.AppTerminal)},
				{at: s(5), do: apps(1)},
				{at: s(10), do: focus(event.AppBrowser)},
				{at: s(100), do: hook("s_a", event.KindSessionStart)},
				{at: s(500), do: sampled(0)},
				want(s(609), fast),
				want(s(610), fast),
				want(s(699), fast),
				want(s(700), slow),
				{at: s(2000), do: sampled(0)},
				want(s(2000), slow),
				want(s(3600), slow),
				{at: s(3600), do: apps(0)},
				want(s(3600), 0),
			},
		},
		{
			name: "T3 Code's own window in front is a sign of work, so its agent app's context takes the usual 15 s, for 600 s after it loses focus",
			steps: []step{
				{at: 0, do: apps(1)},
				{at: s(10), do: focus(event.AppAgentApp)},
				{at: s(10), do: sampled(0)},
				want(s(10), fast),
				{at: s(20), do: focus(event.AppBrowser)},
				{at: s(500), do: sampled(0)},
				want(s(619), fast),
				want(s(620), slow),
			},
		},
		{
			// The sample each sign takes is the sampler's (TestSampler).
			name: "a project app's focus, a hook or a harness process brings SAMPLE_S back at once, with a sample",
			steps: []step{
				{at: 0, do: apps(1)},
				{at: 0, do: sampled(0)},
				want(s(5), slow),
				{at: s(10), do: hook("s_a", event.KindSessionStart)},
				want(s(10), fast),
				{at: s(300), do: sampled(0)},
				want(s(609), fast),
				want(s(610), slow),
				{at: s(700), do: procs(1)},
				{at: s(700), do: sampled(s(5))},
				want(s(700), fast),
				{at: s(800), do: procs(0)},
				want(s(800), slow),
				{at: s(900), do: focus(event.AppEditor)},
				want(s(900), fast),
				{at: s(910), do: focus(event.AppBrowser)},
				{at: s(1400), do: sampled(0)},
				want(s(1509), fast),
				want(s(1510), slow),
			},
		},
		{
			name: "a session start hook keeps its session's context live for 600 s",
			steps: []step{
				{at: 0, do: hook("s_a", event.KindSessionStart)},
				want(s(599), fast),
				want(s(600), 0),
			},
		},
		{
			name: "a tool call comes only in a running turn, so it keeps the context live past 600 s",
			steps: []step{
				{at: 0, do: hook("s_a", event.KindAgentTool)},
				want(s(1200), fast),
				want(s(1799), fast),
				want(s(1800), 0),
			},
		},
		{
			name: "a forgotten session's running turn ends with no context left live, where its stop would keep the context 600 s",
			steps: []step{
				{at: 0, do: hook("s_a", event.KindAgentTool)},
				want(s(1), fast),
				{at: s(2), do: forgot("s_a")},
				want(s(2), 0),
				{at: s(3), do: hook("s_b", event.KindAgentTool)},
				{at: s(4), do: hook("s_b", event.KindAgentStop)},
				want(s(603), fast),
				want(s(604), 0),
			},
		},
		{
			name: "a permission request comes only in a running turn too",
			steps: []step{
				{at: 0, do: hook("s_a", event.KindAgentPermission)},
				want(s(1200), fast),
			},
		},
		{
			name: "a running turn keeps the context live past 600 s",
			steps: []step{
				{at: 0, do: hook("s_a", event.KindPromptSubmit)},
				want(s(1200), fast),
			},
		},
		{
			name: "a stop leaves the context live for 600 s after it",
			steps: []step{
				{at: 0, do: hook("s_a", event.KindPromptSubmit)},
				{at: s(100), do: hook("s_a", event.KindAgentStop)},
				want(s(699), fast),
				want(s(700), 0),
			},
		},
		{
			name: "an interrupt ends the turn like a stop",
			steps: []step{
				{at: 0, do: hook("s_a", event.KindPromptSubmit)},
				{at: s(100), do: hook("s_a", event.KindAgentInterrupt)},
				want(s(700), 0),
			},
		},
		{
			name: "a session end ends the session's context at once",
			steps: []step{
				{at: 0, do: hook("s_a", event.KindPromptSubmit)},
				{at: s(50), do: hook("s_a", event.KindSessionEnd)},
				want(s(50), 0),
			},
		},
		{
			name: "a session end leaves another session's turn running",
			steps: []step{
				{at: 0, do: hook("s_a", event.KindPromptSubmit)},
				{at: 0, do: hook("s_b", event.KindPromptSubmit)},
				{at: s(50), do: hook("s_a", event.KindSessionEnd)},
				want(s(1200), fast),
			},
		},
		{
			name: "a turn with no hook for 30 minutes counts as stopped when it went quiet",
			steps: []step{
				{at: 0, do: hook("s_a", event.KindPromptSubmit)},
				want(s(1799), fast),
				want(s(1800), 0),
			},
		},
		{
			name: "a later hook in the turn keeps it running",
			steps: []step{
				{at: 0, do: hook("s_a", event.KindPromptSubmit)},
				{at: s(1500), do: hook("s_a", event.KindAgentTool)},
				want(s(3299), fast),
				want(s(3300), 0),
			},
		},
		{
			name: "600 s with no input slows sampling to every 60 s",
			steps: []step{
				{at: 0, do: focus(event.AppTerminal)},
				{at: 0, do: sampled(0)},
				want(s(599), fast),
				want(s(600), slow),
			},
		},
		{
			name: "the 600 s run from the input a sample shows, not from the sample",
			steps: []step{
				{at: 0, do: focus(event.AppTerminal)},
				{at: s(100), do: sampled(s(100))},
				want(s(599), fast),
				want(s(600), slow),
			},
		},
		{
			name: "a sample with input brings back 15 s sampling",
			steps: []step{
				{at: 0, do: focus(event.AppTerminal)},
				{at: 0, do: sampled(0)},
				want(s(700), slow),
				{at: s(720), do: sampled(s(5))},
				want(s(720), fast),
			},
		},
		{
			name: "a context that starts long after the last input samples slowly from the start",
			steps: []step{
				{at: 0, do: procs(1)},
				want(0, fast),
				{at: 0, do: sampled(2 * time.Hour)},
				want(0, slow),
			},
		},
		{
			name: "a running turn keeps 15 s sampling with no input",
			steps: []step{
				{at: 0, do: focus(event.AppTerminal)},
				{at: 0, do: sampled(0)},
				{at: s(10), do: hook("s_a", event.KindPromptSubmit)},
				want(s(1500), fast),
			},
		},
		{
			name: "the 600 s with no input run from the end of the last turn",
			steps: []step{
				{at: 0, do: focus(event.AppTerminal)},
				{at: 0, do: sampled(0)},
				{at: s(10), do: hook("s_a", event.KindPromptSubmit)},
				{at: s(300), do: hook("s_a", event.KindAgentStop)},
				want(s(899), fast),
				want(s(900), slow),
			},
		},
		{
			name: "a turn a stop hook continues has no prompt, and its tool calls keep 15 s sampling for 40 minutes with no input",
			steps: slices.Concat(
				[]step{
					{at: 0, do: focus(event.AppTerminal)},
					{at: 0, do: sampled(0)},
					{at: s(10), do: hook("s_a", event.KindPromptSubmit)},
					{at: s(100), do: hook("s_a", event.KindAgentStop)},
				},
				toolCalls("s_a", s(200), s(2600), s(100), fast),
			),
		},
		{
			name: "the stop of a turn a stop hook continued starts the 600 s again",
			steps: []step{
				{at: 0, do: focus(event.AppTerminal)},
				{at: 0, do: sampled(0)},
				{at: s(10), do: hook("s_a", event.KindPromptSubmit)},
				{at: s(100), do: hook("s_a", event.KindAgentStop)},
				{at: s(200), do: hook("s_a", event.KindAgentTool)},
				{at: s(300), do: hook("s_a", event.KindAgentStop)},
				want(s(899), fast),
				want(s(900), slow),
			},
		},
		{
			name: "a turn that goes stale counts as stopped 30 minutes after its last hook",
			steps: []step{
				{at: 0, do: focus(event.AppTerminal)},
				{at: 0, do: sampled(0)},
				{at: s(10), do: hook("s_a", event.KindPromptSubmit)},
				want(s(1809), fast),
				want(s(2409), fast),
				want(s(2410), slow),
			},
		},
		{
			name: "a wall clock set back doesn't stretch the context",
			steps: []step{
				{at: 0, do: focus(event.AppTerminal)},
				{at: s(100), do: focus(event.AppBrowser)},
				// The clock goes back 50 s: the focus change counts as now.
				want(s(50), fast),
				want(s(649), fast),
				want(s(650), 0),
			},
		},
	}
	origin := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := gate{cadence: d14}
			for i, st := range tt.steps {
				now := origin.Add(st.at)
				if st.do != nil {
					st.do(&g, now)
				}
				if !st.check {
					continue
				}
				if got := g.interval(now); got != st.want {
					t.Errorf("step %d, at %v: interval %v, want %v", i, st.at, got, st.want)
				}
			}
		})
	}
}

// TestAppOnly checks when a running agent app alone keeps the context live
// (D-relayed-context, as #225 amended it), which slows sampling to once a
// minute whatever the input and keeps focus from recording: never while
// another sign of a project context is there.
func TestAppOnly(t *testing.T) {
	origin := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		steps []step // their checks are ignored: only do runs
		at    time.Duration
		want  bool
	}{
		{"a running agent app with nothing else is the context's only sign", []step{{at: 0, do: apps(1)}}, s(1), true},
		{"with no agent app running there is no app's context", []step{{at: 0, do: focus(event.AppBrowser)}}, s(1), false},
		{"a terminal in front is a sign of work", []step{{at: 0, do: apps(1)}, {at: 0, do: focus(event.AppTerminal)}}, s(1), false},
		{"T3 Code's own window in front, an agent app's, is a sign of work", []step{{at: 0, do: apps(1)}, {at: 0, do: focus(event.AppAgentApp)}}, s(1), false},
		{"an editor that lost focus less than 600 s ago is still a sign of work",
			[]step{{at: 0, do: apps(1)}, {at: 0, do: focus(event.AppEditor)}, {at: s(10), do: focus(event.AppBrowser)}}, s(609), false},
		{"once its 600 s have passed the agent app is the only sign left",
			[]step{{at: 0, do: apps(1)}, {at: 0, do: focus(event.AppEditor)}, {at: s(10), do: focus(event.AppBrowser)}}, s(610), true},
		{"a hook in the last 600 s is a sign of work", []step{{at: 0, do: apps(1)}, {at: s(5), do: hook("s_a", event.KindSessionStart)}}, s(604), false},
		{"a running harness process is a sign of work", []step{{at: 0, do: apps(1)}, {at: 0, do: procs(1)}}, s(1), false},
		{"unknown focus is no sign of work, so the agent app is still the only sign",
			[]step{{at: 0, do: apps(1)}, {at: 0, do: focus(event.AppUnknown)}}, s(1), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := gate{cadence: cadenceOf(table010(t))}
			for _, st := range tt.steps {
				if st.do != nil {
					st.do(&g, origin.Add(st.at))
				}
			}
			now := origin.Add(tt.at)
			g.settle(now)
			if got := g.appOnly(now); got != tt.want {
				t.Errorf("appOnly = %v, want %v", got, tt.want)
			}
		})
	}
}
