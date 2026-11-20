// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/some-du6e/pairtime/internal/timelines"
	"github.com/some-du6e/pairtime/pkg/event"
)

// WP-SC2's oracle is SM 6's minute rows and totals, through WP-FX1's,
// WP-FX3's and WP-FX4's fixtures, written before this code. These tests sum
// the scorer's credit slots by minute and compare each minute with
// expected.json: credited seconds by project and reason, uncredited seconds
// by reason where SM decides them, agent_running_s, and which minutes have a
// bucket (SM 5.1).

// creditFixtures are the SM fixtures WP-SC2 reproduces. 6.3 and its two-tab
// variant need one timeline across projects (WP-SC3), and 6.5 and 4.2's
// alternation need dark flow (WP-SC6).
var creditFixtures = []string{"6.1", "6.2", "6.4", "6.6"}

// creditCases returns the fixtures WP-SC2 reproduces: creditFixtures and
// every side-door case.
func creditCases(t *testing.T) []*timelines.Fixture {
	t.Helper()
	var out []*timelines.Fixture
	for _, f := range loadFixtures(t) {
		if slices.Contains(creditFixtures, f.Name) {
			out = append(out, f)
		}
	}
	if len(out) != len(creditFixtures) {
		t.Fatalf("found %d of the fixtures %q", len(out), creditFixtures)
	}
	return append(out, sideDoorCases(t)...)
}

// minuteSum is one minute of credit slots, in seconds.
type minuteSum struct {
	credit     map[string]map[event.Reason]float64
	uncredited map[event.Reason]float64
	agent      float64
}

func newMinuteSum() *minuteSum {
	return &minuteSum{credit: map[string]map[event.Reason]float64{}, uncredited: map[event.Reason]float64{}}
}

func (m *minuteSum) credited() float64 {
	var n float64
	for _, rs := range m.credit {
		for _, s := range rs {
			n += s
		}
	}
	return n
}

// bucket reports whether the minute has a minute bucket: something was
// credited or an agent was running (SM 5.1).
func (m *minuteSum) bucket() bool { return m.credited() > 0 || m.agent > 0 }

func (m *minuteSum) add(o *minuteSum) {
	for p, rs := range o.credit {
		if m.credit[p] == nil {
			m.credit[p] = map[event.Reason]float64{}
		}
		for r, s := range rs {
			m.credit[p][r] += s
		}
	}
	for r, s := range o.uncredited {
		m.uncredited[r] += s
	}
	m.agent += o.agent
}

// byMinute sums credit slots by UTC minute.
func byMinute(d Derived) map[int64]*minuteSum {
	out := map[int64]*minuteSum{}
	for _, c := range d.Credits {
		start := c.Slot - c.Slot%60_000
		m := out[start]
		if m == nil {
			m = newMinuteSum()
			out[start] = m
		}
		for _, e := range c.Credited {
			if m.credit[e.ProjectKey] == nil {
				m.credit[e.ProjectKey] = map[event.Reason]float64{}
			}
			m.credit[e.ProjectKey][e.Reason] += float64(e.Halves) / 2
		}
		for r, n := range c.Uncredited {
			m.uncredited[r] += float64(n)
		}
		m.agent += float64(c.AgentS)
	}
	return out
}

// sameSeconds reports whether two maps of seconds agree, a missing key
// counting as zero.
func sameSeconds[K comparable](a, b map[K]float64) bool {
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	for k, v := range b {
		if a[k] != v {
			return false
		}
	}
	return true
}

func TestFixtureMinutes(t *testing.T) {
	for _, f := range creditCases(t) {
		got := byMinute(scoreAll(t, fixtureDevice(f), f.Events))
		for _, row := range f.Expected.Rows {
			name := fmt.Sprintf("%s %s has the credit, uncredited seconds and agent time of its row", f.Name, row.Local)
			if !row.Bucket {
				name = fmt.Sprintf("%s %s credits nothing and runs no agent, so it has no bucket", f.Name, row.Local)
			}
			t.Run(name, func(t *testing.T) {
				sum := newMinuteSum()
				buckets := 0
				for m := row.Start.UnixMilli(); m < row.End().UnixMilli(); m += 60_000 {
					if g := got[m]; g != nil && g.bucket() {
						buckets++
						sum.add(g)
					}
				}
				if !row.Bucket {
					if buckets > 0 {
						t.Fatalf("%d minutes have credit or a running agent: credit %v, agent %v s", buckets, sum.credit, sum.agent)
					}
					return
				}
				if buckets != row.BucketMinutes {
					t.Errorf("%d minutes with a bucket, want %d", buckets, row.BucketMinutes)
				}
				for _, p := range slices.Sorted(maps.Keys(sum.credit)) {
					if _, ok := row.Credit[p]; !ok && len(sum.credit[p]) > 0 {
						t.Errorf("credits project %s %v, which the row doesn't", p, sum.credit[p])
					}
				}
				for p, want := range row.Credit {
					if !sameSeconds(sum.credit[p], want) {
						t.Errorf("credit for %s is %v, want %v (%s)", p, sum.credit[p], want, row.SM)
					}
				}
				if row.Uncredited != nil && !sameSeconds(sum.uncredited, row.Uncredited) {
					t.Errorf("uncredited %v, want %v (%s)", sum.uncredited, row.Uncredited, row.SM)
				}
				if sum.agent != row.AgentRunningS {
					t.Errorf("agent_running_s %v, want %v", sum.agent, row.AgentRunningS)
				}
			})
		}
	}
}

