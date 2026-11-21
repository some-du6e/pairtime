// SPDX-License-Identifier: AGPL-3.0-or-later

package device_test

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/devtoken"
	"github.com/some-du6e/pairtime/internal/server/device"
	"github.com/some-du6e/pairtime/internal/server/retention"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// TestDeviceAPI is the device's own API, /api/v1/devices/me, and
// /account/devices.
func TestDeviceAPI(t *testing.T) {
	for _, engine := range storetest.Engines() {
		t.Run(string(engine), func(t *testing.T) {
			tests := []struct {
				name string
				run  func(t *testing.T, h *harness, b *browser, linked tokenReply, key ed25519.PrivateKey)
			}{
				{name: "GET /api/v1/devices/me describes the device, with the key its code named", run: func(t *testing.T, h *harness, _ *browser, linked tokenReply, key ed25519.PrivateKey) {
					r := h.call(http.MethodGet, device.MePath, linked.AccessToken, nil)
					for _, want := range []string{`"device_id":"` + testDeviceID + `"`, `"os":"linux"`, `"client_build":"v0.1.0"`, `"public_key":"` + publicKey(key) + `"`, `"linked_at":"`, `"last_seen_at":"`} {
						if r.status != http.StatusOK || !strings.Contains(r.body, want) {
							t.Errorf("GET %s answered %d: %s; want %s", device.MePath, r.status, r.body, want)
						}
					}
				}},
				{name: "a token alone can't set or swap the device's key: the registration endpoint is gone", run: func(t *testing.T, h *harness, _ *browser, linked tokenReply, key ed25519.PrivateKey) {
					other := newKey(t)
					r := h.call(http.MethodPost, device.MePath+"/key", linked.AccessToken, map[string]string{"public_key": publicKey(other), "signature": "AAAA"})
					if r.status != http.StatusNotFound && r.status != http.StatusMethodNotAllowed {
						t.Errorf("POST %s/key answered %d: %s; want 404 or 405", device.MePath, r.status, r.body)
					}
					if d, err := h.store.Authenticate(context.Background(), linked.AccessToken); err != nil || !d.PublicKey.Equal(key.Public().(ed25519.PublicKey)) {
						t.Errorf("Authenticate = %+v, %v; want the key the code named", d, err)
					}
				}},
				{name: "a request with a token that isn't valid gets 401 invalid_token, and one with none a challenge without an error (RFC 6750 3.1)", run: func(t *testing.T, h *harness, _ *browser, linked tokenReply, _ ed25519.PrivateKey) {
					for name, c := range map[string]struct{ token, challenge string }{
						"no token":          {"", `Bearer realm="pairtime"`},
						"a corrupted token": {corrupt(linked.AccessToken), `Bearer realm="pairtime", error="invalid_token"`},
						"an unknown token":  {devtoken.New(), `Bearer realm="pairtime", error="invalid_token"`},
					} {
						r := h.call(http.MethodGet, device.MePath, c.token, nil)
						if r.status != http.StatusUnauthorized || r.header.Get("WWW-Authenticate") != c.challenge {
							t.Errorf("%s answered %d, WWW-Authenticate %q; want 401 and %q", name, r.status, r.header.Get("WWW-Authenticate"), c.challenge)
						}
					}
				}},
				{name: "the account page lists the device, and Remove stops its token at once and deletes a device that never uploaded", run: func(t *testing.T, h *harness, b *browser, linked tokenReply, _ ed25519.PrivateKey) {
					tok := linked.AccessToken
					r := b.get(device.DevicesPath)
					if r.status != http.StatusOK || !strings.Contains(r.body, testDeviceID) || !strings.Contains(r.body, "Linux, pairtime v0.1.0") || !strings.Contains(r.body, ">Remove</button>") {
						t.Fatalf("GET %s answered %d:\n%s", device.DevicesPath, r.status, r.body)
					}
					if r := b.post(device.RemovePath, url.Values{"csrf": {"wrong"}, "device_id": {testDeviceID}}); r.status != http.StatusForbidden {
						t.Errorf("Remove without the CSRF token answered %d, want 403", r.status)
					}
					if r := h.call(http.MethodGet, device.MePath, tok, nil); r.status != http.StatusOK {
						t.Fatalf("the token stopped working before Remove: %d", r.status)
					}
					r = b.post(device.RemovePath, url.Values{"csrf": {b.csrf(device.DevicesPath)}, "device_id": {testDeviceID}})
					if r.status != http.StatusSeeOther || r.header.Get("Location") != device.DevicesPath {
						t.Fatalf("Remove answered %d, Location %q", r.status, r.header.Get("Location"))
					}
					if r := h.call(http.MethodGet, device.MePath, tok, nil); r.status != http.StatusUnauthorized {
						t.Errorf("a removed device's token answered %d, want 401", r.status)
					}
					if _, err := h.store.Authenticate(context.Background(), tok); !errors.Is(err, device.ErrUnknownToken) {
						t.Errorf("Authenticate after Remove = %v, want ErrUnknownToken", err)
					}
					if n := h.count(`SELECT count(*) FROM devices`); n != 0 {
						t.Errorf("Remove left %d device rows of a device that never uploaded", n)
					}
					if r := b.get(device.DevicesPath); strings.Contains(r.body, testDeviceID) || !strings.Contains(r.body, "No computer is linked yet.") {
						t.Errorf("the page after Remove:\n%s", r.body)
					}
				}},
				{name: "Remove keeps a device that uploaded as a tombstone, which the page lists as removed", run: func(t *testing.T, h *harness, b *browser, linked tokenReply, _ ed25519.PrivateKey) {
					h.ingest(linked.AccessToken)
					if r := b.post(device.RemovePath, url.Values{"csrf": {b.csrf(device.DevicesPath)}, "device_id": {testDeviceID}}); r.status != http.StatusSeeOther {
						t.Fatalf("Remove answered %d", r.status)
					}
					if r := h.call(http.MethodGet, device.MePath, linked.AccessToken, nil); r.status != http.StatusUnauthorized {
						t.Errorf("a removed device's token answered %d, want 401", r.status)
					}
					if n := h.count(`SELECT count(*) FROM devices WHERE removed_at_ms IS NOT NULL AND token_hash IS NULL AND ingest_req IS NOT NULL`); n != 1 {
						t.Errorf("%d tombstones keeping ingest's state and no token, want 1", n)
					}
					if r := b.get(device.DevicesPath); !strings.Contains(r.body, "Removed ") || strings.Contains(r.body, ">Remove</button>") || !strings.Contains(r.body, testDeviceID) {
						t.Errorf("the page after Remove:\n%s", r.body)
					}
					if r := b.post(device.RemovePath, url.Values{"csrf": {b.csrf(device.DevicesPath)}, "device_id": {testDeviceID}}); r.status != http.StatusSeeOther {
						t.Errorf("removing it again answered %d, want the same redirect", r.status)
					}
				}},
				{name: "an account can't remove another account's device", run: func(t *testing.T, h *harness, _ *browser, linked tokenReply, _ ed25519.PrivateKey) {
					other := h.signIn(verified("ident!teen2", "U0TEEN2"))
					// other has no device, so its devices page has no form; /device
					// carries the same session's token.
					if r := other.post(device.RemovePath, url.Values{"csrf": {other.csrf(device.PagePath)}, "device_id": {testDeviceID}}); r.status != http.StatusNotFound {
						t.Errorf("removing another account's device answered %d, want 404", r.status)
					}
					if r := h.call(http.MethodGet, device.MePath, linked.AccessToken, nil); r.status != http.StatusOK {
						t.Errorf("the device's token answered %d after another account tried to remove it", r.status)
					}
				}},
				{name: "DELETE /api/v1/devices/me removes the device, and its token stops working", run: func(t *testing.T, h *harness, _ *browser, linked tokenReply, _ ed25519.PrivateKey) {
					if r := h.call(http.MethodDelete, device.MePath, linked.AccessToken, nil); r.status != http.StatusNoContent {
						t.Fatalf("DELETE answered %d: %s", r.status, r.body)
					}
					for _, method := range []string{http.MethodGet, http.MethodDelete} {
						if r := h.call(method, device.MePath, linked.AccessToken, nil); r.status != http.StatusUnauthorized {
							t.Errorf("%s %s with a removed device's token answered %d, want 401", method, device.MePath, r.status)
						}
					}
				}},
				{name: "an account Hack Club Auth turns ineligible loses its devices in the sign-in that finds out", run: func(t *testing.T, h *harness, _ *browser, linked tokenReply, _ ed25519.PrivateKey) {
					uploaded := h.link(h.signIn(verified("ident!teen1", "U0TEEN1")), "d_uploadeddevice000000000a")
					h.ingest(uploaded.AccessToken)
					gone := verified("ident!teen1", "U0TEEN1")
					gone.VerificationStatus = "ineligible"
					if b, _ := h.trySignIn(gone); b.signedIn() {
						t.Fatal("an ineligible identity signed in")
					}
					for _, tok := range []string{linked.AccessToken, uploaded.AccessToken} {
						if _, err := h.store.Authenticate(context.Background(), tok); !errors.Is(err, device.ErrUnknownToken) {
							t.Errorf("Authenticate = %v, want ErrUnknownToken: the device was removed", err)
						}
					}
					if n := h.count(`SELECT count(*) FROM devices`); n != 1 {
						t.Errorf("%d device rows left; want 1, the tombstone of the device that uploaded", n)
					}
					if n := h.count(`SELECT count(*) FROM devices WHERE device_id = $1 AND removed_at_ms IS NOT NULL`, "d_uploadeddevice000000000a"); n != 1 {
						t.Errorf("the device that uploaded has %d tombstones, want 1", n)
					}
				}},
				{name: "a device whose account turned ineligible behind the server's back can still remove itself", run: func(t *testing.T, h *harness, _ *browser, linked tokenReply, _ ed25519.PrivateKey) {
					if err := h.w.Do(context.Background(), 1, func(ctx context.Context, tx writer.Tx) error {
						_, err := tx.ExecContext(ctx, `UPDATE users SET verification_status = 'ineligible'`)
						return err
					}); err != nil {
						t.Fatal(err)
					}
					if _, err := h.store.Authenticate(context.Background(), linked.AccessToken); !errors.Is(err, device.ErrRevoked) {
						t.Errorf("Authenticate = %v, want ErrRevoked", err)
					}
					if r := h.call(http.MethodGet, device.MePath, linked.AccessToken, nil); r.status != http.StatusUnauthorized {
						t.Errorf("GET %s answered %d, want 401", device.MePath, r.status)
					}
					if r := h.call(http.MethodDelete, device.MePath, linked.AccessToken, nil); r.status != http.StatusNoContent {
						t.Errorf("DELETE %s answered %d, want 204", device.MePath, r.status)
					}
					if n := h.count(`SELECT count(*) FROM devices`); n != 0 {
						t.Errorf("%d device rows left after the device removed itself", n)
					}
				}},
				{name: "a token's use moves its device's last-seen time, at most every 5 minutes", run: func(t *testing.T, h *harness, _ *browser, linked tokenReply, _ ed25519.PrivateKey) {
					ctx := context.Background()
					tok := linked.AccessToken
					d0, err := h.store.Authenticate(ctx, tok)
					if err != nil {
						t.Fatal(err)
					}
					h.clock.Advance(time.Minute)
					if d, _ := h.store.Authenticate(ctx, tok); !d.LastSeenAt.Equal(d0.LastSeenAt) {
						t.Errorf("a use a minute later moved last seen from %v to %v", d0.LastSeenAt, d.LastSeenAt)
					}
					h.clock.Advance(device.TouchEvery)
					h.store.Authenticate(ctx, tok)
					if d, _ := h.store.Authenticate(ctx, tok); !d.LastSeenAt.After(d0.LastSeenAt.Add(device.TouchEvery)) {
						t.Errorf("a use 6 minutes later left last seen at %v (first %v)", d.LastSeenAt, d0.LastSeenAt)
					}
				}},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					h := newHarness(t, harnessConfig{engine: engine})
					b := h.signIn(verified("ident!teen1", "U0TEEN1"))
					key := newKey(t)
					tt.run(t, h, b, h.linkWith(b, testDeviceID, key), key)
				})
			}
		})
	}
}

