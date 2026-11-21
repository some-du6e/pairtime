// SPDX-License-Identifier: AGPL-3.0-or-later

package device

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/server/accounts"
	"github.com/some-du6e/pairtime/internal/server/auth"
	"github.com/some-du6e/pairtime/internal/server/config"
	"github.com/some-du6e/pairtime/internal/server/pages"
)

// The paths this package serves.
const (
	CodePath     = "/oauth/device/code"
	TokenPath    = "/oauth/token"
	PagePath     = "/device"
	DecisionPath = "/device/decision"
	DevicesPath  = "/account/devices"
	RemovePath   = "/account/devices/remove"
	MePath       = "/api/v1/devices/me"
)

// ClientID is the OAuth client id of the pairtime app, the only client of
// the device grant.
const ClientID = "pairtime"

// GrantType is RFC 8628's grant type for the token request.
const GrantType = "urn:ietf:params:oauth:grant-type:device_code"

// Scope is what a device token may do (SP 8.3): upload, and read its own
// account's data.
const Scope = "ingest read:self"

// SignIn is what the pages need from Hack Club Auth sign-in, which
// *auth.Service provides.
type SignIn interface {
	// Enabled reports whether sign-in is set up on this server.
	Enabled() bool
	// Session returns the request's live session, or
	// accounts.ErrNoSession.
	Session(r *http.Request) (*accounts.Session, error)
}

// Config configures a Service.
type Config struct {
	Store *Store
	// BaseURL is pairtimed's base_url; verification_uri is BaseURL +
	// PagePath.
	BaseURL string
	SignIn  SignIn
	// Site renders the pages through the server's layout.
	Site *pages.Site
	// Handle is the name the pages show for an account. Nil shows the
	// Slack ID, or the HCA subject without one.
	Handle func(*accounts.User) string
	// SignupsOpen reports whether sign-ups are open to everyone, so that
	// the code response can say when only an account that exists already
	// can approve a code (pages.Signups.Open). Nil means they are open.
	SignupsOpen func() bool
	// Logger gets a line per link, refusal and removal. Nil discards them.
	Logger *slog.Logger
}

// Service serves the device grant, the device pages and the device API.
type Service struct {
	store   *Store
	baseURL string
	// origin is base_url as a browser's Origin header writes it.
	origin string
	signIn SignIn
	site   *pages.Site
	handle func(*accounts.User) string
	// signups is Config.SignupsOpen, or nil.
	signups func() bool
	log     *slog.Logger
	// csrfKey keys the pages' CSRF tokens. It lives only in memory, so a
	// form from before a restart is refused and shown again.
	csrfKey []byte
	tries   *limiter
}

// NewService returns a Service for c.
func NewService(c Config) (*Service, error) {
	if c.Store == nil || c.SignIn == nil || c.Site == nil {
		return nil, errors.New("device: NewService needs a store, sign-in and the site")
	}
	if c.Logger == nil {
		c.Logger = slog.New(slog.DiscardHandler)
	}
	if c.Handle == nil {
		c.Handle = func(u *accounts.User) string {
			if u.SlackID != "" {
				return u.SlackID
			}
			return u.HCASub
		}
	}
	base := strings.TrimSuffix(c.BaseURL, "/")
	s := &Service{
		store:   c.Store,
		baseURL: base,
		origin:  config.CanonicalOrigin(base),
		signIn:  c.SignIn,
		site:    c.Site,
		handle:  c.Handle,
		signups: c.SignupsOpen,
		log:     c.Logger,
		csrfKey: make([]byte, 32),
		tries:   newLimiter(maxWrongCodes, wrongCodeWindow, c.Store.now),
	}
	rand.Read(s.csrfKey)
	return s, nil
}

// Register adds the device grant, the pages and the API to mux.
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST "+CodePath, s.deviceCode)
	mux.HandleFunc("POST "+TokenPath, s.token)
	mux.HandleFunc("GET "+PagePath, s.page)
	mux.HandleFunc("POST "+PagePath, s.enter)
	mux.HandleFunc("POST "+DecisionPath, s.decide)
	mux.HandleFunc("GET "+DevicesPath, s.devices)
	mux.HandleFunc("POST "+RemovePath, s.remove)
	mux.HandleFunc("GET "+MePath, s.me)
	mux.HandleFunc("DELETE "+MePath, s.removeMe)
}

// signupsOpen reports whether sign-ups are open to everyone.
func (s *Service) signupsOpen() bool {
	return s.signups == nil || s.signups()
}

// Maintain deletes the rows of old device codes once an hour until ctx
// ends.
func (s *Service) Maintain(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := s.store.DeleteExpiredCodes(ctx)
			if err != nil {
				if ctx.Err() == nil {
					s.log.Error("deleting old device codes", "err", err)
				}
				continue
			}
			if n > 0 {
				s.log.Info("deleted old device codes", "rows", n)
			}
		}
	}
}

