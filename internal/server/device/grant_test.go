// SPDX-License-Identifier: AGPL-3.0-or-later

package device_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/devtoken"
	"github.com/some-du6e/pairtime/internal/server/auth"
	"github.com/some-du6e/pairtime/internal/server/device"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
)

// TestGrant is RFC 8628 3.4 and 3.5 as the app sees them, with a person
// answering on /device.
func TestGrant(t *testing.T) {
	for _, engine := range storetest.Engines() {
		t.Run(string(engine), func(t *testing.T) {
			tests := []struct {
				name string
				run  func(t *testing.T, h *harness)
			}{
				{name: "an approved code gets a ptd_ token for the app's own device id, once", run: func(t *testing.T, h *harness) {
					b := h.signIn(verified("ident!teen1", "U0TEEN1"))
					c := h.requestCode(testDeviceID)
					if !strings.HasSuffix(c.VerificationURI, device.PagePath) || c.VerificationURIComplete != c.VerificationURI+"?user_code="+url.QueryEscape(c.UserCode) ||
						c.ExpiresIn != 900 || c.Interval != 5 || len(c.UserCode) != 9 || c.UserCode[4] != '-' {
						t.Errorf("code reply = %+v", c)
					}
					if r := b.decide(c.UserCode, true); !strings.Contains(r.body, "<h1>Linked</h1>") {
						t.Fatalf("approving answered %d:\n%s", r.status, r.body)
					}
					status, tr := h.poll(c.DeviceCode)
					if status != http.StatusOK || tr.TokenType != "Bearer" || tr.DeviceID != testDeviceID || tr.Scope != device.Scope {
						t.Fatalf("token reply %d: %+v", status, tr)
					}
					if err := devtoken.Check(tr.AccessToken); err != nil {
						t.Errorf("the token %q fails its own check: %v", tr.AccessToken, err)
					}
					d, err := h.store.Authenticate(context.Background(), tr.AccessToken)
					if err != nil || d.ID != testDeviceID || d.OS != device.OSLinux || d.ClientBuild != "v0.1.0" || d.Revoked || d.PublicKey != nil {
						t.Errorf("Authenticate = %+v, %v", d, err)
					}
					if status, tr := h.poll(c.DeviceCode); status != http.StatusBadRequest || tr.Error != "invalid_grant" {
						t.Errorf("a second poll after the token answered %d %q, want invalid_grant", status, tr.Error)
					}
					if n := h.count(`SELECT count(*) FROM devices WHERE token_hash = $1`, tr.AccessToken); n != 0 {
						t.Error("the database holds the token itself, not only its hash")
					}
				}},
				{name: "a poll sooner than the interval gets slow_down, and each slow_down adds 5 s to the interval", run: func(t *testing.T, h *harness) {
					c := h.requestCode(testDeviceID)
					want := []struct {
						after time.Duration
						error string
					}{
						{0, "authorization_pending"},
						{0, "slow_down"},                            // now 10 s
						{5 * time.Second, "slow_down"},              // too soon for 10 s; now 15 s
						{15 * time.Second, "authorization_pending"}, // waited the 15 s
						{14 * time.Second, "slow_down"},             // 1 s early; now 20 s
						{20 * time.Second, "authorization_pending"},
					}
					for i, w := range want {
						h.clock.Advance(w.after)
						if status, tr := h.poll(c.DeviceCode); status != http.StatusBadRequest || tr.Error != w.error {
							t.Fatalf("poll %d, %v after the last: %d %q, want %q", i+1, w.after, status, tr.Error, w.error)
						}
					}
				}},
				{name: "a poll a little early within the slack isn't slowed down", run: func(t *testing.T, h *harness) {
					c := h.requestCode(testDeviceID)
					h.poll(c.DeviceCode)
					h.clock.Advance(device.Interval - device.PollSlack/2)
					if _, tr := h.poll(c.DeviceCode); tr.Error != "authorization_pending" {
						t.Errorf("a poll %v early got %q, want authorization_pending", device.PollSlack/2, tr.Error)
					}
				}},
				{name: "a code nobody answers within 15 minutes gets expired_token, and the page no longer finds it", run: func(t *testing.T, h *harness) {
					b := h.signIn(verified("ident!teen1", "U0TEEN1"))
					c := h.requestCode(testDeviceID)
					h.clock.Advance(device.CodeLifetime)
					if status, tr := h.poll(c.DeviceCode); status != http.StatusBadRequest || tr.Error != "expired_token" {
						t.Errorf("poll after 15 minutes: %d %q, want expired_token", status, tr.Error)
					}
					if r := b.decide(c.UserCode, true); !strings.Contains(r.body, "or it has expired") {
						t.Errorf("typing an expired code answered %d:\n%s", r.status, r.body)
					}
				}},
				{name: "an approved code the app doesn't redeem within 15 minutes gets expired_token", run: func(t *testing.T, h *harness) {
					b := h.signIn(verified("ident!teen1", "U0TEEN1"))
					c := h.requestCode(testDeviceID)
					b.decide(c.UserCode, true)
					h.clock.Advance(device.CodeLifetime)
					if _, tr := h.poll(c.DeviceCode); tr.Error != "expired_token" {
						t.Errorf("poll = %q, want expired_token", tr.Error)
					}
				}},
				{name: "a denied code gets access_denied, and links nothing", run: func(t *testing.T, h *harness) {
					b := h.signIn(verified("ident!teen1", "U0TEEN1"))
					c := h.requestCode(testDeviceID)
					if r := b.decide(c.UserCode, false); !strings.Contains(r.body, "<h1>Not linked</h1>") {
						t.Fatalf("denying answered %d:\n%s", r.status, r.body)
					}
					if status, tr := h.poll(c.DeviceCode); status != http.StatusBadRequest || tr.Error != "access_denied" {
						t.Errorf("poll = %d %q, want access_denied", status, tr.Error)
					}
					if n := h.count(`SELECT count(*) FROM devices`); n != 0 {
						t.Errorf("a denied code made %d device rows", n)
					}
				}},
				{name: "an unknown device code gets invalid_grant", run: func(t *testing.T, h *harness) {
					if status, tr := h.poll("not-a-device-code"); status != http.StatusBadRequest || tr.Error != "invalid_grant" {
						t.Errorf("poll = %d %q, want invalid_grant", status, tr.Error)
					}
				}},
				{name: "a code made before a restart gets expired_token, since its user code's key is gone", run: func(t *testing.T, h *harness) {
					c := h.requestCode(testDeviceID)
					h.clock.Advance(time.Second)
					restarted, err := device.New(device.Options{DB: h.db, Writer: h.w, Now: h.clock.Now})
					if err != nil {
						t.Fatal(err)
					}
					if _, _, err := restarted.Poll(context.Background(), c.DeviceCode); !errors.Is(err, device.ErrExpired) {
						t.Errorf("Poll after a restart = %v, want ErrExpired", err)
					}
					code, _ := device.NormalizeUserCode(c.UserCode)
					if _, err := restarted.Lookup(context.Background(), code); !errors.Is(err, device.ErrNoCode) {
						t.Errorf("Lookup after a restart = %v, want ErrNoCode", err)
					}
				}},
				{name: "approving needs a signed-in session: without one the browser is sent to sign in, and the code stays pending", run: func(t *testing.T, h *harness) {
					c := h.requestCode(testDeviceID)
					anon := h.anonymous()
					r := h.postForm(anon, device.DecisionPath, url.Values{"user_code": {c.UserCode}, "decision": {"approve"}}, http.Header{"Origin": {h.srv.URL}})
					if r.status != http.StatusSeeOther || !strings.HasPrefix(r.header.Get("Location"), auth.LoginPath+"?return_to=") {
						t.Errorf("a signed-out approval answered %d, Location %q; want a 303 to sign-in", r.status, r.header.Get("Location"))
					}
					if _, tr := h.poll(c.DeviceCode); tr.Error != "authorization_pending" {
						t.Errorf("poll = %q, want authorization_pending", tr.Error)
					}
				}},
				{name: "approving needs the session's CSRF token", run: func(t *testing.T, h *harness) {
					b := h.signIn(verified("ident!teen1", "U0TEEN1"))
					c := h.requestCode(testDeviceID)
					for _, tok := range []string{"", "x" + b.csrf(device.PagePath)} {
						r := b.post(device.DecisionPath, url.Values{"csrf": {tok}, "user_code": {c.UserCode}, "decision": {"approve"}})
						if r.status != http.StatusForbidden {
							t.Errorf("an approval with CSRF token %q answered %d, want 403", tok, r.status)
						}
					}
					other := h.signIn(verified("ident!teen2", "U0TEEN2"))
					r := b.post(device.DecisionPath, url.Values{"csrf": {other.csrf(device.PagePath)}, "user_code": {c.UserCode}, "decision": {"approve"}})
					if r.status != http.StatusForbidden {
						t.Errorf("an approval with another session's CSRF token answered %d, want 403", r.status)
					}
					if _, tr := h.poll(c.DeviceCode); tr.Error != "authorization_pending" {
						t.Errorf("poll = %q, want authorization_pending", tr.Error)
					}
				}},
				{name: "approving needs the server's own Origin", run: func(t *testing.T, h *harness) {
					b := h.signIn(verified("ident!teen1", "U0TEEN1"))
					c := h.requestCode(testDeviceID)
					form := url.Values{"csrf": {b.csrf(device.PagePath)}, "user_code": {c.UserCode}, "decision": {"approve"}}
					for _, hdr := range []http.Header{{"Origin": {"https://evil.example"}}, {"Origin": {"null"}}, {"Sec-Fetch-Site": {"cross-site"}}} {
						if r := h.postForm(b.c, device.DecisionPath, form, hdr); r.status != http.StatusForbidden {
							t.Errorf("an approval with %v answered %d, want 403", hdr, r.status)
						}
					}
					if _, tr := h.poll(c.DeviceCode); tr.Error != "authorization_pending" {
						t.Errorf("poll = %q, want authorization_pending", tr.Error)
					}
				}},
				{name: "a device linked to one account can't be linked to another", run: func(t *testing.T, h *harness) {
					a := h.signIn(verified("ident!teen1", "U0TEEN1"))
					h.link(a, testDeviceID)
					b := h.signIn(verified("ident!teen2", "U0TEEN2"))
					c := h.requestCode(testDeviceID)
					tok := b.csrf(device.PagePath)
					if r := b.post(device.PagePath, url.Values{"csrf": {tok}, "user_code": {c.UserCode}}); !strings.Contains(r.body, "linked to another account") || strings.Contains(r.body, `value="approve"`) {
						t.Errorf("typing the code of another account's device answered %d:\n%s", r.status, r.body)
					}
					if r := b.post(device.DecisionPath, url.Values{"csrf": {tok}, "user_code": {c.UserCode}, "decision": {"approve"}}); !strings.Contains(r.body, "linked to another account") {
						t.Errorf("approving it anyway answered %d:\n%s", r.status, r.body)
					}
					if _, tr := h.poll(c.DeviceCode); tr.Error != "authorization_pending" {
						t.Errorf("poll = %q, want authorization_pending", tr.Error)
					}
				}},
				{name: "the account that removed a device can link it again: the new token works, the old one doesn't, and the key is forgotten", run: func(t *testing.T, h *harness) {
					b := h.signIn(verified("ident!teen1", "U0TEEN1"))
					first := h.link(b, testDeviceID)
					if r := h.call(http.MethodPost, device.KeyPath, first.AccessToken, registration(t, testDeviceID, newKey(t))); r.status != http.StatusCreated {
						t.Fatalf("registering a key answered %d: %s", r.status, r.body)
					}
					if r := h.call(http.MethodDelete, device.MePath, first.AccessToken, nil); r.status != http.StatusNoContent {
						t.Fatalf("DELETE %s answered %d", device.MePath, r.status)
					}
					second := h.link(b, testDeviceID)
					if second.AccessToken == first.AccessToken {
						t.Fatal("linking again handed out the same token")
					}
					if r := h.call(http.MethodGet, device.MePath, first.AccessToken, nil); r.status != http.StatusUnauthorized {
						t.Errorf("the old token answered %d, want 401", r.status)
					}
					r := h.call(http.MethodGet, device.MePath, second.AccessToken, nil)
					if r.status != http.StatusOK || !strings.Contains(r.body, `"public_key":null`) {
						t.Errorf("the new token's device answered %d: %s", r.status, r.body)
					}
					if r := h.call(http.MethodPost, device.KeyPath, second.AccessToken, registration(t, testDeviceID, newKey(t))); r.status != http.StatusCreated {
						t.Errorf("registering the new login's key answered %d: %s", r.status, r.body)
					}
				}},
				{name: "signed out, /device sends the browser through sign-in and back with the code", run: func(t *testing.T, h *harness) {
					anon := h.anonymous()
					req, _ := http.NewRequest(http.MethodGet, h.srv.URL+device.PagePath+"?user_code=wdjb+mjht", nil)
					r := do(t, anon, req)
					want := auth.LoginPath + "?return_to=" + url.QueryEscape(device.PagePath+"?user_code=WDJB-MJHT")
					if r.status != http.StatusSeeOther || r.header.Get("Location") != want {
						t.Errorf("GET /device signed out answered %d, Location %q; want 303 to %q", r.status, r.header.Get("Location"), want)
					}
				}},
				{name: "verification_uri_complete shows the confirmation with the code at once", run: func(t *testing.T, h *harness) {
					b := h.signIn(verified("ident!teen1", "U0TEEN1"))
					c := h.requestCode(testDeviceID)
					u, _ := url.Parse(c.VerificationURIComplete)
					r := b.get(u.RequestURI())
					if r.status != http.StatusOK || !strings.Contains(r.body, "<code>"+c.UserCode+"</code>") || !strings.Contains(r.body, `value="approve"`) || !strings.Contains(r.body, "Linux") {
						t.Errorf("GET %s answered %d:\n%s", u.RequestURI(), r.status, r.body)
					}
					if csp := r.header.Get("Content-Security-Policy"); !strings.Contains(csp, "form-action 'self'") || !strings.Contains(csp, "default-src 'none'") {
						t.Errorf("the page's CSP is %q", csp)
					}
					if rp := r.header.Get("Referrer-Policy"); rp != "same-origin" {
						t.Errorf("Referrer-Policy = %q, want same-origin, so the form's POST carries this server's Origin", rp)
					}
				}},
				{name: "after 10 wrong codes a session gets no more tries, even with the right code", run: func(t *testing.T, h *harness) {
					b := h.signIn(verified("ident!teen1", "U0TEEN1"))
					c := h.requestCode(testDeviceID)
					tok := b.csrf(device.PagePath)
					for i := range 10 {
						if r := b.post(device.PagePath, url.Values{"csrf": {tok}, "user_code": {"BBBB-BBBB"}}); r.status != http.StatusOK {
							t.Fatalf("wrong code %d answered %d", i+1, r.status)
						}
					}
					r := b.post(device.PagePath, url.Values{"csrf": {tok}, "user_code": {c.UserCode}})
					if r.status != http.StatusTooManyRequests || strings.Contains(r.body, `value="approve"`) {
						t.Errorf("the right code after 10 wrong ones answered %d:\n%s", r.status, r.body)
					}
					other := h.signIn(verified("ident!teen2", "U0TEEN2"))
					if r := other.decide(c.UserCode, false); !strings.Contains(r.body, "<h1>Not linked</h1>") {
						t.Errorf("another session's try answered %d:\n%s", r.status, r.body)
					}
				}},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					tt.run(t, newHarness(t, harnessConfig{engine: engine}))
				})
			}
		})
	}
}

