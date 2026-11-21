// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/ipc"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/agent/localhttp"
	"github.com/some-du6e/pairtime/internal/agent/state"
)

// syncBuffer collects the agent's log for a failing test to print.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// running is an agent started by startAgent.
type running struct {
	cancel context.CancelFunc
	done   chan struct{}
	err    error // Run's result, once done is closed
	port   localhttp.PortFile
}

// startAgent runs the agent on its own goroutine and waits until it is
// ready. The test's cleanup stops it.
func startAgent(t *testing.T, opts Options) *running {
	t.Helper()
	ready := make(chan struct{})
	opts.Ready = func() { close(ready) }
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{cancel: cancel, done: make(chan struct{})}
	go func() {
		r.err = Run(ctx, opts)
		close(r.done)
	}()
	t.Cleanup(func() {
		cancel()
		<-r.done
	})
	select {
	case <-ready:
	case <-r.done:
		t.Fatalf("Run returned before the agent was ready: %v", r.err)
	case <-time.After(20 * time.Second):
		t.Fatal("the agent wasn't ready after 20 s")
	}
	pf, err := localhttp.ReadPortFile(opts.Dirs.PortFile())
	if err != nil {
		t.Fatal(err)
	}
	r.port = pf
	return r
}

// waitExit waits for Run to return and gives its result.
func (r *running) waitExit(t *testing.T) error {
	t.Helper()
	select {
	case <-r.done:
		return r.err
	case <-time.After(20 * time.Second):
		t.Fatal("Run didn't return within 20 s")
		return nil
	}
}

