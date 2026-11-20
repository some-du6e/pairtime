// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ClockSkew is how far pairtimed's clock and HCA's may disagree when an ID
// token's times are checked. HCA's ID tokens live 120 s.
const ClockSkew = 60 * time.Second

// FreshLoginMaxAge is how long before the callback HCA may have
// authenticated the person for a login that asked for prompt=login to
// count as fresh.
const FreshLoginMaxAge = 5 * time.Minute

// maxIDTokenBytes bounds an ID token before it is parsed.
const maxIDTokenBytes = 16 << 10

// claims are the ID token claims pairtimed reads: OIDC Core 2's and HCA's
// own (SP 3.1). A claim HCA didn't send is nil, or "" for the strings OIDC
// Core requires.
type claims struct {
	Iss      string       `json:"iss"`
	Sub      string       `json:"sub"`
	Aud      audience     `json:"aud"`
	Azp      string       `json:"azp"`
	Exp      *numericDate `json:"exp"`
	Iat      *numericDate `json:"iat"`
	Nbf      *numericDate `json:"nbf"`
	AuthTime *numericDate `json:"auth_time"`
	Nonce    string       `json:"nonce"`

	Email              *string `json:"email"`
	EmailVerified      *bool   `json:"email_verified"`
	SlackID            *string `json:"slack_id"`
	VerificationStatus *string `json:"verification_status"`
	YSWSEligible       *bool   `json:"ysws_eligible"`
}

// audience is the aud claim, which is a string or an array of strings
// (OIDC Core 2).
type audience []string

func (a *audience) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*a = nil
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*a = audience{s}
		return nil
	}
	var list []string
	if err := json.Unmarshal(b, &list); err != nil {
		return errors.New("aud is neither a string nor an array of strings")
	}
	*a = list
	return nil
}

// numericDate is a JWT time: seconds since the epoch, as a JSON number
// (RFC 7519 2).
type numericDate struct {
	t time.Time
}

func (d *numericDate) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || b[0] < '0' || b[0] > '9' {
		return errors.New("a time claim must be a non-negative JSON number")
	}
	var f float64
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	if f > 1e11 {
		return errors.New("a time claim is past the year 5000")
	}
	d.t = time.Unix(int64(f), 0)
	return nil
}

// expectation is what a callback expects of its ID token.
type expectation struct {
	issuer, clientID, nonce string
	now                     time.Time
	// fresh is set when the login asked for prompt=login.
	fresh bool
}

// keyFinder returns the keys that may have signed a token naming kid.
type keyFinder func(kid string) ([]*rsa.PublicKey, error)

// header is the JOSE header of an ID token (RFC 7515 4).
type header struct {
	Alg  string          `json:"alg"`
	Kid  string          `json:"kid"`
	Typ  string          `json:"typ"`
	Crit json.RawMessage `json:"crit"`
}

