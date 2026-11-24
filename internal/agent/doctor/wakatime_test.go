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
	}{
		{name: "with no line of pairtime's in ~/.wakatime.cfg, there is no WakaTime line"},
		{name: "both destinations answering is ok, whatever status the server gives a request without a key", w: &WakaTime{Pairtime: answers, Server: hackatime}, shown: true, result: OK, text: "both WakaTime destinations answer: pairtime's agent and hackatime.hackclub.com"},
		{name: "in a config pairtime made, the agent answering is ok", w: &WakaTime{Pairtime: answers}, shown: true, result: OK, text: "sends your editor time to pairtime's agent, which answers"},
		{name: "a line that isn't there any more fails, saying so", w: &WakaTime{Problem: "pairtime's line isn't in ~/.wakatime.cfg any more"}, shown: true, result: Fail, text: "isn't in ~/.wakatime.cfg any more"},
		{name: "a config without an api_key fails, since wakatime-cli sends nothing", w: &WakaTime{NoKey: true, Pairtime: answers, Server: hackatime}, shown: true, result: Fail, text: "no api_key for its own server"},
		{name: "an agent that doesn't answer fails, naming where it was asked", w: &WakaTime{Pairtime: Destination{Host: "127.0.0.1:7247", Err: "connection refused"}, Server: hackatime}, shown: true, result: Fail, text: "nothing answered at 127.0.0.1:7247 (connection refused)"},
		{name: "an agent that refuses the line's key fails", w: &WakaTime{Pairtime: Destination{Host: "127.0.0.1:7247", Status: http.StatusUnauthorized}}, shown: true, result: Fail, text: "refuses the key of pairtime's line"},
		{name: "an agent without WakaTime's endpoint fails", w: &WakaTime{Pairtime: Destination{Host: "127.0.0.1:7247", Status: http.StatusNotFound}}, shown: true, result: Fail, text: "doesn't take WakaTime's heartbeats"},
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
		})
	}
}

func TestGatherWakaTime(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	line := func(port int) string { return "\n[api_urls]\n.+ = " + wakacfg.URL(port) + "|" + wakaKey + "\n" }
	made, err := wakacfg.Make(wakaKey, port)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		// config is what the config the setup file names holds, "" for no
		// config; setup says install wrote a setup file.
		config string
		setup  bool
		want   func(t *testing.T, w *WakaTime)
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
			tt.want(t, GatherWakaTime(context.Background(), data, nil))
		})
	}
	if sentKey.Load() {
		t.Error("doctor sent a key to the teen's server")
	}
}
