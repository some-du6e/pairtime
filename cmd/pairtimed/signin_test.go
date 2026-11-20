// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/accounts"
	"github.com/some-du6e/pairtime/internal/server/auth"
	"github.com/some-du6e/pairtime/internal/server/auth/hcafake"
	"github.com/some-du6e/pairtime/internal/server/store"
)

// writeSignInConfig writes writeConfig's pairtime.toml with a named
// controller and hca.client_id set, pointing hca.issuer at issuer. top holds
// more top-level keys, such as "signups_open = true\n". It returns the
// file's path and the database's.
func writeSignInConfig(t *testing.T, issuer, top string) (configPath, dbPath string) {
	t.Helper()
	configPath, dbPath = writeConfig(t)
	f, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "%s\n[controller]\nname = \"Test Club\"\ncontact = \"privacy@example.org\"\n\n[hca]\nclient_id = %q\nissuer = %q\n",
		top, hcafake.ClientID, issuer); err != nil {
		t.Fatal(err)
	}
	return configPath, dbPath
}

func TestServeSignInSecrets(t *testing.T) {
	hca := hcafake.New(t)
	secret := "PAIRTIME_HCA_CLIENT_SECRET=" + hcafake.ClientSecret
	key := "PAIRTIME_EMAIL_KEY=" + strings.Repeat("5a", 32)
	tests := []struct {
		name       string
		environ    []string
		wantStderr string
	}{
		{name: "with hca.client_id, serve refuses to start without the email key", environ: []string{secret}, wantStderr: "PAIRTIME_EMAIL_KEY"},
		{name: "with hca.client_id, serve refuses to start without the client secret", environ: []string{key}, wantStderr: "PAIRTIME_HCA_CLIENT_SECRET"},
		{name: "a malformed email key is refused without being quoted", environ: []string{secret, "PAIRTIME_EMAIL_KEY=hunter2hunter2"}, wantStderr: "64 hex digits"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath, _ := writeSignInConfig(t, hca.URL, "signups_open = true\n")
			e, _, stderr := testEnv(tt.environ...)
			e.listening = func(public, ops string) {
				t.Errorf("serve listened on %s and %s", public, ops)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if code := run(ctx, []string{"serve", "-config", configPath}, e); code != 1 {
				t.Errorf("serve exit code = %d, want 1", code)
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
			if strings.Contains(stderr.String(), "hunter2") || strings.Contains(stderr.String(), hcafake.ClientSecret) {
				t.Errorf("stderr quotes a secret: %q", stderr.String())
			}
		})
	}
}

func TestServeSignIn(t *testing.T) {
	hca := hcafake.New(t)
	configPath, _ := writeSignInConfig(t, hca.URL, "signups_open = true\n")
	e, _, stderr := testEnv(
		"PAIRTIME_HCA_CLIENT_SECRET="+hcafake.ClientSecret,
		"PAIRTIME_EMAIL_KEY="+strings.Repeat("5a", 32),
	)
	up := make(chan string, 1)
	e.listening = func(public, _ string) { up <- public }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exited := make(chan int, 1)
	go func() { exited <- run(ctx, []string{"serve", "-config", configPath}, e) }()
	var public string
	select {
	case public = <-up:
	case code := <-exited:
		t.Fatalf("serve exited with %d before listening; stderr:\n%s", code, stderr.String())
	case <-time.After(60 * time.Second):
		t.Fatalf("serve didn't start within a minute; stderr:\n%s", stderr.String())
	}

	client := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Get("http://" + public + "/auth/hca/login")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	q := loc.Query()
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "the login page redirects", ok: resp.StatusCode == http.StatusFound},
		{name: "it redirects to the authorization endpoint of hca.issuer's discovery document", ok: strings.HasPrefix(loc.String(), hca.URL+"/oauth/authorize?")},
		{name: "the client id is hca.client_id", ok: q.Get("client_id") == hcafake.ClientID},
		{name: "the redirect URI is base_url's callback", ok: q.Get("redirect_uri") == "http://localhost:8080/auth/hca/callback"},
		{name: "the scopes are the four pairtime asks for", ok: q.Get("scope") == "openid email slack_id verification_status"},
		{name: "the login cookie is set", ok: strings.Contains(resp.Header.Get("Set-Cookie"), "__Host-pairtime_hca_login=")},
		{name: "the log names the redirect URI to register with HCA", ok: strings.Contains(stderr.String(), "redirect_uri=http://localhost:8080/auth/hca/callback")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Errorf("login = %d, Location %q, Set-Cookie %q; stderr:\n%s", resp.StatusCode, loc, resp.Header.Get("Set-Cookie"), stderr.String())
			}
		})
	}
	if strings.Contains(stderr.String(), hcafake.ClientSecret) {
		t.Error("the log holds the client secret")
	}

	cancel()
	select {
	case code := <-exited:
		if code != 0 {
			t.Errorf("serve exit code after cancel = %d, want 0; stderr:\n%s", code, stderr.String())
		}
	case <-time.After(60 * time.Second):
		t.Fatal("serve didn't stop within a minute of its context ending")
	}
}

