// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/timelines"
	"github.com/some-du6e/pairtime/pkg/event"
)

// The oracle for WP-SC8's scorer is testdata/timelines/focus-unknown:
// WP-FX8's cases (a) to (f) and (b)'s X11 twin, worked out by hand from SM
// 2.5's focus-unknown paragraph and D-split-slots before this code. These
// tests hold the scorer to every case's expected.json, anchors.json and
// slots.json, scored a Step a minute, as the agent scores (DESIGN 3.7).
// internal/timelines checks the cases against themselves, their READMEs
// and their generators.

// focusUnknownDir is testdata/timelines' directory of the focus-unknown
// cases.
const focusUnknownDir = "focus-unknown"

// focusUnknownFixtures loads every case under
// testdata/timelines/focus-unknown through timelines.List and Load, since
// LoadAll reads only the top-level timelines.
func focusUnknownFixtures(t *testing.T) []*timelines.Fixture {
	t.Helper()
	root, err := timelines.Root()
	if err != nil {
		t.Fatal(err)
	}
	names, err := timelines.List(filepath.Join(root, focusUnknownDir))
	if err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		t.Fatal("no cases under testdata/timelines/focus-unknown")
	}
	var out []*timelines.Fixture
	for _, n := range names {
		f, err := timelines.Load(root, focusUnknownDir+"/"+n)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, f)
	}
	return out
}

// focusUnknownTimeline names the totals of a focus-unknown case that say
// something of its timeline, not of an output of the scorer: how many
// seconds both projects' candidates wanted, and how old the one os.focus
// is when the work starts (testdata/timelines/focus-unknown/README.md).
var focusUnknownTimeline = map[string]bool{
	"contested_buckets_s":     true,
	"contested_leads_s":       true,
	"checkpoints_after_focus": true,
	"focus_age_at_work_s":     true,
}

// TestFocusUnknownFixtures holds the scorer to each focus-unknown case: the
// anchors and turns of its anchors.json, with their levels and labels, the
// credit, codes and agent time of each of its expected.json's rows, and its
// totals, every one of which but those of its timeline some output of the
// scorer gives. A Step a minute gives what one Step over every record does,
// and every derived record passes pkg/event validation.
func TestFocusUnknownFixtures(t *testing.T) {
	for _, f := range focusUnknownFixtures(t) {
		d := scoreByMinute(t, fixtureDevice(f), f.Events)
		checkAnchors(t, f, d)
		checkTurns(t, f, d)
		checkMinutes(t, f, d)
		checkTotals(t, f, d)
		checkCreditSlots(t, f, d)
		t.Run(f.Name+": every total its expected.json states, but those of its timeline, is one an output of the scorer gives", func(t *testing.T) {
			tot := totalsOf(d, f.Expected.Window.Start.UnixMilli(), f.Expected.Window.End.UnixMilli())
			for _, k := range slices.Sorted(maps.Keys(f.Expected.Totals.Other)) {
				if _, ok := fixtureTotal(d, f, tot, k); !ok && !focusUnknownTimeline[k] {
					t.Errorf("%s: no output of the scorer gives it", k)
				}
			}
		})
		t.Run(f.Name+": a Step a minute gives what one Step over every record does", func(t *testing.T) {
			if got, want := marshal(t, d), marshal(t, scoreAll(t, fixtureDevice(f), f.Events)); !bytes.Equal(got, want) {
				t.Errorf("a Step a minute differs from one Step %s", firstDiff(got, want))
			}
		})
		t.Run(f.Name+": every slot, turn, anchor and credited entry passes pkg/event validation", func(t *testing.T) {
			checkDerived(t, d)
		})
	}
}

// TestFocusUnknownFixtureBuckets checks the minute buckets the agent
// uploads for each focus-unknown case, each minute's last revision a Step a
// minute: every one passes validation, and they add up to the case's rows.
func TestFocusUnknownFixtureBuckets(t *testing.T) {
	for _, f := range focusUnknownFixtures(t) {
		checkBuckets(t, f, relayedBuckets(t, f))
	}
}

