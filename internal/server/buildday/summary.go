// SPDX-License-Identifier: AGPL-3.0-or-later

package buildday

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/server/device"
	"github.com/some-du6e/pairtime/internal/server/rollup"
	"github.com/some-du6e/pairtime/internal/server/shadow"
)

// SummaryPath is where a device reads its account's build days, the
// summary the local Build days page mirrors (DESIGN 3.8; orchestrator,
// 2026-11-23; docs/spec/teen-layer-v1.md).
const SummaryPath = "/api/v1/build-days"

// The states of a day in a Summary.
const (
	// BuildDay is a day stored as a build day.
	BuildDay = "build_day"
	// NotCounted is a day decided at its deadline not to count, or one
	// whose deadline passed with nothing to decide.
	NotCounted = "not_counted"
	// OpenDay is a day that hasn't counted yet and whose deadline hasn't
	// passed, or that the job hasn't decided yet.
	OpenDay = "open"
)

// Summary is an account's build days, weeks and freezes as of a time.
type Summary struct {
	// Calibrating says the account's calibration flag hides its credited
	// time, so the summary holds none of what comes of it, and the JSON
	// leaves out every field Calibrate hides (D-server-shadow).
	Calibrating bool `json:"calibrating,omitempty"`
	// TimeZone is the profile's time zone, which sets every day's bounds,
	// and Today the local date in it.
	TimeZone string `json:"time_zone"`
	Today    string `json:"today"`
	// WeeklyGoal is the account's goal in build days a week.
	WeeklyGoal int64 `json:"weekly_goal"`
	// BuildDays is consistency: the build days among the last WindowDays
	// days, today included.
	BuildDays  int `json:"build_days"`
	WindowDays int `json:"window_days"`
	// WeeksInARow is the weeks in a row met or covered by a freeze, with
	// the current week counted once it is met. A week not decided yet
	// neither adds to it nor ends it.
	WeeksInARow int     `json:"weeks_in_a_row"`
	Freezes     Freezes `json:"freezes"`
	// Days are the last WindowDays days, oldest first.
	Days []Day `json:"days"`
	// Weeks are the weeks those days fall in, oldest first.
	Weeks []Week `json:"weeks"`
}

// Freezes is an account's freezes.
type Freezes struct {
	Held   int64 `json:"held"`
	Max    int64 `json:"max"`
	Earned int64 `json:"earned"`
	// TowardNext is the build days counted toward the next freeze, of
	// Every.
	TowardNext int64 `json:"toward_next"`
	Every      int64 `json:"every"`
}

// Day is one local day.
type Day struct {
	Date string `json:"date"`
	// State is BuildDay, NotCounted or OpenDay.
	State string `json:"state"`
	// AMS and BMS are its credited milliseconds at levels A and B: as D10
	// decided them for a stored day, and for an open one its credit so far
	// by the as-of read the job decides it by (rollup.CreditAsOf), never
	// rollups_daily's figure at the levels the devices claimed (#278).
	AMS int64 `json:"a_ms"`
	BMS int64 `json:"b_ms"`
	// Deadline is when the day is decided if it hasn't counted by then.
	Deadline time.Time `json:"deadline"`
	// DecidedAt is when it was stored, for a stored day.
	DecidedAt *time.Time `json:"decided_at,omitempty"`
}

// Week is one week, Monday to Sunday.
type Week struct {
	Start string `json:"start"`
	// Result is Met, Frozen, Missed or Open.
	Result string `json:"result"`
	// Goal is the goal it was decided by, or the account's goal while it
	// is open, and BuildDays its build days so far.
	Goal      int64 `json:"goal"`
	BuildDays int64 `json:"build_days"`
}

// maxStreakWeeks bounds how far back Read walks for the weeks in a row.
const maxStreakWeeks = 1000

