// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"maps"
	"slices"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
)

// WP-SC3's rules: one timeline across projects (SM 3), with SM 3.2 rule 1
// as E-6.3-two-tabs decides it. Most cases put two projects' sessions in one
// terminal window, as two tabs are, where the OS can't tell which is in
// front.

// says adds agent text in session sess of project proj, via a log reader.
func says(b *tl, t int64, sess, proj, turn string, words int) {
	b.harness(t, event.ViaLog, sess, proj, &event.AgentOutput{TurnKey: turn, Words: words})
}

// stops adds the agent's stop in session sess of project proj.
func stops(b *tl, t int64, sess, proj, turn string) {
	b.harness(t, event.ViaHook, sess, proj, &event.AgentStop{TurnKey: turn, StopReason: event.StopEndTurn})
}

// sharedTerminal is a terminal in focus at base that hosts s_1 of project
// p_1 and s_2 of project p_2, as two tabs of one window do, so its focus
// record names no project. A person started both from an interactive shell.
func sharedTerminal(b *tl) {
	b.focus(at(0), event.AppTerminal, "", "s_1", "s_2")
	b.start(at(0), "s_1", "p_1", event.EntrypointCLI, event.ParentTerminalApp, true)
	b.start(at(0), "s_2", "p_2", event.EntrypointCLI, event.ParentTerminalApp, true)
}

// projSecs is seconds by project, then by reason. Uncredited seconds are
// under the project "".
type projSecs = map[string]map[event.Reason]int64

// projSums sums d's credit slots in [from, to), which are slot boundaries:
// credited seconds by project and reason, and uncredited ones by reason
// under the project "".
func projSums(d Derived, from, to int64) projSecs {
	out := projSecs{}
	add := func(p string, r event.Reason, n int64) {
		if out[p] == nil {
			out[p] = map[event.Reason]int64{}
		}
		out[p][r] += n
	}
	for _, c := range d.Credits {
		if c.Slot < from || c.Slot >= to {
			continue
		}
		for _, e := range c.Credited {
			add(e.ProjectKey, e.Reason, e.Seconds)
		}
		for r, n := range c.Uncredited {
			add("", r, n)
		}
	}
	return out
}

func sameProjSecs(a, b projSecs) bool {
	return maps.EqualFunc(a, b, func(x, y map[event.Reason]int64) bool { return maps.Equal(x, y) })
}

// projWant is what a range of slots, in seconds after base, should hold.
type projWant struct {
	from, to int64
	want     projSecs
}

