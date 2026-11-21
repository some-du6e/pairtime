// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"testing"

	"github.com/some-du6e/pairtime/internal/timelines"
	"github.com/some-du6e/pairtime/pkg/event"
)

// fixtureSubmission is a submission of a fixture's window and the projects
// its expected.json credits.
func fixtureSubmission(f *timelines.Fixture) Submission {
	return Submission{From: f.Expected.Window.Start.UnixMilli(), To: f.Expected.Window.End.UnixMilli(),
		Projects: slices.Sorted(maps.Keys(f.Expected.Totals.CreditedByProject)),
		Witness:  func(int64) event.Witness { return f.Expected.Witness }}
}

// TestSideDoorTotals checks each side-door case's submission totals
// against its expected.json: WP-FX4's oracle for DESIGN 6.2 D19's via
// filter, SM 5.4's level-C cap and the evidence levels.
func TestSideDoorTotals(t *testing.T) {
	for _, f := range sideDoorCases(t) {
		tot := fixtureSubmission(f).Totals(scoreAll(t, fixtureDevice(f), f.Events), f.Events)
		want := f.Expected.Totals
		t.Run(f.Name+": the totals pass pkg/event validation", func(t *testing.T) {
			if err := tot.Validate(); err != nil {
				t.Error(err)
			}
		})
		if c := f.Name; c == "sidedoor/a-track-jiggler" || c == "sidedoor/c-plugin-sizes" || c == "sidedoor/d-editor-script" {
			t.Run(c+" adds 0 to logged_s and ai_logged_s, which never count track, plugin or unverified editor records (DESIGN 6.2 D19)", func(t *testing.T) {
				if tot.LoggedS != 0 || tot.AILoggedS != 0 {
					t.Errorf("logged_s %v, ai_logged_s %v, want 0 and 0", tot.LoggedS, tot.AILoggedS)
				}
			})
		}
		for _, c := range []struct {
			key string
			got float64
		}{
			{"credited_s", tot.CreditedS},
			{"logged_s", tot.LoggedS},
			{"ai_logged_s", tot.AILoggedS},
			{"level_a_s", tot.LevelAS},
			{"level_b_s", tot.LevelBS},
			{"level_c_s", tot.LevelCS},
			{"via_track_s", tot.ViaTrackS},
			{"level_c_cap_s", LevelCCap(tot)},
		} {
			w, ok := want.Get(c.key)
			if !ok {
				continue
			}
			t.Run(fmt.Sprintf("%s's %s is %v, as its expected.json says", f.Name, c.key, w), func(t *testing.T) {
				if c.got != w {
					t.Errorf("%s = %v, want %v", c.key, c.got, w)
				}
			})
		}
	}
}

// TestFixtureSubmissionTotals checks the totals of a submission of an SM
// fixture's window against the totals SM states for it.
func TestFixtureSubmissionTotals(t *testing.T) {
	for _, f := range creditCases(t) {
		if _, ok := f.Expected.Totals.Get("ai_credited_s"); !ok {
			continue
		}
		tot := fixtureSubmission(f).Totals(scoreAll(t, fixtureDevice(f), f.Events), f.Events)
		for _, c := range []struct {
			key string
			got float64
		}{
			{"credited_s", tot.CreditedS},
			{"ai_credited_s", tot.AICreditedS},
			{"hand_credited_s", tot.HandCreditedS},
			{"agent_only_s", tot.AgentOnlyS},
		} {
			w, ok := f.Expected.Totals.Get(c.key)
			if !ok {
				continue
			}
			t.Run(fmt.Sprintf("%s's %s is %v, as SM says", f.Name, c.key, w), func(t *testing.T) {
				if c.got != w {
					t.Errorf("%s = %v, want %v", c.key, c.got, w)
				}
			})
		}
	}
	// SM 6.1's window replays to logged_s 7,712, ai_logged_s 3,930 and
	// handbook_s 5,092 under main's SM 5.3 and 5.4, as 6.1's expected.json
	// notes; SM's own "about 8,300" and "about 4,050" are estimates
	// (docs/errata/2026-11-19-sm-6.1-hackatime-replay.md). E-6.1-hackatime
	// gives 7,832, 4,050 and 5,132, once P08 lands its SM text and sets
	// hackatimeDecided.
	f := fixture(t, "6.1")
	tot := fixtureSubmission(f).Totals(scoreAll(t, fixtureDevice(f), f.Events), f.Events)
	for _, c := range []struct {
		name      string
		rule      loggedRule
		got, want float64
	}{
		{"SM 6.1's logged_s is min(gap, 120 s) over its window's heartbeats and agent events: 7,712 s (SM 5.3)", smRule, tot.LoggedS, 7712},
		{"SM 6.1's ai_logged_s is the same rule over its agent events alone: 3,930 s (SM 5.4)", smRule, tot.AILoggedS, 3930},
		{"SM 6.1's handbook_s is coding_logged_s plus a third of ai_logged_s: 5,092 s", smRule, tot.HandbookS, 5092},
		{"SM 6.1's ai_ratio is 5,965 / 3,930", smRule, ratio(tot), 5965.0 / 3930},
		{"E-6.1-hackatime: SM 6.1's logged_s counts its window from the 13:57:05 heartbeat before it: 7,832 s", decidedRule, tot.LoggedS, 7832},
		{"E-6.1-hackatime: SM 6.1's ai_logged_s gives each gap to the record that ends it: 4,050 s", decidedRule, tot.AILoggedS, 4050},
		{"E-6.1-hackatime: SM 6.1's handbook_s is 5,132 s", decidedRule, tot.HandbookS, 5132},
		{"E-6.1-hackatime: SM 6.1's ai_ratio is 5,965 / 4,050 = 1.47", decidedRule, ratio(tot), 5965.0 / 4050},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.rule.skip(t)
			if math.IsNaN(c.got) || math.Abs(c.got-c.want) > 1e-9 {
				t.Errorf("%v, want %v", c.got, c.want)
			}
		})
	}
	t.Run("SM 6.1's totals pass pkg/event validation", func(t *testing.T) {
		if err := tot.Validate(); err != nil {
			t.Error(err)
		}
	})
}

