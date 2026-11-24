// SPDX-License-Identifier: AGPL-3.0-or-later

package scorer

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/store"
	"github.com/some-du6e/pairtime/internal/timelines"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/params"
	"github.com/some-du6e/pairtime/pkg/score"
)

// The loop on a computer whose agent can't sample (WP-HL1,
// docs/errata/2026-11-23-headless-scoring.md), such as a server with no
// desktop: its only OS record is the os.focus its agent writes as it
// starts, saying focus is unknown. While pkg/score took that record for its
// clock, the loop finalized nothing PENDING_WINDOW_S after each start, kept
// every record since in its window, and after a restart more than
// BUCKET_TTL_S later gave the minutes still pending empty final revisions,
// zeroing their agent time.

// hourMS is an hour in milliseconds.
const hourMS = 3600 * 1000

// agentDay returns the records of testdata/timelines/relayed/agent-day, a
// day of Claude Code turns T3 Code's server runs on a computer with no
// desktop, from from to to hours into the day, after the os.focus the
// agent writes as it starts, a second before the first of them, saying
// focus is unknown: as on a server, the computer's only OS record.
func agentDay(t *testing.T, from, to int64) []event.Event {
	t.Helper()
	root, err := timelines.Root()
	if err != nil {
		t.Fatal(err)
	}
	f, err := timelines.Load(root, "relayed/agent-day")
	if err != nil {
		t.Fatal(err)
	}
	day := f.Events[0].T - floorMod(f.Events[0].T, hourMS)
	var out []event.Event
	for _, e := range f.Events {
		if e.T < day+from*hourMS || e.T >= day+to*hourMS {
			continue
		}
		if len(out) == 0 {
			out = append(out, event.Event{Envelope: event.Envelope{
				EventID: fmt.Sprintf("01a20700-0000-7000-8000-%012x", from+1), DeviceID: e.DeviceID, Seq: e.Seq,
				T: e.T - 1000, Mono: e.Mono - 1000, BootID: e.BootID, Source: event.SourceOS, Via: event.ViaOS,
				Harness: event.HarnessNone, SessionKey: event.NoSession,
			}, Data: &event.OSFocus{AppClass: event.AppUnknown}})
		}
		out = append(out, e)
	}
	if len(out) == 0 {
		t.Fatalf("agent-day has no record from %d to %d hours into its day", from, to)
	}
	return out
}

func TestHeadlessLoop(t *testing.T) {
	// hours returns the runs of the agent that write the given hours of
	// agent-day, each with a loop of its own, as after a restart.
	hours := func(spans ...[2]int64) func(t *testing.T) [][]event.Event {
		return func(t *testing.T) [][]event.Event {
			var runs [][]event.Event
			for _, h := range spans {
				runs = append(runs, agentDay(t, h[0], h[1]))
			}
			return runs
		}
	}
	cases := []struct {
		name string
		runs func(t *testing.T) [][]event.Event
	}{
		{"a computer whose only OS record is the os.focus its agent writes as it starts keeps finalizing minutes for hours, past Lookback, as one Step over every record gives them",
			hours([2]int64{0, 9})},
		{"a restart after a stall longer than BUCKET_TTL_S derives every minute since the last final, the ones still pending when the agent stopped included",
			hours([2]int64{0, 1}, [2]int64{4, 5})},
		{"a restart between two hours of turns keeps finalizing as one run does",
			hours([2]int64{0, 3}, [2]int64{3, 6})},
		{"a desktop whose sampler stops, as KDE on X11 and then on Wayland, finalizes as a computer that can't sample once its agent's records come BUCKET_TTL_S after the last sample, and the window slides",
			func(t *testing.T) [][]event.Event {
				// An hour of a hooked turn on X11, sampled once a minute,
				// ending three hours before agent-day's first record, which
				// the agent writes on Wayland.
				day := agentDay(t, 0, 8)
				var tools []int64
				for m := int64(5); m <= 60; m += 5 {
					tools = append(tools, m*minuteMS)
				}
				return [][]event.Event{hookTurn(t, day[0].T-4*hourMS, tools), day}
			}},
		// In the next two, the first turn's run ends BUCKET_TTL_S after its
		// last tool call, before the window's start once the agent records
		// again, so the window slides past the sampler's last records while
		// the minutes before them are still pending. A turn a log reader
		// reports runs through those minutes (logTurn), with no record the
		// agent stamps itself, so its agent time gives them buckets: asleep,
		// their seconds are X.absent, and as a never-sampling device's they
		// would take SM 5.2's codes, which they do only if the carry wrongly
		// drops the sampler's last record.
		{"a sampler silence longer than Lookback that ends with a sample before any record the agent stamps, as when the agent starts again ten hours later, keeps the carried sample's reach: the hours between are asleep, as one Step reads them, a log reader's turn running in them included",
			func(t *testing.T) [][]event.Event {
				first := hookTurn(t, hookAt, hourTools())
				again := hookAt + 10*hourMS
				second := rekeyed(hookTurn(t, again+70_000, hourTools()), 1)
				idle := 585.0
				woke := sampleAt(first, again, "01a1fb1e-5318-78a1-a7f0-000000000001",
					&event.OSPresence{IdleAnyS: idle, IdleKeysS: &idle})
				run := append(logTurn(t, again-40*minuteMS, woke), woke)
				return [][]event.Event{first, append(run, second...)}
			}},
		{"a suspend longer than Lookback keeps the carried suspend's reach once the window slides past it: the ten hours the machine slept are asleep, as one Step reads them, a log reader's turn dated in them included",
			func(t *testing.T) [][]event.Event {
				first := hookTurn(t, hookAt, hourTools())
				last := first[len(first)-1].T
				asleep := sampleAt(first, last+15_000, "01a1fb1e-5318-78a1-a7f0-000000000002",
					&event.OSPower{Op: event.PowerSuspend})
				resume := last + 15_000 + 10*hourMS
				woke := sampleAt(first, resume, "01a1fb1e-5318-78a1-a7f0-000000000003",
					&event.OSPower{Op: event.PowerResume})
				second := rekeyed(hookTurn(t, resume+70_000, hourTools()), 2)
				run := append(append(first, asleep), logTurn(t, resume-40*minuteMS, woke)...)
				return [][]event.Event{append(append(run, woke), second...)}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, score.Device{})
			var all []event.Event
			for _, evs := range c.runs(t) {
				r.write(evs, nil)
				all = append(all, evs...)
			}
			chain, err := ledger.ReadChain(r.path("ledger"))
			if err != nil {
				t.Fatal(err)
			}
			want, fb := direct(r.table, r.dev, chain.DeviceID, all)
			from, to := span(all)
			got := r.stored(from, to)
			compare(t, got, want)
			st := r.rescore()
			if st.FinalBefore != fb {
				t.Errorf("the loop's FinalBefore is %s, and one Step's %s", time.UnixMilli(st.FinalBefore).UTC(),
					time.UnixMilli(fb).UTC())
			}
			// One Step and the loop could stall alike, so the case holds
			// both to what the records call for: FinalBefore PENDING_WINDOW_S
			// behind the latest record, every minute before it with agent
			// time final with that time, and the window Lookback behind it.
			last := all[len(all)-1].T
			pending := r.table.Int(params.PENDING_WINDOW_S) * 1000
			if st.FinalBefore < last-pending-event.SlotMS || st.FinalBefore > last-pending {
				t.Errorf("FinalBefore is %s, and the latest record %s: want PENDING_WINDOW_S between them",
					time.UnixMilli(st.FinalBefore).UTC(), time.UnixMilli(last).UTC())
			}
			stored := map[int64]store.Bucket{}
			for _, b := range got {
				stored[b.At()] = b
			}
			for m, w := range want {
				if w.AgentRunningS == 0 || m+minuteMS > st.FinalBefore {
					continue
				}
				if g, ok := stored[m]; !ok || g.State != event.StateFinal || g.AgentRunningS != w.AgentRunningS {
					t.Errorf("%s has %v s of agent time in one Step, and the loop's latest revision is %+v; want it final with that time",
						time.UnixMilli(m).UTC(), w.AgentRunningS, g)
				}
			}
			if cur := r.savedCursor(); cur.From < st.FinalBefore-DefaultLookback.Milliseconds()-15*minuteMS {
				t.Errorf("the window starts at %s, more than Lookback before FinalBefore at %s",
					time.UnixMilli(cur.From).UTC(), time.UnixMilli(st.FinalBefore).UTC())
			}
		})
	}
}

