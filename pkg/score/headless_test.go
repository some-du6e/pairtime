// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/some-du6e/pairtime/internal/timelines"
	"github.com/some-du6e/pairtime/pkg/event"
)

// A device whose agent can't sample (SM 2.1; WP-HL1,
// docs/errata/2026-11-23-headless-scoring.md): a server with no desktop, or
// a desktop that gives no idle time, as KDE and wlroots don't. Its agent
// records no os.presence and no os.power, and its focus component writes
// one os.focus each time the agent starts, saying focus is unknown. Before
// WP-HL1 that record was the scorer's clock, so nothing was final
// PENDING_WINDOW_S after an agent's start, and after a silence longer than
// BUCKET_TTL_S a Step derived none of the minutes before it. A device whose
// sampler stops while its agent runs on is one too, from its last sample
// on, once a record its agent stamps is dated BUCKET_TTL_S after it.

// unknownFocus adds the os.focus an agent writes as it starts when it can't
// tell which window is in front (SM 1.2).
func (b *tl) unknownFocus(t int64) {
	b.os(t, &event.OSFocus{AppClass: event.AppUnknown})
}

// serverTurns starts session sess of p_1 from seconds after base, as T3
// Code's server starts Claude Code, and adds a turn every five minutes
// until to: its prompt, a tool call a minute in, its output and its stop
// three minutes in. The session has no TTY, so SM 2.2 rule 4 makes every
// prompt headless, and each turn runs 180 s.
func serverTurns(b *tl, sess string, from, to int64) {
	appStart(b, from, sess, "p_1")
	for s := from + 10; s+180 <= to; s += 300 {
		turn := fmt.Sprintf("t_%s_%d", sess, s)
		b.prompt(at(s), event.ViaHook, sess, "p_1", turn, 40)
		b.harness(at(s+60), event.ViaHook, sess, "p_1", &event.AgentTool{ToolClass: event.ToolBash, TStart: at(s + 55),
			TEnd: at(s + 60), VisibleOutputLines: 2})
		says(b, at(s+175), sess, "p_1", turn, 30)
		stops(b, at(s+180), sess, "p_1", turn)
	}
}

// stepByMinute steps one State a minute of recs at a time, as the agent
// does, and returns what became final over all the Steps and the last
// Step's Output.
func stepByMinute(t *testing.T, recs []event.Event) (Derived, Output) {
	t.Helper()
	finals, outs := stepBatches(t, minuteBatches(recs))
	return finals, outs[len(outs)-1]
}

// minuteBatches splits recs, which are in record order, into a batch for
// each minute that holds one.
func minuteBatches(recs []event.Event) [][]event.Event {
	var out [][]event.Event
	for i := 0; i < len(recs); {
		j := i
		for j < len(recs) && floorMinute(recs[j].T) == floorMinute(recs[i].T) {
			j++
		}
		out = append(out, recs[i:j:j])
		i = j
	}
	return out
}

// stepBatches steps one State a batch at a time, as the agent does once
// the ledger seals each, and returns what became final over all the Steps
// and each Step's Output.
func stepBatches(t *testing.T, batches [][]event.Event) (Derived, []Output) {
	t.Helper()
	s := NewState(testTable(t), Device{})
	var finals Derived
	outs := make([]Output, len(batches))
	for i, b := range batches {
		s, outs[i] = Step(s, b)
		finals.Append(outs[i].Final)
	}
	return finals, outs
}

