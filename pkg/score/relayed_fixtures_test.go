// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"bytes"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/timelines"
	"github.com/some-du6e/pairtime/pkg/event"
)

// The oracle for WP-ADE1's scorer is testdata/timelines/relayed: D-relayed's
// cases (docs/errata/2026-11-22-relayed-prompts.md), worked out by hand from
// SM 2.2 rule 9, 2.3, 2.4 and 5.2 before this code. These tests hold the
// scorer to every case's expected.json and anchors.json as the fixture
// tests hold the SM and side-door cases, with the relayed conventions of
// testdata/timelines/relayed/README.md: a relayed anchor's event is its
// relay.message and its t the message's sent_at, the stamp its lead ends
// at; t3-here's typed anchor stays at its prompt's t, with the stamp in
// sent_at and the record in relay; and the counts among the totals come
// from the scorer's turns and what became of each relay record
// (relayedCount). internal/timelines checks the cases against themselves,
// their READMEs and their generators.

// relayedDir is testdata/timelines' directory of the relayed cases.
const relayedDir = "relayed"

// relayedFixtures loads every case under testdata/timelines/relayed
// through timelines.List and Load, as goldenCases loads the lookback cases.
func relayedFixtures(t *testing.T) []*timelines.Fixture {
	t.Helper()
	root, err := timelines.Root()
	if err != nil {
		t.Fatal(err)
	}
	names, err := timelines.List(filepath.Join(root, relayedDir))
	if err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		t.Fatal("no cases under testdata/timelines/relayed")
	}
	var out []*timelines.Fixture
	for _, n := range names {
		f, err := timelines.Load(root, relayedDir+"/"+n)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, f)
	}
	return out
}

// relayedScores holds scoreRelayed's output by case, so that the tests
// below step agent-day's day of records through the scorer once.
var relayedScores = struct {
	sync.Mutex
	m map[string]Derived
}{m: map[string]Derived{}}

// scoreRelayed scores a relayed case a Step a minute, as the agent does
// (DESIGN 3.7) and as TestGolden does, and rebuilds the whole picture from
// the Finals and the last Pending. One Step over the records of a device
// that never samples derives only the BUCKET_TTL_S before now, which would
// leave most of agent-day's day out. Callers must not change what it
// returns.
func scoreRelayed(t *testing.T, f *timelines.Fixture) Derived {
	t.Helper()
	relayedScores.Lock()
	defer relayedScores.Unlock()
	d, ok := relayedScores.m[f.Name]
	if !ok {
		d = scoreByMinute(t, fixtureDevice(f), f.Events)
		relayedScores.m[f.Name] = d
	}
	return d
}

// relayedBuckets returns the minute buckets the agent uploads for a
// relayed case: the last revision Revisions writes for each minute, a Step
// a minute, where something was credited or an agent ran.
func relayedBuckets(t *testing.T, f *timelines.Fixture) map[int64]event.MinuteBucket {
	t.Helper()
	revs := stepRevisions(t, fixtureDevice(f), f.Expected.Device, f.Expected.Witness, arrivals(f.Events, nil))
	got := map[int64]event.MinuteBucket{}
	for m, b := range lastRevisions(revs) {
		if b.CreditedS > 0 || b.AgentRunningS > 0 {
			got[m] = b
		}
	}
	return got
}

// relayedCount returns a relayed case's count total over [from, to), from
// the scorer's turns and what became of each relay record
// (testdata/timelines/relayed/README.md): the prompts whose turns are
// relayed, headless, scheduled or human_typed; the cross-session subagents,
// SM 2.2 rule 3's turns for a message the app says another thread sent;
// and the relay records that paired with nothing. It reports false for any
// other total.
func relayedCount(d Derived, from, to int64, k string) (float64, bool) {
	byOrigin := map[string]event.Origin{
		"relayed_prompts":   event.OriginRelayed,
		"headless_prompts":  event.OriginHeadless,
		"scheduled_prompts": event.OriginScheduled,
		"typed_prompts":     event.OriginHumanTyped,
	}
	in := func(tu Turn) bool { return tu.Prompt != "" && tu.TSubmit >= from && tu.TSubmit < to }
	var n float64
	switch o, ok := byOrigin[k]; {
	case ok:
		for _, tu := range d.Turns {
			if in(tu) && tu.Origin == o {
				n++
			}
		}
	case k == "cross_session_prompts":
		for _, tu := range d.Turns {
			if !in(tu) || tu.Origin != event.OriginSubagent {
				continue
			}
			if r, ok := relayByID(d, tu.Relay); ok && r.Declared == event.RelayDeclaredCrossSession {
				n++
			}
		}
	case k == "unpaired_relay_records":
		for _, r := range d.Relays {
			if r.T >= from && r.T < to && r.Prompt == "" {
				n++
			}
		}
	default:
		return 0, false
	}
	return n, true
}