// fuSlot is a slot of a focus-unknown case's slots.json: the window SM 2.5
// gives it, the one project its credit goes to (D-split-slots), its credit
// by project and reason, its credited seconds by level, and the level and
// labels its minute bucket's slot carries (SM 5.1).
type fuSlot struct {
	Start     time.Time                           `json:"start"`
	Local     string                              `json:"local"`
	Focus     event.AppClass                      `json:"focus"`
	P         string                              `json:"p"`
	CreditedS float64                             `json:"credited_s"`
	Credit    map[string]map[event.Reason]float64 `json:"credit"`
	Levels    map[event.Evidence]float64          `json:"levels"`
	Ev        event.Evidence                      `json:"ev"`
	Lbl       []event.Label                       `json:"lbl"`
}

// readFocusSlots reads a focus-unknown case's slots.json: every slot of
// every bucket minute.
func readFocusSlots(t *testing.T, f *timelines.Fixture) []fuSlot {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.Dir, "slots.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Fixture string   `json:"fixture"`
		Slots   []fuSlot `json:"slots"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		t.Fatalf("%s/slots.json: %v", f.Name, err)
	}
	return file.Slots
}

// TestFocusUnknownSlots checks every slot of every bucket minute of each
// focus-unknown case against its slots.json, a Step a minute: the window SM
// 2.5 gives it, its credit by project and reason, its credited seconds by
// level, the project they go to (D-split-slots), and the level and labels
// its minute bucket's slot carries: its weakest second's level, and
// focus_unknown where a second is level C because focus was unknown (SM
// 5.1). Totals can't tell which slots are level C, as (c)'s README says,
// and the slots can.
func TestFocusUnknownSlots(t *testing.T) {
	for _, f := range focusUnknownFixtures(t) {
		want := readFocusSlots(t, f)
		d := scoreByMinute(t, fixtureDevice(f), f.Events)
		buckets := relayedBuckets(t, f)
		t.Run(f.Name+": every slot's focus, credit, levels, project and labels are its slots.json's", func(t *testing.T) {
			if len(want) == 0 {
				t.Fatal("slots.json lists no slot")
			}
			at := make(map[int64]int, len(d.Slots))
			for i, s := range d.Slots {
				at[s.Start] = i
			}
			for _, w := range want {
				start := w.Start.UnixMilli()
				i, ok := at[start]
				if !ok {
					t.Errorf("%s: the scorer derived no slot", w.Local)
					continue
				}
				if s := d.Slots[i]; s.FocusClass != w.Focus {
					t.Errorf("%s: focus %q, want %q", w.Local, s.FocusClass, w.Focus)
				}
				c := d.Credits[i]
				credit := map[string]map[event.Reason]float64{}
				levels := map[event.Evidence]float64{}
				var n float64
				for _, e := range c.Credited {
					v := float64(e.Halves) / 2
					if credit[e.ProjectKey] == nil {
						credit[e.ProjectKey] = map[event.Reason]float64{}
					}
					credit[e.ProjectKey][e.Reason] += v
					levels[e.Level] += v
					n += v
				}
				if n != w.CreditedS {
					t.Errorf("%s: %v s credited, want %v", w.Local, n, w.CreditedS)
				}
				if !sameCredit(credit, w.Credit) {
					t.Errorf("%s: credit %v, want %v", w.Local, credit, w.Credit)
				}
				if !sameSeconds(levels, w.Levels) {
					t.Errorf("%s: seconds by level %v, want %v", w.Local, levels, w.Levels)
				}
				if n == 0 {
					continue
				}
				if c.ProjectKey != w.P {
					t.Errorf("%s: credit goes to %q, want %q", w.Local, c.ProjectKey, w.P)
				}
				m := start - start%60_000
				b, ok := buckets[m]
				if !ok {
					t.Errorf("%s: no minute bucket for the slot's minute", w.Local)
					continue
				}
				bs := b.Slots[(start-m)/event.SlotMS]
				if bs.Project != w.P || bs.Ev != w.Ev || !slices.Equal(bs.Lbl, w.Lbl) {
					t.Errorf("%s: the bucket's slot goes to %q at level %q with labels %q, want %q at %q with %q",
						w.Local, bs.Project, bs.Ev, bs.Lbl, w.P, w.Ev, w.Lbl)
				}
			}
		})
	}
}

// sameCredit reports whether two slots' credit by project and reason agree,
// with a project or reason of no seconds the same as none.
func sameCredit(a, b map[string]map[event.Reason]float64) bool {
	for _, pair := range [][2]map[string]map[event.Reason]float64{{a, b}, {b, a}} {
		for p, rs := range pair[0] {
			for r, v := range rs {
				if v != 0 && pair[1][p][r] != v {
					return false
				}
			}
		}
	}
	return true
}
