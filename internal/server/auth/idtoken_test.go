// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/auth/hcafake"
)

const (
	testIssuer = "https://auth.example.org"
	testClient = "pairtime-client"
	testNonce  = "the-nonce-of-this-sign-in"
	testKid    = "kid-1"
)

var testNow = time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

// token returns the header and claims of an ID token HCA would issue now.
func token() *hcafake.Token {
	n := testNow.Unix()
	return &hcafake.Token{
		Header: map[string]any{"typ": "JWT", "alg": "RS256", "kid": testKid},
		Claims: map[string]any{
			"iss": testIssuer, "sub": "ident!abc123", "aud": testClient,
			"iat": n, "exp": n + 120, "nonce": testNonce, "auth_time": n - 30,
			"verification_status": "verified",
		},
	}
}

// keysFor returns a key finder that knows kid as key, like a JWKS.
func keysFor(kid string, key *rsa.PrivateKey) keyFinder {
	return func(want string) ([]*rsa.PublicKey, error) {
		if want != "" && want != kid {
			return nil, errors.New("unknown kid")
		}
		return []*rsa.PublicKey{&key.PublicKey}, nil
	}
}

func TestVerifyIDToken(t *testing.T) {
	right, wrong := hcafake.Key(0), hcafake.Key(1)
	n := testNow.Unix()
	sign := func(edit func(tok *hcafake.Token)) string {
		tok := token()
		if edit != nil {
			edit(tok)
		}
		return tok.SignWith(right)
	}
	tests := []struct {
		name string
		raw  string
		keys keyFinder // nil: keysFor(testKid, right)
		// freshSince, when set, is the Unix time a login that asked for
		// prompt=login started.
		freshSince int64
		wantCode   string // "" means the token passes
	}{
		{name: "a token HCA would issue passes", raw: sign(nil)},
		{name: "aud may be an array holding only the client", raw: sign(func(t *hcafake.Token) { t.Claims["aud"] = []string{testClient} })},
		{name: "azp naming the client passes", raw: sign(func(t *hcafake.Token) { t.Claims["azp"] = testClient })},
		{name: "typ may be lowercase jwt", raw: sign(func(t *hcafake.Token) { t.Header["typ"] = "jwt" })},
		{name: "a token without typ passes", raw: sign(func(t *hcafake.Token) { delete(t.Header, "typ") })},
		{name: "a fractional exp is read as seconds", raw: sign(func(t *hcafake.Token) { t.Claims["exp"] = float64(n) + 120.5 })},
		{name: "a token that expired within the clock skew passes", raw: sign(func(t *hcafake.Token) { t.Claims["exp"] = n - 30 })},
		{name: "a token that expired past the clock skew is refused", raw: sign(func(t *hcafake.Token) { t.Claims["exp"] = n - 61 }), wantCode: CodeExpired},
		{name: "a token without exp is refused", raw: sign(func(t *hcafake.Token) { delete(t.Claims, "exp") }), wantCode: CodeMalformedIDToken},
		{name: "exp as a string is refused", raw: sign(func(t *hcafake.Token) { t.Claims["exp"] = "1795089720" }), wantCode: CodeMalformedIDToken},
		{name: "a negative exp is refused", raw: sign(func(t *hcafake.Token) { t.Claims["exp"] = -1 }), wantCode: CodeMalformedIDToken},
		{name: "a token without iat is refused", raw: sign(func(t *hcafake.Token) { delete(t.Claims, "iat") }), wantCode: CodeMalformedIDToken},
		{name: "iat past the clock skew in the future is refused", raw: sign(func(t *hcafake.Token) { t.Claims["iat"] = n + 61 }), wantCode: CodeNotYetValid},
		{name: "nbf in the future is refused", raw: sign(func(t *hcafake.Token) { t.Claims["nbf"] = n + 300 }), wantCode: CodeNotYetValid},
		{name: "auth_time in the future is refused", raw: sign(func(t *hcafake.Token) { t.Claims["auth_time"] = n + 300 }), wantCode: CodeNotYetValid},
		{name: "another issuer is refused", raw: sign(func(t *hcafake.Token) { t.Claims["iss"] = "https://auth.example.org/" }), wantCode: CodeWrongIssuer},
		{name: "a missing aud is refused", raw: sign(func(t *hcafake.Token) { delete(t.Claims, "aud") }), wantCode: CodeWrongAudience},
		{name: "an empty aud array is refused", raw: sign(func(t *hcafake.Token) { t.Claims["aud"] = []string{} }), wantCode: CodeWrongAudience},
		{name: "aud as a number is refused", raw: sign(func(t *hcafake.Token) { t.Claims["aud"] = 7 }), wantCode: CodeMalformedIDToken},
		{name: "azp naming another party is refused", raw: sign(func(t *hcafake.Token) { t.Claims["azp"] = "other" }), wantCode: CodeWrongAudience},
		{name: "a missing nonce is refused", raw: sign(func(t *hcafake.Token) { delete(t.Claims, "nonce") }), wantCode: CodeWrongNonce},
		{name: "a nonce differing in one character is refused", raw: sign(func(t *hcafake.Token) { t.Claims["nonce"] = testNonce[:len(testNonce)-1] + "X" }), wantCode: CodeWrongNonce},
		{name: "a crit header is refused", raw: sign(func(t *hcafake.Token) { t.Header["crit"] = []string{"exp"} }), wantCode: CodeMalformedIDToken},
		{name: "typ at+jwt, an access token, is refused", raw: sign(func(t *hcafake.Token) { t.Header["typ"] = "at+jwt" }), wantCode: CodeMalformedIDToken},
		{name: "RS512 is refused", raw: sign(func(t *hcafake.Token) { t.Header["alg"] = "RS512" }), wantCode: CodeUnsupportedAlg},
		{name: "alg none is refused", raw: token().Unsigned(), wantCode: CodeUnsupportedAlg},
		{name: "a five-part token is refused", raw: sign(nil) + ".x.y", wantCode: CodeMalformedIDToken},
		{name: "a token over 16 KB is refused", raw: sign(func(t *hcafake.Token) { t.Claims["pad"] = strings.Repeat("x", 17<<10) }), wantCode: CodeMalformedIDToken},
		{name: "a signature by another key is refused", raw: token().SignWith(wrong), wantCode: CodeBadSignature},
		{
			name:     "the signature is checked before any claim",
			raw:      func() string { tok := token(); tok.Claims["iss"] = "https://evil.example"; return tok.SignWith(wrong) }(),
			wantCode: CodeBadSignature,
		},
		{name: "a signature that isn't base64url is refused", raw: sign(nil) + "*", wantCode: CodeMalformedIDToken},
		{name: "an unknown kid is refused", raw: sign(func(t *hcafake.Token) { t.Header["kid"] = "kid-2" }), wantCode: CodeUnknownKey},
		{
			name: "without a kid every candidate key is tried",
			raw:  sign(func(t *hcafake.Token) { delete(t.Header, "kid") }),
			keys: func(string) ([]*rsa.PublicKey, error) {
				return []*rsa.PublicKey{&wrong.PublicKey, &right.PublicKey}, nil
			},
		},
		{name: "a fresh login authenticated after it started passes", raw: sign(nil), freshSince: n - 60},
		{name: "a fresh login authenticated within the clock skew before it started passes", raw: sign(func(t *hcafake.Token) { t.Claims["auth_time"] = n - 120 }), freshSince: n - 60},
		{name: "a fresh login authenticated past the clock skew before it started is refused", raw: sign(func(t *hcafake.Token) { t.Claims["auth_time"] = n - 121 }), freshSince: n - 60, wantCode: CodeLoginNotFresh},
		{name: "a fresh login started 9 minutes ago passes with the auth_time of its own login", raw: sign(func(t *hcafake.Token) { t.Claims["auth_time"] = n - 500 }), freshSince: n - 540},
		{name: "a fresh login without auth_time is refused", raw: sign(func(t *hcafake.Token) { delete(t.Claims, "auth_time") }), freshSince: n - 60, wantCode: CodeNoAuthTime},
		{name: "an ordinary sign-in passes with an auth_time three days old", raw: sign(func(t *hcafake.Token) { t.Claims["auth_time"] = n - 3*24*3600 })},
		{name: "an ordinary sign-in passes without auth_time", raw: sign(func(t *hcafake.Token) { delete(t.Claims, "auth_time") })},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keys := tt.keys
			if keys == nil {
				keys = keysFor(testKid, right)
			}
			var freshSince time.Time
			if tt.freshSince != 0 {
				freshSince = time.Unix(tt.freshSince, 0)
			}
			c, err := verifyIDToken(tt.raw, keys, expectation{
				issuer:     testIssuer,
				clientID:   testClient,
				nonce:      testNonce,
				now:        testNow,
				freshSince: freshSince,
			})
			if tt.wantCode == "" {
				if err != nil {
					t.Fatalf("verifyIDToken refused it: %v", err)
				}
				if c.Sub != "ident!abc123" || c.VerificationStatus == nil || *c.VerificationStatus != "verified" {
					t.Errorf("claims = %+v", c)
				}
				return
			}
			if err == nil {
				t.Fatalf("verifyIDToken accepted it, want %s", tt.wantCode)
			}
			if err.Code != tt.wantCode {
				t.Errorf("code = %s (%v), want %s", err.Code, err.Err, tt.wantCode)
			}
			if strings.Contains(err.Error(), testNonce) {
				t.Errorf("the error quotes the nonce: %v", err)
			}
		})
	}
}

