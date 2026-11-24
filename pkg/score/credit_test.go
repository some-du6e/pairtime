// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
)

// The agent's records for these tests, all in project p_1: transcript text
// (via log), a stop and an edit (via hook).

func (b *tl) agentSays(t int64, sess, turn string, words int) {
	b.harness(t, event.ViaLog, sess, "p_1", &event.AgentOutput{TurnKey: turn, Words: words})
}

func (b *tl) agentStops(t int64, sess, turn string) {
	b.harness(t, event.ViaHook, sess, "p_1", &event.AgentStop{TurnKey: turn, StopReason: event.StopEndTurn})
}

func (b *tl) agentEdits(t int64, sess string, lines int) {
	b.harness(t, event.ViaHook, sess, "p_1", &event.AgentTool{ToolClass: event.ToolEdit, LinesAdded: lines,
		TStart: t - 500, TEnd: t})
}

// scheduled marks a prompt as a /loop iteration (SM 2.2 rule 1).
func scheduled(e event.Event) { e.Data.(*event.PromptSubmit).OriginDeclared = event.DeclaredLoop }

// sumIn sums the seconds of d's credit slots in [from, to), which are slot
// boundaries: credited seconds by reason and uncredited ones by reason.
func sumIn(d Derived, from, to int64) map[event.Reason]int64 {
	out := map[event.Reason]int64{}
	for _, c := range d.Credits {
		if c.Slot < from || c.Slot >= to {
			continue
		}
		for _, e := range c.Credited {
			out[e.Reason] += e.Seconds
		}
		for r, n := range c.Uncredited {
			out[r] += n
		}
	}
	return out
}

func sameCounts(a, b map[event.Reason]int64) bool {
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	for k, v := range b {
		if a[k] != v {
			return false
		}
	}
	return true
}

// secondsWant is what a range of slots, in seconds after base, should hold.
type secondsWant struct {
	from, to int64
	want     map[event.Reason]int64
}

const (
	rP       = event.ReasonPrompt
	rSUP     = event.ReasonSupervise
	rREAD    = event.ReasonRead
	rNAV     = event.ReasonNavigate
	rPA      = event.ReasonPermission
	rG       = event.ReasonCommit
	rLOOKUP  = event.ReasonLookup
	xAbsent  = event.ReasonAbsent
	xNoAnch  = event.ReasonNoAnchor
	xAgent   = event.ReasonAgentOnly
	xAuto    = event.ReasonAutomated
	xOther   = event.ReasonOtherFocus
	xAmbig   = event.ReasonAmbiguous
	xLookCap = event.ReasonLookupCap
	// xBucketCap is X.bucket_cap.
	xBucketCap = event.ReasonBucketCap
)

