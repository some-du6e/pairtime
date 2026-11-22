// SPDX-License-Identifier: AGPL-3.0-or-later

package scorer

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/timelines"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/score"
)

// creditEntry is one credit entry of a slot: halves half-seconds of
// project p under reason r at level ev, behind one anchor.
func creditEntry(p string, r event.Reason, ev event.Evidence, halves int64, labels ...event.Label) score.Credited {
	return score.Credited{ProjectKey: p, Reason: r, Level: ev, Seconds: halves / 2, Halves: halves,
		AnchorIDs: []string{"0190f4b2-5a1c-7d3e-8f00-123456789abc"}, Labels: labels}
}

func TestSlotSummary(t *testing.T) {
	const slot = 1795046505000 // 2026-11-19T00:01:45Z, a slot boundary
	cases := []struct {
		name    string
		credits []score.Credited
		want    event.BucketSlot
	}{
		{name: "a slot nothing credited is empty", want: event.BucketSlot{}},
		{
			name: "a slot credited at level A and at level C is level C, its weakest second's, with the larger entry's project, reason and activity (DESIGN 6.4)",
			credits: []score.Credited{
				creditEntry("p_9f2c", event.ReasonPrompt, event.EvidenceA, 20),
				creditEntry("p_9f2c", event.ReasonRead, event.EvidenceC, 10),
			},
			want: event.BucketSlot{Project: "p_9f2c", Credited: 15, Why: event.ReasonPrompt, Kind: event.ActivityPrompting, Ev: event.EvidenceC},
		},
		{
			name: "the larger entry names the reason even when the smaller one is the weaker level",
			credits: []score.Credited{
				creditEntry("p_9f2c", event.ReasonPrompt, event.EvidenceB, 4),
				creditEntry("p_9f2c", event.ReasonRead, event.EvidenceA, 20),
			},
			want: event.BucketSlot{Project: "p_9f2c", Credited: 12, Why: event.ReasonRead, Kind: event.ActivityReadingOutput, Ev: event.EvidenceB},
		},
		{
			name: "an entry that earned nothing adds neither its level nor its labels",
			credits: []score.Credited{
				creditEntry("p_9f2c", event.ReasonPrompt, event.EvidenceA, 30),
				creditEntry("p_9f2c", event.ReasonRead, event.EvidenceC, 0, event.LabelViaTrack),
			},
			want: event.BucketSlot{Project: "p_9f2c", Credited: 15, Why: event.ReasonPrompt, Kind: event.ActivityPrompting, Ev: event.EvidenceA},
		},
		{
			name: "labels come in pkg/event's slot order, the side doors then focus_unknown, and an anchor's own labels stay off the slot",
			credits: []score.Credited{
				creditEntry("p_9f2c", event.ReasonPrompt, event.EvidenceC, 10, event.LabelTypingModule, event.LabelFocusUnknown),
				creditEntry("p_9f2c", event.ReasonRead, event.EvidenceC, 10, event.LabelViaPlugin, event.LabelVM),
			},
			want: event.BucketSlot{Project: "p_9f2c", Credited: 10, Why: event.ReasonPrompt, Kind: event.ActivityPrompting, Ev: event.EvidenceC,
				Lbl: []event.Label{event.LabelViaPlugin, event.LabelFocusUnknown}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := slotSummary(score.Credit{Slot: slot, ProjectKey: "p_9f2c", Credited: tc.credits})
			if got.Project != tc.want.Project || got.Credited != tc.want.Credited || got.Why != tc.want.Why ||
				got.Kind != tc.want.Kind || got.Ev != tc.want.Ev || !slices.Equal(got.Lbl, tc.want.Lbl) {
				t.Errorf("slotSummary = %+v, want %+v", got, tc.want)
			}
			// Whatever it sums, the slot is one pkg/event takes.
			b := event.MinuteBucket{V: score.Version(), Params: testTable().Hash(), Minute: time.UnixMilli(slot - 45000).UTC(),
				Device: "d_test", Slots: []event.BucketSlot{{}, {}, {}, got}, CreditedS: got.Credited,
				State: event.StateFinal, Witness: event.Witnessed}
			if err := b.Validate(); err != nil {
				t.Errorf("a bucket with the slot doesn't validate: %v", err)
			}
		})
	}
}

