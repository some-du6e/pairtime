// SPDX-License-Identifier: AGPL-3.0-or-later

package privacy_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/privacy"
)

func TestVerify(t *testing.T) {
	h := newHub(t)
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	stranger, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	v := &privacy.Verifier{Audience: audience, Keys: h}
	type edit func(header, claims map[string]any)
	set := func(key string, val any) edit { return func(_, c map[string]any) { c[key] = val } }
	setHeader := func(key string, val any) edit { return func(hd, _ map[string]any) { hd[key] = val } }
	drop := func(key string) edit { return func(_, c map[string]any) { delete(c, key) } }
	tests := []struct {
		name   string
		edit   edit
		action string
		// token, when set, makes the token from the hub's signed one.
		token func(signed string) string
		// sign signs with another key than the hub's.
		sign       *ecdsa.PrivateKey
		wantStatus int
		wantCode   string
	}{
		{name: "a token the hub signed for this audience, at this moment, for this endpoint verifies"},
		{name: "an aud array that names this server verifies", edit: set("aud", []string{"another-program", audience})},
		{name: "a NumericDate with a fraction verifies", edit: func(_, c map[string]any) {
			c["iat"] = float64(now.Unix()) + 0.25
			c["exp"] = float64(now.Unix()) + 120.5
		}},
		{name: "an exp 29 s gone is inside the leeway", edit: func(_, c map[string]any) {
			c["exp"] = now.Add(-29 * time.Second).Unix()
			c["iat"] = now.Add(-5 * time.Minute).Unix()
		}},
		{name: "a token whose exp passed more than 30 s ago is refused", edit: func(_, c map[string]any) {
			c["exp"] = now.Add(-31 * time.Second).Unix()
			c["iat"] = now.Add(-5 * time.Minute).Unix()
		}, wantStatus: 401, wantCode: "expired"},
		{name: "an iat more than 30 s ahead is refused", edit: func(_, c map[string]any) {
			c["iat"] = now.Add(31 * time.Second).Unix()
			c["exp"] = now.Add(2 * time.Minute).Unix()
		}, wantStatus: 401, wantCode: "not_yet_valid"},
		{name: "an nbf more than 30 s ahead is refused", edit: set("nbf", now.Add(time.Minute).Unix()), wantStatus: 401, wantCode: "not_yet_valid"},
		{name: "a lifetime over 300 s is refused", edit: set("exp", now.Add(301*time.Second).Unix()), wantStatus: 401, wantCode: "invalid_lifetime"},
		{name: "an exp before the iat is refused", edit: func(_, c map[string]any) {
			c["iat"] = now.Unix()
			c["exp"] = now.Add(-time.Second).Unix()
		}, wantStatus: 401, wantCode: "invalid_lifetime"},
		{name: "another issuer is refused", edit: set("iss", "someone-else"), wantStatus: 401, wantCode: "invalid_issuer"},
		{name: "another program's audience is refused", edit: set("aud", "another-program"), wantStatus: 401, wantCode: "invalid_audience"},
		{name: "an aud array without this server is refused", edit: set("aud", []string{"another-program"}), wantStatus: 401, wantCode: "invalid_audience"},
		{name: "a delete token at the export endpoint is a 400 action_mismatch", action: privacy.ActionExport, wantStatus: 400, wantCode: "action_mismatch"},
		{name: "an action neither endpoint has is a 400 action_mismatch", edit: set("action", "purge"), wantStatus: 400, wantCode: "action_mismatch"},
		{name: "alg HS256 is refused", edit: setHeader("alg", "HS256"), wantStatus: 401, wantCode: "unsupported_alg"},
		{name: "alg none is refused", edit: setHeader("alg", "none"), wantStatus: 401, wantCode: "unsupported_alg"},
		{name: "a typ other than JWT is refused", edit: setHeader("typ", "at+jwt"), wantStatus: 401, wantCode: "invalid_typ"},
		{name: "a header without a kid is refused", edit: func(hd, _ map[string]any) { delete(hd, "kid") }, wantStatus: 401, wantCode: "missing_kid"},
		{name: "a kid the hub doesn't publish is refused", edit: setHeader("kid", "another-key"), wantStatus: 401, wantCode: "unknown_key"},
		{name: "a crit header is refused, since no extension is known", edit: setHeader("crit", []string{"exp"}), wantStatus: 401, wantCode: "unsupported_crit"},
		{name: "a token another key signed is refused", sign: stranger, wantStatus: 401, wantCode: "invalid_signature"},
		{name: "a payload changed after signing is refused", token: func(s string) string {
			parts := strings.Split(s, ".")
			parts[1] = part(t, map[string]any{"iss": privacy.Issuer, "aud": audience})
			return strings.Join(parts, ".")
		}, wantStatus: 401, wantCode: "invalid_signature"},
		{name: "a signature of 63 bytes is refused", token: func(s string) string {
			parts := strings.Split(s, ".")
			parts[2] = base64.RawURLEncoding.EncodeToString(make([]byte, 63))
			return strings.Join(parts, ".")
		}, wantStatus: 401, wantCode: "invalid_signature"},
		{name: "a token of two parts is malformed", token: func(s string) string { return s[:strings.LastIndex(s, ".")] }, wantStatus: 401, wantCode: "malformed_token"},
		{name: "a token of four parts is malformed", token: func(s string) string { return s + ".x" }, wantStatus: 401, wantCode: "malformed_token"},
		{name: "base64 padding in the header is malformed", token: func(s string) string { return strings.Replace(s, ".", "=.", 1) }, wantStatus: 401, wantCode: "malformed_token"},
		{name: "a jti over 128 bytes is refused before anything is stored", edit: set("jti", strings.Repeat("j", privacy.MaxIDSize+1)), wantStatus: 401, wantCode: "invalid_claims"},
		{name: "a jti with a byte past ASCII is refused", edit: set("jti", "jti-\xc3\xa9"), wantStatus: 401, wantCode: "invalid_claims"},
		{name: "an empty jti is refused", edit: set("jti", ""), wantStatus: 401, wantCode: "invalid_claims"},
		{name: "a token without a request_id is refused", edit: drop("request_id"), wantStatus: 401, wantCode: "invalid_claims"},
		{name: "a dry_run that isn't a boolean is refused", edit: set("dry_run", "false"), wantStatus: 401, wantCode: "invalid_claims"},
		{name: "a token without dry_run is refused", edit: drop("dry_run"), wantStatus: 401, wantCode: "invalid_claims"},
		{name: "a token without a subject is refused", edit: drop("subject"), wantStatus: 401, wantCode: "invalid_claims"},
		{name: "a subject without emails is refused", edit: set("subject", map[string]any{"slack_id": "U0TEEN0001"}), wantStatus: 401, wantCode: "invalid_claims"},
		{name: "an iat written as a string isn't a NumericDate", edit: set("iat", "1795521600"), wantStatus: 401, wantCode: "invalid_claims"},
		{name: "a token without exp is refused", edit: drop("exp"), wantStatus: 401, wantCode: "invalid_claims"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header, claims := h.header(), h.claims(now, privacy.ActionDelete, subject([]string{"teen@example.org"}, "U0TEEN0001", "ident!teen1"))
			if tt.edit != nil {
				tt.edit(header, claims)
			}
			key := h.key
			if tt.sign != nil {
				key = tt.sign
			}
			token := sign(t, key, header, claims)
			if tt.token != nil {
				token = tt.token(token)
			}
			action := privacy.ActionDelete
			if tt.action != "" {
				action = tt.action
			}
			c, err := v.Verify(context.Background(), token, action, now)
			if tt.wantCode == "" {
				if err != nil {
					t.Fatalf("Verify: %v", err)
				}
				want := privacy.Subject{Emails: []string{"teen@example.org"}, SlackID: "U0TEEN0001", IdentityID: "ident!teen1"}
				if c.JTI != claims["jti"] || c.RequestID != claims["request_id"] || c.Action != privacy.ActionDelete || c.DryRun || !reflect.DeepEqual(c.Subject, want) {
					t.Errorf("claims %+v, want the token's", c)
				}
				return
			}
			var e *privacy.Error
			if !errors.As(err, &e) || e.Status != tt.wantStatus || e.Code != tt.wantCode {
				t.Errorf("Verify error = %v, want %d %s", err, tt.wantStatus, tt.wantCode)
			}
		})
	}
}

// TestVerifyNeedsKeys checks a Verifier refuses to check anything without
// its audience or keys, which would make every token's aud or signature
// pass.
func TestVerifyNeedsKeys(t *testing.T) {
	tests := []struct {
		name string
		v    *privacy.Verifier
	}{
		{name: "a verifier without an audience checks nothing", v: &privacy.Verifier{Keys: newHub(t)}},
		{name: "a verifier without keys checks nothing", v: &privacy.Verifier{Audience: audience}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.v.Verify(context.Background(), "a.b.c", privacy.ActionDelete, time.Now())
			var e *privacy.Error
			if err == nil || errors.As(err, &e) {
				t.Errorf("Verify error = %v, want the verifier's own", err)
			}
		})
	}
}