func TestCredit(t *testing.T) {
	typedPrompt := func(resume int64) func(b *tl) {
		return func(b *tl) { b.prompt(at(resume+19), event.ViaHook, "s_1", "p_1", "t_2", 30) }
	}
	// lookup is SM 2.5's case: editor work, 240 s in the browser from 60 s,
	// then editor work again. after adds the navigation at 330 s whose lead
	// brackets the browser run; agent runs a scheduled turn during it.
	lookup := func(after, agent bool) func(b *tl) {
		return func(b *tl) {
			b.focus(at(0), event.AppEditor, "p_1")
			b.mouse(every(at(1), at(596), 5000)...)
			b.editor(at(60), event.ViaEditor, "p_1", event.ActNavigate, event.EditSourceUnknown)
			b.focus(at(60), event.AppBrowser, "")
			b.focus(at(300), event.AppEditor, "p_1")
			if after {
				b.editor(at(330), event.ViaEditor, "p_1", event.ActNavigate, event.EditSourceUnknown)
			}
			if agent {
				b.start(at(0), "s_1", "p_1", event.EntrypointCLI, event.ParentTerminalApp, true)
				scheduled(b.prompt(at(100), event.ViaHook, "s_1", "p_1", "t_1", 30))
				b.agentStops(at(150), "s_1", "t_1")
			}
			b.samples(at(0), at(600))
		}
	}
	// back is SM 2.4's TTL case: a 300 s answer at 61 s, read for 30 s,
	// and the person back at ret s with a prompt 200 s later, whose lead
	// starts 60 s after ret.
	back := func(ret int64) func(b *tl) {
		return func(b *tl) {
			terminalSession(b)
			b.mouse(every(at(1), at(61), 5000)...)
			b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 30)
			b.agentSays(at(61), "s_1", "t_1", 900)
			b.agentStops(at(62), "s_1", "t_1")
			b.mouse(every(at(ret+1), at(ret+196), 5000)...)
			b.prompt(at(ret+200), event.ViaHook, "s_1", "p_1", "t_2", 30)
			b.samples(at(0), at(ret+260))
		}
	}
	cases := []struct {
		name  string
		keys  bool
		build func(b *tl)
		want  []secondsWant
	}{
		{
			name: "SM 2.4: a prompt resets the bucket to R_TURN_BASE_S, output adds to it, and present seconds drain it as SUP while the agent runs and READ after",
			build: func(b *tl) {
				terminalSession(b)
				b.mouse(every(at(1), at(296), 5000)...)
				b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 30)
				b.agentSays(at(70), "s_1", "t_1", 45)
				b.agentSays(at(90), "s_1", "t_1", 30)
				b.agentStops(at(90), "s_1", "t_1")
				b.samples(at(0), at(300))
			},
			want: []secondsWant{
				{0, 60, map[event.Reason]int64{rP: 60}},
				{60, 90, map[event.Reason]int64{rSUP: 30}},
				{90, 105, map[event.Reason]int64{rREAD: 15}},
				{105, 300, map[event.Reason]int64{xNoAnch: 195}},
			},
		},
		{
			name: "SM 2.4: a scheduled turn's output fills the bucket without resetting it, and its idle seconds are X.automated",
			build: func(b *tl) {
				terminalSession(b)
				b.mouse(every(at(1), at(296), 5000)...)
				b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 30)
				// 300 words are 100 s of reading, so with the base 60 s are
				// still unread when the scheduled prompt comes at 120 s. A
				// reset that cleared them would make [120, 135) X.automated.
				b.agentSays(at(65), "s_1", "t_1", 300)
				b.agentStops(at(65), "s_1", "t_1")
				scheduled(b.prompt(at(120), event.ViaHook, "s_1", "p_1", "t_2", 30))
				b.agentSays(at(135), "s_1", "t_2", 30)
				b.agentStops(at(200), "s_1", "t_2")
				b.samples(at(0), at(300))
			},
			want: []secondsWant{
				{60, 75, map[event.Reason]int64{rSUP: 5, rREAD: 10}},
				{75, 120, map[event.Reason]int64{rREAD: 45}},
				{120, 195, map[event.Reason]int64{rSUP: 70, xAuto: 5}},
				{195, 210, map[event.Reason]int64{xAuto: 5, xNoAnch: 10}},
			},
		},
		{
			name: "SM 2.4: output past BUCKET_MAX_S evicts the oldest entries, so 1,500 s of diff leaves 600 s to read, and the reading past it is X.bucket_cap",
			build: func(b *tl) {
				terminalSession(b)
				b.mouse(every(at(1), at(1206), 5000)...)
				b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 30)
				b.agentEdits(at(61), "s_1", 200)
				b.agentStops(at(62), "s_1", "t_1")
				b.git(at(1200), "p_1", event.ActorHuman)
				b.samples(at(0), at(1260))
			},
			want: []secondsWant{
				{60, 75, map[event.Reason]int64{rSUP: 2, rREAD: 13}},
				{75, 660, map[event.Reason]int64{rREAD: 585}},
				// The second at 660 s starts READ_HORIZON_S after the
				// prompt, outside its horizon, but the commit at 1,200 s is
				// within READ_HORIZON_S after it. From there on the bucket
				// is empty only because the cap evicted 919 s of output.
				{660, 675, map[event.Reason]int64{rREAD: 1, xBucketCap: 14}},
				{675, 1140, map[event.Reason]int64{xBucketCap: 465}},
				{1140, 1200, map[event.Reason]int64{rG: 60}},
			},
		},
		{
			name:  "SM 2.4: output younger than BUCKET_TTL_S waits in the bucket for the person to come back",
			build: back(6990),
			want: []secondsWant{
				{6990, 7050, map[event.Reason]int64{rREAD: 60}},
				{7050, 7065, map[event.Reason]int64{rP: 15}},
			},
		},
		{
			name:  "SM 2.4: output older than BUCKET_TTL_S is dropped",
			build: back(7290),
			want: []secondsWant{
				{7290, 7350, map[event.Reason]int64{xNoAnch: 60}},
				{7350, 7365, map[event.Reason]int64{rP: 15}},
			},
		},
		{
			name: "SM 2.4: the bucket drains only within READ_HORIZON_S of a human act in the project",
			build: func(b *tl) {
				terminalSession(b)
				b.mouse(every(at(1), at(796), 5000)...)
				b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 30)
				b.agentSays(at(61), "s_1", "t_1", 900)
				b.agentStops(at(62), "s_1", "t_1")
				b.focus(at(75), event.AppOther, "")
				b.focus(at(495), event.AppTerminal, "p_1", "s_1")
				b.samples(at(0), at(810))
			},
			want: []secondsWant{
				{60, 75, map[event.Reason]int64{rSUP: 2, rREAD: 13}},
				{75, 495, map[event.Reason]int64{xOther: 420}},
				{495, 660, map[event.Reason]int64{rREAD: 165}},
				{660, 675, map[event.Reason]int64{xNoAnch: 15}},
				{675, 795, map[event.Reason]int64{xNoAnch: 120}},
			},
		},
		{
			name: "SM 2.4: a queued message resets the bucket when the agent picks it up, so what the agent writes in answer is there to read",
			build: func(b *tl) {
				terminalSession(b)
				b.mouse(every(at(1), at(896), 5000)...)
				b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 30)
				// A message typed while the agent works, which it takes up
				// at its next tool call, then edits and sums up.
				b.harness(at(95), event.ViaHook, "s_1", "p_1", &event.AgentQueue{TurnKey: "t_q", LenChars: 40})
				b.harness(at(100), event.ViaHook, "s_1", "p_1", &event.AgentTool{ToolClass: event.ToolBash, TStart: at(97),
					TEnd: at(100), VisibleOutputLines: 2})
				b.agentEdits(at(150), "s_1", 200)
				b.agentSays(at(290), "s_1", "t_1", 300)
				b.agentStops(at(300), "s_1", "t_1")
				b.samples(at(0), at(900))
			},
			want: []secondsWant{
				// The Q anchor at 95 s qualifies reading until 695 s.
				{300, 690, map[event.Reason]int64{rREAD: 390}},
			},
		},
		{
			name: "SM 3.3 rule 4: of two sessions in the project, the bucket with the oldest entry drains first",
			build: func(b *tl) {
				b.focus(at(0), event.AppTerminal, "p_1", "s_1", "s_2")
				b.start(at(0), "s_1", "p_1", event.EntrypointCLI, event.ParentTerminalApp, true)
				b.start(at(0), "s_2", "p_1", event.EntrypointCLI, event.ParentTerminalApp, true)
				b.mouse(every(at(1), at(296), 5000)...)
				// The older entry is in s_2, the session with the higher
				// key, so draining by key order instead would differ.
				b.prompt(at(60), event.ViaHook, "s_2", "p_1", "t_1", 30)
				b.focus(atMS(60_500), event.AppOther, "")
				b.agentStops(at(61), "s_2", "t_1")
				b.focus(at(90), event.AppTerminal, "p_1", "s_1", "s_2")
				b.prompt(at(100), event.ViaHook, "s_1", "p_1", "t_2", 30)
				// The agent samples at every hook event (DESIGN 3.3), which
				// shows the input before this mid-slot prompt.
				b.sample(at(100))
				b.agentStops(at(200), "s_1", "t_2")
				b.samples(at(0), at(300))
			},
			want: []secondsWant{
				{90, 105, map[event.Reason]int64{rP: 10, rREAD: 5}},
				{105, 120, map[event.Reason]int64{rREAD: 15}},
				{120, 135, map[event.Reason]int64{rSUP: 15}},
				{135, 150, map[event.Reason]int64{rSUP: 5, xAgent: 10}},
			},
		},
		{
			name:  "SM 2.5: a browser run bracketed by credit in the project is LOOKUP up to LOOKUP_MAX_S, and X.lookup_cap after",
			build: lookup(true, false),
			want: []secondsWant{
				{0, 60, map[event.Reason]int64{rNAV: 60}},
				{60, 240, map[event.Reason]int64{rLOOKUP: 180}},
				{240, 300, map[event.Reason]int64{xLookCap: 60}},
				{300, 330, map[event.Reason]int64{rNAV: 30}},
				{330, 600, map[event.Reason]int64{xNoAnch: 270}},
			},
		},
		{
			name:  "SM 2.5: a browser run with no credit after it is X.other_focus",
			build: lookup(false, false),
			want:  []secondsWant{{60, 300, map[event.Reason]int64{xOther: 240}}},
		},
		{
			name:  "SM 2.5: a browser run while the project's agent runs is waiting, not a lookup",
			build: lookup(true, true),
			want:  []secondsWant{{60, 300, map[event.Reason]int64{xOther: 240}}},
		},
		{
			name: "SM 3.3 rule 3: the lead of the next act beats the trail of an earlier one",
			build: func(b *tl) {
				terminalSession(b)
				b.mouse(every(at(1), at(296), 5000)...)
				b.harness(at(100), event.ViaHook, "s_1", "p_1", &event.AgentPermission{RequestedAt: at(90), AnsweredAt: at(100),
					Decision: event.DecisionAccept, DecidedBy: event.DecidedByUser})
				b.focus(at(120), event.AppEditor, "p_1")
				b.editor(at(150), event.ViaEditor, "p_1", event.ActNavigate, event.EditSourceUnknown)
				b.samples(at(0), at(300))
			},
			want: []secondsWant{
				{0, 30, map[event.Reason]int64{xNoAnch: 30}},
				{30, 90, map[event.Reason]int64{rNAV: 60}},
				{90, 105, map[event.Reason]int64{rPA: 10, rNAV: 5}},
				{105, 150, map[event.Reason]int64{rNAV: 45}},
			},
		},
		{
			name: "SM 2.7: where the OS reports key presses, a lead's typing part isn't realized in a slot without one",
			keys: true,
			build: func(b *tl) {
				terminalSession(b)
				b.mouse(every(at(1), at(44), 5000)...)
				b.press(every(atMS(45_500), atMS(59_500), 1000)...)
				b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 45)
				b.mouse(every(at(61), at(116), 5000)...)
				b.samples(at(0), at(120))
			},
			want: []secondsWant{
				{0, 30, map[event.Reason]int64{rP: 30}},
				{30, 45, map[event.Reason]int64{xNoAnch: 15}},
				{45, 60, map[event.Reason]int64{rP: 15}},
			},
		},
		{
			name:  "SM 2.1: a returned run credits the unread prose left in the bucket as READ~r and the prompt's think lead as P~r, and the rest is X.absent",
			build: func(b *tl) { *b = *silence(event.AppTerminal, 451, typedPrompt(451)) },
			want: []secondsWant{
				{105, 450, map[event.Reason]int64{rREAD: 25, rREAD + event.ReturnedSuffix: 25, rP + event.ReturnedSuffix: 120,
					xAbsent: 175}},
			},
		},
		{
			name: "SM 2.1: a prompt's own hook sample doesn't date the end of a silence, so the typing before it in that slot is P",
			build: func(b *tl) {
				*b = *silence(event.AppTerminal, 451, func(b *tl) {
					b.prompt(at(460), event.ViaHook, "s_1", "p_1", "t_2", 30)
					b.sample(at(460))
				})
			},
			want: []secondsWant{{450, 465, map[event.Reason]int64{rP: 10, rSUP: 5}}},
		},
		{
			name: "SM 5.2: a no-input prompt opens no window, and while its turn runs alone the project's seconds are X.no_input",
			build: func(b *tl) {
				terminalSession(b)
				// No input in the KEYS_PRE_S before the prompt at 60 s, as
				// when a script sends it into the terminal (SM 2.2 rule 8).
				b.mouse(every(at(1), at(44), 5000)...)
				b.mouse(every(at(61), at(296), 5000)...)
				b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 30)
				b.agentStops(at(120), "s_1", "t_1")
				b.samples(at(0), at(300))
			},
			want: []secondsWant{
				{0, 60, map[event.Reason]int64{xNoAnch: 60}},
				{60, 120, map[event.Reason]int64{event.ReasonNoInput: 60}},
				{120, 135, map[event.Reason]int64{xNoAnch: 15}},
			},
		},
		{
			name: "SM 5.2: focus on a terminal that resolves to no project is X.ambiguous, on another app X.other_focus",
			build: func(b *tl) {
				b.focus(at(0), event.AppTerminal, "")
				b.mouse(every(at(1), at(56), 5000)...)
				b.focus(at(30), event.AppOther, "")
				b.samples(at(0), at(60))
			},
			want: []secondsWant{
				{0, 30, map[event.Reason]int64{xAmbig: 30}},
				{30, 45, map[event.Reason]int64{xOther: 15}},
			},
		},
		{
			name: "E-6.6-codes: a slot before the client's first os.focus record is X.ambiguous, not X.other_focus",
			build: func(b *tl) {
				b.mouse(every(at(1), at(56), 5000)...)
				b.focus(at(30), event.AppTerminal, "p_1")
				b.samples(at(0), at(60))
			},
			want: []secondsWant{
				{0, 30, map[event.Reason]int64{xAmbig: 30}},
				{30, 60, map[event.Reason]int64{xNoAnch: 30}},
			},
		},
		{
			name: "SM 2.5, 5.2: a slot whose focus the client recorded as unknown skips X.ambiguous and X.other_focus, so with no turn running it is X.no_anchor",
			build: func(b *tl) {
				b.os(at(0), &event.OSFocus{AppClass: event.AppUnknown})
				b.mouse(every(at(1), at(56), 5000)...)
				b.focus(at(30), event.AppOther, "")
				b.samples(at(0), at(60))
			},
			want: []secondsWant{
				{0, 30, map[event.Reason]int64{xNoAnch: 30}},
				{30, 45, map[event.Reason]int64{xOther: 15}},
			},
		},
		{
			name: "SM 5.2: while a person's turn and an automated turn of the project both run, the seconds nothing credits are X.agent_only",
			build: func(b *tl) {
				b.focus(at(0), event.AppTerminal, "p_1", "s_1", "s_2")
				b.start(at(0), "s_1", "p_1", event.EntrypointCLI, event.ParentTerminalApp, true)
				b.start(at(0), "s_2", "p_1", event.EntrypointCLI, event.ParentTerminalApp, true)
				b.mouse(every(at(1), at(296), 5000)...)
				b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 30)
				scheduled(b.prompt(at(61), event.ViaHook, "s_2", "p_1", "t_2", 30))
				b.agentStops(at(290), "s_1", "t_1")
				b.agentStops(at(290), "s_2", "t_2")
				b.samples(at(0), at(300))
			},
			want: []secondsWant{{90, 285, map[event.Reason]int64{xAgent: 195}}},
		},
		{
			name: "SM 5.2: while an automated turn and a no-input turn of the project both run, the seconds nothing credits are X.automated",
			build: func(b *tl) {
				b.focus(at(0), event.AppTerminal, "p_1", "s_1", "s_2")
				b.start(at(0), "s_1", "p_1", event.EntrypointCLI, event.ParentTerminalApp, true)
				b.start(at(0), "s_2", "p_1", event.EntrypointCLI, event.ParentTerminalApp, true)
				// No input in the KEYS_PRE_S before s_1's prompt at 60 s, so
				// it is no_input (SM 2.2 rule 8).
				b.mouse(every(at(1), at(44), 5000)...)
				b.mouse(every(at(61), at(296), 5000)...)
				b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 30)
				scheduled(b.prompt(at(61), event.ViaHook, "s_2", "p_1", "t_2", 30))
				b.agentStops(at(290), "s_1", "t_1")
				b.agentStops(at(290), "s_2", "t_2")
				b.samples(at(0), at(300))
			},
			want: []secondsWant{{75, 285, map[event.Reason]int64{xAuto: 210}}},
		},
		{
			name: "E-6.6-codes: an automated turn in a slot whose terminal hosts no harness for most of it is X.ambiguous",
			build: func(b *tl) {
				b.focus(at(0), event.AppTerminal, "")
				b.mouse(every(at(1), at(116), 5000)...)
				b.start(at(50), "s_1", "p_1", event.EntrypointCLI, event.ParentTerminalApp, true)
				scheduled(b.prompt(at(50), event.ViaHook, "s_1", "p_1", "t_1", 30))
				// The agent walks the window again a moment after the session
				// starts under it, and again when it exits (SM 3.2).
				b.focus(at(58), event.AppTerminal, "p_1", "s_1")
				b.agentStops(at(70), "s_1", "t_1")
				b.focus(at(70), event.AppTerminal, "")
				b.samples(at(0), at(120))
			},
			want: []secondsWant{
				{45, 60, map[event.Reason]int64{xAmbig: 15}},
				{60, 75, map[event.Reason]int64{xAuto: 10, xNoAnch: 5}},
			},
		},
		{
			name: "SM 2.4, 5.2: output BUCKET_MAX_S evicted drains as X.bucket_cap until it runs out, and then the seconds are X.no_anchor",
			build: func(b *tl) {
				terminalSession(b)
				b.mouse(every(at(1), at(956), 5000)...)
				b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 30)
				// 675 s of diff on top of what is left of the 20 s base
				// evicts 94 s: 19 s of base and 75 s of the diff.
				b.agentEdits(at(61), "s_1", 90)
				b.agentStops(at(62), "s_1", "t_1")
				b.git(at(900), "p_1", event.ActorHuman)
				b.samples(at(0), at(960))
			},
			want: []secondsWant{
				{660, 675, map[event.Reason]int64{rREAD: 1, xBucketCap: 14}},
				{675, 750, map[event.Reason]int64{xBucketCap: 75}},
				{750, 765, map[event.Reason]int64{xBucketCap: 5, xNoAnch: 10}},
				{765, 840, map[event.Reason]int64{xNoAnch: 75}},
				{840, 900, map[event.Reason]int64{rG: 60}},
			},
		},
		{
			name: "SM 2.4: side-door output past its turn's R_TURN_BASE_S never enters the bucket, so it gives no X.bucket_cap",
			build: func(b *tl) {
				plugin := func(tm int64, p event.Payload) event.Event {
					return b.emit(tm, event.Envelope{Source: event.SourceHook, Via: event.ViaPlugin, Harness: event.HarnessOther,
						SessionKey: "s_9", ProjectKey: "p_1"}, p)
				}
				b.focus(at(0), event.AppTerminal, "p_1", "s_9")
				b.mouse(every(at(1), at(296), 5000)...)
				scheduled(plugin(at(60), &event.PromptSubmit{TurnKey: "t_9", LenChars: 30, TypedLenEst: 30,
					OriginDeclared: event.DeclaredNone}))
				plugin(at(70), &event.AgentOutput{TurnKey: "t_9", Words: 9000})
				plugin(at(70), &event.AgentStop{TurnKey: "t_9", StopReason: event.StopEndTurn})
				b.git(at(100), "p_1", event.ActorHuman)
				b.samples(at(0), at(300))
			},
			want: []secondsWant{
				{90, 120, map[event.Reason]int64{rG: 10, rREAD: 20}},
				{120, 300, map[event.Reason]int64{xNoAnch: 180}},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(c.keys)
			c.build(b)
			d := b.run(t, Device{})
			for _, w := range c.want {
				if got := sumIn(d, at(w.from), at(w.to)); !sameCounts(got, w.want) {
					t.Errorf("[%d s, %d s): %v, want %v", w.from, w.to, got, w.want)
				}
			}
		})
	}
}

