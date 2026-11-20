// SPDX-License-Identifier: AGPL-3.0-or-later

package device_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
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

func newKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv
}

// registration is the body of a key registration for the device id, as
// the app sends it.
func registration(t *testing.T, id string, priv ed25519.PrivateKey) map[string]string {
	t.Helper()
	pub := base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey))
	sig := ed25519.Sign(priv, device.KeyMessage(id, pub))
	return map[string]string{"public_key": pub, "signature": base64.StdEncoding.EncodeToString(sig)}
}

// TestDeviceAPI is the device's own API, POST /api/v1/devices/me/key and
// /api/v1/devices/me, and /account/devices.
func TestDeviceAPI(t *testing.T) {
	for _, engine := range storetest.Engines() {
		t.Run(string(engine), func(t *testing.T) {
			tests := []struct {
				name string
				run  func(t *testing.T, h *harness, b *browser, tok string)
			}{
				{name: "a key registers once: 201, the same key again 200, a different key 409", run: func(t *testing.T, h *harness, _ *browser, tok string) {
					key := newKey(t)
					for _, want := range []int{http.StatusCreated, http.StatusOK} {
						if r := h.call(http.MethodPost, device.KeyPath, tok, registration(t, testDeviceID, key)); r.status != want {
							t.Fatalf("registering the key answered %d: %s; want %d", r.status, r.body, want)
						}
					}
					r := h.call(http.MethodPost, device.KeyPath, tok, registration(t, testDeviceID, newKey(t)))
					if r.status != http.StatusConflict || !strings.Contains(r.body, `"error":"key_already_registered"`) {
						t.Errorf("a different key answered %d: %s; want 409 key_already_registered", r.status, r.body)
					}
					d, err := h.store.Authenticate(context.Background(), tok)
					if err != nil || !key.Public().(ed25519.PublicKey).Equal(d.PublicKey) || d.KeyRegisteredAt.IsZero() {
						t.Errorf("Authenticate = %+v, %v; want the first key", d, err)
					}
				}},
				{name: "a key whose signature doesn't verify is refused", run: func(t *testing.T, h *harness, _ *browser, tok string) {
					key := newKey(t)
					forged := registration(t, testDeviceID, key)
					forged["public_key"] = base64.StdEncoding.EncodeToString(newKey(t).Public().(ed25519.PublicKey))
					otherDevice := registration(t, "d_someotherdevice00000000aa", key)
					short := registration(t, testDeviceID, key)
					short["public_key"] = base64.StdEncoding.EncodeToString(make([]byte, 31))
					for name, body := range map[string]map[string]string{"signed by another key": forged, "signed for another device": otherDevice, "a 31-byte key": short} {
						if r := h.call(http.MethodPost, device.KeyPath, tok, body); r.status != http.StatusBadRequest {
							t.Errorf("a key %s answered %d: %s; want 400", name, r.status, r.body)
						}
					}
					if r := h.call(http.MethodPost, device.KeyPath, tok, registration(t, testDeviceID, key)); r.status != http.StatusCreated {
						t.Errorf("the refusals registered something: a good key then answered %d", r.status)
					}
				}},
				{name: "a request with a token that isn't valid gets 401 invalid_token, and one with none a challenge without an error (RFC 6750 3.1)", run: func(t *testing.T, h *harness, _ *browser, tok string) {
					for name, c := range map[string]struct{ token, challenge string }{
						"no token":          {"", `Bearer realm="pairtime"`},
						"a corrupted token": {corrupt(tok), `Bearer realm="pairtime", error="invalid_token"`},
						"an unknown token":  {devtoken.New(), `Bearer realm="pairtime", error="invalid_token"`},
					} {
						r := h.call(http.MethodGet, device.MePath, c.token, nil)
						if r.status != http.StatusUnauthorized || r.header.Get("WWW-Authenticate") != c.challenge {
							t.Errorf("%s answered %d, WWW-Authenticate %q; want 401 and %q", name, r.status, r.header.Get("WWW-Authenticate"), c.challenge)
						}
					}
				}},
				{name: "the account page lists the device, and Remove stops its token at once", run: func(t *testing.T, h *harness, b *browser, tok string) {
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
					d, err := h.store.Authenticate(context.Background(), tok)
					if !errors.Is(err, device.ErrRevoked) || !d.Revoked || d.ID != testDeviceID {
						t.Errorf("Authenticate after Remove = %+v, %v; want the device with Revoked and ErrRevoked", d, err)
					}
					if r := b.get(device.DevicesPath); !strings.Contains(r.body, "Removed ") || strings.Contains(r.body, ">Remove</button>") {
						t.Errorf("the page after Remove:\n%s", r.body)
					}
				}},
				{name: "an account can't remove another account's device", run: func(t *testing.T, h *harness, _ *browser, tok string) {
					other := h.signIn(verified("ident!teen2", "U0TEEN2"))
					// other has no device, so its devices page has no form; /device
					// carries the same session's token.
					if r := other.post(device.RemovePath, url.Values{"csrf": {other.csrf(device.PagePath)}, "device_id": {testDeviceID}}); r.status != http.StatusNotFound {
						t.Errorf("removing another account's device answered %d, want 404", r.status)
					}
					if r := h.call(http.MethodGet, device.MePath, tok, nil); r.status != http.StatusOK {
						t.Errorf("the device's token answered %d after another account tried to remove it", r.status)
					}
				}},
				{name: "DELETE /api/v1/devices/me removes the device, and its token stops working", run: func(t *testing.T, h *harness, _ *browser, tok string) {
					if r := h.call(http.MethodDelete, device.MePath, tok, nil); r.status != http.StatusNoContent {
						t.Fatalf("DELETE answered %d: %s", r.status, r.body)
					}
					for _, path := range []string{device.MePath, device.KeyPath} {
						method := http.MethodGet
						if path == device.KeyPath {
							method = http.MethodPost
						}
						if r := h.call(method, path, tok, registration(t, testDeviceID, newKey(t))); r.status != http.StatusUnauthorized {
							t.Errorf("%s %s with a removed device's token answered %d, want 401", method, path, r.status)
						}
					}
				}},
				{name: "an account Hack Club Auth marks ineligible loses its devices", run: func(t *testing.T, h *harness, _ *browser, tok string) {
					if err := h.w.Do(context.Background(), 1, func(ctx context.Context, tx writer.Tx) error {
						_, err := tx.ExecContext(ctx, `UPDATE users SET verification_status = 'ineligible'`)
						return err
					}); err != nil {
						t.Fatal(err)
					}
					if _, err := h.store.Authenticate(context.Background(), tok); !errors.Is(err, device.ErrRevoked) {
						t.Errorf("Authenticate = %v, want ErrRevoked", err)
					}
				}},
				{name: "a token's use moves its device's last-seen time, at most every 5 minutes", run: func(t *testing.T, h *harness, _ *browser, tok string) {
					ctx := context.Background()
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
					tt.run(t, h, b, h.link(b, testDeviceID).AccessToken)
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
