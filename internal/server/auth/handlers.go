// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/server/accounts"
)

// loginState is what the login cookie carries from the login page to the
// callback. The cookie also carries an HMAC of it under the service's login
// key (sealLogin), so a browser can't write a login state of its own: the
// callback believes Fresh and Started, and the cookie's __Host- prefix
// keeps other origins from setting it.
type loginState struct {
	State string `json:"s"`
	// Secret is a random value only this cookie holds. The nonce sent to
	// HCA is its SHA-256 (nonceFor), so the authorization request never
	// carries it (OIDC Core 15.5.2).
	Secret   string `json:"v"`
	ReturnTo string `json:"r"`
	Fresh    bool   `json:"f,omitempty"`
	Started  int64  `json:"t"`
}

// login starts a sign-in: GET /auth/hca/login, with optional return_to (a
// path on this server) and fresh=1 (DESIGN 7.5 step 2).
func (s *Service) login(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	if !s.Enabled() {
		s.fail(w, r, notConfigured())
		return
	}
	q := r.URL.Query()
	st := loginState{
		State:    randomString(),
		Secret:   randomString(),
		ReturnTo: localPath(q.Get("return_to")),
		Fresh:    q.Get("fresh") == "1",
		Started:  s.now().Unix(),
	}
	p, err := s.oidc.provider(r.Context())
	if err != nil {
		s.fail(w, r, unreachable(err))
		return
	}
	target, err := s.authorizeURL(p, st)
	if err != nil {
		s.fail(w, r, unreachable(err))
		return
	}
	value, err := s.sealLogin(st)
	if err != nil {
		s.fail(w, r, internal(err))
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     loginCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   int(loginTTL / time.Second),
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, target, http.StatusFound)
}