// TestSideDoorFillCap checks DESIGN 5.2's cap on plugin output by itself:
// it fills a turn up to R_TURN_BASE_S, base included, so a person's plugin
// prompt gets its base and nothing more, while an automated plugin turn,
// which has no base, gets up to R_TURN_BASE_S of its output.
func TestSideDoorFillCap(t *testing.T) {
	plugin := func(b *tl, sess string, tm int64, p event.Payload) event.Event {
		return b.emit(tm, event.Envelope{Source: event.SourceHook, Via: event.ViaPlugin, Harness: event.HarnessOther,
			SessionKey: sess, ProjectKey: "p_1"}, p)
	}
	for _, c := range []struct {
		name      string
		automated bool
		fillMS    int64
	}{
		{"a person's plugin turn fills its 20 s base and nothing of its output", false, 20_000},
		{"an automated plugin turn fills R_TURN_BASE_S of its output", true, 20_000},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(false)
			b.focus(at(0), event.AppTerminal, "p_1", "s_9")
			b.mouse(every(at(1), at(116), 5000)...)
			e := plugin(b, "s_9", at(60), &event.PromptSubmit{TurnKey: "t_9", LenChars: 30, TypedLenEst: 30,
				OriginDeclared: event.DeclaredNone})
			if c.automated {
				scheduled(e)
			}
			plugin(b, "s_9", at(70), &event.AgentOutput{TurnKey: "t_9", Words: 9000})
			plugin(b, "s_9", at(70), &event.AgentStop{TurnKey: "t_9", StopReason: event.StopEndTurn})
			b.samples(at(0), at(120))
			d := b.run(t, Device{})
			if len(d.Fills) != 1 {
				t.Fatalf("fills %+v, want one", d.Fills)
			}
			f := d.Fills[0]
			if f.FillMS != c.fillMS || f.OutputMS != 3_000_000 || f.Backed {
				t.Errorf("fill %+v; want %d ms of 3,000,000 ms of output, not backed", f, c.fillMS)
			}
		})
	}
}

