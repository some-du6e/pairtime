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
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/localhttp"
	"github.com/some-du6e/pairtime/internal/coach/text"
	"github.com/some-du6e/pairtime/internal/install/wakacfg"
)

// WakaTime is what doctor's WakaTime line is worked out from (DESIGN 5.9,
// "both WakaTime destinations reachable"): the setup install recorded for
// pairtime's line in ~/.wakatime.cfg (DESIGN 5.6), and how each of
// wakatime-cli's two destinations answered (GatherWakaTime).
type WakaTime struct {
	// Config is the config install put pairtime's line in, as its setup
	// file records it: ~/.wakatime.cfg, or $WAKATIME_HOME's or
	// $XDG_CONFIG_HOME's when wakatime-cli reads that one (wakacfg.Find).
	// The lines name it, and "" names ~/.wakatime.cfg.
	Config string
	// Problem is what keeps wakatime-cli from sending to pairtime as
	// install set it up, "" when nothing does: the config is gone or can't
	// be read, or pairtime's line isn't in it any more.
	Problem string
	// NoKey says the config has no key for its own server, and
	// WAKATIME_API_KEY none either, without which wakatime-cli sends
	// nothing at all, to pairtime neither, unless a plugin passes it one
	// with --key.
	NoKey bool
	// BadKey names where wakatime-cli finds a key it refuses for its
	// format, which fails its whole run (wakacfg.State.BadKey), or
	// WAKATIME_API_KEY when that is where its key comes from; "" when it
	// finds none.
	BadKey string
	// Proxy is where a proxy comes from that wakatime-cli would send
	// pairtime's line through too, so that the line can't do what install
	// set it up for, "" when there is none: configProxy for the config's
	// own (wakacfg.ErrProxy), or the name of the variable of doctor's
	// environment whose proxy wakatime-cli takes for the config's main
	// api_url, never its value, which can hold a password
	// (wakacfg.ProxyFor). Doctor then asks neither destination. A proxy an
	// editor's plugin passes wakatime-cli with --proxy, as WakaTime's plugin
	// for JetBrains IDEs passes the IDE's, is out of doctor's sight.
	Proxy string
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
	cfg := w.Config
	if cfg == "" {
		cfg = "~/.wakatime.cfg"
	}
	switch {
	case w.Problem != "":
		c.Result, c.Text = Fail, w.Problem
	case w.Proxy == configProxy:
		c.Result, c.Text = Fail, "pairtime's WakaTime destination isn't set up: "+cfg+" sets a proxy, and wakatime-cli sends pairtime's line through it too, so the proxy gets your project and file names unencrypted, and while it doesn't pass them on to pairtime's agent, each send that fails holds back what wakatime-cli sends your own server; take the proxy out of "+cfg+", or take pairtime's line out with pairtime uninstall"
	case w.Proxy != "":
		c.Result, c.Text = Fail, "pairtime's WakaTime destination isn't set up: "+w.Proxy+" is set here, and wakatime-cli started with it sends pairtime's line through that proxy too, so the proxy gets your project and file names unencrypted, and while it doesn't pass them on to pairtime's agent, each send that fails holds back what wakatime-cli sends your own server; start your editors without "+w.Proxy+", or take pairtime's line out with pairtime uninstall"
	case w.BadKey != "":
		where := w.BadKey + " in " + cfg
		if w.BadKey == apiKeyEnv {
			where = apiKeyEnv
		}
		c.Result, c.Text = Fail, "wakatime-cli refuses the key in "+where+", which isn't in WakaTime's format, so it sends your editor time nowhere, pairtime included"
	case w.NoKey:
		// A plugin can still give wakatime-cli a key with --key, which
		// doctor can't see, so this warns.
		c.Result, c.Text = Warn, cfg+" has no key for its own server, and WAKATIME_API_KEY isn't set, so unless your editor's WakaTime plugin gives wakatime-cli a key itself, it sends your editor time nowhere, pairtime included"
	case w.Pairtime.Status != http.StatusOK:
		c.Result, c.Text = Fail, "wakatime-cli's heartbeats don't reach pairtime: "+agentWhy(w.Pairtime, cfg)
	case w.Server != nil && !takesHeartbeats(w.Server.Status):
		c.Result = Warn
		c.Text = fmt.Sprintf("pairtime's agent takes wakatime-cli's heartbeats, but %s, the server wakatime-cli sends them to as well, %s. For about the first hour that it doesn't take them, wakatime-cli holds back what it sends to pairtime too, so editor time from then may not count",
			w.Server.Host, serverWhy(*w.Server))
	case w.Server == nil:
		c.Result, c.Text = OK, "wakatime-cli sends your editor time to pairtime's agent, which answers"
	default:
		c.Result, c.Text = OK, "both WakaTime destinations answer: pairtime's agent and "+w.Server.Host
	}
	// Error text from the network can hold what a server or a man in the
	// middle chose, such as a certificate's names, which a terminal would
	// act on.
	c.Text = text.Printable(c.Text)
	return c, true
}