// totals are a fixture window's sums over every slot in it, with and
// without a bucket.
type totals struct {
	credited, ai, agentRunning, agentOnly float64
	byReason, uncredited                  map[event.Reason]float64
	byLevel                               map[event.Evidence]float64
	labels                                map[event.Label]float64
}

// aiReasons are SM 5.4's AI-assisted reasons. Any other credited second
// counts as AI-assisted too when a turn of its project was running.
var aiReasons = []event.Reason{event.ReasonPrompt, event.ReasonAnswer, event.ReasonGoAhead, event.ReasonQueued,
	event.ReasonPermission, event.ReasonInterrupt, event.ReasonSupervise, event.ReasonRead}

func totalsOf(d Derived, from, to int64) totals {
	tot := totals{byReason: map[event.Reason]float64{}, uncredited: map[event.Reason]float64{},
		byLevel: map[event.Evidence]float64{}, labels: map[event.Label]float64{}}
	for _, c := range d.Credits {
		if c.Slot < from || c.Slot >= to {
			continue
		}
		for _, e := range c.Credited {
			s := float64(e.Halves) / 2
			tot.credited += s
			tot.byReason[e.Reason] += s
			tot.byLevel[e.Level] += s
			for _, l := range e.Labels {
				tot.labels[l] += s
			}
			if e.InTurn || slices.Contains(aiReasons, e.Reason.Base()) {
				tot.ai += s
			}
		}
		for r, n := range c.Uncredited {
			tot.uncredited[r] += float64(n)
		}
		tot.agentRunning += float64(c.AgentS)
		for _, n := range c.AgentOnly {
			tot.agentOnly += float64(n)
		}
	}
	return tot
}

func TestFixtureTotals(t *testing.T) {
	for _, f := range creditCases(t) {
		from, to := f.Expected.Window.Start.UnixMilli(), f.Expected.Window.End.UnixMilli()
		d := scoreAll(t, fixtureDevice(f), f.Events)
		tot := totalsOf(d, from, to)
		want := f.Expected.Totals
		t.Run(f.Name+" credits its total, by reason, and runs its agents for as long as SM says", func(t *testing.T) {
			if tot.credited != want.CreditedS {
				t.Errorf("credited_s %v, want %v", tot.credited, want.CreditedS)
			}
			if !sameSeconds(tot.byReason, want.CreditedByReason) {
				t.Errorf("credited by reason %v, want %v", tot.byReason, want.CreditedByReason)
			}
			if tot.agentRunning != want.AgentRunningS {
				t.Errorf("agent_running_s %v, want %v", tot.agentRunning, want.AgentRunningS)
			}
		})
		for _, k := range slices.Sorted(maps.Keys(want.Other)) {
			var got float64
			switch k {
			case "wall_s":
				got = float64(to-from) / 1000
			case "agent_only_s":
				got = tot.agentOnly
			case "absent_s":
				got = tot.uncredited[event.ReasonAbsent]
			case "ai_credited_s":
				got = tot.ai
			case "hand_credited_s":
				got = tot.credited - tot.ai
			case "first_cycle_credited_s":
				got = totalsOf(d, from, from+12*60_000).credited
			case "level_a_s":
				got = tot.byLevel[event.EvidenceA]
			case "level_b_s":
				got = tot.byLevel[event.EvidenceB]
			case "level_c_s":
				got = tot.byLevel[event.EvidenceC]
			case "via_track_s":
				got = tot.labels[event.LabelViaTrack]
			default:
				// logged_s, ai_logged_s and level_c_cap_s are submission
				// totals (SM 5.3, 5.4; WP-SC4); lid_open_s and bucket_left_s
				// describe the timeline rather than any output.
				continue
			}
			t.Run(fmt.Sprintf("%s's %s is %v, as its expected.json says", f.Name, k, want.Other[k]), func(t *testing.T) {
				if got != want.Other[k] {
					t.Errorf("%s = %v, want %v", k, got, want.Other[k])
				}
			})
		}
	}
}