// TestAuthenticateChecksFirst is the rule that a corrupted token fails its
// checksum without a database lookup: on a closed database, it still gets
// ErrBadToken, while a well-formed token reaches the database and fails
// there.
func TestAuthenticateChecksFirst(t *testing.T) {
	h := newHarness(t, harnessConfig{engine: storetest.Engines()[0]})
	good := devtoken.New()
	h.db.Read.Close()
	tests := []struct {
		name    string
		token   string
		wantBad bool
	}{
		{name: "a token with a wrong last character fails before any lookup", token: corrupt(good), wantBad: true},
		{name: "a token cut short fails before any lookup", token: good[:len(good)-2], wantBad: true},
		{name: "an empty token fails before any lookup", token: "", wantBad: true},
		{name: "a well-formed token is looked up, so the closed database fails it", token: good},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := h.store.Authenticate(context.Background(), tt.token)
			if got := errors.Is(err, device.ErrBadToken); got != tt.wantBad || err == nil {
				t.Errorf("Authenticate = %v; want ErrBadToken %v", err, tt.wantBad)
			}
			if !tt.wantBad && (errors.Is(err, device.ErrUnknownToken) || errors.Is(err, device.ErrRevoked)) {
				t.Errorf("Authenticate = %v, which a lookup on an open database would give", err)
			}
		})
	}
}

