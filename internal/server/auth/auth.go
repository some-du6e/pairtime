// SPDX-License-Identifier: AGPL-3.0-or-later

// Package auth signs people in to pairtimed with Hack Club Auth (HCA), the
// OpenID Connect provider at auth.hackclub.com (DESIGN 3.10; SP 3.1).
//
// HCA has no PKCE and its apps are confidential, so pairtimed is the client
// and holds the secret; the desktop client never sees an HCA token (SP 3.5).
// The flow is OIDC Core's authorization code flow:
//
//   - GET /auth/hca/login sets a short-lived cookie holding a random state
//     and nonce, and redirects to HCA's authorization endpoint asking for
//     the scopes "openid email slack_id verification_status". They are
//     named explicitly, because HCA's discovery document lists only "openid
//     profile phone birthdate" (SP 3.1). With fresh=1 it also sends
//     prompt=login, and no max_age, for the fresh login a submission freeze
//     asks for (DESIGN 7.5 step 2).
//   - GET /auth/hca/callback checks the state against the cookie before it
//     redeems the code, redeems it at the token endpoint with the client
//     secret, and checks the ID token by OIDC Core 3.1.3.7: an RS256
//     signature by a key from HCA's JWKS, the issuer, the audience, the
//     expiry and issue times, and the nonce. A fresh login must also carry
//     a recent auth_time. HCA's access and refresh tokens are dropped
//     unused, since everything pairtime needs is in the ID token.
//   - The claims go to package accounts, which applies DESIGN 8.2: an
//     ineligible identity is refused, and sign-ups may be closed (DESIGN
//     8.6). A sign-in it accepts gets a session cookie that is __Host-
//     prefixed, Secure, HttpOnly and SameSite=Lax, Lax so that it is set on
//     the redirect back from HCA.
//   - POST /auth/logout ends the session, and GET /api/v1/users/me shows
//     the signed-in account.
//
// Errors are pages with a stable code, such as wrong_nonce, which is also
// logged. Nothing logs a token, a code, a state, a nonce or a secret.
package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/server/accounts"
)

// The paths this package serves.
const (
	LoginPath    = "/auth/hca/login"
	CallbackPath = "/auth/hca/callback"
	LogoutPath   = "/auth/logout"
	MePath       = "/api/v1/users/me"
)

// Scopes are the scopes pairtimed asks HCA for: all four are community
// scopes (SP 3.1). profile isn't asked for, so HCA never sends a name.
var Scopes = []string{"openid", "email", "slack_id", "verification_status"}

// SessionCookie is the name of the session cookie. The __Host- prefix makes
// browsers refuse it unless it is Secure, has Path=/ and no Domain.
const SessionCookie = "__Host-pairtime_session"

// loginCookie holds the state and nonce of a sign-in in progress.
const loginCookie = "__Host-pairtime_hca_login"

// loginTTL is how long a sign-in may take between the login page and the
// callback.
const loginTTL = 10 * time.Minute

// maxConcurrentExchanges bounds the callbacks redeeming a code at HCA at
// once.
const maxConcurrentExchanges = 16

// exchangeTimeout bounds a callback's requests to HCA.
const exchangeTimeout = 30 * time.Second

// The codes of refused sign-ins, shown on the error page and logged.
const (
	CodeNotConfigured             = "not_configured"
	CodeHCAUnreachable            = "hca_unreachable"
	CodeStateMissing              = "state_missing"
	CodeStateMismatch             = "state_mismatch"
	CodeWrongIssuerParam          = "wrong_iss_param"
	CodeAccessDenied              = "access_denied"
	CodeHCAError                  = "hca_error"
	CodeNoCode                    = "no_code"
	CodeTokenExchange             = "token_exchange_failed"
	CodeMalformedIDToken          = "malformed_id_token"
	CodeUnsupportedAlg            = "unsupported_alg"
	CodeUnknownKey                = "unknown_key"
	CodeBadSignature              = "bad_signature"
	CodeWrongIssuer               = "wrong_issuer"
	CodeWrongAudience             = "wrong_audience"
	CodeExpired                   = "expired"
	CodeNotYetValid               = "not_yet_valid"
	CodeWrongNonce                = "wrong_nonce"
	CodeNoAuthTime                = "no_auth_time"
	CodeLoginNotFresh             = "login_not_fresh"
	CodeBadSubject                = "bad_subject"
	CodeNoVerificationStatus      = "no_verification_status"
	CodeUnknownVerificationStatus = "unknown_verification_status"
	CodeIneligible                = "ineligible"
	CodeSignupsClosed             = "signups_closed"
	CodeCrossOrigin               = "cross_origin"
	CodeInternal                  = "internal"
)

// Error is a refused sign-in.
type Error struct {
	// Status is the HTTP status of the error page.
	Status int
	// Code is the stable reason, one of the Code constants.
	Code string
	// Message is the sentence the page shows.
	Message string
	// Err is the detail for the log. It is never shown and never holds a
	// token, a code or a secret.
	Err error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Code + ": " + e.Err.Error()
	}
	return e.Code
}