func TestParseKeys(t *testing.T) {
	good := &hcafake.Key(0).PublicKey
	n64 := base64.RawURLEncoding.EncodeToString(good.N.Bytes())
	small := new(big.Int).Lsh(big.NewInt(1), 1023)
	small.Add(small, big.NewInt(1))
	tests := []struct {
		name    string
		set     []jwk
		wantN   int // keys kept; 0 means an error
		wantKid string
	}{
		{name: "an RS256 signing key is kept with its kid", set: []jwk{{Kty: "RSA", Use: "sig", Alg: "RS256", Kid: "a", N: n64, E: "AQAB"}}, wantN: 1, wantKid: "a"},
		{name: "a key without use or alg is kept", set: []jwk{{Kty: "RSA", N: n64, E: "AQAB"}}, wantN: 1},
		{name: "an encryption key is skipped", set: []jwk{{Kty: "RSA", Use: "enc", N: n64, E: "AQAB"}, {Kty: "RSA", Kid: "b", N: n64, E: "AQAB"}}, wantN: 1, wantKid: "b"},
		{name: "a key for another alg is skipped", set: []jwk{{Kty: "RSA", Alg: "RS512", N: n64, E: "AQAB"}}},
		{name: "an EC key is skipped", set: []jwk{{Kty: "EC"}}},
		{name: "a modulus under 2048 bits is refused", set: []jwk{{Kty: "RSA", N: base64.RawURLEncoding.EncodeToString(small.Bytes()), E: "AQAB"}}},
		{name: "an even exponent is refused", set: []jwk{{Kty: "RSA", N: n64, E: "AQAA"}}},
		{name: "an exponent of 1 is refused", set: []jwk{{Kty: "RSA", N: n64, E: "AQ"}}},
		{name: "an exponent over four bytes is refused", set: []jwk{{Kty: "RSA", N: n64, E: "AQABAQAB"}}},
		{name: "an empty set is refused", set: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keys, err := parseKeys(tt.set)
			if tt.wantN == 0 {
				if err == nil {
					t.Fatalf("parseKeys kept %d keys, want an error", len(keys))
				}
				return
			}
			if err != nil || len(keys) != tt.wantN {
				t.Fatalf("parseKeys = %d keys, %v; want %d", len(keys), err, tt.wantN)
			}
			if keys[0].kid != tt.wantKid || keys[0].key.N.Cmp(good.N) != 0 || keys[0].key.E != 65537 {
				t.Errorf("key = %q, E %d", keys[0].kid, keys[0].key.E)
			}
		})
	}
}