// readingBy sums the SUP and READ seconds credited in [from, to) by level,
// and how many of them carry label.
func readingBy(d Derived, from, to int64, label event.Label) (map[event.Evidence]int64, int64) {
	levels := map[event.Evidence]int64{}
	var labeled int64
	for _, c := range d.Credits {
		if c.Slot < from || c.Slot >= to {
			continue
		}
		for _, e := range c.Credited {
			if b := e.Reason.Base(); b != rSUP && b != rREAD {
				continue
			}
			levels[e.Level] += e.Seconds
			if slices.Contains(e.Labels, label) {
				labeled += e.Seconds
			}
		}
	}
	return levels, labeled
}

// TestSideDoorPump checks the two halves of DESIGN 5.2's cap that a side
// door could get around by claiming many turns: output fills only a turn a
// prompt opened, and what a side door put in a bucket drains at level C
// with its label, whatever anchor qualifies the drain
// (docs/errata/2026-11-20-design-6.4-side-door-reading.md).
func TestSideDoorPump(t *testing.T) {
	plugin := func(b *tl, tm int64, p event.Payload) event.Event {
		return b.emit(tm, event.Envelope{Source: event.SourceHook, Via: event.ViaPlugin, Harness: event.HarnessOther,
			SessionKey: "s_9", ProjectKey: "p_1"}, p)
	}
	// scene is a hook prompt in s_1 at 60 s, typed where the OS reports key
	// presses, with a plugin's session s_9 of the same project hosted by the
	// same terminal, and the person present until 300 s.
	scene := func(b *tl) {
		b.focus(at(0), event.AppTerminal, "p_1", "s_1", "s_9")
		b.start(at(0), "s_1", "p_1", event.EntrypointCLI, event.ParentTerminalApp, true)
		b.press(every(atMS(30_500), atMS(59_500), 1000)...)
		b.mouse(every(at(61), at(296), 5000)...)
		b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 30)
		b.agentStops(at(61), "s_1", "t_1")
	}
	cases := []struct {
		name    string
		build   func(b *tl)
		levels  map[event.Evidence]int64
		labeled int64
	}{
		{
			name: "30 plugin outputs under 30 turn keys no prompt opened fill nothing, leaving only the hook prompt's base",
			build: func(b *tl) {
				for i := int64(0); i < 30; i++ {
					plugin(b, at(62+i), &event.AgentOutput{TurnKey: fmt.Sprintf("t_c%02d", i), Words: 300})
				}
			},
			levels: map[event.Evidence]int64{event.EvidenceA: 20},
		},
		{
			name: "output a scheduled plugin turn fills drains at level C with via_plugin, after the hook prompt's base at level A",
			build: func(b *tl) {
				scheduled(plugin(b, at(62), &event.PromptSubmit{TurnKey: "t_9", LenChars: 30, TypedLenEst: 30,
					OriginDeclared: event.DeclaredNone}))
				plugin(b, at(63), &event.AgentOutput{TurnKey: "t_9", Words: 300})
				plugin(b, at(64), &event.AgentStop{TurnKey: "t_9", StopReason: event.StopEndTurn})
			},
			levels:  map[event.Evidence]int64{event.EvidenceA: 20, event.EvidenceC: 20},
			labeled: 20,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(true)
			scene(b)
			c.build(b)
			b.samples(at(0), at(300))
			d := b.run(t, Device{})
			levels, labeled := readingBy(d, at(60), at(300), event.LabelViaPlugin)
			if !maps.Equal(levels, c.levels) || labeled != c.labeled {
				t.Errorf("reading by level %v, %d s labeled via_plugin; want %v and %d", levels, labeled, c.levels, c.labeled)
			}
			for _, f := range d.Fills {
				if f.SessionKey == "s_9" && f.FillMS > 20_000 {
					t.Errorf("the plugin's turn %s fills %d ms", f.TurnKey, f.FillMS)
				}
			}
		})
	}
}