// hookAt is when the turns of TestHeadlessLoop's sampled cases start, in
// Unix ms: 6.4's day, as hookTurn's records are.
const hookAt = int64(1_795_214_400_000)

// hourTools returns the times of a tool call every five minutes for an
// hour after a hookTurn's prompt, in ms after it.
func hourTools() []int64 {
	var tools []int64
	for m := int64(5); m <= 60; m += 5 {
		tools = append(tools, m*minuteMS)
	}
	return tools
}

// rekeyed returns evs, a hookTurn's records, as a turn of a session of its
// own: event ids of their own, numbered by n, and session and turn keys
// that differ from the ones hookTurn gives.
func rekeyed(evs []event.Event, n int) []event.Event {
	out := slices.Clone(evs)
	for i := range out {
		e := &out[i]
		e.EventID = fmt.Sprintf("01a1fb1e-5318-78a1-a7%02x-%012x", 0x81+n, i+1)
		if e.SessionKey != event.NoSession {
			e.SessionKey += "x"
		}
		if p, ok := e.Data.(*event.PromptSubmit); ok {
			q := *p
			q.TurnKey += "x"
			e.Data = &q
		}
	}
	return out
}

// sampleAt returns another record of the presence sampler's like the first
// sample in evs, dated ts, with event id id and payload d.
func sampleAt(evs []event.Event, ts int64, id string, d event.Payload) event.Event {
	for _, e := range evs {
		if _, ok := e.Data.(*event.OSPresence); ok {
			e.EventID = id
			e.Mono += ts - e.T
			e.T = ts
			e.Data = d
			return e
		}
	}
	panic("no presence sample to copy")
}

// logTurn returns a turn of a session a log reader reports, with nothing
// the agent stamps itself: 6.4's prompt and an agent output five minutes
// later, both read from a transcript, the prompt at at, in a session and
// turn of their own. Its run goes on BUCKET_TTL_S past that output, so it
// has agent time in the minutes after whatever their presence, and no
// record of it cuts a sample's reach. The records take ref's boot and
// monotonic clock, as records of the same run.
func logTurn(t *testing.T, at int64, ref event.Event) []event.Event {
	t.Helper()
	var prompt, output *event.Event
	evs := fixture(t, "6.4").Events
	for i := range evs {
		e := &evs[i]
		switch d := e.Data.(type) {
		case *event.PromptSubmit:
			prompt = cmpOr(prompt, e)
		case *event.AgentOutput:
			if e.Via == event.ViaLog && !d.IsFinal {
				output = cmpOr(output, e)
			}
		}
	}
	if prompt == nil || output == nil {
		t.Fatal("6.4 has no prompt, or no agent output a log reader read")
	}
	clone := func(tmpl *event.Event, ts int64, n int) event.Event {
		e := *tmpl
		e.EventID = fmt.Sprintf("01a1fb1e-5318-78a1-a7f3-%012x", n)
		e.BootID, e.Mono, e.T = ref.BootID, ref.Mono+ts-ref.T, ts
		e.Source, e.Via, e.SessionKey = event.SourceLog, event.ViaLog, e.SessionKey+"l"
		return e
	}
	p, o := clone(prompt, at, 1), clone(output, at+5*minuteMS, 2)
	pd, od := *prompt.Data.(*event.PromptSubmit), *output.Data.(*event.AgentOutput)
	pd.TurnKey += "l"
	od.TurnKey += "l"
	p.Data, o.Data = &pd, &od
	return []event.Event{p, o}
}

// retagged returns evs, a logTurn's records, as a turn of a session of
// their own: event ids of their own under id, and tag added to their
// session and turn keys.
func retagged(evs []event.Event, tag, id string) []event.Event {
	out := slices.Clone(evs)
	for i := range out {
		e := &out[i]
		e.EventID = fmt.Sprintf("01a1fb1e-5318-78a1-%s-%012x", id, i+1)
		e.SessionKey += tag
		switch d := e.Data.(type) {
		case *event.PromptSubmit:
			q := *d
			q.TurnKey += tag
			e.Data = &q
		case *event.AgentOutput:
			q := *d
			q.TurnKey += tag
			e.Data = &q
		}
	}
	return out
}

// logMorning returns logTurn's prompt at from and its agent output every
// ten minutes after it until to, each with an event id of its own: a turn
// a log reader reports for hours, with nothing the agent stamps itself.
func logMorning(t *testing.T, from, to int64, ref event.Event) []event.Event {
	t.Helper()
	turn := logTurn(t, from, ref)
	out := []event.Event{turn[0]}
	n := 0
	for ts := from + 10*minuteMS; ts <= to; ts += 10 * minuteMS {
		n++
		e := turn[1]
		e.EventID = fmt.Sprintf("01a1fb1e-5318-78a1-a7f8-%012x", n)
		e.Mono += ts - e.T
		e.T = ts
		out = append(out, e)
	}
	return out
}

