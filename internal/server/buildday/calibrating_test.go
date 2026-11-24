// SPDX-License-Identifier: AGPL-3.0-or-later

package buildday

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/store"
)

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

// TestAPICalibrating checks GET /api/v1/build-days for a calibration
// volunteer (D-server-shadow): while the account's flag hides its credited
// time, the answer holds nothing that comes of credited time and says
// calibrating, and once the server scores at 1.0.0 it is the summary as it
// is. Today, 2026-11-20, is a build day of 900 s at level A.
func TestAPICalibrating(t *testing.T) {
	devices := fakeDevices{"ptd_good": {ID: "d1", Account: "u1"}}
	now := func() time.Time { return utc(2026, 11, 20, 12, 0) }
	logger := slog.New(slog.DiscardHandler)
	w := newWorld(t, store.SQLite, "")
	buildOn(w, 2026, 11, 20)
	if _, err := w.db.Write.ExecContext(context.Background(), `UPDATE users SET calibrating_since_ms = 1 WHERE id = 'u1'`); err != nil {
		t.Fatal(err)
	}
	get := func(t *testing.T, scoring string) (string, map[string]any) {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, SummaryPath, nil)
		r.Header.Set("Authorization", "Bearer ptd_good")
		rec := httptest.NewRecorder()
		API(w.db.Read, devices, now, scoring, logger)(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d %s", SummaryPath, rec.Code, rec.Body)
		}
		var doc map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		return rec.Body.String(), doc
	}
	body, doc := get(t, "0.6.0")

	// What the flag leaves out and keeps, field by field: everything that
	// comes of credited time goes, and the calendar and the account's own
	// settings stay.
	fields := []struct {
		path string
		kept bool
	}{
		{"build_days", false},
		{"weeks_in_a_row", false},
		{"freezes.held", false},
		{"freezes.earned", false},
		{"freezes.toward_next", false},
		{"days[*].state", false},
		{"days[*].a_ms", false},
		{"days[*].b_ms", false},
		{"days[*].decided_at", false},
		{"weeks[*].result", false},
		{"weeks[*].build_days", false},
		{"calibrating", true},
		{"time_zone", true},
		{"today", true},
		{"weekly_goal", true},
		{"window_days", true},
		{"freezes.max", true},
		{"freezes.every", true},
		{"days[*].date", true},
		{"days[*].deadline", true},
		{"weeks[*].start", true},
		{"weeks[*].goal", true},
	}
	for _, f := range fields {
		name := f.path + " is left out"
		if f.kept {
			name = f.path + " stays"
		}
		t.Run(name, func(t *testing.T) {
			got := present(doc, f.path)
			if len(got) == 0 {
				t.Fatalf("the answer has no place for %s:\n%s", f.path, body)
			}
			for i, p := range got {
				if p != f.kept {
					t.Fatalf("%s (place %d of %d) is there: %v, want %v:\n%s", f.path, i+1, len(got), p, f.kept, body)
				}
			}
		})
	}
	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{name: "the answer says calibrating, and names no day's state and none of its seconds", check: func(t *testing.T) {
			if doc["calibrating"] != true {
				t.Errorf("calibrating is %v", doc["calibrating"])
			}
			for _, s := range []string{BuildDay, NotCounted, OpenDay, "900000"} {
				if strings.Contains(body, s) {
					t.Errorf("the answer holds %q:\n%s", s, body)
				}
			}
		}},
		{name: "once the server scores at 1.0.0, the flag hides nothing and the summary is as it is", check: func(t *testing.T) {
			plain, doc := get(t, "1.0.0")
			if doc["calibrating"] != nil || !strings.Contains(plain, `"build_days":1,"window_days":30`) || !strings.Contains(plain, `"state":"build_day"`) {
				t.Errorf("at 1.0.0 the answer is\n%s", plain)
			}
		}},
		{name: "a summary without the flag encodes as the Summary type did before the flag", check: func(t *testing.T) {
			// before is the Summary as it was, field for field.
			type before struct {
				TimeZone    string  `json:"time_zone"`
				Today       string  `json:"today"`
				WeeklyGoal  int64   `json:"weekly_goal"`
				BuildDays   int     `json:"build_days"`
				WindowDays  int     `json:"window_days"`
				WeeksInARow int     `json:"weeks_in_a_row"`
				Freezes     Freezes `json:"freezes"`
				Days        []Day   `json:"days"`
				Weeks       []Week  `json:"weeks"`
			}
			s, err := Read(context.Background(), w.db.Read, "u1", now())
			if err != nil {
				t.Fatal(err)
			}
			got, err := json.Marshal(s)
			if err != nil {
				t.Fatal(err)
			}
			want, err := json.Marshal(before{s.TimeZone, s.Today, s.WeeklyGoal, s.BuildDays, s.WindowDays, s.WeeksInARow, s.Freezes, s.Days, s.Weeks})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("got\n%s\nwant\n%s", got, want)
			}
		}},
		{name: "Calibrate leaves the summary it was given as it was", check: func(t *testing.T) {
			s, err := Read(context.Background(), w.db.Read, "u1", now())
			if err != nil {
				t.Fatal(err)
			}
			last := s.Days[len(s.Days)-1]
			Calibrate(s)
			if s.Calibrating || s.Days[len(s.Days)-1] != last || s.BuildDays != 1 {
				t.Errorf("Calibrate changed the summary it was given: %+v", s)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.check)
	}
}