// sideDoorScene is D-side-door-reading's scene: a hook prompt in s_1 at
// 60 s, level B with no key presses reported, with a plugin's session s_9
// of the same project hosted by the same terminal, and the person present
// until 300 s. plugin writes a record as TestSideDoorPump's plugin does.
func sideDoorScene(b *tl) {
	b.focus(at(0), event.AppTerminal, "p_1", "s_1", "s_9")
	b.start(at(0), "s_1", "p_1", event.EntrypointCLI, event.ParentTerminalApp, true)
	b.mouse(every(at(1), at(296), 5000)...)
	b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 30)
	b.agentStops(at(61), "s_1", "t_1")
}

func sideDoorPlugin(b *tl, tm int64, p event.Payload) event.Event {
	return b.emit(tm, event.Envelope{Source: event.SourceHook, Via: event.ViaPlugin, Harness: event.HarnessOther,
		SessionKey: "s_9", ProjectKey: "p_1"}, p)
}

// TestSideDoorBase checks rule 3 of the side-door reading erratum
// (docs/errata/2026-11-20-design-6.4-side-door-reading.md, decided as
// D-side-door-reading): the R_TURN_BASE_S a track call, a plugin or bridge
// prompt, or a queued message a plugin reported starts its turn with
// drains at no better level than that act, with its side-door label,
// though a later hook prompt qualifies the drain at level B (DESIGN 6.4).
func TestSideDoorBase(t *testing.T) {
	cases := []struct {
		name    string
		build   func(b *tl)
		label   event.Label
		levels  map[event.Evidence]int64
		labeled int64
	}{
		{
			name: "a person's plugin prompt starts its turn with a base that drains at the prompt's level C with via_plugin, though the later hook prompt qualifies the drain at level B",
			build: func(b *tl) {
				sideDoorPlugin(b, at(20), &event.PromptSubmit{TurnKey: "t_9", LenChars: 30, TypedLenEst: 30,
					OriginDeclared: event.DeclaredNone})
			},
			label:   event.LabelViaPlugin,
			levels:  map[event.Evidence]int64{event.EvidenceB: 20, event.EvidenceC: 20},
			labeled: 20,
		},
		{
			name:    "a pairtime track call starts its turn with a base that drains at the call's level C with via_track, though the later hook prompt qualifies the drain at level B",
			build:   func(b *tl) { b.track(at(20), "s_9", 30, event.LogNoReader, "") },
			label:   event.LabelViaTrack,
			levels:  map[event.Evidence]int64{event.EvidenceB: 20, event.EvidenceC: 20},
			labeled: 20,
		},
		{
			name:   "with no side-door act, the hook prompt's base drains at its own level B, unlabeled",
			build:  func(*tl) {},
			label:  event.LabelViaPlugin,
			levels: map[event.Evidence]int64{event.EvidenceB: 20},
		},
		{
			name: "a queued message that a plugin reported starts its turn with a base that drains at the message's level C with via_plugin, though the later hook prompt qualifies the drain at level B",
			build: func(b *tl) {
				sideDoorPlugin(b, at(20), &event.AgentQueue{TurnKey: "t_q", LenChars: 30})
			},
			label:   event.LabelViaPlugin,
			levels:  map[event.Evidence]int64{event.EvidenceB: 20, event.EvidenceC: 20},
			labeled: 20,
		},
		{
			name: "a queued message that a hook reported starts its turn with a base that drains at the qualifying anchor's level B, unlabeled",
			build: func(b *tl) {
				b.harness(at(20), event.ViaHook, "s_9", "p_1", &event.AgentQueue{TurnKey: "t_q", LenChars: 30})
			},
			label:  event.LabelViaPlugin,
			levels: map[event.Evidence]int64{event.EvidenceB: 40},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(false)
			sideDoorScene(b)
			c.build(b)
			b.samples(at(0), at(300))
			d := b.run(t, Device{})
			levels, labeled := readingBy(d, at(60), at(300), c.label)
			if !maps.Equal(levels, c.levels) || labeled != c.labeled {
				t.Errorf("reading by level %v, %d s labeled %s; want %v and %d", levels, labeled, c.label, c.levels, c.labeled)
			}
		})
	}
}

