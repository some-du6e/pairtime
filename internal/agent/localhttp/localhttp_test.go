// SPDX-License-Identifier: AGPL-3.0-or-later

package localhttp

import (
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestGuard sends requests through Guard and RequireToken arranged as the
// agent arranges them: the UI at / needs no token, the API under /v1/ does.
func TestGuard(t *testing.T) {
	const port = 7247
	const token = "test-token"
	mux := http.NewServeMux()
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ui")) }))
	mux.Handle("/v1/", RequireToken(token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("api")) })))
	h := Guard(port, mux)

	tests := []struct {
		name     string
		path     string
		host     string   // "" means 127.0.0.1:7247
		origins  []string // Origin headers to send
		remote   string   // "" means 127.0.0.1:50000
		auth     string   // the Authorization header; "" sends none
		wantCode int
	}{
		{name: "an API request with the token and a 127.0.0.1 Host passes", path: "/v1/status", auth: "Bearer " + token, wantCode: 200},
		{name: "localhost is an accepted Host", path: "/v1/status", host: "localhost:7247", auth: "Bearer " + token, wantCode: 200},
		{name: "Host is compared without case", path: "/v1/status", host: "LOCALHOST:7247", auth: "Bearer " + token, wantCode: 200},
		{name: "a foreign Host gets 403, which stops DNS rebinding", path: "/v1/status", host: "attacker.example:7247", auth: "Bearer " + token, wantCode: 403},
		{name: "a Host with another port gets 403", path: "/v1/status", host: "127.0.0.1:8080", auth: "Bearer " + token, wantCode: 403},
		{name: "a Host without the port gets 403", path: "/v1/status", host: "localhost", auth: "Bearer " + token, wantCode: 403},
		{name: "the dashboard's own Origin passes", path: "/v1/status", origins: []string{"http://127.0.0.1:7247"}, auth: "Bearer " + token, wantCode: 200},
		{name: "the dashboard's localhost Origin passes", path: "/v1/status", origins: []string{"http://localhost:7247"}, auth: "Bearer " + token, wantCode: 200},
		{name: "a foreign Origin gets 403, which stops cross-site requests", path: "/v1/status", origins: []string{"https://attacker.example"}, auth: "Bearer " + token, wantCode: 403},
		{name: "Origin null gets 403", path: "/v1/status", origins: []string{"null"}, auth: "Bearer " + token, wantCode: 403},
		{name: "an https Origin on the agent's port gets 403", path: "/v1/status", origins: []string{"https://127.0.0.1:7247"}, auth: "Bearer " + token, wantCode: 403},
		{name: "two Origin headers get 403", path: "/v1/status", origins: []string{"http://127.0.0.1:7247", "https://attacker.example"}, auth: "Bearer " + token, wantCode: 403},
		{name: "a request from another computer gets 403", path: "/v1/status", remote: "192.0.2.10:50000", auth: "Bearer " + token, wantCode: 403},
		{name: "an API request without a token gets 401", path: "/v1/status", wantCode: 401},
		{name: "an API request with the wrong token gets 401", path: "/v1/status", auth: "Bearer wrong", wantCode: 401},
		{name: "an API request with another scheme gets 401", path: "/v1/status", auth: "Basic " + token, wantCode: 401},
		{name: "the Bearer scheme is matched without case", path: "/v1/status", auth: "bearer " + token, wantCode: 200},
		{name: "a foreign Host is refused before the token is asked for", path: "/v1/status", host: "attacker.example:7247", wantCode: 403},
		{name: "a foreign Origin is refused before the token is asked for", path: "/v1/status", origins: []string{"https://attacker.example"}, wantCode: 403},
		{name: "the UI needs no token", path: "/", wantCode: 200},
		{name: "the UI still refuses a foreign Host", path: "/", host: "attacker.example:7247", wantCode: 403},
		{name: "the UI still refuses a foreign Origin", path: "/", origins: []string{"https://attacker.example"}, wantCode: 403},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", tt.path, nil)
			r.Host = "127.0.0.1:" + strconv.Itoa(port)
			if tt.host != "" {
				r.Host = tt.host
			}
			r.RemoteAddr = "127.0.0.1:50000"
			if tt.remote != "" {
				r.RemoteAddr = tt.remote
			}
			for _, o := range tt.origins {
				r.Header.Add("Origin", o)
			}
			if tt.auth != "" {
				r.Header.Set("Authorization", tt.auth)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tt.wantCode {
				t.Errorf("status = %d, want %d; body %q", w.Code, tt.wantCode, w.Body.String())
			}
			if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
				t.Errorf("X-Frame-Options = %q, want DENY on every response", got)
			}
			if tt.wantCode == 401 && !strings.HasPrefix(w.Header().Get("WWW-Authenticate"), "Bearer") {
				t.Errorf("a 401 without WWW-Authenticate: Bearer")
			}
		})
	}
}