// TestCarryReach checks the carry against pkg/score's reach rule (SM 2.1):
// the window drops a carried record of the presence sampler's only when a
// record the agent stamps itself, dated more than BUCKET_TTL_S after it,
// comes before the sampler's next record, whether the carry folded that
// record or the window holds it (window.cutBy), and only once the newest
// record the device's own clock dated is PENDING_WINDOW_S - READ_HORIZON_S
// past it, whatever a log reader's or the bridge's records say. A Step
// over the carry and the window then derives every slot from the window's
// start as one Step over every record does: asleep through a silence or a
// suspend longer than Lookback that ends with a record of the sampler's,
// or while a cut still waits; a never-sampling device's after a stamped
// record cut the reach, with no gap from before the stretch in the slot of
// the record that starts the sampler again, an unlock and the sample at
// its time included. TestHeadlessLoop compares minute buckets, and a
// minute with no credit and no agent time has none, which is why its carry
// cases need a turn running in the minutes in question to tell those slots
// apart.
func TestCarryReach(t *testing.T) {
	table := testTable()
	turn := hookTurn(t, hookAt, hourTools())
	last := turn[len(turn)-1].T
	n := 0
	rec := func(ts int64, d event.Payload) event.Event {
		n++
		return sampleAt(turn, ts, fmt.Sprintf("01a1fb1e-5318-78a1-a7f1-%012x", n), d)
	}
	// idle is a sample that saw no input for s seconds.
	idle := func(s float64) *event.OSPresence { return &event.OSPresence{IdleAnyS: s, IdleKeysS: &s} }
	// woken is the sampler starting again at ts, with a sample every 15 s
	// for five minutes, the last input ten minutes before ts, and an
	// os.focus a minute in, a record the agent stamps itself.
	woken := func(ts int64) []event.Event {
		var out []event.Event
		for k := int64(0); k <= 20; k++ {
			out = append(out, rec(ts+k*15_000, idle(float64(600+15*k))))
		}
		return append(out, rec(ts+61_000, &event.OSFocus{AppClass: event.AppUnknown}))
	}
	again := last + 10*hourMS
	resume := last + 15_000 + 10*hourMS
	cut := rec(last+3*hourMS, &event.OSFocus{AppClass: event.AppUnknown})
	cases := []struct {
		name  string
		after []event.Event
		from  int64
		// kept says the carry keeps a record of the sampler's.
		kept bool
		// waits says the case holds a cut that still waits. No loop carries
		// a record then, since FinalBefore stays at it while the cut waits,
		// and one Step holds its clock there, deriving no slot after it,
		// while the carried copy is dated again. So only the carry's
		// decision is checked: it keeps the record, as pkg/score keeps the
		// reach until the cut takes effect.
		waits bool
	}{
		{"a sample after a silence longer than Lookback, before any record the agent stamps, keeps the carried sample's reach: the silence is asleep",
			woken(again), again - 15*minuteMS, true, false},
		{"a resume after a ten-hour suspend, before any record the agent stamps, keeps the carried suspend's reach: the machine slept",
			append([]event.Event{rec(last+15_000, &event.OSPower{Op: event.PowerSuspend}),
				rec(resume, &event.OSPower{Op: event.PowerResume})}, woken(resume+15_000)...),
			resume - 15*minuteMS, true, false},
		{"a record the agent stamps BUCKET_TTL_S after the last sample, in the window before the sampler's next record, ends the carried sample's reach",
			append([]event.Event{cut}, woken(again)...), last + 2*hourMS, false, false},
		{"the same record folded into the carry ends the carried sample's reach too",
			append([]event.Event{cut}, woken(again)...), last + 4*hourMS, false, false},
		{"an unlock that starts the sampler again after the cut, with the sample the agent takes at its time, keeps the gap from before the stretch out of that slot",
			append([]event.Event{cut, rec(again, &event.OSPower{Op: event.PowerUnlock})}, woken(again)...), last + 2*hourMS, false, false},
		{"a suspend and a resume with no sample after either, and a stamped record more than BUCKET_TTL_S after the last sample but not after the resume, keep the carried sample, and the next sample opens no gap from it, since the sampler ran (D-sampleless-power)",
			append([]event.Event{rec(last+hourMS, &event.OSPower{Op: event.PowerSuspend}),
				rec(last+70*minuteMS, &event.OSPower{Op: event.PowerResume}), cut}, woken(again)...),
			again - 15*minuteMS, true, false},
		{"a lock before a stretch a stamped record cut, with the sampler starting again after it, leaves the carry with the restart's sample alone, so the lock doesn't reach the window",
			append([]event.Event{rec(last+15_000, &event.OSPower{Op: event.PowerLock}), cut,
				rec(last+4*hourMS, idle(600))}, woken(again)...),
			again - 15*minuteMS, true, false},
		{"a suspend and a resume that start the sampler again with no sample after them, as the agent writes when an idle read fails, keep the stretch's last sample out of the carry, whichever record the restart is",
			append([]event.Event{cut, rec(last+4*hourMS, &event.OSPower{Op: event.PowerSuspend}),
				rec(last+4*hourMS+30*minuteMS, &event.OSPower{Op: event.PowerResume})}, woken(again)...),
			again - 15*minuteMS, true, false},
		{"a restart's second sample keeps the restart: a lock from before the stretch stays out of the carry",
			append([]event.Event{rec(last+15_000, &event.OSPower{Op: event.PowerLock}), cut,
				rec(last+4*hourMS, idle(600)), rec(last+4*hourMS+15_000, idle(615))}, woken(again)...),
			again - 15*minuteMS, true, false},
		{"after a suspend that restarts the sampler and a resume, with no sample after either, no slot takes its keyboard flag from the sample before the stretch, which the carry drops",
			append([]event.Event{cut, rec(last+4*hourMS, &event.OSPower{Op: event.PowerSuspend}),
				rec(last+12*hourMS, &event.OSPower{Op: event.PowerResume})},
				logTurn(t, last+12*hourMS+10*minuteMS, turn[len(turn)-1])...),
			last + 6*hourMS, true, false},
		{"a lock and then a suspend that start the sampler again with no sample after either, as a lid close writes them, and a resume with none, keep the screen locked after the resume until the next sample: the carry keeps the lock beside the suspend",
			append(append([]event.Event{cut, rec(last+4*hourMS-minuteMS, &event.OSPower{Op: event.PowerLock}),
				rec(last+4*hourMS, &event.OSPower{Op: event.PowerSuspend}),
				rec(last+12*hourMS, &event.OSPower{Op: event.PowerResume})},
				logTurn(t, last+12*hourMS+10*minuteMS, turn[len(turn)-1])...), woken(last+13*hourMS)...),
			last + 6*hourMS, true, false},
		{"after that lid close, the samples an hour after its resume, which close its lock, and a nap whose suspend and resume come with no sample, the window doesn't lock the slots after the nap: the carry leaves out the lock those samples closed, dated at grace with them",
			append(append(append(append([]event.Event{cut, rec(last+4*hourMS-minuteMS, &event.OSPower{Op: event.PowerLock}),
				rec(last+4*hourMS, &event.OSPower{Op: event.PowerSuspend}),
				rec(last+12*hourMS, &event.OSPower{Op: event.PowerResume})},
				logTurn(t, last+12*hourMS+10*minuteMS, turn[len(turn)-1])...), woken(last+13*hourMS)...),
				rec(last+14*hourMS, &event.OSPower{Op: event.PowerSuspend}),
				rec(last+23*hourMS, &event.OSPower{Op: event.PowerResume})), woken(last+24*hourMS)...),
			last + 22*hourMS, true, false},
		{"a lock with no sample, which starts the sampler again after the cut and stays its latest os.power, and the samples after it that close it, both carried, leave the window's slots unlocked: the carry leaves out the lock those samples closed, dated at grace with them",
			append(append([]event.Event{cut, rec(last+4*hourMS, &event.OSPower{Op: event.PowerLock})},
				woken(last+5*hourMS)...), woken(last+11*hourMS)...),
			last + 10*hourMS, true, false},
		{"a stamped record before the window's first sample that the newest record isn't yet rules.late past doesn't end the carried sample's reach yet",
			[]event.Event{rec(again-60_000, &event.OSFocus{AppClass: event.AppUnknown}),
				rec(again, idle(600))},
			again - 15*minuteMS, true, true},
		{"a log reader's records dated ten minutes past the window's first sample don't make the stamped record before it end the carried sample's reach: the cut waits until a record the device's own clock dated is rules.late past it",
			append([]event.Event{rec(again-60_000, &event.OSFocus{AppClass: event.AppUnknown}),
				rec(again, idle(600))}, logTurn(t, again+10*minuteMS, turn[len(turn)-1])...),
			again - 15*minuteMS, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			all := append(slices.Clone(turn), c.after...)
			w := &window{gaps: gapsOf(table), ttl: table.Int(params.BUCKET_TTL_S) * 1000,
				late: (table.Int(params.PENDING_WINDOW_S) - table.Int(params.READ_HORIZON_S)) * 1000}
			for i, e := range all {
				w.add(e, uint64(i+1))
			}
			w.slide(c.from)
			recs := w.records()
			if kept := slices.ContainsFunc(recs, func(e event.Event) bool { return e.T < c.from && sampler(&e) }); kept != c.kept {
				t.Errorf("the carry keeps a record of the sampler's: %v, want %v", kept, c.kept)
			}
			if c.waits {
				return
			}
			got, want := slotsFrom(table, recs, c.from), slotsFrom(table, all, c.from)
			if len(got) != len(want) {
				t.Fatalf("a Step over the carry and the window derives %d slots from the window's start, one Step over every record %d",
					len(got), len(want))
			}
			for i := range got {
				if !reflect.DeepEqual(got[i], want[i]) {
					t.Fatalf("the slot at %s is %+v over the carry and the window, and %+v in one Step over every record",
						time.UnixMilli(want[i].Start).UTC(), got[i], want[i])
				}
			}
		})
	}
}