func TestHeadlessScoring(t *testing.T) {
	r := newRules(testTable(t))
	const hour = 3600
	// A full hour of serverTurns has twelve turns of 180 s.
	const hourAgent = 12 * 180
	agentIn := func(d Derived, from, to int64) int64 {
		var n int64
		for _, c := range d.Credits {
			if c.Slot >= from && c.Slot < to {
				n += c.AgentS
			}
		}
		return n
	}
	cases := []struct {
		name  string
		build func(b *tl)
		// check gets what became final a Step a minute, the last Step's
		// Output, the latest record's t and one Step's output.
		check func(t *testing.T, finals Derived, last Output, high int64, one Derived)
	}{
		{
			name: "an os.focus saying focus is unknown leaves a server one that never samples: whole minutes, every slot unknown, and SM 5.2's codes for a live slot whose presence is unknown",
			build: func(b *tl) {
				b.unknownFocus(at(0))
				serverTurns(b, "s_1", 0, hour)
			},
			check: func(t *testing.T, _ Derived, _ Output, _ int64, one Derived) {
				n := len(one.Slots)
				if n == 0 || one.Slots[0].Start != floorMinute(one.Slots[0].Start) || n%4 != 0 {
					t.Fatalf("%d slots, want whole minutes", n)
				}
				for _, s := range one.Slots {
					if s.Present != event.PresentUnknown {
						t.Fatalf("the slot at %d s is %q, want unknown", (s.Start-base)/1000, s.Present)
					}
				}
				got := sumIn(one, math.MinInt64, math.MaxInt64)
				if got[event.ReasonAbsent] != 0 || got[event.ReasonAutomated] == 0 {
					t.Errorf("uncredited %v, want X.automated while the headless turns run and no X.absent", got)
				}
			},
		},
		{
			name: "a server whose only OS record is an os.focus a run keeps finalizing minutes for hours after its agent starts, with every hour's agent time",
			build: func(b *tl) {
				b.unknownFocus(at(0))
				serverTurns(b, "s_1", 0, 3*hour)
				// The agent restarts after three hours.
				b.unknownFocus(at(3 * hour))
				serverTurns(b, "s_2", 3*hour, 6*hour)
			},
			check: func(t *testing.T, finals Derived, last Output, high int64, _ Derived) {
				if want := floorSlot(high - r.settle); last.FinalBefore != want {
					t.Errorf("FinalBefore %d s, want %d s: PENDING_WINDOW_S behind the latest record",
						(last.FinalBefore-base)/1000, (want-base)/1000)
				}
				for h := int64(0); h < 5; h++ {
					if got := agentIn(finals, at(h*hour), at((h+1)*hour)); got != hourAgent {
						t.Errorf("hour %d has %d s of agent time final, want %d", h, got, hourAgent)
					}
				}
			},
		},
		{
			name: "a silence longer than BUCKET_TTL_S on a server leaves no gap: the minutes still pending when it began are final after it, with their agent time",
			build: func(b *tl) {
				b.unknownFocus(at(0))
				serverTurns(b, "s_1", 0, hour)
				b.unknownFocus(at(4 * hour))
				serverTurns(b, "s_2", 4*hour, 5*hour)
			},
			check: func(t *testing.T, finals Derived, _ Output, _ int64, _ Derived) {
				if got := agentIn(finals, at(0), at(hour)); got != hourAgent {
					t.Errorf("the first hour has %d s of agent time final, want %d", got, hourAgent)
				}
			},
		},
		{
			name: "a desktop whose sampler stops while its agent runs on, as KDE on X11 and then on Wayland, is a never-sampling device from its last sample on, once a record its agent stamps is dated BUCKET_TTL_S after that sample",
			build: func(b *tl) {
				terminalSession(b)
				b.mouse(every(at(1), at(1796), 5000)...)
				b.samples(at(0), at(1800))
				// The agent starts again on Wayland, which gives no idle
				// time, a minute after the last sample. Its prompt at
				// 9,070 s is the first record it stamps more than
				// BUCKET_TTL_S after that sample.
				b.unknownFocus(at(1860))
				serverTurns(b, "s_2", 1860, 1860+4*hour)
			},
			check: func(t *testing.T, finals Derived, last Output, high int64, one Derived) {
				if want := floorSlot(high - r.settle); last.FinalBefore != want {
					t.Errorf("FinalBefore %d s, want %d s: PENDING_WINDOW_S behind the latest record",
						(last.FinalBefore-base)/1000, (want-base)/1000)
				}
				for h := int64(1); h < 4; h++ {
					if got := agentIn(finals, at(h*hour), at((h+1)*hour)); got != hourAgent {
						t.Errorf("hour %d has %d s of agent time final, want %d", h, got, hourAgent)
					}
				}
				for _, s := range one.Slots {
					if s.Start >= at(1800) && (s.Present != event.PresentUnknown || s.InputAny) {
						t.Fatalf("the slot at %d s is %q, input %v; want unknown with none, as after the sampler stopped",
							(s.Start-base)/1000, s.Present, s.InputAny)
					}
				}
				if got := sumIn(one, at(1800), at(4*hour)); got[event.ReasonAbsent] != 0 || got[event.ReasonAutomated] == 0 {
					t.Errorf("uncredited after the last sample %v, want X.automated while the turns run and no X.absent", got)
				}
			},
		},
		{
			name: "five hours with no sample and then samples: the minutes before the first sample stay a never-sampling device's, as they became final, and a Step a minute gives what one Step gives",
			build: func(b *tl) {
				b.unknownFocus(at(0))
				serverTurns(b, "s_1", 0, 5*hour)
				// A desktop session starts and its agent samples. Each sample
				// dates the last input at 4 h 1 min 40 s, between two
				// prompts, inside the minutes already final.
				b.mouse(at(4*hour + 100))
				b.focus(at(5*hour), event.AppTerminal, "p_1", "s_1")
				b.samples(at(5*hour), at(6*hour))
			},
			check: func(t *testing.T, finals Derived, _ Output, _ int64, one Derived) {
				for h := int64(0); h < 4; h++ {
					if got := agentIn(finals, at(h*hour), at((h+1)*hour)); got != hourAgent {
						t.Errorf("hour %d has %d s of agent time final, want %d", h, got, hourAgent)
					}
				}
				for _, s := range one.Slots {
					if s.Start < at(5*hour) && (s.Present != event.PresentUnknown || s.InputAny) {
						t.Fatalf("the slot at %d s is %q, input %v; want unknown with none, as before the first sample",
							(s.Start-base)/1000, s.Present, s.InputAny)
					}
				}
				if got := sumIn(one, at(0), at(5*hour))[event.ReasonAbsent]; got != 0 {
					t.Errorf("%d s of X.absent before the first sample, want SM 5.2's codes for a live slot whose presence is unknown", got)
				}
			},
		},
		{
			name: "on a device that samples, an os.focus after the last sample doesn't take FinalBefore past the samples",
			build: func(b *tl) {
				terminalSession(b)
				b.mouse(every(at(1), at(596), 5000)...)
				b.samples(at(0), at(600))
				b.focus(at(1200), event.AppEditor, "p_1")
				// A bridge prompt an hour ahead of the device's clock.
				b.prompt(at(4800), event.ViaBridge, "s_b", "p_1", "t_b", 30)
			},
			check: func(t *testing.T, _ Derived, last Output, _ int64, _ Derived) {
				if last.FinalBefore != at(600) {
					t.Errorf("FinalBefore %d s, want 600 s, the last sample's slot", (last.FinalBefore-base)/1000)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(false)
			c.build(b)
			recs := b.records()
			finals, last := stepByMinute(t, recs)
			one := scoreAll(t, Device{}, recs)
			var all Derived
			all.Append(finals)
			all.Append(last.Pending)
			if got, want := marshal(t, all), marshal(t, one); !bytes.Equal(got, want) {
				t.Errorf("a Step a minute differs from one Step %s", firstDiff(got, want))
			}
			checkDerived(t, one)
			c.check(t, finals, last, recs[len(recs)-1].T, one)
		})
	}
}

// TestCutWaits checks when a record that ends a sample's reach takes
// effect (SM 2.1): once the newest record is rules.late past it. Until
// then the reach holds, now stays bounded by it, and a record of the
// sampler's dated before the cut that arrives in that time keeps the
// reach, as a resume still on the presence queue does when a later-dated
// stop hook seals its batch. The other order waits the same way: a
// sampler's record more than BUCKET_TTL_S after the one before it holds
// the clock at that one until the newest record is rules.late past it, so
// a stamped record dated inside the gap and sealed after it still ends
// the reach before anything in the gap is final, and when none comes,
// FinalBefore moves on then. So nothing goes final early, and a Step a
// batch gives what one Step gives for any record up to rules.late late,
// in either order. A lock open across a never-sampling stretch stays on
// the slots before it when the sampler starts again.
func TestCutWaits(t *testing.T) {
	r := newRules(testTable(t))
	// turn is a person's input until 595 s and a hooked turn of s_1 from
	// 600 s with a tool call every two minutes until until.
	turn := func(b *tl, until int64) {
		terminalSession(b)
		b.mouse(every(at(1), at(595), 5000)...)
		b.prompt(at(600), event.ViaHook, "s_1", "p_1", "t_1", 40)
		for s := int64(660); s <= until; s += 120 {
			b.harness(at(s), event.ViaHook, "s_1", "p_1", &event.AgentTool{ToolClass: event.ToolBash,
				TStart: at(s - 5), TEnd: at(s), VisibleOutputLines: 2})
		}
	}
	// hooked is turn with a sample on every slot boundary until 1,800 s.
	// The first tool call more than BUCKET_TTL_S after that last sample is
	// the one at 9,060 s, and the newest record is rules.late past it from
	// 9,360 s on.
	hooked := func(b *tl, until int64) {
		turn(b, until)
		b.samples(at(0), at(1800))
	}
	const cut, takes = 9060, 9360
	// heldFrom checks that while the newest record is in [from, to),
	// FinalBefore stays at 1,800 s, the last sample before the gap or the
	// cut that holds it, and that some Step's newest record falls there.
	heldFrom := func(t *testing.T, batches [][]event.Event, outs []Output, from, to int64) {
		t.Helper()
		high, seen := int64(0), false
		for i, b := range batches {
			for _, e := range b {
				high = max(high, e.T)
			}
			if high >= from && high < to {
				seen = true
				if outs[i].FinalBefore != at(1800) {
					t.Errorf("with the newest record at %d s, FinalBefore %d s; want 1800 s, the last sample, while the clock is held",
						(high-base)/1000, (outs[i].FinalBefore-base)/1000)
				}
			}
		}
		if !seen {
			t.Fatal("no Step's newest record falls while the clock is held")
		}
	}
	// waits checks that while the cut at 9,060 s waits, FinalBefore stays on
	// the last sample before it.
	waits := func(t *testing.T, batches [][]event.Event, outs []Output) {
		t.Helper()
		heldFrom(t, batches, outs, at(cut), at(takes))
	}
	// sealedAfter returns a batch a minute, but with the records pick
	// selects sealed at the start of the batch after the first one that
	// holds a record after selects, as when they wait on a queue the other
	// passed.
	sealedAfter := func(pick, after func(event.Event) bool) func(*testing.T, []event.Event) [][]event.Event {
		return func(t *testing.T, recs []event.Event) [][]event.Event {
			var moved []event.Event
			var batches [][]event.Event
			for _, b := range minuteBatches(recs) {
				var kept []event.Event
				for _, e := range b {
					if pick(e) {
						moved = append(moved, e)
					} else {
						kept = append(kept, e)
					}
				}
				if len(kept) > 0 {
					batches = append(batches, kept)
				}
			}
			for i, b := range batches {
				if slices.ContainsFunc(b, after) {
					if len(moved) == 0 || i+1 == len(batches) {
						t.Fatal("nothing to seal late, or no batch after the one it waits for")
					}
					batches[i+1] = append(moved, batches[i+1]...)
					return batches
				}
			}
			t.Fatal("no batch holds the record the others wait for")
			return nil
		}
	}
	isStop := func(e event.Event) bool {
		_, ok := e.Data.(*event.AgentStop)
		return ok
	}
	isPower := func(op event.PowerOp) func(event.Event) bool {
		return func(e event.Event) bool {
			p, ok := e.Data.(*event.OSPower)
			return ok && p.Op == op
		}
	}
	// unknownFrom checks that one Step reads the slots in [from, to) as a
	// never-sampling device's: unknown, with no hard break.
	unknownFrom := func(t *testing.T, one Derived, from, to int64) {
		t.Helper()
		n := 0
		for _, s := range one.Slots {
			if s.Start >= from && s.Start < to {
				n++
				if s.Present != event.PresentUnknown || s.Asleep || s.Locked {
					t.Fatalf("the slot at %d s is %q, asleep %v, locked %v; want unknown with no hard break, as the cut made it",
						(s.Start-base)/1000, s.Present, s.Asleep, s.Locked)
				}
			}
		}
		if n == 0 {
			t.Fatal("no slot derived between the last sample and the next")
		}
	}
	const suspend = 1805
	const resume = suspend + 8*3600
	cases := []struct {
		name  string
		build func(b *tl)
		// batches splits the records into the batches the ledger seals; nil
		// is a batch a minute.
		batches func(t *testing.T, recs []event.Event) [][]event.Event
		check   func(t *testing.T, batches [][]event.Event, outs []Output, finals, one Derived)
	}{
		{
			name: "a resume sealed one batch after a later-dated stop hook keeps the suspend's reach, so the eight hours the machine slept stay asleep, as one Step reads them",
			build: func(b *tl) {
				hooked(b, 1790)
				b.power(at(suspend), event.PowerSuspend)
				b.power(at(resume), event.PowerResume)
				stops(b, at(resume+2), "s_1", "p_1", "t_1")
				b.samples(at(resume), at(resume+1800))
			},
			batches: func(t *testing.T, recs []event.Event) [][]event.Event {
				// The resume and the samples after it are still on the
				// presence queue when the stop hook seals the stop's minute,
				// so they come in the batch after.
				batches := minuteBatches(recs)
				for i, b := range batches {
					if i+1 == len(batches) || !slices.ContainsFunc(b, func(e event.Event) bool {
						_, ok := e.Data.(*event.AgentStop)
						return ok
					}) {
						continue
					}
					var kept, held []event.Event
					for _, e := range b {
						if samplerRecord(e) {
							held = append(held, e)
						} else {
							kept = append(kept, e)
						}
					}
					if len(held) == 0 || len(kept) == 0 {
						t.Fatal("the stop's minute holds no record of the sampler's to hold back, or only those")
					}
					batches[i], batches[i+1] = kept, append(held, batches[i+1]...)
					return batches
				}
				t.Fatal("no batch holds the stop with one after it")
				return nil
			},
			check: func(t *testing.T, _ [][]event.Event, _ []Output, finals, _ Derived) {
				from, to := floorSlot(at(suspend)), floorSlot(at(resume))
				n := 0
				for _, s := range finals.Slots {
					if s.Start < from || s.Start >= to {
						continue
					}
					n++
					if !s.Asleep {
						t.Fatalf("the slot at %d s is final and not asleep; want every slot the machine slept through asleep", (s.Start-base)/1000)
					}
				}
				if want := int((to - from) / event.SlotMS); n != want {
					t.Errorf("%d slots the machine slept through are final, want %d", n, want)
				}
			},
		},
		{
			name: "a record more than BUCKET_TTL_S after the last sample ends its reach only once the newest record is rules.late past it, and then FinalBefore follows the newest record",
			build: func(b *tl) {
				hooked(b, 9500)
			},
			check: func(t *testing.T, batches [][]event.Event, outs []Output, _, _ Derived) {
				waits(t, batches, outs)
				last := outs[len(outs)-1]
				if want := floorSlot(at(9420) - r.settle); last.FinalBefore != want {
					t.Errorf("FinalBefore %d s once the cut took effect, want %d s, PENDING_WINDOW_S behind the newest record",
						(last.FinalBefore-base)/1000, (want-base)/1000)
				}
			},
		},
		{
			name: "a sampler that starts again while a cut waits keeps nothing after the last sample before the cut final until the cut takes effect, and the slots between are a never-sampling device's",
			build: func(b *tl) {
				hooked(b, 9500)
				b.samples(at(9100), at(9100+1800))
			},
			check: func(t *testing.T, batches [][]event.Event, outs []Output, _, one Derived) {
				waits(t, batches, outs)
				for _, s := range one.Slots {
					if s.Start >= at(1800) && s.Start < at(9105) && (s.Present != event.PresentUnknown || s.Asleep) {
						t.Fatalf("the slot at %d s is %q, asleep %v; want unknown, as the cut made it", (s.Start-base)/1000, s.Present, s.Asleep)
					}
				}
			},
		},
		{
			name: "a stop hook stamped a second before the resume but sealed one batch after it ends the suspend's reach as one Step reads it: the gap holds the clock until the newest record is rules.late past its end, so nothing in it goes final asleep first",
			build: func(b *tl) {
				hooked(b, 1790)
				b.power(at(suspend), event.PowerSuspend)
				b.power(at(resume), event.PowerResume)
				stops(b, at(resume-1), "s_1", "p_1", "t_1")
				b.samples(at(resume), at(resume+1800))
			},
			batches: sealedAfter(isStop, isPower(event.PowerResume)),
			check: func(t *testing.T, batches [][]event.Event, outs []Output, _, one Derived) {
				heldFrom(t, batches, outs, at(resume), at(resume-1)+r.late)
				unknownFrom(t, one, at(1800), floorSlot(at(resume)))
			},
		},
		{
			name: "a tool call stamped a second before the sample that ends a gap longer than BUCKET_TTL_S, but sealed one batch after it, ends the reach before the gap as one Step reads it",
			build: func(b *tl) {
				hooked(b, 1790)
				b.harness(at(9059), event.ViaHook, "s_1", "p_1", &event.AgentTool{ToolClass: event.ToolBash,
					TStart: at(9054), TEnd: at(9059), VisibleOutputLines: 2})
				b.samples(at(9060), at(9060+1800))
			},
			batches: sealedAfter(func(e event.Event) bool {
				_, ok := e.Data.(*event.AgentTool)
				return ok && e.T == at(9059)
			}, func(e event.Event) bool {
				_, ok := e.Data.(*event.OSPresence)
				return ok && e.T == at(9060)
			}),
			check: func(t *testing.T, batches [][]event.Event, outs []Output, _, one Derived) {
				heldFrom(t, batches, outs, at(9060), at(9059)+r.late)
				unknownFrom(t, one, at(1800), at(9060))
			},
		},
		{
			name: "a gap longer than BUCKET_TTL_S with no record the agent stamps in it holds the clock only until the newest record is rules.late past its end: then FinalBefore follows the newest record, and the gap stays asleep",
			build: func(b *tl) {
				hooked(b, 1790)
				b.power(at(suspend), event.PowerSuspend)
				b.power(at(resume), event.PowerResume)
				b.samples(at(resume), at(resume+1800))
			},
			check: func(t *testing.T, batches [][]event.Event, outs []Output, _, one Derived) {
				heldFrom(t, batches, outs, at(resume), at(resume)+r.late)
				high, moved := int64(0), false
				for i, b := range batches {
					for _, e := range b {
						high = max(high, e.T)
					}
					if high >= at(resume)+r.late {
						if want := floorSlot(high - r.settle); outs[i].FinalBefore != want {
							t.Fatalf("with the newest record at %d s, FinalBefore %d s; want %d s, PENDING_WINDOW_S behind it, once the hold ends",
								(high-base)/1000, (outs[i].FinalBefore-base)/1000, (want-base)/1000)
						}
						moved = true
					}
				}
				if !moved {
					t.Fatal("no Step's newest record is rules.late past the resume")
				}
				for _, s := range one.Slots {
					if s.Start >= at(1800) && s.Start < floorSlot(at(resume)) && !s.Asleep {
						t.Fatalf("the slot at %d s isn't asleep; want every slot the machine slept through asleep", (s.Start-base)/1000)
					}
				}
			},
		},
		{
			name: "a lock open across the never-sampling stretch stays on the slots before it when an unlock starts the sampler again",
			build: func(b *tl) {
				turn(b, 9500)
				b.samples(at(0), at(990))
				b.power(at(1000), event.PowerLock)
				b.samples(at(1005), at(1800))
				b.power(at(9100), event.PowerUnlock)
				b.samples(at(9100), at(9100+1800))
			},
			check: func(t *testing.T, batches [][]event.Event, outs []Output, _, one Derived) {
				waits(t, batches, outs)
				n := 0
				for _, s := range one.Slots {
					if s.Start >= at(1005) && s.Start < at(1800) {
						n++
						if !s.Locked {
							t.Fatalf("the slot at %d s isn't locked; want the lock from 1,000 s on every slot before the stretch", (s.Start-base)/1000)
						}
					}
				}
				if n == 0 {
					t.Fatal("no slot derived while the screen was locked")
				}
				unknownFrom(t, one, at(1800), floorSlot(at(9100)))
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(false)
			c.build(b)
			recs := b.records()
			batches := minuteBatches(recs)
			if c.batches != nil {
				batches = c.batches(t, recs)
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
			c.check(t, batches, outs, finals, one)
		})
	}
}

// TestHeadlessAgentDay checks testdata/timelines/relayed/agent-day, a day of
// Claude Code turns T3 Code's server runs on a computer with no desktop,
// with the os.focus its agent writes as it starts, saying focus is
// unknown, a second before the day's first record, and again as it starts
// at noon after a restart. Neither changes what the day scores: a Step a
// minute gives the case's golden.jsonl.
func TestHeadlessAgentDay(t *testing.T) {
	root, err := timelines.Root()
	if err != nil {
		t.Fatal(err)
	}
	f, err := timelines.Load(root, relayedDir+"/agent-day")
	if err != nil {
		t.Fatal(err)
	}
	noon := f.Events[0].T + 12*3600*1000
	evs := make([]event.Event, 0, len(f.Events)+2)
	n := 0
	for i, e := range f.Events {
		if i == 0 || e.T >= noon && f.Events[i-1].T < noon {
			n++
			evs = append(evs, event.Event{Envelope: event.Envelope{
				EventID: fmt.Sprintf("01a20700-0000-7000-8000-%012x", n), DeviceID: e.DeviceID, Seq: e.Seq,
				T: e.T - 1000, Mono: e.Mono - 1000, BootID: e.BootID, Source: event.SourceOS, Via: event.ViaOS,
				Harness: event.HarnessNone, SessionKey: event.NoSession,
			}, Data: &event.OSFocus{AppClass: event.AppUnknown}})
		}
		evs = append(evs, e)
	}
	if n != 2 {
		t.Fatalf("%d os.focus records, want one at the start and one at noon", n)
	}
	want, err := os.ReadFile(filepath.Join(f.Dir, goldenFile))
	if err != nil {
		t.Fatal(err)
	}
	g := *f
	g.Events = evs
	if got := goldenOutput(t, &g); !bytes.Equal(got, want) {
		t.Errorf("agent-day with its agent's os.focus records differs from its golden.jsonl, %s", lineDiff(got, want))
	}
}

// TestFirstSampleDatesItsPrompt checks the typing of the prompt whose hook
// starts the agent: only the agent's first sample, taken at the hook, dates
// it, and it falls in a slot before that sample, a never-sampling device's.
// A sample dates an input there, for the slot's input and for an act's
// evidence test, only when it came within SLEEP_GAP_S of the input
// (presence.dates), so whatever it decides is still pending.
func TestFirstSampleDatesItsPrompt(t *testing.T) {
	cases := []struct {
		name string
		// after is how long after the prompt the first sample comes, in ms.
		after int64
		want  event.Origin
	}{
		{"the first sample half a second after the prompt dates its typing across a slot boundary, so the prompt is typed",
			500, event.OriginHumanTyped},
		{"a first sample a minute after the prompt dates nothing in the slot before it, so the prompt came with no input",
			60_000, event.OriginNoInput},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(true)
			terminalSession(b)
			// Keys until 59.9 s, in the slot before the one at 60 s, and the
			// prompt at 59.95 s.
			b.press(every(atMS(55_000), atMS(59_900), 100)...)
			b.prompt(atMS(59_950), event.ViaHook, "s_1", "p_1", "t_1", 40)
			b.sample(atMS(59_950 + c.after))
			b.samples(atMS(59_950+c.after+event.SlotMS), at(300))
			d := b.run(t, Device{})
			if tu, ok := turnByKey(d, "t_1"); !ok || tu.Origin != c.want {
				t.Errorf("turn %+v, want origin %s", tu, c.want)
			}
		})
	}
}
