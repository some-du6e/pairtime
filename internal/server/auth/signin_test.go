// SPDX-License-Identifier: AGPL-3.0-or-later

package auth_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/auth"
	"github.com/some-du6e/pairtime/internal/server/auth/hcafake"
	"github.com/some-du6e/pairtime/internal/server/config"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
)

// withFake returns a harness signing in through a fresh fake HCA, where
// mayCreate decides who may sign up.
func withFake(t *testing.T, engine store.Engine, mayCreate func(sub string) bool) (*harness, *hcafake.Server) {
	t.Helper()
	hca := hcafake.New(t)
	h := newHarness(t, harnessConfig{
		engine:       engine,
		issuer:       hca.URL,
		clientID:     hcafake.ClientID,
		clientSecret: hcafake.ClientSecret,
		mayCreate:    mayCreate,
		register:     hca.AddRedirectURI,
	})
	return h, hca
}

func boolPtr(b bool) *bool { return &b }

// resign returns a hook that edits the claims and signs the token as HCA
// would.
func resign(edit func(c map[string]any)) func(*hcafake.Token) string {
	return func(tok *hcafake.Token) string {
		edit(tok.Claims)
		return tok.Sign()
	}
}

func TestSignIn(t *testing.T) {
	for _, engine := range storetest.Engines() {
		t.Run(string(engine), func(t *testing.T) { testSignIn(t, engine) })
	}
}