// TestRelayedFixtures holds the scorer to each relayed case: the anchors
// and turns of its anchors.json, the credit, codes and agent time of each
// of its expected.json's rows, and its totals, every one of which, the
// counts included, some output of the scorer gives. The credit has the
// shape TestCreditSlots checks the other fixtures' for, and every derived
// record passes pkg/event validation.
func TestRelayedFixtures(t *testing.T) {
	for _, f := range relayedFixtures(t) {
		d := scoreRelayed(t, f)
		checkAnchors(t, f, d)
		checkTurns(t, f, d)
		checkMinutes(t, f, d)
		checkTotals(t, f, d)
		checkCreditSlots(t, f, d)
		t.Run(f.Name+": every total its expected.json states is one an output of the scorer gives", func(t *testing.T) {
			tot := totalsOf(d, f.Expected.Window.Start.UnixMilli(), f.Expected.Window.End.UnixMilli())
			for _, k := range slices.Sorted(maps.Keys(f.Expected.Totals.Other)) {
				if _, ok := fixtureTotal(d, f, tot, k); !ok {
					t.Errorf("%s: no output of the scorer gives it", k)
				}
			}
		})
		t.Run(f.Name+": every slot, turn, anchor and credited entry passes pkg/event validation", func(t *testing.T) {
			checkDerived(t, d)
		})
	}
}

// TestRelayedFixtureBuckets checks the minute buckets the agent uploads for
// each relayed case, each minute's last revision a Step a minute: every one
// passes validation, they add up to the case's rows, and every credited
// slot in the window claims the case's level, with via_relay where its
// credit is relayed (SM 5.1, DESIGN 6.4). A case's credit is all relayed or
// none of it: t3-here's is typed.
func TestRelayedFixtureBuckets(t *testing.T) {
	for _, f := range relayedFixtures(t) {
		got := relayedBuckets(t, f)
		checkBuckets(t, f, got)
		via, _ := f.Expected.Totals.Get("via_relay_s")
		relayed := via > 0 && via == f.Expected.Totals.CreditedS
		t.Run(fmt.Sprintf("%s: every credited slot is level %s, and carries via_relay: %v", f.Name, f.Expected.Evidence, relayed), func(t *testing.T) {
			from, to := f.Expected.Window.Start.UnixMilli(), f.Expected.Window.End.UnixMilli()
			for _, m := range slices.Sorted(maps.Keys(got)) {
				if m < from || m >= to {
					continue
				}
				for i, s := range got[m].Slots {
					if s.Credited > 0 && (s.Ev != f.Expected.Evidence || slices.Contains(s.Lbl, event.LabelViaRelay) != relayed) {
						t.Errorf("%s slot %d: level %q, labels %q", minuteName(m), i, s.Ev, s.Lbl)
					}
				}
			}
		})
	}
}

// TestRelayedSubmissionTotals checks the totals of a submission of each
// relayed case's window and projects against its expected.json, as
// TestSideDoorTotals does the side-door cases': credited_s, the credit by
// level, and via_relay_s, the relayed seconds, which are part of
// level_c_s. An agent app's relay records count toward neither logged_s
// nor ai_logged_s (DESIGN 6.2 D19), so the case's records without them give
// the same.
func TestRelayedSubmissionTotals(t *testing.T) {
	for _, f := range relayedFixtures(t) {
		d := scoreRelayed(t, f)
		sub := fixtureSubmission(f)
		sub.Projects = slices.Sorted(maps.Keys(f.Expected.Projects))
		tot := sub.Totals(d, f.Events)
		t.Run(f.Name+": the totals pass pkg/event validation", func(t *testing.T) {
			if err := tot.Validate(); err != nil {
				t.Error(err)
			}
		})
		for _, c := range []struct {
			key string
			got float64
		}{
			{"credited_s", tot.CreditedS},
			{"level_a_s", tot.LevelAS},
			{"level_b_s", tot.LevelBS},
			{"level_c_s", tot.LevelCS},
			{"via_relay_s", tot.ViaRelayS},
		} {
			w, ok := f.Expected.Totals.Get(c.key)
			if !ok {
				continue
			}
			t.Run(fmt.Sprintf("%s's submission %s is %v, as its expected.json says", f.Name, c.key, w), func(t *testing.T) {
				if c.got != w {
					t.Errorf("%s = %v, want %v", c.key, c.got, w)
				}
			})
		}
		t.Run(f.Name+": its relay records add nothing to logged_s or ai_logged_s, which count no record via app (DESIGN 6.2 D19)", func(t *testing.T) {
			var without []event.Event
			for _, e := range f.Events {
				if e.Via != event.ViaApp {
					without = append(without, e)
				}
			}
			if o := sub.Totals(d, without); o.LoggedS != tot.LoggedS || o.AILoggedS != tot.AILoggedS {
				t.Errorf("logged_s %v and ai_logged_s %v with the relay records, %v and %v without them", tot.LoggedS,
					tot.AILoggedS, o.LoggedS, o.AILoggedS)
			}
		})
	}
}