// TestCarryQuestions checks the carry's question state
// (sessionCarry.Asks) against pkg/score's answer class (SM 2.2; ledger-v1
// 10.7): a prompt reads its session's outputs and stops numbered
// before it, and once a hook or log record of the session is numbered
// before it, not a side door's, whatever the window folded into the carry.
// Each case's morning prompt comes eight hours after an evening the carry
// folded, typed into the terminal that hosts the session, and a Step over
// the carry and the window must class it as one Step over every record
// does.
func TestCarryQuestions(t *testing.T) {
	table := testTable()
	evening := hookTurn(t, hookAt, hourTools())
	var prompt event.Event
	for _, e := range evening {
		if _, ok := e.Data.(*event.PromptSubmit); ok {
			prompt = e
		}
	}
	sess, turn := prompt.SessionKey, prompt.Data.(*event.PromptSubmit).TurnKey
	last := evening[len(evening)-1].T
	morning := last + 8*hourMS
	n := 0
	id := func() string {
		n++
		return fmt.Sprintf("01a1fb1e-5318-78a1-a78b-%012x", n)
	}
	// harness is a record of the session at ts by via, made from the
	// evening's prompt.
	harness := func(ts int64, via event.Via, d event.Payload) event.Event {
		e := prompt
		e.EventID = id()
		e.Mono += ts - e.T
		e.T, e.Via, e.Data = ts, via, d
		e.Source = event.SourceHook
		if via == event.ViaLog {
			e.Source = event.SourceLog
		}
		return e
	}
	output := func(ts int64, via event.Via, asks bool) event.Event {
		return harness(ts, via, &event.AgentOutput{TurnKey: turn, Words: 30, IsFinal: true, AsksQuestion: asks})
	}
	stop := func(ts int64, via event.Via) event.Event {
		return harness(ts, via, &event.AgentStop{TurnKey: turn, StopReason: event.StopEndTurn})
	}
	start := func(ts int64, via event.Via, kind event.StartKind) event.Event {
		return harness(ts, via, &event.SessionStart{StartKind: kind, Entrypoint: event.EntrypointCLI,
			ParentClass: event.ParentTerminalApp, HasTTY: true})
	}
	// typed is the morning: focus on the terminal hosting the session,
	// input every 15 s from a minute before the prompt, and the prompt, by
	// via.
	typed := func(via event.Via) []event.Event {
		out := []event.Event{sampleAt(evening, morning-2*minuteMS, id(), &event.OSFocus{AppClass: event.AppTerminal,
			FocusProject: prompt.ProjectKey, HostedSessions: []string{sess}})}
		for ts := morning - minuteMS; ts <= morning+5*minuteMS; ts += 15_000 {
			idle := 1.0
			out = append(out, sampleAt(evening, ts, id(), &event.OSPresence{IdleAnyS: idle, IdleKeysS: &idle}))
		}
		return append(out, harness(morning, via, &event.PromptSubmit{TurnKey: "t_morning", LenChars: 30,
			TypedLenEst: 30, OriginDeclared: event.DeclaredNone}))
	}
	samples := func() []event.Event {
		var out []event.Event
		for _, e := range evening {
			if sampler(&e) {
				out = append(out, e)
			}
		}
		return out
	}
	// morningClass is the class one Step over recs gives the morning prompt.
	morningClass := func(t *testing.T, recs []event.Event) event.PromptClass {
		t.Helper()
		_, out := score.Step(score.NewState(table, score.Device{}), recs)
		var all score.Derived
		all.Append(out.Final)
		all.Append(out.Pending)
		for _, tu := range all.Turns {
			if tu.TurnKey == "t_morning" {
				return tu.PromptClass
			}
		}
		t.Fatal("no turn for the morning prompt")
		return ""
	}
	cases := []struct {
		name string
		// recs returns the case's records in the order the agent writes
		// them, which numbers them.
		recs func() []event.Event
		want event.PromptClass
	}{
		{"a hook prompt after the hooked turn's question, which a plugin's output follows, is an answer: the carry keeps the question, though its Output is the plugin's",
			func() []event.Event {
				out := append(slices.Clone(evening), output(last+minuteMS, event.ViaLog, true),
					output(last+2*minuteMS, event.ViaPlugin, false), stop(last+3*minuteMS, event.ViaHook))
				return append(out, typed(event.ViaHook)...)
			}, event.ClassAnswer},
		{"a plugin prompt in a session whose only hook record is its first session.start reads no plugin's question: the carry keeps that record, though its Start is the plugin's",
			func() []event.Event {
				out := append([]event.Event{start(hookAt-2*minuteMS, event.ViaHook, event.StartStartup),
					start(hookAt-minuteMS, event.ViaPlugin, event.StartResume)}, samples()...)
				out = append(out, harness(hookAt, event.ViaPlugin, &event.PromptSubmit{TurnKey: turn, LenChars: 30,
					TypedLenEst: 30, OriginDeclared: event.DeclaredNone}),
					output(last+minuteMS, event.ViaPlugin, true), stop(last+2*minuteMS, event.ViaPlugin))
				return append(out, typed(event.ViaPlugin)...)
			}, event.ClassSubstantive},
		{"an output a log reader read late, numbered after a question dated later, ends the question: the carry keeps the outputs by seq, not by t",
			func() []event.Event {
				out := append(slices.Clone(evening), output(last+2*minuteMS, event.ViaLog, true),
					output(last+minuteMS, event.ViaLog, false), stop(last+3*minuteMS, event.ViaHook))
				return append(out, typed(event.ViaHook)...)
			}, event.ClassSubstantive},
		{"a hook prompt after the hooked turn's question is an answer, though a log reader's output dated in the evening and numbered after the prompt folds into the carry: the carry keeps the question the prompt read",
			func() []event.Event {
				out := append(slices.Clone(evening), output(last+minuteMS, event.ViaLog, true), stop(last+2*minuteMS, event.ViaHook))
				out = append(out, typed(event.ViaHook)...)
				return append(out, output(last+3*minuteMS, event.ViaLog, false))
			}, event.ClassAnswer},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			all := c.recs()
			for i := range all {
				all[i].Seq = uint64(i + 1)
			}
			w := &window{gaps: gapsOf(table), ttl: table.Int(params.BUCKET_TTL_S) * 1000,
				late: (table.Int(params.PENDING_WINDOW_S) - table.Int(params.READ_HORIZON_S)) * 1000}
			for i, e := range all {
				w.add(e, uint64(i+1))
			}
			w.slide(morning - 30*minuteMS)
			want := morningClass(t, all)
			if want != c.want {
				t.Fatalf("one Step over every record classes the morning prompt %q, and the case expects %q", want, c.want)
			}
			if got := morningClass(t, w.records()); got != want {
				t.Errorf("a Step over the carry and the window classes the morning prompt %q, and one Step over every record %q", got, want)
			}
		})
	}
}

// slotsFrom returns the slots one Step over recs derives from from on.
func slotsFrom(table *params.Table, recs []event.Event, from int64) []score.Slot {
	_, out := score.Step(score.NewState(table, score.Device{}), recs)
	var all score.Derived
	all.Append(out.Final)
	all.Append(out.Pending)
	var slots []score.Slot
	for _, s := range all.Slots {
		if s.Start >= from {
			slots = append(slots, s)
		}
	}
	return slots
}