func testSignIn(t *testing.T, engine store.Engine) {
	now := func() int64 { return time.Now().Unix() }
	tests := []struct {
		name     string
		identity *hcafake.Identity // nil: hcafake.Verified()
		// closed closes sign-ups, and mayCreate, when set, decides who may
		// sign up. Otherwise anyone may.
		closed    bool
		mayCreate func(sub string) bool
		// sessionStart, when set, starts the browser's HCA session this
		// long before the sign-in.
		sessionStart time.Duration
		deny         bool
		hook         func(*hcafake.Token) string
		opts         signInOptions
		// wantCode is the refusal's code; "" means the sign-in succeeds
		// and lands on wantLocation, "/" when empty.
		wantStatus   int
		wantCode     string
		wantLocation string
		check        func(t *testing.T, h *harness, hca *hcafake.Server, f *flow, me auth.Me)
	}{
		{
			name: "a verified identity signs in with exactly the four scopes, a state and a nonce",
			check: func(t *testing.T, h *harness, hca *hcafake.Server, f *flow, me auth.Me) {
				q := f.authorize.Query()
				for k, want := range map[string]string{
					"response_type": "code",
					"client_id":     hcafake.ClientID,
					"redirect_uri":  h.srv.URL + auth.CallbackPath,
					"scope":         "openid email slack_id verification_status",
				} {
					if got := q.Get(k); got != want {
						t.Errorf("authorize %s = %q, want %q", k, got, want)
					}
				}
				for _, k := range []string{"state", "nonce"} {
					if len(q.Get(k)) < 43 {
						t.Errorf("authorize %s = %q, want 32 random bytes", k, q.Get(k))
					}
				}
				if q.Get("state") == q.Get("nonce") {
					t.Error("the state and the nonce are the same value")
				}
				for _, k := range []string{"prompt", "max_age", "code_challenge"} {
					if q.Has(k) {
						t.Errorf("an ordinary sign-in sent %s=%q", k, q.Get(k))
					}
				}
				want := auth.Me{
					HCASub:             "ident!fakeVerified1",
					SlackID:            "U0FAKEVERIFIED",
					Email:              "verified@example.org",
					VerificationStatus: "verified",
					CanSubmit:          true,
					Roles:              []string{},
				}
				if me.HCASub != want.HCASub || me.SlackID != want.SlackID || me.Email != want.Email ||
					me.VerificationStatus != want.VerificationStatus || me.CanSubmit != want.CanSubmit ||
					me.EligibilityToConfirm || !slices.Equal(me.Roles, want.Roles) || me.ID == "" {
					t.Errorf("me = %+v, want %+v", me, want)
				}
				if n := hca.TokenCalls(); n != 1 {
					t.Errorf("the code was redeemed %d times, want once", n)
				}
				if me.Session.FreshLoginAt != nil {
					t.Error("an ordinary sign-in's session is marked as a fresh login")
				}
			},
		},
		{
			name: "the nonce is the SHA-256 of a secret only the login cookie holds",
			check: func(t *testing.T, _ *harness, _ *hcafake.Server, f *flow, _ auth.Me) {
				secret := loginSecret(t, f.loginCookie)
				sum := sha256.Sum256([]byte(secret))
				nonce := f.authorize.Query().Get("nonce")
				if nonce != base64.RawURLEncoding.EncodeToString(sum[:]) {
					t.Error("the nonce sent to HCA isn't the base64url SHA-256 of the login cookie's secret")
				}
				if strings.Contains(f.authorize.String(), secret) {
					t.Error("the authorization request carries the login cookie's secret")
				}
			},
		},
		{
			name:         "an ordinary sign-in keeps an auth_time a day old and isn't a fresh login",
			sessionStart: 24 * time.Hour,
			check: func(t *testing.T, _ *harness, _ *hcafake.Server, _ *flow, me auth.Me) {
				day := time.Now().Add(-24 * time.Hour)
				if at := me.Session.AuthTime; at == nil || at.Sub(day) > time.Minute || at.Sub(day) < -time.Minute {
					t.Errorf("session auth_time = %v, want about %v", at, day)
				}
				if me.Session.FreshLoginAt != nil {
					t.Error("an ordinary sign-in's session is marked as a fresh login")
				}
			},
		},
		{
			name:     "a pending identity signs in but can't submit",
			identity: &hcafake.Identity{Sub: "ident!fakePending1", Email: "p@example.org", VerificationStatus: "pending"},
			check: func(t *testing.T, _ *harness, _ *hcafake.Server, _ *flow, me auth.Me) {
				if me.VerificationStatus != "pending" || me.CanSubmit {
					t.Errorf("me = %+v, want pending and can_submit false", me)
				}
			},
		},
		{
			name:     "needs_submission signs in but can't submit",
			identity: &hcafake.Identity{Sub: "ident!fakeNew1", VerificationStatus: "needs_submission"},
			check: func(t *testing.T, _ *harness, _ *hcafake.Server, _ *flow, me auth.Me) {
				if me.VerificationStatus != "needs_submission" || me.CanSubmit || me.Email != "" {
					t.Errorf("me = %+v, want needs_submission, can_submit false and no email", me)
				}
			},
		},
		{
			name:     "verified but not YSWS-eligible may submit with the eligibility-to-confirm note",
			identity: &hcafake.Identity{Sub: "ident!fakeAdult1", VerificationStatus: "verified", YSWSEligible: boolPtr(false)},
			check: func(t *testing.T, _ *harness, _ *hcafake.Server, _ *flow, me auth.Me) {
				if !me.CanSubmit || !me.EligibilityToConfirm {
					t.Errorf("me = %+v, want can_submit and eligibility_to_confirm", me)
				}
			},
		},
		{
			name:       "an ineligible identity is refused and gets no account row",
			identity:   &hcafake.Identity{Sub: "ident!fakeYoung1", Email: "y@example.org", VerificationStatus: "ineligible"},
			wantStatus: http.StatusForbidden,
			wantCode:   auth.CodeIneligible,
			check: func(t *testing.T, h *harness, _ *hcafake.Server, _ *flow, _ auth.Me) {
				if n := h.count(`SELECT count(*) FROM users`); n != 0 {
					t.Errorf("%d account rows, want 0", n)
				}
			},
		},
		{
			name:       "while sign-ups are closed a new identity is refused and gets no account row",
			closed:     true,
			wantStatus: http.StatusForbidden,
			wantCode:   auth.CodeSignupsClosed,
			check: func(t *testing.T, h *harness, _ *hcafake.Server, _ *flow, _ auth.Me) {
				if n := h.count(`SELECT count(*) FROM users`); n != 0 {
					t.Errorf("%d account rows, want 0", n)
				}
			},
		},
		{
			name:      "while sign-ups are closed an identity MayCreate admits gets an account",
			mayCreate: func(sub string) bool { return sub == hcafake.Verified().Sub },
			check: func(t *testing.T, h *harness, _ *hcafake.Server, _ *flow, me auth.Me) {
				if me.HCASub != hcafake.Verified().Sub || h.count(`SELECT count(*) FROM users`) != 1 {
					t.Errorf("me = %+v; want the admitted identity, with one account row", me)
				}
			},
		},
		{
			name:       "while sign-ups are closed an identity MayCreate doesn't admit is refused",
			mayCreate:  func(sub string) bool { return sub == "ident!someoneElse" },
			wantStatus: http.StatusForbidden,
			wantCode:   auth.CodeSignupsClosed,
			check: func(t *testing.T, h *harness, _ *hcafake.Server, _ *flow, _ auth.Me) {
				if n := h.count(`SELECT count(*) FROM users`); n != 0 {
					t.Errorf("%d account rows, want 0", n)
				}
			},
		},
		{
			name: "a callback whose state isn't the cookie's is refused before the code is redeemed",
			opts: signInOptions{edit: func(u *url.URL) {
				q := u.Query()
				q.Set("state", q.Get("state")+"x")
				u.RawQuery = q.Encode()
			}},
			wantStatus: http.StatusBadRequest,
			wantCode:   auth.CodeStateMismatch,
			check: func(t *testing.T, _ *harness, hca *hcafake.Server, _ *flow, _ auth.Me) {
				if n := hca.TokenCalls(); n != 0 {
					t.Errorf("the code was redeemed %d times, want never", n)
				}
			},
		},
		{
			name: "a callback without a state is refused",
			opts: signInOptions{edit: func(u *url.URL) {
				q := u.Query()
				q.Del("state")
				u.RawQuery = q.Encode()
			}},
			wantStatus: http.StatusBadRequest,
			wantCode:   auth.CodeStateMismatch,
		},
		{
			name:       "a callback in a browser that didn't start the sign-in is refused",
			opts:       signInOptions{otherBrowser: true},
			wantStatus: http.StatusBadRequest,
			wantCode:   auth.CodeStateMissing,
			check: func(t *testing.T, _ *harness, hca *hcafake.Server, _ *flow, _ auth.Me) {
				if n := hca.TokenCalls(); n != 0 {
					t.Errorf("the code was redeemed %d times, want never", n)
				}
			},
		},
		{
			name: "an iss parameter naming another provider is refused",
			opts: signInOptions{edit: func(u *url.URL) {
				q := u.Query()
				q.Set("iss", "https://auth.example.org")
				u.RawQuery = q.Encode()
			}},
			wantStatus: http.StatusBadRequest,
			wantCode:   auth.CodeWrongIssuerParam,
		},
		{
			name:       "cancelling at HCA shows the cancelled page",
			deny:       true,
			wantStatus: http.StatusForbidden,
			wantCode:   auth.CodeAccessDenied,
		},
		{
			name:       "an ID token with another nonce is refused",
			hook:       resign(func(c map[string]any) { c["nonce"] = "not-the-nonce-pairtimed-sent" }),
			wantStatus: http.StatusUnauthorized,
			wantCode:   auth.CodeWrongNonce,
		},
		{
			name:       "an ID token without a nonce is refused",
			hook:       resign(func(c map[string]any) { delete(c, "nonce") }),
			wantStatus: http.StatusUnauthorized,
			wantCode:   auth.CodeWrongNonce,
		},
		{
			name:       "an ID token from another issuer is refused",
			hook:       resign(func(c map[string]any) { c["iss"] = "https://auth.example.org" }),
			wantStatus: http.StatusUnauthorized,
			wantCode:   auth.CodeWrongIssuer,
		},
		{
			name:       "an ID token for another audience is refused",
			hook:       resign(func(c map[string]any) { c["aud"] = "another-client" }),
			wantStatus: http.StatusUnauthorized,
			wantCode:   auth.CodeWrongAudience,
		},
		{
			name:       "an ID token that also names an untrusted audience is refused",
			hook:       resign(func(c map[string]any) { c["aud"] = []string{hcafake.ClientID, "another-client"} }),
			wantStatus: http.StatusUnauthorized,
			wantCode:   auth.CodeWrongAudience,
		},
		{
			name:       "an ID token authorized for another party is refused",
			hook:       resign(func(c map[string]any) { c["azp"] = "another-client" }),
			wantStatus: http.StatusUnauthorized,
			wantCode:   auth.CodeWrongAudience,
		},
		{
			name: "an expired ID token is refused",
			hook: resign(func(c map[string]any) {
				c["iat"] = now() - 600
				c["exp"] = now() - 480
			}),
			wantStatus: http.StatusUnauthorized,
			wantCode:   auth.CodeExpired,
		},
		{
			name: "an ID token issued in the future is refused",
			hook: resign(func(c map[string]any) {
				c["iat"] = now() + 600
				c["exp"] = now() + 720
			}),
			wantStatus: http.StatusUnauthorized,
			wantCode:   auth.CodeNotYetValid,
		},
		{
			name:       "an ID token signed by a key HCA never published is refused",
			hook:       func(tok *hcafake.Token) string { return tok.SignWith(hcafake.Key(1)) },
			wantStatus: http.StatusUnauthorized,
			wantCode:   auth.CodeBadSignature,
		},
		{
			name: "an ID token naming a kid HCA never published is refused",
			hook: func(tok *hcafake.Token) string {
				tok.Header["kid"] = "not-a-published-kid"
				return tok.SignWith(hcafake.Key(1))
			},
			wantStatus: http.StatusUnauthorized,
			wantCode:   auth.CodeUnknownKey,
		},
		{
			name: "an ID token whose claims changed after signing is refused",
			hook: func(tok *hcafake.Token) string {
				signed := tok.Sign()
				tok.Claims["verification_status"] = "verified"
				tok.Claims["sub"] = "ident!someoneElse"
				payload, _ := json.Marshal(tok.Claims)
				parts := strings.Split(signed, ".")
				return parts[0] + "." + base64.RawURLEncoding.EncodeToString(payload) + "." + parts[2]
			},
			wantStatus: http.StatusUnauthorized,
			wantCode:   auth.CodeBadSignature,
		},
		{
			name:       "an unsigned ID token is refused",
			hook:       func(tok *hcafake.Token) string { return tok.Unsigned() },
			wantStatus: http.StatusUnauthorized,
			wantCode:   auth.CodeUnsupportedAlg,
		},
		{
			name:       "an HS256 ID token keyed with HCA's public key is refused",
			hook:       func(tok *hcafake.Token) string { return tok.SignHS256(tok.PublicKeyBytes()) },
			wantStatus: http.StatusUnauthorized,
			wantCode:   auth.CodeUnsupportedAlg,
		},
		{
			name:       "an ID token without verification_status is refused",
			hook:       resign(func(c map[string]any) { delete(c, "verification_status") }),
			wantStatus: http.StatusForbidden,
			wantCode:   auth.CodeNoVerificationStatus,
		},
		{
			name:       "an unknown verification_status is refused",
			hook:       resign(func(c map[string]any) { c["verification_status"] = "verified_by_vibes" }),
			wantStatus: http.StatusForbidden,
			wantCode:   auth.CodeUnknownVerificationStatus,
		},
		{
			name:       "a subject with a space is refused",
			hook:       resign(func(c map[string]any) { c["sub"] = "ident! spaced" }),
			wantStatus: http.StatusUnauthorized,
			wantCode:   auth.CodeBadSubject,
		},
		{
			name: "an email HCA didn't verify isn't kept",
			hook: resign(func(c map[string]any) { c["email_verified"] = false }),
			check: func(t *testing.T, h *harness, _ *hcafake.Server, _ *flow, me auth.Me) {
				if me.Email != "" {
					t.Errorf("me.email = %q, want none", me.Email)
				}
				if n := h.count(`SELECT count(*) FROM users WHERE email_ct IS NULL AND email_index IS NULL`); n != 1 {
					t.Errorf("the account row holds an email")
				}
			},
		},
		{
			name: "a Slack id of the wrong shape isn't kept",
			hook: resign(func(c map[string]any) { c["slack_id"] = "not a slack id" }),
			check: func(t *testing.T, _ *harness, _ *hcafake.Server, _ *flow, me auth.Me) {
				if me.SlackID != "" {
					t.Errorf("me.slack_id = %q, want none", me.SlackID)
				}
			},
		},
		{
			name: "a fresh login sends prompt=login without max_age and keeps HCA's auth_time",
			opts: signInOptions{query: "?fresh=1"},
			check: func(t *testing.T, h *harness, _ *hcafake.Server, f *flow, me auth.Me) {
				q := f.authorize.Query()
				if q.Get("prompt") != "login" || q.Has("max_age") {
					t.Errorf("authorize prompt = %q, max_age = %q; want prompt=login and no max_age", q.Get("prompt"), q.Get("max_age"))
				}
				for name, at := range map[string]*time.Time{"auth_time": me.Session.AuthTime, "fresh_login_at": me.Session.FreshLoginAt} {
					if at == nil || time.Since(*at) > time.Minute || time.Since(*at) < -time.Minute {
						t.Errorf("session %s = %v, want about now", name, at)
					}
				}
			},
		},
		{
			name:         "a fresh login after a day-old HCA session is fresh from the new login",
			sessionStart: 24 * time.Hour,
			opts:         signInOptions{query: "?fresh=1"},
			check: func(t *testing.T, _ *harness, _ *hcafake.Server, _ *flow, me auth.Me) {
				if at := me.Session.FreshLoginAt; at == nil || time.Since(*at) > time.Minute {
					t.Errorf("session fresh_login_at = %v, want about now", at)
				}
			},
		},
		{
			name:       "a fresh login HCA authenticated two minutes before it started is refused",
			opts:       signInOptions{query: "?fresh=1"},
			hook:       resign(func(c map[string]any) { c["auth_time"] = now() - 120 }),
			wantStatus: http.StatusUnauthorized,
			wantCode:   auth.CodeLoginNotFresh,
		},
		{
			name:       "a fresh login HCA authenticated an hour ago is refused",
			opts:       signInOptions{query: "?fresh=1"},
			hook:       resign(func(c map[string]any) { c["auth_time"] = now() - 3600 }),
			wantStatus: http.StatusUnauthorized,
			wantCode:   auth.CodeLoginNotFresh,
		},
		{
			name: "the fake HCA refuses prompt=login sent with max_age, a pair HCA's guide never uses",
			opts: signInOptions{query: "?fresh=1", editAuthorize: func(u *url.URL) {
				q := u.Query()
				q.Set("max_age", "0")
				u.RawQuery = q.Encode()
			}},
			wantStatus: http.StatusBadGateway,
			wantCode:   auth.CodeHCAError,
			check: func(t *testing.T, _ *harness, hca *hcafake.Server, f *flow, _ auth.Me) {
				if got := f.callback.Query().Get("error"); got != "invalid_request" {
					t.Errorf("the fake answered error=%q, want invalid_request", got)
				}
				if n := hca.TokenCalls(); n != 0 {
					t.Errorf("a code was redeemed %d times, want never", n)
				}
			},
		},
		{
			name:       "a fresh login without auth_time is refused",
			opts:       signInOptions{query: "?fresh=1"},
			hook:       resign(func(c map[string]any) { delete(c, "auth_time") }),
			wantStatus: http.StatusUnauthorized,
			wantCode:   auth.CodeNoAuthTime,
		},
		{
			name:         "return_to brings the browser back to a page on this server",
			opts:         signInOptions{query: "?return_to=" + url.QueryEscape("/projects/demo?tab=time")},
			wantLocation: "/projects/demo?tab=time",
		},
		{
			name:         "a return_to on another site is replaced by /",
			opts:         signInOptions{query: "?return_to=" + url.QueryEscape("//evil.example/steal")},
			wantLocation: "/",
		},
		{
			name:         "a return_to with a scheme is replaced by /",
			opts:         signInOptions{query: "?return_to=" + url.QueryEscape("https://evil.example/")},
			wantLocation: "/",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mayCreate := anyone
			switch {
			case tt.closed:
				mayCreate = nil
			case tt.mayCreate != nil:
				mayCreate = tt.mayCreate
			}
			h, hca := withFake(t, engine, mayCreate)
			if tt.identity != nil {
				hca.SetIdentity(*tt.identity)
			}
			if tt.sessionStart != 0 {
				hca.SetSessionStart(time.Now().Add(-tt.sessionStart))
			}
			hca.SetHook(tt.hook)
			hca.SetDeny(tt.deny)
			f := h.signIn(tt.opts)
			var me auth.Me
			if tt.wantCode != "" {
				checkRefused(t, h, f, tt.wantStatus, tt.wantCode)
			} else {
				loc := tt.wantLocation
				if loc == "" {
					loc = "/"
				}
				me = checkSignedIn(t, h, f, loc)
			}
			if tt.check != nil {
				tt.check(t, h, hca, f, me)
			}
		})
	}
}