// everyFixture is every timeline under testdata/timelines: the top-level
// fixtures, then the side-door and lookback cases in their own
// directories, as pkg/score's golden tests read them (goldenCases).
func everyFixture(t *testing.T) []*timelines.Fixture {
	t.Helper()
	root, err := timelines.Root()
	if err != nil {
		t.Fatal(err)
	}
	out := loadFixtures(t)
	for _, dir := range []string{"sidedoor", "lookback"} {
		names, err := timelines.List(filepath.Join(root, dir))
		if err != nil {
			t.Fatal(err)
		}
		if len(names) == 0 {
			t.Fatalf("no timelines under testdata/timelines/%s", dir)
		}
		for _, n := range names {
			f, err := timelines.Load(root, dir+"/"+n)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, f)
		}
	}
	return out
}

// TestSlotSummaryMatchesPkgScore holds the agent's slot summary to
// pkg/score's Minute.Bucket, the summary SM 5.1 uploads, on every timeline
// under testdata/timelines, the side-door and lookback cases included:
// each slot pkg/score derives gets the same project, credited seconds,
// level and labels from both. The reason may differ: slotSummary takes the
// largest entry's, Bucket the reason with the most credit. The side-door
// timelines hold slots with labels, so a summary that dropped one fails
// here. Where a timeline credits a slot at two levels, a summary that took
// the largest entry's level fails too; TestSlotSummary's mixed slot holds
// that rule either way.
func TestSlotSummaryMatchesPkgScore(t *testing.T) {
	table := testTable()
	var mixed, labeled int
	for _, f := range everyFixture(t) {
		t.Run(f.Name+": the agent's slots claim what pkg/score's do", func(t *testing.T) {
			_, out := score.Step(score.NewState(table, fixtureDevice(f)), f.Events)
			var all score.Derived
			all.Append(out.Final)
			all.Append(out.Pending)
			bad := 0
			for _, m := range score.Minutes(all) {
				b := m.Bucket(score.BucketInfo{Table: table, Device: "d_test", State: event.StateFinal, Witness: event.Witnessed})
				for _, s := range m.Slots {
					got, want := slotSummary(s.Credit), b.Slots[(s.Slot-m.Start)/event.SlotMS]
					if len(levelsOf(s.Credit)) > 1 {
						mixed++
					}
					if len(want.Lbl) > 0 {
						labeled++
					}
					if got.Project == want.Project && got.Credited == want.Credited && got.Ev == want.Ev && slices.Equal(got.Lbl, want.Lbl) {
						continue
					}
					if bad++; bad <= 10 {
						t.Errorf("slot %s: the agent's %+v, pkg/score's %+v", time.UnixMilli(s.Slot).UTC().Format(time.RFC3339), got, want)
					}
				}
			}
		})
	}
	t.Run("the side-door timelines hold slots with labels, so a summary that drops one fails above", func(t *testing.T) {
		if labeled == 0 {
			t.Error("no timeline's slot has a label")
		}
	})
	t.Run("a slot credited at two levels, whose weakest level isn't its largest entry's, is compared above", func(t *testing.T) {
		if mixed == 0 {
			t.Skip("no timeline credits a slot at two levels yet; TestSlotSummary's mixed slot holds the rule")
		}
	})
}

// levelsOf returns the levels a slot's credited entries carry, each once.
func levelsOf(c score.Credit) []event.Evidence {
	var out []event.Evidence
	for _, e := range c.Credited {
		if e.Halves > 0 && !slices.Contains(out, e.Level) {
			out = append(out, e.Level)
		}
	}
	return out
}
