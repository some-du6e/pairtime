// SPDX-License-Identifier: AGPL-3.0-or-later

package dashboard

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// calibratingView is a month with the owner's time and the agent's in three
// projects. The owner's time ranks Alpha, then Bravo, then Charlie, and the
// agent's ranks Charlie, then Alpha, then Bravo. The owner's seconds are
// numbers no other field holds, so a test can look for them: you final and
// 617.5 pending today, 345.5 yesterday, and the week's and month's final
// you+345.5, 4667 at the usual 4321.5.
func calibratingView(t *testing.T, you float64) View {
	t.Helper()
	v, err := Build(Meta{
		AsOf: time.Date(2026, 11, 24, 15, 0, 0, 0, time.UTC), TZ: "UTC", Today: "2026-11-24", Agent: AgentName,
		Names:         map[string]string{"pa": "Alpha", "pb": "Bravo", "pc": "Charlie"},
		Codes:         map[string]string{"pa": "aa11", "pb": "bb22", "pc": "cc33"},
		Keys:          map[string][]string{"pa": {"k_a"}, "pb": {"k_b"}, "pc": {"k_c"}},
		SplitByDevice: true,
	}, []Row{
		{Day: "2026-11-24", Project: "pa", Kind: "prompting", Level: "A", Seconds: you},
		{Day: "2026-11-24", Project: "pa", Kind: "testing", Level: "B", Pending: true, Seconds: 617.5},
		{Day: "2026-11-23", Project: "pb", Kind: "reviewing", Level: "C", Seconds: 345.5},
		{Day: "2026-11-24", Project: "pa", Agent: true, Seconds: 60},
		{Day: "2026-11-24", Project: "pc", Agent: true, Seconds: 9600},
		{Day: "2026-11-02", Project: "pb", Agent: true, Pending: true, Seconds: 30},
	})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// present returns, for each place path names in v, whether its last key is
// there. path is keys joined by dots, and "[*]" after a key steps into
// every element of its list.
func present(v any, path string) []bool {
	key, rest, more := strings.Cut(path, ".")
	each := strings.HasSuffix(key, "[*]")
	key = strings.TrimSuffix(key, "[*]")
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	got, ok := m[key]
	if !more && !each {
		return []bool{ok}
	}
	if !ok {
		return nil
	}
	if !each {
		return present(got, rest)
	}
	var out []bool
	list, _ := got.([]any)
	for _, el := range list {
		out = append(out, present(el, rest)...)
	}
	return out
}

func TestCalibrate(t *testing.T) {
	v := calibratingView(t, 4321.5)
	c := Calibrate(v)
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}

	// What D-shadow-scope hides and keeps, field by field, in the JSON
	// GET /api/v1/users/me/dashboard sends. The View has no moments,
	// uncredited codes or sessions, the rest of what D-shadow-scope names.
	fields := []struct {
		path string
		kept bool
	}{
		{"days[*].you", false},
		{"days[*].projects[*].you", false},
		{"week.you", false},
		{"week.projects[*].you", false},
		{"week.levels", false},
		{"week.kinds", false},
		{"month.you", false},
		{"month.projects[*].you", false},
		{"month.levels", false},
		{"month.kinds", false},
		{"sample", true},
		{"calibrating", true},
		{"as_of", true},
		{"tz", true},
		{"today", true},
		{"agent", true},
		{"projects_split_by_device", true},
		{"projects[*].id", true},
		{"projects[*].keys", true},
		{"projects[*].name", true},
		{"projects[*].code", true},
		{"days[*].date", true},
		{"days[*].agent", true},
		{"days[*].projects[*].project", true},
		{"days[*].projects[*].agent", true},
		{"week.from", true},
		{"week.to", true},
		{"week.agent", true},
		{"week.projects[*].project", true},
		{"week.projects[*].agent", true},
		{"month.from", true},
		{"month.to", true},
		{"month.agent", true},
		{"month.projects[*].project", true},
		{"month.projects[*].agent", true},
	}
	for _, f := range fields {
		name := f.path + " is left out"
		if f.kept {
			name = f.path + " stays"
		}
		t.Run("the API: "+name, func(t *testing.T) {
			got := present(doc, f.path)
			if len(got) == 0 {
				t.Fatalf("the JSON has no place for %s:\n%s", f.path, data)
			}
			for i, p := range got {
				if p != f.kept {
					t.Fatalf("%s (place %d of %d) is there: %v, want %v:\n%s", f.path, i+1, len(got), p, f.kept, data)
				}
			}
		})
	}

	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{name: "the view says calibrating", check: func(t *testing.T) {
			if !c.Calibrating || doc["calibrating"] != true {
				t.Errorf("Calibrating = %v, and the JSON's calibrating %v", c.Calibrating, doc["calibrating"])
			}
		}},
		{name: "the JSON holds none of the owner's seconds, one by one or summed", check: func(t *testing.T) {
			for _, n := range []string{"4321.5", "617.5", "345.5", "4667"} {
				if strings.Contains(string(data), n) {
					t.Errorf("the JSON holds %s:\n%s", n, data)
				}
			}
		}},
		{name: "the owner's time is zero in every day, project, week and month of the view, and the splits by level and kind are empty", check: func(t *testing.T) {
			for _, d := range c.Days {
				if d.You != (Time{}) {
					t.Errorf("%s has your time %+v", d.Date, d.You)
				}
				for _, pt := range d.Projects {
					if pt.You != (Time{}) {
						t.Errorf("%s's %s has your time %+v", d.Date, pt.Project, pt.You)
					}
				}
			}
			for _, w := range []Totals{c.Week, c.Month} {
				if w.You != (Time{}) || len(w.Levels) != 0 || len(w.Kinds) != 0 {
					t.Errorf("%s to %s has your time %+v, levels %v and kinds %v", w.From, w.To, w.You, w.Levels, w.Kinds)
				}
				for _, pt := range w.Projects {
					if pt.You != (Time{}) {
						t.Errorf("%s to %s's %s has your time %+v", w.From, w.To, pt.Project, pt.You)
					}
				}
			}
		}},
		{name: "the agent's time stays as it was, in every day, project, week and month", check: func(t *testing.T) {
			agents := func(pts []ProjectTime) map[string]Time {
				out := make(map[string]Time, len(pts))
				for _, pt := range pts {
					out[pt.Project] = pt.Agent
				}
				return out
			}
			for i, d := range c.Days {
				if d.Agent != v.Days[i].Agent || !reflect.DeepEqual(agents(d.Projects), agents(v.Days[i].Projects)) {
					t.Errorf("%s has the agent's time %+v %v, want %+v %v", d.Date, d.Agent, agents(d.Projects), v.Days[i].Agent, agents(v.Days[i].Projects))
				}
			}
			if c.Week.Agent != v.Week.Agent || c.Month.Agent != v.Month.Agent || !reflect.DeepEqual(agents(c.Month.Projects), agents(v.Month.Projects)) {
				t.Errorf("the week and month have the agent's time %+v and %+v, want %+v and %+v", c.Week.Agent, c.Month.Agent, v.Week.Agent, v.Month.Agent)
			}
		}},
		{name: "every project stays, in the order of the agent's time over the month, and every list of them follows it", check: func(t *testing.T) {
			ids := func(pts []ProjectTime) []string {
				out := []string{}
				for _, pt := range pts {
					out = append(out, pt.Project)
				}
				return out
			}
			var order []string
			for _, p := range c.Projects {
				order = append(order, p.ID)
			}
			if want := []string{"pc", "pa", "pb"}; !slices.Equal(order, want) {
				t.Errorf("projects go %v, want %v, by the agent's time and not the owner's", order, want)
			}
			if got := ids(c.Month.Projects); !slices.Equal(got, order) {
				t.Errorf("the month's projects go %v, want %v", got, order)
			}
			if got := ids(c.Days[len(c.Days)-1].Projects); !slices.Equal(got, []string{"pc", "pa"}) {
				t.Errorf("today's projects go %v, want [pc pa]", got)
			}
		}},
		{name: "two projects the agent gave the same time go by name", check: func(t *testing.T) {
			w := calibratingView(t, 4321.5)
			w.Projects = []Project{{ID: "p2", Name: "Zulu"}, {ID: "p1", Name: "Yankee"}}
			w.Month.Projects = []ProjectTime{{Project: "p2", Agent: Time{FinalS: 5}}, {Project: "p1", Agent: Time{FinalS: 5}}}
			if got := Calibrate(w).Projects; got[0].ID != "p1" {
				t.Errorf("projects go %+v, want Yankee first", got)
			}
		}},
		{name: "Calibrate leaves the view it was given as it was", check: func(t *testing.T) {
			if !reflect.DeepEqual(v, calibratingView(t, 4321.5)) {
				t.Error("Calibrate changed the view it was given")
			}
		}},
		{name: "the ETag of a calibrating view doesn't move with the owner's time", check: func(t *testing.T) {
			if a, b := ETag(c), ETag(Calibrate(calibratingView(t, 99.5))); a != b {
				t.Errorf("the ETags differ, %s and %s, though only the owner's hidden time does", a, b)
			}
			if ETag(c) == ETag(v) {
				t.Error("a calibrating view has the ETag of the view it hides")
			}
		}},
		{name: "the API sends a calibrating view as it encodes, with its ETag", check: func(t *testing.T) {
			var asked string
			h := API(source{v: c, asked: &asked}, func(*http.Request) (string, error) { return "u1", nil },
				func() time.Time { return c.AsOf }, slog.New(slog.DiscardHandler))
			rec := httptest.NewRecorder()
			h(rec, httptest.NewRequest(http.MethodGet, APIPath, nil))
			if rec.Code != http.StatusOK || rec.Header().Get("ETag") != ETag(c) {
				t.Fatalf("status %d, ETag %q, want 200 and %q", rec.Code, rec.Header().Get("ETag"), ETag(c))
			}
			if body := bytes.TrimSpace(rec.Body.Bytes()); !bytes.Equal(body, data) {
				t.Errorf("the API sent\n%s\nwant\n%s", body, data)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.check)
	}
}

// TestUncalibratedJSON holds a view without the flag to the JSON the View
// had before D-server-shadow, byte for byte.
func TestUncalibratedJSON(t *testing.T) {
	// before is the View as it was, field for field, with no calibrating.
	type before struct {
		Sample        bool      `json:"sample"`
		AsOf          time.Time `json:"as_of"`
		TZ            string    `json:"tz"`
		Today         string    `json:"today"`
		Agent         string    `json:"agent"`
		SplitByDevice bool      `json:"projects_split_by_device"`
		Projects      []Project `json:"projects"`
		Days          []Day     `json:"days"`
		Week          Totals    `json:"week"`
		Month         Totals    `json:"month"`
	}
	tests := []struct {
		name string
		v    View
	}{
		{name: "an account's view", v: calibratingView(t, 4321.5)},
		{name: "the demo's sample", v: demoView(t, demoNow)},
	}
	for _, tt := range tests {
		t.Run(tt.name+" encodes as the View did before the flag", func(t *testing.T) {
			v := tt.v
			got, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			want, err := json.Marshal(before{v.Sample, v.AsOf, v.TZ, v.Today, v.Agent, v.SplitByDevice, v.Projects, v.Days, v.Week, v.Month})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("got\n%s\nwant\n%s", got, want)
			}
		})
	}
}