// TestCreditSlots checks the shape of every fixture's credit: one Credit per
// slot, whose seconds add up to the slot's 15, each entry a valid
// event.CreditSlot whose seconds trace back to anchors (SM 2.7).
func TestCreditSlots(t *testing.T) {
	for _, f := range append(loadFixtures(t), sideDoorCases(t)...) {
		d := scoreAll(t, fixtureDevice(f), f.Events)
		t.Run(f.Name+": one credit slot per presence slot, each accounting for its 15 seconds", func(t *testing.T) {
			if len(d.Credits) != len(d.Slots) {
				t.Fatalf("%d credit slots for %d slots", len(d.Credits), len(d.Slots))
			}
			for i, c := range d.Credits {
				if c.Slot != d.Slots[i].Start {
					t.Fatalf("credit slot %d starts at %d, its slot at %d", i, c.Slot, d.Slots[i].Start)
				}
				var n int64
				for _, e := range c.Credited {
					n += e.Seconds
				}
				for _, s := range c.Uncredited {
					n += s
				}
				if n != event.SlotMS/1000 || c.AgentS > n {
					t.Fatalf("slot %d accounts for %d seconds and %d agent seconds", c.Slot, n, c.AgentS)
				}
			}
		})
		t.Run(f.Name+": every credited entry is a valid event.CreditSlot naming its anchors", func(t *testing.T) {
			ids := map[string]bool{}
			for _, a := range d.Anchors {
				ids[a.ID] = true
			}
			for _, c := range d.Credits {
				for _, e := range c.Credited {
					if err := e.Event(c.Slot).Validate(); err != nil {
						t.Fatalf("slot %d, %s: %v", c.Slot, e.Reason, err)
					}
					for _, id := range e.AnchorIDs {
						if !ids[id] {
							t.Fatalf("slot %d, %s names %s, which is no anchor", c.Slot, e.Reason, id)
						}
					}
				}
				for r := range c.Uncredited {
					if !r.Uncredited() {
						t.Fatalf("slot %d has uncredited seconds under %q", c.Slot, r)
					}
				}
			}
		})
	}
}

// sideDoorTurns is the part of sidedoor.json that pins each turn's reading
// fill (WP-FX4).
type sideDoorTurns struct {
	Turns []struct {
		OpenedBy    string    `json:"opened_by"`
		TurnKey     string    `json:"turn_key"`
		SessionKey  string    `json:"session_key"`
		T           int64     `json:"t"`
		Local       string    `json:"local"`
		Via         event.Via `json:"via"`
		Backed      bool      `json:"backed"`
		OutputFillS float64   `json:"output_fill_s"`
		FillS       float64   `json:"fill_s"`
		Why         string    `json:"why"`
	} `json:"turns"`
}

func secondsMS(s float64) int64 { return int64(math.Round(s * 1000)) }

func readJSONFile(t *testing.T, dir, name string, v any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s/%s: %v", dir, name, err)
	}
}

// TestSideDoorFills checks each side-door turn's reading fill against
// sidedoor.json: plugin and bridge output fill a turn only up to
// R_TURN_BASE_S unless a hook or transcript record backs it (DESIGN 5.2),
// and a track turn fills its base alone (DESIGN 5.4).
func TestSideDoorFills(t *testing.T) {
	for _, f := range sideDoorCases(t) {
		var sd sideDoorTurns
		readJSONFile(t, f.Dir, "sidedoor.json", &sd)
		d := scoreAll(t, fixtureDevice(f), f.Events)
		byOpener := map[string]Fill{}
		for _, fl := range d.Fills {
			if fl.OpenedBy != "" {
				byOpener[fl.OpenedBy] = fl
			}
		}
		for _, want := range sd.Turns {
			t.Run(fmt.Sprintf("%s turn at %s fills %v s of reading: %s", f.Name, want.Local, want.FillS, want.Why), func(t *testing.T) {
				got, ok := byOpener[want.OpenedBy]
				if !ok {
					t.Fatalf("no fill for the turn %s opened", want.OpenedBy)
				}
				if got.FillMS != secondsMS(want.FillS) || got.OutputMS != secondsMS(want.OutputFillS) {
					t.Errorf("fills %d ms of %d ms of output; want %d of %d", got.FillMS, got.OutputMS,
						secondsMS(want.FillS), secondsMS(want.OutputFillS))
				}
				if got.Backed != want.Backed || got.Via != want.Via || got.SessionKey != want.SessionKey || got.T != want.T {
					t.Errorf("fill %+v; want backed %v, via %s, session %s, at %d", got, want.Backed, want.Via, want.SessionKey, want.T)
				}
				if want.Via != event.ViaTrack && got.TurnKey != want.TurnKey {
					t.Errorf("turn key %s, want %s", got.TurnKey, want.TurnKey)
				}
			})
		}
		if c := f.Name; c == "sidedoor/c-plugin-sizes" || c == "sidedoor/e-bridge-lines" {
			t.Run(c+": no turn fills more than R_TURN_BASE_S = 20 s (WP-FX4)", func(t *testing.T) {
				n := 0
				for _, fl := range d.Fills {
					n++
					if fl.FillMS > 20_000 {
						t.Errorf("the turn at %d fills %d ms", fl.T, fl.FillMS)
					}
				}
				if n == 0 {
					t.Error("no fills")
				}
			})
		}
	}
}