// TestServeSignups signs in through pairtimed serve and the fake HCA, the
// real callback, while signups_open is false: an identity on the operators
// list gets an account, and any other new identity gets the signups_closed
// page and no account (DESIGN 8.6; WP-SV14).
func TestServeSignups(t *testing.T) {
	hca := hcafake.New(t)
	// base_url's callback, which pairtimed sends as its redirect URI.
	hca.AddRedirectURI("http://localhost:8080" + auth.CallbackPath)
	operator := hcafake.Verified()
	stranger := hcafake.Identity{Sub: "ident!fakeStranger1", Email: "stranger@example.org", VerificationStatus: "verified"}
	configPath, dbPath := writeSignInConfig(t, hca.URL, fmt.Sprintf("operators = [%q]\n", operator.Sub))
	e, _, stderr := testEnv(
		"PAIRTIME_HCA_CLIENT_SECRET="+hcafake.ClientSecret,
		"PAIRTIME_EMAIL_KEY="+strings.Repeat("5a", 32),
	)
	up := make(chan string, 1)
	e.listening = func(public, _ string) { up <- public }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exited := make(chan int, 1)
	go func() { exited <- run(ctx, []string{"serve", "-config", configPath}, e) }()
	var public string
	select {
	case public = <-up:
	case code := <-exited:
		t.Fatalf("serve exited with %d before listening; stderr:\n%s", code, stderr.String())
	case <-time.After(60 * time.Second):
		t.Fatalf("serve didn't start within a minute; stderr:\n%s", stderr.String())
	}

	client := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	// get fetches u with the cookies given. pairtimed's cookies are Secure,
	// and the test speaks plain http to a loopback port, so it carries them
	// itself instead of through a cookie jar.
	get := func(t *testing.T, u string, cookies ...*http.Cookie) (*http.Response, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cookies {
			req.AddCookie(c)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp, string(b)
	}
	cookie := func(resp *http.Response, name string) *http.Cookie {
		for _, c := range resp.Cookies() {
			if c.Name == name && c.MaxAge >= 0 {
				return c
			}
		}
		return nil
	}
	// signIn runs a sign-in as a browser would: pairtimed's login page, the
	// fake HCA's authorization endpoint, and pairtimed's callback, which the
	// fake sends to base_url and the test sends to the real listener.
	signIn := func(t *testing.T, id hcafake.Identity) (*http.Response, string) {
		t.Helper()
		hca.SetIdentity(id)
		resp, body := get(t, "http://"+public+auth.LoginPath)
		login := cookie(resp, "__Host-pairtime_hca_login")
		if resp.StatusCode != http.StatusFound || login == nil {
			t.Fatalf("the login page answered %d without a login cookie; body:\n%s", resp.StatusCode, body)
		}
		resp, body = get(t, resp.Header.Get("Location"))
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("the fake HCA answered %d; body:\n%s", resp.StatusCode, body)
		}
		callback, err := url.Parse(resp.Header.Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		callback.Host = public
		return get(t, callback.String(), login)
	}

	t.Run("the home page offers a visitor who isn't signed in the Sign in with Hack Club button, and says who may get an account while sign-ups are closed", func(t *testing.T) {
		resp, body := get(t, "http://"+public+"/")
		button := `<a class="button" href="` + auth.LoginPath + `">Sign in with Hack Club</a>`
		closed := "Until sign-ups open, this server makes accounts only for the people who run it."
		if resp.StatusCode != http.StatusOK || !strings.Contains(body, button) || !strings.Contains(body, closed) || strings.Contains(body, "Signed in") {
			t.Errorf("/ without a session answered %d, want 200 with the sign-in button and %q; body:\n%s", resp.StatusCode, closed, body)
		}
	})

	tests := []struct {
		name     string
		identity hcafake.Identity
		// wantCode is the error page's code, or "" for a sign-in that
		// succeeds.
		wantCode string
	}{
		{name: "while signups_open is false an identity on the operators list signs in and gets an account", identity: operator},
		{name: "while signups_open is false a new identity not on the operators list gets the signups_closed page", identity: stranger, wantCode: auth.CodeSignupsClosed},
		{name: "the operator's account signs in again", identity: operator},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, body := signIn(t, tt.identity)
			session := cookie(resp, auth.SessionCookie)
			if tt.wantCode != "" {
				if resp.StatusCode != http.StatusForbidden || !strings.Contains(body, "<code>"+tt.wantCode+"</code>") || !strings.Contains(body, "no account was made for you") {
					t.Errorf("the callback answered %d, want 403 with code %s; body:\n%s", resp.StatusCode, tt.wantCode, body)
				}
				if session != nil {
					t.Error("a refused sign-in set a session cookie")
				}
				return
			}
			if resp.StatusCode != http.StatusSeeOther || session == nil {
				t.Fatalf("the callback answered %d, session cookie %v; want 303 with a session; body:\n%s\nstderr:\n%s", resp.StatusCode, session, body, stderr.String())
			}
			// Without return_to, the sign-in lands on the home page, which
			// names the account and offers to sign out.
			if landing := resp.Header.Get("Location"); landing != "/" {
				t.Errorf("the callback sent the browser to %q, want /", landing)
			}
			resp, body = get(t, "http://"+public+"/", session)
			signedIn := "<p>Signed in as <strong>" + tt.identity.SlackID + "</strong></p>"
			signOut := `<form method="post" action="` + auth.LogoutPath + `"><button type="submit">Sign out</button></form>`
			if resp.StatusCode != http.StatusOK || !strings.Contains(body, signedIn) || !strings.Contains(body, signOut) {
				t.Errorf("/ with the session answered %d, want 200 with %q and the sign-out form; body:\n%s", resp.StatusCode, signedIn, body)
			}
			if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "form-action 'self'") {
				t.Errorf("the signed-in home page's CSP is %q, which doesn't let its sign-out form post", csp)
			}
			resp, body = get(t, "http://"+public+auth.MePath, session)
			var me auth.Me
			if resp.StatusCode != http.StatusOK || json.Unmarshal([]byte(body), &me) != nil || me.HCASub != tt.identity.Sub {
				t.Errorf("%s answered %d: %s; want the account of %s", auth.MePath, resp.StatusCode, body, tt.identity.Sub)
			}

			// The sign-out form posts with the Origin a browser sends under
			// the home page's same-origin referrer policy, and lands back on
			// the home page, signed out.
			req, err := http.NewRequest(http.MethodPost, "http://"+public+auth.LogoutPath, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.AddCookie(session)
			req.Header.Set("Origin", "http://localhost:8080")
			out, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			out.Body.Close()
			if out.StatusCode != http.StatusSeeOther || out.Header.Get("Location") != "/" {
				t.Errorf("signing out answered %d to %q, want 303 to /", out.StatusCode, out.Header.Get("Location"))
			}
			resp, body = get(t, "http://"+public+"/", session)
			if resp.StatusCode != http.StatusOK || strings.Contains(body, "Signed in") || !strings.Contains(body, `href="`+auth.LoginPath+`"`) {
				t.Errorf("/ after signing out answered %d, want 200 with the sign-in button again; body:\n%s", resp.StatusCode, body)
			}
		})
	}

	cancel()
	select {
	case code := <-exited:
		if code != 0 {
			t.Errorf("serve exit code after cancel = %d, want 0; stderr:\n%s", code, stderr.String())
		}
	case <-time.After(60 * time.Second):
		t.Fatal("serve didn't stop within a minute of its context ending")
	}

	db, err := store.Open(context.Background(), store.Options{Path: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows := []struct {
		name string
		sub  string
		want int
	}{
		{name: "the operator has one account row", sub: operator.Sub, want: 1},
		{name: "the refused identity has no account row", sub: stranger.Sub, want: 0},
	}
	for _, tt := range rows {
		t.Run(tt.name, func(t *testing.T) {
			var n int
			if err := db.Read.QueryRow(`SELECT count(*) FROM users WHERE hca_sub = $1`, tt.sub).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != tt.want {
				t.Errorf("%d account rows for %s, want %d", n, tt.sub, tt.want)
			}
		})
	}
}

// TestHandle checks the name the home page gives a signed-in account.
func TestHandle(t *testing.T) {
	tests := []struct {
		name string
		user accounts.User
		want string
	}{
		{name: "an account with a Slack ID is named by it, even when it has an email address", user: accounts.User{HCASub: "ident!fakeVerified1", SlackID: "U0FAKEVERIFIED", HasEmail: true}, want: "U0FAKEVERIFIED"},
		{name: "an account without a Slack ID is named by its HCA subject", user: accounts.User{HCASub: "ident!fakeVerified1", HasEmail: true}, want: "ident!fakeVerified1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := handle(&tt.user); got != tt.want {
				t.Errorf("handle(%+v) = %q, want %q", tt.user, got, tt.want)
			}
		})
	}
}
