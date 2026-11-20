// SPDX-License-Identifier: AGPL-3.0-or-later

package device_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// syncBuffer collects log lines from the server's goroutines.
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

// clock is the device store's clock: real time, moved on by Advance, so a
// test can let a code expire or an interval pass.
type clock struct{ offset atomic.Int64 }

func (c *clock) Now() time.Time          { return time.Now().Add(time.Duration(c.offset.Load())) }
func (c *clock) Advance(d time.Duration) { c.offset.Add(int64(d)) }

// harness is pairtimed's sign-in, pages and device service on a TLS test
// server, over a migrated database.
type harness struct {
	t     *testing.T
	srv   *httptest.Server
	hca   *hcafake.Server // nil when sign-in is off
	store *device.Store
	db    *store.DB
	w     *writer.Writer
	clock *clock
	logs  *syncBuffer
	// app is the pairtime app's client: no cookies, no redirects.
	app *http.Client
}

type harnessConfig struct {
	engine store.Engine
	// signInOff builds the server without HCA sign-in, as the live server
	// runs until its HCA app exists.
	signInOff bool
}

var emailKey = bytes.Repeat([]byte{7}, accounts.EmailKeySize)

func newHarness(t *testing.T, c harnessConfig) *harness {
	t.Helper()
	db := openDB(t, c.engine)
	w := writer.Start(db.Write, writer.Options{Window: time.Millisecond})
	t.Cleanup(w.Close)
	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	srv := httptest.NewUnstartedServer(nil)
	base := "https://" + srv.Listener.Addr().String()
	h := &harness{t: t, srv: srv, db: db, w: w, clock: &clock{}, logs: logs}

	signIn := newSignIn(t, h, base, db, w, logger, c.signInOff)
	policy, err := retention.Default()
	if err != nil {
		t.Fatal(err)
	}
	site, err := pages.New(pages.Options{BaseURL: base, Retention: policy, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	h.store, err = device.New(device.Options{DB: db, Writer: w, Now: h.clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := device.NewService(device.Config{Store: h.store, BaseURL: base, SignIn: signIn, Site: site, Logger: logger})
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
	if srv.URL != base {
		t.Fatalf("the server's URL is %s, not %s", srv.URL, base)
	}
	h.app = h.newClient(nil)
	return h
}

// newClient returns a client of its own for the test server, with jar,
// that follows no redirects. srv.Client returns one shared client, so a
// jar set on it would give every browser the same cookies.
func (h *harness) newClient(jar http.CookieJar) *http.Client {
	return &http.Client{
		Transport:     h.srv.Client().Transport,
		Jar:           jar,
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// anonymous returns a browser that isn't signed in.
func (h *harness) anonymous() *http.Client {
	h.t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		h.t.Fatal(err)
	}
	return h.newClient(jar)
}

// newSignIn returns HCA sign-in through a fresh fake HCA, which it puts in
// h.hca, or with off, sign-in that isn't set up.
func newSignIn(t *testing.T, h *harness, base string, db *store.DB, w *writer.Writer, logger *slog.Logger, off bool) *auth.Service {
	t.Helper()
	if off {
		svc, err := auth.New(auth.Config{BaseURL: base, Logger: logger})
		if err != nil {
			t.Fatal(err)
		}
		return svc
	}
	h.hca = hcafake.New(t)
	emails, err := accounts.NewEmailCipher(emailKey)
	if err != nil {
		t.Fatal(err)
	}
	acc, err := accounts.New(accounts.Options{DB: db, Writer: w, Emails: emails, MayCreate: func(string) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := auth.New(auth.Config{
		BaseURL:      base,
		Issuer:       h.hca.URL,
		ClientID:     hcafake.ClientID,
		ClientSecret: hcafake.ClientSecret,
		Accounts:     acc,
		Logger:       logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.hca.AddRedirectURI(svc.RedirectURI())
	return svc
}

// openDB returns a migrated database on engine.
func openDB(t *testing.T, engine store.Engine) *store.DB {
	t.Helper()
	db := storetest.Open(t, engine)
	ms, err := migrations.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(context.Background(), db, ms, time.Now); err != nil {
		t.Fatal(err)
	}
	return db
}

// response is a finished response, its body read.
type response struct {
	status int
	header http.Header
	body   string
}

func do(t *testing.T, c *http.Client, req *http.Request) response {
	t.Helper()
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response{status: resp.StatusCode, header: resp.Header, body: string(b)}
}

// postForm posts form to path on the server with c. header adds headers,
// such as the Origin a browser sends.
func (h *harness) postForm(c *http.Client, path string, form url.Values, header http.Header) response {
	h.t.Helper()
	req, err := http.NewRequest(http.MethodPost, h.srv.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		h.t.Fatal(err)
	}
	for k, vs := range header {
		req.Header[k] = vs
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return do(h.t, c, req)
}

// call sends method to path with the device token tok and body as JSON
// when it isn't nil.
func (h *harness) call(method, path, tok string, body any) response {
	h.t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			h.t.Fatal(err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, h.srv.URL+path, r)
	if err != nil {
		h.t.Fatal(err)
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return do(h.t, h.app, req)
}

// codeReply is the device authorization response.
type codeReply struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// tokenReply is a token response, success or error.
type tokenReply struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	DeviceID    string `json:"device_id"`
	Error       string `json:"error"`
}

// testDeviceID is the device id of a test app, shaped like the ledger
// writer's.
const testDeviceID = "d_7lq3k2m4nhv5xgjw6ycpbz2rsa"

// codeForm is a device authorization request for id.
func codeForm(id string) url.Values {
	return url.Values{"client_id": {device.ClientID}, "device_id": {id}, "os": {device.OSLinux}, "client_build": {"v0.1.0"}}
}

// requestCode asks for a code for the device id, as the app does.
func (h *harness) requestCode(id string) codeReply {
	h.t.Helper()
	r := h.postForm(h.app, device.CodePath, codeForm(id), nil)
	if r.status != http.StatusOK {
		h.t.Fatalf("POST %s answered %d: %s", device.CodePath, r.status, r.body)
	}
	var c codeReply
	if err := json.Unmarshal([]byte(r.body), &c); err != nil {
		h.t.Fatal(err)
	}
	return c
}

// poll asks for the token once, as the app does.
func (h *harness) poll(deviceCode string) (int, tokenReply) {
	h.t.Helper()
	r := h.postForm(h.app, device.TokenPath, url.Values{
		"grant_type":  {device.GrantType},
		"device_code": {deviceCode},
		"client_id":   {device.ClientID},
	}, nil)
	var tr tokenReply
	if err := json.Unmarshal([]byte(r.body), &tr); err != nil {
		h.t.Fatalf("POST %s answered %d with a body that isn't JSON: %s", device.TokenPath, r.status, r.body)
	}
	return r.status, tr
}

// browser is a signed-in browser.
type browser struct {
	h *harness
	c *http.Client
}

// signIn signs a new browser in through the fake HCA as id.
func (h *harness) signIn(id hcafake.Identity) *browser {
	h.t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		h.t.Fatal(err)
	}
	c := h.newClient(jar)
	h.hca.SetIdentity(id)
	b := &browser{h: h, c: c}
	// The login page sends the browser to HCA, and HCA back to the
	// callback, both by absolute URLs; the callback then sets the session
	// cookie and sends it home.
	r := b.get(auth.LoginPath)
	for range 2 {
		if r.status != http.StatusFound {
			break
		}
		req, err := http.NewRequest(http.MethodGet, r.header.Get("Location"), nil)
		if err != nil {
			h.t.Fatal(err)
		}
		r = do(h.t, c, req)
	}
	if !b.signedIn() {
		h.t.Fatalf("the browser didn't sign in; last answer %d: %s\nlog:\n%s", r.status, r.body, h.logs.String())
	}
	return b
}

// signedIn reports whether the browser has a session.
func (b *browser) signedIn() bool {
	return b.get(auth.MePath).status == http.StatusOK
}

func (b *browser) get(path string) response {
	b.h.t.Helper()
	req, err := http.NewRequest(http.MethodGet, b.h.srv.URL+path, nil)
	if err != nil {
		b.h.t.Fatal(err)
	}
	return do(b.h.t, b.c, req)
}

// post posts form from one of the server's own pages: with its Origin.
func (b *browser) post(path string, form url.Values) response {
	b.h.t.Helper()
	return b.h.postForm(b.c, path, form, http.Header{"Origin": {b.h.srv.URL}})
}

// csrfField finds the CSRF token in a page's form.
var csrfField = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

// csrf returns the CSRF token of the page at path.
func (b *browser) csrf(path string) string {
	b.h.t.Helper()
	r := b.get(path)
	m := csrfField.FindStringSubmatch(r.body)
	if m == nil {
		b.h.t.Fatalf("GET %s answered %d with no CSRF field:\n%s", path, r.status, r.body)
	}
	return m[1]
}

// decide types code on /device and answers it, as a person would.
func (b *browser) decide(code string, approve bool) response {
	b.h.t.Helper()
	tok := b.csrf(device.PagePath)
	r := b.post(device.PagePath, url.Values{"csrf": {tok}, "user_code": {code}})
	if r.status != http.StatusOK || !strings.Contains(r.body, `name="decision"`) {
		return r
	}
	decision := "deny"
	if approve {
		decision = "approve"
	}
	return b.post(device.DecisionPath, url.Values{"csrf": {tok}, "user_code": {code}, "decision": {decision}})
}

// link runs the whole grant for the device id with the signed-in browser
// b, and returns the token.
func (h *harness) link(b *browser, id string) tokenReply {
	h.t.Helper()
	c := h.requestCode(id)
	if r := b.decide(c.UserCode, true); r.status != http.StatusOK || !strings.Contains(r.body, "<h1>Linked</h1>") {
		h.t.Fatalf("approving answered %d:\n%s", r.status, r.body)
	}
	status, tr := h.poll(c.DeviceCode)
	if status != http.StatusOK || tr.AccessToken == "" {
		h.t.Fatalf("the poll after approval answered %d: %+v", status, tr)
	}
	return tr
}

// verified is an identity HCA verified.
func verified(sub, slack string) hcafake.Identity {
	yes := true
	return hcafake.Identity{Sub: sub, Email: slack + "@example.org", SlackID: slack, VerificationStatus: "verified", YSWSEligible: &yes}
}
