// SPDX-License-Identifier: AGPL-3.0-or-later

package buildday

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/some-du6e/pairtime/internal/server/ingest"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// GoalPath is where a device sets its account's weekly goal, from the
// local Build days page through the agent (WP-SV12 part 2;
// docs/spec/teen-layer-v1.md).
const GoalPath = SummaryPath + "/goal"

// The weekly goal's bounds, in build days a week: a week has seven days,
// and a goal of none would meet every week.
const (
	MinGoal = 1
	MaxGoal = 7
)

// ErrGoal means a weekly goal outside MinGoal to MaxGoal.
var ErrGoal = errors.New("buildday: a weekly goal is 1 to 7 build days")

// goalRows is the rows SetGoal declares to the writer, a batch-size hint:
// the goal's row and up to three weeks it meets. While the job keeps up,
// an account has at most two weeks not decided yet with build days in
// them, this one and last week until its deadline, and a third spares a
// zone change's longer day.
const goalRows = 4

// goalMaxBody bounds a goal's body, which is a few bytes.
const goalMaxBody = 1 << 10

// goalShape is body_shape's detail for a goal's body.
const goalShape = `The body is {"weekly_goal": n}, with n a whole number of build days.`

// SetGoal sets the account's weekly goal to goal, inside tx, at at. The
// goal applies to every week not decided yet, from the account's
// next_week on, while a week decided keeps the goal it was decided by
// (orchestrator, 2026-11-24). So a week not decided yet whose build days
// already reach a lowered goal is met at once, as count meets a week the
// moment its build days reach the goal: lowering it can meet last week
// until that week's deadline. Raising it unmeets nothing, since a week met
// is decided. Setting the goal the account has already changes nothing.
func SetGoal(ctx context.Context, tx writer.Tx, account string, goal, at int64, p Params) error {
	if goal < MinGoal || goal > MaxGoal {
		return ErrGoal
	}
	g, err := loadGoals(ctx, tx, account, p)
	if err != nil {
		return err
	}
	if g.exists && g.goal == goal {
		return nil
	}
	g.goal, g.changed = goal, true
	if g.nextWeek != "" {
		weeks, err := buildWeeksFrom(ctx, tx, account, g.nextWeek)
		if err != nil {
			return err
		}
		for _, w := range weeks {
			if w.n < goal {
				continue
			}
			done, err := weekStored(ctx, tx, account, w.start)
			if err != nil {
				return err
			}
			if done {
				continue
			}
			if err := putWeek(ctx, tx, account, w.start, goal, w.n, Met, at); err != nil {
				return err
			}
		}
	}
	return g.save(ctx, tx, account)
}

// weekCount is a week's build days.
type weekCount struct {
	// start is the week's Monday.
	start string
	n     int64
}

// buildWeeksFrom counts the account's build days in each week from the
// Monday from on, oldest first, leaving out the weeks with none.
func buildWeeksFrom(ctx context.Context, q Querier, account, from string) ([]weekCount, error) {
	rows, err := q.QueryContext(ctx, `SELECT day FROM build_days WHERE user_id = $1 AND day >= $2 AND counted = 1 ORDER BY day`, account, from)
	if err != nil {
		return nil, fmt.Errorf("buildday: reading %s's build days from %s: %w", account, from, err)
	}
	defer rows.Close()
	var out []weekCount
	for rows.Next() {
		var day string
		if err := rows.Scan(&day); err != nil {
			return nil, fmt.Errorf("buildday: reading %s's build days from %s: %w", account, from, err)
		}
		w, err := weekOf(day)
		if err != nil {
			return nil, err
		}
		if n := len(out); n > 0 && out[n-1].start == w {
			out[n-1].n++
			continue
		}
		out = append(out, weekCount{start: w, n: 1})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("buildday: reading %s's build days from %s: %w", account, from, err)
	}
	return out, nil
}

// GoalOptions wire GoalAPI.
type GoalOptions struct {
	// Devices authenticates the device token each request carries.
	Devices Devices
	// Writer takes the change.
	Writer Doer
	// Rate is the teen layer's rate of each device, the one package
	// devlog's routes count against (devlog.Options.Rate), so a device's
	// goal changes and its publishes and deletes share one burst. Nil
	// means a rate of its own, ingest's default.
	Rate *ingest.Limiter
	// Now is the clock. A change's time is Now inside its writer op.
	Now func() time.Time
	// Logger hears of writes that fail.
	Logger *slog.Logger
}

