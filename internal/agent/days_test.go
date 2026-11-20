// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/agent/store"
	"github.com/some-du6e/pairtime/pkg/event"
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
		b := store.Bucket{V: tab.Version(), Params: tab.Hash(), Minute: at.Add(time.Duration(i) * time.Minute).UTC().Truncate(time.Minute),
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

// startWith starts an agent on a fresh data directory, with config.toml
// holding config when it isn't "", after prepare has filled the directory.
func startWith(t *testing.T, config string, prepare func(home.Dirs)) *running {
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
	return startAgent(t, Options{Dirs: dirs, Ports: []int{0}, KeyStores: []keys.Store{}})
}

func get(t *testing.T, a *running, path string, v any) {
	t.Helper()
	code, body := request(t, "GET", fmt.Sprintf("http://127.0.0.1:%d%s", a.port.Port, path), "", "", a.port.Token)
	if code != http.StatusOK {
		t.Fatalf("GET %s = %d %s", path, code, body)
	}
	if err := json.Unmarshal([]byte(body), v); err != nil {
		t.Fatalf("GET %s: %v in %s", path, err, body)
	}
}

// replyTally is the part of a reply the tests read.
type replyTally struct {
	Project    string                     `json:"project"`
	CreditedS  *float64                   `json:"credited_s"`
	PendingS   *float64                   `json:"pending_s"`
	ByKind     map[event.Activity]float64 `json:"credited_by_kind"`
	ByEvidence map[event.Evidence]float64 `json:"credited_by_evidence"`
	AgentOnlyS float64                    `json:"agent_only_s"`
	ByHarness  map[event.Harness]float64  `json:"agent_only_by_harness"`
	Uncredited map[event.Reason]float64   `json:"uncredited"`
}

type replyDay struct {
	Date     string       `json:"date"`
	Shadow   bool         `json:"shadow"`
	Projects []replyTally `json:"projects"`
}

func TestDays(t *testing.T) {
	today := time.Now().Format(time.DateOnly)
	todayStart, _, err := store.DayBounds(today, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		config  string
		prepare func(t *testing.T, dirs home.Dirs)
		path    string
		check   func(t *testing.T, d replyDay)
	}{
		{
			name:    "today comes per project by kind and level, with the agent's time on its own by harness and the uncredited reasons",
			prepare: func(t *testing.T, dirs home.Dirs) { putMinutes(t, dirs, todayStart.Add(2*time.Hour), twoMinutes...) },
			path:    "/v1/days/today",
			check: func(t *testing.T, d replyDay) {
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
				if r.AgentOnlyS != 25 || r.ByHarness[event.HarnessCursor] != 25 || len(r.ByHarness) != 1 {
					t.Errorf("p_remind agent-only %v s by harness %v, want 25 s of Cursor", r.AgentOnlyS, r.ByHarness)
				}
				if r.Uncredited[event.ReasonAgentOnly] != 25 || r.Uncredited[event.ReasonAbsent] != 5 {
					t.Errorf("p_remind uncredited %v", r.Uncredited)
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
						AgentOnlyByHarness: map[event.Harness]float64{event.HarnessClaudeCode: 0}, Uncredited: map[event.Reason]float64{}}}}})
				if err != nil {
					t.Fatal(err)
				}
			},
			path: "/v1/days/today",
			check: func(t *testing.T, d replyDay) {
				if len(d.Projects) != 1 || d.Projects[0].Project != "p_stored" || *d.Projects[0].CreditedS != 42 {
					t.Fatalf("day %+v, want the stored rollup's p_stored with 42 s", d)
				}
				if h, ok := d.Projects[0].ByHarness[event.HarnessClaudeCode]; !ok || h != 0 {
					t.Errorf("by harness %v, want claude_code listed with 0", d.Projects[0].ByHarness)
				}
			},
		},
		{
			name: "a day asked for by its date is that day",
			prepare: func(t *testing.T, dirs home.Dirs) {
				putMinutes(t, dirs, time.Date(2026, 11, 24, 15, 0, 0, 0, time.Local), twoMinutes...)
			},
			path: "/v1/days/2026-11-24",
			check: func(t *testing.T, d replyDay) {
				if d.Date != "2026-11-24" || len(d.Projects) != 2 {
					t.Errorf("day %+v, want 2026-11-24 with two projects", d)
				}
			},
		},
		{
			name:   "with display.shadow on a day leaves out every credited number and keeps the agent's time and the uncredited reasons",
			config: "[display]\nshadow = true\n",
			prepare: func(t *testing.T, dirs home.Dirs) {
				putMinutes(t, dirs, todayStart.Add(2*time.Hour), twoMinutes...)
			},
			path: "/v1/days/today",
			check: func(t *testing.T, d replyDay) {
				if !d.Shadow || len(d.Projects) != 2 {
					t.Fatalf("day %+v, want shadow and two projects", d)
				}
				for _, p := range d.Projects {
					if p.CreditedS != nil || p.PendingS != nil || p.ByKind != nil || p.ByEvidence != nil {
						t.Errorf("%s shows credit: %+v", p.Project, p)
					}
				}
				if r := d.Projects[1]; r.AgentOnlyS != 25 || r.Uncredited[event.ReasonAbsent] != 5 {
					t.Errorf("p_remind %+v, want its 25 s of agent time and 5 s away", r)
				}
			},
		},
		{
			name: "an empty day has no projects",
			path: "/v1/days/today",
			check: func(t *testing.T, d replyDay) {
				if d.Date != today || d.Projects == nil || len(d.Projects) != 0 {
					t.Errorf("day %+v, want today with an empty list", d)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := startWith(t, tt.config, func(dirs home.Dirs) {
				if tt.prepare != nil {
					tt.prepare(t, dirs)
				}
			})
			var d replyDay
			get(t, a, tt.path, &d)
			tt.check(t, d)
		})
	}
}