// TestSideDoorLookup checks rule 4 of the side-door reading erratum: a
// lookup takes the lower level of its two brackets as they were credited,
// and a bracket's side-door label, not the levels of the anchors that
// qualify them (DESIGN 6.4). The browser is in front from 90 s to 150 s.
func TestSideDoorLookup(t *testing.T) {
	cases := []struct {
		name    string
		build   func(b *tl)
		levels  map[event.Evidence]int64
		labeled int64
	}{
		{
			name: "a lookup whose later bracket drained from a scheduled plugin turn's output takes level C and via_plugin, though the hook prompt qualifies both brackets at level B",
			build: func(b *tl) {
				says(b, at(61), "s_1", "p_1", "t_1", 30)
				scheduled(sideDoorPlugin(b, at(62), &event.PromptSubmit{TurnKey: "t_9", LenChars: 30, TypedLenEst: 30,
					OriginDeclared: event.DeclaredNone}))
				sideDoorPlugin(b, at(63), &event.AgentOutput{TurnKey: "t_9", Words: 300})
				sideDoorPlugin(b, at(64), &event.AgentStop{TurnKey: "t_9", StopReason: event.StopEndTurn})
			},
			levels:  map[event.Evidence]int64{event.EvidenceC: 60},
			labeled: 60,
		},
		{
			name:   "the same run bracketed after by the hook session's own output takes level B, unlabeled",
			build:  func(b *tl) { says(b, at(61), "s_1", "p_1", "t_1", 90) },
			levels: map[event.Evidence]int64{event.EvidenceB: 60},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(false)
			sideDoorScene(b)
			b.focus(at(90), event.AppBrowser, "")
			b.focus(at(150), event.AppTerminal, "p_1", "s_1", "s_9")
			c.build(b)
			b.samples(at(0), at(300))
			d := b.run(t, Device{})
			levels := map[event.Evidence]int64{}
			var labeled int64
			for _, cr := range d.Credits {
				if cr.Slot < at(90) || cr.Slot >= at(150) {
					continue
				}
				for _, e := range cr.Credited {
					if e.Reason.Base() != event.ReasonLookup {
						continue
					}
					levels[e.Level] += e.Seconds
					if slices.Contains(e.Labels, event.LabelViaPlugin) {
						labeled += e.Seconds
					}
				}
			}
			if !maps.Equal(levels, c.levels) || labeled != c.labeled {
				t.Errorf("LOOKUP by level %v, %d s labeled via_plugin; want %v and %d", levels, labeled, c.levels, c.labeled)
			}
		})
	}
}

