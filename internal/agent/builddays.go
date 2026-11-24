// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/account"
	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/uploader"
	"github.com/some-du6e/pairtime/internal/version"
	"github.com/some-du6e/pairtime/pkg/event"
)

// The dashboard's Build days page mirrors the build days pairtimed
// decides (WP-SV12 part 2; DESIGN 3.8, 6.2 D10; docs/spec/teen-layer-v1.md
// 6): GET /v1/build-days asks the server's GET /api/v1/build-days with
// this computer's device token, and PUT /v1/build-days/goal sets the
// weekly goal there. Build days are the server's to decide, from the
// minutes it witnessed, so the agent keeps nothing of them on disk. The
// last answer stays in memory for buildDaysFresh, and while the server
// can't be reached the page shows the last answer of this run with its
// time. A computer that pairtime login never linked, or that is
// local-only, asks the server nothing (orchestrator, 2026-11-24).

// The server's routes (docs/spec/teen-layer-v1.md 6).
const (
	buildDaysPath = "/api/v1/build-days"
	buildGoalPath = buildDaysPath + "/goal"
)

// buildDaysFresh is how long an answer serves the page before the agent
// asks the server again.
const buildDaysFresh = 60 * time.Second

// buildDaysTimeout bounds one request to the server, from dialing to the
// last byte, so the page never waits long on a server that doesn't
// answer.
const buildDaysTimeout = 15 * time.Second

// buildDaysMax bounds the server's answer, which holds 30 days and the
// weeks they fall in: a few kilobytes.
const buildDaysMax = 256 << 10

// The weekly goal's bounds, in build days a week, as the server's.
const (
	minWeeklyGoal = 1
	maxWeeklyGoal = 7
)

// The problems of a GET /v1/build-days answer (buildDaysReply.Problem).
const (
	// problemUnreachable: the server didn't answer, or answered with an
	// error of its own.
	problemUnreachable = "unreachable"
	// problemSignedOut: the server refused this computer's device token,
	// or the sign-in doesn't read on this computer.
	problemSignedOut = "signed_out"
	// problemUnreadable: the server's answer isn't a summary of build
	// days.
	problemUnreadable = "unreadable"
)

// buildDaysReply is GET /v1/build-days.
type buildDaysReply struct {
	// Linked says pairtime login linked this computer, and Online that
	// config.toml also lets it upload: the agent asks the server only when
	// both hold.
	Linked bool `json:"linked"`
	Online bool `json:"online"`
	// Shadow is display.shadow, which leaves out of Summary everything
	// that comes of credited time (bdSummary.calibrated).
	Shadow bool `json:"shadow"`
	// Summary is the server's last answer this run, nil before the first,
	// and AsOf when the agent got it.
	Summary *bdSummary `json:"summary"`
	AsOf    *time.Time `json:"as_of,omitempty"`
	// Problem says, on a computer that asks, why the agent has no answer
	// newer than buildDaysFresh: problemUnreachable, problemSignedOut or
	// problemUnreadable. It is absent once an answer came.
	Problem string `json:"problem,omitempty"`
}

// bdSummary is the server's summary (docs/spec/teen-layer-v1.md 6) as the
// agent passes it on: the members the spec names and no other, so the
// local API keeps to docs/api/local.openapi.yaml whatever a newer server
// adds. The members a calibrating summary leaves out are pointers, absent
// when nil.
type bdSummary struct {
	// Calibrating says the summary leaves out everything that comes of
	// credited time: the server's own answer for an account whose
	// calibration flag is on (D-server-shadow), or this computer's while
	// display.shadow is on (D-shadow-scope).
	Calibrating bool      `json:"calibrating,omitempty"`
	TimeZone    string    `json:"time_zone"`
	Today       string    `json:"today"`
	WeeklyGoal  int64     `json:"weekly_goal"`
	BuildDays   *int64    `json:"build_days,omitempty"`
	WindowDays  int64     `json:"window_days"`
	WeeksInARow *int64    `json:"weeks_in_a_row,omitempty"`
	Freezes     bdFreezes `json:"freezes"`
	Days        []bdDay   `json:"days"`
	Weeks       []bdWeek  `json:"weeks"`
}

type bdFreezes struct {
	Held       *int64 `json:"held,omitempty"`
	Max        int64  `json:"max"`
	Earned     *int64 `json:"earned,omitempty"`
	TowardNext *int64 `json:"toward_next,omitempty"`
	Every      int64  `json:"every"`
}

type bdDay struct {
	Date      string     `json:"date"`
	State     string     `json:"state,omitempty"`
	AMS       *int64     `json:"a_ms,omitempty"`
	BMS       *int64     `json:"b_ms,omitempty"`
	Deadline  time.Time  `json:"deadline"`
	DecidedAt *time.Time `json:"decided_at,omitempty"`
}

