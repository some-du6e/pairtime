// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/localhttp"
)

// fakeAgent answers POST /v1/dashboard/link the way the agent does, with
// the link a test picks, and only with the token.
func fakeAgent(t *testing.T, token string, reply func(port string) (int, string)) int {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/dashboard/link" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_, port, _ := strings.Cut(r.Host, ":")
		code, body := reply(port)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().(*net.TCPAddr).Port
}

func goodLink(port string) (int, string) {
	return http.StatusCreated, `{"url":"http://127.0.0.1:` + port + `/signin?code=abc","expires_in_s":120}`
}

func TestDashboard(t *testing.T) {
	const token = "test-token"
	type world struct {
		dir     string
		port    int // the fake agent's
		opened  []string
		started int
	}
	writePort := func(t *testing.T, w *world, pid int) {
		t.Helper()
		pf := localhttp.PortFile{Port: w.port, Token: token, PID: pid}
		if err := localhttp.WritePortFile(home.Dirs{Data: w.dir}.PortFile(), pf); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name     string
		args     []string
		reply    func(port string) (int, string)
		setup    func(t *testing.T, w *world)
		start    func(t *testing.T, w *world) error // nil: the agent can't be started
		open     error                              // what opening the browser returns
		wantCode int
		check    func(t *testing.T, w *world, stdout, stderr string)
	}{
		{
			name:  "with the agent running it opens the agent's link and prints it to paste",
			reply: goodLink,
			setup: func(t *testing.T, w *world) { writePort(t, w, 4242) },
			check: func(t *testing.T, w *world, stdout, stderr string) {
				want := fmt.Sprintf("http://127.0.0.1:%d/signin?code=abc", w.port)
				if len(w.opened) != 1 || w.opened[0] != want {
					t.Errorf("opened %v, want %s", w.opened, want)
				}
				if !strings.Contains(stdout, want) || !strings.Contains(stdout, "It works once, for the next 2 minutes") {
					t.Errorf("stdout %q doesn't give the link and how long it works", stdout)
				}
				if w.started != 0 {
					t.Error("it started a second agent")
				}
			},
		},
		{
			name:  "--no-open prints the link alone and opens nothing",
			args:  []string{"--no-open"},
			reply: goodLink,
			setup: func(t *testing.T, w *world) { writePort(t, w, 4242) },
			check: func(t *testing.T, w *world, stdout, stderr string) {
				if want := fmt.Sprintf("http://127.0.0.1:%d/signin?code=abc\n", w.port); stdout != want || len(w.opened) != 0 {
					t.Errorf("stdout %q, opened %v; want %q and nothing opened", stdout, w.opened, want)
				}
			},
		},
		{
			name:  "with no port.json it starts the agent and opens the link once the agent answers",
			reply: goodLink,
			start: func(t *testing.T, w *world) error {
				writePort(t, w, 4242)
				return nil
			},
			check: func(t *testing.T, w *world, stdout, stderr string) {
				if w.started != 1 || len(w.opened) != 1 {
					t.Errorf("started %d agents and opened %v, want 1 and the link", w.started, w.opened)
				}
			},
		},
		{
			name:  "a port.json from an agent that stopped cleanly means starting one",
			reply: goodLink,
			setup: func(t *testing.T, w *world) { writePort(t, w, 0) },
			start: func(t *testing.T, w *world) error {
				writePort(t, w, 4242)
				return nil
			},
			check: func(t *testing.T, w *world, stdout, stderr string) {
				if w.started != 1 || len(w.opened) != 1 {
					t.Errorf("started %d agents and opened %v, want 1 and the link", w.started, w.opened)
				}
			},
		},
		{
			name:     "when the agent can't be started it says so and exits 1",
			reply:    goodLink,
			start:    func(t *testing.T, w *world) error { return errors.New("no such file") },
			wantCode: 1,
			check: func(t *testing.T, w *world, stdout, stderr string) {
				if !strings.Contains(stderr, "isn't running, and starting it failed: no such file") || len(w.opened) != 0 {
					t.Errorf("stderr %q, opened %v", stderr, w.opened)
				}
			},
		},
		{
			name:     "when a started agent never answers it gives up after the wait and exits 1",
			reply:    goodLink,
			start:    func(t *testing.T, w *world) error { return nil },
			wantCode: 1,
			check: func(t *testing.T, w *world, stdout, stderr string) {
				if !strings.Contains(stderr, "didn't start within") || len(w.opened) != 0 {
					t.Errorf("stderr %q, opened %v", stderr, w.opened)
				}
			},
		},
		{
			name: "a link that isn't the agent's own sign-in page on 127.0.0.1 is never opened",
			reply: func(port string) (int, string) {
				return http.StatusCreated, `{"url":"https://example.com/signin?code=abc","expires_in_s":120}`
			},
			setup:    func(t *testing.T, w *world) { writePort(t, w, 4242) },
			wantCode: 1,
			check: func(t *testing.T, w *world, stdout, stderr string) {
				if len(w.opened) != 0 || !strings.Contains(stderr, "won't open it") {
					t.Errorf("opened %v, stderr %q", w.opened, stderr)
				}
			},
		},
		{
			name:     "an agent that refuses the token says what it answered and exits 1",
			reply:    func(port string) (int, string) { return http.StatusUnauthorized, "unauthorized" },
			setup:    func(t *testing.T, w *world) { writePort(t, w, 4242) },
			wantCode: 1,
			check: func(t *testing.T, w *world, stdout, stderr string) {
				if !strings.Contains(stderr, "401") || w.started != 0 {
					t.Errorf("stderr %q, started %d", stderr, w.started)
				}
			},
		},
		{
			name:  "when no browser opens it prints the link to paste and exits 0",
			reply: goodLink,
			setup: func(t *testing.T, w *world) { writePort(t, w, 4242) },
			open:  errors.New("xdg-open: no method available"),
			check: func(t *testing.T, w *world, stdout, stderr string) {
				if !strings.Contains(stdout, "couldn't open a browser (xdg-open: no method available)") || !strings.Contains(stdout, "/signin?code=abc") {
					t.Errorf("stdout %q", stdout)
				}
			},
		},
		{name: "an argument is a usage error", args: []string{"today"}, wantCode: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &world{dir: t.TempDir()}
			if tt.reply != nil {
				w.port = fakeAgent(t, token, tt.reply)
			}
			if tt.setup != nil {
				tt.setup(t, w)
			}
			deps := dashboardDeps{
				dirs: func() (home.Dirs, error) { return home.Dirs{Data: w.dir, Config: w.dir, Runtime: w.dir}, nil },
				startAgent: func() error {
					w.started++
					if tt.start == nil {
						return errors.New("this test starts no agent")
					}
					return tt.start(t, w)
				},
				openURL: func(u string) error {
					w.opened = append(w.opened, u)
					return tt.open
				},
				wait:   time.Second,
				client: &http.Client{Timeout: 2 * time.Second},
			}
			var stdout, stderr bytes.Buffer
			if code := dashboard(tt.args, &stdout, &stderr, deps); code != tt.wantCode {
				t.Fatalf("exit %d, want %d; stdout %q, stderr %q", code, tt.wantCode, stdout.String(), stderr.String())
			}
			if tt.check != nil {
				tt.check(t, w, stdout.String(), stderr.String())
			}
		})
	}
}

func TestWords(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{name: "whole minutes are minutes", d: 2 * time.Minute, want: "2 minutes"},
		{name: "one minute is singular", d: time.Minute, want: "1 minute"},
		{name: "anything else is seconds", d: 90 * time.Second, want: "90 seconds"},
		{name: "one second is singular", d: time.Second, want: "1 second"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := words(tt.d); got != tt.want {
				t.Errorf("words(%s) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}