// TestOlderPlaceCarry checks open's rebuild of a carry that an agent
// before WP-HL1 saved in state.json, with no stamped, restart_at, lock or
// v (#294). A sampler stops at C while the turn's hooks run on for three
// hours, past BUCKET_TTL_S, and stop. The machine suspends and resumes with
// no sample after either, as when its idle read fails, and hooks run again
// after the resume. Loaded as it is, the carry reads as if no record ended
// C's reach. Rebuilt from the ledger, it must be the carry the window kept,
// and the loop must give what one Step over every record gives, minute by
// minute.
func TestOlderPlaceCarry(t *testing.T) {
	first := hookTurn(t, hookAt, hourTools())
	c := first[len(first)-1].T
	resume := c + 14*hourMS
	tests := []struct {
		name string
		// upgrade is when the agent upgrades, after the resume.
		upgrade int64
		// restart says the carry has folded the suspend, which starts the
		// sampler again after C's cut, and lock that a lock with no sample
		// comes a minute before it, as a lid close writes them.
		restart, lock bool
	}{
		{name: "with the suspend the window's first record of the sampler's and nothing stamped before it there, the rebuilt carry cuts C, so the hours after the resume get one Step's buckets",
			upgrade: resume + 2*hourMS},
		{name: "with the suspend folded into the carry as the sampler's restart after C's cut, the rebuilt carry keeps that restart, which drops C",
			upgrade: resume + 4*hourMS, restart: true},
		{name: "with a lid close's lock and suspend folded into the carry, the rebuilt carry keeps the lock beside the suspend, as the window did",
			upgrade: resume + 4*hourMS, restart: true, lock: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var tool, prompt event.Event
			for _, e := range first {
				switch e.Data.(type) {
				case *event.AgentTool:
					tool = e
				case *event.PromptSubmit:
					prompt = e
				}
			}
			n := 0
			clone := func(tmpl event.Event, ts int64, d event.Payload) event.Event {
				n++
				e := tmpl
				e.EventID = fmt.Sprintf("01a1fb1e-5318-78a1-a7f5-%012x", n)
				e.Mono += ts - e.T
				e.T, e.Data = ts, d
				return e
			}
			run := slices.Clone(first)
			for ts := c + 10*minuteMS; ts <= c+3*hourMS; ts += 10 * minuteMS {
				d := *tool.Data.(*event.AgentTool)
				d.TStart, d.TEnd = ts-800, ts
				run = append(run, clone(tool, ts, &d))
			}
			run = append(run, clone(prompt, c+3*hourMS+minuteMS,
				&event.AgentStop{TurnKey: prompt.Data.(*event.PromptSubmit).TurnKey, StopReason: event.StopEndTurn}))
			if tc.lock {
				run = append(run, clone(first[len(first)-1], c+10*hourMS-minuteMS, &event.OSPower{Op: event.PowerLock}))
			}
			run = append(run, clone(first[len(first)-1], c+10*hourMS, &event.OSPower{Op: event.PowerSuspend}),
				clone(first[len(first)-1], resume, &event.OSPower{Op: event.PowerResume}))
			var tools []int64
			for m := int64(10); m <= 300; m += 10 {
				tools = append(tools, m*minuteMS)
			}
			var rest []event.Event
			for _, e := range rekeyed(hookTurn(t, resume+70_000, tools), 4) {
				switch _, sample := e.Data.(*event.OSPresence); {
				case sample:
				case e.T < tc.upgrade:
					run = append(run, e)
				default:
					rest = append(rest, e)
				}
			}
			r := newRig(t, score.Device{})
			r.write(run, nil)
			kept := r.savedCursor().Carry
			if kept.Stamped == 0 || (kept.RestartAt != 0) != tc.restart {
				t.Fatalf("the window kept a carry with stamped %d and restart_at %d; the case needs the hooks' stamped and restart %v",
					kept.Stamped, kept.RestartAt, tc.restart)
			}
			if (kept.Lock != nil) != tc.lock {
				t.Fatalf("the window kept a carry with lock %v; the case needs the lid close's lock in it %v", kept.Lock != nil, tc.lock)
			}
			olderPlace(t, r, 0, false)
			r.rescore()
			if got := r.savedCursor().Carry; !reflect.DeepEqual(got, kept) {
				t.Errorf("the rebuilt carry is\n%s\nand the window kept\n%s", carryJSON(t, got), carryJSON(t, kept))
			}
			r.write(rest, nil)
			all := append(slices.Clone(run), rest...)
			chain, err := ledger.ReadChain(r.path("ledger"))
			if err != nil {
				t.Fatal(err)
			}
			want, fb := direct(r.table, r.dev, chain.DeviceID, all)
			from, to := span(all)
			compare(t, r.stored(from, to), want)
			if st := r.rescore(); st.FinalBefore != fb {
				t.Errorf("the loop's FinalBefore is %s, and one Step's %s", time.UnixMilli(st.FinalBefore).UTC(), time.UnixMilli(fb).UTC())
			}
		})
	}
}

// TestOlderPlaceNoSample checks open's rebuild of an older agent's carry
// on a machine whose idle read never succeeded, so the carry has Power and
// no Presence. A lock with no sample comes a minute before a turn whose
// hooks run three hours, past BUCKET_TTL_S, so they end its reach; a
// suspend with no sample after them starts the sampler again, and drops
// the lock (SM 2.1); and the machine resumes eight hours later, with an
// hour and a half of hooks after the resume. They stay within BUCKET_TTL_S
// of it, so the slots after it are unknown, as the sampler's after a
// restart with no sample, and a lock that outlived the rebuild would lock
// them. A place saved past the suspend carries the lock, the suspend and
// the restart there. Rebuilt from the ledger, the carry must be
// the one the window kept, with its restart dropping the lock, whether the
// place came before WP-HL1 or between its first part and
// D-sampleless-power (#353), alpha.7's rebuild of an older place included,
// which kept no restart_at (#362), and the loop must give what one Step
// over every record gives, minute by minute.
func TestOlderPlaceNoSample(t *testing.T) {
	// unsampled returns evs without their presence samples.
	unsampled := func(evs []event.Event) []event.Event {
		return slices.DeleteFunc(slices.Clone(evs), func(e event.Event) bool {
			_, sample := e.Data.(*event.OSPresence)
			return sample
		})
	}
	tests := []struct {
		name string
		v    int
		// restart says a v 1 place kept the restart_at its agent's own fold
		// gave it, where alpha.7's rebuild of an older place found none.
		restart bool
	}{
		{"a place an agent before WP-HL1 saved gets the restart folded again from every record before the window, so the suspend drops the rebuilt lock",
			0, false},
		{"a place v0.1.0-alpha.7 saved from its own fold keeps its restart, which drops the rebuilt lock", 1, true},
		{"a place v0.1.0-alpha.7 rebuilt from an older one, at v 1 with no restart_at, gets the restart folded again, which drops the rebuilt lock",
			1, false},
		{"a place an agent before E-prompt-class-asked saved, at v 2, gets its sessions' question state folded again",
			2, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			full := hookTurn(t, hookAt, hourTools())
			first := unsampled(full)
			lockAt := first[0].T - minuteMS
			var tool event.Event
			for _, e := range first {
				if _, ok := e.Data.(*event.AgentTool); ok {
					tool = e
				}
			}
			n := 0
			clone := func(tmpl event.Event, ts int64, d event.Payload) event.Event {
				n++
				e := tmpl
				e.EventID = fmt.Sprintf("01a1fb1e-5318-78a1-a789-%012x", n)
				e.Mono += ts - e.T
				e.T, e.Data = ts, d
				return e
			}
			// power is an os.power with no sample after it, as the agent
			// writes when its idle read fails.
			power := func(ts int64, op event.PowerOp) event.Event {
				n++
				return sampleAt(full, ts, fmt.Sprintf("01a1fb1e-5318-78a1-a78a-%012x", n), &event.OSPower{Op: op})
			}
			run := append([]event.Event{power(lockAt, event.PowerLock)}, first...)
			for ts := first[len(first)-1].T + 10*minuteMS; ts <= lockAt+3*hourMS; ts += 10 * minuteMS {
				d := *tool.Data.(*event.AgentTool)
				d.TStart, d.TEnd = ts-800, ts
				run = append(run, clone(tool, ts, &d))
			}
			resume := lockAt + 12*hourMS
			run = append(run, power(lockAt+4*hourMS, event.PowerSuspend), power(resume, event.PowerResume))
			var tools []int64
			for m := int64(10); m <= 90; m += 10 {
				tools = append(tools, m*minuteMS)
			}
			upgrade := resume + 30*minuteMS
			var rest []event.Event
			for _, e := range unsampled(rekeyed(hookTurn(t, resume+70_000, tools), 3)) {
				if e.T < upgrade {
					run = append(run, e)
				} else {
					rest = append(rest, e)
				}
			}
			r := newRig(t, score.Device{})
			r.write(run, nil)
			kept := r.savedCursor().Carry
			if kept.Power == nil || kept.Presence != nil || kept.Lock == nil || kept.RestartAt != lockAt+4*hourMS {
				t.Fatalf("the window kept a carry with power %v, presence %v, lock %v and restart_at %d; the case needs the suspend, no sample, the lock and the restart at the suspend",
					kept.Power != nil, kept.Presence != nil, kept.Lock != nil, kept.RestartAt)
			}
			olderPlace(t, r, tc.v, tc.restart)
			r.rescore()
			if got := r.savedCursor().Carry; !reflect.DeepEqual(got, kept) {
				t.Errorf("the rebuilt carry is\n%s\nand the window kept\n%s", carryJSON(t, got), carryJSON(t, kept))
			}
			r.write(rest, nil)
			all := append(slices.Clone(run), rest...)
			chain, err := ledger.ReadChain(r.path("ledger"))
			if err != nil {
				t.Fatal(err)
			}
			want, fb := direct(r.table, r.dev, chain.DeviceID, all)
			from, to := span(all)
			compare(t, r.stored(from, to), want)
			if st := r.rescore(); st.FinalBefore != fb {
				t.Errorf("the loop's FinalBefore is %s, and one Step's %s", time.UnixMilli(st.FinalBefore).UTC(), time.UnixMilli(fb).UTC())
			}
		})
	}
}

