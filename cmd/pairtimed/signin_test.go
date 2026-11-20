// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/auth/hcafake"
	"github.com/some-du6e/pairtime/internal/server/config"
)

// writeSignInConfig writes writeConfig's pairtime.toml with sign-ups open
// and hca.client_id set, pointing hca.issuer at issuer.
func writeSignInConfig(t *testing.T, issuer string) string {
	t.Helper()
	configPath, _ := writeConfig(t)
	f, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "signups_open = true\n\n[hca]\nclient_id = %q\nissuer = %q\n", hcafake.ClientID, issuer); err != nil {
		t.Fatal(err)
	}
	return configPath
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
			configPath := writeSignInConfig(t, hca.URL)
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
	configPath := writeSignInConfig(t, hca.URL)
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

func TestSignupGate(t *testing.T) {
	tests := []struct {
		name        string
		signupsOpen bool
		want        bool
	}{
		{name: "with signups_open = true any new identity may create an account", signupsOpen: true, want: true},
		{name: "with signups_open = false no new identity may", signupsOpen: false, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gate := signupGate(&config.Config{SignupsOpen: tt.signupsOpen})
			for _, sub := range []string{"ident!abc123", "ident!someoneElse"} {
				if got := gate(sub); got != tt.want {
					t.Errorf("the gate answers %v for %s, want %v", got, sub, tt.want)
				}
			}
		})
	}
}