type bdWeek struct {
	Start     string `json:"start"`
	Result    string `json:"result,omitempty"`
	Goal      int64  `json:"goal"`
	BuildDays *int64 `json:"build_days,omitempty"`
}

// calibrated returns s with everything that comes of credited time left
// out, as pairtimed's buildday.Calibrate leaves it out of a calibrating
// account's summary: each day's state, credit and decision, the build
// days in the window, the weeks in a row, the freezes held, earned and
// owed, and each week's result and build days. What stays comes of the
// calendar and the account's settings alone. display.shadow hides on
// this computer what the account's flag hides on the server
// (D-shadow-scope; D-server-shadow).
func (s bdSummary) calibrated() bdSummary {
	s.Calibrating = true
	s.BuildDays, s.WeeksInARow = nil, nil
	s.Freezes = bdFreezes{Max: s.Freezes.Max, Every: s.Freezes.Every}
	days := make([]bdDay, len(s.Days))
	for i, d := range s.Days {
		days[i] = bdDay{Date: d.Date, Deadline: d.Deadline}
	}
	s.Days = days
	weeks := make([]bdWeek, len(s.Weeks))
	for i, w := range s.Weeks {
		weeks[i] = bdWeek{Start: w.Start, Goal: w.Goal}
	}
	s.Weeks = weeks
	return s
}

// valid reports whether s reads as a summary: a time zone, today and
// every day as dates, and, unless it is calibrating, the numbers a
// calibrating one leaves out.
func (s bdSummary) valid() bool {
	if s.TimeZone == "" || !isDate(s.Today) || s.Days == nil || s.Weeks == nil {
		return false
	}
	for _, d := range s.Days {
		if !isDate(d.Date) || (!s.Calibrating && (d.State == "" || d.AMS == nil || d.BMS == nil)) {
			return false
		}
	}
	for _, w := range s.Weeks {
		if !isDate(w.Start) || (!s.Calibrating && (w.Result == "" || w.BuildDays == nil)) {
			return false
		}
	}
	return s.Calibrating || (s.BuildDays != nil && s.WeeksInARow != nil && s.Freezes.Held != nil && s.Freezes.Earned != nil && s.Freezes.TowardNext != nil)
}

func isDate(s string) bool {
	_, err := time.Parse(time.DateOnly, s)
	return err == nil
}

// buildDaysCache is the server's last summary this run, for the server
// and device it came from, so a sign-in to another never shows the last
// one's.
type buildDaysCache struct {
	mu             sync.Mutex
	server, device string
	summary        *bdSummary
	// at is when the agent got it, with the monotonic reading time.Now
	// gives.
	at time.Time
}

// summaryFresh reports whether the summary got at at still serves at
// now: less than buildDaysFresh ago by both the monotonic clock and the
// wall clock, since the monotonic clock stops while the computer sleeps.
func summaryFresh(at, now time.Time) bool {
	mono, wall := now.Sub(at), now.Round(0).Sub(at.Round(0))
	return mono >= 0 && mono < buildDaysFresh && wall >= 0 && wall < buildDaysFresh
}

// serveBuildDays answers GET /v1/build-days.
func (a *agent) serveBuildDays(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.buildDaysNow(r.Context(), false))
}

// buildDaysNow is GET /v1/build-days: the server's summary from memory
// while it is fresh, unless again asks the server anyway, as after a
// change of the goal.
func (a *agent) buildDaysNow(ctx context.Context, again bool) buildDaysReply {
	reply := buildDaysReply{Shadow: a.config().Display.Shadow}
	reply.Linked, reply.Online = a.linkedOnline()
	if !reply.Online {
		return reply
	}
	c, ok := a.credentials()
	if !ok {
		reply.Problem = problemSignedOut
		return reply
	}
	bc := &a.buildDays
	bc.mu.Lock()
	defer bc.mu.Unlock()
	if bc.server != c.Server || bc.device != c.DeviceID {
		bc.server, bc.device, bc.summary, bc.at = c.Server, c.DeviceID, nil, time.Time{}
	}
	if now := time.Now(); again || bc.summary == nil || !summaryFresh(bc.at, now) {
		s, problem := a.fetchBuildDays(ctx, c)
		if problem != "" {
			reply.Problem = problem
		} else {
			bc.summary, bc.at = s, now
		}
	}
	if bc.summary != nil {
		s := *bc.summary
		if reply.Shadow {
			s = s.calibrated()
		}
		at := bc.at.Round(0).UTC()
		reply.Summary, reply.AsOf = &s, &at
	}
	return reply
}

// linkedOnline says whether pairtime login linked this computer, and
// whether config.toml, as uploads read it, also lets it upload.
func (a *agent) linkedOnline() (linked, online bool) {
	cfg, broken := UploadConfig(a.dirs)
	_, err := account.Load(a.dirs.Data)
	linked = err == nil
	return linked, linked && broken == "" && cfg.Mode == config.Online
}