// Read returns the account's Summary as of now, from build_days,
// build_weeks and build_goals, and, for the days still open, the as-of
// read.
func Read(ctx context.Context, q Querier, account string, now time.Time) (Summary, error) {
	p := Current()
	h, err := rollup.ZoneHistory(ctx, q, account)
	if err != nil {
		return Summary{}, err
	}
	nowMS := now.UnixMilli()
	today := h.Today(nowMS)
	first := addDays(today, 1-p.Window)
	g, err := loadGoals(ctx, q, account, p)
	if err != nil {
		return Summary{}, err
	}
	s := Summary{
		TimeZone: h.Name, Today: today, WeeklyGoal: g.goal, WindowDays: p.Window,
		Freezes: Freezes{Held: g.held, Max: p.MaxHeld, Earned: g.earned, TowardNext: g.freezeDays, Every: p.EarnDays},
	}

	type storedDay struct {
		a, b      int64
		counted   bool
		decidedAt int64
	}
	decided := make(map[string]storedDay)
	rows, err := q.QueryContext(ctx, `SELECT day, a_ms, b_ms, counted, decided_at_ms FROM build_days
WHERE user_id = $1 AND day >= $2 AND day <= $3`, account, first, today)
	if err != nil {
		return Summary{}, fmt.Errorf("buildday: reading %s's build days: %w", account, err)
	}
	for rows.Next() {
		var day string
		var d storedDay
		var counted int64
		if err := rows.Scan(&day, &d.a, &d.b, &counted, &d.decidedAt); err != nil {
			rows.Close()
			return Summary{}, fmt.Errorf("buildday: reading %s's build days: %w", account, err)
		}
		d.counted = counted == 1
		decided[day] = d
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Summary{}, fmt.Errorf("buildday: reading %s's build days: %w", account, err)
	}
	rolled, err := rollup.Read(ctx, q, account, first, today)
	if err != nil {
		return Summary{}, err
	}
	hasRollups := make(map[string]bool)
	for _, r := range rolled {
		hasRollups[r.Day] = true
	}
	for day := first; day <= today; day = addDays(day, 1) {
		deadline, err := h.Deadline(day, p.Lag)
		if err != nil {
			return Summary{}, err
		}
		d := Day{Date: day, State: OpenDay, Deadline: time.UnixMilli(deadline).UTC()}
		switch sd, ok := decided[day]; {
		case ok:
			d.State, d.AMS, d.BMS = NotCounted, sd.a, sd.b
			if sd.counted {
				d.State = BuildDay
				s.BuildDays++
			}
			at := time.UnixMilli(sd.decidedAt).UTC()
			d.DecidedAt = &at
		case !hasRollups[day] && nowMS >= deadline:
			d.State = NotCounted
		case hasRollups[day]:
			// The as-of read the job decides by, never rollups_daily's
			// figure at the levels the devices claimed (#278). A day whose
			// deadline has passed and that the job hasn't decided yet reads
			// as of its deadline.
			c, err := rollup.CreditAsOf(ctx, q, account, day, min(nowMS, deadline))
			if err != nil {
				return Summary{}, err
			}
			d.AMS, d.BMS = c.AMS, c.BMS
		}
		s.Days = append(s.Days, d)
	}

	results, err := weekResults(ctx, q, account)
	if err != nil {
		return Summary{}, err
	}
	state := func(week string) (Week, error) {
		// The week's build days now: a week met early keeps counting the
		// build days that come after (its row holds the count it was met
		// at).
		n, err := buildDaysIn(ctx, q, account, week)
		if err != nil {
			return Week{}, err
		}
		if w, ok := results[week]; ok {
			w.BuildDays = n
			return w, nil
		}
		w := Week{Start: week, Result: Open, Goal: g.goal, BuildDays: n}
		if g.nextWeek != "" && week >= g.nextWeek {
			return w, nil
		}
		deadline, err := weekDeadline(h, week, p.Lag)
		if err != nil {
			return Week{}, err
		}
		if nowMS >= deadline {
			w.Result = Missed
		}
		return w, nil
	}
	firstWeek, err := weekOf(first)
	if err != nil {
		return Summary{}, err
	}
	current, err := weekOf(today)
	if err != nil {
		return Summary{}, err
	}
	for week := firstWeek; week <= current; week = addDays(week, 7) {
		w, err := state(week)
		if err != nil {
			return Summary{}, err
		}
		s.Weeks = append(s.Weeks, w)
	}
	week := current
	for range maxStreakWeeks {
		w, err := state(week)
		if err != nil {
			return Summary{}, err
		}
		if w.Result == Missed {
			break
		}
		if w.Result != Open {
			s.WeeksInARow++
		}
		week = addDays(week, -7)
	}
	return s, nil
}