func (e *Error) Unwrap() error { return e.Err }

// Config configures a Service.
type Config struct {
	// BaseURL is pairtimed's base_url. The redirect URI registered with HCA
	// is BaseURL + CallbackPath.
	BaseURL string
	// Issuer is HCA's issuer, hca.issuer in pairtime.toml.
	Issuer string
	// ClientID and ClientSecret identify pairtimed's HCA app. An empty
	// ClientID turns sign-in off, and the login and callback pages say so.
	ClientID, ClientSecret string
	// Accounts keeps accounts and sessions. It may be nil only when
	// ClientID is empty.
	Accounts *accounts.Store
	// HTTPClient makes the requests to HCA. Nil means a client with a 10 s
	// timeout that never follows a redirect.
	HTTPClient *http.Client
	// Logger gets one line per sign-in and per refusal. Nil discards them.
	Logger *slog.Logger
	// Now returns the current time. Nil means time.Now.
	Now func() time.Time
}

// Service serves sign-in.
type Service struct {
	baseURL, redirectURI string
	issuer, clientID     string
	accounts             *accounts.Store
	oidc                 *oidcClient // nil while sign-in is off
	log                  *slog.Logger
	now                  func() time.Time
	exchanges            chan struct{}
}

// New returns a Service for c.
func New(c Config) (*Service, error) {
	if c.Logger == nil {
		c.Logger = slog.New(slog.DiscardHandler)
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	s := &Service{
		baseURL:   strings.TrimSuffix(c.BaseURL, "/"),
		issuer:    c.Issuer,
		clientID:  c.ClientID,
		accounts:  c.Accounts,
		log:       c.Logger,
		now:       c.Now,
		exchanges: make(chan struct{}, maxConcurrentExchanges),
	}
	s.redirectURI = s.baseURL + CallbackPath
	if c.ClientID == "" {
		return s, nil
	}
	if err := CheckURL(s.baseURL); err != nil {
		return nil, fmt.Errorf("auth: base_url: %w", err)
	}
	if err := CheckURL(c.Issuer); err != nil {
		return nil, fmt.Errorf("auth: hca.issuer: %w", err)
	}
	if c.ClientSecret == "" {
		return nil, errors.New("auth: hca.client_id is set, so the secret PAIRTIME_HCA_CLIENT_SECRET must be too")
	}
	if c.Accounts == nil {
		return nil, errors.New("auth: sign-in needs an account store")
	}
	hc := c.HTTPClient
	if hc == nil {
		hc = &http.Client{
			Timeout: 10 * time.Second,
			// A redirect from the token endpoint would turn the POST into
			// a GET to wherever it points.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	s.oidc = &oidcClient{
		issuer:       c.Issuer,
		clientID:     c.ClientID,
		clientSecret: c.ClientSecret,
		redirectURI:  s.redirectURI,
		http:         hc,
		now:          c.Now,
	}
	return s, nil
}

// Enabled reports whether sign-in is on.
func (s *Service) Enabled() bool {
	return s.oidc != nil
}

// RedirectURI is the URI to register with HCA.
func (s *Service) RedirectURI() string {
	return s.redirectURI
}

// Register adds the sign-in endpoints to mux.
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET "+LoginPath, s.login)
	mux.HandleFunc("GET "+CallbackPath, s.callback)
	mux.HandleFunc("POST "+LogoutPath, s.logout)
	mux.HandleFunc("GET "+MePath, s.me)
}

// Session returns the live session of the request's cookie, or
// accounts.ErrNoSession.
func (s *Service) Session(r *http.Request) (*accounts.Session, error) {
	if s.accounts == nil {
		return nil, accounts.ErrNoSession
	}
	c, err := r.Cookie(SessionCookie)
	if err != nil {
		return nil, accounts.ErrNoSession
	}
	return s.accounts.Session(r.Context(), c.Value)
}

// Maintain deletes old session rows once an hour until ctx ends.
func (s *Service) Maintain(ctx context.Context) {
	if s.accounts == nil {
		return
	}
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := s.accounts.DeleteExpiredSessions(ctx)
			if err != nil {
				if ctx.Err() == nil {
					s.log.Error("deleting old sessions", "err", err)
				}
				continue
			}
			if n > 0 {
				s.log.Info("deleted old sessions", "rows", n)
			}
		}
	}
}

// CheckURL accepts an absolute https URL with a host and no fragment, or
// an http one whose host is localhost or a loopback address, for local runs
// and tests. It is the rule for hca.issuer and for every endpoint HCA's
// discovery document names.
func CheckURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Fragment != "" || u.User != nil {
		return fmt.Errorf("%q isn't an absolute URL with a host and no fragment", raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) {
			return nil
		}
		return fmt.Errorf("%q uses http; only https is allowed, except on localhost", raw)
	default:
		return fmt.Errorf("%q must start with https://", raw)
	}
}