// fetchBuildDays asks the server c names for its summary of c's account,
// and returns it, or the problem that kept it.
func (a *agent) fetchBuildDays(ctx context.Context, c uploader.Credentials) (*bdSummary, string) {
	status, _, body, err := askServer(ctx, c, http.MethodGet, buildDaysPath, nil)
	switch {
	case err != nil:
		a.log.Info("the server's build days didn't come", "err", err)
		return nil, problemUnreachable
	case status == http.StatusUnauthorized:
		return nil, problemSignedOut
	case status != http.StatusOK:
		a.log.Info("the server didn't give its build days", "status", status)
		return nil, problemUnreachable
	}
	var s bdSummary
	if err := json.Unmarshal(body, &s); err != nil || !s.valid() {
		a.log.Warn("the server's build days don't read as a summary", "err", err)
		return nil, problemUnreadable
	}
	return &s, ""
}

// askServer sends method path to the server c names, with c's device
// token and body as JSON when it isn't nil, and returns the answer's
// status, header and at most buildDaysMax bytes of its body. It follows
// no redirect, so the token goes to that server alone.
func askServer(ctx context.Context, c uploader.Credentials, method, path string, body []byte) (int, http.Header, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, buildDaysTimeout)
	defer cancel()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(c.Server, "/")+path, rd)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("User-Agent", agentUserAgent())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{
		Transport:     http.DefaultTransport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, buildDaysMax))
	if err != nil {
		return 0, nil, nil, err
	}
	return resp.StatusCode, resp.Header, data, nil
}

// agentUserAgent is the User-Agent the agent's requests carry, as the
// uploader's: the running build in tier 1's version form, or v0.0.0-dev.
func agentUserAgent() string {
	v := version.Get().Version
	if !event.IsTier1Version(v) {
		v = "v0.0.0-dev"
	}
	return "pairtime-agent/" + v
}

// goalChoice is PUT /v1/build-days/goal's body.
type goalChoice struct {
	WeeklyGoal *int64 `json:"weekly_goal"`
}

// goalShape is the 400 of a goal's body that isn't one.
const goalShape = `the body must be {"weekly_goal": n}, with n a whole number of build days from 1 to 7`

// serveBuildDaysGoal answers PUT /v1/build-days/goal: it sets the weekly
// goal on the server at once and answers GET /v1/build-days as the server
// has it then. It queues nothing, since a goal is a setting and not
// content: on a computer that isn't linked or is local-only, or whose
// server can't be reached, it refuses and says why, and the goal stays
// as it was (orchestrator, 2026-11-24).
func (a *agent) serveBuildDaysGoal(w http.ResponseWriter, r *http.Request) {
	var choice goalChoice
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&choice); err != nil || choice.WeeklyGoal == nil || dec.More() {
		http.Error(w, goalShape, http.StatusBadRequest)
		return
	}
	goal := *choice.WeeklyGoal
	if goal < minWeeklyGoal || goal > maxWeeklyGoal {
		http.Error(w, goalShape, http.StatusBadRequest)
		return
	}
	linked, online := a.linkedOnline()
	switch {
	case !linked:
		http.Error(w, "This computer isn't signed in to a pairtime server, which keeps your weekly goal. Run pairtime login, then set it.", http.StatusConflict)
		return
	case !online:
		http.Error(w, "This computer is local-only, so it sends the server nothing, and your weekly goal stays as it was. Set config.toml's mode to online to set it here.", http.StatusConflict)
		return
	}
	c, ok := a.credentials()
	if !ok {
		http.Error(w, "This computer's sign-in doesn't read, so pairtime can't reach the server. Run pairtime login again.", http.StatusConflict)
		return
	}
	body, _ := json.Marshal(map[string]int64{"weekly_goal": goal})
	status, header, _, err := askServer(r.Context(), c, http.MethodPut, buildGoalPath, body)
	switch {
	case err != nil:
		a.log.Info("the weekly goal didn't reach the server", "err", err)
		http.Error(w, "pairtime couldn't reach the server, so your weekly goal stays as it was. Try again once you're online.", http.StatusServiceUnavailable)
		return
	case status == http.StatusUnauthorized:
		http.Error(w, "The server no longer knows this computer's sign-in. Run pairtime login again.", http.StatusConflict)
		return
	case status == http.StatusTooManyRequests:
		wait, _ := strconv.Atoi(header.Get("Retry-After"))
		wait = max(wait, 1)
		w.Header().Set("Retry-After", strconv.Itoa(wait))
		http.Error(w, fmt.Sprintf("This computer has sent the server a lot just now. Try again in %d seconds.", wait), http.StatusTooManyRequests)
		return
	case status != http.StatusOK:
		a.log.Info("the server didn't set the weekly goal", "status", status)
		http.Error(w, "The server couldn't set your weekly goal just now, so it stays as it was. Try again in a minute.", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, a.buildDaysNow(r.Context(), true))
}
