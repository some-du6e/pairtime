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
	// docs/errata/2026-11-19-sm-6.1-hackatime-replay.md's decision
	// (E-6.1-hackatime) pins SM 6.1's replay: logged_s 7,832, ai_logged_s
	// 4,050, handbook_s 5,132 and ai_ratio 5,965 / 4,050 = 1.47.
	f := fixture(t, "6.1")
	tot := fixtureSubmission(f).Totals(scoreAll(t, fixtureDevice(f), f.Events), f.Events)
	for _, c := range []struct {
		name      string
		got, want float64
	}{
		{"SM 6.1's logged_s counts its window from the 13:57:05 heartbeat before it: 7,832 s (SM 5.3)", tot.LoggedS, 7832},
		{"SM 6.1's ai_logged_s gives each gap to the record that ends it: 4,050 s (SM 5.4)", tot.AILoggedS, 4050},
		{"SM 6.1's handbook_s is coding_logged_s plus a third of ai_logged_s: 5,132 s", tot.HandbookS, 5132},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("%v, want %v", c.got, c.want)
			}
		})
	}
	t.Run("SM 6.1's ai_ratio is 5,965 / 4,050 = 1.47, and its totals pass pkg/event validation", func(t *testing.T) {
		if tot.AIRatio == nil || math.Abs(*tot.AIRatio-5965.0/4050) > 1e-9 {
			t.Errorf("ai_ratio %v", tot.AIRatio)
		}
		if err := tot.Validate(); err != nil {
			t.Error(err)
		}
	})
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
	cases := []struct {
		name       string
		build      func(b *tl)
		logged, ai float64
	}{
		{"each counted record adds its gap to the one before it, at most 120 s (SM 5.3, HC 1.4)",
			func(b *tl) {
				heartbeat(b, 0, event.ViaEditor, "p_1")
				heartbeat(b, 60, event.ViaEditor, "p_1")
				heartbeat(b, 300, event.ViaEditor, "p_1")
			},
			180, 0},
		{"the window's first record counts its gap from the last counted record before the window (SM 5.3)",
			func(b *tl) { heartbeat(b, -100, event.ViaEditor, "p_1"); heartbeat(b, 0, event.ViaEditor, "p_1") }, 100, 0},
		{"track, plugin, editor_unverified and bridge_unverified records never count (DESIGN 6.2 D19)",
			func(b *tl) {
				heartbeat(b, 0, event.ViaEditorUnverified, "p_1")
				heartbeat(b, 60, event.ViaEditorUnverified, "p_1")
				b.harness(at(90), event.ViaPlugin, "s_9", "p_1", &event.AgentStop{TurnKey: "t_9", StopReason: event.StopEndTurn})
				b.harness(at(100), event.ViaBridgeUnverified, "s_8", "p_1", &event.AgentStop{TurnKey: "t_8", StopReason: event.StopEndTurn})
				b.track(at(110), "s_t", 30, event.LogNoReader, "")
			}, 0, 0},
		{"each gap goes to the record that ends it: to ai_logged_s at an agent event, to coding at a heartbeat (SM 5.4)",
			func(b *tl) {
				heartbeat(b, 0, event.ViaEditor, "p_1")
				b.prompt(at(50), event.ViaHook, "s_1", "p_1", "t_1", 30)
				heartbeat(b, 100, event.ViaEditor, "p_1")
			}, 100, 50},
		{"a heartbeat within 5 s of an agent edit of its file counts as part of the edit (SM 5.4)",
			func(b *tl) {
				heartbeat(b, 0, event.ViaEditor, "p_1")
				edit(b, 50)
				heartbeat(b, 55, event.ViaEditor, "p_1")
			}, 55, 55},
		{"a heartbeat 6 s after the agent's edit of its file is the person's",
			func(b *tl) {
				heartbeat(b, 0, event.ViaEditor, "p_1")
				edit(b, 50)
				heartbeat(b, 56, event.ViaEditor, "p_1")
			}, 56, 50},
		{"records of projects outside the submission don't count",
			func(b *tl) {
				heartbeat(b, 0, event.ViaEditor, "p_1")
				heartbeat(b, 60, event.ViaEditor, "p_2")
				heartbeat(b, 90, event.ViaEditor, "p_1")
			},
			90, 0},
		{"records at or after the window's end don't count",
			func(b *tl) { heartbeat(b, 500, event.ViaEditor, "p_1"); heartbeat(b, 600, event.ViaEditor, "p_1") }, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
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