func TestAttribution(t *testing.T) {
	// leads has s_1's prompt at 300 s and s_2's at 420 s, 45 characters
	// each, so their leads, 120 + 30 s, start on slot boundaries: [150,
	// 300) and [270, 420). Each agent stops 10 s after its prompt.
	leads := func(b *tl) {
		sharedTerminal(b)
		b.mouse(every(at(1), at(596), 5000)...)
		b.prompt(at(300), event.ViaHook, "s_1", "p_1", "t_1", 45)
		stops(b, at(310), "s_1", "p_1", "t_1")
		b.prompt(at(420), event.ViaHook, "s_2", "p_2", "t_2", 45)
		stops(b, at(430), "s_2", "p_2", "t_2")
		b.samples(at(0), at(600))
	}
	// watch has s_1's prompt at 60 s, whose agent shows 30 s and 20 s of
	// text at 70 s and 100 s and stops at 150 s, and s_2's prompt at 405 s,
	// whose lead starts at 255 s. With waiting set, a scheduled turn of s_2
	// leaves 100 s of text in s_2's bucket at 20 s, which nothing resets.
	watch := func(waiting bool) func(b *tl) {
		return func(b *tl) {
			sharedTerminal(b)
			b.mouse(every(at(1), at(596), 5000)...)
			if waiting {
				scheduled(b.prompt(at(10), event.ViaHook, "s_2", "p_2", "t_0", 30))
				says(b, at(20), "s_2", "p_2", "t_0", 300)
				stops(b, at(30), "s_2", "p_2", "t_0")
			}
			b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 45)
			says(b, at(70), "s_1", "p_1", "t_1", 90)
			says(b, at(100), "s_1", "p_1", "t_1", 60)
			stops(b, at(150), "s_1", "p_1", "t_1")
			b.prompt(at(405), event.ViaHook, "s_2", "p_2", "t_2", 45)
			stops(b, at(410), "s_2", "p_2", "t_2")
			b.samples(at(0), at(600))
		}
	}
	// oneProject is a window whose focus record names no project, hosting
	// the sessions given, all of p_1, with a prompt in s_1 at 60 s whose
	// agent stops at once.
	oneProject := func(hosted ...string) func(b *tl) {
		return func(b *tl) {
			b.focus(at(0), event.AppTerminal, "", hosted...)
			for _, s := range hosted {
				b.start(at(0), s, "p_1", event.EntrypointCLI, event.ParentTerminalApp, true)
			}
			b.mouse(every(at(1), at(296), 5000)...)
			b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 30)
			b.agentStops(at(61), "s_1", "t_1")
			b.samples(at(0), at(300))
		}
	}
	cases := []struct {
		name  string
		build func(b *tl)
		want  []projWant
	}{
		{
			name:  "SM 3.2 rule 1: a slot that starts inside the lead of the first anchor after it in the window goes to that anchor's session",
			build: leads,
			want: []projWant{
				{150, 300, projSecs{"p_1": {rP: 150}}},
				{300, 420, projSecs{"p_2": {rP: 120}}},
			},
		},
		{
			name:  "E-6.3-two-tabs: an anchor at a slot's very start isn't that slot's next anchor, so the slot from 300 s goes to the lead of s_2's prompt",
			build: leads,
			want:  []projWant{{300, 315, projSecs{"p_2": {rP: 15}}}},
		},
		{
			name:  "SM 3.2 rule 1: with no look-ahead, the session prompted at the slot's start or less than ATTRIB_STICKY_S before it keeps the window",
			build: leads,
			want:  []projWant{{420, 540, projSecs{"p_2": {rSUP: 10, rREAD: 10}, "": {xNoAnch: 100}}}},
		},
		{
			name:  "SM 3.2 rule 1: with no anchor ahead and none less than ATTRIB_STICKY_S before, the slot is X.ambiguous",
			build: leads,
			want: []projWant{
				{0, 150, projSecs{"": {xAmbig: 150}}},
				{540, 600, projSecs{"": {xAmbig: 60}}},
			},
		},
		{
			name:  "E-6.3-two-tabs: a slot outside the next anchor's lead whose session has an empty bucket goes to the session prompted within ATTRIB_STICKY_S, as SUP while its agent shows output",
			build: watch(false),
			want: []projWant{
				{0, 60, projSecs{"p_1": {rP: 60}}},
				{60, 135, projSecs{"p_1": {rSUP: 70}, "": {xAgent: 5}}},
				{135, 180, projSecs{"": {xAgent: 15, xNoAnch: 30}}},
				{180, 255, projSecs{"": {xAmbig: 75}}},
				{255, 405, projSecs{"p_2": {rP: 150}}},
			},
		},
		{
			name:  "E-6.3-two-tabs: a slot outside the next anchor's lead goes to that anchor's session when it comes within READ_HORIZON_S and its session's bucket isn't empty, ahead of ATTRIB_STICKY_S",
			build: watch(true),
			want: []projWant{
				{0, 60, projSecs{"p_1": {rP: 60}}},
				{60, 165, projSecs{"p_2": {rREAD: 100}, "": {xNoAnch: 5}}},
				{165, 180, projSecs{"p_1": {rREAD: 15}}},
			},
		},
		{
			name:  "SM 3.2 rule 1: an ambiguous slot drains no bucket, though s_1's still holds 55 s of reading",
			build: watch(true),
			want:  []projWant{{180, 255, projSecs{"": {xAmbig: 75}}}},
		},
		{
			name:  "SM 3.2: a window whose hosted sessions all belong to one project is that project's, however long since its last anchor",
			build: oneProject("s_1", "s_3"),
			want: []projWant{
				{0, 60, projSecs{"p_1": {rP: 60}}},
				{90, 300, projSecs{"": {xNoAnch: 210}}},
			},
		},
		{
			name:  "SM 3.2 rule 1: one hosted session gives its project, though the focus record names none",
			build: oneProject("s_1"),
			want: []projWant{
				{0, 60, projSecs{"p_1": {rP: 60}}},
				{90, 300, projSecs{"": {xNoAnch: 210}}},
			},
		},
		{
			name: "SM 3.2 rule 2: an editor whose focus record names no project takes the project of the latest heartbeat with input in the same or previous slot",
			build: func(b *tl) {
				b.focus(at(0), event.AppEditor, "")
				b.mouse(every(at(1), at(296), 5000)...)
				b.editor(at(40), event.ViaEditor, "p_1", event.ActNavigate, event.EditSourceUnknown)
				b.editor(at(160), event.ViaEditor, "p_2", event.ActNavigate, event.EditSourceUnknown)
				b.samples(at(0), at(300))
			},
			want: []projWant{
				{0, 30, projSecs{"": {xAmbig: 30}}},
				{30, 60, projSecs{"p_1": {rNAV: 10}, "": {xNoAnch: 20}}},
				{60, 150, projSecs{"": {xAmbig: 90}}},
				{150, 180, projSecs{"p_2": {rNAV: 10}, "": {xNoAnch: 20}}},
				{180, 300, projSecs{"": {xAmbig: 120}}},
			},
		},
		{
			name: "SM 3.2 rules 4 and 5: a browser slot resolves to no project whatever its focus record names, so only a lookup credits it",
			build: func(b *tl) {
				b.focus(at(0), event.AppEditor, "p_1")
				b.mouse(every(at(1), at(296), 5000)...)
				b.focus(at(60), event.AppBrowser, "p_1")
				b.focus(at(90), event.AppEditor, "p_1")
				b.editor(at(150), event.ViaEditor, "p_1", event.ActNavigate, event.EditSourceUnknown)
				b.samples(at(0), at(300))
			},
			want: []projWant{
				{30, 60, projSecs{"p_1": {rNAV: 30}}},
				{60, 90, projSecs{"p_1": {rLOOKUP: 30}}},
				{90, 150, projSecs{"p_1": {rNAV: 60}}},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(false)
			c.build(b)
			d := b.run(t, Device{})
			for _, w := range c.want {
				if got := projSums(d, at(w.from), at(w.to)); !sameProjSecs(got, w.want) {
					t.Errorf("[%d s, %d s): %v, want %v", w.from, w.to, got, w.want)
				}
			}
		})
	}
}

