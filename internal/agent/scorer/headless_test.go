// SPDX-License-Identifier: AGPL-3.0-or-later

package scorer

import (
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
		// the minutes before them are still pending. In those minutes falls
		// the lead of a message sent from T3 Code's web client while the
		// machine was away, which its server runs once it is back (relayedAt):
		// a slot the machine slept through refuses the lead, and a
		// never-sampling device's slot would realize it, so the minute has
		// credit only if the carry wrongly drops the sampler's last record.
		{"a sampler silence longer than Lookback that ends with a sample before any record the agent stamps, as when the agent starts again ten hours later, keeps the carried sample's reach: the hours between are asleep, as one Step reads them, and refuse a relayed lead",
			func(t *testing.T) [][]event.Event {
				first := hookTurn(t, hookAt, hourTools())
				again := hookAt + 10*hourMS
				second := rekeyed(hookTurn(t, again+70_000, hourTools()), 1)
				idle := 585.0
				woke := sampleAt(first, again, "01a1fb1e-5318-78a1-a7f0-000000000001",
					&event.OSPresence{IdleAnyS: idle, IdleKeysS: &idle})
				run := append([]event.Event{woke}, relayedAt(t, again-30_000, again)...)
				return [][]event.Event{first, append(run, second...)}
			}},
		{"a suspend longer than Lookback keeps the carried suspend's reach once the window slides past it: the ten hours the machine slept are asleep, as one Step reads them, and refuse a relayed lead",
			func(t *testing.T) [][]event.Event {
				first := hookTurn(t, hookAt, hourTools())
				last := first[len(first)-1].T
				asleep := sampleAt(first, last+15_000, "01a1fb1e-5318-78a1-a7f0-000000000002",
					&event.OSPower{Op: event.PowerSuspend})
				resume := last + 15_000 + 10*hourMS
				woke := sampleAt(first, resume, "01a1fb1e-5318-78a1-a7f0-000000000003",
					&event.OSPower{Op: event.PowerResume})
				second := rekeyed(hookTurn(t, resume+70_000, hourTools()), 2)
				run := append(append(first, asleep, woke), relayedAt(t, resume-30_000, resume)...)
				return [][]event.Event{append(run, second...)}
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

// relayedAt returns testdata/timelines/relayed/t3-away's first message as
// records of a session T3 Code's server starts here: the session's start,
// its prompt and the relay.message that pairs with it, written a second,
// three and five seconds after at, with the message stamped at sent. So a
// message sent before at, while the machine was away, gets its relayed lead
// before at, which a slot the machine slept through refuses (SM 2.3).
func relayedAt(t *testing.T, sent, at int64) []event.Event {
	t.Helper()
	root, err := timelines.Root()
	if err != nil {
		t.Fatal(err)
	}
	f, err := timelines.Load(root, "relayed/t3-away")
	if err != nil {
		t.Fatal(err)
	}
	var start, prompt, relay *event.Event
	for i := range f.Events {
		e := &f.Events[i]
		switch d := e.Data.(type) {
		case *event.SessionStart:
			start = cmpOr(start, e)
		case *event.RelayMessage:
			if relay == nil && d.Prompt != "" {
				relay = e
			}
		}
	}
	for i := range f.Events {
		if relay != nil && f.Events[i].EventID == relay.Data.(*event.RelayMessage).Prompt {
			prompt = &f.Events[i]
		}
	}
	if start == nil || prompt == nil || relay == nil {
		t.Fatal("t3-away has no session start, or no message that names its prompt")
	}
	clone := func(tmpl *event.Event, ts int64, n int) event.Event {
		e := *tmpl
		e.EventID = fmt.Sprintf("01a1fb1e-5318-78a1-a7f2-%012x", n)
		e.Mono += ts - e.T
		e.T = ts
		return e
	}
	s, p, r := clone(start, at+1000, 1), clone(prompt, at+3000, 2), clone(relay, at+5000, 3)
	m := *relay.Data.(*event.RelayMessage)
	m.SentAt, m.Prompt = sent, p.EventID
	r.Data = &m
	return []event.Event{s, p, r}
}

// TestCarryReach checks the carry against pkg/score's reach rule (SM 2.1):
// the window drops a carried record of the presence sampler's only when a
// record the agent stamps itself, dated more than BUCKET_TTL_S after it,
// comes before the sampler's next record, whether the carry folded that
// record or the window holds it (window.cutBy), and only once the newest
// record is PENDING_WINDOW_S - READ_HORIZON_S past it. A Step over the
// carry and the window then derives every slot from the window's start as
// one Step over every record does: asleep through a silence or a suspend
// longer than Lookback that ends with a record of the sampler's, or while a
// cut still waits; a never-sampling device's after a stamped record cut the
// reach, with no gap from before the stretch in the slot of the record
// that starts the sampler again, an unlock and the sample at its time
// included. TestHeadlessLoop compares minute buckets, and a minute with no
// credit and no agent time has none, which is why its carry cases need a
// relayed lead to tell those slots apart.
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
	}{
		{"a sample after a silence longer than Lookback, before any record the agent stamps, keeps the carried sample's reach: the silence is asleep",
			woken(again), again - 15*minuteMS, true},
		{"a resume after a ten-hour suspend, before any record the agent stamps, keeps the carried suspend's reach: the machine slept",
			append([]event.Event{rec(last+15_000, &event.OSPower{Op: event.PowerSuspend}),
				rec(resume, &event.OSPower{Op: event.PowerResume})}, woken(resume+15_000)...),
			resume - 15*minuteMS, true},
		{"a record the agent stamps BUCKET_TTL_S after the last sample, in the window before the sampler's next record, ends the carried sample's reach",
			append([]event.Event{cut}, woken(again)...), last + 2*hourMS, false},
		{"the same record folded into the carry ends the carried sample's reach too",
			append([]event.Event{cut}, woken(again)...), last + 4*hourMS, false},
		{"an unlock that starts the sampler again after the cut, with the sample the agent takes at its time, keeps the gap from before the stretch out of that slot",
			append([]event.Event{cut, rec(again, &event.OSPower{Op: event.PowerUnlock})}, woken(again)...), last + 2*hourMS, false},
		{"a stamped record before the window's first sample that the newest record isn't yet rules.late past leaves the carried sample's reach to wait",
			[]event.Event{rec(again-60_000, &event.OSFocus{AppClass: event.AppUnknown}),
				rec(again, idle(600))},
			again - 15*minuteMS, true},
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
			switch n := len(p.puts); {
			case !c.empty && n > 0:
				t.Errorf("the loop wrote %d revisions of the minute, the last %+v; want none", n, p.puts[n-1])
			case c.empty && n == 0:
				t.Error("the loop wrote no revision of the minute; want an empty final one")
			case c.empty:
				if last := p.puts[n-1]; !last.Empty() || last.State != event.StateFinal {
					t.Errorf("the minute's last revision is %+v; want an empty final one", last)
				}
			}
		})
	}
}