// TestCarryPluginRun checks the carry's question state against a turn's run
// (#396): only a prompt or a queued message reads the asks, so the carry
// never brings back an earlier output of a turn whose prompt it holds. One
// such output, dated before the turn's carried prompt, would cut the run
// of a session no hook or log reader covers, where one Step over every
// record runs it on to its last output.
func TestCarryPluginRun(t *testing.T) {
	table := testTable()
	cases := []struct {
		name string
		// from is how long after the turn's prompt the window starts.
		from int64
	}{
		{"a plugin-only session's turn, whose prompt and first outputs the carry holds while it runs, runs on to its last output, as one Step runs it",
			9 * minuteMS},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var prompt event.Event
			for _, e := range hookTurn(t, hookAt, []int64{minuteMS}) {
				if _, ok := e.Data.(*event.PromptSubmit); ok {
					prompt = e
				}
			}
			n := 0
			plugin := func(ts int64, d event.Payload) event.Event {
				n++
				e := prompt
				e.EventID = fmt.Sprintf("01a1fb1e-5318-78a1-a7fd-%012x", n)
				e.Mono += ts - e.T
				e.T, e.Data, e.Source, e.Via, e.Harness, e.SessionKey = ts, d, event.SourceHook, event.ViaPlugin,
					event.HarnessOther, "s_9"
				return e
			}
			all := []event.Event{plugin(hookAt, &event.PromptSubmit{TurnKey: "t_9", LenChars: 30, TypedLenEst: 30,
				OriginDeclared: event.DeclaredNone})}
			// An output every 2 min, so the carry dates its prompt and its
			// latest output at their own times, no further apart than the
			// scorer's reach.
			for k := int64(1); k <= 10; k++ {
				all = append(all, plugin(hookAt+k*2*minuteMS, &event.AgentOutput{TurnKey: "t_9", Words: 30, IsFinal: k == 10}))
			}
			for i := range all {
				all[i].Seq = uint64(i + 1)
			}
			w := &window{gaps: gapsOf(table), ttl: table.Int(params.BUCKET_TTL_S) * 1000,
				late: (table.Int(params.PENDING_WINDOW_S) - table.Int(params.READ_HORIZON_S)) * 1000}
			for i, e := range all {
				w.add(e, uint64(i+1))
			}
			w.slide(hookAt + c.from)
			runUntil := func(t *testing.T, recs []event.Event) int64 {
				t.Helper()
				_, out := score.Step(score.NewState(table, score.Device{}), recs)
				var d score.Derived
				d.Append(out.Final)
				d.Append(out.Pending)
				for _, tu := range d.Turns {
					if tu.TurnKey == "t_9" {
						return tu.RunUntil
					}
				}
				t.Fatal("no turn for the plugin's prompt")
				return 0
			}
			want := runUntil(t, all)
			if last := all[len(all)-1].T; want != last {
				t.Fatalf("one Step over every record runs the turn %d s past its prompt, and the case needs it to its last output, %d s",
					(want-hookAt)/1000, (last-hookAt)/1000)
			}
			if got := runUntil(t, w.records()); got != want {
				t.Errorf("a Step over the carry and the window runs the turn until %d s past its prompt, one Step over every record until %d s",
					(got-hookAt)/1000, (want-hookAt)/1000)
			}
		})
	}
}

// TestOlderPlaceQuestions checks open's rebuild of the carry's question
// state against the window's (#380): a log reader's output that asks a
// question folds in once the log turn's run has ended, which holds the
// window's start while it runs (holdFor); a hook prompt comes nine hours
// after it, inside the window; and a log reader's output dated before the
// question but numbered after the prompt folds in as it comes. The
// window's carry keeps the question for the prompt in the window, and a
// place an agent before E-prompt-class-asked saved, rebuilt from the
// ledger, must be that carry.
func TestOlderPlaceQuestions(t *testing.T) {
	cases := []struct {
		name string
		// hours is how long the session runs: past the log turn's run, which
		// holds the window's start while it runs (holdFor), so its question
		// folds into the carry.
		hours int64
	}{
		{"a place an agent before E-prompt-class-asked saved, rebuilt from the ledger, keeps the question a prompt in the window read, though a log reader's output numbered after the prompt folded in after it", 9},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tmpl := hookTurn(t, hookAt, []int64{minuteMS})
			var start, prompt event.Event
			for _, e := range tmpl {
				switch e.Data.(type) {
				case *event.SessionStart:
					start = e
				case *event.PromptSubmit:
					prompt = e
				}
			}
			n := 0
			clone := func(from event.Event, ts int64, via event.Via, d event.Payload) event.Event {
				n++
				e := from
				e.EventID = fmt.Sprintf("01a1fb1e-5318-78a1-a7f9-%012x", n)
				e.Mono += ts - e.T
				e.T, e.Via, e.Data, e.Source = ts, via, d, event.SourceHook
				if via == event.ViaLog {
					e.Source = event.SourceLog
				}
				return e
			}
			question := clone(prompt, hookAt+2*minuteMS, event.ViaLog, &event.AgentOutput{TurnKey: "t_0", Words: 30, IsFinal: true,
				AsksQuestion: true})
			asked := clone(prompt, hookAt+c.hours*hourMS-5*minuteMS, event.ViaHook, &event.PromptSubmit{TurnKey: "t_1", LenChars: 30,
				TypedLenEst: 30, OriginDeclared: event.DeclaredNone})
			late := clone(prompt, hookAt+minuteMS, event.ViaLog, &event.AgentOutput{TurnKey: "t_0", Words: 30, IsFinal: true})
			evs := []event.Event{clone(start, hookAt-minuteMS, event.ViaHook, start.Data), question, asked}
			m := 0
			for ts := hookAt - 55_000; ts <= hookAt+c.hours*hourMS; ts += minuteMS {
				m++
				idle := float64(600 + (ts-hookAt+55_000)/1000)
				evs = append(evs, sampleAt(tmpl, ts, fmt.Sprintf("01a1fb1e-5318-78a1-a7fb-%012x", m),
					&event.OSPresence{IdleAnyS: idle, IdleKeysS: &idle}))
			}
			slices.SortStableFunc(evs, func(a, b event.Event) int { return cmp.Compare(a.T, b.T) })
			i := slices.IndexFunc(evs, func(e event.Event) bool { return e.EventID == asked.EventID })
			evs = slices.Insert(evs, i+1, late)
			r := newRig(t, score.Device{})
			// A prompt seals at once, without waiting for the seal to finish, so
			// the rig settles after this one: the late output then comes in the
			// next batch, which the window folds it from.
			r.each = func(i int, l *Loop, w *ledger.Writer) {
				if evs[i].EventID == asked.EventID {
					r.settle(l, w)
				}
			}
			r.write(evs, nil)
			kept := r.savedCursor().Carry
			s := kept.Sessions[prompt.SessionKey]
			if s == nil || !slices.ContainsFunc(s.Asks, func(e *event.Event) bool { return e.EventID == question.EventID }) {
				t.Fatalf("the window's carry doesn't keep the question for the prompt in the window, which the case needs:\n%s",
					carryJSON(t, kept))
			}
			olderPlace(t, r, 2, true)
			r.rescore()
			if got := r.savedCursor().Carry; !reflect.DeepEqual(got, kept) {
				t.Errorf("the rebuilt carry is\n%s\nand the window kept\n%s", carryJSON(t, got), carryJSON(t, kept))
			}
		})
	}
}

