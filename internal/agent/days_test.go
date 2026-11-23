// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/ipc"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/agent/store"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/score"
)

// putMinutes writes final minutes to the bucket store in dirs, the first at
// at and each a minute after the one before.
func putMinutes(t *testing.T, dirs home.Dirs, at time.Time, minutes ...map[string]store.ProjectMinute) {
	t.Helper()
	bs, err := store.OpenBuckets(dirs.Buckets())
	if err != nil {
		t.Fatal(err)
	}
	defer bs.Close()
	tab := newestTable()
	for i, projects := range minutes {
		b := store.Bucket{V: score.Version(), Params: tab.Hash(), Minute: at.Add(time.Duration(i) * time.Minute).UTC().Truncate(time.Minute),
			Device: "d_other", Slots: make([]event.BucketSlot, 4), State: event.StateFinal, Projects: projects}
		for _, pm := range projects {
			for _, c := range pm.Credited {
				b.CreditedS += float64(c.Halves) / 2
			}
			b.AgentRunningS += float64(pm.AgentS)
		}
		if _, _, err := bs.Put(b); err != nil {
			t.Fatal(err)
		}
	}
}

// twoMinutes are two minutes of SM 6.2's shape: remind-bot earns 15 s of
// prompting at level B while Cursor runs 40 s, 25 of them on its own,
// then 10 s of reading at level B, and another project 5 s of hand edits
// at level C.
var twoMinutes = []map[string]store.ProjectMinute{
	{"p_remind": {Credited: []store.Credited{{Why: event.ReasonPrompt, Ev: event.EvidenceB, Halves: 30}}, AgentS: 40, AgentOnlyS: 25,
		Harnesses: map[event.Harness]int64{event.HarnessCursor: 25}, Uncredited: map[event.Reason]int64{event.ReasonAgentOnly: 25, event.ReasonAbsent: 5}}},
	{
		"p_remind": {Credited: []store.Credited{{Why: event.ReasonRead, Ev: event.EvidenceB, Halves: 20}}, Harnesses: map[event.Harness]int64{event.HarnessCursor: 0}},
		"p_other":  {Credited: []store.Credited{{Why: event.ReasonEdit, Ev: event.EvidenceC, Halves: 10}}},
	},
}

// loopMinute is a minute in which remind-bot earns 20 s of prompting,
// loses 20 s to a re-roll (SM 4.2) and 10 s to time away, and Claude Code
// runs 10 s on its own.
var loopMinute = map[string]store.ProjectMinute{
	"p_remind": {Credited: []store.Credited{{Why: event.ReasonPrompt, Ev: event.EvidenceA, Halves: 40}}, AgentS: 10, AgentOnlyS: 10,
		Harnesses:  map[event.Harness]int64{event.HarnessClaudeCode: 10},
		Uncredited: map[event.Reason]int64{event.ReasonReroll: 20, event.ReasonAbsent: 10, event.ReasonAgentOnly: 10}},
}