func TestSignInAgain(t *testing.T) {
	for _, engine := range storetest.Engines() {
		t.Run(string(engine), func(t *testing.T) {
			h, hca := withFake(t, engine, anyone)
			first := checkSignedIn(t, h, h.signIn(signInOptions{}), "/")
			id := hcafake.Verified()
			id.SlackID = "U0NEWSLACK"
			id.Email = "new@example.org"
			id.YSWSEligible = boolPtr(false)
			hca.SetIdentity(id)
			second := checkSignedIn(t, h, h.signIn(signInOptions{}), "/")
			tests := []struct {
				name string
				ok   bool
			}{
				{name: "the same HCA subject keeps its account", ok: second.ID == first.ID},
				{name: "the Slack id is refreshed", ok: second.SlackID == "U0NEWSLACK"},
				{name: "the email is refreshed", ok: second.Email == "new@example.org"},
				{name: "ysws_eligible is refreshed", ok: second.EligibilityToConfirm},
				{name: "there is still one account row", ok: h.count(`SELECT count(*) FROM users`) == 1},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					if !tt.ok {
						t.Errorf("first = %+v, second = %+v", first, second)
					}
				})
			}
		})
	}
}

func TestIneligibleLater(t *testing.T) {
	for _, engine := range storetest.Engines() {
		t.Run(string(engine), func(t *testing.T) {
			h, hca := withFake(t, engine, anyone)
			checkSignedIn(t, h, h.signIn(signInOptions{}), "/")
			id := hcafake.Verified()
			id.VerificationStatus = "ineligible"
			hca.SetIdentity(id)
			f := h.signIn(signInOptions{})
			tests := []struct {
				name string
				ok   bool
			}{
				{name: "the sign-in is refused as ineligible", ok: f.resp.status == http.StatusForbidden && errorCode(f.resp) == auth.CodeIneligible},
				{name: "the earlier session no longer works", ok: func() bool { r, _ := h.me(); return r.status == http.StatusUnauthorized }()},
				{name: "the account keeps its row, marked ineligible", ok: h.count(`SELECT count(*) FROM users WHERE verification_status = 'ineligible'`) == 1},
				{name: "the row keeps no email or Slack id, though HCA sent both", ok: h.count(`SELECT count(*) FROM users WHERE slack_id IS NULL AND email_ct IS NULL AND email_index IS NULL`) == 1},
				{name: "no session of the account is live", ok: h.count(`SELECT count(*) FROM sessions WHERE revoked_at_ms IS NULL`) == 0},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					if !tt.ok {
						t.Errorf("callback %d %q; log:\n%s", f.resp.status, errorCode(f.resp), h.logs.String())
					}
				})
			}
		})
	}
}