// TestAttributionLevelCap checks DESIGN 6.4's cap in a shared terminal: a
// second in a slot that SM 3.2 rule 1 gave to a session takes no better
// level than the anchor that gave it, and carries its label
// (E-6.3-two-tabs).
func TestAttributionLevelCap(t *testing.T) {
	// track is a `pairtime track` go-ahead from s_t, a session of p_2 that
	// no hook or log reader sees (DESIGN 5.4). It resets s_t's bucket to
	// its base, and the agent samples when it comes.
	track := func(b *tl, tm int64) {
		b.emit(tm, event.Envelope{Source: event.SourceCLI, Via: event.ViaTrack, Harness: event.HarnessOther,
			SessionKey: "s_t", ProjectKey: "p_2"}, &event.CLITrack{Label: "check", TextLen: 3, LogMatch: event.LogNoReader})
		b.sample(tm)
	}
	// scene: from 90 s a terminal hosts s_1 of p_1 and s_t of p_2, and
	// names the project given. s_h, a hooked session of p_2 in no window in
	// front, has 200 s of output from a scheduled turn in its bucket. s_t's
	// calls at 100 s and 600 s are level C, with pointer input and no key
	// press before them, and p_2's human commit at 400 s is level A, with a
	// key press in its slot. s_t's bucket holds the 100 s call's base from
	// then on, so where the terminal names no project the look-ahead gives
	// its slots to s_t's call at 600 s, and they drain s_h's older output
	// first (SM 3.3 rule 4). From 251 s the commit is the anchor of p_2
	// nearest each second, which qualifies the drain at its level (SM 2.4,
	// DESIGN 6.4), until s_h's output runs out at 300 s.
	scene := func(b *tl, project string) {
		b.focus(at(0), event.AppOther, "")
		b.start(at(0), "s_1", "p_1", event.EntrypointCLI, event.ParentTerminalApp, true)
		b.start(at(0), "s_h", "p_2", event.EntrypointCLI, event.ParentTerminalApp, true)
		scheduled(b.prompt(at(10), event.ViaHook, "s_h", "p_2", "t_h", 30))
		says(b, at(11), "s_h", "p_2", "t_h", 600)
		stops(b, at(12), "s_h", "p_2", "t_h")
		b.focus(at(90), event.AppTerminal, project, "s_1", "s_t")
		b.mouse(every(at(1), at(696), 5000)...)
		track(b, at(100))
		b.press(at(395))
		b.git(at(400), "p_2", event.ActorHuman)
		track(b, at(600))
		b.samples(at(0), at(700))
	}
	cases := []struct {
		name    string
		project string
		levels  map[event.Evidence]int64
		labeled int64
	}{
		{"a level-C track anchor whose session takes a shared terminal's slots through its bucket leaves their reading level C with via_track, though a level-A commit qualifies the drain",
			"", map[event.Evidence]int64{event.EvidenceC: 45}, 45},
		{"the same reading in a window whose focus record names p_2 takes the commit's level A", "p_2",
			map[event.Evidence]int64{event.EvidenceA: 45}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(true)
			scene(b, c.project)
			d := b.run(t, Device{})
			levels, labeled := readingBy(d, at(255), at(300), event.LabelViaTrack)
			if !maps.Equal(levels, c.levels) || labeled != c.labeled {
				t.Errorf("reading in [255 s, 300 s) by level %v, %d s labeled via_track; want %v and %d", levels, labeled,
					c.levels, c.labeled)
			}
			if c.project != "" {
				return
			}
			cr, ok := creditAt(d, at(255))
			if !ok {
				t.Fatal("no credit slot at 255 s")
			}
			call, ok := anchorByID(d, cr.ResolvedBy)
			if !ok || call.T != at(600) || call.Via != event.ViaTrack {
				t.Errorf("slot at 255 s resolved by %q, want the track call at 600 s", cr.ResolvedBy)
			}
			for _, e := range cr.Credited {
				if !slices.Contains(e.AnchorIDs, cr.ResolvedBy) {
					t.Errorf("%s at 255 s names anchors %v, without the one that resolved its slot", e.Reason, e.AnchorIDs)
				}
			}
		})
	}
}