// startWith starts an agent on a fresh data directory, with config.toml
// holding config when it isn't "" and counting days in zone when it isn't
// nil, after prepare has filled the directory.
func startWith(t *testing.T, config string, zone *time.Location, prepare func(home.Dirs)) *running {
	t.Helper()
	dirs := home.Dirs{Data: t.TempDir(), Config: t.TempDir()}
	dirs.Runtime = dirs.Data
	if err := dirs.Create(); err != nil {
		t.Fatal(err)
	}
	if config != "" {
		if err := os.WriteFile(dirs.ConfigFile(), []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if prepare != nil {
		prepare(dirs)
	}
	return startAgent(t, Options{Dirs: dirs, Ports: []int{0}, KeyStores: []keys.Store{}, Zone: zone})
}

// get asks the agent for path and decodes its answer into each of vs.
func get(t *testing.T, a *running, path string, vs ...any) {
	t.Helper()
	code, body := request(t, "GET", fmt.Sprintf("http://127.0.0.1:%d%s", a.port.Port, path), "", "", a.port.Token)
	if code != http.StatusOK {
		t.Fatalf("GET %s = %d %s", path, code, body)
	}
	for _, v := range vs {
		if err := json.Unmarshal([]byte(body), v); err != nil {
			t.Fatalf("GET %s: %v in %s", path, err, body)
		}
	}
}

// replyTally is the part of a reply the tests read.
type replyTally struct {
	Project    string                     `json:"project"`
	CreditedS  *float64                   `json:"credited_s"`
	PendingS   *float64                   `json:"pending_s"`
	ByKind     map[event.Activity]float64 `json:"credited_by_kind"`
	ByEvidence map[event.Evidence]float64 `json:"credited_by_evidence"`
	ByReason   map[event.Reason]float64   `json:"credited_by_reason"`
	AgentOnlyS float64                    `json:"agent_only_s"`
	ByHarness  map[event.Harness]float64  `json:"agent_only_by_harness"`
	Uncredited map[event.Reason]float64   `json:"uncredited"`
	OnHoldS    *float64                   `json:"on_hold_s"`
}

type replyDay struct {
	Date     string       `json:"date"`
	Start    time.Time    `json:"start"`
	End      time.Time    `json:"end"`
	Shadow   bool         `json:"shadow"`
	Projects []replyTally `json:"projects"`
}

// shadowHides are the fields of a tally that display.shadow leaves out
// (D-shadow-scope).
var shadowHides = []string{"credited_s", "pending_s", "credited_by_kind", "credited_by_evidence", "credited_by_reason", "on_hold_s"}

// hidesAll reports, through t, every one of keys that obj holds.
func hidesAll(t *testing.T, where string, obj any, keys ...string) {
	t.Helper()
	m, ok := obj.(map[string]any)
	if !ok {
		t.Fatalf("%s is %T, not an object", where, obj)
	}
	for _, k := range keys {
		if v, ok := m[k]; ok {
			t.Errorf("%s shows %s, %v, while display.shadow is on", where, k, v)
		}
	}
}

// skipShadowAfterFreeze skips a case whose config.toml turns
// display.shadow on once pairtime's scoring has reached 1.0.0, when Parse
// reads that line as off (config.Calibrating): nobody sees the views it
// checks any more, and WP-CA3 can delete those cases.
func skipShadowAfterFreeze(t *testing.T, configFile string) {
	t.Helper()
	if strings.Contains(configFile, "shadow = true") && !config.Calibrating() {
		t.Skip("pairtime's scoring has reached 1.0.0, so display.shadow is off whatever config.toml says")
	}
}

func TestDays(t *testing.T) {
	today := time.Now().Format(time.DateOnly)
	todayStart, _, err := store.DayBounds(today, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		config string
		// santiago has the agent count days in America/Santiago, where the
		// clocks skip midnight on 2027-09-05.
		santiago bool
		prepare  func(t *testing.T, dirs home.Dirs)
		path     string
		check    func(t *testing.T, d replyDay, raw map[string]any)
	}{
		{
			name:    "today comes per project by kind, level and reason, with the agent's time on its own by harness and the uncredited reasons",
			prepare: func(t *testing.T, dirs home.Dirs) { putMinutes(t, dirs, todayStart.Add(2*time.Hour), twoMinutes...) },
			path:    "/v1/days/today",
			check: func(t *testing.T, d replyDay, _ map[string]any) {
				if d.Date != today || len(d.Projects) != 2 || d.Projects[0].Project != "p_other" || d.Projects[1].Project != "p_remind" {
					t.Fatalf("day %+v, want today with p_other and p_remind", d)
				}
				r := d.Projects[1]
				if r.CreditedS == nil || *r.CreditedS != 25 || r.ByEvidence[event.EvidenceB] != 25 || len(r.ByEvidence) != 1 {
					t.Errorf("p_remind credited %v by level %v, want 25 s, all level B", r.CreditedS, r.ByEvidence)
				}
				if r.ByKind[event.ActivityPrompting] != 15 || r.ByKind[event.ActivityReadingOutput] != 10 {
					t.Errorf("p_remind by kind %v", r.ByKind)
				}
				if want := map[event.Reason]float64{event.ReasonPrompt: 15, event.ReasonRead: 10}; !maps.Equal(r.ByReason, want) {
					t.Errorf("p_remind by reason %v, want %v", r.ByReason, want)
				}
				if r.AgentOnlyS != 25 || r.ByHarness[event.HarnessCursor] != 25 || len(r.ByHarness) != 1 {
					t.Errorf("p_remind agent-only %v s by harness %v, want 25 s of Cursor", r.AgentOnlyS, r.ByHarness)
				}
				if r.Uncredited[event.ReasonAgentOnly] != 25 || r.Uncredited[event.ReasonAbsent] != 5 {
					t.Errorf("p_remind uncredited %v", r.Uncredited)
				}
				if r.OnHoldS != nil {
					t.Errorf("p_remind has %v s on hold with no X.noprog", *r.OnHoldS)
				}
				o := d.Projects[0]
				if o.CreditedS == nil || *o.CreditedS != 5 || o.ByEvidence[event.EvidenceC] != 5 || o.ByHarness == nil || len(o.ByHarness) != 0 {
					t.Errorf("p_other %+v, want 5 s at level C and no harness", o)
				}
			},
		},
		{
			name: "today is answered from its stored rollup in days/",
			prepare: func(t *testing.T, dirs home.Dirs) {
				start, end, _ := store.DayBounds(today, time.Local)
				err := store.WriteDay(dirs.Days(), store.Day{Date: today, Zone: "Local", Start: start.UTC(), End: end.UTC(),
					Projects: []store.DayProject{{Project: "p_stored", Tally: store.Tally{CreditedS: 42,
						CreditedByKind: map[event.Activity]float64{event.ActivityHandEdit: 42}, CreditedByEvidence: map[event.Evidence]float64{event.EvidenceA: 42},
						CreditedByReason:   map[event.Reason]float64{event.ReasonEdit: 42},
						AgentOnlyByHarness: map[event.Harness]float64{event.HarnessClaudeCode: 0}, Uncredited: map[event.Reason]float64{}, OnHoldS: 3}}}})
				if err != nil {
					t.Fatal(err)
				}
			},
			path: "/v1/days/today",
			check: func(t *testing.T, d replyDay, _ map[string]any) {
				if len(d.Projects) != 1 || d.Projects[0].Project != "p_stored" || *d.Projects[0].CreditedS != 42 {
					t.Fatalf("day %+v, want the stored rollup's p_stored with 42 s", d)
				}
				p := d.Projects[0]
				if h, ok := p.ByHarness[event.HarnessClaudeCode]; !ok || h != 0 {
					t.Errorf("by harness %v, want claude_code listed with 0", p.ByHarness)
				}
				if p.OnHoldS == nil || *p.OnHoldS != 3 || p.ByReason[event.ReasonEdit] != 42 {
					t.Errorf("on hold %v, by reason %v; want 3 s and 42 s of E", p.OnHoldS, p.ByReason)
				}
			},
		},
		{
			name: "a day asked for by its date is that day",
			prepare: func(t *testing.T, dirs home.Dirs) {
				putMinutes(t, dirs, time.Date(2026, 11, 24, 15, 0, 0, 0, time.Local), twoMinutes...)
			},
			path: "/v1/days/2026-11-24",
			check: func(t *testing.T, d replyDay, _ map[string]any) {
				if d.Date != "2026-11-24" || len(d.Projects) != 2 {
					t.Errorf("day %+v, want 2026-11-24 with two projects", d)
				}
			},
		},
		{
			name:   "with display.shadow on a day summed from its buckets keeps the agent's time and leaves out your time and the uncredited time that isn't a loop",
			config: "[display]\nshadow = true\n",
			prepare: func(t *testing.T, dirs home.Dirs) {
				putMinutes(t, dirs, todayStart.Add(2*time.Hour), twoMinutes...)
			},
			path: "/v1/days/today",
			check: func(t *testing.T, d replyDay, raw map[string]any) {
				if !d.Shadow || len(d.Projects) != 2 {
					t.Fatalf("day %+v, want shadow and two projects", d)
				}
				for i, p := range raw["projects"].([]any) {
					hidesAll(t, d.Projects[i].Project, p, shadowHides...)
				}
				r := d.Projects[1]
				if r.AgentOnlyS != 25 || r.ByHarness[event.HarnessCursor] != 25 || r.Uncredited == nil || len(r.Uncredited) != 0 {
					t.Errorf("p_remind %+v, want Cursor's 25 s and no uncredited code", r)
				}
			},
		},
		{
			name:   "with display.shadow on a stored day keeps SM 4.2's loops alone of its uncredited codes, X.day_cap left out",
			config: "[display]\nshadow = true\n",
			prepare: func(t *testing.T, dirs home.Dirs) {
				start, end, _ := store.DayBounds(today, time.Local)
				err := store.WriteDay(dirs.Days(), store.Day{Date: today, Zone: "Local", Start: start.UTC(), End: end.UTC(),
					Projects: []store.DayProject{{Project: "p_capped", Tally: store.Tally{CreditedS: 43200, PendingS: 30,
						CreditedByKind:     map[event.Activity]float64{event.ActivityPrompting: 43200},
						CreditedByEvidence: map[event.Evidence]float64{event.EvidenceA: 43200},
						CreditedByReason:   map[event.Reason]float64{event.ReasonPrompt: 43200},
						AgentOnlyS:         20, AgentOnlyByHarness: map[event.Harness]float64{event.HarnessClaudeCode: 20},
						Uncredited: map[event.Reason]float64{event.ReasonDayCap: 40, event.ReasonReroll: 15, event.ReasonSameError: 5,
							event.ReasonAbsent: 5, event.ReasonAgentOnly: 20, event.ReasonNoProgress: 9},
						OnHoldS: 9}}}})
				if err != nil {
					t.Fatal(err)
				}
			},
			path: "/v1/days/today",
			check: func(t *testing.T, d replyDay, raw map[string]any) {
				if !d.Shadow || len(d.Projects) != 1 {
					t.Fatalf("day %+v, want shadow and one project", d)
				}
				hidesAll(t, "p_capped", raw["projects"].([]any)[0], shadowHides...)
				p := d.Projects[0]
				if want := map[event.Reason]float64{event.ReasonReroll: 15, event.ReasonSameError: 5}; !maps.Equal(p.Uncredited, want) {
					t.Errorf("uncredited %v, want the loops alone, %v", p.Uncredited, want)
				}
				if p.AgentOnlyS != 20 || p.ByHarness[event.HarnessClaudeCode] != 20 {
					t.Errorf("agent time %v s by harness %v, want Claude Code's 20 s", p.AgentOnlyS, p.ByHarness)
				}
			},
		},
		{
			name:     "a day whose midnight the clocks skip is a day of 23 hours from 01:00, as 2027-09-05 is in Santiago",
			santiago: true,
			prepare: func(t *testing.T, dirs home.Dirs) {
				putMinutes(t, dirs, time.Date(2027, 9, 5, 3, 59, 0, 0, time.UTC), twoMinutes...)
			},
			path: "/v1/days/2027-09-05",
			check: func(t *testing.T, d replyDay, _ map[string]any) {
				if !d.Start.Equal(time.Date(2027, 9, 5, 4, 0, 0, 0, time.UTC)) || !d.End.Equal(time.Date(2027, 9, 6, 3, 0, 0, 0, time.UTC)) {
					t.Errorf("2027-09-05 runs %s to %s, want 04:00Z to 03:00Z the next day", d.Start, d.End)
				}
				if len(d.Projects) != 2 || *d.Projects[1].CreditedS != 10 {
					t.Errorf("projects %+v, want the 04:00Z minute's two, remind-bot with 10 s", d.Projects)
				}
			},
		},
		{
			name:     "the day before a skipped midnight keeps its last hour, as 2027-09-04 does in Santiago",
			santiago: true,
			prepare: func(t *testing.T, dirs home.Dirs) {
				putMinutes(t, dirs, time.Date(2027, 9, 5, 3, 59, 0, 0, time.UTC), twoMinutes...)
			},
			path: "/v1/days/2027-09-04",
			check: func(t *testing.T, d replyDay, _ map[string]any) {
				if !d.Start.Equal(time.Date(2027, 9, 4, 4, 0, 0, 0, time.UTC)) || !d.End.Equal(time.Date(2027, 9, 5, 4, 0, 0, 0, time.UTC)) {
					t.Errorf("2027-09-04 runs %s to %s, want 04:00Z to 04:00Z the next day", d.Start, d.End)
				}
				if len(d.Projects) != 1 || *d.Projects[0].CreditedS != 15 {
					t.Errorf("projects %+v, want the 03:59Z minute's remind-bot with 15 s", d.Projects)
				}
			},
		},
		{
			name: "an empty day has no projects",
			path: "/v1/days/today",
			check: func(t *testing.T, d replyDay, _ map[string]any) {
				if d.Date != today || d.Projects == nil || len(d.Projects) != 0 {
					t.Errorf("day %+v, want today with an empty list", d)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			skipShadowAfterFreeze(t, tt.config)
			var zone *time.Location
			if tt.santiago {
				loc, err := time.LoadLocation("America/Santiago")
				if err != nil {
					t.Skip("no time zone data:", err)
				}
				zone = loc
			}
			a := startWith(t, tt.config, zone, func(dirs home.Dirs) {
				if tt.prepare != nil {
					tt.prepare(t, dirs)
				}
			})
			var d replyDay
			var raw map[string]any
			get(t, a, tt.path, &d, &raw)
			tt.check(t, d, raw)
		})
	}
}

func TestLastSession(t *testing.T) {
	type session struct {
		replyTally
		Date  string     `json:"date"`
		Start *time.Time `json:"start"`
		End   *time.Time `json:"end"`
		Open  *bool      `json:"open"`
	}
	type reply struct {
		Shadow  bool     `json:"shadow"`
		Session *session `json:"session"`
	}
	now := time.Now().UTC().Truncate(time.Minute)
	tests := []struct {
		name    string
		config  string
		prepare func(t *testing.T, dirs home.Dirs)
		check   func(t *testing.T, r reply, raw map[string]any)
	}{
		{
			name: "the last session is the latest run of credited minutes in one project, with its date and tally",
			prepare: func(t *testing.T, dirs home.Dirs) {
				// An earlier session, then 21 minutes without credit, then
				// the last one, whose two minutes are a minute apart.
				putMinutes(t, dirs, now.Add(-3*time.Hour), twoMinutes[:1]...)
				putMinutes(t, dirs, now.Add(-2*time.Hour), twoMinutes[:1]...)
				putMinutes(t, dirs, now.Add(-2*time.Hour+2*time.Minute), twoMinutes[1:]...)
			},
			check: func(t *testing.T, r reply, _ map[string]any) {
				s := r.Session
				if s == nil {
					t.Fatal("no session")
				}
				if s.Project != "p_remind" || s.Start == nil || s.End == nil || !s.Start.Equal(now.Add(-2*time.Hour)) || !s.End.Equal(now.Add(-2*time.Hour+3*time.Minute)) {
					t.Errorf("session %s from %v to %v", s.Project, s.Start, s.End)
				}
				if want := now.Add(-2 * time.Hour).Format(time.DateOnly); s.Date != want {
					t.Errorf("session date %q, want %q", s.Date, want)
				}
				if s.CreditedS == nil || *s.CreditedS != 25 || s.AgentOnlyS != 25 || s.ByHarness[event.HarnessCursor] != 25 {
					t.Errorf("session tally %+v, want 25 s credited and Cursor's 25 s", s.replyTally)
				}
				if s.Open == nil || *s.Open {
					t.Errorf("a session two hours old has open %v, want false", s.Open)
				}
			},
		},
		{
			name: "a session with credit in the last SESSION_GAP_S is still going",
			prepare: func(t *testing.T, dirs home.Dirs) {
				putMinutes(t, dirs, now.Add(-5*time.Minute), twoMinutes...)
			},
			check: func(t *testing.T, r reply, _ map[string]any) {
				if r.Session == nil || r.Session.Open == nil || !*r.Session.Open {
					t.Errorf("session %+v, want one still going", r.Session)
				}
			},
		},
		{
			name:   "with display.shadow on the last session is its date and project, the agent's time and the loops, with no start, end or open",
			config: "[display]\nshadow = true\n",
			prepare: func(t *testing.T, dirs home.Dirs) {
				putMinutes(t, dirs, now.Add(-5*time.Minute), loopMinute)
			},
			check: func(t *testing.T, r reply, raw map[string]any) {
				s := r.Session
				if !r.Shadow || s == nil || s.Project != "p_remind" {
					t.Fatalf("reply %+v, want shadow and remind-bot's session", r)
				}
				if want := now.Add(-5 * time.Minute).Format(time.DateOnly); s.Date != want {
					t.Errorf("session date %q, want %q", s.Date, want)
				}
				hidesAll(t, "the session", raw["session"], append([]string{"start", "end", "open"}, shadowHides...)...)
				if want := map[event.Reason]float64{event.ReasonReroll: 20}; !maps.Equal(s.Uncredited, want) {
					t.Errorf("uncredited %v, want the re-roll alone, %v", s.Uncredited, want)
				}
				if s.AgentOnlyS != 10 || s.ByHarness[event.HarnessClaudeCode] != 10 {
					t.Errorf("agent time %v s by harness %v, want Claude Code's 10 s", s.AgentOnlyS, s.ByHarness)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			skipShadowAfterFreeze(t, tt.config)
			a := startWith(t, tt.config, time.UTC, func(dirs home.Dirs) { tt.prepare(t, dirs) })
			var r reply
			var raw map[string]any
			get(t, a, "/v1/sessions/last", &r, &raw)
			tt.check(t, r, raw)
		})
	}
}

func TestAnswerQuestion(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	tests := []struct {
		name     string
		shadow   bool
		question string
		check    func(t *testing.T, ans json.RawMessage, err error, want []byte)
	}{
		{
			name: "sessions/last is answered with what GET /v1/sessions/last gives",
			check: func(t *testing.T, ans json.RawMessage, err error, want []byte) {
				if err != nil || string(ans) != string(want) {
					t.Errorf("answer %s (%v), want %s", ans, err, want)
				}
			},
		},
		{
			name:   "with display.shadow on sessions/last is answered by the same rule as GET /v1/sessions/last",
			shadow: true,
			check: func(t *testing.T, ans json.RawMessage, err error, want []byte) {
				var raw map[string]any
				if err != nil || json.Unmarshal(ans, &raw) != nil || string(ans) != string(want) {
					t.Fatalf("answer %s (%v), want %s", ans, err, want)
				}
				hidesAll(t, "the answer's session", raw["session"], append([]string{"start", "end", "open"}, shadowHides...)...)
			},
		},
		{
			name:     "a question the agent has no answer for is refused",
			question: "days/today",
			check: func(t *testing.T, ans json.RawMessage, err error, _ []byte) {
				if !errors.Is(err, ipc.ErrUnknownQuestion) || ans != nil {
					t.Errorf("answer %s, %v; want ipc.ErrUnknownQuestion", ans, err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dirs := home.Dirs{Data: t.TempDir(), Config: t.TempDir()}
			dirs.Runtime = dirs.Data
			if err := dirs.Create(); err != nil {
				t.Fatal(err)
			}
			putMinutes(t, dirs, now.Add(-5*time.Minute), loopMinute)
			bs, err := store.OpenBuckets(dirs.Buckets())
			if err != nil {
				t.Fatal(err)
			}
			defer bs.Close()
			a := &agent{table: newestTable(), loc: time.UTC, buckets: bs, log: slog.New(slog.DiscardHandler)}
			cfg := config.Default()
			cfg.Display.Shadow = tt.shadow
			a.cfg.Store(&cfg)
			q := tt.question
			if q == "" {
				q = ipc.AskLastSession
			}
			reply, err := a.lastSession(time.Now())
			if err != nil {
				t.Fatal(err)
			}
			want, err := json.Marshal(reply)
			if err != nil {
				t.Fatal(err)
			}
			ans, err := a.answerQuestion(q)
			tt.check(t, ans, err, want)
		})
	}
}

func TestSessionsLastOverTheSocket(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	tests := []struct {
		name   string
		config string
	}{
		{"sessions/last over the agent's socket gets what GET /v1/sessions/last gives", ""},
		{"with display.shadow on, sessions/last over the socket leaves out what GET /v1/sessions/last does", "[display]\nshadow = true\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			skipShadowAfterFreeze(t, tt.config)
			// A short path, which the socket's name needs.
			dir, err := os.MkdirTemp("", "pt")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(dir) })
			dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
			if err := dirs.Create(); err != nil {
				t.Fatal(err)
			}
			if tt.config != "" {
				if err := os.WriteFile(dirs.ConfigFile(), []byte(tt.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			putMinutes(t, dirs, now.Add(-5*time.Minute), loopMinute)
			a := startAgent(t, Options{Dirs: dirs, Ports: []int{0}, KeyStores: []keys.Store{}, Zone: time.UTC})
			ep, err := ipc.EndpointFor(dirs)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			ans, err := ipc.Ask(ctx, ep, ipc.AskLastSession)
			if err != nil {
				t.Fatalf("Ask: %v", err)
			}
			var overSocket, overHTTP map[string]any
			if err := json.Unmarshal(ans, &overSocket); err != nil {
				t.Fatalf("the answer %s: %v", ans, err)
			}
			get(t, a, "/v1/sessions/last", &overHTTP)
			if !reflect.DeepEqual(overSocket, overHTTP) {
				t.Errorf("over the socket %v,\nover HTTP %v", overSocket, overHTTP)
			}
			if overSocket["session"] == nil {
				t.Error("no session over the socket")
			}
		})
	}
}

func TestSessionOpen(t *testing.T) {
	const gap = 1200 * 1000 // SESSION_GAP_S
	// The last credited minute is 10:00 to 10:01, and its credit stopped at
	// 10:00:45.
	end := time.Date(2026, 11, 24, 10, 1, 0, 0, time.UTC)
	s := &store.Session{End: end, CreditUntil: end.Add(-15 * time.Second)}
	at := func(d time.Duration) int64 { return end.Add(d).UnixMilli() }
	tests := []struct {
		name           string
		now, lastBreak int64
		open           bool
	}{
		{"a session with no lock since its credit stopped goes on within SESSION_GAP_S of its end", at(5 * time.Minute), 0, true},
		{"a lock inside the last credited minute after the credit stopped ends the session", at(time.Minute), at(-10 * time.Second), false},
		{"a lock at the instant the credit stopped ends the session", at(time.Minute), at(-15 * time.Second), false},
		{"a lock before the credit stopped doesn't end the session", at(time.Minute), at(-30 * time.Second), true},
		{"a session goes on SESSION_GAP_S after its last credited minute ends", at(20 * time.Minute), 0, true},
		{"a session is over past SESSION_GAP_S after its last credited minute ends", at(20*time.Minute + time.Millisecond), 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sessionOpen(s, tt.now, gap, tt.lastBreak); got != tt.open {
				t.Errorf("open = %v, want %v", got, tt.open)
			}
		})
	}
}