// TestCodeInjection replays the code from one browser's sign-in in another
// browser, as someone who saw the first browser's authorization response
// could. The nonce ties the code's ID token to the browser that started the
// sign-in (OIDC Core 15.5.2), and the login cookie's HMAC keeps a browser
// from writing its own cookie around another sign-in's nonce.
func TestCodeInjection(t *testing.T) {
	victim := hcafake.Verified()
	attacker := hcafake.Identity{Sub: "ident!fakeAttacker1", VerificationStatus: "verified"}
	tests := []struct {
		name string
		// forge writes the attacker's login cookie by hand from the
		// victim's authorization request, instead of starting a sign-in.
		forge      bool
		wantStatus int
		wantCode   string
	}{
		{name: "another browser's code fails the nonce check in the attacker's own sign-in", wantStatus: http.StatusUnauthorized, wantCode: auth.CodeWrongNonce},
		{name: "a login cookie written by hand around another browser's state and nonce is refused", forge: true, wantStatus: http.StatusBadRequest, wantCode: auth.CodeStateMissing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, hca := withFake(t, storetest.Engines()[0], anyone)
			hca.SetIdentity(victim)
			v := h.startSignIn(signInOptions{})
			// From here on the client is the attacker's browser.
			h.client.Jar = newJar(t)
			hca.SetIdentity(attacker)
			callback := *v.callback
			if tt.forge {
				// What an unsigned cookie would have needed: the victim's
				// state, and the nonce under both its old and new names.
				q := v.authorize.Query()
				st, err := json.Marshal(map[string]any{"s": q.Get("state"), "n": q.Get("nonce"), "v": q.Get("nonce"), "r": "/", "t": time.Now().Unix()})
				if err != nil {
					t.Fatal(err)
				}
				u, err := url.Parse(h.srv.URL)
				if err != nil {
					t.Fatal(err)
				}
				h.client.Jar.SetCookies(u, []*http.Cookie{{Name: loginCookieName, Value: base64.RawURLEncoding.EncodeToString(st), Path: "/", Secure: true, HttpOnly: true}})
			} else {
				own := h.startSignIn(signInOptions{})
				callback = *own.callback
				q := callback.Query()
				q.Set("code", v.callback.Query().Get("code"))
				callback.RawQuery = q.Encode()
			}
			f := &flow{authorize: v.authorize, callback: &callback, resp: h.get(callback.String())}
			checkRefused(t, h, f, tt.wantStatus, tt.wantCode)
			if n := h.count(`SELECT count(*) FROM sessions`); n != 0 {
				t.Errorf("%d sessions after the replayed code, want none", n)
			}
		})
	}
}

