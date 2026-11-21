// SPDX-License-Identifier: AGPL-3.0-or-later

package localhttp

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
	"sync"
	"time"
)

// The dashboard's sign-in (DESIGN 3.9). The dashboard runs in the teen's
// own browser, which can't read port.json, so `pairtime dashboard` asks
// the agent for a one-time code over the per-user socket, which needs no
// token, and opens /signin?code=<code> from a file only this user can
// read, so the code is on no command line (D-dashboard-link). The agent
// trades the code for a session cookie. The cookie is SameSite=Strict, so
// no other site's page can send it, and HttpOnly, so no script can read
// it. Sessions live in the agent's memory only: a restart signs every
// browser out, and `pairtime dashboard` signs one back in.
//
// A browser sends a cookie for 127.0.0.1 to every port there, not only the
// agent's, so a server on another port of this computer that the browser
// visits could replay the session. That server is one this computer runs;
// Guard keeps every other computer and every other site out.

const (
	// CookieName is the session cookie's name.
	CookieName = "pairtime_session"
	// CodeTTL is how long a sign-in code works.
	CodeTTL = 2 * time.Minute
	// SessionIdle ends a session no request has used for this long.
	SessionIdle = 8 * time.Hour
	// maxCodes bounds the codes waiting to be used. A new one past it
	// pushes out the one closest to expiring.
	maxCodes = 32
)

// Sessions holds the sign-in codes and the sessions they started. It keeps
// only the SHA-256 of each code and session id, so a map lookup, not a
// byte-by-byte comparison with what a request sent, finds them. Its methods
// are safe for concurrent use.
type Sessions struct {
	now func() time.Time

	mu       sync.Mutex
	codes    map[[sha256.Size]byte]time.Time // when each code stops working
	sessions map[[sha256.Size]byte]time.Time // when each session was last used
}

// NewSessions returns a Sessions with no codes or sessions. now is the
// wall clock; nil means time.Now.
func NewSessions(now func() time.Time) *Sessions {
	if now == nil {
		now = time.Now
	}
	return &Sessions{
		now:      now,
		codes:    make(map[[sha256.Size]byte]time.Time),
		sessions: make(map[[sha256.Size]byte]time.Time),
	}
}

func digest(s string) [sha256.Size]byte { return sha256.Sum256([]byte(s)) }

// wall reads the clock without its monotonic reading, so the two minutes
// of a code and the eight hours of a session run on while the computer
// sleeps, as the wall clock does (CP 2.2).
func (s *Sessions) wall() time.Time { return s.now().Round(0) }

// NewCode returns a code that Redeem takes once, within CodeTTL.
func (s *Sessions) NewCode() string {
	code := NewToken()
	now := s.wall()
	s.mu.Lock()
	defer s.mu.Unlock()
	for d, until := range s.codes {
		if !now.Before(until) {
			delete(s.codes, d)
		}
	}
	for len(s.codes) >= maxCodes {
		var first [sha256.Size]byte
		var soonest time.Time
		for d, until := range s.codes {
			if soonest.IsZero() || until.Before(soonest) {
				first, soonest = d, until
			}
		}
		delete(s.codes, first)
	}
	s.codes[digest(code)] = now.Add(CodeTTL)
	return code
}

// Redeem trades a code from NewCode for a new session id. A code works
// once: Redeem reports false for one that is unknown, used or older than
// CodeTTL.
func (s *Sessions) Redeem(code string) (string, bool) {
	if code == "" {
		return "", false
	}
	now := s.wall()
	d := digest(code)
	s.mu.Lock()
	defer s.mu.Unlock()
	until, ok := s.codes[d]
	delete(s.codes, d)
	if !ok || !now.Before(until) {
		return "", false
	}
	for d, last := range s.sessions {
		if now.Sub(last) >= SessionIdle {
			delete(s.sessions, d)
		}
	}
	id := NewToken()
	s.sessions[digest(id)] = now
	return id, true
}

// Valid reports whether id is a live session, and counts the call as a use
// that keeps it alive for another SessionIdle.
func (s *Sessions) Valid(id string) bool {
	if id == "" {
		return false
	}
	now := s.wall()
	d := digest(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	last, ok := s.sessions[d]
	if !ok {
		return false
	}
	if now.Sub(last) >= SessionIdle {
		delete(s.sessions, d)
		return false
	}
	s.sessions[d] = now
	return true
}

// RequireAuth passes a request that carries the bearer token, as local
// tools send it, or a live session cookie, as the dashboard does, and
// answers anything else with 401. A request the cookie signs in that isn't
// a GET or HEAD must also carry an Origin, which Guard has checked is the
// dashboard's own. Browsers send one with every such fetch, so a change
// without one didn't come from the dashboard's page, and it gets 403.
func RequireAuth(token string, sessions *Sessions, next http.Handler) http.Handler {
	if token == "" {
		panic("localhttp: RequireAuth needs a token")
	}
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hasBearer(r, want) {
			next.ServeHTTP(w, r)
			return
		}
		c, err := r.Cookie(CookieName)
		if err != nil || !sessions.Valid(c.Value) {
			unauthorized(w)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("Origin") == "" {
			http.Error(w, "forbidden: a change asked for with the dashboard's cookie must come from the dashboard's page", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hasBearer reports whether r has the header "Authorization: Bearer
// <token>".
func hasBearer(r *http.Request, token []byte) bool {
	scheme, got, _ := strings.Cut(r.Header.Get("Authorization"), " ")
	return strings.EqualFold(scheme, "Bearer") && subtle.ConstantTimeCompare([]byte(got), token) == 1
}

// unauthorized answers 401 the way docs/api/local.openapi.yaml says.
func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="pairtime"`)
	http.Error(w, "unauthorized: open the dashboard with `pairtime dashboard`, or send the bearer token from port.json", http.StatusUnauthorized)
}

// SignIn serves GET /signin?code=<code>. A code Redeem takes sets the
// session cookie and sends the browser on to the dashboard at /. Anything
// else goes to /?signin=expired, where the dashboard says to run `pairtime
// dashboard` again. Both answers are 303s that nothing may cache. Only GET
// redeems a code, so a HEAD, such as a link preview's, can't use it up.
func SignIn(sessions *Sessions) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		id, ok := sessions.Redeem(r.URL.Query().Get("code"))
		if !ok {
			http.Redirect(w, r, "/?signin=expired", http.StatusSeeOther)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     CookieName,
			Value:    id,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		})
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
}