// maxFormBytes bounds the body of a form or JSON request.
const maxFormBytes = 4 << 10

// oauthError is an RFC 6749 5.2 error body.
type oauthError struct {
	Error       string `json:"error"`
	Description string `json:"error_description,omitempty"`
}

// writeJSON writes v with status, never to be stored (RFC 6749 5.1).
func writeJSON(w http.ResponseWriter, status int, v any) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Pragma", "no-cache")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeOAuthError(w http.ResponseWriter, status int, code, description string) {
	writeJSON(w, status, oauthError{Error: code, Description: description})
}

// form reads an application/x-www-form-urlencoded body, at most
// maxFormBytes. Every parameter may appear at most once, and a blank one
// counts as missing (RFC 8628 3.1); parameters in the URL are ignored, so
// a device code never has to travel in one. It returns a description of
// the problem when there is one.
func form(w http.ResponseWriter, r *http.Request) (url.Values, string) {
	mt, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";")
	if !strings.EqualFold(strings.TrimSpace(mt), "application/x-www-form-urlencoded") {
		return nil, "The body must be a form, application/x-www-form-urlencoded."
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		return nil, "The form doesn't parse, or is too long."
	}
	for _, vs := range r.PostForm {
		if len(vs) > 1 {
			// The name isn't echoed: error_description may hold only
			// printable ASCII without '"' and '\' (RFC 6749 5.2).
			return nil, "A parameter appears more than once."
		}
	}
	return r.PostForm, ""
}

// sameOrigin reports whether a state-changing request comes from this
// server's own pages, by the rule package auth uses for sign-out: an
// Origin header must be this server's, and without one, Sec-Fetch-Site
// must not say another site sent it.
func (s *Service) sameOrigin(r *http.Request) bool {
	if o := r.Header.Get("Origin"); o != "" {
		return s.origin != "" && config.CanonicalOrigin(o) == s.origin
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
		return true
	}
	return false
}

// csrfLabel starts the HMAC input of a CSRF token.
const csrfLabel = "pairtime device csrf v1\x00"

// csrfToken is the CSRF token of the request's session: an HMAC of its
// session cookie, so a page from one session can't post for another, and
// a cross-site page can't know it.
func (s *Service) csrfToken(r *http.Request) string {
	c, err := r.Cookie(auth.SessionCookie)
	if err != nil {
		return ""
	}
	m := hmac.New(sha256.New, s.csrfKey)
	m.Write([]byte(csrfLabel))
	m.Write([]byte(c.Value))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// checkCSRF reports whether a POST from the pages carries its session's
// CSRF token and comes from this server's origin.
func (s *Service) checkCSRF(r *http.Request, f url.Values) bool {
	want := s.csrfToken(r)
	got := f.Get("csrf")
	return want != "" && got != "" && hmac.Equal([]byte(got), []byte(want)) && s.sameOrigin(r)
}

// bearer returns the token of an Authorization: Bearer header, or "".
func bearer(r *http.Request) string {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

// The WWW-Authenticate challenges of a 401 (RFC 6750 3.1): one without an
// error code for a request that sent no token, and one with
// invalid_token for a token that isn't valid.
const (
	challengeNoToken  = `Bearer realm="pairtime"`
	challengeBadToken = `Bearer realm="pairtime", error="invalid_token"`
)

// authenticate finds the device of the request's bearer token, or answers
// 401 itself and reports false (RFC 6750 3.1). With ineligible, it also
// returns a device that Authenticate refused only because Hack Club Auth
// has marked its account ineligible, for a request that removes it.
func (s *Service) authenticate(w http.ResponseWriter, r *http.Request, ineligible bool) (Device, bool) {
	tok := bearer(r)
	if tok == "" {
		w.Header().Set("WWW-Authenticate", challengeNoToken)
		writeOAuthError(w, http.StatusUnauthorized, "unauthorized", "This request needs the device token pairtime login keeps.")
		return Device{}, false
	}
	d, err := s.store.Authenticate(r.Context(), tok)
	switch {
	case err == nil:
		return d, true
	case ineligible && errors.Is(err, ErrRevoked) && d.ID != "" && d.RemovedAt.IsZero():
		return d, true
	case errors.Is(err, ErrBadToken), errors.Is(err, ErrUnknownToken), errors.Is(err, ErrRevoked):
		w.Header().Set("WWW-Authenticate", challengeBadToken)
		writeOAuthError(w, http.StatusUnauthorized, "invalid_token", "This device token isn't valid, or its device was removed. Run pairtime login again.")
	default:
		s.log.Error("authenticating a device", "err", err)
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Something went wrong on this pairtime server. Try again in a minute.")
	}
	return Device{}, false
}