func request(t *testing.T, method, url, host, origin, token string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

func getStatus(t *testing.T, r *running) statusReply {
	t.Helper()
	code, body := request(t, "GET", fmt.Sprintf("http://127.0.0.1:%d/v1/status", r.port.Port), "", "", r.port.Token)
	if code != http.StatusOK {
		t.Fatalf("GET /v1/status = %d %s", code, body)
	}
	var s statusReply
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

// waitStatus polls the status until ok holds, for up to 10 s.
func waitStatus(t *testing.T, r *running, ok func(statusReply) bool) statusReply {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		s := getStatus(t, r)
		if ok(s) {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("the status never got there; last: %+v", s)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func readAgentState(t *testing.T, dirs home.Dirs) agentState {
	t.Helper()
	st, problem, err := state.Open(dirs.StateFile())
	if err != nil || problem != "" {
		t.Fatalf("state.Open: %v %s", err, problem)
	}
	var s agentState
	if ok, err := st.Get(agentKey, &s); !ok || err != nil {
		t.Fatalf("state.json has no agent entry: %v %v", ok, err)
	}
	return s
}

// TestRun starts a real agent on a temporary data directory and goes
// through its life: first start, the HTTP rules, a second agent, a config
// edit, a clean stop over HTTP, and a restart.
func TestRun(t *testing.T) {
	// Separate data and config directories, as on Windows and Linux.
	dirs := home.Dirs{Data: t.TempDir(), Config: t.TempDir()}
	dirs.Runtime = dirs.Data
	logs := &syncBuffer{}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("agent log:\n%s", logs.String())
		}
	})
	opts := Options{Dirs: dirs, Log: slog.New(slog.NewTextHandler(logs, nil)), Ports: []int{0}, KeyStores: []keys.Store{}}

	a := startAgent(t, opts)
	base := fmt.Sprintf("http://127.0.0.1:%d", a.port.Port)

	t.Run("port.json has the agent's pid and a valid token", func(t *testing.T) {
		if a.port.PID != os.Getpid() || !localhttp.ValidToken(a.port.Token) || a.port.Port == 0 {
			t.Errorf("port.json = %+v", a.port)
		}
	})

	httpTests := []struct {
		name     string
		method   string
		path     string
		host     string
		origin   string
		token    bool
		wantCode int
		wantBody string
	}{
		{name: "status with the token answers 200 and says local mode", method: "GET", path: "/v1/status", token: true, wantCode: 200, wantBody: `"mode":"local"`},
		{name: "status without the token answers 401", method: "GET", path: "/v1/status", wantCode: 401},
		{name: "today's day with the token answers 200 with its projects", method: "GET", path: "/v1/days/today", token: true, wantCode: 200, wantBody: `"projects":[`},
		{name: "a day without the token or a session answers 401", method: "GET", path: "/v1/days/today", wantCode: 401},
		{name: "a day that isn't a date answers 400", method: "GET", path: "/v1/days/yesterday", token: true, wantCode: 400},
		{name: "the last session before any work is null", method: "GET", path: "/v1/sessions/last", token: true, wantCode: 200, wantBody: `"session":null`},
		{name: "a foreign Host answers 403", method: "GET", path: "/v1/status", host: "attacker.example", token: true, wantCode: 403},
		{name: "a foreign Origin answers 403", method: "GET", path: "/v1/status", origin: "https://attacker.example", token: true, wantCode: 403},
		{name: "stop needs POST", method: "GET", path: "/v1/agent/stop", token: true, wantCode: 405},
		{name: "stop without the token answers 401", method: "POST", path: "/v1/agent/stop", wantCode: 401},
		{name: "the placeholder dashboard is served without a token", method: "GET", path: "/", wantCode: 200, wantBody: "pairtime"},
		{name: "the dashboard refuses POST", method: "POST", path: "/", wantCode: 405},
		{name: "a new agent's day has no projects", method: "GET", path: "/v1/days/today", token: true, wantCode: 200, wantBody: `"projects":[]`},
		{name: "a sign-in link isn't served over HTTP, since the CLI asks the socket for it", method: "POST", path: "/v1/dashboard/link", token: true, wantCode: 404},
	}
	for _, tt := range httpTests {
		t.Run(tt.name, func(t *testing.T) {
			token := ""
			if tt.token {
				token = a.port.Token
			}
			code, body := request(t, tt.method, base+tt.path, tt.host, tt.origin, token)
			if code != tt.wantCode || !strings.Contains(body, tt.wantBody) {
				t.Errorf("%s %s = %d %q, want %d with %q", tt.method, tt.path, code, body, tt.wantCode, tt.wantBody)
			}
		})
	}

	t.Run("a link from the socket signs a browser in once, with a cookie that reads and can't stop the agent", func(t *testing.T) {
		ep, err := ipc.EndpointFor(dirs)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ans, err := ipc.Ask(ctx, ep, ipc.AskDashboardLink)
		if err != nil {
			t.Fatalf("asking the socket for a link: %v", err)
		}
		var link ipc.DashboardLink
		if err := json.Unmarshal(ans, &link); err != nil || !strings.HasPrefix(link.URL, base+"/signin?code=") || link.ExpiresInS != int(localhttp.CodeTTL/time.Second) {
			t.Fatalf("the socket's link is %s (%v), want one to %s/signin that works for %s", ans, err, base, localhttp.CodeTTL)
		}
		// A browser follows the 303 itself; this client stops at it.
		client := &http.Client{
			Timeout:       10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
		signIn := func() *http.Response {
			resp, err := client.Get(link.URL)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			return resp
		}
		first := signIn()
		var session *http.Cookie
		for _, c := range first.Cookies() {
			if c.Name == localhttp.CookieName {
				session = c
			}
		}
		if first.StatusCode != http.StatusSeeOther || first.Header.Get("Location") != "/" || session == nil || !session.HttpOnly || session.SameSite != http.SameSiteStrictMode {
			t.Fatalf("GET /signin = %d to %q with cookie %v; want 303 to / with an HttpOnly, SameSite=Strict session", first.StatusCode, first.Header.Get("Location"), session)
		}
		withCookie := func(method, path string) int {
			req, err := http.NewRequest(method, base+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.AddCookie(session)
			// A browser's fetch from the dashboard's page sends its Origin.
			req.Header.Set("Origin", base)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			return resp.StatusCode
		}
		for _, path := range []string{"/v1/status", "/v1/days/today", "/v1/sessions/last"} {
			if code := withCookie("GET", path); code != http.StatusOK {
				t.Errorf("GET %s with the session cookie = %d, want 200", path, code)
			}
		}
		if code := withCookie("POST", "/v1/agent/stop"); code != http.StatusUnauthorized {
			t.Errorf("POST /v1/agent/stop with the session cookie = %d, want 401: only the token stops the agent", code)
		}
		if s := getStatus(t, a); s.PID != os.Getpid() {
			t.Errorf("the agent's status after the cookie's stop = %+v, want it running", s)
		}
		if again := signIn(); again.Header.Get("Location") != "/?signin=expired" || len(again.Cookies()) != 0 {
			t.Errorf("the link used a second time went to %q with %d cookies, want /?signin=expired and none", again.Header.Get("Location"), len(again.Cookies()))
		}
	})

	t.Run("the socket's status question answers what GET /v1/status gives, for doctor", func(t *testing.T) {
		ep, err := ipc.EndpointFor(dirs)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ans, err := ipc.Ask(ctx, ep, ipc.AskStatus)
		if err != nil {
			t.Fatalf("asking the socket for the status: %v", err)
		}
		var asked statusReply
		if err := json.Unmarshal(ans, &asked); err != nil {
			t.Fatalf("the socket's status %s doesn't parse: %v", ans, err)
		}
		got := getStatus(t, a)
		// The counters and problems may move between the two reads; what
		// says which agent this is, and what it reads, doesn't.
		same := func(s statusReply) string {
			b, err := json.Marshal([]any{s.Version, s.PID, s.Started, s.Mode, s.Port, s.Signals})
			if err != nil {
				t.Fatal(err)
			}
			return string(b)
		}
		if same(asked) != same(got) {
			t.Errorf("the socket's status is %+v, GET /v1/status's %+v", asked, got)
		}
	})

	t.Run("a second agent on the same data directory gets ErrAlreadyRunning", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := Run(ctx, opts); !errors.Is(err, ErrAlreadyRunning) {
			t.Errorf("second Run = %v, want ErrAlreadyRunning", err)
		}
	})

	t.Run("the first start writes config.toml in local mode", func(t *testing.T) {
		cfg, problems, err := config.Load(dirs.ConfigFile())
		if err != nil || len(problems) != 0 || cfg.Mode != config.Local {
			t.Errorf("config.Load = %+v, %q, %v", cfg, problems, err)
		}
	})

	t.Run("state.json counts the start", func(t *testing.T) {
		if s := readAgentState(t, dirs); s.Starts != 1 || s.Started.IsZero() || !s.Stopped.IsZero() {
			t.Errorf("agent state = %+v, want one start and no stop", s)
		}
	})

	t.Run("an unknown mode in config.toml is reported and the agent stays local", func(t *testing.T) {
		if err := os.WriteFile(dirs.ConfigFile(), []byte("mode = \"cloud\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		waitStatus(t, a, func(s statusReply) bool {
			return strings.Contains(s.Problems["config"], `"cloud"`) && s.Mode == config.Local
		})
	})

	t.Run("fixing config.toml clears the problem", func(t *testing.T) {
		if err := config.WriteDefault(dirs.ConfigFile()); err != nil {
			t.Fatal(err)
		}
		waitStatus(t, a, func(s statusReply) bool { return s.Problems["config"] == "" })
	})

	t.Run("the status reply says where idle time, focus, power and the lock state come from, or why each is unknown (D-design-5.9)", func(t *testing.T) {
		s := getStatus(t, a)
		for name, sig := range map[string]signalReply{"idle": s.Signals.Idle, "focus": s.Signals.Focus, "power": s.Signals.Power, "lock": s.Signals.Lock} {
			if (sig.Via == "") == (sig.Unknown == "") {
				t.Errorf("signals.%s = %+v, want exactly one of via and unknown", name, sig)
			}
		}
	})

	t.Run("POST /v1/agent/stop stops the agent cleanly", func(t *testing.T) {
		if code, body := request(t, "POST", base+"/v1/agent/stop", "", "", a.port.Token); code != http.StatusAccepted {
			t.Fatalf("POST /v1/agent/stop = %d %s", code, body)
		}
		if err := a.waitExit(t); err != nil {
			t.Fatalf("Run = %v after a stop request, want nil", err)
		}
	})

	t.Run("a clean stop sets pid 0 in port.json and keeps the token", func(t *testing.T) {
		pf, err := localhttp.ReadPortFile(dirs.PortFile())
		if err != nil {
			t.Fatal(err)
		}
		if pf.PID != 0 || pf.Token != a.port.Token {
			t.Errorf("port.json after the stop = %+v, want pid 0 and the token %s", pf, a.port.Token)
		}
	})

	t.Run("a clean stop records the stop in state.json", func(t *testing.T) {
		if s := readAgentState(t, dirs); s.Stopped.Before(s.Started) {
			t.Errorf("agent state = %+v, want a stop after the start", s)
		}
	})

	t.Run("a restart keeps the token and counts a second start", func(t *testing.T) {
		b := startAgent(t, opts)
		if b.port.Token != a.port.Token {
			t.Errorf("the token changed across a restart")
		}
		if s := readAgentState(t, dirs); s.Starts != 2 {
			t.Errorf("starts = %d, want 2", s.Starts)
		}
		b.cancel()
		if err := b.waitExit(t); err != nil {
			t.Errorf("Run = %v after its context was cancelled, want nil", err)
		}
	})
}