func TestLogout(t *testing.T) {
	tests := []struct {
		name      string
		origin    string // "" sends none; "self" sends the server's origin
		fetchSite string
		// breakWriter closes the writer first, so the revoke fails.
		breakWriter bool
		wantStatus  int
		wantCode    string // the error page's code, if there is one
		wantOut     bool   // this browser is signed out afterwards
		wantRevoked bool   // the session's row is revoked
	}{
		{name: "a sign-out from pairtimed's own page ends the session", origin: "self", wantStatus: http.StatusSeeOther, wantOut: true, wantRevoked: true},
		{name: "a sign-out posted from another site is refused", origin: "https://evil.example", wantStatus: http.StatusForbidden, wantCode: auth.CodeCrossOrigin},
		{name: "a cross-site request without Origin is refused by Sec-Fetch-Site", fetchSite: "cross-site", wantStatus: http.StatusForbidden, wantCode: auth.CodeCrossOrigin},
		{
			name:        "a sign-out the server can't record still signs this browser out, and its page says so",
			origin:      "self",
			breakWriter: true,
			wantStatus:  http.StatusInternalServerError,
			wantCode:    auth.CodeLogoutFailed,
			wantOut:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := withFake(t, storetest.Engines()[0], anyone)
			checkSignedIn(t, h, h.signIn(signInOptions{}), "/")
			if tt.breakWriter {
				h.w.Close()
			}
			header := http.Header{}
			switch tt.origin {
			case "":
			case "self":
				header.Set("Origin", h.srv.URL)
			default:
				header.Set("Origin", tt.origin)
			}
			if tt.fetchSite != "" {
				header.Set("Sec-Fetch-Site", tt.fetchSite)
			}
			r := h.postForm(h.srv.URL+auth.LogoutPath, url.Values{}, header)
			if r.status != tt.wantStatus || errorCode(r) != tt.wantCode {
				t.Fatalf("logout = %d with code %q, want %d with %q; body:\n%s", r.status, errorCode(r), tt.wantStatus, tt.wantCode, r.body)
			}
			if tt.wantCode == auth.CodeLogoutFailed && !strings.Contains(r.body, "This browser is signed out") {
				t.Errorf("the page doesn't say this browser is signed out:\n%s", r.body)
			}
			me, _ := h.me()
			if out := me.status == http.StatusUnauthorized; out != tt.wantOut {
				t.Errorf("signed out = %v, want %v", out, tt.wantOut)
			}
			if revoked := h.count(`SELECT count(*) FROM sessions WHERE revoked_at_ms IS NOT NULL`) == 1; revoked != tt.wantRevoked {
				t.Errorf("the session row revoked = %v, want %v", revoked, tt.wantRevoked)
			}
		})
	}
}

