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
	t.Run("FinalBefore is PENDING_WINDOW_S behind now, on a slot boundary", func(t *testing.T) {
		r := newRules(testTable(t))
		if want := floorSlot(s1.High() - r.settle); o1.FinalBefore != want {
			t.Errorf("FinalBefore %d, want %d", o1.FinalBefore, want)
		}
	})
	t.Run("a new record dated long before FinalBefore joins the state, but what is final stays final", func(t *testing.T) {
		s2, o2 := Step(s1, f.Events[half:])
		// A sample the agent writes late, an hour into the fixture, with
		// an event id no record has.
		late := event.Event{Envelope: event.Envelope{EventID: "019a0000-0000-7000-8000-00000000ffff", DeviceID: "d_test",
			BootID: "b_test", T: f.Events[0].T + 3_600_000, Source: event.SourceOS, Via: event.ViaOS,
			Harness: event.HarnessNone, SessionKey: event.NoSession}, Data: &event.OSPresence{IdleAnyS: 0.5}}
		if late.T >= o2.FinalBefore {
			t.Fatalf("the late record at %d isn't before FinalBefore %d", late.T, o2.FinalBefore)
		}
		s3, o3 := Step(s2, []event.Event{late})
		if s3.Len() != s2.Len()+1 {
			t.Errorf("the state holds %d records, want %d", s3.Len(), s2.Len()+1)
		}
		if o3.FinalBefore != o2.FinalBefore {
			t.Errorf("FinalBefore went from %d to %d", o2.FinalBefore, o3.FinalBefore)
		}
		if f := o3.Final; len(f.Slots)+len(f.Turns)+len(f.Anchors)+len(f.Returns)+len(f.Credits)+len(f.Fills) != 0 {
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
	type stepCase struct {
		name  string
		build func(b *tl)
		check func(t *testing.T, d Derived)
	}
	// agentIn sums agent_running_s over the slots in [from, to).
	agentIn := func(d Derived, from, to int64) int64 {
		var n int64
		for _, c := range d.Credits {
			if c.Slot >= from && c.Slot < to {
				n += c.AgentS
			}
		}
		return n
	}
	// hooked is a typed prompt at 60 s in s_1, which hooks see, its first
	// output at 70 s, and the person present until 2,900 s.
	hooked := func(b *tl) {
		terminalSession(b)
		b.press(every(atMS(30_500), atMS(59_500), 1000)...)
		b.mouse(every(at(61), at(2896), 5000)...)
		b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 30)
		b.agentSays(at(70), "s_1", "t_1", 30)
	}
	running := func(want int64) func(t *testing.T, d Derived) {
		return func(t *testing.T, d Derived) {
			if got := agentIn(d, at(600), at(2400)); got != want {
				t.Errorf("agent_running_s from 600 s to 2,400 s is %d, want %d", got, want)
			}
		}
	}
	plugin := func(b *tl, tm int64, p event.Payload) event.Event {
		return b.emit(tm, event.Envelope{Source: event.SourceHook, Via: event.ViaPlugin, Harness: event.HarnessOther,
			SessionKey: "s_1", ProjectKey: "p_1"}, p)
	}
	cases := []stepCase{
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
			// Before the track-windows decision, a hook record of any date
			// made the session seen, so one Step called this call
			// track_without_prompt while the Finals kept its anchor
			// (E-track-windows).
			name: "a hook record 40 minutes after a track call leaves the call's anchor in place, in a Step per minute as in one Step",
			build: func(b *tl) {
				b.focus(at(0), event.AppTerminal, "p_1", "s_t")
				b.mouse(every(at(1), at(2696), 5000)...)
				b.track(at(60), "s_t", 30, event.LogNoReader, "")
				b.start(at(2460), "s_t", "p_1", event.EntrypointCLI, event.ParentTerminalApp, true)
				b.samples(at(0), at(2700))
			},
			check: func(t *testing.T, d Derived) {
				if len(d.Tracks) != 1 || d.Tracks[0].Outcome != TrackAnchor {
					t.Errorf("track calls %+v, want one that opened an anchor", d.Tracks)
				}
				if slices.ContainsFunc(d.Flags, func(f Flag) bool { return f.Name == FlagTrackWithoutPrompt }) {
					t.Errorf("flags %+v, want no track_without_prompt", d.Flags)
				}
			},
		},
		{
			// With the 1,200 s reach the scorer had before the return-rule
			// decision, this command returned the run (E-return-lookahead).
			name: "a typed command recorded 700 s after a silence began ends no returned run, in a Step per minute as in one Step",
			build: func(b *tl) {
				b.focus(at(0), event.AppTerminal, "p_1")
				// The last input is at 96 s, so the silence starts with the
				// slot at 105 s; it ends at the slot holding the key presses
				// before the command, typed at 470 s.
				b.mouse(every(at(1), at(96), 5000)...)
				b.press(every(atMS(460_500), atMS(469_500), 1000)...)
				b.mouse(every(at(471), at(896), 5000)...)
				b.shell(at(805), "p_1", &event.ShellCommand{CmdClass: event.CmdRun, TStart: at(470), TEnd: at(800),
					Interactive: true})
				b.samples(at(0), at(900))
			},
			check: func(t *testing.T, d Derived) {
				if len(d.Anchors) != 1 || d.Anchors[0].Kind != event.AnchorTypedCommand {
					t.Fatalf("anchors %+v, want the command's", d.Anchors)
				}
				if len(d.Returns) != 0 {
					t.Errorf("returns %+v, want none: the command is on record 700 s after the silence began", d.Returns)
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
		{
			name: "a hooked turn runs through a 40-minute tool call before its stop is recorded",
			build: func(b *tl) {
				hooked(b)
				b.harness(at(2470), event.ViaHook, "s_1", "p_1", &event.AgentTool{ToolClass: event.ToolBash, TStart: at(80),
					TEnd: at(2470), VisibleOutputLines: 2})
				b.agentStops(at(2480), "s_1", "t_1")
				b.samples(at(0), at(2900))
			},
			check: running(1800),
		},
		{
			name: "a hooked turn runs while it waits 40 minutes on a permission prompt",
			build: func(b *tl) {
				hooked(b)
				b.harness(at(2505), event.ViaHook, "s_1", "p_1", &event.AgentPermission{RequestedAt: at(100), AnsweredAt: at(2500),
					Decision: event.DecisionAccept, DecidedBy: event.DecidedByUser})
				b.agentStops(at(2520), "s_1", "t_1")
				b.samples(at(0), at(2900))
			},
			check: running(1800),
		},
		{
			name: "a continuation whose first record comes 40 minutes after the stop before it starts at that record",
			build: func(b *tl) {
				hooked(b)
				b.agentStops(at(80), "s_1", "t_1")
				b.agentSays(at(2470), "s_1", "t_2", 30)
				b.harness(at(2480), event.ViaHook, "s_1", "p_1", &event.AgentStop{TurnKey: "t_2", StopReason: event.StopEndTurn,
					StopHookActive: true})
				b.samples(at(0), at(2900))
			},
			check: func(t *testing.T, d Derived) {
				running(0)(t, d)
				if tu, ok := turnByKey(d, "t_2"); !ok || tu.TSubmit != at(2470) {
					t.Errorf("continuation %+v, want it submitted at its first record", tu)
				}
			},
		},
		{
			name: "a turn no hook sees ends at its last agent record before a 40-minute silence of them",
			build: func(b *tl) {
				b.focus(at(0), event.AppTerminal, "p_1", "s_1")
				b.mouse(every(at(1), at(2896), 5000)...)
				plugin(b, at(60), &event.PromptSubmit{TurnKey: "t_1", LenChars: 30, TypedLenEst: 30, OriginDeclared: event.DeclaredNone})
				plugin(b, at(70), &event.AgentOutput{TurnKey: "t_1", Words: 30})
				plugin(b, at(2470), &event.AgentOutput{TurnKey: "t_1", Words: 30})
				plugin(b, at(2480), &event.AgentStop{TurnKey: "t_1", StopReason: event.StopEndTurn})
				b.samples(at(0), at(2900))
			},
			check: func(t *testing.T, d Derived) {
				running(0)(t, d)
				if tu, ok := turnByKey(d, "t_1"); !ok || tu.RunUntil != at(70) {
					t.Errorf("turn %+v, want it to run until 70 s", tu)
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

// TestFutureRecord checks that a record dated ahead of the device's clock,
// as a bridge prompt with a skewed transcript timestamp is, doesn't move
// "now": FinalBefore stays where the samples put it, and the records that
// come after it are still output.
func TestFutureRecord(t *testing.T) {
	r := newRules(testTable(t))
	bridgePrompt := func(b *tl, tm int64) event.Event {
		return b.harness(tm, event.ViaBridge, "s_b", "p_1", &event.PromptSubmit{TurnKey: "t_b", LenChars: 30,
			TypedLenEst: 30, OriginDeclared: event.DeclaredNone})
	}
	t.Run("a bridge record an hour ahead leaves FinalBefore behind the samples, and the records after it are output", func(t *testing.T) {
		b := newTL(false)
		terminalSession(b)
		b.mouse(every(at(1), at(5996), 5000)...)
		b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 30)
		b.agentStops(at(61), "s_1", "t_1")
		future := bridgePrompt(b, at(1500+3600))
		b.samples(at(0), at(6000))
		recs := b.records()
		// It arrives at 1,500 s, with the records up to then; the rest
		// come after it.
		var first, rest []event.Event
		for _, e := range recs {
			if e.T <= at(1500) || e.EventID == future.EventID {
				first = append(first, e)
			} else {
				rest = append(rest, e)
			}
		}
		s1, o1 := Step(NewState(testTable(t), Device{}), first)
		if want := floorSlot(at(1500) + r.pendingWindow - r.settle); o1.FinalBefore != want {
			t.Errorf("FinalBefore %d s, want %d s: at the last sample: now is PENDING_WINDOW_S past it, and settle is PENDING_WINDOW_S",
				(o1.FinalBefore-base)/1000, (want-base)/1000)
		}
		_, o2 := Step(s1, rest)
		var all Derived
		all.Append(o1.Final)
		all.Append(o2.Final)
		all.Append(o2.Pending)
		if got, want := marshal(t, all), marshal(t, scoreAll(t, Device{}, recs)); !bytes.Equal(got, want) {
			t.Errorf("two Steps differ from one %s", firstDiff(got, want))
		}
	})
	t.Run("a record a day ahead, with the screen left locked, derives no slot past PENDING_WINDOW_S after the samples", func(t *testing.T) {
		b := newTL(false)
		terminalSession(b)
		b.mouse(every(at(1), at(296), 5000)...)
		b.samples(at(0), at(299))
		b.power(at(300), event.PowerLock)
		b.samples(at(300), at(600))
		bridgePrompt(b, at(600+86_400))
		d := b.run(t, Device{})
		if n, most := len(d.Slots), int((600+r.pendingWindow/1000)/15)+1; n > most {
			t.Errorf("%d slots, want at most %d", n, most)
		}
	})
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