func creditAt(d Derived, slot int64) (Credit, bool) {
	i := slices.IndexFunc(d.Credits, func(c Credit) bool { return c.Slot == slot })
	if i < 0 {
		return Credit{}, false
	}
	return d.Credits[i], true
}

// TestAgentTime checks SM 5.1's agent_running_s and SM 2.6's agent-only
// seconds where turns of two projects run at once, through a screen lock,
// where no second has any other state.
func TestAgentTime(t *testing.T) {
	// A typed turn in p_1 runs from 60 s to 300 s and a scheduled one in
	// p_2 from 100 s to 200 s. The person is in p_1's terminal, and the
	// screen is locked from 150 s to 240 s.
	b := newTL(false)
	terminalSession(b)
	b.harness(at(0), event.ViaHook, "s_2", "p_2", &event.SessionStart{StartKind: event.StartStartup,
		Entrypoint: event.EntrypointCLI, ParentClass: event.ParentTerminalApp, HasTTY: true})
	b.mouse(every(at(1), at(146), 5000)...)
	b.mouse(every(at(241), at(356), 5000)...)
	b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 30)
	scheduled(b.prompt(at(100), event.ViaHook, "s_2", "p_2", "t_2", 30))
	b.harness(at(200), event.ViaHook, "s_2", "p_2", &event.AgentStop{TurnKey: "t_2", StopReason: event.StopEndTurn})
	b.agentStops(at(300), "s_1", "t_1")
	b.samples(at(0), at(149))
	b.power(at(150), event.PowerLock)
	b.samples(at(150), at(239))
	b.power(at(240), event.PowerUnlock)
	b.samples(at(240), at(360))
	d := b.run(t, Device{})
	for _, c := range []struct {
		name      string
		slot      int64
		agentS    int64
		agentOnly map[string]int64
		absent    int64
	}{
		{"while both run, each second counts once, and every uncredited second is agent-only for both projects", 105, 15,
			map[string]int64{"p_1": 15, "p_2": 15}, 0},
		{"through the lock both turns still run, and nothing credits either", 150, 15,
			map[string]int64{"p_1": 15, "p_2": 15}, 15},
		{"p_2's turn stops at 200 s, inside the lock", 195, 15, map[string]int64{"p_1": 15, "p_2": 5}, 15},
		{"after the lock only p_1's turn runs", 240, 15, map[string]int64{"p_1": 15}, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			cr, ok := creditAt(d, at(c.slot))
			if !ok {
				t.Fatal("no credit slot")
			}
			if cr.AgentS != c.agentS || !maps.Equal(cr.AgentOnly, c.agentOnly) || cr.Uncredited[xAbsent] != c.absent {
				t.Errorf("agent_running_s %d, agent-only %v, X.absent %d; want %d, %v and %d", cr.AgentS, cr.AgentOnly,
					cr.Uncredited[xAbsent], c.agentS, c.agentOnly, c.absent)
			}
		})
	}
}

