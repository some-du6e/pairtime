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
// after a sample whose reach hasn't ended, the slots from the sample to it
// are unknown once the newest record is rules.late past it, and the sample
// holds the clock until then, so a sample taken with it and sealed a batch
// later still decides them. And FinalBefore never passes a slot that
// nothing decides (SM 2.1).
//
// Each case is the same evening: input until 595 s, a hooked turn from
// 600 s to 1,790 s, and a sample on every slot boundary until C, the last
// one, after which the desktop's idle source goes.
func TestSamplelessPower(t *testing.T) {
	r := newRules(testTable(t))
	const c, suspend, resume = 1800, 1800 + 3600, 1800 + 3600 + 8*3600
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
			name: "a resume and a lock under BUCKET_TTL_S later, neither with a sample, leave the slots between undecided, so FinalBefore waits at the resume however far the hooks run, and Output.Undecided says where",
			build: func(b *tl) {
				evening(b)
				b.power(at(suspend), event.PowerSuspend)
				b.power(at(resume), event.PowerResume)
				b.power(at(resume+1800), event.PowerLock)
				morning(b, resume)
			},
			check: func(t *testing.T, _ [][]event.Event, outs []Output, one Derived) {
				last := outs[len(outs)-1]
				if last.FinalBefore != at(resume) || last.Undecided != at(resume) {
					t.Fatalf("FinalBefore %d s, undecided at %d s, want both at the resume, %d s", (last.FinalBefore-base)/1000,
						(last.Undecided-base)/1000, resume)
				}
				slots(t, one, at(c), at(resume), func(Slot) {})
				if n := len(one.Slots); n > 0 && one.Slots[n-1].Start >= at(resume) {
					t.Fatalf("a slot derived at %d s, want none from the resume on", (one.Slots[n-1].Start-base)/1000)
				}
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