// weekResults returns the account's stored week results, by Monday.
func weekResults(ctx context.Context, q Querier, account string) (map[string]Week, error) {
	rows, err := q.QueryContext(ctx, `SELECT week, goal, build_days, result FROM build_weeks WHERE user_id = $1`, account)
	if err != nil {
		return nil, fmt.Errorf("buildday: reading %s's weeks: %w", account, err)
	}
	defer rows.Close()
	out := make(map[string]Week)
	for rows.Next() {
		var w Week
		if err := rows.Scan(&w.Start, &w.Goal, &w.BuildDays, &w.Result); err != nil {
			return nil, fmt.Errorf("buildday: reading %s's weeks: %w", account, err)
		}
		out[w.Start] = w
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("buildday: reading %s's weeks: %w", account, err)
	}
	return out, nil
}

// Devices finds the device of a token: *device.Store.
type Devices interface {
	Authenticate(ctx context.Context, token string) (device.Device, error)
}

// errorBody is the JSON of a refusal, as ingest's (docs/spec/ingest-v1.md).
type errorBody struct {
	Error  string `json:"error"`
	Detail string `json:"detail"`
}

// API returns GET /api/v1/build-days, which answers the summary of the
// account the request's device token belongs to (docs/spec/teen-layer-v1.md).
// While the account's calibration flag hides its credited time, the
// summary is Calibrate's (D-server-shadow); scoring is the scoring version
// the server runs, which shadow.Hides reads the flag by.
func API(q Querier, devices Devices, now func() time.Time, scoring string, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dev, ok := authenticate(w, r, devices, logger)
		if !ok {
			return
		}
		s, err := Read(r.Context(), q, dev.Account, now())
		var hidden bool
		if err == nil {
			hidden, err = shadow.Hides(r.Context(), q, dev.Account, scoring)
		}
		if err != nil {
			logger.Error("reading build days", "user", dev.Account, "err", err)
			unavailable(w, "The server couldn't read your build days; try again in 5 seconds.")
			return
		}
		if hidden {
			s = Calibrate(s)
		}
		writeJSON(w, http.StatusOK, s)
	}
}

// authenticate finds the device of r's bearer token, or answers 401 or 503
// itself and reports false.
func authenticate(w http.ResponseWriter, r *http.Request, devices Devices, logger *slog.Logger) (device.Device, bool) {
	scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")
	token = strings.TrimSpace(token)
	if !found || !strings.EqualFold(scheme, "Bearer") || token == "" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="pairtime"`)
		writeJSON(w, http.StatusUnauthorized, errorBody{Error: "token_missing", Detail: "Send the device token as Authorization: Bearer ptd_..."})
		return device.Device{}, false
	}
	dev, err := devices.Authenticate(r.Context(), token)
	switch {
	case err == nil && !dev.Revoked:
		return dev, true
	case err == nil, errors.Is(err, device.ErrBadToken), errors.Is(err, device.ErrUnknownToken), errors.Is(err, device.ErrRevoked):
		w.Header().Set("WWW-Authenticate", `Bearer realm="pairtime", error="invalid_token"`)
		writeJSON(w, http.StatusUnauthorized, errorBody{Error: "token_invalid", Detail: "This device token isn't valid, or its device was removed. Run pairtime login again."})
	default:
		logger.Error("authenticating a device", "err", err)
		unavailable(w, "The server couldn't check the device token; try again in 5 seconds.")
	}
	return device.Device{}, false
}

// unavailable answers 503 with Retry-After: 5, as ingest's 503s do.
func unavailable(w http.ResponseWriter, detail string) {
	w.Header().Set("Retry-After", "5")
	writeJSON(w, http.StatusServiceUnavailable, errorBody{Error: "unavailable", Detail: detail})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
