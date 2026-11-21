// SPDX-License-Identifier: AGPL-3.0-or-later

package auth_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/accounts"
	"github.com/some-du6e/pairtime/internal/server/auth"
	"github.com/some-du6e/pairtime/internal/server/config"
	"github.com/some-du6e/pairtime/internal/server/migrations"
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

// harness is pairtimed's sign-in on a TLS test server, with a migrated
// database and a client that acts as the browser.
type harness struct {
	t      *testing.T
	srv    *httptest.Server
	svc    *auth.Service
	acc    *accounts.Store
	db     *store.DB
	w      *writer.Writer
	client *http.Client
	logs   *syncBuffer
}

type harnessConfig struct {
	engine       store.Engine
	issuer       string
	clientID     string
	clientSecret string
	// mayCreate is accounts.Options.MayCreate: nil closes sign-ups, and
	// anyone opens them.
	mayCreate func(sub string) bool
	reviewers []config.Reviewer
	// now is the sign-in service's clock; nil means time.Now.
	now func() time.Time
	// register gets the redirect URI before the server starts.
	register func(redirectURI string)
}

var emailKey = bytes.Repeat([]byte{9}, accounts.EmailKeySize)

// anyone is MayCreate with sign-ups open to every identity.
func anyone(string) bool { return true }

