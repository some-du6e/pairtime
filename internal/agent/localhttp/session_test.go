// SPDX-License-Identifier: AGPL-3.0-or-later

package localhttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeClock is a wall clock a test moves by hand.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func newClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 11, 20, 16, 0, 0, 0, time.UTC)}
}

func TestSessions(t *testing.T) {
	tests := []struct {
		name  string
		check func(t *testing.T, s *Sessions, c *fakeClock)
	}{
		{
			name: "a code redeems once, for a session that is valid",
			check: func(t *testing.T, s *Sessions, c *fakeClock) {
				id, ok := s.Redeem(s.NewCode())
				if !ok || !ValidToken(id) {
					t.Fatalf("Redeem = %q, %v; want a new session id", id, ok)
				}
				if !s.Valid(id) {
					t.Error("the new session isn't valid")
				}
			},
		},
		{
			name: "a code that was used is refused",
			check: func(t *testing.T, s *Sessions, c *fakeClock) {
				code := s.NewCode()
				s.Redeem(code)
				if _, ok := s.Redeem(code); ok {
					t.Error("a code redeemed twice")
				}
			},
		},
		{
			name: "a code is refused once CodeTTL has passed",
			check: func(t *testing.T, s *Sessions, c *fakeClock) {
				code := s.NewCode()
				c.t = c.t.Add(CodeTTL)
				if _, ok := s.Redeem(code); ok {
					t.Error("a code older than CodeTTL redeemed")
				}
			},
		},
		{
			name: "a code still works just before CodeTTL",
			check: func(t *testing.T, s *Sessions, c *fakeClock) {
				code := s.NewCode()
				c.t = c.t.Add(CodeTTL - time.Second)
				if _, ok := s.Redeem(code); !ok {
					t.Error("a code inside CodeTTL was refused")
				}
			},
		},
		{
			name: "an empty or unknown code is refused, and no session id is valid without a code",
			check: func(t *testing.T, s *Sessions, c *fakeClock) {
				for _, code := range []string{"", "nope", NewToken()} {
					if _, ok := s.Redeem(code); ok {
						t.Errorf("Redeem(%q) succeeded", code)
					}
				}
				if s.Valid("") || s.Valid(NewToken()) {
					t.Error("a session id no code made is valid")
				}
			},
		},
		{
			name: "a session that is used stays valid past SessionIdle from its start",
			check: func(t *testing.T, s *Sessions, c *fakeClock) {
				id, _ := s.Redeem(s.NewCode())
				for range 3 {
					c.t = c.t.Add(SessionIdle - time.Minute)
					if !s.Valid(id) {
						t.Fatal("a session used within SessionIdle ended")
					}
				}
			},
		},
		{
			name: "a session nothing used for SessionIdle ends, for good",
			check: func(t *testing.T, s *Sessions, c *fakeClock) {
				id, _ := s.Redeem(s.NewCode())
				c.t = c.t.Add(SessionIdle)
				if s.Valid(id) {
					t.Error("an idle session is still valid")
				}
				c.t = c.t.Add(-time.Hour)
				if s.Valid(id) {
					t.Error("an ended session came back when the clock went back")
				}
			},
		},
		{
			name: "past maxCodes waiting codes, a new code pushes out the one closest to expiring",
			check: func(t *testing.T, s *Sessions, c *fakeClock) {
				first := s.NewCode()
				var rest []string
				for range maxCodes {
					c.t = c.t.Add(time.Millisecond)
					rest = append(rest, s.NewCode())
				}
				if _, ok := s.Redeem(first); ok {
					t.Error("the oldest code survived past maxCodes")
				}
				for _, code := range rest {
					if _, ok := s.Redeem(code); !ok {
						t.Fatal("a newer code was pushed out")
					}
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newClock()
			tt.check(t, NewSessions(c.now), c)
		})
	}
}

// TestRequireAuth sends requests through Guard and RequireAuth arranged as
// the agent arranges them for the dashboard's API.
func TestRequireAuth(t *testing.T) {
	const port = 7247
	const token = "test-token"
	c := newClock()
	sessions := NewSessions(c.now)
	live, _ := sessions.Redeem(sessions.NewCode())
	idle, _ := sessions.Redeem(sessions.NewCode())
	h := Guard(port, RequireAuth(token, sessions, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("api"))
	})))

	tests := []struct {
		name     string
		method   string
		auth     string // the Authorization header; "" sends none
		cookie   string // the session cookie's value; "" sends none
		origin   string // the Origin header; "" sends none
		later    time.Duration
		wantCode int
	}{
		{name: "the bearer token passes", method: "GET", auth: "Bearer " + token, wantCode: 200},
		{name: "the bearer token passes a POST without an Origin, as local tools send it", method: "POST", auth: "Bearer " + token, wantCode: 200},
		{name: "a live session cookie passes a GET", method: "GET", cookie: live, wantCode: 200},
		{name: "a live session cookie passes a POST from the dashboard's Origin", method: "POST", cookie: live, origin: "http://127.0.0.1:7247", wantCode: 200},
		{name: "a live session cookie on a POST without an Origin gets 403", method: "POST", cookie: live, wantCode: 403},
		{name: "a live session cookie on a POST from another site gets 403 from Guard", method: "POST", cookie: live, origin: "https://attacker.example", wantCode: 403},
		{name: "no token and no cookie get 401", method: "GET", wantCode: 401},
		{name: "a cookie no sign-in made gets 401", method: "GET", cookie: NewToken(), wantCode: 401},
		{name: "a wrong token gets 401", method: "GET", auth: "Bearer nope", wantCode: 401},
		{name: "a session idle for SessionIdle gets 401", method: "GET", cookie: idle, later: SessionIdle, wantCode: 401},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.later > 0 {
				c.t = c.t.Add(tt.later)
				// The live session is used by every other case; keep it so.
				t.Cleanup(func() { c.t = c.t.Add(-tt.later) })
			}
			r := httptest.NewRequest(tt.method, "http://127.0.0.1:7247/v1/days", nil)
			r.RemoteAddr = "127.0.0.1:50000"
			if tt.auth != "" {
				r.Header.Set("Authorization", tt.auth)
			}
			if tt.cookie != "" {
				r.AddCookie(&http.Cookie{Name: CookieName, Value: tt.cookie})
			}
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tt.wantCode {
				t.Errorf("got %d %q, want %d", w.Code, w.Body.String(), tt.wantCode)
			}
			if tt.wantCode == 401 && w.Header().Get("WWW-Authenticate") != `Bearer realm="pairtime"` {
				t.Errorf("a 401 with WWW-Authenticate %q", w.Header().Get("WWW-Authenticate"))
			}
		})
	}
}

