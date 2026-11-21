// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/timelines"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/params"
)

// The oracle for WP-SC1 is testdata/timelines: SM 6's worked examples as
// events, with the anchors and turns SM names, written before the scorer
// (docs/PLAN.md, WP-FX1). These tests feed a fixture's events to the scorer
// and compare what comes out with anchors.json, and with the presence the
// fixture READMEs state.

// testTable returns parameter table 0.2.0, the newest. The tests pin it,
// so a later table with other values doesn't change what they expect.
// Table 0.1.0 predates INPUT_RELEASE_GRACE_MS, which the scorer reads, so
// the scorer can't run with it.
func testTable(t *testing.T) *params.Table {
	t.Helper()
	tab, err := params.ByVersion("0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	return tab
}

func loadFixtures(t *testing.T) []*timelines.Fixture {
	t.Helper()
	fs, err := timelines.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) == 0 {
		t.Fatal("no fixtures under testdata/timelines")
	}
	return fs
}

func fixture(t *testing.T, name string) *timelines.Fixture {
	t.Helper()
	for _, f := range loadFixtures(t) {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no fixture %s", name)
	return nil
}

// fixtureDevice says where a fixture's key presses come from. Off macOS,
// the OS reports none, so a machine with input_keys has the typing module.
func fixtureDevice(f *timelines.Fixture) Device {
	os := f.Expected.OS
	return Device{TypingModule: f.Expected.InputKeys && os != "macos" && os != "darwin"}
}

// scoreAll scores records in one Step and returns all it derived.
func scoreAll(t *testing.T, d Device, records []event.Event) Derived {
	t.Helper()
	_, out := Step(NewState(testTable(t), d), records)
	var all Derived
	all.Append(out.Final)
	all.Append(out.Pending)
	return all
}

// scoreByMinute steps the scorer once per minute of records, as the agent
// does (DESIGN 3.7), and rebuilds the whole picture from the Finals and the
// last Pending.
func scoreByMinute(t *testing.T, d Device, records []event.Event) Derived {
	t.Helper()
	s := NewState(testTable(t), d)
	var all Derived
	var out Output
	for i := 0; i < len(records); {
		minute := records[i].T / 60_000
		j := i
		for j < len(records) && records[j].T/60_000 == minute {
			j++
		}
		s, out = Step(s, records[i:j])
		all.Append(out.Final)
		i = j
	}
	all.Append(out.Pending)
	return all
}

func seconds(ms int64) float64 { return float64(ms) / 1000 }

func TestFixtureAnchors(t *testing.T) {
	for _, f := range loadFixtures(t) {
		checkAnchors(t, f, scoreAll(t, fixtureDevice(f), f.Events))
	}
}

func TestFixtureTurns(t *testing.T) {
	for _, f := range loadFixtures(t) {
		checkTurns(t, f, scoreAll(t, fixtureDevice(f), f.Events))
	}
}

// checkAnchors compares the anchors the scorer derived for a fixture with
// its anchors.json.
func checkAnchors(t *testing.T, f *timelines.Fixture, got Derived) {
	t.Helper()
	byID := map[string]Anchor{}
	for _, a := range got.Anchors {
		byID[a.ID] = a
	}
	t.Run(f.Name+" yields the anchors its oracle names and no others whose windows reach its window", func(t *testing.T) {
		// anchors.json lists the acts SM names. Records before and after a
		// fixture's window set up state and aren't checked
		// (testdata/timelines/README.md), so an act there whose windows
		// credit nothing inside the window, such as 6.1's editor heartbeat
		// at 13:57:05, needn't be listed.
		from, to := f.Expected.Window.Start.UnixMilli(), f.Expected.Window.End.UnixMilli()
		want := map[string]bool{}
		for _, a := range f.Anchors.Anchors {
			want[a.EventID] = true
		}
		for _, a := range got.Anchors {
			if !want[a.ID] && reaches(a, from, to) {
				t.Errorf("anchor %s, a %s at %d, isn't in anchors.json", a.ID, a.Kind, a.T)
			}
		}
		for _, a := range f.Anchors.Anchors {
			if _, ok := byID[a.EventID]; !ok {
				t.Errorf("no anchor for %s, the %s at %s", a.EventID, a.Kind, a.Local)
			}
		}
	})
	for _, want := range f.Anchors.Anchors {
		name := fmt.Sprintf("%s %s %s has the oracle's kind, time, project, origin, class, lead, trail, via and level",
			f.Name, want.Local, want.Kind)
		t.Run(name, func(t *testing.T) {
			a, ok := byID[want.EventID]
			if !ok {
				t.Fatalf("no anchor for event %s", want.EventID)
			}
			for _, c := range []struct {
				field     string
				got, want any
			}{
				{"kind", a.Kind, want.Kind},
				{"t", a.T, want.T},
				{"project_key", a.ProjectKey, want.ProjectKey},
				{"origin", a.Origin, want.Origin},
				{"prompt_class", a.PromptClass, want.PromptClass},
				{"lead_s", seconds(a.LeadMS), want.LeadS},
				{"trail_s", seconds(a.TrailMS), want.TrailS},
				{"via", a.Via, want.Via},
				{"level", a.Level, want.Level},
			} {
				if c.got != c.want {
					t.Errorf("%s = %v, want %v (%s)", c.field, c.got, c.want, want.SM)
				}
			}
			if err := a.Event().Validate(); err != nil {
				t.Errorf("the anchor isn't a valid event.Anchor: %v", err)
			}
		})
	}
}

// reaches reports whether any of an anchor's windows, its lead, its run or
// its trail, overlaps [from, to).
func reaches(a Anchor, from, to int64) bool {
	return a.T-a.LeadMS < to && max(a.T+a.WatchMS, a.TrailFrom+a.TrailMS) > from
}

// checkTurns compares the turns the scorer derived for a fixture with its
// anchors.json.
func checkTurns(t *testing.T, f *timelines.Fixture, got Derived) {
	t.Helper()
	byKey := map[[2]string]Turn{}
	for _, tu := range got.Turns {
		byKey[[2]string{tu.SessionKey, tu.TurnKey}] = tu
	}
	t.Run(f.Name+" yields the turns its oracle describes and no others", func(t *testing.T) {
		if len(got.Turns) != len(f.Anchors.Turns) {
			t.Errorf("%d turns, want %d", len(got.Turns), len(f.Anchors.Turns))
		}
	})
	for _, want := range f.Anchors.Turns {
		name := fmt.Sprintf("%s turn submitted at %s has the oracle's times, origin and class", f.Name, want.LocalSubmit)
		t.Run(name, func(t *testing.T) {
			tu, ok := byKey[[2]string{want.SessionKey, want.TurnKey}]
			if !ok {
				t.Fatalf("no turn %s in session %s", want.TurnKey, want.SessionKey)
			}
			if tu.TSubmit != want.TSubmit || tu.TStop != want.TStop {
				t.Errorf("turn runs %d to %d, want %d to %d", tu.TSubmit, tu.TStop, want.TSubmit, want.TStop)
			}
			if tu.Origin != want.Origin || tu.PromptClass != want.PromptClass {
				t.Errorf("origin %q, class %q; want %q, %q (%s)", tu.Origin, tu.PromptClass, want.Origin, want.PromptClass, want.SM)
			}
			if err := tu.Event().Validate(); err != nil {
				t.Errorf("the turn isn't a valid event.Turn: %v", err)
			}
		})
	}
}

// TestScriptedLoopEarnsNothing is WP-SC1's check on SM 6.6: a `claude -p`
// loop run from a script, with a jiggler, on a machine without a key
// counter.
func TestScriptedLoopEarnsNothing(t *testing.T) {
	f := fixture(t, "6.6")
	got := scoreAll(t, fixtureDevice(f), f.Events)
	t.Run("6.6 yields no anchor", func(t *testing.T) {
		if len(got.Anchors) != 0 {
			t.Errorf("%d anchors, want none: %+v", len(got.Anchors), got.Anchors)
		}
	})
	t.Run("every prompt in 6.6 is headless (SM 2.2 rule 4)", func(t *testing.T) {
		n := 0
		for _, tu := range got.Turns {
			if tu.Prompt == "" {
				continue
			}
			n++
			if tu.Origin != event.OriginHeadless {
				t.Errorf("turn %s at %d is %q, want headless", tu.TurnKey, tu.TSubmit, tu.Origin)
			}
		}
		if n != 49 {
			t.Errorf("%d prompts, want SM's 49 iterations", n)
		}
	})
}

// TestFixturePresence checks slots against the presence SM and the fixture
// READMEs state for them.
func TestFixturePresence(t *testing.T) {
	type check struct {
		name    string
		fixture string
		// at is the slot's start, as seconds after the fixture window's.
		at       int64
		present  event.Presence
		until    int64 // seconds after the window start; 0 means the slot's end
		then     event.Presence
		asleep   bool
		locked   bool
		keysNull bool
	}
	cases := []check{
		{"6.2 07:40:00 has her touch and the typing that follows: yes", "6.2", 0, event.PresentYes, 0, event.PresentYes, false, false, true},
		{"6.2 07:41:15 is silent between touches 45 s apart: bridged", "6.2", 75, event.PresentBridged, 0, event.PresentBridged, false, false, true},
		{"6.2 07:41:30 is in the same short silence: bridged", "6.2", 90, event.PresentBridged, 0, event.PresentBridged, false, false, true},
		{"6.2 07:44:30 holds her last touch at 07:44:40: yes", "6.2", 270, event.PresentYes, 0, event.PresentYes, false, false, true},
		{"6.2 07:44:45 overlaps the suspend, a hard break: no", "6.2", 285, event.PresentNo, 0, event.PresentNo, true, false, true},
		{"6.4 23:43:00 holds the last touch, at 23:43:10: yes", "6.4", 360, event.PresentYes, 0, event.PresentYes, false, false, false},
		{"6.4 23:43:15 is inside the 30 s tail: tail", "6.4", 375, event.PresentTail, 0, event.PresentTail, false, false, false},
		{"6.4 23:43:30's tail ends at 23:43:40, then no", "6.4", 390, event.PresentTail, 400, event.PresentNo, false, false, false},
		{"6.4 23:44:00 is in a long silence: no", "6.4", 420, event.PresentNo, 0, event.PresentNo, false, false, false},
		{"6.4 23:53:00 overlaps the lock at 23:53:10: no", "6.4", 960, event.PresentNo, 0, event.PresentNo, false, true, false},
		{"6.4 03:00:00 is locked: no", "6.4", 12_180, event.PresentNo, 0, event.PresentNo, false, true, false},
		{"6.4 07:29:45 holds the password typed on the locked screen: no", "6.4", 28_365, event.PresentNo, 0, event.PresentNo, false, true, false},
		{"6.4 07:30:00 follows the unlock with scrolling: yes", "6.4", 28_380, event.PresentYes, 0, event.PresentYes, false, false, false},
	}
	derived := map[string]Derived{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := fixture(t, c.fixture)
			d, ok := derived[c.fixture]
			if !ok {
				d = scoreAll(t, fixtureDevice(f), f.Events)
				derived[c.fixture] = d
			}
			start := f.Expected.Window.Start.UnixMilli() + c.at*1000
			i := slices.IndexFunc(d.Slots, func(s Slot) bool { return s.Start == start })
			if i < 0 {
				t.Fatalf("no slot at %s", time.UnixMilli(start).UTC())
			}
			s := d.Slots[i]
			until := s.End()
			if c.until != 0 {
				until = f.Expected.Window.Start.UnixMilli() + c.until*1000
			}
			if s.Present != c.present || s.PresentUntil != until || s.Then != c.then {
				t.Errorf("present %q until %d then %q, want %q until %d then %q", s.Present, s.PresentUntil, s.Then, c.present, until, c.then)
			}
			if s.Asleep != c.asleep || s.Locked != c.locked {
				t.Errorf("asleep %v locked %v, want %v and %v", s.Asleep, s.Locked, c.asleep, c.locked)
			}
			if (s.InputKeys == nil) != c.keysNull {
				t.Errorf("input_keys %v; want null %v", s.InputKeys, c.keysNull)
			}
			if err := s.Event().Validate(); err != nil {
				t.Errorf("the slot isn't a valid event.Slot: %v", err)
			}
		})
	}
	t.Run("6.6: the jiggler keeps every slot from 22:00 to 02:00 present (SM 6.6)", func(t *testing.T) {
		f := fixture(t, "6.6")
		d := scoreAll(t, fixtureDevice(f), f.Events)
		from, to := f.Expected.Window.Start.UnixMilli(), f.Expected.Window.End.UnixMilli()
		n := 0
		for _, s := range d.Slots {
			if s.Start < from || s.Start >= to {
				continue
			}
			n++
			if s.Present != event.PresentYes {
				t.Errorf("slot %s is %q, want yes", time.UnixMilli(s.Start).UTC(), s.Present)
			}
		}
		if want := int((to - from) / event.SlotMS); n != want {
			t.Errorf("%d slots in the window, want %d", n, want)
		}
	})
}

// TestFixtureSteps checks that how records are split across Steps doesn't
// change what the scorer derives: one Step over a fixture, a Step per
// minute, and one Step over the records reversed and repeated give the same
// bytes.
func TestFixtureSteps(t *testing.T) {
	for _, f := range loadFixtures(t) {
		dev := fixtureDevice(f)
		want := marshal(t, scoreAll(t, dev, f.Events))
		t.Run(f.Name+": a Step per minute gives what one Step gives", func(t *testing.T) {
			if got := marshal(t, scoreByMinute(t, dev, f.Events)); !bytes.Equal(got, want) {
				t.Error("the Finals of a Step per minute and the last Pending differ from one Step's output")
			}
		})
		t.Run(f.Name+": records in reverse order, each twice, give what one Step gives", func(t *testing.T) {
			var shuffled []event.Event
			for i := len(f.Events) - 1; i >= 0; i-- {
				shuffled = append(shuffled, f.Events[i], f.Events[i])
			}
			if got := marshal(t, scoreAll(t, dev, shuffled)); !bytes.Equal(got, want) {
				t.Error("the output depends on the order records arrive in")
			}
		})
	}
}

func marshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
