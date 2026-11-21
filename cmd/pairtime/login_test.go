// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/account"
	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/server/accounts"
	"github.com/some-du6e/pairtime/internal/server/auth"
	"github.com/some-du6e/pairtime/internal/server/auth/hcafake"
	"github.com/some-du6e/pairtime/internal/server/device"
	"github.com/some-du6e/pairtime/internal/server/migrations"
	"github.com/some-du6e/pairtime/internal/server/pages"
	"github.com/some-du6e/pairtime/internal/server/retention"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// oidcEnv names the base URL of a running navikt/mock-oauth2-server, which
// ci.yml starts for WP-SV2's oracle and this test.
const oidcEnv = "PAIRTIME_TEST_OIDC_URL"

// TestLoginEndToEnd is WP-SV3's oracle: the real `pairtime login`
// command links this install to a real pairtimed, whose sign-in goes
// through an OpenID Connect server, while a browser signs in and approves
// on /device; then `pairtime logout` unlinks it. The OIDC server is the
// fake HCA, and with PAIRTIME_TEST_OIDC_URL set, as ci.yml does, also
// navikt/mock-oauth2-server, which nobody on this project wrote. The
// command polls in real time, so each case takes about 5 s.
func TestLoginEndToEnd(t *testing.T) {
	tests := []struct {
		name     string
		provider func(t *testing.T) oidcProvider
	}{
		{name: "hcafake", provider: func(t *testing.T) oidcProvider {
			fake := hcafake.New(t)
			return oidcProvider{issuer: fake.URL, clientID: hcafake.ClientID, secret: hcafake.ClientSecret, register: fake.AddRedirectURI}
		}},
		{name: "mock-oauth2-server", provider: func(t *testing.T) oidcProvider {
			base := os.Getenv(oidcEnv)
			if base == "" {
				t.Skipf("%s isn't set; ci.yml starts navikt/mock-oauth2-server and sets it", oidcEnv)
			}
			return oidcProvider{issuer: strings.TrimSuffix(base, "/") + "/hca", clientID: "pairtime-verified", secret: "mock-oauth2-server-takes-any-secret"}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { testLoginEndToEnd(t, tt.provider(t)) })
	}
}

// oidcProvider is the OIDC server pairtimed's sign-in uses in a case.
type oidcProvider struct {
	issuer, clientID, secret string
	// register registers the redirect URI, for a provider that checks it.
	register func(redirectURI string)
}

// userCode finds the code `pairtime login` prints.
var userCode = regexp.MustCompile(`\b[BCDFGHJKLMNPQRSTVWXZ]{4}-[BCDFGHJKLMNPQRSTVWXZ]{4}\b`)

func testLoginEndToEnd(t *testing.T, p oidcProvider) {
	srv, devices := startPairtimed(t, p)
	dir := t.TempDir()
	t.Setenv(home.EnvHome, dir)
	const deviceID = "d_e2elogin4nhv5xgjw6ycpbz2r"
	// The install's agent ran before, so its ledger chain is on record.
	ks, err := keys.Open(keys.Options{Dir: dir, Stores: []keys.Store{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := ks.RecordChain(deviceID); err != nil {
		t.Fatal(err)
	}
	savedTransport, savedContext := loginTransport, loginContext
	t.Cleanup(func() { loginTransport, loginContext = savedTransport, savedContext })
	loginTransport = srv.Client().Transport
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	loginContext = func() context.Context { return ctx }

	// pairtime login, with a browser that approves the code it prints.
	out, errOut, code, approveErr := runLoginApproving(srv, cancel)
	if approveErr != nil || code != 0 {
		t.Fatalf("pairtime login exited %d (approving: %v)\nstdout:\n%s\nstderr:\n%s", code, approveErr, out, errOut)
	}
	if !strings.Contains(out, "Linked. This computer is device "+deviceID) || !strings.Contains(out, "It's linked to the account U0") {
		t.Errorf("pairtime login printed:\n%s", out)
	}

	l, err := account.Load(dir)
	if err != nil || l.Server != srv.URL || l.DeviceID != deviceID {
		t.Errorf("account.json = %+v, %v; want server %s and device %s", l, err, srv.URL, deviceID)
	}
	cfg, problems, err := config.Load(filepath.Join(dir, "config.toml"))
	if err != nil || len(problems) != 0 || cfg.Mode != config.Online || cfg.Server.URL != srv.URL {
		t.Errorf("config.toml = %+v, %q, %v; want mode online and the server", cfg, problems, err)
	}
	creds, err := account.Open(dir, ks)
	if err != nil {
		t.Fatalf("account.Open = %v", err)
	}
	d, err := devices.Authenticate(context.Background(), creds.Token)
	switch {
	case err != nil:
		t.Fatalf("the server doesn't know the token login kept: %v", err)
	case d.ID != deviceID || d.OS != account.OSFamily():
		t.Errorf("the server's device = %+v", d)
	case !d.PublicKey.Equal(creds.Key.Public().(ed25519.PublicKey)):
		t.Error("the key the server registered isn't the one in the keystore")
	}

	// pairtime logout removes the device on the server and forgets it here.
	var lout, lerr bytes.Buffer
	if code := run([]string{"logout"}, &lout, &lerr); code != 0 {
		t.Fatalf("pairtime logout exited %d\nstdout:\n%s\nstderr:\n%s", code, lout.String(), lerr.String())
	}
	if _, err := devices.Authenticate(context.Background(), creds.Token); !errors.Is(err, device.ErrUnknownToken) {
		t.Errorf("after logout the server answers the token with %v, want ErrUnknownToken: a device that never uploaded is deleted", err)
	}
	if _, err := account.Load(dir); !errors.Is(err, account.ErrNotLinked) {
		t.Errorf("account.json after logout: %v", err)
	}
	if _, _, err := ks.Account(); !errors.Is(err, keys.ErrNotFound) {
		t.Errorf("the keystore's sign-in after logout: %v", err)
	}
	if cfg, _, _ := config.Load(filepath.Join(dir, "config.toml")); cfg.Mode != config.Local {
		t.Errorf("config.toml's mode after logout is %q", cfg.Mode)
	}
}

// TestLogoutWithoutKeystore is pairtime logout when the device keystore
// can't be read, so switching config.toml to local is all that stops
// uploads: it says nothing uploads only when that switch worked.
func TestLogoutWithoutKeystore(t *testing.T) {
	tests := []struct {
		name       string
		config     string
		wantMode   config.Mode
		wantStderr string
	}{
		{name: "with config.toml back in local mode it says nothing uploads", config: "mode = \"online\"\n",
			wantMode: config.Local, wantStderr: "pairtime is back in local mode, so nothing uploads"},
		{name: "with a config.toml it couldn't switch it says the mode is still online", config: "mode = \"online\"\nnote = \"\"\"\nkept\n\"\"\"\n",
			wantMode: config.Online, wantStderr: "The mode is still online, so uploads start again once the keystore opens"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv(home.EnvHome, dir)
			files := map[string]string{
				account.FileName: `{"v":1,"server":"https://pairtime.example.org","device_id":"d_e2elogin4nhv5xgjw6ycpbz2r","public_key":"","linked_at":"2026-11-20T00:00:00Z"}`,
				// A keystore.json that doesn't parse, so the keystore won't open.
				filepath.Join("keys", "keystore.json"): "{not json",
				"config.toml":                          tt.config,
			}
			for name, data := range files {
				path := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var out, errOut bytes.Buffer
			code := run([]string{"logout"}, &out, &errOut)
			if code != 1 || !strings.Contains(errOut.String(), tt.wantStderr) {
				t.Errorf("pairtime logout exited %d\nstdout:\n%s\nstderr:\n%s\nwant %q", code, out.String(), errOut.String(), tt.wantStderr)
			}
			if cfg, _, _ := config.Load(filepath.Join(dir, "config.toml")); cfg.Mode != tt.wantMode {
				t.Errorf("config.toml's mode is %q, want %q", cfg.Mode, tt.wantMode)
			}
			if _, err := account.Load(dir); err != nil {
				t.Errorf("account.json after the logout: %v; want it kept, so a later logout can tell the server", err)
			}
		})
	}
}

// syncBuffer collects what a command writes from another goroutine.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// runLoginApproving runs `pairtime login --server <srv> --no-browser`,
// and when it prints its code, approves it in a browser. A failed approval
// cancels the login, which would otherwise wait for the code to expire.
func runLoginApproving(srv *httptest.Server, cancel context.CancelFunc) (stdout, stderr string, code int, approveErr error) {
	pr, pw := io.Pipe()
	var errOut syncBuffer
	exit := make(chan int, 1)
	go func() {
		c := run([]string{"login", "--server", srv.URL, "--no-browser"}, pw, &errOut)
		pw.Close()
		exit <- c
	}()
	approved := make(chan error, 1)
	var out strings.Builder
	started := false
	sc := bufio.NewScanner(pr)
	for sc.Scan() {
		line := sc.Text()
		out.WriteString(line + "\n")
		if m := userCode.FindString(line); m != "" && !started {
			started = true
			go func() {
				err := approve(srv, m)
				if err != nil {
					cancel()
				}
				approved <- err
			}()
		}
	}
	io.Copy(io.Discard, pr)
	code = <-exit
	if started {
		approveErr = <-approved
	} else {
		approveErr = errors.New("pairtime login printed no code")
	}
	return out.String(), errOut.String(), code, approveErr
}

// csrfField finds the CSRF token in a page's form.
var csrfField = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

// approve signs a new browser in through the server's OIDC sign-in, opens
// the code's page and chooses Link it.
func approve(srv *httptest.Server, code string) error {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	c := &http.Client{
		Transport:     srv.Client().Transport,
		Jar:           jar,
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	get := func(u string) (int, string, string, error) {
		resp, err := c.Get(u)
		if err != nil {
			return 0, "", "", err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("Location"), string(b), err
	}
	// The login page sends the browser to the OIDC server, and that back
	// to the callback, which signs it in.
	status, loc, body, err := get(srv.URL + auth.LoginPath + "?return_to=" + url.QueryEscape(device.PagePath))
	for i := 0; err == nil && status == http.StatusFound && i < 2; i++ {
		status, loc, body, err = get(loc)
	}
	if err != nil {
		return err
	}
	if status != http.StatusSeeOther || loc != device.PagePath {
		return fmt.Errorf("signing in ended with %d, Location %q:\n%s", status, loc, body)
	}
	status, _, body, err = get(srv.URL + device.PagePath + "?user_code=" + url.QueryEscape(code))
	if err != nil {
		return err
	}
	m := csrfField.FindStringSubmatch(body)
	if status != http.StatusOK || m == nil || !strings.Contains(body, code) {
		return fmt.Errorf("the page for code %s answered %d:\n%s", code, status, body)
	}
	form := url.Values{"csrf": {m[1]}, "user_code": {code}, "decision": {"approve"}}
	req, err := http.NewRequest(http.MethodPost, srv.URL+device.DecisionPath, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", srv.URL)
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), "<h1>Linked</h1>") {
		return fmt.Errorf("approving answered %d:\n%s", resp.StatusCode, b)
	}
	return nil
}

// startPairtimed runs pairtimed's sign-in, pages and device service on a
// TLS test server over a migrated SQLite database, as serve wires them.
func startPairtimed(t *testing.T, p oidcProvider) (*httptest.Server, *device.Store) {
	t.Helper()
	ctx := context.Background()
	db := storetest.Open(t, store.SQLite)
	ms, err := migrations.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(ctx, db, ms, time.Now); err != nil {
		t.Fatal(err)
	}
	w := writer.Start(db.Write, writer.Options{Window: time.Millisecond})
	t.Cleanup(w.Close)
	emails, err := accounts.NewEmailCipher(bytes.Repeat([]byte{3}, accounts.EmailKeySize))
	if err != nil {
		t.Fatal(err)
	}
	acc, err := accounts.New(accounts.Options{DB: db, Writer: w, Emails: emails, MayCreate: func(string) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(nil)
	base := "https://" + srv.Listener.Addr().String()
	signIn, err := auth.New(auth.Config{BaseURL: base, Issuer: p.issuer, ClientID: p.clientID, ClientSecret: p.secret, Accounts: acc})
	if err != nil {
		t.Fatal(err)
	}
	if p.register != nil {
		p.register(signIn.RedirectURI())
	}
	policy, err := retention.Default()
	if err != nil {
		t.Fatal(err)
	}
	site, err := pages.New(pages.Options{BaseURL: base, Retention: policy, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	devices, err := device.New(device.Options{DB: db, Writer: w})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := device.NewService(device.Config{Store: devices, BaseURL: base, SignIn: signIn, Site: site})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	site.Register(mux)
	signIn.Register(mux)
	svc.Register(mux)
	srv.Config.Handler = mux
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, devices
}