// TestCodeRequest checks what POST /oauth/device/code refuses.
func TestCodeRequest(t *testing.T) {
	h := newHarness(t, harnessConfig{engine: storetest.Engines()[0]})
	off := newHarness(t, harnessConfig{engine: storetest.Engines()[0], signInOff: true})
	with := func(edit func(f url.Values)) url.Values {
		f := codeForm(testDeviceID)
		edit(f)
		return f
	}
	tests := []struct {
		name       string
		h          *harness
		form       url.Values
		raw        string // a body sent as is instead of form
		wantStatus int
		wantError  string
	}{
		{name: "a request with every field gets a code", h: h, form: codeForm(testDeviceID), wantStatus: http.StatusOK},
		{name: "scope ingest read:self is accepted", h: h, form: with(func(f url.Values) { f.Set("scope", "ingest read:self") }), wantStatus: http.StatusOK},
		{name: "another client_id is invalid_client", h: h, form: with(func(f url.Values) { f.Set("client_id", "wakatime") }), wantStatus: http.StatusBadRequest, wantError: "invalid_client"},
		{name: "a missing client_id is invalid_client", h: h, form: with(func(f url.Values) { f.Del("client_id") }), wantStatus: http.StatusBadRequest, wantError: "invalid_client"},
		{name: "a scope a device token lacks is invalid_scope", h: h, form: with(func(f url.Values) { f.Set("scope", "ingest admin") }), wantStatus: http.StatusBadRequest, wantError: "invalid_scope"},
		{name: "a missing device_id is invalid_request", h: h, form: with(func(f url.Values) { f.Del("device_id") }), wantStatus: http.StatusBadRequest, wantError: "invalid_request"},
		{name: "a device_id shorter than 16 characters is invalid_request", h: h, form: with(func(f url.Values) { f.Set("device_id", "d_short") }), wantStatus: http.StatusBadRequest, wantError: "invalid_request"},
		{name: "a device_id that looks like a path is invalid_request", h: h, form: with(func(f url.Values) { f.Set("device_id", "/home/teen/project/one") }), wantStatus: http.StatusBadRequest, wantError: "invalid_request"},
		{name: "an os outside the four families is invalid_request", h: h, form: with(func(f url.Values) { f.Set("os", "Windows 11 Home") }), wantStatus: http.StatusBadRequest, wantError: "invalid_request"},
		{name: "a client_build with a space is invalid_request", h: h, form: with(func(f url.Values) { f.Set("client_build", "v0.1.0 (devel)") }), wantStatus: http.StatusBadRequest, wantError: "invalid_request"},
		{name: "a parameter given twice is invalid_request", h: h, raw: codeForm(testDeviceID).Encode() + "&device_id=" + testDeviceID, wantStatus: http.StatusBadRequest, wantError: "invalid_request"},
		{name: "a body that isn't a form is invalid_request", h: h, raw: `{"client_id":"pairtime"}`, wantStatus: http.StatusBadRequest, wantError: "invalid_request"},
		{name: "with sign-in off the app hears at once that nobody can approve a code", h: off, form: codeForm(testDeviceID), wantStatus: http.StatusServiceUnavailable, wantError: "temporarily_unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r response
			switch {
			case tt.raw != "" && strings.HasPrefix(tt.raw, "{"):
				req, _ := http.NewRequest(http.MethodPost, tt.h.srv.URL+device.CodePath, strings.NewReader(tt.raw))
				req.Header.Set("Content-Type", "application/json")
				r = do(t, tt.h.app, req)
			case tt.raw != "":
				req, _ := http.NewRequest(http.MethodPost, tt.h.srv.URL+device.CodePath, strings.NewReader(tt.raw))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				r = do(t, tt.h.app, req)
			default:
				r = tt.h.postForm(tt.h.app, device.CodePath, tt.form, nil)
			}
			if r.status != tt.wantStatus || (tt.wantError != "" && !strings.Contains(r.body, `"error":"`+tt.wantError+`"`)) {
				t.Errorf("answered %d: %s; want %d %s", r.status, r.body, tt.wantStatus, tt.wantError)
			}
			if cc := r.header.Get("Cache-Control"); cc != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", cc)
			}
		})
	}
}

