// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/timelines"
	"github.com/some-du6e/pairtime/pkg/event"
)

// WP-SC4's oracle for minute buckets is the same as WP-SC2's for credit:
// SM 6's minute rows, through the fixtures' expected.json. These tests build
// each fixture's buckets and check that what a bucket says adds up to its
// rows (docs/PLAN.md, WP-SC4).

// fixtureBuckets returns the bucket of every minute one Step derives from a
// fixture's events in which something was credited or an agent ran, final
// or pending as that Step's FinalBefore says.
func fixtureBuckets(t *testing.T, f *timelines.Fixture) map[int64]event.MinuteBucket {
	t.Helper()
	tab := testTable(t)
	_, out := Step(NewState(tab, fixtureDevice(f)), f.Events)
	var all Derived
	all.Append(out.Final)
	all.Append(out.Pending)
	got := map[int64]event.MinuteBucket{}
	for _, m := range Minutes(all) {
		if m.empty() {
			continue
		}
		got[m.Start] = m.Bucket(BucketInfo{Table: tab, Device: f.Expected.Device,
			State: m.State(m.End() <= out.FinalBefore), Witness: f.Expected.Witness})
	}
	return got
}

func TestFixtureBuckets(t *testing.T) {
	for _, f := range creditCases(t) {
		checkBuckets(t, f, fixtureBuckets(t, f))
	}
}

// checkBuckets checks a fixture's minute buckets, by minute start: each
// passes pkg/event validation, as the device keeps it and as it uploads,
// and each of its expected.json's rows has its buckets' credit by project,
// uncredited seconds and agent time.
func checkBuckets(t *testing.T, f *timelines.Fixture, got map[int64]event.MinuteBucket) {
	t.Helper()
	t.Run(f.Name+": every bucket passes pkg/event validation, as the device keeps it and as it uploads", func(t *testing.T) {
		if len(got) == 0 {
			t.Fatal("no buckets")
		}
		for _, m := range slices.Sorted(maps.Keys(got)) {
			b := got[m]
			if err := b.Validate(); err != nil {
				t.Errorf("%s: %v", b.Minute.Format(time.RFC3339), err)
			}
			if err := b.Shareable().Validate(); err != nil {
				t.Errorf("%s, shareable: %v", b.Minute.Format(time.RFC3339), err)
			}
		}
	})
	for _, row := range f.Expected.Rows {
		name := fmt.Sprintf("%s %s: its buckets' credit by project, uncredited seconds and agent time are the row's", f.Name, row.Local)
		if !row.Bucket {
			name = fmt.Sprintf("%s %s credits nothing and runs no agent, so it has no bucket", f.Name, row.Local)
		}
		t.Run(name, func(t *testing.T) {
			n := 0
			var credited, agent float64
			byProject := map[string]float64{}
			uncredited := map[event.Reason]float64{}
			for m := row.Start.UnixMilli(); m < row.End().UnixMilli(); m += MinuteMS {
				b, ok := got[m]
				if !ok {
					continue
				}
				n++
				credited += b.CreditedS
				agent += b.AgentRunningS
				for _, s := range b.Slots {
					if s.Credited > 0 {
						byProject[s.Project] += s.Credited
					}
				}
				for r, v := range b.Uncredited {
					uncredited[r] += v
				}
			}
			if !row.Bucket {
				if n > 0 {
					t.Errorf("%d minutes have a bucket: %v s credited, %v s of agent time", n, credited, agent)
				}
				return
			}
			if n != row.BucketMinutes {
				t.Errorf("%d minutes with a bucket, want %d", n, row.BucketMinutes)
			}
			want := map[string]float64{}
			var total float64
			for p, rs := range row.Credit {
				for _, s := range rs {
					want[p] += s
					total += s
				}
			}
			if !sameSeconds(byProject, want) {
				t.Errorf("credit by project %v, want %v (%s)", byProject, want, row.SM)
			}
			if credited != total {
				t.Errorf("credited_s %v, want %v", credited, total)
			}
			if row.Uncredited != nil && !sameSeconds(uncredited, row.Uncredited) {
				t.Errorf("uncredited %v, want %v (%s)", uncredited, row.Uncredited, row.SM)
			}
			if agent != row.AgentRunningS {
				t.Errorf("agent_running_s %v, want %v", agent, row.AgentRunningS)
			}
		})
	}
}

