// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"context"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/some-du6e/pairtime/internal/install/wakacfg"
)

// wakaKey is pairtime's key in these tests, and hackatimeKey the teen's,
// which doctor must never send anywhere.
const (
	wakaKey      = "00000000-0000-4000-8000-00000000a9e7"
	hackatimeKey = "00000000-0000-4000-8000-0000000048c1"
)

func TestWakaTimeCheck(t *testing.T) {
	answers := Destination{Host: "127.0.0.1:7247", Status: http.StatusOK}
	hackatime := &Destination{Host: "hackatime.hackclub.com", Status: http.StatusUnauthorized}
	tests := []struct {
		name   string
		w      *WakaTime
		shown  bool
		result Result
		text   string // a part of the line
		// notText is what the line must not hold.
		notText string
	}{
		{name: "with no line of pairtime's in ~/.wakatime.cfg, there is no WakaTime line"},
		{name: "both destinations answering is ok, the server's 401 to a request without a key included", w: &WakaTime{Pairtime: answers, Server: hackatime}, shown: true, result: OK, text: "both WakaTime destinations answer: pairtime's agent and hackatime.hackclub.com"},
		{name: "a server that answers 200 answers", w: &WakaTime{Pairtime: answers, Server: &Destination{Host: "hackatime.hackclub.com", Status: http.StatusOK}}, shown: true, result: OK, text: "both WakaTime destinations answer"},
		{name: "a server that answers 502, as Cloudflare does in front of one that is down, warns, since wakatime-cli's sends fail there too", w: &WakaTime{Pairtime: answers, Server: &Destination{Host: "hackatime.hackclub.com", Status: http.StatusBadGateway}}, shown: true, result: Warn, text: "hackatime.hackclub.com, the server wakatime-cli sends them to as well, answered with status 502. For about the first hour"},
		{name: "a filter's 403 warns as well", w: &WakaTime{Pairtime: answers, Server: &Destination{Host: "hackatime.hackclub.com", Status: http.StatusForbidden}}, shown: true, result: Warn, text: "answered with status 403"},
		{name: "in a config pairtime made, the agent answering is ok", w: &WakaTime{Pairtime: answers}, shown: true, result: OK, text: "sends your editor time to pairtime's agent, which answers"},
		{name: "a line that isn't there any more fails, saying so", w: &WakaTime{Problem: "pairtime's line isn't in ~/.wakatime.cfg any more"}, shown: true, result: Fail, text: "isn't in ~/.wakatime.cfg any more"},
		{name: "a config without a key for its server warns, since wakatime-cli sends nothing unless a plugin passes it one", w: &WakaTime{NoKey: true, Pairtime: answers, Server: hackatime}, shown: true, result: Warn, text: "has no key for its own server, and WAKATIME_API_KEY isn't set"},
		{name: "a key wakatime-cli refuses fails, since its whole run fails", w: &WakaTime{BadKey: "[api_urls]", Pairtime: answers, Server: hackatime}, shown: true, result: Fail, text: "wakatime-cli refuses the key in [api_urls] in ~/.wakatime.cfg, which isn't in WakaTime's format"},
		{name: "a WAKATIME_API_KEY wakatime-cli refuses fails as well", w: &WakaTime{BadKey: apiKeyEnv, Pairtime: answers, Server: hackatime}, shown: true, result: Fail, text: "refuses the key in WAKATIME_API_KEY, which isn't"},
		{name: "a config that sets a proxy fails as not set up, never ok, since wakatime-cli sends pairtime's line through the proxy", w: &WakaTime{Proxy: configProxy}, shown: true, result: Fail, text: "pairtime's WakaTime destination isn't set up: ~/.wakatime.cfg sets a proxy"},
		{name: "a proxy variable fails as not set up too, naming the variable", w: &WakaTime{Proxy: "HTTPS_PROXY"}, shown: true, result: Fail, text: "pairtime's WakaTime destination isn't set up: HTTPS_PROXY is set here"},
		{name: "an agent that doesn't answer fails, naming where it was asked", w: &WakaTime{Pairtime: Destination{Host: "127.0.0.1:7247", Err: "connection refused"}, Server: hackatime}, shown: true, result: Fail, text: "nothing answered at 127.0.0.1:7247 (connection refused)"},
		{name: "an agent that refuses the line's key fails", w: &WakaTime{Pairtime: Destination{Host: "127.0.0.1:7247", Status: http.StatusUnauthorized}}, shown: true, result: Fail, text: "refuses the key of pairtime's line"},
		{name: "an agent that refuses the line's key names the config install edited", w: &WakaTime{Config: "/x/.config/wakatime/wakatime.cfg", Pairtime: Destination{Host: "127.0.0.1:7247", Status: http.StatusUnauthorized}}, shown: true, result: Fail, text: "refuses the key of pairtime's line in /x/.config/wakatime/wakatime.cfg; run pairtime install again"},
		{name: "an agent without WakaTime's endpoint fails", w: &WakaTime{Pairtime: Destination{Host: "127.0.0.1:7247", Status: http.StatusNotFound}}, shown: true, result: Fail, text: "doesn't take WakaTime's heartbeats"},
		{name: "network error text with control characters, as in a certificate's names, shows none of them", w: &WakaTime{Pairtime: answers, Server: &Destination{Host: "hackatime.hackclub.com", Err: "x509: certificate is valid for \x1b]52;c;cGFpcnRpbWU=\x07evil.example, not hackatime.hackclub.com"}}, shown: true, result: Warn, text: "didn't answer (x509: certificate is valid for", notText: "\x1b"},
		{name: "an agent's error text shows no control characters either", w: &WakaTime{Pairtime: Destination{Host: "127.0.0.1:7247", Err: "read: \x1b[2Jreset"}}, shown: true, result: Fail, text: "nothing answered at 127.0.0.1:7247", notText: "\x1b"},
		{name: "a server that doesn't answer warns that wakatime-cli holds back what it sends to pairtime too", w: &WakaTime{Pairtime: answers, Server: &Destination{Host: "hackatime.hackclub.com", Err: "i/o timeout"}}, shown: true, result: Warn, text: "hackatime.hackclub.com, the server wakatime-cli sends them to as well, didn't answer (i/o timeout)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, shown := wakatimeCheck(tt.w)
			if shown != tt.shown {
				t.Fatalf("wakatimeCheck shows a line: %v, want %v (%+v)", shown, tt.shown, c)
			}
			if !shown {
				return
			}
			if c.Name != "wakatime" || c.Result != tt.result || !strings.Contains(c.Text, tt.text) {
				t.Errorf("wakatimeCheck = %+v, want wakatime, %s, a text with %q", c, tt.result, tt.text)
			}
			if tt.notText != "" && strings.Contains(c.Text, tt.notText) {
				t.Errorf("wakatimeCheck's text %q holds %q", c.Text, tt.notText)
			}
		})
	}
}