// authorizeURL is HCA's authorization endpoint with the request's
// parameters (OIDC Core 3.1.2.1). A query the endpoint already has is kept
// (RFC 6749 3.1).
func (s *Service) authorizeURL(p *provider, st loginState) (string, error) {
	u, err := url.Parse(p.AuthorizationEndpoint)
	if err != nil {
		return "", fmt.Errorf("the authorization endpoint %q doesn't parse", p.AuthorizationEndpoint)
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", s.clientID)
	q.Set("redirect_uri", s.redirectURI)
	q.Set("scope", strings.Join(Scopes, " "))
	q.Set("state", st.State)
	q.Set("nonce", nonceFor(st.Secret))
	if st.Fresh {
		// prompt=login asks HCA to authenticate the person again before it
		// answers (OIDC Core 3.1.2.1). It goes alone, as HCA's OIDC guide
		// shows it (SP 3.1): the guide offers prompt=login and max_age as
		// two separate ways to ask, so pairtime never adds max_age. The
		// callback then needs the ID token's auth_time to show the person
		// authenticated after this sign-in started, the time the signed
		// login cookie holds.
		q.Set("prompt", "login")
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// callback finishes a sign-in: GET /auth/hca/callback.
func (s *Service) callback(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	// The URL holds the authorization code.
	w.Header().Set("Referrer-Policy", "no-referrer")
	if !s.Enabled() {
		s.fail(w, r, notConfigured())
		return
	}
	// The login cookie is good for one callback, whatever happens next.
	st, ok := s.readLoginState(r)
	http.SetCookie(w, expiredCookie(loginCookie))
	q := r.URL.Query()
	if !ok {
		s.fail(w, r, &Error{Status: http.StatusBadRequest, Code: CodeStateMissing,
			Message: "This sign-in took too long, or it started in another browser. Start again."})
		return
	}
	// The state is checked before anything else is believed, and before
	// the code goes anywhere (RFC 6749 10.12).
	if got := q.Get("state"); got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(st.State)) != 1 {
		s.fail(w, r, &Error{Status: http.StatusBadRequest, Code: CodeStateMismatch,
			Message: "This answer from Hack Club Auth doesn't belong to the sign-in this browser started. Start again."})
		return
	}
	if iss := q.Get("iss"); iss != "" && iss != s.issuer {
		// RFC 9207: an answer from another provider is refused.
		s.fail(w, r, &Error{Status: http.StatusBadRequest, Code: CodeWrongIssuerParam,
			Message: "This answer came from a sign-in provider other than Hack Club Auth. Start again.",
			Err:     fmt.Errorf("the iss parameter is %q, want %q", iss, s.issuer)})
		return
	}
	if e := q.Get("error"); e != "" {
		if e == "access_denied" {
			s.fail(w, r, &Error{Status: http.StatusForbidden, Code: CodeAccessDenied,
				Message: "Sign-in was cancelled at Hack Club Auth, so you aren't signed in."})
			return
		}
		s.fail(w, r, &Error{Status: http.StatusBadGateway, Code: CodeHCAError,
			Message: "Hack Club Auth couldn't finish the sign-in. Try again in a minute.",
			Err:     fmt.Errorf("HCA answered with error %q", errorCode(e))})
		return
	}
	code := q.Get("code")
	if code == "" {
		s.fail(w, r, &Error{Status: http.StatusBadRequest, Code: CodeNoCode,
			Message: "Hack Club Auth's answer had no authorization code. Start again."})
		return
	}

	select {
	case s.exchanges <- struct{}{}:
		defer func() { <-s.exchanges }()
	case <-r.Context().Done():
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), exchangeTimeout)
	defer cancel()
	p, err := s.oidc.provider(ctx)
	if err != nil {
		s.fail(w, r, unreachable(err))
		return
	}
	raw, err := s.oidc.exchange(ctx, p, code)
	if err != nil {
		s.fail(w, r, &Error{Status: http.StatusBadGateway, Code: CodeTokenExchange,
			Message: "Hack Club Auth didn't accept pairtime's request to finish the sign-in. Start again.",
			Err:     err})
		return
	}
	keys := func(kid string) ([]*rsa.PublicKey, error) {
		return s.oidc.signingKeys(ctx, p.JWKSURI, kid)
	}
	var freshSince time.Time
	if st.Fresh {
		freshSince = time.Unix(st.Started, 0)
	}
	c, verr := verifyIDToken(raw, keys, expectation{
		issuer:     s.issuer,
		clientID:   s.clientID,
		nonce:      nonceFor(st.Secret),
		now:        s.now(),
		freshSince: freshSince,
	})
	if verr != nil {
		s.fail(w, r, verr)
		return
	}
	id, authTime, perr := identity(c)
	if perr != nil {
		s.fail(w, r, perr)
		return
	}
	// A fresh login's auth_time passed verifyIDToken's freshSince check,
	// so HCA authenticated the person after this sign-in started.
	sess, created, err := s.accounts.SignIn(ctx, id, accounts.Login{AuthTime: authTime, Fresh: st.Fresh})
	switch {
	case errors.Is(err, accounts.ErrIneligible):
		s.fail(w, r, &Error{Status: http.StatusForbidden, Code: CodeIneligible,
			Message: "Hack Club Auth marks this identity as not eligible, so pairtime can't sign you in. Hack Club's services are for teenagers; if you think this is a mistake, ask Hack Club Auth's support."})
		return
	case errors.Is(err, accounts.ErrSignupsClosed):
		s.fail(w, r, &Error{Status: http.StatusForbidden, Code: CodeSignupsClosed,
			Message: "This pairtime server isn't taking new accounts yet. Your time keeps being tracked on your own computer, and nothing is lost."})
		return
	case err != nil:
		s.fail(w, r, internal(err))
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    sess.Token,
		Path:     "/",
		MaxAge:   max(1, int(sess.ExpiresAt.Sub(s.now())/time.Second)),
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	s.log.Info("signed in with HCA", "user", sess.User.ID, "new_account", created, "fresh_login", st.Fresh)
	http.Redirect(w, r, st.ReturnTo, http.StatusSeeOther)
}

// loginMACLabel starts the input of the login cookie's HMAC.
const loginMACLabel = "pairtime hca login v1\x00"

// sealLogin encodes st as the login cookie's value: its JSON in base64url,
// a dot, and the base64url HMAC-SHA256 of that encoding under the login
// key.
func (s *Service) sealLogin(st loginState) (string, error) {
	b, err := json.Marshal(st)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(b)
	return body + "." + base64.RawURLEncoding.EncodeToString(s.loginMAC(body)), nil
}

// loginMAC is the HMAC of a login cookie's encoded state.
func (s *Service) loginMAC(body string) []byte {
	m := hmac.New(sha256.New, s.loginKey)
	m.Write([]byte(loginMACLabel))
	m.Write([]byte(body))
	return m.Sum(nil)
}

// readLoginState reads the login cookie, if it is there, carries this
// service's HMAC, is well formed and is younger than loginTTL.
func (s *Service) readLoginState(r *http.Request) (loginState, bool) {
	c, err := r.Cookie(loginCookie)
	if err != nil || len(c.Value) > 2048 {
		return loginState{}, false
	}
	body, mac64, ok := strings.Cut(c.Value, ".")
	if !ok {
		return loginState{}, false
	}
	mac, err := base64.RawURLEncoding.DecodeString(mac64)
	if err != nil || !hmac.Equal(mac, s.loginMAC(body)) {
		return loginState{}, false
	}
	var st loginState
	b, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil || json.Unmarshal(b, &st) != nil {
		return loginState{}, false
	}
	age := s.now().Sub(time.Unix(st.Started, 0))
	if st.State == "" || st.Secret == "" || age < -ClockSkew || age > loginTTL {
		return loginState{}, false
	}
	st.ReturnTo = localPath(st.ReturnTo)
	return st, true
}

// nonceFor is the nonce of a sign-in whose login cookie holds secret: the
// base64url SHA-256 of it (OIDC Core 15.5.2).
func nonceFor(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// slackID is the shape of a Slack user id. HCA itself requires U and
// uppercase letters and digits.
var slackID = regexp.MustCompile(`^[UW][A-Z0-9]{2,31}$`)

// identity turns checked claims into what accounts stores, and refuses an
// identity HCA said too little about (DESIGN 8.2).
func identity(c *claims) (accounts.Identity, time.Time, *Error) {
	var id accounts.Identity
	if err := accounts.CheckSub(c.Sub); err != nil {
		return id, time.Time{}, &Error{Status: http.StatusUnauthorized, Code: CodeBadSubject,
			Message: "Hack Club Auth's answer didn't name a usable identity, so you weren't signed in.",
			Err:     err}
	}
	if c.VerificationStatus == nil {
		return id, time.Time{}, &Error{Status: http.StatusForbidden, Code: CodeNoVerificationStatus,
			Message: "Hack Club Auth didn't say whether this identity is verified, so pairtime can't sign you in. The server's HCA app may not have the verification_status scope.",
			Err:     errors.New("the ID token has no verification_status claim")}
	}
	status, err := accounts.ParseVerificationStatus(*c.VerificationStatus)
	if err != nil {
		return id, time.Time{}, &Error{Status: http.StatusForbidden, Code: CodeUnknownVerificationStatus,
			Message: "Hack Club Auth sent a verification status pairtime doesn't know yet, so it can't sign you in.",
			Err:     err}
	}
	id = accounts.Identity{Sub: c.Sub, Status: status, YSWSEligible: c.YSWSEligible}
	// Only an address HCA says it verified is kept, since programs match
	// people by email (SP 3.3).
	if c.Email != nil && c.EmailVerified != nil && *c.EmailVerified && plausibleEmail(*c.Email) {
		id.Email = strings.TrimSpace(*c.Email)
	}
	if c.SlackID != nil && slackID.MatchString(*c.SlackID) {
		id.SlackID = *c.SlackID
	}
	var authTime time.Time
	if c.AuthTime != nil {
		authTime = c.AuthTime.t
	}
	return id, authTime, nil
}

// plausibleEmail accepts a short address with something on both sides of
// an @ and no spaces or control characters.
func plausibleEmail(e string) bool {
	e = strings.TrimSpace(e)
	at := strings.LastIndexByte(e, '@')
	return len(e) <= 320 && at > 0 && at < len(e)-1 &&
		!strings.ContainsFunc(e, func(r rune) bool { return r <= ' ' || r == 0x7f })
}

// logout ends the session: POST /auth/logout.
func (s *Service) logout(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	if !s.sameOrigin(r) {
		s.fail(w, r, &Error{Status: http.StatusForbidden, Code: CodeCrossOrigin, Title: "Signing out didn't work",
			Message: "Signing out has to start from a pairtime page."})
		return
	}
	// The browser forgets the session first, so it is signed out even
	// when the server can't record that, as on a shared computer.
	http.SetCookie(w, expiredCookie(SessionCookie))
	if c, err := r.Cookie(SessionCookie); err == nil && s.accounts != nil {
		if err := s.accounts.Revoke(r.Context(), c.Value); err != nil {
			s.fail(w, r, &Error{Status: http.StatusInternalServerError, Code: CodeLogoutFailed, Title: "Signing out didn't finish",
				Message: "This browser is signed out, but an error on this pairtime server kept it from ending the session on the server too. Without this browser's cookie, which is deleted now, that session can't be used, and it expires on its own.",
				Err:     err})
			return
		}
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// sameOrigin reports whether a state-changing request comes from
// pairtimed's own pages. Browsers send Origin with every POST, and
// Sec-Fetch-Site too; a request with neither isn't from a browser, so it
// carries no cookie it didn't mean to. The session cookie's SameSite=Lax
// keeps it off cross-site POSTs as well.
func (s *Service) sameOrigin(r *http.Request) bool {
	if o := r.Header.Get("Origin"); o != "" {
		return o == s.baseURL
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
		return true
	}
	return false
}

// me describes the signed-in account: GET /api/v1/users/me.
func (s *Service) me(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	sess, err := s.Session(r)
	if err != nil {
		if !errors.Is(err, accounts.ErrNoSession) {
			s.log.Error("reading a session", "err", err)
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not_signed_in"})
		return
	}
	email, err := s.accounts.Email(&sess.User)
	if err != nil {
		s.log.Warn("an account's email doesn't decrypt", "user", sess.User.ID, "err", err)
		email = ""
	}
	roles := sess.Roles
	if roles == nil {
		roles = []string{}
	}
	body := Me{
		ID:                   sess.User.ID,
		HCASub:               sess.User.HCASub,
		SlackID:              sess.User.SlackID,
		Email:                email,
		VerificationStatus:   string(sess.User.Status),
		CanSubmit:            sess.User.CanSubmit(),
		EligibilityToConfirm: sess.User.EligibilityToConfirm(),
		Roles:                roles,
		Session: MeSession{
			CreatedAt: sess.CreatedAt.UTC(),
			ExpiresAt: sess.ExpiresAt.UTC(),
		},
	}
	if !sess.AuthTime.IsZero() {
		t := sess.AuthTime.UTC()
		body.Session.AuthTime = &t
	}
	if !sess.FreshLoginAt.IsZero() {
		t := sess.FreshLoginAt.UTC()
		body.Session.FreshLoginAt = &t
	}
	writeJSON(w, http.StatusOK, body)
}

// Me is the body of GET /api/v1/users/me.
type Me struct {
	ID                 string `json:"id"`
	HCASub             string `json:"hca_sub"`
	SlackID            string `json:"slack_id,omitempty"`
	Email              string `json:"email,omitempty"`
	VerificationStatus string `json:"verification_status"`
	// CanSubmit is true only for a verified identity (DESIGN 8.2).
	CanSubmit bool `json:"can_submit"`
	// EligibilityToConfirm marks a verified identity HCA didn't call
	// eligible for YSWS programs: its submissions carry the note
	// (DESIGN 8.2).
	EligibilityToConfirm bool      `json:"eligibility_to_confirm"`
	Roles                []string  `json:"roles"`
	Session              MeSession `json:"session"`
}

// MeSession describes the session in Me.
type MeSession struct {
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	// AuthTime is when HCA last authenticated the person, if it said.
	AuthTime *time.Time `json:"auth_time,omitempty"`
	// FreshLoginAt is set only for a session from a fresh login
	// (?fresh=1): when HCA authenticated the person again for it.
	FreshLoginAt *time.Time `json:"fresh_login_at,omitempty"`
}

// localPath returns p when it is a path on this server, and "/" otherwise,
// so a sign-in never ends on another site.
func localPath(p string) string {
	if p == "" || len(p) > 512 || !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") ||
		strings.ContainsFunc(p, func(r rune) bool { return r < ' ' || r == 0x7f || r == '\\' }) {
		return "/"
	}
	u, err := url.Parse(p)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return "/"
	}
	return p
}

// randomString returns 32 random bytes as base64url.
func randomString() string {
	return base64.RawURLEncoding.EncodeToString(randomBytes(32))
}

// randomBytes returns n random bytes.
func randomBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}

// expiredCookie deletes the cookie name.
func expiredCookie(name string) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func notConfigured() *Error {
	return &Error{Status: http.StatusServiceUnavailable, Code: CodeNotConfigured,
		Message: "Signing in with Hack Club Auth isn't set up on this pairtime server yet."}
}

func unreachable(err error) *Error {
	return &Error{Status: http.StatusServiceUnavailable, Code: CodeHCAUnreachable,
		Message: "Hack Club Auth can't be reached right now. Try again in a minute.",
		Err:     err}
}

func internal(err error) *Error {
	return &Error{Status: http.StatusInternalServerError, Code: CodeInternal,
		Message: "Something went wrong on this pairtime server, and you weren't signed in. Try again in a minute.",
		Err:     err}
}

// page is the error page.
var page = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · pairtime</title>
</head>
<body>
<h1>{{.Title}}</h1>
<p>{{.Message}}</p>
<p>Error code: <code>{{.Code}}</code></p>
<p><a href="{{.Retry}}">Sign in with Hack Club Auth</a></p>
</body>
</html>
`))

// fail logs a refused sign-in or a failed sign-out and shows its error
// page.
func (s *Service) fail(w http.ResponseWriter, r *http.Request, e *Error) {
	level := slog.LevelWarn
	if e.Status >= 500 {
		level = slog.LevelError
	}
	attrs := []any{"code", e.Code, "status", e.Status, "path", r.URL.Path}
	if e.Err != nil {
		attrs = append(attrs, "err", e.Err.Error())
	}
	msg := "HCA sign-in refused"
	if r.URL.Path == LogoutPath {
		msg = "sign-out failed"
	}
	s.log.Log(r.Context(), level, msg, attrs...)
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	title := e.Title
	if title == "" {
		title = "Sign-in didn't work"
	}
	w.WriteHeader(e.Status)
	page.Execute(w, struct{ Title, Message, Code, Retry string }{title, e.Message, e.Code, LoginPath})
}