// TestOneProjectPerSlot checks what a bucket slot's single p rests on: no
// slot credits two projects (SM 3.1, 3.3 rule 1), so p holds all of the
// slot's credit.
func TestOneProjectPerSlot(t *testing.T) {
	for _, f := range creditCases(t) {
		t.Run(f.Name+": no slot credits more than one project", func(t *testing.T) {
			for _, c := range scoreAll(t, fixtureDevice(f), f.Events).Credits {
				ps := map[string]bool{}
				for _, e := range c.Credited {
					if e.Halves > 0 {
						ps[e.ProjectKey] = true
					}
				}
				if len(ps) > 1 {
					t.Errorf("slot %s credits %v", time.UnixMilli(c.Slot).UTC().Format(time.RFC3339), slices.Sorted(maps.Keys(ps)))
				}
			}
		})
	}
}

// Anchor ids for the synthetic slots below.
const (
	idA = "019a0000-0000-7000-8000-0000000000a1"
	idB = "019a0000-0000-7000-8000-0000000000b2"
)

// credOf is seconds credited at weight 1 to project p under reason r at
// level l, behind anchor idA.
func credOf(p string, r event.Reason, l event.Evidence, seconds int64, labels ...event.Label) Credited {
	return Credited{ProjectKey: p, Reason: r, Level: l, Seconds: seconds, Halves: 2 * seconds, AnchorIDs: []string{idA},
		Labels: labels}
}

// slotOf is the slot at base with the given credit.
func slotOf(es ...Credited) MinuteSlot { return MinuteSlot{Credit: Credit{Slot: base, Credited: es}} }