// TestSamplelessPowerLoop checks the loop against one Step over an evening
// whose sampler loses its idle source before a suspend, so the suspend and
// the morning's resume come with no sample, as the agent writes them when
// its idle read fails, and a turn runs on after the resume
// (D-sampleless-power, #298). Before #298 pkg/score derived no slot
// after the evening's last sample while FinalBefore moved on with the
// morning's hooks, in one Step and in the loop alike, so comparing the two
// passed with the morning's agent time gone. Each case checks that time
// too.
func TestSamplelessPowerLoop(t *testing.T) {
	// hooks is a hooked turn for five hours from the resume, with no sample.
	hooks := func(t *testing.T, _ []event.Event, resume int64) []event.Event {
		var tools []int64
		for m := int64(10); m <= 300; m += 10 {
			tools = append(tools, m*minuteMS)
		}
		var out []event.Event
		for _, e := range rekeyed(hookTurn(t, resume+70_000, tools), 5) {
			if _, sample := e.Data.(*event.OSPresence); !sample {
				out = append(out, e)
			}
		}
		return out
	}
	// back is samples every 15 s for half an hour from ts, with ids of
	// their own under id.
	back := func(first []event.Event, ts int64, id string) []event.Event {
		var out []event.Event
		for k := int64(0); k <= 120; k++ {
			idle := float64(1 + k%4)
			out = append(out, sampleAt(first, ts+k*15_000, fmt.Sprintf("01a1fb1e-5318-78a1-%s-%012x", id, k+1),
				&event.OSPresence{IdleAnyS: idle, IdleKeysS: &idle}))
		}
		return out
	}
	cases := []struct {
		name string
		// suspend and wake are how long after the evening's last sample the
		// machine suspends, an hour when 0, and resumes.
		suspend, wake int64
		// cut puts an os.focus the agent stamps three hours after the last
		// sample, which ends that sample's reach, so the suspend starts the
		// sampler again (SM 2.1), and lock a lock with no sample a minute
		// before the suspend, as a lid close writes them.
		cut, lock bool
		// morning is what comes after the resume, and agent how long after
		// the resume its turn starts, whose minutes have agent time until
		// until after the resume, or up to FinalBefore when until is 0.
		morning      func(t *testing.T, first []event.Event, resume int64) []event.Event
		agent, until int64
	}{
		{name: "after a night's suspend, longer than BUCKET_TTL_S, the loop gives one Step's minutes, and the morning's carry their agent time",
			wake: 9 * hourMS, morning: hooks},
		{name: "after a half-hour nap, the loop gives one Step's minutes, and the morning's carry their agent time",
			wake: 90 * minuteMS, morning: hooks},
		{name: "with a log reader's turn for three hours after the resume, nothing stamped and the samples back after it, the loop gives one Step's minutes, those the late samples decide and those no sample came within BUCKET_TTL_S of alike",
			wake: 9 * hourMS, morning: func(t *testing.T, first []event.Event, resume int64) []event.Event {
				out := logMorning(t, resume+minuteMS, resume+3*hourMS, first[len(first)-1])
				return append(out, back(first, resume+3*hourMS+10*minuteMS, "a7f9")...)
			}},
		{name: "with the last sample's reach cut, a suspend that starts the sampler again and a resume, neither with a sample, then a log reader's turn and samples an hour later, the loop gives one Step's minutes: its window, whose carry drops the samples before the restart, codes the turn's unknown minutes as one Step does",
			suspend: 4 * hourMS, wake: 12 * hourMS, cut: true, agent: 10 * minuteMS,
			morning: func(t *testing.T, first []event.Event, resume int64) []event.Event {
				out := logTurn(t, resume+10*minuteMS, first[len(first)-1])
				return append(out, back(first, resume+hourMS, "a7fa")...)
			}},
		{name: "with the last sample's reach cut and a lid close's lock and suspend, neither with a sample, starting the sampler again, then a resume with none, a log reader's turn and samples an hour later, the loop gives one Step's minutes: its window, whose carry keeps the lock beside the suspend, keeps the screen locked after the resume as one Step does",
			suspend: 4 * hourMS, wake: 12 * hourMS, cut: true, lock: true, agent: 10 * minuteMS,
			morning: func(t *testing.T, first []event.Event, resume int64) []event.Event {
				out := logTurn(t, resume+10*minuteMS, first[len(first)-1])
				return append(out, back(first, resume+hourMS, "a7fc")...)
			}},
		{name: "after that lid close, the samples an hour after its resume, which close its lock, then a nap whose suspend and resume come with no sample, a log reader's turn after it and samples an hour later, the loop gives one Step's minutes: its window's carry leaves out the lock those samples closed, so the minutes after the nap aren't locked",
			suspend: 4 * hourMS, wake: 12 * hourMS, cut: true, lock: true, agent: 10 * minuteMS, until: 2 * hourMS,
			morning: func(t *testing.T, first []event.Event, resume int64) []event.Event {
				out := logTurn(t, resume+10*minuteMS, first[len(first)-1])
				out = append(out, back(first, resume+hourMS, "a7fd")...)
				nap, wake := resume+2*hourMS, resume+11*hourMS
				out = append(out,
					sampleAt(first, nap, "01a1fb1e-5318-78a1-a7fe-000000000001", &event.OSPower{Op: event.PowerSuspend}),
					sampleAt(first, wake, "01a1fb1e-5318-78a1-a7fe-000000000002", &event.OSPower{Op: event.PowerResume}))
				out = append(out, retagged(logTurn(t, wake+10*minuteMS, first[len(first)-1]), "n", "a7f7")...)
				// The samples come back an hour after the nap, the last
				// input ten minutes before the first of them, so one Step
				// takes that slot for present.
				for k := int64(0); k <= 20; k++ {
					idle := float64(600 + 15*k)
					out = append(out, sampleAt(first, wake+hourMS+k*15_000, fmt.Sprintf("01a1fb1e-5318-78a1-a7ff-%012x", k+1),
						&event.OSPresence{IdleAnyS: idle, IdleKeysS: &idle}))
				}
				return out
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			first := hookTurn(t, hookAt, hourTools())
			last := first[len(first)-1].T
			n := 0
			power := func(ts int64, op event.PowerOp) event.Event {
				n++
				return sampleAt(first, ts, fmt.Sprintf("01a1fb1e-5318-78a1-a7f6-%012x", n), &event.OSPower{Op: op})
			}
			resume := last + c.wake
			suspend := c.suspend
			if suspend == 0 {
				suspend = hourMS
			}
			all := slices.Clone(first)
			if c.cut {
				all = append(all, sampleAt(first, last+3*hourMS, "01a1fb1e-5318-78a1-a7fb-000000000001",
					&event.OSFocus{AppClass: event.AppUnknown}))
			}
			if c.lock {
				all = append(all, power(last+suspend-minuteMS, event.PowerLock))
			}
			all = append(all, power(last+suspend, event.PowerSuspend), power(resume, event.PowerResume))
			all = append(all, c.morning(t, first, resume)...)
			r := newRig(t, score.Device{})
			r.write(all, nil)
			chain, err := ledger.ReadChain(r.path("ledger"))
			if err != nil {
				t.Fatal(err)
			}
			want, fb := direct(r.table, r.dev, chain.DeviceID, all)
			from, to := span(all)
			got := r.stored(from, to)
			compare(t, got, want)
			st := r.rescore()
			if st.FinalBefore != fb {
				t.Errorf("the loop's FinalBefore is %s, and one Step's %s", time.UnixMilli(st.FinalBefore).UTC(), time.UnixMilli(fb).UTC())
			}
			if high := all[len(all)-1].T; st.FinalBefore < high-r.table.Int(params.PENDING_WINDOW_S)*1000-event.SlotMS {
				t.Errorf("FinalBefore is %s, and the newest record %s: want PENDING_WINDOW_S between them",
					time.UnixMilli(st.FinalBefore).UTC(), time.UnixMilli(high).UTC())
			}
			stored := map[int64]store.Bucket{}
			for _, b := range got {
				stored[b.At()] = b
			}
			start, end := resume+c.agent, st.FinalBefore
			if c.until > 0 {
				end = min(end, resume+c.until)
			}
			for m := start - floorMod(start, minuteMS) + 2*minuteMS; m+minuteMS <= end; m += minuteMS {
				if b, ok := stored[m]; !ok || b.AgentRunningS == 0 || b.State != event.StateFinal {
					t.Fatalf("%s: the loop's latest revision is %+v; want it final with the morning turn's agent time",
						time.UnixMilli(m).UTC(), b)
				}
			}
		})
	}
}

// carryJSON returns c as JSON, for a failure message.
func carryJSON(t *testing.T, c carry) string {
	t.Helper()
	j, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return string(j)
}

