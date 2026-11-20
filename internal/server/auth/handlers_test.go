// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLoginCookie(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	service := func() *Service {
		s, err := New(Config{BaseURL: "https://pairtime.example.org", Now: func() time.Time { return now }})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	a, b := service(), service()
	seal := func(st loginState) string {
		v, err := a.sealLogin(st)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	st := loginState{State: "the-state", Secret: "the-secret", ReturnTo: "/projects/demo", Fresh: true, Started: now.Unix()}
	good := seal(st)
	body, mac, _ := strings.Cut(good, ".")
	// forged edits the state after sealing and keeps good's HMAC.
	forged := func(edit func(*loginState)) string {
		f := st
		edit(&f)
		j, err := json.Marshal(f)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(j) + "." + mac
	}
	tests := []struct {
		name   string
		reader *Service
		value  string
		want   bool
	}{
		{name: "a cookie the service sealed reads back as it was", reader: a, value: good, want: true},
		{name: "a cookie another service sealed is refused", reader: b, value: good},
		{name: "a cookie whose fresh flag changed after sealing is refused", reader: a, value: forged(func(s *loginState) { s.Fresh = false })},
		{name: "a cookie whose start time changed after sealing is refused", reader: a, value: forged(func(s *loginState) { s.Started -= 300 })},
		{name: "a cookie whose secret changed after sealing is refused", reader: a, value: forged(func(s *loginState) { s.Secret = "another-secret" })},
		{name: "a cookie without its HMAC is refused", reader: a, value: body},
		{name: "a cookie with a cut HMAC is refused", reader: a, value: good[:len(good)-4]},
		{name: "a cookie sealed 11 minutes ago is refused", reader: a, value: seal(loginState{State: "s", Secret: "v", ReturnTo: "/", Started: now.Add(-11 * time.Minute).Unix()})},
		{name: "a cookie started over a minute in the future is refused", reader: a, value: seal(loginState{State: "s", Secret: "v", ReturnTo: "/", Started: now.Add(2 * time.Minute).Unix()})},
		{name: "a sealed cookie without a secret is refused", reader: a, value: seal(loginState{State: "s", ReturnTo: "/", Started: now.Unix()})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, CallbackPath, nil)
			req.AddCookie(&http.Cookie{Name: loginCookie, Value: tt.value})
			got, ok := tt.reader.readLoginState(req)
			if ok != tt.want {
				t.Fatalf("readLoginState ok = %v, want %v", ok, tt.want)
			}
			if ok && got != st {
				t.Error("readLoginState returned a state other than the one sealed")
			}
		})
	}
}

func TestNonceFor(t *testing.T) {
	secret := randomString()
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "the nonce is the base64url SHA-256 of the cookie's secret", ok: nonceFor("abc") == "ungWv48Bz-pBQUDeXa4iI7ADYaOWF3qctBD_YfIAFa0"},
		{name: "the nonce never shows the secret it comes from", ok: nonceFor(secret) != secret && !strings.Contains(nonceFor(secret), secret)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Error("doesn't hold")
			}
		})
	}
}

func TestSameOrigin(t *testing.T) {
	const base = "https://pairtime.example.org"
	tests := []struct {
		name      string
		baseURL   string // "" means base
		origin    string
		fetchSite string
		want      bool
	}{
		{name: "an Origin equal to base_url is the same origin", origin: base, want: true},
		{name: "a base_url with capitals matches the lowercase Origin browsers send", baseURL: "https://Pairtime.Example.org", origin: base, want: true},
		{name: "a base_url with the default port matches an Origin without it", baseURL: "https://pairtime.example.org:443", origin: base, want: true},
		{name: "a base_url with a trailing slash matches", baseURL: base + "/", origin: base, want: true},
		{name: "another host is refused", origin: "https://evil.example"},
		{name: "a host that only starts like pairtime's is refused", origin: "https://pairtime.example.org.evil.example"},
		{name: "http for an https base_url is refused", origin: "http://pairtime.example.org"},
		{name: "another port is refused", origin: "https://pairtime.example.org:8443"},
		{name: "the opaque Origin null is refused", origin: "null"},
		{name: "without Origin a same-origin fetch is allowed", fetchSite: "same-origin", want: true},
		{name: "without Origin a cross-site fetch is refused", fetchSite: "cross-site"},
		{name: "a request with neither header isn't from a browser and is allowed", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := tt.baseURL
			if b == "" {
				b = base
			}
			s, err := New(Config{BaseURL: b})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, LogoutPath, nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.fetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", tt.fetchSite)
			}
			if got := s.sameOrigin(req); got != tt.want {
				t.Errorf("sameOrigin = %v, want %v", got, tt.want)
			}
		})
	}
}