func newHarness(t *testing.T, c harnessConfig) *harness {
	t.Helper()
	ctx := context.Background()
	db := storetest.Open(t, c.engine)
	ms, err := migrations.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(ctx, db, ms, time.Now); err != nil {
		t.Fatal(err)
	}
	w := writer.Start(db.Write, writer.Options{Window: time.Millisecond})
	t.Cleanup(w.Close)
	emails, err := accounts.NewEmailCipher(emailKey)
	if err != nil {
		t.Fatal(err)
	}
	acc, err := accounts.New(accounts.Options{
		DB:        db,
		Writer:    w,
		Emails:    emails,
		Reviewers: c.reviewers,
		MayCreate: c.mayCreate,
	})
	if err != nil {
		t.Fatal(err)
	}

	// The listener exists before the server starts, so the base URL, and
	// with it the redirect URI, is known first.
	srv := httptest.NewUnstartedServer(nil)
	base := "https://" + srv.Listener.Addr().String()
	logs := &syncBuffer{}
	svc, err := auth.New(auth.Config{
		BaseURL:      base,
		Issuer:       c.issuer,
		ClientID:     c.clientID,
		ClientSecret: c.clientSecret,
		Accounts:     acc,
		Logger:       slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Now:          c.now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.register != nil {
		c.register(svc.RedirectURI())
	}
	mux := http.NewServeMux()
	svc.Register(mux)
	srv.Config.Handler = mux
	srv.StartTLS()
	t.Cleanup(srv.Close)
	if srv.URL != base {
		t.Fatalf("the server's URL is %s, not the %s sign-in was built with", srv.URL, base)
	}

	client := srv.Client()
	client.Jar = newJar(t)
	client.Timeout = 30 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &harness{t: t, srv: srv, svc: svc, acc: acc, db: db, w: w, client: client, logs: logs}
}

func newJar(t *testing.T) http.CookieJar {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return jar
}

// response is a finished response, its body read.
type response struct {
	status  int
	header  http.Header
	body    string
	cookies []*http.Cookie
}

func (h *harness) do(req *http.Request) response {
	h.t.Helper()
	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		h.t.Fatal(err)
	}
	return response{status: resp.StatusCode, header: resp.Header, body: string(b), cookies: resp.Cookies()}
}

func (h *harness) get(u string) response {
	h.t.Helper()
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	return h.do(req)
}

func (h *harness) postForm(u string, form url.Values, header http.Header) response {
	h.t.Helper()
	req, err := http.NewRequest(http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		h.t.Fatal(err)
	}
	for k, vs := range header {
		req.Header[k] = vs
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return h.do(req)
}

func (h *harness) location(r response) *url.URL {
	h.t.Helper()
	u, err := url.Parse(r.header.Get("Location"))
	if err != nil || r.header.Get("Location") == "" {
		h.t.Fatalf("response %d has no usable Location; body:\n%s", r.status, r.body)
	}
	return u
}

// flow is one sign-in as a browser saw it.
type flow struct {
	// loginCookie is the value of the login cookie the login page set.
	loginCookie string
	// authorize is where the login page sent the browser.
	authorize *url.URL
	// callback is where HCA sent it back.
	callback *url.URL
	// prompted is set when HCA showed a login form instead of answering
	// at once.
	prompted bool
	// resp is the callback's response.
	resp response
}

type signInOptions struct {
	// query is the login page's query, such as "?fresh=1".
	query string
	// editAuthorize changes the authorization request before the browser
	// follows it.
	editAuthorize func(*url.URL)
	// edit changes the callback URL before the browser follows it.
	edit func(*url.URL)
	// otherBrowser follows the callback without the login cookie.
	otherBrowser bool
	// loginClaims, when set, is posted as the claims field of the login
	// form mock-oauth2-server shows for prompt=login, which adds those
	// claims to its ID token. It is called as the form is submitted, the
	// moment the person authenticates.
	loginClaims func() string
}

// signIn runs a sign-in as a browser would: the login page, HCA's
// authorization endpoint, and the callback.
func (h *harness) signIn(o signInOptions) *flow {
	h.t.Helper()
	f := h.startSignIn(o)
	if o.edit != nil {
		o.edit(f.callback)
	}
	if o.otherBrowser {
		h.client.Jar = newJar(h.t)
	}
	f.resp = h.get(f.callback.String())
	return f
}

// startSignIn runs a sign-in up to HCA's answer: the login page and HCA's
// authorization endpoint, but not the callback HCA sends the browser to.
// When the authorization endpoint shows a login form, as
// mock-oauth2-server does for prompt=login, it posts a username, and the
// loginClaims option, to it.
func (h *harness) startSignIn(o signInOptions) *flow {
	h.t.Helper()
	f := &flow{}
	r := h.get(h.srv.URL + auth.LoginPath + o.query)
	if r.status != http.StatusFound {
		h.t.Fatalf("the login page answered %d, want 302; body:\n%s", r.status, r.body)
	}
	if c := cookie(r, loginCookieName); c != nil {
		f.loginCookie = c.Value
	}
	f.authorize = h.location(r)
	if o.editAuthorize != nil {
		o.editAuthorize(f.authorize)
	}
	r = h.get(f.authorize.String())
	if r.status == http.StatusOK {
		f.prompted = true
		form := url.Values{"username": {"pairtime-test"}}
		if o.loginClaims != nil {
			form.Set("claims", o.loginClaims())
		}
		r = h.postForm(f.authorize.String(), form, nil)
	}
	if r.status != http.StatusFound {
		h.t.Fatalf("HCA's authorization endpoint answered %d, want 302; body:\n%s", r.status, r.body)
	}
	f.callback = h.location(r)
	return f
}

// loginCookieName is the name of auth's login cookie.
const loginCookieName = "__Host-pairtime_hca_login"

// loginSecret is the secret a login cookie's value holds, read from its
// JSON part.
func loginSecret(t *testing.T, value string) string {
	t.Helper()
	body, _, _ := strings.Cut(value, ".")
	b, err := base64.RawURLEncoding.DecodeString(body)
	var st struct {
		Secret string `json:"v"`
	}
	if err != nil || json.Unmarshal(b, &st) != nil || st.Secret == "" {
		t.Fatal("the login cookie's value doesn't decode to a state with a secret")
	}
	return st.Secret
}

// me fetches GET /api/v1/users/me.
func (h *harness) me() (response, auth.Me) {
	h.t.Helper()
	r := h.get(h.srv.URL + auth.MePath)
	var m auth.Me
	if r.status == http.StatusOK {
		if err := json.Unmarshal([]byte(r.body), &m); err != nil {
			h.t.Fatalf("%s isn't JSON: %v\n%s", auth.MePath, err, r.body)
		}
	}
	return r, m
}

// count runs a count(*) query on the harness's database.
func (h *harness) count(query string, args ...any) int {
	h.t.Helper()
	var n int
	if err := h.db.Read.QueryRow(query, args...).Scan(&n); err != nil {
		h.t.Fatalf("%s: %v", query, err)
	}
	return n
}

// cookie returns the named cookie a response set, or nil.
func cookie(r response, name string) *http.Cookie {
	for _, c := range r.cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// errorCode is the code an error page shows.
func errorCode(r response) string {
	_, after, ok := strings.Cut(r.body, "<code>")
	if !ok {
		return ""
	}
	code, _, _ := strings.Cut(after, "</code>")
	return code
}

// checkRefused checks that a sign-in was refused with code and left the
// browser signed out.
func checkRefused(t *testing.T, h *harness, f *flow, status int, code string) {
	t.Helper()
	if f.resp.status != status || errorCode(f.resp) != code {
		t.Fatalf("the callback answered %d with code %q, want %d with %q; body:\n%s\nlog:\n%s",
			f.resp.status, errorCode(f.resp), status, code, f.resp.body, h.logs.String())
	}
	if c := cookie(f.resp, auth.SessionCookie); c != nil && c.MaxAge > 0 {
		t.Error("a refused sign-in set a session cookie")
	}
	if r, _ := h.me(); r.status != http.StatusUnauthorized {
		t.Errorf("%s answered %d after a refused sign-in, want 401", auth.MePath, r.status)
	}
	if !strings.Contains(h.logs.String(), "code="+code) {
		t.Errorf("the log doesn't record code=%s:\n%s", code, h.logs.String())
	}
	// The page offers sign-in again only where that can help, and always
	// links the home page (#119).
	retry := strings.Contains(f.resp.body, `href="`+auth.LoginPath+`"`)
	switch code {
	case auth.CodeSignupsClosed, auth.CodeIneligible:
		if retry {
			t.Errorf("the %s page offers to sign in again, which would only show it again", code)
		}
	default:
		if !retry {
			t.Errorf("the %s page doesn't offer to sign in again", code)
		}
	}
	if !strings.Contains(f.resp.body, `<a href="/">`) {
		t.Errorf("the %s page doesn't link the home page", code)
	}
	if status == http.StatusUnauthorized && f.resp.header.Get("WWW-Authenticate") == "" {
		t.Errorf("the %s page is a 401 without WWW-Authenticate (RFC 9110 15.5.2)", code)
	}
}

// checkSignedIn checks that a sign-in succeeded and that the session
// cookie has the attributes it must, and returns the account as
// /api/v1/users/me shows it.
func checkSignedIn(t *testing.T, h *harness, f *flow, wantLocation string) auth.Me {
	t.Helper()
	if f.resp.status != http.StatusSeeOther {
		t.Fatalf("the callback answered %d with code %q, want 303; body:\n%s\nlog:\n%s",
			f.resp.status, errorCode(f.resp), f.resp.body, h.logs.String())
	}
	if got := f.resp.header.Get("Location"); got != wantLocation {
		t.Errorf("the callback redirected to %q, want %q", got, wantLocation)
	}
	c := cookie(f.resp, auth.SessionCookie)
	switch {
	case c == nil:
		t.Fatal("the callback set no session cookie")
	case !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode:
		t.Errorf("session cookie Secure = %v, HttpOnly = %v, SameSite = %v; want Secure, HttpOnly and SameSite=Lax", c.Secure, c.HttpOnly, c.SameSite)
	case c.Path != "/" || c.Domain != "" || !strings.HasPrefix(c.Name, "__Host-"):
		t.Errorf("session cookie %s has Path %q and Domain %q; a __Host- cookie needs Path=/ and no Domain", c.Name, c.Path, c.Domain)
	case c.MaxAge <= 0:
		t.Errorf("session cookie Max-Age = %d, want a lifetime", c.MaxAge)
	}
	if lc := cookie(f.resp, loginCookieName); lc == nil || lc.MaxAge >= 0 {
		t.Error("the callback didn't delete the login cookie")
	}
	r, m := h.me()
	if r.status != http.StatusOK {
		t.Fatalf("%s answered %d after signing in; body: %s", auth.MePath, r.status, r.body)
	}
	return m
}