// TestTokenRequest checks what POST /oauth/token refuses before it looks
// at the code.
func TestTokenRequest(t *testing.T) {
	h := newHarness(t, harnessConfig{engine: storetest.Engines()[0]})
	c := h.requestCode(testDeviceID)
	tests := []struct {
		name      string
		form      url.Values
		wantError string
	}{
		{name: "another grant type is unsupported_grant_type", form: url.Values{"grant_type": {"authorization_code"}, "device_code": {c.DeviceCode}, "client_id": {device.ClientID}}, wantError: "unsupported_grant_type"},
		{name: "a missing grant type is invalid_request", form: url.Values{"device_code": {c.DeviceCode}, "client_id": {device.ClientID}}, wantError: "invalid_request"},
		{name: "another client_id is invalid_client", form: url.Values{"grant_type": {device.GrantType}, "device_code": {c.DeviceCode}, "client_id": {"other"}}, wantError: "invalid_client"},
		{name: "a missing device code is invalid_request", form: url.Values{"grant_type": {device.GrantType}, "client_id": {device.ClientID}}, wantError: "invalid_request"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := h.postForm(h.app, device.TokenPath, tt.form, nil)
			if r.status != http.StatusBadRequest || !strings.Contains(r.body, `"error":"`+tt.wantError+`"`) {
				t.Errorf("answered %d: %s; want 400 %s", r.status, r.body, tt.wantError)
			}
		})
	}
	t.Run("a device code sent in the URL instead of the body is ignored", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, h.srv.URL+device.TokenPath+"?device_code="+url.QueryEscape(c.DeviceCode), strings.NewReader(url.Values{"grant_type": {device.GrantType}, "client_id": {device.ClientID}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if r := do(t, h.app, req); !strings.Contains(r.body, `"error":"invalid_request"`) {
			t.Errorf("answered %d: %s; want invalid_request", r.status, r.body)
		}
	})
}

// TestSignInOff is the live server until its HCA app exists: the pages
// say linking isn't open, and nothing sends a browser to a sign-in that
// can't work.
func TestSignInOff(t *testing.T) {
	h := newHarness(t, harnessConfig{engine: storetest.Engines()[0], signInOff: true})
	for _, path := range []string{device.PagePath, device.PagePath + "?user_code=WDJB-MJHT", device.DevicesPath} {
		t.Run("GET "+path+" says linking isn't open yet", func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, h.srv.URL+path, nil)
			r := do(t, h.app, req)
			if r.status != http.StatusOK || !strings.Contains(r.body, "Linking isn't open yet") || strings.Contains(r.body, "<form") {
				t.Errorf("answered %d:\n%s", r.status, r.body)
			}
		})
	}
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