// TestSigningKeys checks when the JWKS is fetched: at the first sign-in,
// again for a kid the cached keys lack, which is how a rotated key arrives,
// and again once the keys are an hour old, but never twice in a minute.
func TestSigningKeys(t *testing.T) {
	tests := []struct {
		name string
		// After a first sign-in, which fetches the JWKS once:
		rotate     bool          // HCA signs with a new key from now on
		unknownKid bool          // the next ID token names a kid HCA never published
		wait       time.Duration // the clock moves on by this before the next sign-in
		wantCode   string        // "" means the next sign-in succeeds
		wantJWKS   int           // JWKS fetches in all
	}{
		{name: "a token signed with a rotated key is accepted after one more fetch", rotate: true, wait: 2 * time.Minute, wantJWKS: 2},
		{name: "a known key comes from the cache for an hour", wait: 59 * time.Minute, wantJWKS: 1},
		{name: "keys an hour old are fetched again", wait: time.Hour, wantJWKS: 2},
		{name: "an unknown kid within a minute of the last fetch is refused without fetching again", unknownKid: true, wait: 30 * time.Second, wantCode: auth.CodeUnknownKey, wantJWKS: 1},
		{name: "an unknown kid a minute after the last fetch is looked for in one more fetch", unknownKid: true, wait: time.Minute, wantCode: auth.CodeUnknownKey, wantJWKS: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mu sync.Mutex
			at := time.Now()
			clock := func() time.Time {
				mu.Lock()
				defer mu.Unlock()
				return at
			}
			hca := hcafake.New(t)
			hca.SetNow(clock)
			h := newHarness(t, harnessConfig{
				engine:       storetest.Engines()[0],
				issuer:       hca.URL,
				clientID:     hcafake.ClientID,
				clientSecret: hcafake.ClientSecret,
				mayCreate:    anyone,
				now:          clock,
				register:     hca.AddRedirectURI,
			})
			checkSignedIn(t, h, h.signIn(signInOptions{}), "/")
			if tt.rotate {
				hca.RotateKey(hcafake.Key(2))
			}
			if tt.unknownKid {
				hca.SetHook(func(tok *hcafake.Token) string {
					tok.Header["kid"] = "not-a-published-kid"
					return tok.Sign()
				})
			}
			mu.Lock()
			at = at.Add(tt.wait)
			mu.Unlock()
			// The next sign-in is another browser's.
			h.client.Jar = newJar(t)
			f := h.signIn(signInOptions{})
			if tt.wantCode == "" {
				checkSignedIn(t, h, f, "/")
			} else {
				checkRefused(t, h, f, http.StatusUnauthorized, tt.wantCode)
			}
			if n := hca.JWKSCalls(); n != tt.wantJWKS {
				t.Errorf("the JWKS was fetched %d times, want %d", n, tt.wantJWKS)
			}
		})
	}
}