// TestRelayedRecords checks what became of each relay record (SM 2.2 rule
// 9). Every link in the relayed cases passes SM 2.2's checks, as
// internal/timelines checks, so each record pairs with the prompt its link
// names and a record no link names pairs with nothing, and the turn that
// went by a paired record has the origin anchors.json gives it.
func TestRelayedRecords(t *testing.T) {
	for _, f := range relayedFixtures(t) {
		d := scoreRelayed(t, f)
		// linked maps each relay record to the prompt its link names, and
		// origin each prompt to its turn's origin in anchors.json.
		linked := map[string]string{}
		origin := map[string]event.Origin{}
		var records []string
		for _, e := range f.Events {
			switch p := e.Data.(type) {
			case *event.RelayMessage:
				records = append(records, e.EventID)
				if p.Prompt != "" {
					linked[e.EventID] = p.Prompt
				}
			case *event.PromptSubmit:
				if p.Relay != "" {
					linked[p.Relay] = e.EventID
				}
				for _, tr := range f.Anchors.Turns {
					if tr.SessionKey == e.SessionKey && tr.TurnKey == p.TurnKey && tr.TSubmit == e.T {
						origin[e.EventID] = tr.Origin
					}
				}
			}
		}
		t.Run(f.Name+": the scorer accounts for every relay record of the case", func(t *testing.T) {
			var got []string
			for _, r := range d.Relays {
				got = append(got, r.ID)
			}
			slices.Sort(got)
			slices.Sort(records)
			if !slices.Equal(got, records) {
				t.Errorf("relay records %q, want %q", got, records)
			}
		})
		for _, r := range d.Relays {
			name := fmt.Sprintf("%s: the message stamped %s pairs with the prompt its link names, and its turn has anchors.json's origin",
				f.Name, time.UnixMilli(r.SentAt).UTC().Format("15:04:05Z"))
			t.Run(name, func(t *testing.T) {
				want := linked[r.ID]
				if r.Prompt != want {
					t.Errorf("paired with %q, want %q", r.Prompt, want)
				}
				switch o, ok := origin[want]; {
				case want == "":
					if r.Origin != "" {
						t.Errorf("origin %q for a record that paired with nothing", r.Origin)
					}
				case !ok:
					t.Errorf("anchors.json has no turn for the prompt %s", want)
				case r.Origin != o:
					t.Errorf("its turn is %q, anchors.json's %q", r.Origin, o)
				}
			})
		}
	}
}

// TestRelayedFixtureSteps checks that how a relayed case's records are
// split across Steps doesn't change what the scorer derives (DESIGN 3.7),
// as TestFixtureSteps does for the SM fixtures. A Step a minute derives the
// turns, anchors and relay records one Step over the records does, and the
// same credit in the window's slots that both derive: one Step over a
// device that never samples derives only the BUCKET_TTL_S before now, for
// agent-day the last two hours of its day. Records in reverse order, each
// twice, give what one Step gives.
func TestRelayedFixtureSteps(t *testing.T) {
	for _, f := range relayedFixtures(t) {
		dev := fixtureDevice(f)
		one := scoreAll(t, dev, f.Events)
		perMinute := scoreRelayed(t, f)
		t.Run(f.Name+": a Step a minute derives what one Step does, where both derive the slots", func(t *testing.T) {
			from, to := f.Expected.Window.Start.UnixMilli(), f.Expected.Window.End.UnixMilli()
			if len(one.Slots) > 0 {
				from = max(from, one.Slots[0].Start)
			}
			inWindow := func(d Derived) []Credit {
				var out []Credit
				for _, c := range d.Credits {
					if c.Slot >= from && c.Slot < to {
						out = append(out, c)
					}
				}
				return out
			}
			for _, c := range []struct {
				what      string
				got, want any
			}{
				{"turns", perMinute.Turns, one.Turns},
				{"anchors", perMinute.Anchors, one.Anchors},
				{"relay records", perMinute.Relays, one.Relays},
				{"credit slots", inWindow(perMinute), inWindow(one)},
			} {
				if got, want := marshal(t, c.got), marshal(t, c.want); !bytes.Equal(got, want) {
					t.Errorf("the %s differ %s", c.what, firstDiff(got, want))
				}
			}
		})
		t.Run(f.Name+": records in reverse order, each twice, give what one Step gives", func(t *testing.T) {
			var shuffled []event.Event
			for i := len(f.Events) - 1; i >= 0; i-- {
				shuffled = append(shuffled, f.Events[i], f.Events[i])
			}
			if got, want := marshal(t, scoreAll(t, dev, shuffled)), marshal(t, one); !bytes.Equal(got, want) {
				t.Errorf("the output depends on the order records arrive in %s", firstDiff(got, want))
			}
		})
	}
}
