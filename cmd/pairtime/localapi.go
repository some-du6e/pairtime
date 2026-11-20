// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// The client side of the agent's local API, which `pairtime today`,
// `recap` and `statusline` read (docs/api/local.openapi.yaml). They read
// two routes the dashboard needs too: GET /v1/days/{date} and GET
// /v1/sessions/last. Serving them is WP-AG4's and WP-UI1's, which add
// them to the OpenAPI file with their handlers. The types below are the
// fields these views need.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/localhttp"
	"github.com/some-du6e/pairtime/pkg/event"
)

// tally is what the agent counts for one project over a span, a local day
// or a work session, from its minute buckets (SM 5.1, 5.3). Seconds may
// be halves.
type tally struct {
	// CreditedS is the credited seconds: your time.
	CreditedS float64 `json:"credited_s"`
	// PendingS is the part of CreditedS in buckets that are still pending
	// (DESIGN 3.7), which late records may change.
	PendingS float64 `json:"pending_s"`
	// CreditedByKind and CreditedByEvidence each split CreditedS.
	CreditedByKind     map[event.Activity]float64 `json:"credited_by_kind"`
	CreditedByEvidence map[event.Evidence]float64 `json:"credited_by_evidence"`
	// AgentOnlyS is the seconds a turn in the project ran and nothing
	// credited, whatever their reason code (SM 5.3's agent_only_s).
	AgentOnlyS float64 `json:"agent_only_s"`
	// AgentOnlyByHarness splits AgentOnlyS by the harness whose turn ran.
	// It names every harness that had a session in the project in the
	// span, with 0 for one that had no agent-only time, so the views can
	// name the agent whose answers were read.
	AgentOnlyByHarness map[event.Harness]float64 `json:"agent_only_by_harness"`
	// Uncredited is the seconds looked at and not credited, by reason
	// (SM 5.2). Agent-only seconds are in it too, under their own codes.
	Uncredited map[event.Reason]float64 `json:"uncredited"`
}

// dayReply is GET /v1/days/{date}: one local day, in the profile's time
// zone, with a tally for each project that has anything in it. The agent
// answers from its day rollups, days/YYYY-MM-DD.json (DESIGN 3.5), so the
// answer is quick enough for a status line.
type dayReply struct {
	// Date is the day, YYYY-MM-DD. Asked for "today", the agent says which
	// day that is.
	Date string `json:"date"`
	// Shadow is display.shadow (DESIGN 6.7 step 2): while it is on, every
	// view shows credited totals as calibrating.
	Shadow   bool         `json:"shadow"`
	Projects []dayProject `json:"projects"`
}

type dayProject struct {
	// Project is the project's key. Name is its display name from the local
	// project map; it never leaves the device.
	Project string `json:"project"`
	Name    string `json:"name"`
	tally
}

// sessionReply is GET /v1/sessions/last: the most recent work session
// (SM 5.3), or null before the first.
type sessionReply struct {
	Shadow  bool         `json:"shadow"`
	Session *workSession `json:"session"`
}

type workSession struct {
	Project string    `json:"project"`
	Name    string    `json:"name"`
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	// Open is set while the session goes on: no SESSION_GAP_S without
	// credit since End, and no lock or suspend (DESIGN 3.8).
	Open bool `json:"open"`
	tally
}

// The ways asking the agent fails that the views tell apart.
var (
	// errAgentDown is no agent to ask: port.json is missing or says the
	// agent stopped, or nothing accepts connections on its port.
	errAgentDown = errors.New("the agent isn't running")
	// errAgentOld is an agent that answered 404: it is older than the
	// route.
	errAgentOld = errors.New("the running agent doesn't serve this")
	// errAgentSlow is an agent that didn't answer in time.
	errAgentSlow = errors.New("the agent didn't answer in time")
)

// maxReply bounds what the views read from the agent.
const maxReply = 1 << 20

// apiClient talks to the agent on 127.0.0.1 only. It uses no proxy, so
// the bearer token never goes anywhere else, follows no redirect, and
// keeps no connection open after the one request a view makes.
var apiClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                  nil,
		DialContext:            (&net.Dialer{}).DialContext,
		DisableKeepAlives:      true,
		MaxResponseHeaderBytes: 64 << 10,
	},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// askAgent sends GET path to the running agent, with the bearer token from
// port.json, and decodes its JSON answer into v. ctx bounds the whole
// exchange.
func askAgent(ctx context.Context, path string, v any) error {
	dirs, err := home.Default()
	if err != nil {
		return err
	}
	pf, err := localhttp.ReadPortFile(dirs.PortFile())
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return errAgentDown
	case err != nil:
		return err
	case pf.PID == 0:
		// The agent wrote 0 when it stopped cleanly.
		return errAgentDown
	case pf.Port < 1 || pf.Port > 65535 || !localhttp.ValidToken(pf.Token):
		return fmt.Errorf("%s has no usable port and token", dirs.PortFile())
	}
	url := "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(pf.Port)) + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+pf.Token)
	req.Header.Set("Accept", "application/json")
	resp, err := apiClient.Do(req)
	if err != nil {
		return dialFailure(ctx, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return errAgentOld
	default:
		return fmt.Errorf("the agent answered %s", resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxReply)).Decode(v); err != nil {
		if timedOut(ctx, err) {
			return errAgentSlow
		}
		return fmt.Errorf("the agent's answer didn't parse: %w", err)
	}
	return nil
}

// dialFailure sorts an error from sending the request: out of time, or no
// agent listening, or something else.
func dialFailure(ctx context.Context, err error) error {
	if timedOut(ctx, err) {
		return errAgentSlow
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return errAgentDown
	}
	return err
}

// timedOut reports whether err came from ctx's deadline. A dial bounded by
// a context can fail with its own i/o timeout a moment before ctx.Err() is
// set, so a network timeout counts as well.
func timedOut(ctx context.Context, err error) bool {
	if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
