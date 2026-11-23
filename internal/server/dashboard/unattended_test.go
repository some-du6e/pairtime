// SPDX-License-Identifier: AGPL-3.0-or-later

package dashboard

import (
	"context"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/pkg/projectname"
)

// unattendedNow is a moment the second sample is viewed at: 18:00, late
// enough for its busiest project's 15h 5m, since one project's agent clock
// is wall time.
var unattendedNow = time.Date(2026, 11, 22, 18, 0, 0, 0, time.UTC)

func TestUnattended(t *testing.T) {
	v, err := Unattended{}.View(context.Background(), "", unattendedNow)
	if err != nil {
		t.Fatal(err)
	}
	today := v.Days[len(v.Days)-1]
	tests := []struct {
		name string
		ok   bool
	}{
		{"it reads as an owner's own view: no sample label, the agent unnamed, each computer's projects apart", !v.Sample && v.Agent == AgentName && v.SplitByDevice && v.Today == "2026-11-22"},
		{"none of the 30 days is the owner's time", v.Month.You.Total() == 0 && v.Week.You.Total() == 0 && today.You.Total() == 0},
		{"today has nine projects with the agent's time", len(today.Projects) == 9},
		{"the agents' time today adds up to more than a day: 31h 5m 40s", today.Agent.Total() == 31*3600+5*60+40},
		{"two of today's projects have a few minutes still pending, 5 in all", today.Agent.PendingS == 300 && pendingProjects(today) == 2},
		{"every project is called by its short code, and the codes differ", codesDiffer(v)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Errorf("the sample's view: today %+v, projects %+v", today, v.Projects)
			}
		})
	}
}

func pendingProjects(d Day) int {
	n := 0
	for _, p := range d.Projects {
		if p.Agent.PendingS > 0 {
			n++
		}
	}
	return n
}

func codesDiffer(v View) bool {
	seen := make(map[string]bool)
	for _, p := range v.Projects {
		if p.Code == "" || p.Name != projectname.CodeName(p.Code) || seen[p.Code] || len(p.Keys) != 1 || projectname.ShortCodes(p.Keys)[p.Keys[0]] != p.Code {
			return false
		}
		seen[p.Code] = true
	}
	return len(seen) == 9
}