func TestLastSession(t *testing.T) {
	type session struct {
		replyTally
		Start time.Time `json:"start"`
		End   time.Time `json:"end"`
		Open  bool      `json:"open"`
	}
	type reply struct {
		Shadow  bool     `json:"shadow"`
		Session *session `json:"session"`
	}
	now := time.Now().UTC().Truncate(time.Minute)
	tests := []struct {
		name    string
		prepare func(t *testing.T, dirs home.Dirs)
		check   func(t *testing.T, r reply)
	}{
		{
			name: "the last session is the latest run of credited minutes in one project, with its tally",
			prepare: func(t *testing.T, dirs home.Dirs) {
				// An earlier session, then 21 minutes without credit, then
				// the last one, whose two minutes are a minute apart.
				putMinutes(t, dirs, now.Add(-3*time.Hour), twoMinutes[:1]...)
				putMinutes(t, dirs, now.Add(-2*time.Hour), twoMinutes[:1]...)
				putMinutes(t, dirs, now.Add(-2*time.Hour+2*time.Minute), twoMinutes[1:]...)
			},
			check: func(t *testing.T, r reply) {
				s := r.Session
				if s == nil {
					t.Fatal("no session")
				}
				if s.Project != "p_remind" || !s.Start.Equal(now.Add(-2*time.Hour)) || !s.End.Equal(now.Add(-2*time.Hour+3*time.Minute)) {
					t.Errorf("session %s from %s to %s", s.Project, s.Start, s.End)
				}
				if s.CreditedS == nil || *s.CreditedS != 25 || s.AgentOnlyS != 25 || s.ByHarness[event.HarnessCursor] != 25 {
					t.Errorf("session tally %+v, want 25 s credited and Cursor's 25 s", s.replyTally)
				}
				if s.Open {
					t.Error("a session two hours old is open")
				}
			},
		},
		{
			name: "a session with credit in the last SESSION_GAP_S is still going",
			prepare: func(t *testing.T, dirs home.Dirs) {
				putMinutes(t, dirs, now.Add(-5*time.Minute), twoMinutes...)
			},
			check: func(t *testing.T, r reply) {
				if r.Session == nil || !r.Session.Open {
					t.Errorf("session %+v, want one still going", r.Session)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := startWith(t, "", func(dirs home.Dirs) { tt.prepare(t, dirs) })
			var r reply
			get(t, a, "/v1/sessions/last", &r)
			tt.check(t, r)
		})
	}
}
