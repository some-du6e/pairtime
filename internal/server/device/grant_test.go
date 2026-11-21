// SPDX-License-Identifier: AGPL-3.0-or-later

package device_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/devtoken"
	"github.com/some-du6e/pairtime/internal/server/auth"
	"github.com/some-du6e/pairtime/internal/server/device"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
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
				{name: "an approved code gets a ptd_ token for the app's own device id and the key it sent, once", run: func(t *testing.T, h *harness) {
					b := h.signIn(verified("ident!teen1", "U0TEEN1"))
					c := h.requestCode(testDeviceID)
					if !strings.HasSuffix(c.VerificationURI, device.PagePath) || c.VerificationURIComplete != c.VerificationURI+"?user_code="+url.QueryEscape(c.UserCode) ||
						c.ExpiresIn != 900 || c.Interval != 5 || len(c.UserCode) != 9 || c.UserCode[4] != '-' || c.SignupsOpen == nil || !*c.SignupsOpen {
						t.Errorf("code reply = %+v", c)
					}
					if r := b.decide(c.UserCode, true); !strings.Contains(r.body, "<h1>Linked</h1>") {
						t.Fatalf("approving answered %d:\n%s", r.status, r.body)
					}
					status, tr := h.poll(c.DeviceCode)
					if status != http.StatusOK || tr.TokenType != "Bearer" || tr.DeviceID != testDeviceID || tr.Scope != device.Scope || tr.Account != "U0TEEN1" ||
						tr.PublicKey != publicKey(c.key) {
						t.Fatalf("token reply %d: %+v", status, tr)
					}
					if err := devtoken.Check(tr.AccessToken); err != nil {
						t.Errorf("the token %q fails its own check: %v", tr.AccessToken, err)
					}
					d, err := h.store.Authenticate(context.Background(), tr.AccessToken)
					if err != nil || d.ID != testDeviceID || d.OS != device.OSLinux || d.ClientBuild != "v0.1.0" || d.Revoked ||
						!d.PublicKey.Equal(c.key.Public().(ed25519.PublicKey)) || d.Generation != 1 {
						t.Errorf("Authenticate = %+v, %v; want the device at link generation 1 with the code's key", d, err)
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
				{name: "an approved code the app doesn't redeem within a minute of its expiry gets expired_token", run: func(t *testing.T, h *harness) {
					b := h.signIn(verified("ident!teen1", "U0TEEN1"))
					c := h.requestCode(testDeviceID)
					b.decide(c.UserCode, true)
					h.clock.Advance(device.CodeLifetime + device.RedeemGrace)
					if _, tr := h.poll(c.DeviceCode); tr.Error != "expired_token" {
						t.Errorf("poll = %q, want expired_token", tr.Error)
					}
					if n := h.count(`SELECT count(*) FROM devices`); n != 0 {
						t.Errorf("an expired code linked %d devices", n)
					}
				}},
				{name: "a code approved in its last seconds is still redeemed by the app's next poll after it expires", run: func(t *testing.T, h *harness) {
					b := h.signIn(verified("ident!teen1", "U0TEEN1"))
					c := h.requestCode(testDeviceID)
					h.clock.Advance(device.CodeLifetime - 2*time.Second)
					if r := b.decide(c.UserCode, true); !strings.Contains(r.body, "<h1>Linked</h1>") {
						t.Fatalf("approving 2 s before the code expires answered %d:\n%s", r.status, r.body)
					}
					h.clock.Advance(device.Interval)
					if status, tr := h.poll(c.DeviceCode); status != http.StatusOK || tr.AccessToken == "" {
						t.Errorf("the poll 3 s after the code expired answered %d %+v, want the token", status, tr)
					}
				}},
				{name: "a pending code expires on time, whatever the grace of an approved one", run: func(t *testing.T, h *harness) {
					c := h.requestCode(testDeviceID)
					h.clock.Advance(device.CodeLifetime + time.Second)
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
				{name: "a clock stepped back after the server started doesn't end the codes it makes", run: func(t *testing.T, h *harness) {
					h.clock.Advance(-time.Hour)
					c := h.requestCode(testDeviceID)
					code, _ := device.NormalizeUserCode(c.UserCode)
					if _, err := h.store.Lookup(context.Background(), code); err != nil {
						t.Errorf("Lookup after the clock stepped back = %v", err)
					}
					if _, tr := h.poll(c.DeviceCode); tr.Error != "authorization_pending" {
						t.Errorf("poll = %q, want authorization_pending", tr.Error)
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
				{name: "a device another account linked stays with it: the page says so, Stop ends the code, and the app gets device_taken", run: func(t *testing.T, h *harness) {
					a := h.signIn(verified("ident!teen1", "U0TEEN1"))
					owned := h.link(a, testDeviceID)
					b := h.signIn(verified("ident!teen2", "U0TEEN2"))
					c := h.requestCode(testDeviceID)
					tok := b.csrf(device.PagePath)
					r := b.post(device.PagePath, url.Values{"csrf": {tok}, "user_code": {c.UserCode}})
					if !strings.Contains(r.body, "linked to another account") || strings.Contains(r.body, ">Link it</button>") || !strings.Contains(r.body, ">Stop pairtime login</button>") {
						t.Errorf("typing the code of another account's device answered %d:\n%s", r.status, r.body)
					}
					if _, tr := h.poll(c.DeviceCode); tr.Error != "authorization_pending" {
						t.Errorf("poll after the page = %q; want authorization_pending, since only Stop ends the code", tr.Error)
					}
					h.clock.Advance(device.Interval)
					if r := b.post(device.DecisionPath, url.Values{"csrf": {tok}, "user_code": {c.UserCode}, "decision": {"approve"}}); !strings.Contains(r.body, "linked to another account") || strings.Contains(r.body, "<h1>Linked</h1>") {
						t.Errorf("Stop answered %d:\n%s", r.status, r.body)
					}
					if status, tr := h.poll(c.DeviceCode); status != http.StatusBadRequest || tr.Error != "device_taken" {
						t.Errorf("poll after Stop = %d %q, want device_taken", status, tr.Error)
					}
					if r := h.call(http.MethodGet, device.MePath, owned.AccessToken, nil); r.status != http.StatusOK {
						t.Errorf("the owner's token answered %d after another account tried its device", r.status)
					}
				}},
				{name: "a removed device that uploaded stays with its account as a tombstone, and another account gets device_taken", run: func(t *testing.T, h *harness) {
					a := h.signIn(verified("ident!teen1", "U0TEEN1"))
					tok := h.link(a, testDeviceID).AccessToken
					h.ingest(tok)
					if r := h.call(http.MethodDelete, device.MePath, tok, nil); r.status != http.StatusNoContent {
						t.Fatalf("DELETE %s answered %d", device.MePath, r.status)
					}
					if n := h.count(`SELECT count(*) FROM devices WHERE device_id = $1 AND removed_at_ms IS NOT NULL AND token_hash IS NULL`, testDeviceID); n != 1 {
						t.Errorf("%d tombstones without a token, want 1", n)
					}
					b := h.signIn(verified("ident!teen2", "U0TEEN2"))
					c := h.requestCode(testDeviceID)
					if r := b.decide(c.UserCode, true); !strings.Contains(r.body, "linked to another account") {
						t.Errorf("another account's try answered %d:\n%s", r.status, r.body)
					}
					tokB := b.csrf(device.PagePath)
					b.post(device.DecisionPath, url.Values{"csrf": {tokB}, "user_code": {c.UserCode}, "decision": {"approve"}})
					if _, tr := h.poll(c.DeviceCode); tr.Error != "device_taken" {
						t.Errorf("poll = %q, want device_taken", tr.Error)
					}
				}},
				{name: "a device removed before it uploaded anything frees its id, and another account links it as a new device", run: func(t *testing.T, h *harness) {
					a := h.signIn(verified("ident!teen1", "U0TEEN1"))
					tok := h.link(a, testDeviceID).AccessToken
					if r := h.call(http.MethodDelete, device.MePath, tok, nil); r.status != http.StatusNoContent {
						t.Fatalf("DELETE %s answered %d", device.MePath, r.status)
					}
					if n := h.count(`SELECT count(*) FROM devices`); n != 0 {
						t.Errorf("the removal left %d device rows, want 0", n)
					}
					b := h.signIn(verified("ident!teen2", "U0TEEN2"))
					second := h.link(b, testDeviceID)
					d, err := h.store.Authenticate(context.Background(), second.AccessToken)
					if err != nil || second.Account != "U0TEEN2" || d.Generation != 1 {
						t.Errorf("the second link = %+v, Authenticate %+v, %v; want a new device of the second account", second, d, err)
					}
				}},
				{name: "the account that removed a device can link it again: a new token and key at the next link generation, and ingest's state stays", run: func(t *testing.T, h *harness) {
					b := h.signIn(verified("ident!teen1", "U0TEEN1"))
					first := h.link(b, testDeviceID)
					st := h.ingest(first.AccessToken)
					if r := h.call(http.MethodDelete, device.MePath, first.AccessToken, nil); r.status != http.StatusNoContent {
						t.Fatalf("DELETE %s answered %d", device.MePath, r.status)
					}
					key := newKey(t)
					second := h.linkWith(b, testDeviceID, key)
					if second.AccessToken == first.AccessToken || second.PublicKey != publicKey(key) {
						t.Fatalf("linking again handed out %+v", second)
					}
					if r := h.call(http.MethodGet, device.MePath, first.AccessToken, nil); r.status != http.StatusUnauthorized {
						t.Errorf("the old token answered %d, want 401", r.status)
					}
					if r := h.call(http.MethodGet, device.MePath, second.AccessToken, nil); r.status != http.StatusOK || !strings.Contains(r.body, `"public_key":"`+publicKey(key)+`"`) {
						t.Errorf("the new token's device answered %d: %s", r.status, r.body)
					}
					ctx := context.Background()
					d, err := h.store.Authenticate(ctx, second.AccessToken)
					if err != nil || d.Generation != 2 || !d.RemovedAt.IsZero() {
						t.Fatalf("Authenticate = %+v, %v; want link generation 2, not removed", d, err)
					}
					var got device.State
					if err := h.w.Do(ctx, 1, func(ctx context.Context, tx writer.Tx) error {
						var err error
						got, err = h.store.LoadState(ctx, tx, d)
						return err
					}); err != nil {
						t.Fatal(err)
					}
					if got.Generation != 2 || got.HeadGeneration != 1 || got.Req != st.Req || got.HeadB != st.HeadB || got.HeadHash != st.HeadHash {
						t.Errorf("LoadState = %+v; want the state from before the removal, its head under generation 1 of 2", got)
					}
				}},
				{name: "a code for a device the account has linked now says since when, and approving it signs that copy out", run: func(t *testing.T, h *harness) {
					b := h.signIn(verified("ident!teen1", "U0TEEN1"))
					first := h.link(b, testDeviceID)
					h.clock.Advance(time.Hour)
					c := h.requestCode(testDeviceID)
					tok := b.csrf(device.PagePath)
					r := b.post(device.PagePath, url.Values{"csrf": {tok}, "user_code": {c.UserCode}})
					if !strings.Contains(r.body, "linked already") || !strings.Contains(r.body, "since ") || !strings.Contains(r.body, "signs that copy of pairtime out") ||
						!strings.Contains(r.body, `value="approve"`) || !strings.Contains(r.body, testDeviceID) {
						t.Errorf("the code of a linked device answered %d:\n%s", r.status, r.body)
					}
					if r := b.post(device.DecisionPath, url.Values{"csrf": {tok}, "user_code": {c.UserCode}, "decision": {"approve"}}); !strings.Contains(r.body, "<h1>Linked</h1>") {
						t.Fatalf("approving answered %d:\n%s", r.status, r.body)
					}
					status, second := h.poll(c.DeviceCode)
					if status != http.StatusOK {
						t.Fatalf("poll = %d %+v", status, second)
					}
					if r := h.call(http.MethodGet, device.MePath, first.AccessToken, nil); r.status != http.StatusUnauthorized {
						t.Errorf("the signed-out copy's token answered %d, want 401", r.status)
					}
					if d, err := h.store.Authenticate(context.Background(), second.AccessToken); err != nil || d.Generation != 2 {
						t.Errorf("Authenticate = %+v, %v; want link generation 2", d, err)
					}
				}},
				{name: "the same answer twice, as from a double click, gets the first one's page and costs no try", run: func(t *testing.T, h *harness) {
					b := h.signIn(verified("ident!teen1", "U0TEEN1"))
					approved, denied := h.requestCode(testDeviceID), h.requestCode("d_otherdevice000000000000aa")
					tok := b.csrf(device.PagePath)
					for i := range 12 {
						if r := b.post(device.DecisionPath, url.Values{"csrf": {tok}, "user_code": {approved.UserCode}, "decision": {"approve"}}); !strings.Contains(r.body, "<h1>Linked</h1>") {
							t.Fatalf("approval %d answered %d:\n%s", i+1, r.status, r.body)
						}
					}
					if r := b.post(device.DecisionPath, url.Values{"csrf": {tok}, "user_code": {approved.UserCode}, "decision": {"deny"}}); strings.Contains(r.body, "Not linked") {
						t.Errorf("a denial after the approval answered %d:\n%s", r.status, r.body)
					}
					for i := range 2 {
						if r := b.post(device.DecisionPath, url.Values{"csrf": {tok}, "user_code": {denied.UserCode}, "decision": {"deny"}}); !strings.Contains(r.body, "<h1>Not linked</h1>") {
							t.Errorf("denial %d answered %d:\n%s", i+1, r.status, r.body)
						}
					}
					if status, tr := h.poll(approved.DeviceCode); status != http.StatusOK || tr.AccessToken == "" {
						t.Errorf("poll = %d %+v, want the token", status, tr)
					}
					if r := b.post(device.DecisionPath, url.Values{"csrf": {tok}, "user_code": {approved.UserCode}, "decision": {"approve"}}); !strings.Contains(r.body, "<h1>Linked</h1>") {
						t.Errorf("an approval after the app redeemed the code answered %d:\n%s", r.status, r.body)
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
				{name: "wrong codes sent at once still get at most 10 tries", run: func(t *testing.T, h *harness) {
					b := h.signIn(verified("ident!teen1", "U0TEEN1"))
					form := url.Values{"csrf": {b.csrf(device.PagePath)}, "user_code": {"DDDD-DDDD"}, "decision": {"approve"}}.Encode()
					statuses := make(chan int, 30)
					var wg sync.WaitGroup
					for range 30 {
						wg.Go(func() {
							req, err := http.NewRequest(http.MethodPost, h.srv.URL+device.DecisionPath, strings.NewReader(form))
							if err != nil {
								statuses <- 0
								return
							}
							req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
							req.Header.Set("Origin", h.srv.URL)
							resp, err := b.c.Do(req)
							if err != nil {
								statuses <- 0
								return
							}
							io.Copy(io.Discard, resp.Body)
							resp.Body.Close()
							statuses <- resp.StatusCode
						})
					}
					wg.Wait()
					close(statuses)
					through, failed := 0, 0
					for st := range statuses {
						switch st {
						case http.StatusTooManyRequests:
						case http.StatusOK:
							through++
						default:
							failed++
						}
					}
					if through > 10 || failed > 0 {
						t.Errorf("of 30 wrong codes sent at once, %d were tried and %d failed otherwise; want at most 10 tried", through, failed)
					}
				}},
				{name: "after 10 wrong codes an account gets no more tries, even with the right code, and signing in again doesn't reset it", run: func(t *testing.T, h *harness) {
					b := h.signIn(verified("ident!teen1", "U0TEEN1"))
					c := h.requestCode(testDeviceID)
					tok := b.csrf(device.PagePath)
					for i := range 5 {
						if r := b.post(device.PagePath, url.Values{"csrf": {tok}, "user_code": {"BBBB-BBBB"}}); r.status != http.StatusOK {
							t.Fatalf("wrong code %d answered %d", i+1, r.status)
						}
					}
					// Codes posted straight to the decision count too.
					for i := range 5 {
						if r := b.post(device.DecisionPath, url.Values{"csrf": {tok}, "user_code": {"CCCC-CCCC"}, "decision": {"approve"}}); r.status != http.StatusOK {
							t.Fatalf("wrong decision %d answered %d", i+1, r.status)
						}
					}
					again := h.signIn(verified("ident!teen1", "U0TEEN1"))
					againTok := again.csrf(device.PagePath)
					r := again.post(device.PagePath, url.Values{"csrf": {againTok}, "user_code": {c.UserCode}})
					if r.status != http.StatusTooManyRequests || strings.Contains(r.body, `value="approve"`) {
						t.Errorf("the right code after 10 wrong ones, from a new sign-in, answered %d:\n%s", r.status, r.body)
					}
					if r := again.post(device.DecisionPath, url.Values{"csrf": {againTok}, "user_code": {c.UserCode}, "decision": {"approve"}}); r.status != http.StatusTooManyRequests {
						t.Errorf("a decision after 10 wrong codes answered %d, want 429", r.status)
					}
					if _, tr := h.poll(c.DeviceCode); tr.Error != "authorization_pending" {
						t.Errorf("poll = %q, want authorization_pending", tr.Error)
					}
					other := h.signIn(verified("ident!teen2", "U0TEEN2"))
					if r := other.decide(c.UserCode, false); !strings.Contains(r.body, "<h1>Not linked</h1>") {
						t.Errorf("another account's try answered %d:\n%s", r.status, r.body)
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

// TestCodeRequest checks what POST /oauth/device/code refuses, and what
// it says about sign-ups.
func TestCodeRequest(t *testing.T) {
	h := newHarness(t, harnessConfig{engine: storetest.Engines()[0]})
	off := newHarness(t, harnessConfig{engine: storetest.Engines()[0], signInOff: true})
	closed := newHarness(t, harnessConfig{engine: storetest.Engines()[0], signupsClosed: true})
	key, other := newKey(t), newKey(t)
	with := func(edit func(f url.Values)) url.Values {
		f := codeForm(testDeviceID, key)
		edit(f)
		return f
	}
	signed := func(id string, priv ed25519.PrivateKey, pub string) string {
		return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, device.KeyMessage(id, pub)))
	}
	tests := []struct {
		name       string
		h          *harness
		form       url.Values
		raw        string // a body sent as is instead of form
		wantStatus int
		wantError  string
		wantBody   string // in the body, when set
	}{
		{name: "a request with every field gets a code, and says sign-ups are open", h: h, form: codeForm(testDeviceID, key), wantStatus: http.StatusOK, wantBody: `"signups_open":true`},
		{name: "while sign-ups are closed the code says so, since only an account that exists can approve it", h: closed, form: codeForm(testDeviceID, key), wantStatus: http.StatusOK, wantBody: `"signups_open":false`},
		{name: "scope ingest read:self is accepted", h: h, form: with(func(f url.Values) { f.Set("scope", "ingest read:self") }), wantStatus: http.StatusOK},
		{name: "another client_id is invalid_client", h: h, form: with(func(f url.Values) { f.Set("client_id", "wakatime") }), wantStatus: http.StatusBadRequest, wantError: "invalid_client"},
		{name: "a missing client_id is invalid_client", h: h, form: with(func(f url.Values) { f.Del("client_id") }), wantStatus: http.StatusBadRequest, wantError: "invalid_client"},
		{name: "a scope a device token lacks is invalid_scope", h: h, form: with(func(f url.Values) { f.Set("scope", "ingest admin") }), wantStatus: http.StatusBadRequest, wantError: "invalid_scope"},
		{name: "a missing device_id is invalid_request", h: h, form: with(func(f url.Values) { f.Del("device_id") }), wantStatus: http.StatusBadRequest, wantError: "invalid_request"},
		{name: "a device_id shorter than 16 characters is invalid_request", h: h, form: with(func(f url.Values) { f.Set("device_id", "d_short") }), wantStatus: http.StatusBadRequest, wantError: "invalid_request"},
		{name: "a device_id that looks like a path is invalid_request", h: h, form: with(func(f url.Values) { f.Set("device_id", "/home/teen/project/one") }), wantStatus: http.StatusBadRequest, wantError: "invalid_request"},
		{name: "an os outside the four families is invalid_request", h: h, form: with(func(f url.Values) { f.Set("os", "Windows 11 Home") }), wantStatus: http.StatusBadRequest, wantError: "invalid_request"},
		{name: "a client_build with a space is invalid_request", h: h, form: with(func(f url.Values) { f.Set("client_build", "v0.1.0 (devel)") }), wantStatus: http.StatusBadRequest, wantError: "invalid_request"},
		{name: "a request without the device key is invalid_request, since a token never gets a key later", h: h, form: with(func(f url.Values) { f.Del("public_key"); f.Del("key_signature") }), wantStatus: http.StatusBadRequest, wantError: "invalid_request"},
		{name: "a public_key that isn't 32 bytes is invalid_request", h: h, form: with(func(f url.Values) {
			short := base64.StdEncoding.EncodeToString(make([]byte, 31))
			f.Set("public_key", short)
			f.Set("key_signature", signed(testDeviceID, key, short))
		}), wantStatus: http.StatusBadRequest, wantError: "invalid_request"},
		{name: "a public_key in unpadded base64 is invalid_request", h: h, form: with(func(f url.Values) { f.Set("public_key", strings.TrimRight(publicKey(key), "=")) }), wantStatus: http.StatusBadRequest, wantError: "invalid_request"},
		{name: "a key_signature by another key is invalid_request, since the app must hold the key it names", h: h, form: with(func(f url.Values) { f.Set("key_signature", signed(testDeviceID, other, publicKey(key))) }), wantStatus: http.StatusBadRequest, wantError: "invalid_request"},
		{name: "a key_signature for another device id is invalid_request", h: h, form: with(func(f url.Values) { f.Set("key_signature", signed("d_someotherdevice00000000aa", key, publicKey(key))) }), wantStatus: http.StatusBadRequest, wantError: "invalid_request"},
		{name: "a parameter given twice is invalid_request", h: h, raw: codeForm(testDeviceID, key).Encode() + "&device_id=" + testDeviceID, wantStatus: http.StatusBadRequest, wantError: "invalid_request"},
		{name: "a body that isn't a form is invalid_request", h: h, raw: `{"client_id":"pairtime"}`, wantStatus: http.StatusBadRequest, wantError: "invalid_request"},
		{name: "with sign-in off the app hears at once that nobody can approve a code", h: off, form: codeForm(testDeviceID, key), wantStatus: http.StatusServiceUnavailable, wantError: "temporarily_unavailable"},
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
			if r.status != tt.wantStatus || (tt.wantError != "" && !strings.Contains(r.body, `"error":"`+tt.wantError+`"`)) || !strings.Contains(r.body, tt.wantBody) {
				t.Errorf("answered %d: %s; want %d %s %s", r.status, r.body, tt.wantStatus, tt.wantError, tt.wantBody)
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