func TestNoSecretsInLogs(t *testing.T) {
	h, hca := withFake(t, storetest.Engines()[0], anyone)
	ok := h.signIn(signInOptions{})
	checkSignedIn(t, h, ok, "/")
	hca.SetHook(resign(func(c map[string]any) { c["nonce"] = "x" }))
	bad := h.signIn(signInOptions{})
	logs := h.logs.String()
	secrets := map[string]string{
		"the client secret":         hcafake.ClientSecret,
		"the authorization code":    ok.callback.Query().Get("code"),
		"a refused sign-in's code":  bad.callback.Query().Get("code"),
		"the state":                 ok.authorize.Query().Get("state"),
		"the nonce":                 ok.authorize.Query().Get("nonce"),
		"the login cookie":          ok.loginCookie,
		"the login cookie's secret": loginSecret(t, ok.loginCookie),
		"the session token":         cookie(ok.resp, auth.SessionCookie).Value,
	}
	for name, v := range secrets {
		t.Run("the log never holds "+name, func(t *testing.T) {
			if v == "" {
				t.Fatal("the test lost the value")
			}
			if strings.Contains(logs, v) {
				t.Errorf("the log holds %s", name)
			}
		})
	}
}

func TestReviewerRoles(t *testing.T) {
	hca := hcafake.New(t)
	h := newHarness(t, harnessConfig{
		engine:       storetest.Engines()[0],
		issuer:       hca.URL,
		clientID:     hcafake.ClientID,
		clientSecret: hcafake.ClientSecret,
		mayCreate:    anyone,
		reviewers:    []config.Reviewer{{Sub: hcafake.Verified().Sub, Roles: []string{"qi", "program_reviewer:stardance"}}},
		register:     hca.AddRedirectURI,
	})
	me := checkSignedIn(t, h, h.signIn(signInOptions{}), "/")
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "the allowlist's roles for the HCA subject come with the session", ok: slices.Equal(me.Roles, []string{"qi", "program_reviewer:stardance"})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Errorf("roles = %v", me.Roles)
			}
		})
	}
}

func TestDiscoveryChecks(t *testing.T) {
	tests := []struct {
		name string
		edit func(doc map[string]any)
	}{
		{name: "a discovery document naming another issuer is refused", edit: func(d map[string]any) { d["issuer"] = "https://auth.example.org" }},
		{name: "a discovery document without response_type code is refused", edit: func(d map[string]any) { d["response_types_supported"] = []string{"token"} }},
		{name: "a discovery document with an http token endpoint off loopback is refused", edit: func(d map[string]any) { d["token_endpoint"] = "http://auth.example.org/oauth/token" }},
		{name: "a discovery document without RS256 is refused", edit: func(d map[string]any) { d["id_token_signing_alg_values_supported"] = []string{"HS256"} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, hca := withFake(t, storetest.Engines()[0], anyone)
			hca.SetDiscovery(tt.edit)
			r := h.get(h.srv.URL + auth.LoginPath)
			if r.status != http.StatusServiceUnavailable || errorCode(r) != auth.CodeHCAUnreachable {
				t.Errorf("login = %d %q, want 503 %q; log:\n%s", r.status, errorCode(r), auth.CodeHCAUnreachable, h.logs.String())
			}
		})
	}
}