// TestDeleteExpiredCodes is the device_codes half of docs/retention.json:
// a code's row goes 1 day after it expires.
func TestDeleteExpiredCodes(t *testing.T) {
	for _, engine := range storetest.Engines() {
		t.Run(string(engine), func(t *testing.T) {
			h := newHarness(t, harnessConfig{engine: engine})
			h.requestCode("d_oldcode0000000000000000aa")
			h.clock.Advance(device.CodeLifetime + device.CodeGrace - time.Minute)
			h.requestCode("d_youngcode00000000000000aa")
			tests := []struct {
				name      string
				advance   time.Duration
				wantCodes int
			}{
				{name: "a code that expired less than a day ago stays", advance: 0, wantCodes: 2},
				{name: "a code that expired a day ago goes, and the one made later stays", advance: time.Minute, wantCodes: 1},
				{name: "every code goes once it expired a day ago", advance: device.CodeLifetime + device.CodeGrace, wantCodes: 0},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					h.clock.Advance(tt.advance)
					if _, err := h.store.DeleteExpiredCodes(context.Background()); err != nil {
						t.Fatal(err)
					}
					if n := h.count(`SELECT count(*) FROM device_codes`); n != tt.wantCodes {
						t.Errorf("%d codes left, want %d", n, tt.wantCodes)
					}
				})
			}
		})
	}
}