func TestGatherWakaTime(t *testing.T) {
	var asked atomic.Int32 // requests either destination got
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		switch {
		case r.URL.Path != "/api/v1/users/current/statusbar/today":
			http.NotFound(w, r)
		case r.Header.Get("Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte(wakaKey)):
			w.WriteHeader(http.StatusUnauthorized)
		default:
			w.Write([]byte(`{"data":{"text":"1 min"}}`))
		}
	}))
	defer agent.Close()
	var sentKey atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		if r.Header.Get("Authorization") != "" || r.URL.Query().Get("api_key") != "" {
			sentKey.Store(true)
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	gone := httptest.NewServer(http.NotFoundHandler())
	gonePort := gone.Listener.Addr().(*net.TCPAddr).Port
	gone.Close()

	port := agent.Listener.Addr().(*net.TCPAddr).Port
	serverHost := strings.TrimPrefix(server.URL, "http://")
	settings := "[settings]\napi_url = " + server.URL + "/api/hackatime/v1\napi_key = " + hackatimeKey + "\n"
	// elsewhere is a config of the teen's whose server is https, as
	// Hackatime's is, on a name that never resolves, so nothing leaves the
	// test.
	elsewhere := "[settings]\napi_url = https://wakapi.example/api\napi_key = " + hackatimeKey + "\n"
	line := func(port int) string { return "\n[api_urls]\n.+ = " + wakacfg.URL(port) + "|" + wakaKey + "\n" }
	made, err := wakacfg.Make(wakaKey, port)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		// config is what the config the setup file names holds, "" for no
		// config; setup says install wrote a setup file; env is
		// WAKATIME_API_KEY, and proxy and noProxy values for HTTPS_PROXY, or
		// proxyVar when it names another variable, and NO_PROXY.
		config   string
		setup    bool
		env      string
		proxy    string
		proxyVar string
		noProxy  string
		want     func(t *testing.T, w *WakaTime)
	}{
		{
			name: "with no setup file, there is nothing to show",
			want: func(t *testing.T, w *WakaTime) {
				if w != nil {
					t.Errorf("GatherWakaTime = %+v, want nil", w)
				}
			},
		},
		{
			name:   "both destinations are asked, the agent with pairtime's key and the server with none",
			config: settings + line(port), setup: true,
			want: func(t *testing.T, w *WakaTime) {
				if w == nil || w.Server == nil {
					t.Fatalf("GatherWakaTime = %+v, want both destinations", w)
				}
				if w.Problem != "" || w.NoKey || w.Pairtime.Status != http.StatusOK || w.Server.Status != http.StatusUnauthorized || w.Server.Host != serverHost {
					t.Errorf("GatherWakaTime = %+v (server %+v), want the agent's 200 and the server's 401 from %s", w, *w.Server, serverHost)
				}
				if c, _ := wakatimeCheck(w); c.Result != OK {
					t.Errorf("the line is %+v, want ok", c)
				}
			},
		},
		{
			name:   "in a config pairtime made, only the agent is asked",
			config: string(made), setup: true,
			want: func(t *testing.T, w *WakaTime) {
				if w == nil || w.Problem != "" || w.Server != nil || w.Pairtime.Status != http.StatusOK {
					t.Errorf("GatherWakaTime = %+v, want only the agent's 200", w)
				}
			},
		},
		{
			name:   "an agent that isn't listening has no status, and says why",
			config: settings + line(gonePort), setup: true,
			want: func(t *testing.T, w *WakaTime) {
				if w == nil || w.Pairtime.Status != 0 || w.Pairtime.Err == "" {
					t.Errorf("GatherWakaTime = %+v, want no answer from the agent", w)
				}
			},
		},
		{
			name:   "with a proxy in the config, neither destination is asked, and the line fails as not set up",
			config: settings + "proxy = http://proxy.example:3128\n" + line(port), setup: true,
			want: func(t *testing.T, w *WakaTime) {
				if w == nil || w.Proxy != configProxy || w.Server != nil || w.Pairtime != (Destination{}) {
					t.Errorf("GatherWakaTime = %+v, want only Proxy %q", w, configProxy)
				}
				if c, _ := wakatimeCheck(w); c.Result != Fail {
					t.Errorf("the line is %+v, want a failure", c)
				}
			},
		},
		{
			name:   "with HTTPS_PROXY set, neither destination is asked, and the line fails as not set up, naming the variable alone",
			config: elsewhere + line(port), setup: true, proxy: "http://teen:secret@proxy.example:3128",
			want: func(t *testing.T, w *WakaTime) {
				if w == nil || w.Proxy != "HTTPS_PROXY" || w.Server != nil || w.Pairtime != (Destination{}) {
					t.Fatalf("GatherWakaTime = %+v, want only Proxy HTTPS_PROXY", w)
				}
				if c, _ := wakatimeCheck(w); c.Result != Fail || strings.Contains(c.Text, "secret") {
					t.Errorf("the line is %+v, want a failure that shows no value", c)
				}
			},
		},
		{
			name:   "with HTTPS_PROXY set, a config pairtime made, whose only api_url is the agent on 127.0.0.1, which no environment proxy reaches, is ok",
			config: string(made), setup: true, proxy: "http://proxy.example:3128",
			want: func(t *testing.T, w *WakaTime) {
				if strings.Count(string(made), "api_url") != 1 || !strings.Contains(string(made), "api_url = http://127.0.0.1:") {
					t.Fatalf("the config pairtime makes is\n%s\nwant one api_url, the agent's", made)
				}
				if w == nil || w.Proxy != "" || w.Pairtime.Status != http.StatusOK {
					t.Fatalf("GatherWakaTime = %+v, want the agent's 200", w)
				}
				if c, _ := wakatimeCheck(w); c.Result != OK {
					t.Errorf("the line is %+v, want ok", c)
				}
			},
		},
		{
			name:   "with HTTPS_PROXY set, a config pairtime made whose api_url now names another server fails as not set up, as any config of the teen's",
			config: strings.Replace(strings.Replace(string(made), "api_url = "+wakacfg.URL(port), "api_url = https://wakapi.example/api", 1), "api_key = "+wakaKey, "api_key = "+hackatimeKey, 1), setup: true, proxy: "http://proxy.example:3128",
			want: func(t *testing.T, w *WakaTime) {
				if w == nil || w.Proxy != "HTTPS_PROXY" {
					t.Fatalf("GatherWakaTime = %+v, want Proxy HTTPS_PROXY", w)
				}
				if c, _ := wakatimeCheck(w); c.Result != Fail || !strings.Contains(c.Text, "isn't set up") {
					t.Errorf("the line is %+v, want a failure saying it isn't set up", c)
				}
			},
		},
		{
			name:   "with HTTPS_PROXY set and NO_PROXY naming the server's host, doctor asks both destinations, as wakatime-cli takes no proxy then",
			config: elsewhere + line(port), setup: true, proxy: "http://proxy.example:3128", noProxy: "wakapi.example",
			want: func(t *testing.T, w *WakaTime) {
				if w == nil || w.Proxy != "" || w.Pairtime.Status != http.StatusOK || w.Server == nil {
					t.Errorf("GatherWakaTime = %+v, want both destinations asked", w)
				}
			},
		},
		{
			name:   "with HTTP_PROXY set, a teen's http server on 127.0.0.1 is asked too, as no environment proxy reaches loopback",
			config: settings + line(port), setup: true, proxy: "http://proxy.example:3128", proxyVar: "HTTP_PROXY",
			want: func(t *testing.T, w *WakaTime) {
				if w == nil || w.Proxy != "" || w.Server == nil || w.Server.Status != http.StatusUnauthorized {
					t.Errorf("GatherWakaTime = %+v, want the server's 401", w)
				}
			},
		},
		{
			name:   "a config with no key of its own takes WAKATIME_API_KEY's, as wakatime-cli does",
			config: "[settings]\napi_url = " + server.URL + "/api/hackatime/v1\n" + line(port), setup: true, env: hackatimeKey,
			want: func(t *testing.T, w *WakaTime) {
				if w == nil || w.NoKey || w.BadKey != "" {
					t.Errorf("GatherWakaTime = %+v, want the server's key from WAKATIME_API_KEY", w)
				}
			},
		},
		{
			name:   "with no key in the config or in WAKATIME_API_KEY, wakatime-cli sends nothing",
			config: "[settings]\napi_url = " + server.URL + "/api/hackatime/v1\n" + line(port), setup: true,
			want: func(t *testing.T, w *WakaTime) {
				if w == nil || !w.NoKey {
					t.Errorf("GatherWakaTime = %+v, want NoKey", w)
				}
			},
		},
		{
			name:   "a WAKATIME_API_KEY not in WakaTime's format is a key wakatime-cli refuses",
			config: "[settings]\napi_url = " + server.URL + "/api/hackatime/v1\n" + line(port), setup: true, env: "nope",
			want: func(t *testing.T, w *WakaTime) {
				if w == nil || w.BadKey != apiKeyEnv {
					t.Errorf("GatherWakaTime = %+v, want BadKey %s", w, apiKeyEnv)
				}
			},
		},
		{
			name:   "an api_url net/url can't parse fails, as wakatime-cli then sends nothing anywhere, and shows neither its password nor its user name",
			config: "[settings]\napi_url = https://maya:hunter^2@wakapi.example/api\napi_key = " + hackatimeKey + "\n" + line(port), setup: true,
			want: func(t *testing.T, w *WakaTime) {
				if w == nil || !strings.Contains(w.Problem, "wakatime-cli can't read the api_url in ") || w.Server != nil {
					t.Fatalf("GatherWakaTime = %+v, want the api_url's problem and no request to the server", w)
				}
				if c, _ := wakatimeCheck(w); c.Result != Fail || strings.Contains(c.Text, "hunter") || strings.Contains(c.Text, "maya") {
					t.Errorf("the line is %+v, want a failure that shows nothing of the api_url's userinfo", c)
				}
			},
		},
		{
			name:   "an api_url with a user name and password is asked without them, as wakatime-cli never sends them",
			config: "[settings]\napi_url = " + strings.Replace(server.URL, "http://", "http://teen:pw@", 1) + "/api/hackatime/v1\napi_key = " + hackatimeKey + "\n" + line(port), setup: true,
			want: func(t *testing.T, w *WakaTime) {
				if w == nil || w.Server == nil || w.Server.Status != http.StatusUnauthorized {
					t.Fatalf("GatherWakaTime = %+v, want the server's 401", w)
				}
				if c, _ := wakatimeCheck(w); strings.Contains(c.Text, "teen") || strings.Contains(c.Text, "pw@") {
					t.Errorf("the line shows what the api_url's userinfo holds: %+v", c)
				}
			},
		},
		{
			name:   "a config pairtime made whose api_key the teen replaced fails, saying what to change, since the agent refuses that key",
			config: strings.Replace(string(made), "api_key = "+wakaKey, "api_key = "+hackatimeKey, 1), setup: true,
			want: func(t *testing.T, w *WakaTime) {
				if w == nil || !strings.Contains(w.Problem, "is the config pairtime made, with an api_key that isn't pairtime's any more") || !strings.Contains(w.Problem, "point its api_url at your own server, or delete it") {
					t.Errorf("GatherWakaTime = %+v, want the taken-over config's problem", w)
				}
			},
		},
		{
			name:   "a config that imports another is a problem, since a line there can take pairtime's place",
			config: settings + "import_cfg = ~/.private/wakatime.cfg\n" + line(port), setup: true,
			want: func(t *testing.T, w *WakaTime) {
				if w == nil || !strings.Contains(w.Problem, "imports another config with import_cfg") {
					t.Errorf("GatherWakaTime = %+v, want the import's problem", w)
				}
			},
		},
		{
			name:   "a config whose line is gone is a problem",
			config: settings, setup: true,
			want: func(t *testing.T, w *WakaTime) {
				if w == nil || !strings.Contains(w.Problem, "pairtime's line isn't in") {
					t.Errorf("GatherWakaTime = %+v, want the line's problem", w)
				}
			},
		},
		{
			name:  "a config that is gone is a problem",
			setup: true,
			want: func(t *testing.T, w *WakaTime) {
				if w == nil || !strings.Contains(w.Problem, "isn't there any more") {
					t.Errorf("GatherWakaTime = %+v, want the config's problem", w)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(apiKeyEnv, tt.env)
			for _, v := range wakacfg.ProxyVars {
				t.Setenv(v, "")
			}
			if tt.proxy != "" {
				v := tt.proxyVar
				if v == "" {
					v = "HTTPS_PROXY"
				}
				t.Setenv(v, tt.proxy)
			}
			if tt.noProxy != "" {
				t.Setenv("NO_PROXY", tt.noProxy)
			}
			data := t.TempDir()
			cfg := filepath.Join(t.TempDir(), ".wakatime.cfg")
			if tt.config != "" {
				if err := os.WriteFile(cfg, []byte(tt.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tt.setup {
				setup := wakacfg.SetupFile(data)
				if err := os.MkdirAll(filepath.Dir(setup), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(setup, wakacfg.Setup{Key: wakaKey, Config: cfg}.Encode(), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before := asked.Load()
			w := GatherWakaTime(context.Background(), data, nil)
			if w != nil && w.Proxy != "" && asked.Load() != before {
				t.Errorf("doctor asked a destination %d times though the config sets a proxy", asked.Load()-before)
			}
			tt.want(t, w)
		})
	}
	if sentKey.Load() {
		t.Error("doctor sent a key to the teen's server")
	}
}

// TestWakaTimeLinesNameTheConfig checks that doctor's WakaTime lines name
// the config install put pairtime's line in, as its setup file records it,
// here a config outside ~/.wakatime.cfg, as with WAKATIME_HOME or
// XDG_CONFIG_HOME, where creating ~/.wakatime.cfg would hide the real one.
func TestWakaTimeLinesNameTheConfig(t *testing.T) {
	gone := httptest.NewServer(http.NotFoundHandler())
	port := gone.Listener.Addr().(*net.TCPAddr).Port
	gone.Close()
	line := "[api_urls]\n.+ = " + wakacfg.URL(port) + "|" + wakaKey + "\n"
	tests := []struct {
		name, config string
		text         string // a part of the line, with <cfg> for the config's path
	}{
		{name: "a config that sets a proxy is named by its path", config: "[settings]\napi_url = https://wakapi.example/api\napi_key = " + hackatimeKey + "\nproxy = http://proxy.example:3128\n" + line, text: "<cfg> sets a proxy"},
		{name: "a config with no key for its server is named by its path", config: "[settings]\napi_url = https://wakapi.example/api\n" + line, text: "<cfg> has no key for its own server"},
		{name: "a key wakatime-cli refuses is named in the config's path", config: "[settings]\napi_url = https://wakapi.example/api\napi_key = nope\n" + line, text: "refuses the key in [settings] api_key in <cfg>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(apiKeyEnv, "")
			for _, v := range wakacfg.ProxyVars {
				t.Setenv(v, "")
			}
			data := t.TempDir()
			cfg := filepath.Join(t.TempDir(), "waka", ".wakatime.cfg")
			for path, body := range map[string][]byte{cfg: []byte(tt.config), wakacfg.SetupFile(data): wakacfg.Setup{Key: wakaKey, Config: cfg}.Encode()} {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, body, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			c, shown := wakatimeCheck(GatherWakaTime(context.Background(), data, nil))
			if want := strings.ReplaceAll(tt.text, "<cfg>", cfg); !shown || !strings.Contains(c.Text, want) {
				t.Errorf("wakatimeCheck = %+v, want a line saying %q", c, want)
			}
		})
	}
}

// TestWakaTimeAsksTheServerWakatimeCliSends checks that doctor asks the
// teen's server at the api_url wakatime-cli sends to, which it trims of a
// final "/", ".bulk" and the heartbeat paths, so a legacy api_url doesn't
// get a 404 that only doctor's request would.
func TestWakaTimeAsksTheServerWakatimeCliSends(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"text":"1 min"}}`))
	}))
	defer agent.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/users/current/statusbar/today" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	port := agent.Listener.Addr().(*net.TCPAddr).Port
	tests := []struct {
		name, apiURL string
	}{
		{"an api_url ending in /heartbeats is asked without it", server.URL + "/api/v1/heartbeats"},
		{"an api_url ending in the bulk heartbeats path is asked without it", server.URL + "/api/v1/users/current/heartbeats.bulk"},
		{"an api_url ending in /heartbeat is asked without it", server.URL + "/api/v1/heartbeat"},
		{"an api_url ending in a slash is asked without it", server.URL + "/api/v1/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(apiKeyEnv, "")
			for _, v := range wakacfg.ProxyVars {
				t.Setenv(v, "")
			}
			data := t.TempDir()
			cfg := filepath.Join(t.TempDir(), ".wakatime.cfg")
			config := "[settings]\napi_url = " + tt.apiURL + "\napi_key = " + hackatimeKey + "\n[api_urls]\n.+ = " + wakacfg.URL(port) + "|" + wakaKey + "\n"
			if err := os.WriteFile(cfg, []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			setup := wakacfg.SetupFile(data)
			if err := os.MkdirAll(filepath.Dir(setup), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(setup, wakacfg.Setup{Key: wakaKey, Config: cfg}.Encode(), 0o600); err != nil {
				t.Fatal(err)
			}
			w := GatherWakaTime(context.Background(), data, nil)
			if w == nil || w.Server == nil || w.Server.Status != http.StatusUnauthorized {
				t.Fatalf("GatherWakaTime = %+v, want the server's 401 at the URL wakatime-cli sends to", w)
			}
			if c, _ := wakatimeCheck(w); c.Result != OK {
				t.Errorf("the line is %+v, want ok", c)
			}
		})
	}
}