func TestMatchKeys(t *testing.T) {
	a, b, c := &hcafake.Key(0).PublicKey, &hcafake.Key(1).PublicKey, &hcafake.Key(2).PublicKey
	keys := []publicKey{{kid: "a", key: a}, {kid: "b", key: b}, {kid: "", key: c}}
	tests := []struct {
		name string
		kid  string
		want []*rsa.PublicKey
	}{
		{name: "a kid picks its key and the keys without a kid", kid: "a", want: []*rsa.PublicKey{a, c}},
		{name: "no kid tries every key", kid: "", want: []*rsa.PublicKey{a, b, c}},
		{name: "an unknown kid gets only the keys without a kid", kid: "z", want: []*rsa.PublicKey{c}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchKeys(keys, tt.kid)
			if len(got) != len(tt.want) {
				t.Fatalf("matchKeys = %d keys, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("key %d differs", i)
				}
			}
		})
	}
}

func TestAuthMethod(t *testing.T) {
	tests := []struct {
		name      string
		supported []string
		want      string
	}{
		{name: "client_secret_post is used when listed, as HCA's guide shows", supported: []string{"client_secret_basic", "client_secret_post"}, want: clientSecretPost},
		{name: "client_secret_basic is used when it is the only one", supported: []string{"client_secret_basic"}, want: clientSecretBasic},
		{name: "a missing list means client_secret_basic (OIDC Discovery 3)", want: clientSecretBasic},
		{name: "a list without either method is refused", supported: []string{"private_key_jwt"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := authMethod(tt.supported)
			if tt.want == "" {
				if err == nil {
					t.Fatalf("authMethod = %q, want an error", got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("authMethod = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestLocalPath(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{name: "a path on this server is kept", in: "/projects/demo?tab=time", want: "/projects/demo?tab=time"},
		{name: "empty means the home page", in: "", want: "/"},
		{name: "a protocol-relative URL is replaced", in: "//evil.example/x", want: "/"},
		{name: "a backslash trick is replaced", in: "/\\evil.example", want: "/"},
		{name: "an absolute URL is replaced", in: "https://evil.example/", want: "/"},
		{name: "a relative path is replaced", in: "projects", want: "/"},
		{name: "a control character is replaced", in: "/a\nb", want: "/"},
		{name: "an overlong path is replaced", in: "/" + strings.Repeat("a", 600), want: "/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := localPath(tt.in); got != tt.want {
				t.Errorf("localPath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCheckURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		ok   bool
	}{
		{name: "HCA's issuer is accepted", in: "https://auth.hackclub.com", ok: true},
		{name: "an https URL with a path is accepted", in: "https://auth.example.org/oauth/token", ok: true},
		{name: "http on localhost is accepted, for a fake HCA", in: "http://localhost:8080/hca", ok: true},
		{name: "http on a loopback address is accepted", in: "http://127.0.0.1:9000", ok: true},
		{name: "http elsewhere is refused", in: "http://auth.example.org"},
		{name: "another scheme is refused", in: "ftp://auth.example.org"},
		{name: "a relative URL is refused", in: "/oauth/token"},
		{name: "a URL with a fragment is refused", in: "https://auth.example.org/#x"},
		{name: "a URL with a user is refused", in: "https://me@auth.example.org"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := CheckURL(tt.in); (err == nil) != tt.ok {
				t.Errorf("CheckURL(%q) = %v, want ok %v", tt.in, err, tt.ok)
			}
		})
	}
}
