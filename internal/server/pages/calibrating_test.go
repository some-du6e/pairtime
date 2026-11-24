// SPDX-License-Identifier: AGPL-3.0-or-later

package pages

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/dashboard"
)

// calibratedViews are an owner's month, as it is and through
// dashboard.Calibrate. Today the owner has 1h 22m, 10m of it pending, and
// the agent 2h 41m; the last 7 days hold 1h 28m of the owner's time.
func calibratedViews(t *testing.T) (plain, calibrated dashboard.View) {
	t.Helper()
	v, err := dashboard.Build(dashboard.Meta{
		AsOf: time.Date(2026, 11, 24, 15, 0, 0, 0, time.UTC), TZ: "Europe/London", Today: "2026-11-24", Agent: dashboard.AgentName,
		Names:         map[string]string{"pa": "Alpha", "pb": "Bravo", "pc": "Charlie"},
		Codes:         map[string]string{"pa": "aa11", "pb": "bb22", "pc": "cc33"},
		Keys:          map[string][]string{"pa": {"k_a"}, "pb": {"k_b"}, "pc": {"k_c"}},
		SplitByDevice: true,
	}, []dashboard.Row{
		{Day: "2026-11-24", Project: "pa", Kind: "prompting", Level: "A", Seconds: 4321.5},
		{Day: "2026-11-24", Project: "pa", Kind: "testing", Level: "B", Pending: true, Seconds: 617.5},
		{Day: "2026-11-23", Project: "pb", Kind: "reviewing", Level: "C", Seconds: 345.5},
		{Day: "2026-11-24", Project: "pa", Agent: true, Seconds: 60},
		{Day: "2026-11-24", Project: "pc", Agent: true, Seconds: 9600},
		{Day: "2026-11-02", Project: "pb", Agent: true, Pending: true, Seconds: 30},
	})
	if err != nil {
		t.Fatal(err)
	}
	return v, dashboard.Calibrate(v)
}

// TestCalibratingDashboard checks the owner's page while their account's
// calibration flag is on (D-server-shadow): it hides what D-shadow-scope
// hides on the volunteer's own computer, keeps the agent's time and says
// calibrating, as the volunteer's own views do.
func TestCalibratingDashboard(t *testing.T) {
	plain, calibrated := calibratedViews(t)
	page := func(v dashboard.View) string {
		t.Helper()
		mux, _ := ownerSite(t, signInOn("U0FAKEVERIFIED"), "u1", fakeSource{v: v}, true, nil)
		resp, body := serve(mux, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d:\n%s", resp.StatusCode, body)
		}
		return body
	}
	shown, hidden := page(plain), page(calibrated)
	tests := []struct {
		name string
		text string
		// kept says the calibrating page shows text; if not, the page
		// without the flag does, and the calibrating page mustn't.
		kept bool
	}{
		{name: "the owner's time today", text: "1h 22m"},
		{name: "the owner's time over the last 7 days", text: "1h 28m"},
		{name: "the owner's pending time", text: "10m of your time is still pending"},
		{name: "the owner's time on the hero line", text: "</strong> of your time</p>"},
		{name: "the split by evidence level", text: "Evidence levels"},
		{name: "the split by activity kind", text: "What you spent it on"},
		{name: "the two clocks that set the owner's time beside the agent's", text: "Two clocks, not a score"},
		{name: "the note the volunteer's own computer shows", text: "Your time shows as calibrating while pairtime checks how it counts.", kept: true},
		{name: "calibrating where the owner's time would be", text: "Your time <strong>calibrating</strong>", kept: true},
		{name: "the agent's time today", text: "<strong>2h 41m</strong> your agent worked on its own.", kept: true},
		{name: "the agent's time in a project", text: "Your agent 2h 40m", kept: true},
		{name: "every project, the one with only the owner's time this week included", text: "Bravo", kept: true},
		{name: "the time zone form", text: `<form class="zone"`, kept: true},
	}
	for _, tt := range tests {
		name := "it hides " + tt.name
		if tt.kept {
			name = "it shows " + tt.name
		}
		t.Run(name, func(t *testing.T) {
			switch {
			case tt.kept && !strings.Contains(hidden, tt.text):
				t.Errorf("the calibrating page doesn't show %q:\n%s", tt.text, hidden)
			case !tt.kept && !strings.Contains(shown, tt.text):
				t.Fatalf("the page without the flag doesn't show %q either, so the case proves nothing:\n%s", tt.text, shown)
			case !tt.kept && strings.Contains(hidden, tt.text):
				t.Errorf("the calibrating page shows %q:\n%s", tt.text, hidden)
			}
		})
	}
	t.Run("the projects go in the order of the agent's time", func(t *testing.T) {
		a, b, c := strings.Index(hidden, ">Charlie<"), strings.Index(hidden, ">Alpha<"), strings.Index(hidden, ">Bravo<")
		if a < 0 || !(a < b && b < c) {
			t.Errorf("Charlie at %d, Alpha at %d, Bravo at %d, want them in that order", a, b, c)
		}
	})
}
