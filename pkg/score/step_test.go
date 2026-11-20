// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"bytes"
	"fmt"
	"slices"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
)

func TestStep(t *testing.T) {
	f := fixture(t, "6.4")
	half := len(f.Events) / 2
	s0 := NewState(testTable(t), fixtureDevice(f))
	s1, o1 := Step(s0, f.Events[:half])

	t.Run("Step leaves the state it was given as it was", func(t *testing.T) {
		_, again := Step(s0, f.Events[:half])
		if !bytes.Equal(marshal(t, again), marshal(t, o1)) {
			t.Error("a second Step from the same state gave a different output")
		}
		if s0.Len() != 0 || s0.High() != 0 {
			t.Errorf("the initial state now holds %d records up to %d", s0.Len(), s0.High())
		}
		_, a := Step(s1, f.Events[half:])
		_, b := Step(s1, f.Events[half:])
		if !bytes.Equal(marshal(t, a), marshal(t, b)) {
			t.Error("two Steps from the same state gave different outputs")
		}
	})
	t.Run("FinalBefore is SM's lookahead and the pending window behind the latest record, on a slot boundary", func(t *testing.T) {
		r := newRules(testTable(t))
		if want := floorSlot(s1.High() - r.settle); o1.FinalBefore != want {
			t.Errorf("FinalBefore %d, want %d", o1.FinalBefore, want)
		}
	})
	t.Run("an old record arriving late doesn't move FinalBefore back", func(t *testing.T) {
		s2, o2 := Step(s1, f.Events[half:])
		_, o3 := Step(s2, f.Events[:1])
		if o3.FinalBefore != o2.FinalBefore {
			t.Errorf("FinalBefore went from %d to %d", o2.FinalBefore, o3.FinalBefore)
		}
		if len(o3.Final.Anchors)+len(o3.Final.Slots)+len(o3.Final.Turns) != 0 {
			t.Error("a Step that made nothing new final emitted finals")
		}
	})
	t.Run("a record without data is ignored", func(t *testing.T) {
		s, _ := Step(s0, []event.Event{{Envelope: event.Envelope{EventID: "019a0000-0000-7000-8000-000000000001", T: base}}})
		if s.Len() != 0 || s.High() != 0 {
			t.Errorf("the state holds %d records up to %d", s.Len(), s.High())
		}
	})
	t.Run("the state keeps its table and device", func(t *testing.T) {
		if s1.Params() != testTable(t) || s1.Device() != fixtureDevice(f) {
			t.Error("the table or device changed")
		}
	})
}

// firstDiff describes where two outputs first differ, for a failure
// message.
func firstDiff(a, b []byte) string {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	from := max(0, i-120)
	return fmt.Sprintf("at byte %d:\n  got  ...%s\n  want ...%s", i, a[from:min(len(a), i+120)], b[from:min(len(b), i+120)])
}

// TestStepsAgree checks that a Step per minute gives what one Step gives
// where a record is about a time long before it: whatever a record changes
// was still pending when it came (rules.reach).
func TestStepsAgree(t *testing.T) {
	cases := []struct {
		name  string
		build func(b *tl)
		check func(t *testing.T, d Derived)
	}{
		{
			name: "a typed command that ran 40 minutes, recorded when it ended, credits only its trail and ends no returned run",
			build: func(b *tl) {
				b.focus(at(0), event.AppTerminal, "p_1")
				b.mouse(every(at(1), at(96), 5000)...)
				// A silence of 480 s, then the command typed at 600 s.
				b.press(atMS(595_500))
				b.mouse(every(at(601), at(3296), 5000)...)
				b.shell(at(3002), "p_1", &event.ShellCommand{CmdClass: event.CmdRun, TStart: at(600), TEnd: at(3000),
					Interactive: true})
				b.samples(at(0), at(3300))
			},
			check: func(t *testing.T, d Derived) {
				if len(d.Anchors) != 1 || d.Anchors[0].Kind != event.AnchorTypedCommand {
					t.Fatalf("anchors %+v, want the command's", d.Anchors)
				}
				if len(d.Returns) != 0 {
					t.Errorf("returns %+v, want none: the command is decided 2,402 s after the silence", d.Returns)
				}
				if got := sumIn(d, at(540), at(900))[event.ReasonCommand]; got != 0 {
					t.Errorf("%d s of CMD in the lead and run, which its record comes too late for", got)
				}
				if got := sumIn(d, at(3000), at(3060)); !sameCounts(got, map[event.Reason]int64{event.ReasonCommand: 60}) {
					t.Errorf("the trail is %v, want CMD_TRAIL_S of CMD", got)
				}
			},
		},
		{
			name: "a permission recorded 40 minutes after its answer opens an anchor whose windows its record comes too late for",
			build: func(b *tl) {
				terminalSession(b)
				b.press(every(atMS(30_500), atMS(59_500), 1000)...)
				b.mouse(every(at(61), at(2896), 5000)...)
				b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 30)
				for s := int64(120); s < 2800; s += 300 {
					b.agentSays(at(s), "s_1", "t_1", 30)
				}
				b.harness(at(2710), event.ViaHook, "s_1", "p_1", &event.AgentPermission{RequestedAt: at(300), AnsweredAt: at(310),
					Decision: event.DecisionAccept, DecidedBy: event.DecidedByUser})
				b.agentStops(at(2800), "s_1", "t_1")
				b.samples(at(0), at(2900))
			},
			check: func(t *testing.T, d Derived) {
				if !slices.ContainsFunc(d.Anchors, func(a Anchor) bool { return a.Kind == event.AnchorPermissionAnswer }) {
					t.Error("no permission anchor")
				}
				if got := sumIn(d, at(285), at(330))[event.ReasonPermission]; got != 0 {
					t.Errorf("%d s of PA, which the record comes too late for", got)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(false)
			c.build(b)
			recs := b.records()
			one := scoreAll(t, Device{}, recs)
			if got, want := marshal(t, scoreByMinute(t, Device{}, recs)), marshal(t, one); !bytes.Equal(got, want) {
				t.Errorf("the Finals of a Step per minute and the last Pending differ from one Step's output %s", firstDiff(got, want))
			}
			checkDerived(t, one)
			if c.check != nil {
				c.check(t, one)
			}
		})
	}
}

func TestStateConstructors(t *testing.T) {
	for _, c := range []struct {
		name string
		call func()
	}{
		{"NewState needs a parameter table", func() { NewState(nil, Device{}) }},
		{"Step refuses the zero State", func() { Step(State{}, nil) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("no panic")
				}
			}()
			c.call()
		})
	}
}