// olderPlace rewrites the scorer's place in the rig's state.json as an
// older agent saved it: for v 0, one before WP-HL1, a carry with no
// stamped, restart_at, lock or v; for v 1, one between WP-HL1's first part
// and D-sampleless-power, such as v0.1.0-alpha.7, a carry with stamped but
// no lock, at v 1, and with restart_at when restart is set. alpha.7's own
// fold keeps restart_at, and its rebuild of an older place, which folded
// it only from the earlier of Power and Presence, finds none on a machine
// with no Presence; for v 2, one between D-sampleless-power and
// E-prompt-class-asked, a carry with lock. No older place's sessions have
// a question state.
func olderPlace(t *testing.T, r *rig, v int, restart bool) {
	t.Helper()
	data, err := os.ReadFile(r.path("state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var keys, cur, carry map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(keys[StateKey], &cur); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(cur["carry"], &carry); err != nil {
		t.Fatal(err)
	}
	if _, ok := carry["stamped"]; !ok {
		t.Fatal("the saved carry holds no stamped record to take out, so the case doesn't load what it means to")
	}
	switch v {
	case 0:
		delete(carry, "stamped")
		delete(carry, "restart_at")
		delete(carry, "lock")
		delete(carry, "v")
	case 1:
		carry["v"] = json.RawMessage("1")
		delete(carry, "lock")
		if !restart {
			delete(carry, "restart_at")
		}
	case 2:
		carry["v"] = json.RawMessage("2")
	default:
		t.Fatalf("no older carry version %d", v)
	}
	if raw, ok := carry["sessions"]; ok {
		var sessions map[string]map[string]json.RawMessage
		if err := json.Unmarshal(raw, &sessions); err != nil {
			t.Fatal(err)
		}
		for _, s := range sessions {
			for _, k := range []string{"asks", "first_native"} {
				delete(s, k)
			}
		}
		if carry["sessions"], err = json.Marshal(sessions); err != nil {
			t.Fatal(err)
		}
	}
	var raw []byte
	if raw, err = json.Marshal(carry); err != nil {
		t.Fatal(err)
	}
	cur["carry"] = raw
	if raw, err = json.Marshal(cur); err != nil {
		t.Fatal(err)
	}
	keys[StateKey] = raw
	if raw, err = json.Marshal(keys); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.path("state.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// savedCursor returns the place state.json holds.
func (r *rig) savedCursor() cursor {
	r.t.Helper()
	data, err := os.ReadFile(r.path("state.json"))
	if err != nil {
		r.t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		r.t.Fatal(err)
	}
	var c cursor
	if raw, ok := keys[StateKey]; ok {
		if err := json.Unmarshal(raw, &c); err != nil {
			r.t.Fatal(err)
		}
	}
	return c
}

// TestHeadlessWindowSlides checks the loop's footprint on a computer whose
// agent can't sample, over twelve hours of agent-day: each hour moves
// FinalBefore on, to PENDING_WINDOW_S behind the latest record, and the
// window's start, scorer.from, follows it Lookback behind, so a Step
// scores the records from there on and not every record since the agent
// started. While the agent's os.focus was the clock, neither moved after
// the first quarter of an hour, and the window grew all day. CI's
// footprint run is too short to show that creep.
func TestHeadlessWindowSlides(t *testing.T) {
	evs := agentDay(t, 0, 12)
	r := newRig(t, score.Device{})
	pending := r.table.Int(params.PENDING_WINDOW_S) * 1000
	lookback := DefaultLookback.Milliseconds()
	type place struct {
		last    int // the index of the latest record written
		cur     cursor
		records int
	}
	var seen []place
	next := evs[0].T - floorMod(evs[0].T, hourMS) + hourMS
	r.each = func(i int, l *Loop, w *ledger.Writer) {
		if i+1 == len(evs) || evs[i+1].T < next {
			return
		}
		r.settle(l, w)
		seen = append(seen, place{last: i, cur: r.savedCursor(), records: l.Status().Records})
		next += hourMS
	}
	r.write(evs, nil)
	if len(seen) != 11 {
		t.Fatalf("%d hourly places, want 11", len(seen))
	}
	for k, p := range seen {
		at := evs[p.last].T
		fb, from := p.cur.FinalBefore, p.cur.From
		if fb < at-pending-event.SlotMS || fb > at-pending {
			t.Errorf("hour %d: FinalBefore is %s, and the latest record %s", k+1, time.UnixMilli(fb).UTC(),
				time.UnixMilli(at).UTC())
		}
		// A turn that ran into the window holds its start at its submit
		// (holdFor), and agent-day's turns run ten minutes at most.
		if from > fb-lookback || from < fb-lookback-15*minuteMS {
			t.Errorf("hour %d: the window starts at %s, and FinalBefore is %s", k+1, time.UnixMilli(from).UTC(),
				time.UnixMilli(fb).UTC())
		}
		in := 0
		for _, e := range evs[:p.last+1] {
			if e.T >= from {
				in++
			}
		}
		if p.records != in {
			t.Errorf("hour %d: the window holds %d records, and %d are from its start on", k+1, p.records, in)
		}
		if k > 0 && (fb <= seen[k-1].cur.FinalBefore || from <= seen[k-1].cur.From) {
			t.Errorf("hour %d: FinalBefore and the window's start didn't move on from hour %d's", k+1, k)
		}
	}
}

// pendingStore is a bucket store that also holds a pending revision of the
// minute at m, which Pending and Latest give as the store's own: a minute
// an earlier loop left pending, of the device whose chain the ledger in
// dir holds. puts holds the revisions of m the loop writes.
type pendingStore struct {
	BucketStore
	dir  string
	m    int64
	puts []store.Bucket
}

func (p *pendingStore) left() store.Bucket {
	chain, err := ledger.ReadChain(p.dir)
	if err != nil {
		panic(err)
	}
	return store.Bucket{V: score.Version(), Params: testTable().Hash(), Minute: time.UnixMilli(p.m).UTC(),
		Device: chain.DeviceID, Slots: make([]event.BucketSlot, minuteMS/event.SlotMS), AgentRunningS: 30,
		State: event.StatePending}
}

func (p *pendingStore) Pending(from int64) ([]store.Bucket, error) {
	bs, err := p.BucketStore.Pending(from)
	if err == nil && p.m >= from {
		bs = append(bs, p.left())
	}
	return bs, err
}

func (p *pendingStore) Latest(ms int64) (store.Bucket, bool, error) {
	if b, ok, err := p.BucketStore.Latest(ms); ok || err != nil || ms != p.m {
		return b, ok, err
	}
	return p.left(), true, nil
}

func (p *pendingStore) Put(b store.Bucket) (store.Bucket, bool, error) {
	if b.At() == p.m {
		p.puts = append(p.puts, b)
	}
	return p.BucketStore.Put(b)
}

// TestNoEmptyFinalUnderived checks what the loop writes for a minute an
// earlier loop left pending with 30 s of agent time: none for one that no
// Step derives, such as a minute whose records the ledger no longer holds,
// so its revision isn't zeroed; and an empty one for a minute a Step
// derives with nothing in it, which supersedes the revision before.
func TestNoEmptyFinalUnderived(t *testing.T) {
	evs := agentDay(t, 0, 2)
	day := evs[0].T - floorMod(evs[0].T, hourMS)
	cases := []struct {
		name string
		m    int64
		// empty says whether the loop writes the minute an empty final
		// revision, else nothing at all.
		empty bool
	}{
		{"a pending minute three hours before every record, which no Step derives, keeps its revision and gets none from the loop",
			day - 3*hourMS, false},
		{"a pending minute of the records' first hour in which no agent runs, which a Step derives with nothing, gets an empty final revision",
			day + 50*minuteMS, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, score.Device{})
			p := &pendingStore{dir: r.path("ledger"), m: c.m}
			r.wrap = func(b BucketStore) BucketStore {
				p.BucketStore = b
				return p
			}
			r.write(evs, nil)
			if n := len(p.puts); !c.empty && n > 0 {
				t.Errorf("the loop wrote %d revisions of the minute, the last %+v; want none", n, p.puts[n-1])
			}
			if !c.empty {
				return
			}
			// What the store keeps, not only what the loop handed it.
			switch got := r.stored(c.m, c.m+minuteMS); {
			case len(got) != 1:
				t.Errorf("the store holds %d revisions of the minute; want its empty final one", len(got))
			case !got[0].Empty() || got[0].State != event.StateFinal:
				t.Errorf("the store's revision of the minute is %+v; want an empty final one", got[0])
			}
		})
	}
}