// verifyIDToken checks raw by OIDC Core 3.1.3.7 and returns its claims. The
// signature is checked first, so no claim is read from a token HCA didn't
// sign.
func verifyIDToken(raw string, keys keyFinder, want expectation) (*claims, *Error) {
	refuse := func(code, format string, args ...any) (*claims, *Error) {
		return nil, &Error{
			Status:  http.StatusUnauthorized,
			Code:    code,
			Message: "Hack Club Auth's answer didn't check out, so you weren't signed in. Try again; if it keeps happening, tell the people who run this server.",
			Err:     fmt.Errorf(format, args...),
		}
	}
	if len(raw) > maxIDTokenBytes {
		return refuse(CodeMalformedIDToken, "the ID token is %d bytes, over %d", len(raw), maxIDTokenBytes)
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		// Five parts would be an encrypted token, which pairtimed never
		// registers for.
		return refuse(CodeMalformedIDToken, "the ID token has %d parts, not the 3 of a signed JWT", len(parts))
	}
	headerJSON, err := b64(parts[0])
	if err != nil {
		return refuse(CodeMalformedIDToken, "the ID token's header isn't base64url")
	}
	var h header
	if err := json.Unmarshal(headerJSON, &h); err != nil {
		return refuse(CodeMalformedIDToken, "the ID token's header isn't a JSON object")
	}
	if h.Alg != "RS256" {
		// HCA signs with RS256 (SP 3.1). Anything else, "none" and HS256
		// included, is refused before a key is looked at.
		return refuse(CodeUnsupportedAlg, "the ID token is signed with alg %q, not RS256", h.Alg)
	}
	if h.Crit != nil {
		return refuse(CodeMalformedIDToken, "the ID token's header has crit, which pairtimed doesn't understand (RFC 7515 4.1.11)")
	}
	if h.Typ != "" && !strings.EqualFold(h.Typ, "JWT") {
		return refuse(CodeMalformedIDToken, "the ID token's typ is %q, not JWT", h.Typ)
	}
	sig, err := b64(parts[2])
	if err != nil {
		return refuse(CodeMalformedIDToken, "the ID token's signature isn't base64url")
	}
	candidates, err := keys(h.Kid)
	if err != nil {
		return refuse(CodeUnknownKey, "finding the ID token's signing key: %w", err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	verified := false
	for _, k := range candidates {
		if rsa.VerifyPKCS1v15(k, crypto.SHA256, digest[:], sig) == nil {
			verified = true
			break
		}
	}
	if !verified {
		return refuse(CodeBadSignature, "the ID token's signature doesn't verify with any of HCA's %d candidate keys", len(candidates))
	}

	payload, err := b64(parts[1])
	if err != nil {
		return refuse(CodeMalformedIDToken, "the ID token's payload isn't base64url")
	}
	var c claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return refuse(CodeMalformedIDToken, "the ID token's claims don't parse: %v", err)
	}
	if c.Iss != want.issuer {
		return refuse(CodeWrongIssuer, "the ID token's iss is %q, want %q", c.Iss, want.issuer)
	}
	if len(c.Aud) == 0 {
		return refuse(CodeWrongAudience, "the ID token has no aud")
	}
	for _, a := range c.Aud {
		// OIDC Core 3.1.3.7 step 3: the client must be an audience, and
		// any other audience is one pairtimed doesn't trust.
		if a != want.clientID {
			return refuse(CodeWrongAudience, "the ID token's aud %q includes %q, which isn't pairtimed's client id", []string(c.Aud), a)
		}
	}
	if c.Azp != "" && c.Azp != want.clientID {
		return refuse(CodeWrongAudience, "the ID token's azp is %q, not pairtimed's client id", c.Azp)
	}
	if c.Exp == nil {
		return refuse(CodeMalformedIDToken, "the ID token has no exp")
	}
	if !want.now.Before(c.Exp.t.Add(ClockSkew)) {
		return refuse(CodeExpired, "the ID token expired at %s, and it is %s", c.Exp.t.UTC().Format(time.RFC3339), want.now.UTC().Format(time.RFC3339))
	}
	if c.Iat == nil {
		return refuse(CodeMalformedIDToken, "the ID token has no iat")
	}
	future := want.now.Add(ClockSkew)
	if c.Iat.t.After(future) {
		return refuse(CodeNotYetValid, "the ID token was issued at %s, in the future", c.Iat.t.UTC().Format(time.RFC3339))
	}
	if c.Nbf != nil && c.Nbf.t.After(future) {
		return refuse(CodeNotYetValid, "the ID token isn't valid before %s", c.Nbf.t.UTC().Format(time.RFC3339))
	}
	if c.AuthTime != nil && c.AuthTime.t.After(future) {
		return refuse(CodeNotYetValid, "the ID token's auth_time %s is in the future", c.AuthTime.t.UTC().Format(time.RFC3339))
	}
	if want.nonce == "" || subtle.ConstantTimeCompare([]byte(c.Nonce), []byte(want.nonce)) != 1 {
		// The nonce's value is never logged.
		return refuse(CodeWrongNonce, "the ID token's nonce isn't the one this browser's sign-in sent")
	}
	if want.fresh {
		if c.AuthTime == nil {
			return refuse(CodeNoAuthTime, "the login asked for prompt=login, and the ID token has no auth_time to show it happened")
		}
		if c.AuthTime.t.Before(want.now.Add(-FreshLoginMaxAge - ClockSkew)) {
			return refuse(CodeLoginNotFresh, "the login asked for prompt=login, and HCA last authenticated the person at %s", c.AuthTime.t.UTC().Format(time.RFC3339))
		}
	}
	return &c, nil
}

// b64 decodes a base64url segment without padding (RFC 7515 2).
func b64(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}