// TestSlotRange checks how far back slots go: from the first record, but
// no more than BUCKET_TTL_S before the presence sampler first ran, so one
// record dated a year back derives no slot for every 15 s since.
func TestSlotRange(t *testing.T) {
	r := newRules(testTable(t))
	for _, c := range []struct {
		name  string
		first int64 // seconds after base of the first record
		want  int64 // seconds after base of the first slot
	}{
		{"slots start with a record a minute before the first sample", -60, -60},
		{"a record a year before the first sample starts them BUCKET_TTL_S before it", -365 * 86_400, -r.bucketTTL / 1000},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(false)
			terminalSession(b)
			b.harness(at(c.first), event.ViaBridge, "s_b", "p_1", &event.PromptSubmit{TurnKey: "t_b", LenChars: 30,
				TypedLenEst: 30, OriginDeclared: event.DeclaredNone})
			b.mouse(every(at(1), at(56), 5000)...)
			b.samples(at(0), at(60))
			d := b.run(t, Device{})
			if len(d.Slots) == 0 {
				t.Fatal("no slots")
			}
			if d.Slots[0].Start != at(c.want) {
				t.Fatalf("slots from %d s, want %d s", (d.Slots[0].Start-base)/1000, c.want)
			}
			if want := int((60 - c.want) / 15); len(d.Slots) != want || len(d.Credits) != want {
				t.Errorf("%d slots and %d credit slots, want %d", len(d.Slots), len(d.Credits), want)
			}
			for _, s := range d.Slots {
				if s.Start < at(0) && s.Present != event.PresentUnknown {
					t.Errorf("slot at %d s before the first sample is %q", (s.Start-base)/1000, s.Present)
				}
			}
		})
	}
}

// TestLookupHoldsFinal checks that a browser run's seconds stay pending
// until its end bracket is final, since a lookup needs credit on both sides
// (SM 2.5), and that stepping in two parts gives what one Step gives.
func TestLookupHoldsFinal(t *testing.T) {
	b := newTL(false)
	b.focus(at(0), event.AppEditor, "p_1")
	b.mouse(every(at(1), at(5996), 5000)...)
	b.editor(at(60), event.ViaEditor, "p_1", event.ActNavigate, event.EditSourceUnknown)
	b.focus(at(60), event.AppBrowser, "")
	b.focus(at(3000), event.AppEditor, "p_1")
	b.editor(at(3030), event.ViaEditor, "p_1", event.ActNavigate, event.EditSourceUnknown)
	b.samples(at(0), at(6000))
	recs := b.records()
	cut := 0
	for cut < len(recs) && recs[cut].T < at(2990) {
		cut++
	}
	s1, o1 := Step(NewState(testTable(t), Device{}), recs[:cut])
	t.Run("while the browser run goes on, nothing from its start on is final", func(t *testing.T) {
		if o1.FinalBefore != at(60) {
			t.Errorf("FinalBefore %d s, want 60 s", (o1.FinalBefore-base)/1000)
		}
	})
	_, o2 := Step(s1, recs[cut:])
	t.Run("once its end bracket is final, the run's lookup credit is too", func(t *testing.T) {
		r := newRules(testTable(t))
		if want := floorSlot(at(6000) - r.settle); o2.FinalBefore != want {
			t.Errorf("FinalBefore %d s, want %d s", (o2.FinalBefore-base)/1000, (want-base)/1000)
		}
		if got := sumIn(o2.Final, at(60), at(240)); !sameCounts(got, map[event.Reason]int64{rLOOKUP: 180}) {
			t.Errorf("the run's first 180 s are %v, want LOOKUP", got)
		}
	})
	t.Run("two Steps give what one Step gives", func(t *testing.T) {
		var all Derived
		all.Append(o1.Final)
		all.Append(o2.Final)
		all.Append(o2.Pending)
		if !bytes.Equal(marshal(t, all), marshal(t, scoreAll(t, Device{}, recs))) {
			t.Error("the Finals and the last Pending differ from one Step's output")
		}
	})
	t.Run("a browser run with nothing credited in the LOOKUP_BRACKET_S before it is no lookup and holds nothing back", func(t *testing.T) {
		b := newTL(false)
		b.focus(at(0), event.AppEditor, "p_1")
		b.mouse(every(at(1), at(5996), 5000)...)
		b.focus(at(60), event.AppBrowser, "")
		b.focus(at(3000), event.AppEditor, "p_1")
		b.editor(at(3030), event.ViaEditor, "p_1", event.ActNavigate, event.EditSourceUnknown)
		b.samples(at(0), at(6000))
		recs := b.records()
		cut := 0
		for cut < len(recs) && recs[cut].T < at(2990) {
			cut++
		}
		_, o := Step(NewState(testTable(t), Device{}), recs[:cut])
		r := newRules(testTable(t))
		if want := floorSlot(recs[cut-1].T - r.settle); o.FinalBefore != want {
			t.Errorf("FinalBefore %d s, want %d s", (o.FinalBefore-base)/1000, (want-base)/1000)
		}
	})
}
