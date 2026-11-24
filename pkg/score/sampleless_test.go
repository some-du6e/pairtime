// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"bytes"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
)

// TestSamplelessPower checks decision D-sampleless-power (#298). The agent
// samples at every power event (DESIGN 3.3), so an os.power with no sample
// shows its idle read failing. When such a record is the sampler's next
// after a sample, or after a resume or unlock with no sample, whose reach
// hasn't ended, the slots from that record to it are unknown once the
// newest record is rules.late past it, and the earlier record holds the
// clock until then, so a sample taken with it and sealed a batch later
// still decides them. A sample decides a slot only within BUCKET_TTL_S
// after the slot's end, a slot no rule decides is unknown once the newest
// record is rules.late past that, and FinalBefore never passes a slot that
// nothing decides yet (SM 2.1).
//
// Each case is the same evening: input until 595 s, a hooked turn from
// 600 s to 1,790 s, and a sample on every slot boundary until C, the last
// one, after which the desktop's idle source goes.
func TestSamplelessPower(t *testing.T) {
	r := newRules(testTable(t))
	const c, suspend, resume = 1800, 1800 + 3600, 1800 + 3600 + 8*3600
	// back is when the samples come again in the backstop's case.
	const back = resume + 3*3600 + 600
	// hooks adds a hooked turn of session sess, its prompt at from and a
	// tool call every two minutes after it until to.
	hooks := func(b *tl, sess, turn string, from, to int64) {
		b.prompt(at(from), event.ViaHook, sess, "p_1", turn, 40)
		for s := from + 120; s <= to; s += 120 {
			b.harness(at(s), event.ViaHook, sess, "p_1", &event.AgentTool{ToolClass: event.ToolBash,
				TStart: at(s - 5), TEnd: at(s), VisibleOutputLines: 2})
		}
	}
	evening := func(b *tl) {
		terminalSession(b)
		b.mouse(every(at(1), at(595), 5000)...)
		hooks(b, "s_1", "t_1", 600, 1790)
		b.samples(at(0), at(c))
	}
	// morning is a session started after the resume, with a hooked turn
	// whose tool calls run past BUCKET_TTL_S after it.
	morning := func(b *tl, from int64) {
		b.start(at(from+30), "s_2", "p_1", event.EntrypointCLI, event.ParentTerminalApp, true)
		hooks(b, "s_2", "t_2", from+60, from+3*3600)
	}
	// slots calls fn with every slot of d from from up to to, and fails
	// when one is missing.
	slots := func(t *testing.T, d Derived, from, to int64, fn func(Slot)) {
		t.Helper()
		n := 0
		for _, s := range d.Slots {
			if s.Start >= from && s.Start < to {
				n++
				fn(s)
			}
		}
		if want := int((to - from) / event.SlotMS); n != want {
			t.Fatalf("%d slots derived from %d s to %d s, want %d: every one of them", n, (from-base)/1000, (to-base)/1000, want)
		}
	}
	// heldAt checks that while the newest record is in [from, to),
	// FinalBefore is fb, and that some Step's newest record falls there.
	heldAt := func(t *testing.T, batches [][]event.Event, outs []Output, from, to, fb int64) {
		t.Helper()
		high, seen := int64(0), false
		for i, b := range batches {
			for _, e := range b {
				high = max(high, e.T)
			}
			if high >= from && high < to {
				seen = true
				if outs[i].FinalBefore != fb {
					t.Errorf("with the newest record at %d s, FinalBefore %d s, want %d s", (high-base)/1000,
						(outs[i].FinalBefore-base)/1000, (fb-base)/1000)
				}
			}
		}
		if !seen {
			t.Fatalf("no Step's newest record falls from %d s to %d s", (from-base)/1000, (to-base)/1000)
		}
	}
	cases := []struct {
		name    string
		build   func(b *tl)
		batches func(t *testing.T, recs []event.Event) [][]event.Event
		check   func(t *testing.T, batches [][]event.Event, outs []Output, one Derived)
	}{
		{
			name: "a suspend with no sample an hour after the last sample, a resume with none and a hooked morning past BUCKET_TTL_S leave no slot underived: unknown to the suspend, asleep to the resume and never-sampling after it, with the morning's agent time",
			build: func(b *tl) {
				evening(b)
				b.power(at(suspend), event.PowerSuspend)
				b.power(at(resume), event.PowerResume)
				morning(b, resume)
			},
			check: func(t *testing.T, batches [][]event.Event, outs []Output, one Derived) {
				// The suspend is alone in its minute's batch, and the sample
				// before it holds the clock while it is new.
				heldAt(t, batches, outs, at(suspend), at(suspend)+r.late, at(c))
				last := outs[len(outs)-1]
				high := batches[len(batches)-1][len(batches[len(batches)-1])-1].T
				if want := floorSlot(high - r.settle); last.FinalBefore != want || last.Undecided != 0 {
					t.Fatalf("FinalBefore %d s, undecided at %d, want %d s, PENDING_WINDOW_S behind the newest record, with no slot undecided",
						(last.FinalBefore-base)/1000, last.Undecided, (want-base)/1000)
				}
				slots(t, one, at(c), at(suspend), func(s Slot) {
					if s.Present != event.PresentUnknown || s.Asleep {
						t.Fatalf("the slot at %d s is %q, asleep %v; want unknown, as the sampler ran there and took no sample", (s.Start-base)/1000, s.Present, s.Asleep)
					}
				})
				slots(t, one, at(suspend), at(resume), func(s Slot) {
					if !s.Asleep {
						t.Fatalf("the slot at %d s isn't asleep; want the suspend's slots asleep up to its resume", (s.Start-base)/1000)
					}
				})
				agent := map[int64]int64{}
				for _, cr := range one.Credits {
					agent[floorMinute(cr.Slot)] += cr.AgentS
				}
				slots(t, one, at(resume), last.FinalBefore, func(s Slot) {
					if s.Present != event.PresentUnknown || s.Asleep {
						t.Fatalf("the slot at %d s is %q, asleep %v; want a never-sampling device's, unknown", (s.Start-base)/1000, s.Present, s.Asleep)
					}
				})
				for m := floorMinute(at(resume+60)) + MinuteMS; m+MinuteMS <= last.FinalBefore; m += MinuteMS {
					if agent[m] == 0 {
						t.Fatalf("the minute at %d s has no agent time; want the morning's turn running in every final minute of it", (m-base)/1000)
					}
				}
			},
		},
		{
			name: "a sample taken with the suspend and sealed a batch later still decides the slots before it: the sample before it holds the clock until it comes",
			build: func(b *tl) {
				evening(b)
				b.power(at(suspend), event.PowerSuspend)
				b.sample(at(suspend))
				b.power(at(resume), event.PowerResume)
				morning(b, resume)
			},
			batches: func(t *testing.T, recs []event.Event) [][]event.Event {
				var out [][]event.Event
				var late []event.Event
				for _, b := range minuteBatches(recs) {
					var kept []event.Event
					for _, e := range b {
						if _, ok := e.Data.(*event.OSPresence); ok && e.T == at(suspend) {
							late = append(late, e)
						} else {
							kept = append(kept, e)
						}
					}
					if len(late) > 0 && len(kept) > 0 && kept[0].T > at(suspend) {
						kept, late = append(late, kept...), nil
					}
					if len(kept) > 0 {
						out = append(out, kept)
					}
				}
				if len(late) > 0 {
					t.Fatal("no batch after the suspend's to seal its sample with")
				}
				return out
			},
			check: func(t *testing.T, batches [][]event.Event, outs []Output, one Derived) {
				heldAt(t, batches, outs, at(suspend), at(suspend)+r.late, at(c))
				// Two samples an hour apart: SM 2.1's gap, a hard break.
				slots(t, one, at(c), at(suspend), func(s Slot) {
					if !s.Asleep || s.Present == event.PresentUnknown {
						t.Fatalf("the slot at %d s is %q, asleep %v; want it asleep, as the sample at the suspend decides it", (s.Start-base)/1000, s.Present, s.Asleep)
					}
				})
			},
		},
		{
			name: "a resume and a lock under BUCKET_TTL_S later, neither with a sample, leave the slots between unknown: the resume holds the clock until the newest record is rules.late past the lock",
			build: func(b *tl) {
				evening(b)
				b.power(at(suspend), event.PowerSuspend)
				b.power(at(resume), event.PowerResume)
				b.power(at(resume+1800), event.PowerLock)
				morning(b, resume)
			},
			check: func(t *testing.T, batches [][]event.Event, outs []Output, one Derived) {
				heldAt(t, batches, outs, at(resume+1800), at(resume+1800)+r.late, at(resume))
				last := outs[len(outs)-1]
				high := batches[len(batches)-1][len(batches[len(batches)-1])-1].T
				if want := floorSlot(high - r.settle); last.FinalBefore != want || last.Undecided != 0 {
					t.Fatalf("FinalBefore %d s, undecided at %d, want %d s, PENDING_WINDOW_S behind the newest record, with no slot undecided",
						(last.FinalBefore-base)/1000, last.Undecided, (want-base)/1000)
				}
				slots(t, one, at(resume), at(resume+1800), func(s Slot) {
					if s.Present != event.PresentUnknown || s.Asleep || s.Locked {
						t.Fatalf("the slot at %d s is %q, asleep %v, locked %v; want unknown", (s.Start-base)/1000, s.Present, s.Asleep, s.Locked)
					}
				})
			},
		},
		{
			name: "a lock with no sample after the last sample, and the sampler sampling again at the unlock, leaves the slots to the lock unknown and the locked ones locked but not asleep, since the sampler ran and opened no gap",
			build: func(b *tl) {
				evening(b)
				b.power(at(2400), event.PowerLock)
				b.power(at(6000), event.PowerUnlock)
				b.mouse(at(6005))
				b.samples(at(6000), at(6600))
			},
			check: func(t *testing.T, _ [][]event.Event, _ []Output, one Derived) {
				slots(t, one, at(c), at(2400), func(s Slot) {
					if s.Present != event.PresentUnknown || s.Asleep {
						t.Fatalf("the slot at %d s is %q, asleep %v; want unknown", (s.Start-base)/1000, s.Present, s.Asleep)
					}
				})
				slots(t, one, at(2400), at(6000), func(s Slot) {
					if !s.Locked || s.Asleep {
						t.Fatalf("the slot at %d s is locked %v, asleep %v; want it locked and not asleep", (s.Start-base)/1000, s.Locked, s.Asleep)
					}
				})
			},
		},
		{
			name: "a resume with no sample, a log reader's turn for three hours with nothing stamped and then samples again: the slots no sample came within BUCKET_TTL_S of are unknown, the resume holds the clock while the samples that end its gap are new, and a Step a batch gives what one Step gives, the late samples included",
			build: func(b *tl) {
				evening(b)
				b.power(at(suspend), event.PowerSuspend)
				b.power(at(resume), event.PowerResume)
				b.prompt(at(resume+60), event.ViaLog, "s_3", "p_1", "t_3", 40)
				for s := int64(resume + 600); s <= resume+3*3600; s += 600 {
					b.harness(at(s), event.ViaLog, "s_3", "p_1", &event.AgentOutput{TurnKey: "t_3", Words: 30})
				}
				b.samples(at(back), at(back+1800))
			},
			check: func(t *testing.T, batches [][]event.Event, outs []Output, one Derived) {
				// The first sample decides the slots ending at most
				// BUCKET_TTL_S before it.
				const decided = back - 7200 - 15
				slots(t, one, at(resume), at(decided), func(s Slot) {
					if s.Present != event.PresentUnknown || s.Asleep {
						t.Fatalf("the slot at %d s is %q, asleep %v; want unknown, as no sample came within BUCKET_TTL_S of it", (s.Start-base)/1000, s.Present, s.Asleep)
					}
				})
				slots(t, one, at(decided), at(back), func(s Slot) {
					if s.Present == event.PresentUnknown || s.Asleep {
						t.Fatalf("the slot at %d s is %q, asleep %v; want it decided by the sample after it", (s.Start-base)/1000, s.Present, s.Asleep)
					}
				})
				// While the first samples are new, the slots between the
				// ones no sample decides and the ones the samples do wait,
				// and the gap the samples end keeps the clock at the resume,
				// so FinalBefore never waits at one of them.
				heldAt(t, batches, outs, at(back), at(back)+r.late, at(resume))
				for i, o := range outs {
					if o.Undecided != 0 {
						t.Fatalf("Step %d waits at an undecided slot at %d s; want the resume's gap to hold the clock first", i, (o.Undecided-base)/1000)
					}
				}
				last := outs[len(outs)-1]
				high := batches[len(batches)-1][len(batches[len(batches)-1])-1].T
				if want := floorSlot(high - r.settle); last.FinalBefore != want {
					t.Fatalf("FinalBefore at the end %d s, want %d s, PENDING_WINDOW_S behind the newest record",
						(last.FinalBefore-base)/1000, (want-base)/1000)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newTL(false)
			tc.build(b)
			recs := b.records()
			batches := minuteBatches(recs)
			if tc.batches != nil {
				batches = tc.batches(t, recs)
			}
			finals, outs := stepBatches(t, batches)
			one := scoreAll(t, Device{}, recs)
			var all Derived
			all.Append(finals)
			all.Append(outs[len(outs)-1].Pending)
			if got, want := marshal(t, all), marshal(t, one); !bytes.Equal(got, want) {
				t.Errorf("a Step a batch differs from one Step %s", firstDiff(got, want))
			}
			checkDerived(t, one)
			tc.check(t, batches, outs, one)
		})
	}
}

// TestUndecidedHold checks (c) of D-sampleless-power, and its bound, with a
// clock no input reaches: State.now never passes a slot that nothing
// decides, so the case gives derive a clock past one, as a disagreement
// between the clock and the slot list would. FinalBefore waits at the first
// slot nothing decides (Output.Undecided). A slot no sample came within
// BUCKET_TTL_S after is unknown once the newest record is rules.late past
// that, so the wait moves on to the first slot still undecided. The newest
// record is a log reader's output, which ends no reach.
func TestUndecidedHold(t *testing.T) {
	r := newRules(testTable(t))
	const c = 1800 // the last sample
	cases := []struct {
		name string
		// high is the newest record, in seconds after base, and the clock.
		high int64
		// wait is where FinalBefore waits, in seconds after base.
		wait int64
	}{
		{"with the clock an hour past the last sample, FinalBefore waits at the slot of the last sample, which no sample after it decides",
			c + 3600, c},
		{"with the newest record more than BUCKET_TTL_S and rules.late past the first slots after the last sample, they are unknown, and FinalBefore waits at the first slot still undecided",
			c + 3*3600, c + 3*3600 - 300 - 7200 - 15},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newTL(false)
			terminalSession(b)
			b.mouse(every(at(1), at(595), 5000)...)
			b.samples(at(0), at(c))
			b.harness(at(tc.high), event.ViaLog, "s_1", "p_1", &event.AgentOutput{TurnKey: "t_1", Words: 30})
			recs := b.records()
			now := at(tc.high)
			d, lookups, relays, undecided := derive(r, Device{}, recs, now, sampledReaches(r, recs, now))
			fb, waits := finalBefore(r, now, 0, undecided, lookups, relays)
			if fb != at(tc.wait) || waits != at(tc.wait) {
				t.Fatalf("FinalBefore %d s, waiting at %d s; want both at %d s", (fb-base)/1000, (waits-base)/1000, tc.wait)
			}
			n := 0
			for _, s := range d.Slots {
				switch {
				case s.Start >= at(tc.wait):
					t.Fatalf("a slot derived at %d s, where FinalBefore waits or after", (s.Start-base)/1000)
				case s.Start >= at(c):
					n++
					if s.Present != event.PresentUnknown {
						t.Fatalf("the slot at %d s is %q; want unknown, as no sample came within BUCKET_TTL_S of it", (s.Start-base)/1000, s.Present)
					}
				}
			}
			if want := int((at(tc.wait) - at(c)) / event.SlotMS); n != want {
				t.Fatalf("%d slots derived from the last sample to the wait, want %d", n, want)
			}
		})
	}
}
