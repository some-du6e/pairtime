// SPDX-License-Identifier: AGPL-3.0-or-later

// Package localhttp is the agent's HTTP server on 127.0.0.1: the local
// dashboard, harness HTTP hooks, OTLP/JSON, WakaTime heartbeats and plugin
// events all arrive here (DESIGN 3.3, 3.9; CP 6.3).
//
// Any web page the teen opens can send requests to 127.0.0.1, and so can
// every other user's processes, so every request passes Guard. It must come
// from a loopback address and carry a Host of 127.0.0.1:<port> or
// localhost:<port>, which defeats DNS rebinding, and an Origin, if it has
// one, must be the dashboard's own, which defeats cross-site requests. API
// routes also need the bearer token from port.json, which only this user
// can read, or the session cookie `pairtime dashboard` signs the teen's
// browser in with (session.go).
package localhttp

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"

	"github.com/some-du6e/pairtime/internal/agent/home"
)

// DefaultPort is the port the agent tries first: PAIR on a phone keypad.
// It is below the ephemeral port ranges of Windows, macOS and Linux, so
// outgoing connections don't take it.
const DefaultPort = 7247

// Candidates lists the ports to try, in order: the configured port; the
// port of the last run, so the port stays put across restarts, as harness
// hook settings and WakaTime's config expect; DefaultPort and the two after
// it; and last 0, for any free port. Zeros and repeats are dropped.
func Candidates(configured, last int) []int {
	var ports []int
	for _, p := range []int{configured, last, DefaultPort, DefaultPort + 1, DefaultPort + 2} {
		if p > 0 && !contains(ports, p) {
			ports = append(ports, p)
		}
	}
	return append(ports, 0)
}

func contains(ports []int, p int) bool {
	for _, q := range ports {
		if q == p {
			return true
		}
	}
	return false
}

// Listen binds 127.0.0.1, and never another address, on the first of ports
// that is free. Port 0 means any free port. The error names every port that
// failed.
func Listen(ports []int) (net.Listener, error) {
	var errs []error
	for _, p := range ports {
		ln, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)))
		if err == nil {
			return ln, nil
		}
		errs = append(errs, err)
	}
	if len(errs) == 0 {
		return nil, errors.New("localhttp: no ports to try")
	}
	return nil, errors.Join(errs...)
}

// Port returns the port ln listens on.
func Port(ln net.Listener) int {
	if a, ok := ln.Addr().(*net.TCPAddr); ok {
		return a.Port
	}
	return 0
}

// Guard refuses, with 403, a request that doesn't come from a loopback
// address, whose Host isn't 127.0.0.1:port or localhost:port, or whose
// Origin, when it has one, isn't the dashboard's own. It also sets headers
// that keep every response out of other sites' frames.
func Guard(port int, next http.Handler) http.Handler {
	p := strconv.Itoa(port)
	hosts := []string{"127.0.0.1:" + p, "localhost:" + p}
	origins := []string{"http://127.0.0.1:" + p, "http://localhost:" + p}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		if !fromLoopback(r.RemoteAddr) {
			http.Error(w, "forbidden: the connection isn't from this computer", http.StatusForbidden)
			return
		}
		if !oneOf(r.Host, hosts) {
			http.Error(w, "forbidden: the Host header must be "+hosts[0]+" or "+hosts[1], http.StatusForbidden)
			return
		}
		if o := r.Header.Values("Origin"); len(o) > 1 || (len(o) == 1 && !oneOf(o[0], origins)) {
			http.Error(w, "forbidden: requests from other sites are refused", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// oneOf reports whether s is one of list, ignoring ASCII case, as host
// names do.
func oneOf(s string, list []string) bool {
	for _, x := range list {
		if strings.EqualFold(s, x) {
			return true
		}
	}
	return false
}

func fromLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

// RequireToken refuses, with 401, a request without the header
// "Authorization: Bearer <token>". Routes the dashboard calls take its
// session cookie too, through RequireAuth; a route behind RequireToken
// alone is for local tools.
func RequireToken(token string, next http.Handler) http.Handler {
	if token == "" {
		panic("localhttp: RequireToken needs a token")
	}
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hasBearer(r, want) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="pairtime"`)
			http.Error(w, "unauthorized: send the bearer token from port.json", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// PortFile is port.json, which tells local clients how to reach the agent.
// WritePortFile writes it for its owner only, because the token opens the
// API (DESIGN 3.5; CP 6.3). The token outlives restarts: the installer
// writes it into harness hook settings that can't read files.
type PortFile struct {
	V     int    `json:"v"`
	Port  int    `json:"port"`
	Token string `json:"token"`
	// PID is the agent's process id, or 0 once it has stopped cleanly.
	PID int `json:"pid"`
}

// ReadPortFile reads port.json.
func ReadPortFile(name string) (PortFile, error) {
	data, err := os.ReadFile(name)
	if err != nil {
		return PortFile{}, err
	}
	var pf PortFile
	if err := json.Unmarshal(data, &pf); err != nil {
		return PortFile{}, fmt.Errorf("%s: %w", name, err)
	}
	return pf, nil
}

// WritePortFile writes port.json with home.WritePrivate.
func WritePortFile(name string, pf PortFile) error {
	pf.V = 1
	data, err := json.Marshal(pf)
	if err != nil {
		return err
	}
	return home.WritePrivate(name, append(data, '\n'))
}

// tokenBytes is how much randomness a token carries.
const tokenBytes = 32

// NewToken returns a new bearer token: 32 random bytes in unpadded
// base64url.
func NewToken() string {
	b := make([]byte, tokenBytes)
	rand.Read(b) // never fails: crypto/rand crashes the program first
	return base64.RawURLEncoding.EncodeToString(b)
}

// ValidToken reports whether t has the form NewToken gives, so a damaged
// port.json gets a new token rather than a weak one.
func ValidToken(t string) bool {
	b, err := base64.RawURLEncoding.DecodeString(t)
	return err == nil && len(b) == tokenBytes
}