// takesHeartbeats reports whether status is how a WakaTime server that takes
// heartbeats answers doctor's request, which has no key: 401, as Hackatime,
// WakaTime and the agent answer it, or a 2xx. Any other status, such as a
// 502 from Cloudflare in front of a server that is down, a filter's 403 or
// the 404 of a wrong api_url, is one wakatime-cli's sends get too, which
// count as failed and hold back every destination's (erratum 2026-11-23,
// item 4).
func takesHeartbeats(status int) bool { return status == http.StatusUnauthorized || status/100 == 2 }

// serverWhy says how the server didn't answer as one that takes heartbeats.
func serverWhy(d Destination) string {
	if d.Status == 0 {
		return fmt.Sprintf("didn't answer (%s)", d.Err)
	}
	return fmt.Sprintf("answered with status %d", d.Status)
}

// agentWhy says why the agent's endpoint didn't take doctor's request, for
// pairtime's line in cfg.
func agentWhy(d Destination, cfg string) string {
	switch d.Status {
	case 0:
		return fmt.Sprintf("nothing answered at %s (%s)", d.Host, d.Err)
	case http.StatusUnauthorized:
		return "the agent refuses the key of pairtime's line in " + cfg + "; run pairtime install again"
	case http.StatusNotFound:
		return "the agent at " + d.Host + " doesn't take WakaTime's heartbeats"
	}
	return fmt.Sprintf("the agent at %s answered with status %d", d.Host, d.Status)
}

// configProxy is WakaTime.Proxy for a proxy that the config sets.
const configProxy = "[settings] proxy"

// apiKeyEnv is the environment variable wakatime-cli takes its key from
// when its config has none (pkg/params loadAPIKey).
const apiKeyEnv = "WAKATIME_API_KEY"

// wakatimeTimeout bounds each of GatherWakaTime's requests.
const wakatimeTimeout = 5 * time.Second

