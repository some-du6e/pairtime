// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"slices"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
)

// unknownFocus writes the os.focus a desktop that hides which window is in
// front gets: app_class unknown and nothing else (SM 1.2, 2.5).
func unknownFocus(b *tl, t int64) { b.os(t, &event.OSFocus{AppClass: event.AppUnknown}) }

// creditBy returns the seconds credited in [from, to), by level, and how
// many of them carry label.
func creditBy(d Derived, from, to int64, label event.Label) (map[event.Evidence]int64, int64) {
	levels := map[event.Evidence]int64{}
	var labeled int64
	for _, c := range d.Credits {
		if c.Slot < from || c.Slot >= to {
			continue
		}
		for _, e := range c.Credited {
			levels[e.Level] += e.Seconds
			if slices.Contains(e.Labels, label) {
				labeled += e.Seconds
			}
		}
	}
	return levels, labeled
}

// uncreditedIn returns the seconds of [from, to) nothing credited, by code.
func uncreditedIn(d Derived, from, to int64) map[event.Reason]int64 {
	out := map[event.Reason]int64{}
	for _, c := range d.Credits {
		if c.Slot < from || c.Slot >= to {
			continue
		}
		for r, n := range c.Uncredited {
			out[r] += n
		}
	}
	return out
}

// TestSideDoorUnknownFocus checks that a side-door anchor never reaches
// level A where focus is unknown (DESIGN 6.4 row A; SM 2.5): its keyboard
// test reads input in the window hosting its session, and a window the
// client couldn't tell hosts none (sessionHost), though rule 6 passes.
func TestSideDoorUnknownFocus(t *testing.T) {
	cases := []struct {
		name    string
		focus   func(b *tl)
		want    event.Evidence
		unknown bool
	}{
		{"with the terminal hosting its session in front, a plugin prompt typed on the keyboard is level A",
			func(b *tl) { b.focus(at(0), event.AppTerminal, "p_1", "s_9") }, event.EvidenceA, false},
		{"where focus is unknown, the same plugin prompt is level C with focus_unknown",
			func(b *tl) { unknownFocus(b, at(0)) }, event.EvidenceC, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// A session only a plugin reports, as TestSideDoorPump's s_9:
			// a hook record would leave its side-door records out.
			b := newTL(true)
			c.focus(b)
			b.press(every(atMS(30_500), atMS(59_500), 1000)...)
			sideDoorPlugin(b, at(60), &event.PromptSubmit{TurnKey: "t_9", LenChars: 30, TypedLenEst: 30,
				OriginDeclared: event.DeclaredNone})
			b.samples(at(0), at(120))
			d := b.run(t, Device{})
			i := slices.IndexFunc(d.Anchors, func(a Anchor) bool { return a.Via == event.ViaPlugin })
			if i < 0 {
				t.Fatal("no anchor for the plugin prompt")
			}
			a := d.Anchors[i]
			if a.Level != c.want || slices.Contains(a.Labels, event.LabelFocusUnknown) != c.unknown {
				t.Errorf("the plugin prompt's anchor is level %s with labels %q, want %s and focus_unknown %v",
					a.Level, a.Labels, c.want, c.unknown)
			}
		})
	}
}

