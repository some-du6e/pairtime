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
// newest record the device's own clock dated is rules.late past it, and the
// earlier record holds the clock until then, so a sample taken with it and
// sealed a batch later still decides them. A sample decides a slot only
// within BUCKET_TTL_S after the slot's end, a slot no rule decides is
// unknown once the newest record the device's own clock dated is rules.late
// past that, and FinalBefore never passes a slot that nothing decides yet
// (SM 2.1).
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
	// bridge adds a bridge prompt at tm, whose t comes from a transcript, not
	// the device's clock.
	bridge := func(b *tl, tm int64) {
		b.harness(tm, event.ViaBridge, "s_b", "p_1", &event.PromptSubmit{TurnKey: "t_b", LenChars: 30, TypedLenEst: 30,
			OriginDeclared: event.DeclaredNone})
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
		// waits says the case's records come in #312's order, which reaches
		// (c)'s hold: a bridge record dated ahead releases a gap hold, which
		// still reads every record, before the backstop, on the device's
		// clock, is due. The shared check that no Step waits skips it.
		waits bool
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
				// Once those slots are decided, the lock keeps the clock, and
				// FinalBefore moves on to it, long before BUCKET_TTL_S would
				// have them count as unknown anyway.
				heldAt(t, batches, outs, at(resume+1800)+r.settle, at(resume+7200), at(resume+1800))
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
			name: "the evening's turn running into the stretch to a sample-less suspend codes its seconds there as SM 5.2 codes a live unknown slot's, never X.absent",
			build: func(b *tl) {
				evening(b)
				b.power(at(suspend), event.PowerSuspend)
				b.power(at(resume), event.PowerResume)
				morning(b, resume)
			},
			check: func(t *testing.T, _ [][]event.Event, _ []Output, one Derived) {
				agent := int64(0)
				for _, cr := range one.Credits {
					if cr.Slot < at(c) || cr.Slot >= at(suspend) {
						continue
					}
					for why, n := range cr.Uncredited {
						switch why {
						case xAgent, xAuto:
							agent += n
						case xNoAnch:
						default:
							t.Fatalf("the slot at %d s codes %d s %s; want only SM 5.2's codes for a live slot whose presence is unknown",
								(cr.Slot-base)/1000, n, why)
						}
					}
				}
				if agent == 0 {
					t.Fatal("no second of the stretch codes the evening's running turn; want X.agent_only or X.automated while it runs")
				}
			},
		},
		{
			name: "a resume with no suspend before it, after a sample-less lock and unlock, dates its sleep from the unlock: the slots to the lock stay unknown and the locked ones locked, as they went final before it came",
			build: func(b *tl) {
				evening(b)
				b.power(at(2400), event.PowerLock)
				b.power(at(3000), event.PowerUnlock)
				// The hooks run on until the resume is rules.late old, so the
				// unlock's wait for the sample taken with the resume ends.
				hooks(b, "s_1", "t_4", 1900, 4800)
				b.power(at(4200), event.PowerResume)
			},
			check: func(t *testing.T, batches [][]event.Event, outs []Output, one Derived) {
				// The unlock keeps the clock, so the slots to it are final
				// before the resume comes.
				heldAt(t, batches, outs, at(3900), at(4200), at(3000))
				slots(t, one, at(c), at(2400), func(s Slot) {
					if s.Present != event.PresentUnknown || s.Asleep {
						t.Fatalf("the slot at %d s is %q, asleep %v; want unknown", (s.Start-base)/1000, s.Present, s.Asleep)
					}
				})
				slots(t, one, at(2400), at(3000), func(s Slot) {
					if !s.Locked || s.Asleep {
						t.Fatalf("the slot at %d s is locked %v, asleep %v; want it locked and not asleep", (s.Start-base)/1000, s.Locked, s.Asleep)
					}
				})
				slots(t, one, at(3000), at(4200), func(s Slot) {
					if !s.Asleep {
						t.Fatalf("the slot at %d s isn't asleep; want the machine asleep from the unlock to the resume", (s.Start-base)/1000)
					}
				})
			},
		},
		{
			name: "a bridge prompt dated an hour ahead doesn't end the wait for the sample taken with a sample-less lock and sealed a batch later: that sample still decides the slot before it",
			build: func(b *tl) {
				terminalSession(b)
				b.mouse(every(at(1), at(1495), 5000)...)
				b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 30)
				b.agentStops(at(61), "s_1", "t_1")
				b.samples(at(0), at(1485))
				bridge(b, at(1500+3600))
				b.power(at(1507), event.PowerLock)
				b.sample(at(1507))
				b.samples(at(1515), at(1800))
			},
			batches: func(t *testing.T, recs []event.Event) [][]event.Event {
				// The bridge prompt comes with the records to 1,485 s, the lock
				// alone, and its sample with the rest.
				var first, lock, rest []event.Event
				for _, e := range recs {
					_, power := e.Data.(*event.OSPower)
					switch {
					case e.T <= at(1485) || e.Via == event.ViaBridge:
						first = append(first, e)
					case power:
						lock = append(lock, e)
					default:
						rest = append(rest, e)
					}
				}
				if len(first) == 0 || len(lock) != 1 || len(rest) == 0 {
					t.Fatal("the records don't split into the first batch, the lock and the rest")
				}
				return [][]event.Event{first, lock, rest}
			},
			check: func(t *testing.T, _ [][]event.Event, outs []Output, _ Derived) {
				if fb := outs[1].FinalBefore; fb > at(1485) {
					t.Fatalf("with the lock's sample on its way, FinalBefore %d s; want at most 1485 s, the last sample, as a record dated ahead of the device's clock ends no wait",
						(fb-base)/1000)
				}
			},
		},
		{
			name: "a bridge prompt dated hours ahead releases none of the slots after a sample-less resume: they wait for a record the device's own clock dated",
			build: func(b *tl) {
				evening(b)
				b.power(at(suspend), event.PowerSuspend)
				b.power(at(resume), event.PowerResume)
				bridge(b, at(resume+5*3600))
			},
			check: func(t *testing.T, _ [][]event.Event, outs []Output, one Derived) {
				for _, s := range one.Slots {
					if s.Start >= at(resume) {
						t.Fatalf("a slot derived at %d s, after the resume; want none, as no record the device's clock dated is past it", (s.Start-base)/1000)
					}
				}
				if fb := outs[len(outs)-1].FinalBefore; fb > at(resume) {
					t.Fatalf("FinalBefore %d s; want it at the resume at most", (fb-base)/1000)
				}
			},
		},
		{
			name:  "a bridge prompt dated ahead releases a sample-less unlock's gap hold before the backstop is due, so FinalBefore waits at the first slot nothing decides until a record the device dated is BUCKET_TTL_S and rules.late past it, and a Step a batch still gives what one Step gives",
			waits: true,
			build: func(b *tl) {
				terminalSession(b)
				b.mouse(every(at(1), at(595), 5000)...)
				b.samples(at(0), at(c))
				b.power(at(c+600), event.PowerUnlock)
				b.samples(at(13200), at(13800))
				bridge(b, at(13800))
			},
			batches: func(t *testing.T, recs []event.Event) [][]event.Event {
				// The bridge prompt is sealed with the samples to 13,245 s.
				var head, first, rest []event.Event
				for _, e := range recs {
					switch {
					case e.T < at(13200):
						head = append(head, e)
					case e.T <= at(13245) || e.Via == event.ViaBridge:
						first = append(first, e)
					default:
						rest = append(rest, e)
					}
				}
				if len(head) == 0 || len(first) != 5 || len(rest) == 0 {
					t.Fatal("the records don't split into the evening, the first samples with the bridge prompt, and the rest")
				}
				return append([][]event.Event{head, first}, minuteBatches(rest)...)
			},
			check: func(t *testing.T, _ [][]event.Event, outs []Output, _ Derived) {
				// With samples to 13,245 s, the backstop has made the slots
				// ending before 5,745 s unknown, and the next one waits.
				if o := outs[1]; o.FinalBefore != at(5730) || o.Undecided != at(5730) {
					t.Fatalf("with the bridge prompt in, FinalBefore %d s, waiting at %d s; want both at 5730 s",
						(o.FinalBefore-base)/1000, (o.Undecided-base)/1000)
				}
				for i, o := range outs {
					if o.Undecided != 0 && o.FinalBefore != o.Undecided {
						t.Fatalf("Step %d waits at %d s with FinalBefore at %d s; want FinalBefore at the slot it waits at",
							i, (o.Undecided-base)/1000, (o.FinalBefore-base)/1000)
					}
				}
				if last := outs[len(outs)-1]; last.Undecided != 0 || last.FinalBefore != at(13800-900) {
					t.Fatalf("at the end FinalBefore %d s, waiting at %d; want 12900 s, PENDING_WINDOW_S behind the newest sample, with no wait",
						(last.FinalBefore-base)/1000, last.Undecided)
				}
			},
		},
		{
			name: "a sample after a blind stretch dates no input in it more than SLEEP_GAP_S before the sample: the stretch's slots, final as unknown, keep no input when a sample long after says there was some",
			build: func(b *tl) {
				evening(b)
				b.power(at(2400), event.PowerLock)
				// A record after the lock moves the clock past the stretch, so
				// its slots are final before the next sample comes.
				b.harness(at(3400), event.ViaHook, "s_1", "p_1", &event.AgentTool{ToolClass: event.ToolBash,
					TStart: at(3395), TEnd: at(3400), VisibleOutputLines: 2})
				b.mouse(at(2000))
				b.power(at(6000), event.PowerUnlock)
				b.samples(at(6000), at(6600))
			},
			check: func(t *testing.T, batches [][]event.Event, outs []Output, one Derived) {
				// The slots to the lock are decided once the newest record the
				// device's own clock dated is rules.late past it, so
				// FinalBefore moves on to the lock, which keeps the clock,
				// rather than waiting at the sample.
				heldAt(t, batches, outs, at(3400), at(3460), at(2400))
				slots(t, one, at(c), at(2400), func(s Slot) {
					if s.Present != event.PresentUnknown || s.InputAny {
						t.Fatalf("the slot at %d s is %q, input %v; want unknown with no input, which a sample over an hour later can't date",
							(s.Start-base)/1000, s.Present, s.InputAny)
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
			// Outside #312's order (waits), these records reach no wait at
			// an undecided slot: (a), the extension and the backstop decide
			// every slot before the clock passes it.
			for i, o := range outs {
				if !tc.waits && o.Undecided != 0 {
					t.Errorf("Step %d holds FinalBefore at an undecided slot at %d s; want every slot decided before the clock passes it",
						i, (o.Undecided-base)/1000)
				}
			}
			tc.check(t, batches, outs, one)
		})
	}
}

// TestUndecidedHold checks (c) of D-sampleless-power, and its bound, with a
// clock past a slot that nothing decides, which State.now gives only in
// #312's order of records (TestSamplelessPower's waits case), so each case
// gives derive such a clock, as a disagreement between the clock and the
// slot list would. FinalBefore waits at the first
// slot nothing decides (Output.Undecided). A slot no sample came within
// BUCKET_TTL_S after is unknown once the newest record the device's own
// clock dated is rules.late past that, so the wait moves on to the first
// slot still undecided.
func TestUndecidedHold(t *testing.T) {
	r := newRules(testTable(t))
	const c = 1800 // the last sample of the evening
	cases := []struct {
		name  string
		build func(b *tl)
		// now is the clock the case gives derive, and wait where
		// FinalBefore waits, both in seconds after base.
		now, wait int64
	}{
		{"with the clock an hour past the last sample and a hook there, FinalBefore waits at the slot of the last sample, which no sample after it decides",
			func(b *tl) {
				b.harness(at(c+3600), event.ViaHook, "s_1", "p_1", &event.AgentTool{ToolClass: event.ToolBash,
					TStart: at(c + 3595), TEnd: at(c + 3600), VisibleOutputLines: 2})
			}, c + 3600, c},
		{"with a sample-less unlock after the last sample and a sample three hours later, the slots no sample came within BUCKET_TTL_S of are unknown, and FinalBefore waits at the first slot still undecided",
			func(b *tl) {
				b.power(at(c+600), event.PowerUnlock)
				b.sample(at(c + 600 + 3*3600))
			}, c + 600 + 3*3600, c + 600 + 3*3600 - 300 - 7200 - 15},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newTL(false)
			terminalSession(b)
			b.mouse(every(at(1), at(595), 5000)...)
			b.samples(at(0), at(c))
			tc.build(b)
			recs := b.records()
			now := at(tc.now)
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
						t.Fatalf("the slot at %d s is %q; want unknown", (s.Start-base)/1000, s.Present)
					}
				}
			}
			if want := int((at(tc.wait) - at(c)) / event.SlotMS); n != want {
				t.Fatalf("%d slots derived from the last sample to the wait, want %d", n, want)
			}
		})
	}
}
