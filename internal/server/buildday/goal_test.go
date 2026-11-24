// SPDX-License-Identifier: AGPL-3.0-or-later

package buildday

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/ingest"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// setGoal sets the account's weekly goal at at, in an op of its own.
func (w *world) setGoal(goal int64, at time.Time) error {
	w.t.Helper()
	return w.d.Do(context.Background(), goalRows, func(ctx context.Context, tx writer.Tx) error {
		return SetGoal(ctx, tx, "u1", goal, at.UnixMilli(), Current())
	})
}

// weekGoal returns the goal build_weeks stored the account's week that
// starts on Monday week by, 0 when there is no row.
func (w *world) weekGoal(week string) int64 {
	w.t.Helper()
	got, err := weekResults(context.Background(), w.db.Read, "u1")
	if err != nil {
		w.t.Fatal(err)
	}
	return got[week].Goal
}

// TestSetGoal checks the weekly goal's setter: a goal from 1 to 7 applies
// to every week not decided yet, so a week whose build days already reach
// a lowered goal is met at once, last week too until its deadline, while a
// week decided keeps its result and the goal it was decided by
// (orchestrator, 2026-11-24). 2026-11-02 is a Monday, and in the UTC
// account a week's deadline is the Thursday after it, 00:00.
func TestSetGoal(t *testing.T) {
	tests := []struct {
		name  string
		do    func(t *testing.T, w *world)
		check func(t *testing.T, w *world)
	}{
		{
			name: "a goal set before any build day is the one the first week is met by",
			do: func(t *testing.T, w *world) {
				if err := w.setGoal(1, utc(2026, 11, 16, 9, 0)); err != nil {
					t.Fatal(err)
				}
				buildOn(w, 2026, 11, 16)
			},
			check: func(t *testing.T, w *world) {
				if got, goal := w.weekResult("2026-11-16"), w.weekGoal("2026-11-16"); got != Met || goal != 1 {
					t.Errorf("the week of 2026-11-16 is %q by a goal of %d after one build day, want met by 1", got, goal)
				}
				if s := w.summary(utc(2026, 11, 16, 12, 0)); s.WeeklyGoal != 1 {
					t.Errorf("the summary's weekly goal is %d, want 1", s.WeeklyGoal)
				}
			},
		},
		{
			name: "lowering the goal to this week's build days meets the week at once, by the new goal",
			do: func(t *testing.T, w *world) {
				buildOn(w, 2026, 11, 16)
				buildOn(w, 2026, 11, 17)
				if got := w.weekResult("2026-11-16"); got != "" {
					t.Fatalf("the week of 2026-11-16 is %q before the change, want open", got)
				}
				if err := w.setGoal(2, utc(2026, 11, 17, 12, 0)); err != nil {
					t.Fatal(err)
				}
			},
			check: func(t *testing.T, w *world) {
				if got, goal := w.weekResult("2026-11-16"), w.weekGoal("2026-11-16"); got != Met || goal != 2 {
					t.Errorf("the week of 2026-11-16 is %q by a goal of %d, want met by 2", got, goal)
				}
				if s := w.summary(utc(2026, 11, 17, 12, 0)); s.WeeksInARow != 1 || s.WeeklyGoal != 2 {
					t.Errorf("weeks in a row %d, goal %d; want 1 and 2", s.WeeksInARow, s.WeeklyGoal)
				}
			},
		},
		{
			name: "lowering the goal meets last week while its deadline hasn't passed",
			do: func(t *testing.T, w *world) {
				buildOn(w, 2026, 11, 9)
				buildOn(w, 2026, 11, 10)
				// Tuesday: the week of 11-09 ends on Sunday and is decided at
				// Thursday 00:00, so the job leaves it open.
				w.run(utc(2026, 11, 17, 10, 0))
				if got, next := w.weekResult("2026-11-09"), w.nextWeek(); got != "" || next != "2026-11-09" {
					t.Fatalf("before the change the week of 2026-11-09 is %q and next_week %q, want open and 2026-11-09", got, next)
				}
				if err := w.setGoal(2, utc(2026, 11, 17, 11, 0)); err != nil {
					t.Fatal(err)
				}
			},
			check: func(t *testing.T, w *world) {
				if got, goal := w.weekResult("2026-11-09"), w.weekGoal("2026-11-09"); got != Met || goal != 2 {
					t.Errorf("the week of 2026-11-09 is %q by a goal of %d, want met by 2", got, goal)
				}
			},
		},
		{
			name: "a week decided missed before the change stays missed when the goal comes down to its build days",
			do: func(t *testing.T, w *world) {
				buildOn(w, 2026, 11, 2)
				buildOn(w, 2026, 11, 3)
				// Past the week of 11-02's deadline, Thursday 11-12 00:00: it is
				// missed, with no freeze to spend.
				w.run(utc(2026, 11, 12, 1, 0))
				if next := w.nextWeek(); next != "2026-11-09" {
					t.Fatalf("next_week is %q after the week of 2026-11-02 was decided, want 2026-11-09", next)
				}
				if err := w.setGoal(2, utc(2026, 11, 12, 2, 0)); err != nil {
					t.Fatal(err)
				}
			},
			check: func(t *testing.T, w *world) {
				if got := w.weekResult("2026-11-02"); got != "" {
					t.Errorf("the missed week of 2026-11-02 is %q after the change, want missed, with no row", got)
				}
			},
		},
		{
			name: "raising the goal leaves a met week met, by the goal it was met by",
			do: func(t *testing.T, w *world) {
				for d := 16; d <= 18; d++ {
					buildOn(w, 2026, 11, d)
				}
				if err := w.setGoal(5, utc(2026, 11, 18, 12, 0)); err != nil {
					t.Fatal(err)
				}
			},
			check: func(t *testing.T, w *world) {
				if got, goal := w.weekResult("2026-11-16"), w.weekGoal("2026-11-16"); got != Met || goal != 3 {
					t.Errorf("the week of 2026-11-16 is %q by a goal of %d, want met by 3", got, goal)
				}
				s := w.summary(utc(2026, 11, 18, 12, 0))
				if s.WeeklyGoal != 5 || s.WeeksInARow != 1 {
					t.Errorf("goal %d, weeks in a row %d; want 5 and 1", s.WeeklyGoal, s.WeeksInARow)
				}
			},
		},
		{
			name: "raising the goal past this week's build days leaves the week open, needing the new goal",
			do: func(t *testing.T, w *world) {
				buildOn(w, 2026, 11, 16)
				buildOn(w, 2026, 11, 17)
				if err := w.setGoal(4, utc(2026, 11, 17, 12, 0)); err != nil {
					t.Fatal(err)
				}
				buildOn(w, 2026, 11, 18)
			},
			check: func(t *testing.T, w *world) {
				if got := w.weekResult("2026-11-16"); got != "" {
					t.Errorf("the week of 2026-11-16 is %q with 3 build days and a goal of 4, want open", got)
				}
				s := w.summary(utc(2026, 11, 18, 12, 0))
				last := s.Weeks[len(s.Weeks)-1]
				if last.Start != "2026-11-16" || last.Result != Open || last.Goal != 4 || last.BuildDays != 3 {
					t.Errorf("this week is %+v, want the week of 2026-11-16 open, 3 build days of a goal of 4", last)
				}
			},
		},
		{
			name: "a goal of 0 or 8 is refused with ErrGoal and stores nothing",
			do: func(t *testing.T, w *world) {
				for _, g := range []int64{0, 8, -1} {
					if err := w.setGoal(g, utc(2026, 11, 16, 9, 0)); !errors.Is(err, ErrGoal) {
						t.Errorf("a goal of %d: %v, want ErrGoal", g, err)
					}
				}
			},
			check: func(t *testing.T, w *world) {
				var n int
				if err := w.db.Read.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM build_goals`).Scan(&n); err != nil || n != 0 {
					t.Errorf("build_goals holds %d rows (%v), want none", n, err)
				}
			},
		},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				w := newWorld(t, engine, "")
				tt.do(t, w)
				tt.check(t, w)
			})
		}
	}
}

// failDoer fails every op, as a writer whose database is down does.
type failDoer struct{}

func (failDoer) Do(context.Context, int, writer.Op) error {
	return errors.New("writer: the database is locked")
}

// TestGoalAPI checks PUT /api/v1/build-days/goal: a device's token sets
// its account's goal with a body of exactly {"weekly_goal": n}, n from 1
// to 7, and the request counts against the device's teen-layer rate once
// its body is in (docs/spec/teen-layer-v1.md).
func TestGoalAPI(t *testing.T) {
	devices := fakeDevices{"ptd_good": {ID: "d1", Account: "u1"}}
	tests := []struct {
		name string
		// noToken sends no Authorization header.
		noToken bool
		body    string
		// before are bodies the device sends first, with the same rate.
		before []string
		// burst is the rate's burst, 0 for ingest's default.
		burst int
		// broken sends the change to a writer that fails.
		broken     bool
		wantCode   int
		wantBody   string
		retryAfter string
	}{
		{name: "a request without a device token is refused with token_missing", noToken: true, body: `{"weekly_goal": 3}`, wantCode: http.StatusUnauthorized, wantBody: `"error":"token_missing"`},
		{name: "a goal from 1 to 7 is set, and the answer is the goal", body: `{"weekly_goal": 5}`, wantCode: http.StatusOK, wantBody: `{"weekly_goal":5}`},
		{name: "a goal of 1 is set", body: `{"weekly_goal":1}`, wantCode: http.StatusOK, wantBody: `{"weekly_goal":1}`},
		{name: "a goal of 7 is set", body: `{"weekly_goal":7}`, wantCode: http.StatusOK, wantBody: `{"weekly_goal":7}`},
		{name: "a goal of 0 is refused with goal_invalid", body: `{"weekly_goal": 0}`, wantCode: http.StatusBadRequest, wantBody: `"error":"goal_invalid"`},
		{name: "a goal of 8 is refused with goal_invalid", body: `{"weekly_goal": 8}`, wantCode: http.StatusBadRequest, wantBody: `"error":"goal_invalid"`},
		{name: "a negative goal is refused with goal_invalid", body: `{"weekly_goal": -3}`, wantCode: http.StatusBadRequest, wantBody: `"error":"goal_invalid"`},
		{name: "an integer too large for 64 bits is refused with goal_invalid", body: `{"weekly_goal": 99999999999999999999}`, wantCode: http.StatusBadRequest, wantBody: `"error":"goal_invalid"`},
		{name: "an empty object is refused with body_shape", body: `{}`, wantCode: http.StatusBadRequest, wantBody: `"error":"body_shape"`},
		{name: "a body that isn't an object is refused with body_shape", body: `[3]`, wantCode: http.StatusBadRequest, wantBody: `"error":"body_shape"`},
		{name: "a bare number is refused with body_shape", body: `3`, wantCode: http.StatusBadRequest, wantBody: `"error":"body_shape"`},
		{name: "a body cut off is refused with body_shape", body: `{"weekly_goal": 3`, wantCode: http.StatusBadRequest, wantBody: `"error":"body_shape"`},
		{name: "a second member is refused with body_shape", body: `{"weekly_goal": 3, "freezes": 2}`, wantCode: http.StatusBadRequest, wantBody: `"error":"body_shape"`},
		{name: "the member in another letter case is refused with body_shape", body: `{"Weekly_Goal": 3}`, wantCode: http.StatusBadRequest, wantBody: `"error":"body_shape"`},
		{name: "the member given twice is refused with body_shape", body: `{"weekly_goal": 3, "weekly_goal": 4}`, wantCode: http.StatusBadRequest, wantBody: `"error":"body_shape"`},
		{name: "a null goal is refused with body_shape", body: `{"weekly_goal": null}`, wantCode: http.StatusBadRequest, wantBody: `"error":"body_shape"`},
		{name: "a goal as a string is refused with body_shape", body: `{"weekly_goal": "3"}`, wantCode: http.StatusBadRequest, wantBody: `"error":"body_shape"`},
		{name: "a fraction is refused with body_shape", body: `{"weekly_goal": 3.5}`, wantCode: http.StatusBadRequest, wantBody: `"error":"body_shape"`},
		{name: "a whole number written with a fraction is refused with body_shape", body: `{"weekly_goal": 3.0}`, wantCode: http.StatusBadRequest, wantBody: `"error":"body_shape"`},
		{name: "a number with an exponent is refused with body_shape", body: `{"weekly_goal": 3e0}`, wantCode: http.StatusBadRequest, wantBody: `"error":"body_shape"`},
		{name: "a second value after the object is refused with body_shape", body: `{"weekly_goal": 3} {}`, wantCode: http.StatusBadRequest, wantBody: `"error":"body_shape"`},
		{name: "a body that isn't UTF-8 is refused with json_invalid", body: "{\"weekly_goal\": 3, \"\xff\": 1}", wantCode: http.StatusBadRequest, wantBody: `"error":"json_invalid"`},
		{name: "a body over 1 KiB is refused with body_too_large", body: `{"weekly_goal": 3` + strings.Repeat(" ", goalMaxBody) + `}`, wantCode: http.StatusRequestEntityTooLarge, wantBody: `"error":"body_too_large"`},
		{name: "a device past its rate is refused with rate_limited and Retry-After", burst: 1, before: []string{`{"weekly_goal": 4}`}, body: `{"weekly_goal": 5}`, wantCode: http.StatusTooManyRequests, wantBody: `"error":"rate_limited"`, retryAfter: "3600"},
		{name: "a body refused for its shape doesn't count against the rate", burst: 1, before: []string{`{"weekly_goal": "4"}`}, body: `{"weekly_goal": 5}`, wantCode: http.StatusOK, wantBody: `{"weekly_goal":5}`},
		{name: "a goal out of range counts against the rate, as a devlog's bad id does", burst: 1, before: []string{`{"weekly_goal": 9}`}, body: `{"weekly_goal": 5}`, wantCode: http.StatusTooManyRequests, wantBody: `"error":"rate_limited"`, retryAfter: "3600"},
		{name: "a writer that fails answers 503 with Retry-After: 5, as ingest does", broken: true, body: `{"weekly_goal": 5}`, wantCode: http.StatusServiceUnavailable, wantBody: `"error":"unavailable"`, retryAfter: "5"},
	}
	now := func() time.Time { return utc(2026, 11, 20, 12, 0) }
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t, store.SQLite, "")
			var doer Doer = w.d
			if tt.broken {
				doer = failDoer{}
			}
			burst := tt.burst
			if burst == 0 {
				burst = ingest.DefaultRateBurst
			}
			api := GoalAPI(GoalOptions{Devices: devices, Writer: doer, Rate: ingest.NewLimiter(burst, time.Hour), Now: now, Logger: logger})
			send := func(body string, token bool) *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodPut, GoalPath, strings.NewReader(body))
				if token {
					r.Header.Set("Authorization", "Bearer ptd_good")
				}
				rec := httptest.NewRecorder()
				api(rec, r)
				return rec
			}
			// want is the goal of the last request answered 200, or the
			// default without one.
			want := Current().Goal
			for _, b := range tt.before {
				if rec := send(b, true); rec.Code == http.StatusOK {
					want = goalOf(t, b)
				}
			}
			rec := send(tt.body, !tt.noToken)
			body := rec.Body.String()
			if rec.Code != tt.wantCode || !strings.Contains(body, tt.wantBody) {
				t.Fatalf("PUT %s %s = %d %s, want %d with %s", GoalPath, tt.body, rec.Code, body, tt.wantCode, tt.wantBody)
			}
			if got := rec.Header().Get("Retry-After"); got != tt.retryAfter {
				t.Errorf("Retry-After = %q, want %q", got, tt.retryAfter)
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}
			if rec.Code == http.StatusOK {
				want = goalOf(t, tt.body)
			}
			if s := w.summary(now()); s.WeeklyGoal != want {
				t.Errorf("the account's weekly goal is %d after the request, want %d", s.WeeklyGoal, want)
			}
		})
	}
}

// goalOf is the goal a body the API took names.
func goalOf(t *testing.T, body string) int64 {
	t.Helper()
	g, ok := parseGoal([]byte(body))
	if !ok {
		t.Fatalf("%s isn't a goal's body", body)
	}
	return g
}