func TestBucketSlot(t *testing.T) {
	const lA, lB, lC = event.EvidenceA, event.EvidenceB, event.EvidenceC
	half := credOf("p_1", rP, lA, 15)
	half.Halves = 15
	nothing := MinuteSlot{Credit: Credit{Slot: base, Uncredited: map[event.Reason]int64{xAbsent: 15}}}
	cases := []struct {
		name       string
		slot       MinuteSlot
		backfilled bool
		want       event.BucketSlot
	}{
		{"a slot credited under two reasons names the one with more credit, and counts all of it",
			slotOf(credOf("p_1", rP, lA, 9), credOf("p_1", rSUP, lA, 6)), false,
			event.BucketSlot{Project: "p_1", Credited: 15, Why: rP, Kind: event.ActivityPrompting, Ev: lA}},
		{"on a tie the reason earlier in SM 5.2's table names the slot",
			slotOf(credOf("p_1", rREAD, lA, 7), credOf("p_1", rP, lA, 7)), false,
			event.BucketSlot{Project: "p_1", Credited: 14, Why: rP, Kind: event.ActivityPrompting, Ev: lA}},
		{"ev is the weakest level among the slot's credited seconds",
			slotOf(credOf("p_1", rP, lA, 10), credOf("p_1", rREAD, lC, 5)), false,
			event.BucketSlot{Project: "p_1", Credited: 15, Why: rP, Kind: event.ActivityPrompting, Ev: lC}},
		{"a level-B second makes a slot of level-A seconds level B",
			slotOf(credOf("p_1", rP, lA, 14), credOf("p_1", rP, lB, 1)), false,
			event.BucketSlot{Project: "p_1", Credited: 15, Why: rP, Kind: event.ActivityPrompting, Ev: lB}},
		{"in a backfilled minute every credited slot is level C (DESIGN 6.4)",
			slotOf(credOf("p_1", rP, lA, 15)), true,
			event.BucketSlot{Project: "p_1", Credited: 15, Why: rP, Kind: event.ActivityPrompting, Ev: lC}},
		{"lbl lists pkg/event's slot labels in their order and leaves out an anchor's own",
			slotOf(credOf("p_1", rP, lC, 15, event.LabelTypingModule, event.LabelViaBridge, event.LabelViaTrack)), false,
			event.BucketSlot{Project: "p_1", Credited: 15, Why: rP, Kind: event.ActivityPrompting, Ev: lC,
				Lbl: []event.Label{event.LabelViaTrack, event.LabelViaBridge}}},
		{"focus_unknown reaches lbl after the side-door labels, as event.SlotLabels orders them, and its level-C seconds make the slot C (SM 2.5)",
			slotOf(credOf("p_1", rP, lA, 10), credOf("p_1", rREAD, lC, 5, event.LabelFocusUnknown, event.LabelViaPlugin)), false,
			event.BucketSlot{Project: "p_1", Credited: 15, Why: rP, Kind: event.ActivityPrompting, Ev: lC,
				Lbl: []event.Label{event.LabelViaPlugin, event.LabelFocusUnknown}}},
		{"a slot nothing credited is empty, whatever its uncredited seconds say", nothing, false, event.BucketSlot{}},
		{"a half-weighted second credits half a second (SM 4.3)", slotOf(half), false,
			event.BucketSlot{Project: "p_1", Credited: 7.5, Why: rP, Kind: event.ActivityPrompting, Ev: lA}},
		{"a returned second keeps its ~r reason, with the activity of its base",
			slotOf(credOf("p_1", rREAD+event.ReturnedSuffix, lB, 15)), false,
			event.BucketSlot{Project: "p_1", Credited: 15, Why: rREAD + event.ReturnedSuffix, Kind: event.ActivityReadingOutput, Ev: lB}},
		{"the project with the most credit names the slot",
			slotOf(credOf("p_b", rP, lA, 8), credOf("p_a", rP, lA, 7)), false,
			event.BucketSlot{Project: "p_b", Credited: 15, Why: rP, Kind: event.ActivityPrompting, Ev: lA}},
		{"on a tie the lower project key names the slot (SM 3.3 rule 6)",
			slotOf(credOf("p_b", rP, lA, 7), credOf("p_a", rP, lA, 7)), false,
			event.BucketSlot{Project: "p_a", Credited: 14, Why: rP, Kind: event.ActivityPrompting, Ev: lA}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.slot.bucketSlot(c.backfilled); !reflect.DeepEqual(got, c.want) {
				t.Errorf("slot %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestMinuteState(t *testing.T) {
	half := credOf("p_1", rP, event.EvidenceA, 15)
	half.Halves = 15
	restored := credOf("p_1", rP+event.RestoredSuffix, event.EvidenceA, 15)
	minute := func(es ...Credited) Minute { return Minute{Start: base, Slots: []MinuteSlot{slotOf(es...)}} }
	cases := []struct {
		name  string
		m     Minute
		final bool
		want  event.BucketState
	}{
		{"a minute inside the pending window is pending (DESIGN 6.2 D1)", minute(credOf("p_1", rP, event.EvidenceA, 15)), false, event.StatePending},
		{"a minute past the pending window is final", minute(credOf("p_1", rP, event.EvidenceA, 15)), true, event.StateFinal},
		{"a final minute holding half-weighted seconds is provisional (SM 4.3)", minute(half), true, event.StateProvisional},
		{"a final minute whose half-weighted seconds a progress event restored is restored", minute(restored), true, event.StateRestored},
		{"a minute still holding half-weighted seconds is provisional though others were restored", minute(restored, half), true, event.StateProvisional},
		{"a pending minute is pending whatever its weights", minute(half), false, event.StatePending},
		{"a final minute with nothing credited is final", Minute{Start: base}, true, event.StateFinal},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.m.State(c.final); got != c.want {
				t.Errorf("state %q, want %q", got, c.want)
			}
		})
	}
}

// TestMinuteBucket checks what a bucket sums over its slots, on a minute
// whose first and third slots the scorer didn't derive.
func TestMinuteBucket(t *testing.T) {
	tab := testTable(t)
	p := credOf("p_1", rP, event.EvidenceA, 15)
	p.AnchorIDs = []string{idB, idA}
	read := credOf("p_1", rREAD, event.EvidenceA, 10)
	m := Minute{Start: base, Slots: []MinuteSlot{
		{Credit: Credit{Slot: at(15), Credited: []Credited{p}, AgentS: 10, AgentOnly: map[string]int64{"p_2": 10, "p_3": 0}}},
		{Credit: Credit{Slot: at(45), Credited: []Credited{read}, Uncredited: map[event.Reason]int64{xNoAnch: 5}, AgentS: 15,
			AgentOnly: map[string]int64{"p_2": 5}}},
	}}
	b := m.Bucket(BucketInfo{Table: tab, Device: "d_test", State: event.StatePending, Witness: event.Witnessed, Rev: 3})
	cases := []struct {
		name string
		ok   bool
	}{
		{"slots the scorer didn't derive are empty", reflect.DeepEqual(b.Slots[0], event.BucketSlot{}) &&
			reflect.DeepEqual(b.Slots[2], event.BucketSlot{})},
		{"each derived slot is in its place", b.Slots[1].Credited == 15 && b.Slots[3].Credited == 10},
		{"credited_s sums the slots", b.CreditedS == 25},
		{"uncredited sums the slots by reason", reflect.DeepEqual(b.Uncredited, map[event.Reason]float64{xNoAnch: 5})},
		{"agent_running_s sums the slots", b.AgentRunningS == 25},
		{"agent_only sums the slots' agent-only time by project and leaves out a project with none (D-bucket-agent-only)",
			reflect.DeepEqual(b.AgentOnly, map[string]float64{"p_2": 15})},
		{"anchors lists each anchor behind the credit once, sorted", slices.Equal(b.Anchors, []string{idA, idB})},
		{"v is pkg/score's VERSION and params the table's hash (SM 7.6)", b.V == Version() && b.Params == tab.Hash()},
		{"minute is the minute's start in UTC", b.Minute.Equal(time.UnixMilli(base)) && b.Minute.Location() == time.UTC},
		{"device, state, witness and rev are the ones given", b.Device == "d_test" && b.State == event.StatePending &&
			b.Witness == event.Witnessed && b.Rev == 3},
		{"the bucket passes pkg/event validation", b.Validate() == nil},
		{"it uploads without its anchor ids (DESIGN 6.2 D9)", b.Shareable().Anchors == nil && b.Shareable().Validate() == nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Errorf("bucket %+v", b)
			}
		})
	}
	t.Run("pkg/score/VERSION is a scoring version", func(t *testing.T) {
		if err := (event.MinuteBucket{V: Version(), Params: tab.Hash(), Minute: time.UnixMilli(base).UTC(), Device: "d_test",
			Slots: make([]event.BucketSlot, 4), State: event.StateFinal, Witness: event.Witnessed}).Validate(); err != nil {
			t.Errorf("version %q: %v", Version(), err)
		}
	})
}

