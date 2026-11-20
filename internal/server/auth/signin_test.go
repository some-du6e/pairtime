// SPDX-License-Identifier: AGPL-3.0-or-later

package auth_test

import (
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

// withFake returns a harness signing in through a fresh fake HCA.
func withFake(t *testing.T, engine store.Engine, signupsOpen bool) (*harness, *hcafake.Server) {
	t.Helper()
	hca := hcafake.New(t)
	h := newHarness(t, harnessConfig{
		engine:       engine,
		issuer:       hca.URL,
		clientID:     hcafake.ClientID,
		clientSecret: hcafake.ClientSecret,
		signupsOpen:  signupsOpen,
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
		closed   bool              // sign-ups closed
		deny     bool
		hook     func(*hcafake.Token) string
		opts     signInOptions
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
			name: "an ID token naming an unknown key is refused without fetching the JWKS again",
			hook: func(tok *hcafake.Token) string {
				tok.Header["kid"] = "not-a-published-kid"
				return tok.SignWith(hcafake.Key(1))
			},
			wantStatus: http.StatusUnauthorized,
			wantCode:   auth.CodeUnknownKey,
			check: func(t *testing.T, _ *harness, hca *hcafake.Server, _ *flow, _ auth.Me) {
				if n := hca.JWKSCalls(); n != 1 {
					t.Errorf("the JWKS was fetched %d times, want once", n)
				}
			},
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
				at := me.Session.AuthTime
				if at == nil || time.Since(*at) > time.Minute || time.Since(*at) < -time.Minute {
					t.Errorf("session auth_time = %v, want about now", at)
				}
			},
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
			h, hca := withFake(t, engine, !tt.closed)
			if tt.identity != nil {
				hca.SetIdentity(*tt.identity)
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
			h, hca := withFake(t, engine, true)
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
			h, hca := withFake(t, engine, true)
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

func TestLogout(t *testing.T) {
	tests := []struct {
		name       string
		origin     string // "" sends none; "self" sends the server's origin
		fetchSite  string
		wantStatus int
		wantOut    bool // signed out afterwards
	}{
		{name: "a sign-out from pairtimed's own page ends the session", origin: "self", wantStatus: http.StatusSeeOther, wantOut: true},
		{name: "a sign-out posted from another site is refused", origin: "https://evil.example", wantStatus: http.StatusForbidden},
		{name: "a cross-site request without Origin is refused by Sec-Fetch-Site", fetchSite: "cross-site", wantStatus: http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := withFake(t, storetest.Engines()[0], true)
			checkSignedIn(t, h, h.signIn(signInOptions{}), "/")
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
			if r.status != tt.wantStatus {
				t.Fatalf("logout = %d, want %d; body:\n%s", r.status, tt.wantStatus, r.body)
			}
			me, _ := h.me()
			if out := me.status == http.StatusUnauthorized; out != tt.wantOut {
				t.Errorf("signed out = %v, want %v", out, tt.wantOut)
			}
			if tt.wantOut && h.count(`SELECT count(*) FROM sessions WHERE revoked_at_ms IS NOT NULL`) != 1 {
				t.Error("the session row wasn't revoked")
			}
		})
	}
}

func TestKeyRotation(t *testing.T) {
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
		signupsOpen:  true,
		now:          clock,
		register:     hca.AddRedirectURI,
	})
	checkSignedIn(t, h, h.signIn(signInOptions{}), "/")
	hca.RotateKey(hcafake.Key(2))
	mu.Lock()
	at = at.Add(2 * time.Minute)
	mu.Unlock()
	checkSignedIn(t, h, h.signIn(signInOptions{}), "/")
	if n := hca.JWKSCalls(); n != 2 {
		t.Errorf("the JWKS was fetched %d times, want twice: once at first, once for the rotated key", n)
	}
}

func TestNoSecretsInLogs(t *testing.T) {
	h, hca := withFake(t, storetest.Engines()[0], true)
	ok := h.signIn(signInOptions{})
	checkSignedIn(t, h, ok, "/")
	hca.SetHook(resign(func(c map[string]any) { c["nonce"] = "x" }))
	bad := h.signIn(signInOptions{})
	logs := h.logs.String()
	secrets := map[string]string{
		"the client secret":        hcafake.ClientSecret,
		"the authorization code":   ok.callback.Query().Get("code"),
		"a refused sign-in's code": bad.callback.Query().Get("code"),
		"the state":                ok.authorize.Query().Get("state"),
		"the nonce":                ok.authorize.Query().Get("nonce"),
		"the session token":        cookie(ok.resp, auth.SessionCookie).Value,
	}
	for name, v := range secrets {
		t.Run("the log never holds "+name, func(t *testing.T) {
			if v == "" {
				t.Fatal("the test lost the value")
			}
			if strings.Contains(logs, v) {
				t.Errorf("the log holds %s:\n%s", name, logs)
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
		signupsOpen:  true,
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
			h, hca := withFake(t, storetest.Engines()[0], true)
			hca.SetDiscovery(tt.edit)
			r := h.get(h.srv.URL + auth.LoginPath)
			if r.status != http.StatusServiceUnavailable || errorCode(r) != auth.CodeHCAUnreachable {
				t.Errorf("login = %d %q, want 503 %q; log:\n%s", r.status, errorCode(r), auth.CodeHCAUnreachable, h.logs.String())
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