// TestCodeRetention holds docs/retention.json's server.device_codes to the
// periods this package runs.
func TestCodeRetention(t *testing.T) {
	p, err := retention.Default()
	if err != nil {
		t.Fatal(err)
	}
	e, ok := p.Find("server.device_codes")
	if !ok {
		t.Fatal("docs/retention.json has no server.device_codes entry")
	}
	const day = 24 * time.Hour
	tests := []struct {
		name string
		ok   bool
		want string
	}{
		{name: "the notice says how long a code works", ok: CodeLifetimeText() != "" && strings.Contains(e.Kept, CodeLifetimeText()+" after the app asks for it"), want: CodeLifetimeText() + " after the app asks for it"},
		{name: "the grace before a code's row goes is a whole number of days", ok: device.CodeGrace%day == 0, want: "CodeGrace in whole days"},
		{name: "the notice says how long the row outlives the code", ok: strings.Contains(e.Kept, "deleted "+retention.Days(int(device.CodeGrace/day))+" after that"), want: "deleted " + retention.Days(int(device.CodeGrace/day)) + " after that"},
		{name: "the entry covers the device_codes table", ok: slices.Contains(e.Tables, "device_codes"), want: `"tables": ["device_codes"]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Errorf("server.device_codes = %+v; want %s", e, tt.want)
			}
		})
	}
}

// CodeLifetimeText says device.CodeLifetime in the notice's words, or ""
// when it isn't a whole number of minutes.
func CodeLifetimeText() string {
	if device.CodeLifetime%time.Minute != 0 {
		return ""
	}
	return fmt.Sprintf("%d minutes", device.CodeLifetime/time.Minute)
}

// corrupt changes a token's last character, as a typo would.
func corrupt(tok string) string {
	c := byte('A')
	if tok != "" && tok[len(tok)-1] == 'A' {
		c = 'B'
	}
	if tok == "" {
		return "A"
	}
	return tok[:len(tok)-1] + string(c)
}