// TestBucketAgentOnly checks each fixture's buckets' agent_only against the
// per-project agent_only_s its expected.json states (D-bucket-agent-only,
// SM 2.6's chess clock): summed over the window, each project's agent_only
// in the last revision of each minute is the seconds a turn of it ran that
// weren't credited to it. SM 6.3 states them for 14:00 to 14:30: 570 s for
// p_0e6a8e0b849e, 180 s for p_a73dbaeaf36c and none for p_418d8413d1cf.
func TestBucketAgentOnly(t *testing.T) {
	sm63 := map[string]float64{"p_0e6a8e0b849e": 570, "p_a73dbaeaf36c": 180, "p_418d8413d1cf": 0}
	checked := false
	for _, f := range creditCases(t) {
		want := map[string]float64{}
		for k, v := range f.Expected.Totals.Other {
			if p, ok := strings.CutPrefix(k, "agent_only_s."); ok {
				want[p] = v
			}
		}
		if len(want) == 0 {
			continue
		}
		if f.Name == "6.3" {
			checked = true
			if !maps.Equal(want, sm63) {
				t.Errorf("6.3's expected.json states agent_only_s %v, want SM 6.3's %v", want, sm63)
			}
		}
		from, to := f.Expected.Window.Start.UnixMilli(), f.Expected.Window.End.UnixMilli()
		got := map[string]float64{}
		last := lastRevisions(stepRevisions(t, fixtureDevice(f), f.Expected.Device, f.Expected.Witness, arrivals(f.Events, nil)))
		for _, m := range slices.Sorted(maps.Keys(last)) {
			if m < from || m >= to {
				continue
			}
			for p, n := range last[m].AgentOnly {
				if n == 0 {
					t.Errorf("%s: the bucket of %s lists %s with no agent-only time; agent_only leaves such a project out", f.Name,
						minuteName(m), p)
				}
				got[p] += n
			}
		}
		for _, p := range slices.Sorted(maps.Keys(want)) {
			t.Run(fmt.Sprintf("%s: %s's agent_only sums to %v s over %s, as its expected.json says (D-bucket-agent-only)", f.Name, p,
				want[p], f.Expected.Window.Local), func(t *testing.T) {
				if got[p] != want[p] {
					t.Errorf("%v s, want %v s", got[p], want[p])
				}
			})
		}
	}
	if !checked {
		t.Error("6.3 isn't among the credit fixtures, so SM 6.3's agent_only_s went unchecked")
	}
}

func TestMinutes(t *testing.T) {
	d := Derived{Credits: []Credit{{Slot: at(60), ProjectKey: "p_2"}, {Slot: at(45), ProjectKey: "p_1"},
		{Slot: at(60), ProjectKey: "p_2"}}}
	ms := Minutes(d)
	cases := []struct {
		name string
		ok   bool
	}{
		{"credit slots are grouped by UTC minute, in time order", len(ms) == 2 && ms[0].Start == base && ms[1].Start == at(60)},
		{"each slot keeps the project its seconds go to", len(ms) == 2 && ms[0].Slots[0].ProjectKey == "p_1" &&
			ms[1].Slots[0].ProjectKey == "p_2"},
		{"a slot listed twice is kept once", len(ms) == 2 && len(ms[1].Slots) == 1},
		{"a minute ends a minute after it starts", len(ms) == 2 && ms[0].End() == at(60)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Errorf("minutes %+v", ms)
			}
		})
	}
}
