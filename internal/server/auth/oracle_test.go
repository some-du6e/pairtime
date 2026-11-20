// SPDX-License-Identifier: AGPL-3.0-or-later

package auth_test

import (
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/auth"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
)

// mockEnv names the base URL of a running navikt/mock-oauth2-server.
const mockEnv = "PAIRTIME_TEST_OIDC_URL"

// TestMockOAuth2Server is WP-SV2's oracle: pairtimed's sign-in end to end
// against navikt/mock-oauth2-server, an OpenID Connect server nobody on
// this project wrote, so the ID token rules of OIDC Core 3.1.3.7 are
// checked against tokens pairtime's own fake didn't make.
//
// ci.yml starts the server, pinned by digest, with
// testdata/mock-oauth2-server.json, and sets PAIRTIME_TEST_OIDC_URL. The
// config gives each client id the claims one case needs: an HCA-shaped
// verified or ineligible identity, or a token with a wrong nonce, issuer,
// audience or expiry. Locally:
//
//	docker run -d -p 8080:8080 -e JSON_CONFIG_PATH=/c/mock-oauth2-server.json \
//	  -v "$PWD/internal/server/auth/testdata:/c:ro" ghcr.io/navikt/mock-oauth2-server:6.1.4
//	PAIRTIME_TEST_OIDC_URL=http://localhost:8080 go test -run TestMockOAuth2Server ./internal/server/auth
func TestMockOAuth2Server(t *testing.T) {
	base := os.Getenv(mockEnv)
	if base == "" {
		t.Skipf("%s isn't set; ci.yml starts navikt/mock-oauth2-server and sets it", mockEnv)
	}
	issuer := strings.TrimSuffix(base, "/") + "/hca"
	tests := []struct {
		name       string
		clientID   string
		opts       signInOptions
		wantStatus int
		wantCode   string // "" means the sign-in succeeds
	}{
		{name: "a verified identity signs in", clientID: "pairtime-verified"},
		{name: "a fresh login gets the provider's login form and comes back with a recent auth_time", clientID: "pairtime-verified", opts: signInOptions{query: "?fresh=1"}},
		{name: "an ineligible identity is refused", clientID: "pairtime-ineligible", wantStatus: http.StatusForbidden, wantCode: auth.CodeIneligible},
		{name: "a callback with a wrong state is refused", clientID: "pairtime-verified", opts: signInOptions{edit: appendToState}, wantStatus: http.StatusBadRequest, wantCode: auth.CodeStateMismatch},
		{name: "an ID token with a wrong nonce is refused", clientID: "pairtime-wrong-nonce", wantStatus: http.StatusUnauthorized, wantCode: auth.CodeWrongNonce},
		{name: "an ID token from a wrong issuer is refused", clientID: "pairtime-wrong-issuer", wantStatus: http.StatusUnauthorized, wantCode: auth.CodeWrongIssuer},
		{name: "an ID token for a wrong audience is refused", clientID: "pairtime-wrong-audience", wantStatus: http.StatusUnauthorized, wantCode: auth.CodeWrongAudience},
		{name: "an expired ID token is refused", clientID: "pairtime-expired", wantStatus: http.StatusUnauthorized, wantCode: auth.CodeExpired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, harnessConfig{
				engine:       storetest.Engines()[0],
				issuer:       issuer,
				clientID:     tt.clientID,
				clientSecret: "mock-oauth2-server-takes-any-secret",
				signupsOpen:  true,
			})
			f := h.signIn(tt.opts)
			if tt.wantCode != "" {
				checkRefused(t, h, f, tt.wantStatus, tt.wantCode)
				if tt.wantCode == auth.CodeIneligible && h.count(`SELECT count(*) FROM users`) != 0 {
					t.Error("an ineligible identity got an account row")
				}
				return
			}
			me := checkSignedIn(t, h, f, "/")
			if me.HCASub != "ident!mockVerified" || me.VerificationStatus != "verified" || !me.CanSubmit ||
				me.Email != "verified@example.org" || me.SlackID != "U0MOCKVERIFIED" {
				t.Errorf("me = %+v", me)
			}
			if tt.opts.query == "?fresh=1" {
				if q := f.authorize.Query(); q.Get("prompt") != "login" || q.Get("max_age") != "0" {
					t.Errorf("authorize prompt = %q, max_age = %q; want login and 0", q.Get("prompt"), q.Get("max_age"))
				}
				if !f.prompted {
					t.Error("the provider didn't ask for a login, so prompt=login didn't reach it")
				}
				at := me.Session.AuthTime
				if at == nil || time.Since(*at) > time.Minute || time.Since(*at) < -time.Minute {
					t.Errorf("session auth_time = %v, want about now", at)
				}
			}
		})
	}
}

// appendToState makes the callback's state differ from the cookie's.
func appendToState(u *url.URL) {
	q := u.Query()
	q.Set("state", q.Get("state")+"x")
	u.RawQuery = q.Encode()
}