// ratio is t's ai_ratio, or NaN when it has none.
func ratio(t event.SubmissionTotals) float64 {
	if t.AIRatio == nil {
		return math.NaN()
	}
	return *t.AIRatio
}

// loggedRule says which logged_s and ai_logged_s rule a test case pins:
// both, main's SM 5.3 and 5.4 (smRule), or E-6.1-hackatime's (decidedRule).
// hackatimeDecided picks the one Totals follows, and the other's cases skip.
type loggedRule int

const (
	bothRules loggedRule = iota
	smRule
	decidedRule
)

// skip skips a case that pins the rule Totals doesn't follow.
func (r loggedRule) skip(t *testing.T) {
	t.Helper()
	switch {
	case r == decidedRule && !hackatimeDecided:
		// TODO(P08): these run once P08 sets hackatimeDecided.
		t.Skip("E-6.1-hackatime's rule waits for its SM 5.3 and 5.4 text, which P08 brings (D-sc4-sm-first, #132)")
	case r == smRule && hackatimeDecided:
		t.Skip("main's SM 5.3 and 5.4 rule, which E-6.1-hackatime replaces")
	}
}

func TestLoggedSeries(t *testing.T) {
	sub := Submission{From: at(0), To: at(600), Projects: []string{"p_1"}}
	heartbeat := func(b *tl, s int64, v event.Via, project string) {
		b.editor(at(s), v, project, event.ActEdit, event.EditSourceHuman)
	}
	edit := func(b *tl, s int64) {
		b.harness(at(s), event.ViaHook, "s_1", "p_1", &event.AgentTool{ToolClass: event.ToolEdit, Files: []string{"f_1"},
			LinesAdded: 1, TStart: at(s) - 500, TEnd: at(s)})
	}
	// The window opens with a heartbeat at 0 s, a prompt at 50 s and a
	// heartbeat at 100 s.
	promptBetween := func(b *tl) {
		heartbeat(b, 0, event.ViaEditor, "p_1")
		b.prompt(at(50), event.ViaHook, "s_1", "p_1", "t_1", 30)
		heartbeat(b, 100, event.ViaEditor, "p_1")
	}
	// An agent edit at 50 s, with a heartbeat of its file 5 s or 6 s
	// after it.
	echoAfter := func(s int64) func(b *tl) {
		return func(b *tl) {
			heartbeat(b, 0, event.ViaEditor, "p_1")
			edit(b, 50)
			heartbeat(b, 50+s, event.ViaEditor, "p_1")
		}
	}
	cases := []struct {
		name       string
		rule       loggedRule
		build      func(b *tl)
		logged, ai float64
	}{
		{"each counted record adds its gap to the one before it, at most 120 s (SM 5.3, HC 1.4)", bothRules,
			func(b *tl) {
				heartbeat(b, 0, event.ViaEditor, "p_1")
				heartbeat(b, 60, event.ViaEditor, "p_1")
				heartbeat(b, 300, event.ViaEditor, "p_1")
			},
			180, 0},
		{"the window's first record adds nothing, since SM 5.3 sums the window's records", smRule,
			func(b *tl) { heartbeat(b, -100, event.ViaEditor, "p_1"); heartbeat(b, 0, event.ViaEditor, "p_1") }, 0, 0},
		{"E-6.1-hackatime: the window's first record counts its gap from the last counted record before the window", decidedRule,
			func(b *tl) { heartbeat(b, -100, event.ViaEditor, "p_1"); heartbeat(b, 0, event.ViaEditor, "p_1") }, 100, 0},
		{"track, plugin, editor_unverified and bridge_unverified records never count (DESIGN 6.2 D19)", bothRules,
			func(b *tl) {
				heartbeat(b, 0, event.ViaEditorUnverified, "p_1")
				heartbeat(b, 60, event.ViaEditorUnverified, "p_1")
				b.harness(at(90), event.ViaPlugin, "s_9", "p_1", &event.AgentStop{TurnKey: "t_9", StopReason: event.StopEndTurn})
				b.harness(at(100), event.ViaBridgeUnverified, "s_8", "p_1", &event.AgentStop{TurnKey: "t_8", StopReason: event.StopEndTurn})
				b.track(at(110), "s_t", 30, event.LogNoReader, "")
			}, 0, 0},
		{"ai_logged_s is the logged_s rule over the agent events alone, so one prompt between two heartbeats adds nothing to it (SM 5.4)", smRule,
			promptBetween, 100, 0},
		{"E-6.1-hackatime: each gap goes to the record that ends it, to ai_logged_s at an agent event and to coding at a heartbeat", decidedRule,
			promptBetween, 100, 50},
		{"a heartbeat 5 s after an agent edit of its file is still a heartbeat, which ai_logged_s doesn't count (SM 5.4)", smRule,
			echoAfter(5), 55, 0},
		{"E-6.1-hackatime: a heartbeat within 5 s of an agent edit of its file counts as part of the edit", decidedRule,
			echoAfter(5), 55, 55},
		{"E-6.1-hackatime: a heartbeat 6 s after the agent's edit of its file is the person's", decidedRule,
			echoAfter(6), 56, 50},
		{"records of projects outside the submission don't count", bothRules,
			func(b *tl) {
				heartbeat(b, 0, event.ViaEditor, "p_1")
				heartbeat(b, 60, event.ViaEditor, "p_2")
				heartbeat(b, 90, event.ViaEditor, "p_1")
			},
			90, 0},
		{"records at or after the window's end don't count", bothRules,
			func(b *tl) { heartbeat(b, 500, event.ViaEditor, "p_1"); heartbeat(b, 600, event.ViaEditor, "p_1") }, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.rule.skip(t)
			b := newTL(false)
			c.build(b)
			tot := sub.Totals(Derived{}, b.records())
			if tot.LoggedS != c.logged || tot.AILoggedS != c.ai {
				t.Errorf("logged_s %v, ai_logged_s %v; want %v and %v", tot.LoggedS, tot.AILoggedS, c.logged, c.ai)
			}
			if err := tot.Validate(); err != nil {
				t.Error(err)
			}
		})
	}
}

// TestIdleTime checks idle_s: X.absent seconds of slots that start while a
// harness session of a submitted project is open (SM 5.3).
func TestIdleTime(t *testing.T) {
	b := newTL(false)
	b.start(at(30), "s_1", "p_1", event.EntrypointCLI, event.ParentTerminalApp, true)
	b.harness(at(150), event.ViaHook, "s_1", "p_1", &event.SessionEnd{})
	b.start(at(400), "s_2", "p_2", event.EntrypointCLI, event.ParentTerminalApp, true)
	absent := func(s int64) Credit { return Credit{Slot: at(s), Uncredited: map[event.Reason]int64{xAbsent: 15}} }
	d := Derived{Credits: []Credit{absent(0), absent(60), absent(120), absent(165), absent(405)}}
	tot := Submission{From: at(0), To: at(600), Projects: []string{"p_1"}}.Totals(d, b.records())
	if tot.IdleS != 30 {
		t.Errorf("idle_s %v, want 30: the slots at 60 s and 120 s, inside s_1's session from 30 s to its end at 150 s", tot.IdleS)
	}
}
