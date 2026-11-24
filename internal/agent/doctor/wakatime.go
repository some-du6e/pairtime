// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/install/wakacfg"
)

// WakaTime is what doctor's WakaTime line is worked out from (DESIGN 5.9,
// "both WakaTime destinations reachable"): the setup install recorded for
// pairtime's line in ~/.wakatime.cfg (DESIGN 5.6), and how each of
// wakatime-cli's two destinations answered (GatherWakaTime).
type WakaTime struct {
	// Problem is what keeps wakatime-cli from sending to pairtime as
	// install set it up, "" when nothing does: the config is gone or can't
	// be read, or pairtime's line isn't in it any more.
	Problem string
	// NoKey says the config has no api_key for its own server, without
	// which wakatime-cli sends nothing at all, to pairtime neither.
	NoKey bool
	// Pairtime is the agent's endpoint, and Server the server
	// wakatime-cli's api_url names, such as Hackatime. Server is nil in a
	// config pairtime made, where the agent is the only destination.
	Pairtime Destination
	Server   *Destination
}

// Destination is how one of wakatime-cli's destinations answered doctor.
type Destination struct {
	// Host is the host and port the destination's URL names, never a key.
	Host string
	// Status is the HTTP status it answered with, 0 when it didn't answer,
	// and Err then says why.
	Status int
	Err    string
}

// wakatimeCheck works out the WakaTime line from w, and reports false when
// w is nil, for a computer where install never added pairtime's line,
// which has no WakaTime line.
func wakatimeCheck(w *WakaTime) (Check, bool) {
	if w == nil {
		return Check{}, false
	}
	c := Check{Name: "wakatime"}
	switch {
	case w.Problem != "":
		c.Result, c.Text = Fail, w.Problem
	case w.NoKey:
		c.Result, c.Text = Fail, "~/.wakatime.cfg has no api_key for its own server, so wakatime-cli sends your editor time nowhere, pairtime included"
	case w.Pairtime.Status != http.StatusOK:
		c.Result, c.Text = Fail, "wakatime-cli's heartbeats don't reach pairtime: "+agentWhy(w.Pairtime)
	case w.Server != nil && w.Server.Status == 0:
		c.Result = Warn
		c.Text = fmt.Sprintf("pairtime's agent takes wakatime-cli's heartbeats, but %s, the server wakatime-cli sends them to as well, didn't answer (%s). While it doesn't, wakatime-cli holds back what it sends to pairtime too, so editor time from then may not count",
			w.Server.Host, w.Server.Err)
	case w.Server == nil:
		c.Result, c.Text = OK, "wakatime-cli sends your editor time to pairtime's agent, which answers"
	default:
		c.Result, c.Text = OK, "both WakaTime destinations answer: pairtime's agent and "+w.Server.Host
	}
	return c, true
}

// agentWhy says why the agent's endpoint didn't take doctor's request.
func agentWhy(d Destination) string {
	switch d.Status {
	case 0:
		return fmt.Sprintf("nothing answered at %s (%s)", d.Host, d.Err)
	case http.StatusUnauthorized:
		return "the agent refuses the key of pairtime's line in ~/.wakatime.cfg; run pairtime install again"
	case http.StatusNotFound:
		return "the agent at " + d.Host + " doesn't take WakaTime's heartbeats"
	}
	return fmt.Sprintf("the agent at %s answered with status %d", d.Host, d.Status)
}

// wakatimeTimeout bounds each of GatherWakaTime's requests.
const wakatimeTimeout = 5 * time.Second

// GatherWakaTime finds WakaTime's line from the setup file install wrote
// in data, pairtime's data directory (wakacfg.SetupFile), and the config
// it names, and asks each destination for today's status bar, as
// wakatime-cli --today does: the agent's endpoint with pairtime's key, and
// the server with no key, since any answer says it can be reached and the
// teen's key stays in the config. A nil client is one with a timeout of
// wakatimeTimeout that follows no redirect. It returns nil when install
// wrote no setup file.
func GatherWakaTime(ctx context.Context, data string, client *http.Client) *WakaTime {
	s, err := wakacfg.ReadSetup(wakacfg.SetupFile(data))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return &WakaTime{Problem: fmt.Sprintf("pairtime's record of its line in ~/.wakatime.cfg can't be read (%v); run pairtime install again", err)}
	}
	cfg, err := os.ReadFile(s.Config)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return &WakaTime{Problem: s.Config + " isn't there any more, so wakatime-cli doesn't send your editor time to pairtime; run pairtime install again"}
	case err != nil:
		return &WakaTime{Problem: fmt.Sprintf("%s can't be read (%v)", s.Config, err)}
	}
	st, err := wakacfg.Inspect(cfg, s.Key)
	if err != nil {
		return &WakaTime{Problem: fmt.Sprintf("%s holds something pairtime doesn't read the way wakatime-cli does (%v)", s.Config, err)}
	}
	if !st.Line {
		return &WakaTime{Problem: "pairtime's line isn't in " + s.Config + " any more, so wakatime-cli doesn't send your editor time to pairtime; run pairtime install again"}
	}
	if client == nil {
		client = &http.Client{Timeout: wakatimeTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	w := &WakaTime{NoKey: !st.Own && !st.ServerKey, Pairtime: askWakaTime(ctx, client, wakacfg.URL(st.Port), s.Key)}
	if !st.Own {
		d := askWakaTime(ctx, client, st.Server, "")
		w.Server = &d
	}
	return w
}

// askWakaTime asks the destination at base, a WakaTime API URL, for
// today's status bar, with key as its Basic auth when there is one, as
// wakatime-cli sends it, and says how it answered.
func askWakaTime(ctx context.Context, client *http.Client, base, key string) Destination {
	d := Destination{Host: hostOf(base)}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(base, "/")+"/users/current/statusbar/today", nil)
	if err != nil {
		d.Err = err.Error()
		return d
	}
	if key != "" {
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(key)))
	}
	resp, err := client.Do(req)
	if err != nil {
		d.Err = err.Error()
		return d
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	resp.Body.Close()
	d.Status = resp.StatusCode
	return d
}

// hostOf returns the host and port of u, a URL, without what it may hold
// besides, a user name and password included, and words for the server
// when u isn't a URL.
func hostOf(u string) string {
	p, err := url.Parse(u)
	if err != nil || p.Host == "" {
		return "wakatime-cli's server"
	}
	return p.Host
}