func TestListen(t *testing.T) {
	// A port that is taken for the whole test.
	taken, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	takenPort := Port(taken)

	tests := []struct {
		name      string
		ports     []int
		wantErr   bool
		wantNotOn int // the port Listen must not return; 0 to skip
	}{
		{name: "port 0 gets any free port", ports: []int{0}},
		{name: "a taken port falls back to the next candidate", ports: []int{takenPort, 0}, wantNotOn: takenPort},
		{name: "when every candidate is taken, Listen fails", ports: []int{takenPort}, wantErr: true},
		{name: "no candidates is an error", ports: nil, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ln, err := Listen(tt.ports)
			if tt.wantErr {
				if err == nil {
					ln.Close()
					t.Fatal("Listen succeeded, want an error")
				}
				for _, p := range tt.ports {
					if !strings.Contains(err.Error(), strconv.Itoa(p)) {
						t.Errorf("the error %q doesn't name port %d", err, p)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			a := ln.Addr().(*net.TCPAddr)
			if !a.IP.Equal(net.IPv4(127, 0, 0, 1)) {
				t.Errorf("listening on %v, want 127.0.0.1 only", a.IP)
			}
			if a.Port == 0 || (tt.wantNotOn != 0 && a.Port == tt.wantNotOn) {
				t.Errorf("listening on port %d", a.Port)
			}
		})
	}
}

func TestCandidates(t *testing.T) {
	tests := []struct {
		name             string
		configured, last int
		want             []int
	}{
		{name: "with nothing set, the default port and the two after it, then any", want: []int{7247, 7248, 7249, 0}},
		{name: "the configured port comes first", configured: 9000, want: []int{9000, 7247, 7248, 7249, 0}},
		{name: "the last run's port comes before the default", last: 7248, want: []int{7248, 7247, 7249, 0}},
		{name: "the configured port beats the last run's", configured: 9000, last: 50123, want: []int{9000, 50123, 7247, 7248, 7249, 0}},
		{name: "repeats are dropped", configured: 7247, last: 7247, want: []int{7247, 7248, 7249, 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Candidates(tt.configured, tt.last); !slices.Equal(got, tt.want) {
				t.Errorf("Candidates(%d, %d) = %v, want %v", tt.configured, tt.last, got, tt.want)
			}
		})
	}
}

func TestTokens(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  bool
	}{
		{name: "NewToken makes a valid token", token: NewToken(), want: true},
		{name: "an empty token isn't valid", token: "", want: false},
		{name: "a short token isn't valid", token: "c2hvcnQ", want: false},
		{name: "a token that isn't base64url isn't valid", token: strings.Repeat("!", 43), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidToken(tt.token); got != tt.want {
				t.Errorf("ValidToken(%q) = %v, want %v", tt.token, got, tt.want)
			}
		})
	}
	if NewToken() == NewToken() {
		t.Error("two calls to NewToken returned the same token")
	}
}

func TestPortFile(t *testing.T) {
	tests := []struct {
		name string
		pf   PortFile
		want PortFile
	}{
		{
			name: "a written port file reads back, with its version set",
			pf:   PortFile{Port: 7247, Token: "abc", PID: 1234},
			want: PortFile{V: 1, Port: 7247, Token: "abc", PID: 1234},
		},
		{
			name: "pid 0 marks an agent that stopped cleanly",
			pf:   PortFile{Port: 7248, Token: "abc"},
			want: PortFile{V: 1, Port: 7248, Token: "abc", PID: 0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := filepath.Join(t.TempDir(), "port.json")
			if err := WritePortFile(name, tt.pf); err != nil {
				t.Fatal(err)
			}
			got, err := ReadPortFile(name)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("ReadPortFile() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
