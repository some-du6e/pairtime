// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
)

// arrivals groups records into the batches a Step a minute takes, by the
// minute of their t, as scoreByMinute does. late moves a record, by event
// id, that many minutes later.
func arrivals(records []event.Event, late map[string]int64) [][]event.Event {
	by := map[int64][]event.Event{}
	for _, e := range records {
		m := floorMinute(e.T)/MinuteMS + late[e.EventID]
		by[m] = append(by[m], e)
	}
	var out [][]event.Event
	for _, m := range slices.Sorted(maps.Keys(by)) {
		out = append(out, by[m])
	}
	return out
}

// stepRevisions steps the scorer once per batch, as the agent does each
// minute, hands every Output to one Revisions, and returns the revisions it
// writes, in order.
func stepRevisions(t *testing.T, dev Device, device string, w event.Witness, batches [][]event.Event) []Revision {
	t.Helper()
	tab := testTable(t)
	s := NewState(tab, dev)
	rv := NewRevisions(tab, device, func(int64) event.Witness { return w })
	var out []Revision
	for _, b := range batches {
		var o Output
		s, o = Step(s, b)
		out = append(out, rv.Update(o)...)
	}
	return out
}

// lastRevisions returns the last revision written for each minute.
func lastRevisions(revs []Revision) map[int64]event.MinuteBucket {
	out := map[int64]event.MinuteBucket{}
	for _, r := range revs {
		out[r.Bucket.Minute.UnixMilli()] = r.Bucket
	}
	return out
}

func minuteName(m int64) string { return time.UnixMilli(m).UTC().Format(time.RFC3339) }

// TestFixtureRevisions steps each fixture a minute at a time through
// Revisions, as the agent does, and checks the revisions it writes.
func TestFixtureRevisions(t *testing.T) {
	for _, f := range creditCases(t) {
		revs := stepRevisions(t, fixtureDevice(f), f.Expected.Device, f.Expected.Witness, arrivals(f.Events, nil))
		t.Run(f.Name+": every revision is a valid bucket, and each minute's revs count up from 0 by one", func(t *testing.T) {
			next := map[int64]uint32{}
			for _, r := range revs {
				m := r.Bucket.Minute.UnixMilli()
				if err := r.Bucket.Validate(); err != nil {
					t.Errorf("%s rev %d: %v", minuteName(m), r.Bucket.Rev, err)
				}
				if r.Bucket.Rev != next[m] {
					t.Errorf("%s: rev %d, want %d", minuteName(m), r.Bucket.Rev, next[m])
				}
				next[m] = r.Bucket.Rev + 1
				if r.Minute.Start != m {
					t.Errorf("%s: the revision's minute starts at %s", minuteName(m), minuteName(r.Minute.Start))
				}
			}
		})
		t.Run(f.Name+": a minute is written once it isn't pending, and never again", func(t *testing.T) {
			done := map[int64]bool{}
			for _, r := range revs {
				m := r.Bucket.Minute.UnixMilli()
				if done[m] {
					t.Errorf("%s: rev %d written after the minute was %s", minuteName(m), r.Bucket.Rev, r.Bucket.State)
				}
				if r.Bucket.State != event.StatePending {
					done[m] = true
				}
			}
		})
		t.Run(f.Name+": each minute's last revision is the bucket one Step over all the records gives", func(t *testing.T) {
			want := fixtureBuckets(t, f)
			last := lastRevisions(revs)
			for _, m := range slices.Sorted(maps.Keys(want)) {
				got, ok := last[m]
				if !ok {
					t.Errorf("%s: no revision, want %+v", minuteName(m), want[m])
					continue
				}
				if !sameBucket(got, want[m]) {
					t.Errorf("%s: last revision %+v, want %+v", minuteName(m), got, want[m])
				}
			}
			for _, m := range slices.Sorted(maps.Keys(last)) {
				if b := last[m]; want[m].V == "" && (b.CreditedS != 0 || b.AgentRunningS != 0) {
					t.Errorf("%s: its last revision has %v s credited and %v s of agent time, but one Step gives it no bucket",
						minuteName(m), b.CreditedS, b.AgentRunningS)
				}
			}
		})
	}
}