// GatherWakaTime finds WakaTime's line from the setup file install wrote
// in data, pairtime's data directory (wakacfg.SetupFile), and the config
// it names, and asks each destination for today's status bar, as
// wakatime-cli --today does: the agent's endpoint with pairtime's key, and
// the server with no key, since any answer says it can be reached and the
// teen's key stays in the config. A nil client is one with a timeout of
// wakatimeTimeout that follows no redirect. It returns nil when install
// wrote no setup file, and asks neither destination when a proxy would
// carry pairtime's line, which keeps the line from working whatever they
// answer: one the config sets, or, for a config that isn't pairtime's own,
// one doctor's environment sets, as the editors' most often does too. A
// config pairtime made has the agent on 127.0.0.1 as its only api_url,
// which Go's environment proxies never reach. Nor does it ask them when
// wakatime-cli can't read the config's api_url as a URL, which stops its
// whole run, or when the config is one pairtime made, as the setup file
// records or, without that record, pairtime's mark says, whose api_key the
// teen replaced, which the agent refuses.
func GatherWakaTime(ctx context.Context, data string, client *http.Client) *WakaTime {
	s, err := wakacfg.ReadSetup(wakacfg.SetupFile(data))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return &WakaTime{Problem: fmt.Sprintf("pairtime's record of its WakaTime line can't be read (%v); run pairtime install again", err)}
	}
	cfg, err := os.ReadFile(s.Config)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return &WakaTime{Config: s.Config, Problem: s.Config + " isn't there any more, so wakatime-cli doesn't send your editor time to pairtime; run pairtime install again"}
	case err != nil:
		return &WakaTime{Config: s.Config, Problem: fmt.Sprintf("%s can't be read (%v)", s.Config, err)}
	}
	// The setup file is install's record: a config it says install made is
	// pairtime's by that record, whether or not pairtime's mark is still in
	// it.
	made := wakacfg.ByMark
	if s.Made {
		made = wakacfg.Recorded
	}
	// The agent's own ports: the one install wrote the line or the config
	// with, and the one port.json says it listens on now. An api_url on
	// 127.0.0.1 on any other port is a server of the teen's own, such as
	// Wakapi's, even in a config the setup file says install made.
	ports := []int{s.Port}
	if pf, err := localhttp.ReadPortFile(home.Dirs{Data: data}.PortFile()); err == nil {
		ports = append(ports, pf.Port)
	}
	st, err := wakacfg.Inspect(cfg, s.Key, made, ports...)
	if err != nil {
		return &WakaTime{Config: s.Config, Problem: fmt.Sprintf("%s holds something pairtime doesn't read the way wakatime-cli does (%v)", s.Config, err)}
	}
	if st.Proxy {
		return &WakaTime{Config: s.Config, Proxy: configProxy}
	}
	if v := wakacfg.ProxyFor(st.Server, os.Getenv); v != "" && !st.Own {
		return &WakaTime{Config: s.Config, Proxy: v}
	}
	if st.Import {
		return &WakaTime{Config: s.Config, Problem: s.Config + " imports another config with import_cfg, which wakatime-cli reads over it, so a line there can take the place of pairtime's; take import_cfg out, or take pairtime's line out with pairtime uninstall"}
	}
	if _, err := url.Parse(st.Server); err != nil && !st.Own {
		// wakatime-cli's loadAPIURL refuses it, and its whole run with it.
		// The URL isn't shown: one net/url can't parse can hold a password.
		return &WakaTime{Config: s.Config, Problem: "wakatime-cli can't read the api_url in " + s.Config + " as a URL, so it sends your editor time nowhere, pairtime included"}
	}
	if st.TakenOver {
		// The route that keeps the teen's key comes first: pairtime made
		// the file, so no backup of install's holds that key.
		return &WakaTime{Config: s.Config, Problem: s.Config + " is the config pairtime made, with an api_key that isn't pairtime's any more, so pairtime's agent refuses what wakatime-cli sends it, and your editor time goes nowhere; to send your editor time to pairtime too, set its api_url to your own server, such as Hackatime's, keep your api_key, and run pairtime install again"}
	}
	if !st.Line {
		return &WakaTime{Config: s.Config, Problem: "pairtime's line isn't in " + s.Config + " any more, so wakatime-cli doesn't send your editor time to pairtime; run pairtime install again"}
	}
	if client == nil {
		client = &http.Client{Timeout: wakatimeTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	w := &WakaTime{Config: s.Config, BadKey: st.BadKey, Pairtime: askWakaTime(ctx, client, wakacfg.URL(st.Port), s.Key)}
	if !st.Own && !st.ServerKey {
		switch k := os.Getenv(apiKeyEnv); {
		case k == "":
			w.NoKey = true
		case !wakacfg.ValidKey(k) && w.BadKey == "":
			w.BadKey = apiKeyEnv
		}
	}
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
	// base is normalized already, as wakatime-cli's is, which appends its
	// paths to it as it is.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/users/current/statusbar/today", nil)
	if err != nil {
		d.Err = urlFree(err)
		return d
	}
	// net/http would send the URL's user name and password as Basic auth,
	// which wakatime-cli never does, since it sets its own.
	req.URL.User = nil
	if key != "" {
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(key)))
	}
	resp, err := client.Do(req)
	if err != nil {
		d.Err = urlFree(err)
		return d
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	resp.Body.Close()
	d.Status = resp.StatusCode
	return d
}

// urlFree returns err's text without the URL a *url.Error names: one that
// net/url can't parse comes in it whole, a password included, and one it
// parses keeps its user name.
func urlFree(err error) string {
	var u *url.Error
	if errors.As(err, &u) {
		return u.Err.Error()
	}
	return err.Error()
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