// GoalAPI returns PUT /api/v1/build-days/goal, which sets the weekly goal
// of the account the request's device token belongs to and answers
// {"weekly_goal": n} (docs/spec/teen-layer-v1.md). The body is that object
// too, with a goal from 1 to 7. Like a devlog's publish, the request
// counts against the device's rate once its body is in, and a body that
// isn't one counts for nothing. The answer carries no summary: GET
// /api/v1/build-days gives it, as the account's calibration flag allows.
func GoalAPI(o GoalOptions) http.HandlerFunc {
	if o.Rate == nil {
		o.Rate = ingest.NewLimiter(ingest.DefaultRateBurst, ingest.DefaultRateEvery)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		dev, ok := authenticate(w, r, o.Devices, o.Logger)
		if !ok {
			return
		}
		goal, ok := readGoal(w, r)
		if !ok {
			return
		}
		done, wait := o.Rate.Take(dev.ID, o.Now())
		if done == nil {
			w.Header().Set("Retry-After", strconv.Itoa(wait))
			writeJSON(w, http.StatusTooManyRequests, errorBody{Error: "rate_limited", Detail: "This device is sending faster than its rate, or another of its requests is still being answered; wait Retry-After seconds, then send it again."})
			return
		}
		defer done()
		if goal < MinGoal || goal > MaxGoal {
			writeJSON(w, http.StatusBadRequest, errorBody{Error: "goal_invalid", Detail: "The weekly goal is a whole number of build days from 1 to 7."})
			return
		}
		p := Current()
		err := o.Writer.Do(r.Context(), goalRows, func(ctx context.Context, tx writer.Tx) error {
			return SetGoal(ctx, tx, dev.Account, goal, o.Now().UnixMilli(), p)
		})
		if err != nil {
			o.Logger.Error("setting a weekly goal", "user", dev.Account, "err", err)
			unavailable(w, "The server couldn't set your weekly goal; try again in 5 seconds.")
			return
		}
		writeJSON(w, http.StatusOK, struct {
			WeeklyGoal int64 `json:"weekly_goal"`
		}{goal})
	}
}

// readGoal reads r's body, a goal's: one JSON object of valid UTF-8, at
// most goalMaxBody bytes, whose one member is weekly_goal, spelt so and
// given once, a JSON integer (parseGoal). It answers 400 or 413 itself and
// reports false for any other body. As at ingest, the body has
// ingest.BodyTimeout to arrive once the headers are in.
func readGoal(w http.ResponseWriter, r *http.Request) (int64, bool) {
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(ingest.BodyTimeout))
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, goalMaxBody))
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		writeJSON(w, http.StatusRequestEntityTooLarge, errorBody{Error: "body_too_large", Detail: "The body is too large: a weekly goal's is a few bytes."})
		return 0, false
	case err != nil:
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "body_unreadable", Detail: "The body couldn't be read, as when the connection broke off; retry the same body."})
		return 0, false
	case !utf8.Valid(data):
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "json_invalid", Detail: "The body must be one JSON object in valid UTF-8."})
		return 0, false
	}
	goal, ok := parseGoal(data)
	if !ok {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "body_shape", Detail: goalShape})
	}
	return goal, ok
}

// parseGoal returns the goal of data, one JSON object whose one member is
// weekly_goal, spelt so and given once, a JSON integer, and reports false
// for anything else: another member or none, the member in another letter
// case or twice, null, a string, a fraction or an exponent. An integer too
// large for 64 bits reads as 0, which no goal is, so it is refused as one
// out of range.
func parseGoal(data []byte) (int64, bool) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return 0, false
	}
	var num json.Number
	for dec.More() {
		t, err := dec.Token()
		if key, ok := t.(string); err != nil || !ok || key != "weekly_goal" || num != "" {
			return 0, false
		}
		t, err = dec.Token()
		n, ok := t.(json.Number)
		if err != nil || !ok {
			return 0, false
		}
		num = n
	}
	if t, err := dec.Token(); err != nil || t != json.Delim('}') || num == "" {
		return 0, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return 0, false
	}
	goal, err := strconv.ParseInt(string(num), 10, 64)
	switch {
	case errors.Is(err, strconv.ErrRange):
		return 0, true
	case err != nil:
		return 0, false
	}
	return goal, true
}
