// SPDX-License-Identifier: AGPL-3.0-or-later

package dashboard

import (
	"context"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/rollup"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/pkg/projectname"
)

// TestProjectNames checks that ProjectNames and NameOf call every project
// what View calls it: the name its owner turned on, or Project and the
// code its keys have among all the account's projects, a project with no
// time in the month included.
func TestProjectNames(t *testing.T) {
	now := time.Date(2026, 11, 20, 18, 0, 0, 0, time.UTC)
	minute := time.Date(2026, 11, 20, 17, 0, 0, 0, time.UTC).Unix()
	bucket := func(device, key string, m int64) rollup.Bucket {
		return rollup.Bucket{Account: "u1", Device: device, Minute: m, State: "final",
			Slots: []rollup.Slot{{Project: key, CreditedS: 15, Kind: "prompting", Level: "A"}}}
	}
	tests := []struct {
		name    string
		buckets []rollup.Bucket
		// links are rollup_projects rows of u1 with no time: device, key
		// and project id.
		links [][3]string
		// labels are project_names rows of u1: device, key and label.
		labels [][3]string
		check  func(t *testing.T, names map[string]string, v View, keys map[string][]string)
	}{
		{
			name:    "a project with time in the month is called what the view calls it, by its owner's label or by its code",
			buckets: []rollup.Bucket{bucket("d1", "p_one", minute), bucket("d2", "p_two", minute+60)},
			labels:  [][3]string{{"d1", "p_one", "My game"}},
			check: func(t *testing.T, names map[string]string, v View, _ map[string][]string) {
				if len(v.Projects) != 2 {
					t.Fatalf("the view lists %d projects, want 2", len(v.Projects))
				}
				var labelled bool
				for _, p := range v.Projects {
					if got := NameOf(names, p.ID); got != p.Name {
						t.Errorf("project %s is %q, and the view calls it %q", p.ID, got, p.Name)
					}
					labelled = labelled || p.Name == "My game"
				}
				if !labelled {
					t.Errorf("no project is called by its owner's label: %+v", v.Projects)
				}
			},
		},
		{
			name:    "a project with no time in the month is called by the code its key has among all the account's projects",
			buckets: []rollup.Bucket{bucket("d1", "p_one", minute)},
			links:   [][3]string{{"d3", "p_old", "proj-old"}},
			check: func(t *testing.T, names map[string]string, v View, keys map[string][]string) {
				want := projectname.CodeName(projectname.ProjectCodes(keys)["proj-old"])
				if got := NameOf(names, "proj-old"); got != want || got == projectname.CodeName("") {
					t.Errorf("the quiet project is %q, want %q", got, want)
				}
				for _, p := range v.Projects {
					if p.ID == "proj-old" {
						t.Errorf("the view lists the quiet project: %+v", p)
					}
				}
			},
		},
		{
			name:   "a quiet project its owner named is called by the name",
			links:  [][3]string{{"d3", "p_old", "proj-old"}},
			labels: [][3]string{{"d3", "p_old", "Old site"}},
			check: func(t *testing.T, names map[string]string, _ View, _ map[string][]string) {
				if got := NameOf(names, "proj-old"); got != "Old site" {
					t.Errorf("the named quiet project is %q, want Old site", got)
				}
			},
		},
		{
			name: "an id no key maps is called by its own code, and no id is No project, as the view calls them",
			check: func(t *testing.T, names map[string]string, _ View, _ map[string][]string) {
				if got, want := NameOf(names, "proj-gone"), projectname.CodeName(projectname.ShortCodes([]string{"proj-gone"})["proj-gone"]); got != want {
					t.Errorf("an unmapped id is %q, want %q", got, want)
				}
				if got := NameOf(names, ""); got != noProject {
					t.Errorf("no id is %q, want %q", got, noProject)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			db := rollupsDB(t, store.SQLite, "UTC")
			applyAll(t, db, tt.buckets)
			for _, l := range tt.links {
				link(t, db, l[2], l[0]+"/"+l[1])
			}
			for _, n := range tt.labels {
				if _, err := db.Write.ExecContext(ctx, `INSERT INTO project_names (device_id, project_key, user_id, label, set_at_ms) VALUES ($1, $2, 'u1', $3, 0)`, n[0], n[1], n[2]); err != nil {
					t.Fatal(err)
				}
			}
			names, err := ProjectNames(ctx, db.Read, "u1")
			if err != nil {
				t.Fatal(err)
			}
			v, err := Rollups{DB: db.Read}.View(ctx, "u1", now)
			if err != nil {
				t.Fatal(err)
			}
			keys, err := rollup.Projects(ctx, db.Read, "u1")
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, names, v, keys)
		})
	}
}
