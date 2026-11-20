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

// todayBuckets writes two final minutes of today, local time, to the
// bucket store in dirs: p_a earns 15 s at level A and 10 s at level B
// while its agent runs 40 s, and p_b earns 5 s at level C.
func todayBuckets(t *testing.T, dirs home.Dirs) {
	t.Helper()
	start, _, err := store.DayBounds(time.Now().Format(time.DateOnly), time.Local)
	if err != nil {
		t.Fatal(err)
	}
	bs, err := store.OpenBuckets(dirs.Buckets())
	if err != nil {
		t.Fatal(err)
	}
	defer bs.Close()
	tab := newestTable()
	at := start.Add(2 * time.Hour).UTC()
	for i, projects := range []map[string]store.ProjectMinute{
		{"p_a": {Credited: []store.Credited{{Why: event.ReasonPrompt, Ev: event.EvidenceA, Halves: 30}}, AgentS: 40, AgentOnlyS: 25}},
		{
			"p_a": {Credited: []store.Credited{{Why: event.ReasonRead, Ev: event.EvidenceB, Halves: 20}}},
			"p_b": {Credited: []store.Credited{{Why: event.ReasonEdit, Ev: event.EvidenceC, Halves: 10}}},
		},
	} {
		b := store.Bucket{V: tab.Version(), Params: tab.Hash(), Minute: at.Add(time.Duration(i) * time.Minute), Device: "d_other",
			Slots: make([]event.BucketSlot, 4), State: event.StateFinal, Projects: projects}
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

func TestToday(t *testing.T) {
	type project struct {
		Key       string             `json:"project_key"`
		CreditedS *float64           `json:"credited_s"`
		ByLevel   map[string]float64 `json:"by_level"`
		AgentS    int64              `json:"agent_s"`
		AgentOnly int64              `json:"agent_only_s"`
	}
	type reply struct {
		Date      string             `json:"date"`
		Shadow    bool               `json:"shadow"`
		CreditedS *float64           `json:"credited_s"`
		ByLevel   map[string]float64 `json:"by_level"`
		Agent     float64            `json:"agent_running_s"`
		Projects  []project          `json:"projects"`
	}
	tests := []struct {
		name   string
		config string
		check  func(t *testing.T, r reply)
	}{
		{
			name: "today's credited seconds come by project and by evidence level, with the agent's time",
			check: func(t *testing.T, r reply) {
				if r.Shadow || r.CreditedS == nil || *r.CreditedS != 30 || r.Agent != 40 {
					t.Fatalf("shadow %v, credited %v, agent %v; want no shadow, 30 s and 40 s", r.Shadow, r.CreditedS, r.Agent)
				}
				if want := map[string]float64{"A": 15, "B": 10, "C": 5}; fmt.Sprint(r.ByLevel) != fmt.Sprint(want) {
					t.Errorf("by level %v, want %v", r.ByLevel, want)
				}
				if len(r.Projects) != 2 || r.Projects[0].Key != "p_a" || r.Projects[1].Key != "p_b" {
					t.Fatalf("projects %+v, want p_a and p_b", r.Projects)
				}
				pa, pb := r.Projects[0], r.Projects[1]
				if pa.CreditedS == nil || *pa.CreditedS != 25 || pa.ByLevel["A"] != 15 || pa.ByLevel["B"] != 10 || pa.AgentS != 40 || pa.AgentOnly != 25 {
					t.Errorf("p_a %+v, want 25 s, 15 at A and 10 at B, an agent for 40 s, 25 of them agent-only", pa)
				}
				if pb.CreditedS == nil || *pb.CreditedS != 5 || pb.ByLevel["C"] != 5 {
					t.Errorf("p_b %+v, want 5 s at C", pb)
				}
			},
		},
		{
			name:   "with display.shadow on the reply leaves out every credited number and keeps the agent's time",
			config: "[display]\nshadow = true\n",
			check: func(t *testing.T, r reply) {
				if !r.Shadow || r.CreditedS != nil || r.ByLevel != nil {
					t.Errorf("shadow %v, credited %v, by level %v; want shadow and no numbers", r.Shadow, r.CreditedS, r.ByLevel)
				}
				for _, p := range r.Projects {
					if p.CreditedS != nil || p.ByLevel != nil {
						t.Errorf("%s shows credit %v by level %v", p.Key, p.CreditedS, p.ByLevel)
					}
				}
				if r.Agent != 40 || len(r.Projects) != 2 || r.Projects[0].AgentS != 40 {
					t.Errorf("agent %v s, projects %+v; want the agent's 40 s kept", r.Agent, r.Projects)
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
			if tt.config != "" {
				if err := os.WriteFile(dirs.ConfigFile(), []byte(tt.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			todayBuckets(t, dirs)
			a := startAgent(t, Options{Dirs: dirs, Ports: []int{0}, KeyStores: []keys.Store{}})
			code, body := request(t, "GET", fmt.Sprintf("http://127.0.0.1:%d/v1/today", a.port.Port), "", "", a.port.Token)
			if code != http.StatusOK {
				t.Fatalf("GET /v1/today = %d %s", code, body)
			}
			var r reply
			if err := json.Unmarshal([]byte(body), &r); err != nil {
				t.Fatal(err)
			}
			if r.Date != time.Now().Format(time.DateOnly) {
				t.Errorf("date %q, want today", r.Date)
			}
			tt.check(t, r)
		})
	}
}