// TestLateRecordRevision is WP-SC4's revision check: a record that arrives
// late, inside the pending window, raises rev by exactly one on exactly one
// minute and leaves every final bucket as it would have been (DESIGN 3.7,
// 6.2 D1).
func TestLateRecordRevision(t *testing.T) {
	b := newTL(false)
	b.focus(at(0), event.AppEditor, "p_1")
	b.mouse(every(at(1), at(1796), 5000)...)
	// A typed edit at 310 s, whose EDIT_LEAD_S lead covers 190 s to 310 s,
	// and a human commit at 360 s, whose COMMIT_LEAD_S lead takes the rest
	// of the minute from 300 s: the lead of the next act wins (SM 3.3 rule
	// 3), so the commit changes only that minute.
	b.editor(at(310), event.ViaEditor, "p_1", event.ActEdit, event.EditSourceHuman)
	commit := b.git(at(360), "p_1", event.ActorHuman)
	b.samples(at(0), at(1800))
	recs := b.records()
	inOrder := lastRevisions(stepRevisions(t, Device{}, "d_test", event.Witnessed, arrivals(recs, nil)))
	// The commit's record reaches the agent five minutes after its time,
	// as an inbox drained after an agent restart would.
	late := lastRevisions(stepRevisions(t, Device{}, "d_test", event.Witnessed, arrivals(recs, map[string]int64{commit.EventID: 5})))
	t.Run("the late record leaves every minute's last bucket as it would have been", func(t *testing.T) {
		if len(late) != len(inOrder) {
			t.Errorf("%d minutes have buckets, want %d", len(late), len(inOrder))
		}
		for _, m := range slices.Sorted(maps.Keys(inOrder)) {
			if !sameBucket(late[m], inOrder[m]) {
				t.Errorf("%s: %+v, want %+v", minuteName(m), late[m], inOrder[m])
			}
		}
	})
	t.Run("it raises rev by exactly one on exactly one minute, the one it changed", func(t *testing.T) {
		var bumped []int64
		for _, m := range slices.Sorted(maps.Keys(inOrder)) {
			switch got, want := late[m].Rev, inOrder[m].Rev; {
			case got == want+1:
				bumped = append(bumped, m)
			case got != want:
				t.Errorf("%s: rev %d, want %d or %d", minuteName(m), got, want, want+1)
			}
		}
		if !slices.Equal(bumped, []int64{at(300)}) {
			t.Errorf("revs raised on %v, want only the minute at 300 s", bumped)
		}
	})
	t.Run("the minute it changed credits the commit's lead and is final", func(t *testing.T) {
		m := late[at(300)]
		if m.State != event.StateFinal || m.CreditedS != 60 || !slices.Contains(m.Anchors, commit.EventID) {
			t.Errorf("minute at 300 s: %+v, want 60 s credited behind the commit, final", m)
		}
	})
}

// TestRevisionsSeed checks the agent's restart: Revisions seeded with the
// revisions already written writes nothing for a rebuild that derives the
// same, and continues a changed minute's revs from the one written.
func TestRevisionsSeed(t *testing.T) {
	f := fixture(t, "6.2")
	tab := testTable(t)
	witness := func(int64) event.Witness { return f.Expected.Witness }
	revs := stepRevisions(t, fixtureDevice(f), f.Expected.Device, f.Expected.Witness, arrivals(f.Events, nil))
	var written []event.MinuteBucket
	for _, r := range revs {
		written = append(written, r.Bucket)
	}
	_, out := Step(NewState(tab, fixtureDevice(f)), f.Events)
	t.Run("a rebuild from one Step over the ledger writes nothing new", func(t *testing.T) {
		rv := NewRevisions(tab, f.Expected.Device, witness)
		rv.Seed(written)
		if got := rv.Update(out); len(got) != 0 {
			t.Errorf("%d revisions, want none: %+v", len(got), got)
		}
	})
	t.Run("a minute that differs from the one written gets the next rev", func(t *testing.T) {
		last := lastRevisions(revs)
		changed := slices.Sorted(maps.Keys(last))[0]
		seed := slices.Clone(written)
		for i := range seed {
			if seed[i].Minute.UnixMilli() == changed {
				seed[i].AgentRunningS++
			}
		}
		rv := NewRevisions(tab, f.Expected.Device, witness)
		rv.Seed(seed)
		got := rv.Update(out)
		if len(got) != 1 || got[0].Bucket.Minute.UnixMilli() != changed || got[0].Bucket.Rev != last[changed].Rev+1 {
			t.Errorf("revisions %+v, want one of the minute at %s with rev %d", got, minuteName(changed), last[changed].Rev+1)
		}
	})
}

// TestRevisionRetracts checks that a minute whose bucket later has nothing
// in it gets a revision saying so, rather than keeping the one written.
func TestRevisionRetracts(t *testing.T) {
	b := newTL(false)
	terminalSession(b)
	b.press(every(atMS(30_500), atMS(59_500), 1000)...)
	b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 30)
	b.agentSays(at(70), "s_1", "t_1", 10)
	stop := b.harness(at(100), event.ViaHook, "s_1", "p_1", &event.AgentStop{TurnKey: "t_1", StopReason: event.StopEndTurn})
	b.samples(at(0), at(300))
	// The stop reaches the agent two minutes late. Until then the hooked
	// turn runs up to now, so the minute at 120 s has agent time, which the
	// stop takes away.
	revs := stepRevisions(t, Device{}, "d_test", event.Witnessed, arrivals(b.records(), map[string]int64{stop.EventID: 2}))
	var minute2 []event.MinuteBucket
	for _, r := range revs {
		if r.Bucket.Minute.UnixMilli() == at(120) {
			minute2 = append(minute2, r.Bucket)
		}
	}
	if len(minute2) != 2 {
		t.Fatalf("%d revisions of the minute at 120 s, want 2: %+v", len(minute2), minute2)
	}
	cases := []struct {
		name string
		ok   bool
	}{
		{"the first revision has the agent time the running turn gave it", minute2[0].Rev == 0 && minute2[0].AgentRunningS > 0},
		{"the next says the minute has nothing, with rev 1", minute2[1].Rev == 1 && minute2[1].CreditedS == 0 &&
			minute2[1].AgentRunningS == 0 && minute2[1].Validate() == nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Errorf("revisions %+v", minute2)
			}
		})
	}
}