func TestDiscoveryRefresh(t *testing.T) {
	type step struct {
		wait time.Duration // the clock moves on by this first
		// discovery, when set, changes HCA's discovery document from now
		// on: "broken" names another issuer, so every fetch fails, and
		// "fixed" undoes that.
		discovery  string
		wantStatus int // the login page's status
		wantCalls  int // discovery fetches so far
	}
	tests := []struct {
		name    string
		steps   []step
		wantLog string // the log holds this at the end
	}{
		{
			name: "a discovery document is fetched again once it is a day old",
			steps: []step{
				{wantStatus: http.StatusFound, wantCalls: 1},
				{wait: 23 * time.Hour, wantStatus: http.StatusFound, wantCalls: 1},
				{wait: time.Hour, wantStatus: http.StatusFound, wantCalls: 2},
			},
		},
		{
			name: "a failed refresh keeps the document fetched before, is logged, and is tried again a minute later",
			steps: []step{
				{wantStatus: http.StatusFound, wantCalls: 1},
				{wait: 25 * time.Hour, discovery: "broken", wantStatus: http.StatusFound, wantCalls: 2},
				{wantStatus: http.StatusFound, wantCalls: 2},
				{wait: 59 * time.Second, wantStatus: http.StatusFound, wantCalls: 2},
				{wait: time.Second, wantStatus: http.StatusFound, wantCalls: 3},
				{wait: time.Minute, discovery: "fixed", wantStatus: http.StatusFound, wantCalls: 4},
				{wait: time.Hour, wantStatus: http.StatusFound, wantCalls: 4},
			},
			wantLog: "discovery document couldn't be refreshed",
		},
		{
			name: "without a document fetched before, a failed fetch is tried again a minute later, not on every sign-in",
			steps: []step{
				{discovery: "broken", wantStatus: http.StatusServiceUnavailable, wantCalls: 1},
				{wantStatus: http.StatusServiceUnavailable, wantCalls: 1},
				{wait: time.Minute, wantStatus: http.StatusServiceUnavailable, wantCalls: 2},
				{wait: time.Minute, discovery: "fixed", wantStatus: http.StatusFound, wantCalls: 3},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mu sync.Mutex
			at := time.Now()
			clock := func() time.Time {
				mu.Lock()
				defer mu.Unlock()
				return at
			}
			hca := hcafake.New(t)
			h := newHarness(t, harnessConfig{
				engine:       storetest.Engines()[0],
				issuer:       hca.URL,
				clientID:     hcafake.ClientID,
				clientSecret: hcafake.ClientSecret,
				mayCreate:    anyone,
				now:          clock,
				register:     hca.AddRedirectURI,
			})
			for i, st := range tt.steps {
				mu.Lock()
				at = at.Add(st.wait)
				mu.Unlock()
				switch st.discovery {
				case "broken":
					hca.SetDiscovery(func(d map[string]any) { d["issuer"] = "https://auth.example.org" })
				case "fixed":
					hca.SetDiscovery(nil)
				}
				r := h.get(h.srv.URL + auth.LoginPath)
				if r.status != st.wantStatus || hca.DiscoveryCalls() != st.wantCalls {
					t.Fatalf("step %d: login = %d after %d discovery fetches, want %d after %d; log:\n%s",
						i, r.status, hca.DiscoveryCalls(), st.wantStatus, st.wantCalls, h.logs.String())
				}
			}
			if !strings.Contains(h.logs.String(), tt.wantLog) {
				t.Errorf("the log doesn't hold %q:\n%s", tt.wantLog, h.logs.String())
			}
		})
	}
}

func TestDiscoveryHang(t *testing.T) {
	var mu sync.Mutex
	at := time.Now()
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return at
	}
	hca := hcafake.New(t)
	h := newHarness(t, harnessConfig{
		engine:       storetest.Engines()[0],
		issuer:       hca.URL,
		clientID:     hcafake.ClientID,
		clientSecret: hcafake.ClientSecret,
		mayCreate:    anyone,
		now:          clock,
		register:     hca.AddRedirectURI,
	})
	if r := h.get(h.srv.URL + auth.LoginPath); r.status != http.StatusFound {
		t.Fatalf("the first login page answered %d, want 302", r.status)
	}
	// From now on HCA's discovery endpoint hangs until released, and the
	// document fetched at first is over a day old.
	started, released := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(released) })
	defer release()
	var once sync.Once
	hca.SetDiscovery(func(map[string]any) {
		once.Do(func() { close(started) })
		<-released
	})
	mu.Lock()
	at = at.Add(25 * time.Hour)
	mu.Unlock()

	// h.get calls t.Fatalf, which only the test's own goroutine may, so the
	// login that starts the refresh uses the client directly.
	first := make(chan int, 1)
	go func() {
		resp, err := h.client.Get(h.srv.URL + auth.LoginPath)
		if err != nil {
			first <- 0
			return
		}
		resp.Body.Close()
		first <- resp.StatusCode
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the refresh never reached HCA")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	second := 0
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.srv.URL+auth.LoginPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := h.client.Do(req); err == nil {
		resp.Body.Close()
		second = resp.StatusCode
	}
	release()
	firstStatus := <-first
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "another sign-in gets the document fetched before while the refresh hangs", ok: second == http.StatusFound},
		{name: "the sign-in that started the refresh gets its answer once HCA answers", ok: firstStatus == http.StatusFound},
		{name: "only the one request fetched the document again", ok: hca.DiscoveryCalls() == 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Errorf("second login = %d, first = %d, discovery fetches = %d", second, firstStatus, hca.DiscoveryCalls())
			}
		})
	}
}

func TestSignInOff(t *testing.T) {
	svc, err := auth.New(auth.Config{BaseURL: "https://pairtime.example.org"})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	svc.Register(mux)
	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantBody   string
	}{
		{name: "the login page says sign-in isn't set up", path: auth.LoginPath, wantStatus: http.StatusServiceUnavailable, wantBody: "<code>not_configured</code>"},
		{name: "the callback says sign-in isn't set up", path: auth.CallbackPath + "?code=x&state=y", wantStatus: http.StatusServiceUnavailable, wantBody: "<code>not_configured</code>"},
		{name: "nobody is signed in", path: auth.MePath, wantStatus: http.StatusUnauthorized, wantBody: `"error":"not_signed_in"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, "https://pairtime.example.org"+tt.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus || !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Errorf("%s = %d %q, want %d containing %q", tt.path, rec.Code, rec.Body.String(), tt.wantStatus, tt.wantBody)
			}
		})
	}
	if svc.Enabled() {
		t.Error("Enabled() = true without a client id")
	}
}
