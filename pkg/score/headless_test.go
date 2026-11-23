// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
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
	s := NewState(testTable(t), Device{})
	var finals Derived
	var out Output
	for i := 0; i < len(recs); {
		j := i
		for j < len(recs) && floorMinute(recs[j].T) == floorMinute(recs[i].T) {
			j++
		}
		s, out = Step(s, recs[i:j])
		finals.Append(out.Final)
		i = j
	}
	return finals, out
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