// TestFocusUnknownRules checks SM 2.5's rule for unknown focus where it
// meets the rest of SM, a case each: time before the first os.focus, a
// lookup, an automated run, and an anchor that passed its focus test only
// through unknown focus and credits a slot of known focus.
func TestFocusUnknownRules(t *testing.T) {
	t.Run("time before the first os.focus credits nothing, though focus turns unknown later", func(t *testing.T) {
		b := newTL(false)
		b.start(at(0), "s_1", "p_1", event.EntrypointCLI, event.ParentTerminalApp, true)
		b.mouse(every(at(1), at(296), 5000)...)
		b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 30)
		says(b, at(70), "s_1", "p_1", "t_1", 90)
		b.agentStops(at(80), "s_1", "t_1")
		unknownFocus(b, at(150))
		// Rule 6 passes on unknown focus, so this prompt is an anchor: its
		// lead reaches back before 150 s, where it credits nothing.
		b.prompt(at(200), event.ViaHook, "s_1", "p_1", "t_2", 30)
		b.agentStops(at(210), "s_1", "t_2")
		b.samples(at(0), at(300))
		d := b.run(t, Device{})
		if levels, _ := creditBy(d, at(0), at(150), ""); len(levels) != 0 {
			t.Errorf("credited before the first os.focus: %v", levels)
		}
		if levels, n := creditBy(d, at(150), at(300), event.LabelFocusUnknown); levels[event.EvidenceC] == 0 ||
			n != levels[event.EvidenceC] || len(levels) != 1 {
			t.Errorf("after the unknown os.focus, credit by level %v, %d s with focus_unknown; want level C alone, all labeled", levels, n)
		}
	})
	t.Run("a slot of unknown focus holds no lookup, where a browser between the same brackets does", func(t *testing.T) {
		lookup := func(between func(b *tl)) int64 {
			b := newTL(false)
			b.focus(at(0), event.AppTerminal, "p_1", "s_1")
			b.start(at(0), "s_1", "p_1", event.EntrypointCLI, event.ParentTerminalApp, true)
			b.mouse(every(at(1), at(296), 5000)...)
			b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 30)
			says(b, at(61), "s_1", "p_1", "t_1", 90)
			b.agentStops(at(62), "s_1", "t_1")
			between(b)
			b.focus(at(150), event.AppTerminal, "p_1", "s_1")
			b.samples(at(0), at(300))
			d := b.run(t, Device{})
			var n int64
			for _, c := range d.Credits {
				for _, e := range c.Credited {
					if e.Reason.Base() == event.ReasonLookup {
						n += e.Seconds
					}
				}
			}
			return n
		}
		if n := lookup(func(b *tl) { b.focus(at(90), event.AppBrowser, "") }); n == 0 {
			t.Fatal("the browser between the brackets holds no lookup, so the case checks nothing")
		}
		if n := lookup(func(b *tl) { unknownFocus(b, at(90)) }); n != 0 {
			t.Errorf("%d s of LOOKUP where focus is unknown, want none", n)
		}
	})
	t.Run("a claude -p run under unknown focus keeps X.automated while it runs", func(t *testing.T) {
		b := newTL(false)
		unknownFocus(b, at(0))
		b.start(at(0), "s_1", "p_1", event.EntrypointSDK, event.ParentScriptRuntime, false)
		b.mouse(every(at(1), at(296), 5000)...)
		b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 30)
		says(b, at(90), "s_1", "p_1", "t_1", 90)
		b.agentStops(at(180), "s_1", "t_1")
		b.samples(at(0), at(300))
		d := b.run(t, Device{})
		got := uncreditedIn(d, at(60), at(180))
		if got[event.ReasonAutomated] == 0 || got[event.ReasonAmbiguous] != 0 || got[event.ReasonOtherFocus] != 0 {
			t.Errorf("uncredited while the run goes %v, want X.automated and no X.ambiguous or X.other_focus", got)
		}
	})
	t.Run("a prompt that passed rule 6 only through unknown focus credits a slot of known focus at level C with focus_unknown", func(t *testing.T) {
		b := newTL(false)
		b.focus(at(0), event.AppTerminal, "p_1", "s_1")
		b.start(at(0), "s_1", "p_1", event.EntrypointCLI, event.ParentTerminalApp, true)
		b.mouse(every(at(1), at(296), 5000)...)
		unknownFocus(b, at(58))
		b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 30)
		b.focus(at(62), event.AppTerminal, "p_1", "s_1")
		says(b, at(70), "s_1", "p_1", "t_1", 90)
		b.agentStops(at(80), "s_1", "t_1")
		b.samples(at(0), at(300))
		d := b.run(t, Device{})
		i := slices.IndexFunc(d.Anchors, func(a Anchor) bool { return a.Kind == event.AnchorSubstantivePrompt })
		if i < 0 {
			t.Fatal("no anchor for the prompt")
		}
		if a := d.Anchors[i]; a.Level != event.EvidenceC || !slices.Contains(a.Labels, event.LabelFocusUnknown) {
			t.Fatalf("the prompt's anchor is level %s with labels %q, want C with focus_unknown", a.Level, a.Labels)
		}
		for _, sl := range [][2]int64{{at(0), at(45)}, {at(75), at(300)}} {
			levels, n := creditBy(d, sl[0], sl[1], event.LabelFocusUnknown)
			if levels[event.EvidenceC] == 0 || n != levels[event.EvidenceC] || len(levels) != 1 {
				t.Errorf("slots of known focus from %d s: credit by level %v, %d s with focus_unknown; want level C alone, all labeled",
					(sl[0]-at(0))/1000, levels, n)
			}
		}
	})
}