func TestSignIn(t *testing.T) {
	signIn := func(h http.Handler, method, code string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1:7247/signin?code="+code, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	tests := []struct {
		name  string
		check func(t *testing.T, s *Sessions, h http.Handler)
	}{
		{
			name: "a good code sets an HttpOnly, SameSite=Strict session cookie and sends the browser to /",
			check: func(t *testing.T, s *Sessions, h http.Handler) {
				w := signIn(h, "GET", s.NewCode())
				if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" {
					t.Fatalf("got %d to %q, want 303 to /", w.Code, w.Header().Get("Location"))
				}
				if w.Header().Get("Cache-Control") != "no-store" {
					t.Errorf("Cache-Control is %q, want no-store", w.Header().Get("Cache-Control"))
				}
				cookies := w.Result().Cookies()
				if len(cookies) != 1 {
					t.Fatalf("got %d cookies, want 1", len(cookies))
				}
				ck := cookies[0]
				if ck.Name != CookieName || ck.Path != "/" || !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode {
					t.Errorf("the cookie is %s; want %s, Path=/, HttpOnly, SameSite=Strict", ck, CookieName)
				}
				if !strings.Contains(w.Header().Get("Set-Cookie"), "SameSite=Strict") {
					t.Errorf("Set-Cookie is %q, without SameSite=Strict", w.Header().Get("Set-Cookie"))
				}
				if !s.Valid(ck.Value) {
					t.Error("the cookie's session isn't valid")
				}
			},
		},
		{
			name: "a code used once sends the browser to /?signin=expired with no cookie",
			check: func(t *testing.T, s *Sessions, h http.Handler) {
				code := s.NewCode()
				signIn(h, "GET", code)
				w := signIn(h, "GET", code)
				if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/?signin=expired" {
					t.Errorf("got %d to %q, want 303 to /?signin=expired", w.Code, w.Header().Get("Location"))
				}
				if len(w.Result().Cookies()) != 0 {
					t.Error("a used code set a cookie")
				}
			},
		},
		{
			name: "no code sends the browser to /?signin=expired",
			check: func(t *testing.T, s *Sessions, h http.Handler) {
				w := signIn(h, "GET", "")
				if w.Header().Get("Location") != "/?signin=expired" {
					t.Errorf("went to %q, want /?signin=expired", w.Header().Get("Location"))
				}
			},
		},
		{
			name: "a HEAD gets 405 and leaves the code for the GET",
			check: func(t *testing.T, s *Sessions, h http.Handler) {
				code := s.NewCode()
				if w := signIn(h, "HEAD", code); w.Code != http.StatusMethodNotAllowed {
					t.Errorf("HEAD got %d, want 405", w.Code)
				}
				if w := signIn(h, "GET", code); w.Header().Get("Location") != "/" {
					t.Errorf("the GET after a HEAD went to %q, want /", w.Header().Get("Location"))
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewSessions(newClock().now)
			tt.check(t, s, SignIn(s))
		})
	}
}
