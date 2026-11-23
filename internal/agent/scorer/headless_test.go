// SPDX-License-Identifier: AGPL-3.0-or-later

package scorer

import (
	"encoding/json"
	"fmt"
	"os"
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
	cases := []struct {
		name string
		// runs are the hours of agent-day that each run of the agent
		// writes, each with a loop of its own, as after a restart.
		runs [][2]int64
	}{
		{"a computer whose only OS record is the os.focus its agent writes as it starts keeps finalizing minutes for hours, past Lookback, as one Step over every record gives them",
			[][2]int64{{0, 9}}},
		{"a restart after a stall longer than BUCKET_TTL_S derives every minute since the last final, the ones still pending when the agent stopped included",
			[][2]int64{{0, 1}, {4, 5}}},
		{"a restart between two hours of turns keeps finalizing as one run does",
			[][2]int64{{0, 3}, {3, 6}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, score.Device{})
			var all []event.Event
			for _, h := range c.runs {
				evs := agentDay(t, h[0], h[1])
				r.write(evs, nil)
				all = append(all, evs...)
			}
			chain, err := ledger.ReadChain(r.path("ledger"))
			if err != nil {
				t.Fatal(err)
			}
			want, fb := direct(r.table, r.dev, chain.DeviceID, all)
			from, to := span(all)
			compare(t, r.stored(from, to), want)
			if st := r.rescore(); st.FinalBefore != fb {
				t.Errorf("the loop's FinalBefore is %s, and one Step's %s", time.UnixMilli(st.FinalBefore).UTC(),
					time.UnixMilli(fb).UTC())
			}
		})
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
