// SPDX-License-Identifier: AGPL-3.0-or-later

package privacy

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Issuer is the iss of every privacy hub token (DESIGN 8.9).
const Issuer = "hackclub-privacy"

// The actions, each one endpoint's.
const (
	ActionDelete = "delete"
	ActionExport = "export"
)

// The token's time rules (DESIGN 8.9).
const (
	// MaxLifetime is the longest a token may live, exp minus iat.
	MaxLifetime = 300 * time.Second
	// Leeway is how far exp, iat and nbf may be off the server's clock.
	Leeway = 30 * time.Second
)

// The bounds this package holds a token to before it acts.
const (
	// MaxTokenSize bounds the token in the Authorization header. A hub
	// token is under a kilobyte.
	MaxTokenSize = 8 << 10
	// MaxIDSize is the most bytes a jti or a request_id may have, each
	// printable ASCII (D-sv13-jti).
	MaxIDSize = 128
	// MaxEmails bounds subject.emails.
	MaxEmails = 64
	// maxEmailSize is the longest address RFC 5321 allows.
	maxEmailSize = 320
	// maxSlackIDSize bounds subject.slack_id.
	maxSlackIDSize = 64
	// maxIdentitySize bounds subject.identity_id, as accounts.CheckSub
	// bounds an HCA subject.
	maxIdentitySize = 255
	// maxKIDSize bounds the header's kid.
	maxKIDSize = 256
)

// Error is a request the endpoints refuse: the HTTP status and the
// snake_case code the answer carries as {"error": code}, which the hub
// shows its staff as http_<status>_<code>. A 503 is one the hub retries,
// with a fresh token.
type Error struct {
	Status int
	Code   string
	// Reason is why a manual_review_required request needs a person.
	Reason string
}

func (e *Error) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("privacy: %d %s: %s", e.Status, e.Code, e.Reason)
	}
	return fmt.Sprintf("privacy: %d %s", e.Status, e.Code)
}

// badToken is a 401: a token that isn't one the hub signed for this server
// and this moment.
func badToken(code string) *Error { return &Error{Status: http.StatusUnauthorized, Code: code} }

// Claims are a verified token's claims (DESIGN 8.9).
type Claims struct {
	// JTI is the token's id, which a server takes once.
	JTI string
	// RequestID names the person's request at the hub. It is the one
	// value of a token that pairtimed logs, and its answer echoes it.
	RequestID string
	// Action is ActionDelete or ActionExport, the endpoint's.
	Action string
	// DryRun is true for the hub's first, practice call, which changes
	// nothing.
	DryRun bool
	// Subject names the person.
	Subject Subject
	// IssuedAt and Expires are iat and exp.
	IssuedAt, Expires time.Time
}

// Subject names the person a request is about, as the hub knows them: the
// email addresses it holds, which it lowercases, and their Slack id and
// Hack Club Auth identity, such as "ident!AbC123", when it has them.
type Subject struct {
	Emails     []string
	SlackID    string
	IdentityID string
}

// Keys gives the hub's public key a token's kid names.
type Keys interface {
	// Key returns kid's key, or an *Error: 401 unknown_key for a kid
	// the hub doesn't publish, and a 503 when its keys can't be fetched.
	Key(ctx context.Context, kid string) (*ecdsa.PublicKey, error)
}

// Verifier checks privacy hub tokens: ES256 JWTs (RFC 7519) signed with a
// key the hub publishes, for this server's audience (DESIGN 8.9).
type Verifier struct {
	// Audience is this server's aud, as the hub's staff registered it:
	// privacy_hub.aud.
	Audience string
	// Keys gives the hub's keys.
	Keys Keys
}

// b64 is the JWS encoding: base64url without padding, refusing any
// character outside the alphabet and any bits left over (RFC 7515 2).
var b64 = base64.RawURLEncoding.Strict()

// Verify checks token for the endpoint of action at now and returns its
// claims. It refuses, as a 401, a token that isn't a JWS of three parts, a
// header other than alg ES256 and typ JWT with a kid, a signature that
// doesn't verify under the key the kid names, iss other than Issuer, an
// aud that isn't Audience, a token expired more than Leeway ago, issued
// or not valid until more than Leeway from now, or living longer than
// MaxLifetime, and claims of the wrong type or past their bounds. A token
// for the other endpoint's action is a 400 action_mismatch. A key it
// can't fetch is the 503 Keys gives.
func (v *Verifier) Verify(ctx context.Context, token, action string, now time.Time) (Claims, error) {
	if v.Audience == "" || v.Keys == nil {
		return Claims{}, errors.New("privacy: a verifier needs an audience and the hub's keys")
	}
	if len(token) > MaxTokenSize {
		return Claims{}, badToken("malformed_token")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, badToken("malformed_token")
	}
	headerJSON, err := b64.DecodeString(parts[0])
	if err != nil {
		return Claims{}, badToken("malformed_token")
	}
	var header struct {
		Alg  *string         `json:"alg"`
		Typ  *string         `json:"typ"`
		Kid  *string         `json:"kid"`
		Crit json.RawMessage `json:"crit"`
	}
	if err := decodeObject(headerJSON, &header); err != nil {
		return Claims{}, badToken("malformed_token")
	}
	switch {
	case header.Alg == nil || *header.Alg != "ES256":
		return Claims{}, badToken("unsupported_alg")
	case header.Typ == nil || *header.Typ != "JWT":
		return Claims{}, badToken("invalid_typ")
	case header.Kid == nil || *header.Kid == "" || len(*header.Kid) > maxKIDSize:
		return Claims{}, badToken("missing_kid")
	case header.Crit != nil:
		// RFC 7515 4.1.11: an extension the reader doesn't know refuses
		// the token, and this one knows none.
		return Claims{}, badToken("unsupported_crit")
	}
	sig, err := b64.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		return Claims{}, badToken("invalid_signature")
	}
	key, err := v.Keys.Key(ctx, *header.Kid)
	if err != nil {
		return Claims{}, err
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !verifyES256(key, digest[:], sig) {
		return Claims{}, badToken("invalid_signature")
	}
	payload, err := b64.DecodeString(parts[1])
	if err != nil {
		return Claims{}, badToken("malformed_token")
	}
	return v.claims(payload, action, now)
}

// verifyES256 checks sig, JWS's 64 bytes of R and S, each a 32-byte
// big-endian integer (RFC 7518 3.4), over digest with key.
func verifyES256(key *ecdsa.PublicKey, digest, sig []byte) bool {
	der, err := asn1.Marshal(struct{ R, S *big.Int }{new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])})
	if err != nil {
		return false
	}
	return ecdsa.VerifyASN1(key, digest, der)
}

// claims checks a signed payload's claims for action at now.
func (v *Verifier) claims(payload []byte, action string, now time.Time) (Claims, error) {
	var p struct {
		Iss       *string         `json:"iss"`
		Aud       json.RawMessage `json:"aud"`
		Iat       json.RawMessage `json:"iat"`
		Exp       json.RawMessage `json:"exp"`
		Nbf       json.RawMessage `json:"nbf"`
		JTI       *string         `json:"jti"`
		RequestID *string         `json:"request_id"`
		Action    *string         `json:"action"`
		DryRun    *bool           `json:"dry_run"`
		Subject   *struct {
			Emails     *[]string `json:"emails"`
			SlackID    *string   `json:"slack_id"`
			IdentityID *string   `json:"identity_id"`
		} `json:"subject"`
	}
	if err := decodeObject(payload, &p); err != nil {
		return Claims{}, badToken("invalid_claims")
	}
	if p.Iss == nil || *p.Iss != Issuer {
		return Claims{}, badToken("invalid_issuer")
	}
	if !hasAudience(p.Aud, v.Audience) {
		return Claims{}, badToken("invalid_audience")
	}
	iat, okIat := numericDate(p.Iat)
	exp, okExp := numericDate(p.Exp)
	if !okIat || !okExp {
		return Claims{}, badToken("invalid_claims")
	}
	switch {
	case now.After(exp.Add(Leeway)):
		return Claims{}, badToken("expired")
	case iat.After(now.Add(Leeway)):
		return Claims{}, badToken("not_yet_valid")
	case exp.Before(iat) || exp.Sub(iat) > MaxLifetime:
		return Claims{}, badToken("invalid_lifetime")
	}
	if p.Nbf != nil {
		nbf, ok := numericDate(p.Nbf)
		if !ok {
			return Claims{}, badToken("invalid_claims")
		}
		if nbf.After(now.Add(Leeway)) {
			return Claims{}, badToken("not_yet_valid")
		}
	}
	if p.JTI == nil || !printableID(*p.JTI) || p.RequestID == nil || !printableID(*p.RequestID) ||
		p.Action == nil || p.DryRun == nil || p.Subject == nil || p.Subject.Emails == nil {
		return Claims{}, badToken("invalid_claims")
	}
	if *p.Action != action {
		return Claims{}, &Error{Status: http.StatusBadRequest, Code: "action_mismatch"}
	}
	c := Claims{JTI: *p.JTI, RequestID: *p.RequestID, Action: action, DryRun: *p.DryRun, IssuedAt: iat, Expires: exp}
	emails := *p.Subject.Emails
	if len(emails) > MaxEmails {
		return Claims{}, badToken("invalid_claims")
	}
	for _, e := range emails {
		if e == "" || len(e) > maxEmailSize {
			return Claims{}, badToken("invalid_claims")
		}
	}
	c.Subject.Emails = emails
	if s := p.Subject.SlackID; s != nil {
		if len(*s) > maxSlackIDSize {
			return Claims{}, badToken("invalid_claims")
		}
		c.Subject.SlackID = *s
	}
	if s := p.Subject.IdentityID; s != nil {
		if len(*s) > maxIdentitySize {
			return Claims{}, badToken("invalid_claims")
		}
		c.Subject.IdentityID = *s
	}
	return c, nil
}

// decodeObject decodes one JSON object into v, and nothing after it.
func decodeObject(data []byte, v any) error {
	if len(bytes.TrimSpace(data)) == 0 || bytes.TrimSpace(data)[0] != '{' {
		return errors.New("not a JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("data after the JSON object")
	}
	return nil
}

// hasAudience reports whether aud, a string or an array of strings, names
// want (RFC 7519 4.1.3).
func hasAudience(aud json.RawMessage, want string) bool {
	var one string
	if err := json.Unmarshal(aud, &one); err == nil {
		return one == want
	}
	var many []string
	if err := json.Unmarshal(aud, &many); err != nil {
		return false
	}
	for _, a := range many {
		if a == want {
			return true
		}
	}
	return false
}

// numericDate reads an RFC 7519 NumericDate: a JSON number of seconds since
// 1970, which may have a fraction. A string, a negative number and one past
// the year 5138 aren't one.
func numericDate(raw json.RawMessage) (time.Time, bool) {
	s := strings.TrimSpace(string(raw))
	if s == "" || (s[0] != '-' && (s[0] < '0' || s[0] > '9')) {
		return time.Time{}, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 || f > 1e11 || math.IsNaN(f) {
		return time.Time{}, false
	}
	sec, frac := math.Modf(f)
	return time.Unix(int64(sec), int64(frac*1e9)), true
}

// printableID reports whether id is 1 to MaxIDSize bytes of printable
// ASCII, 0x20 to 0x7e (D-sv13-jti).
func printableID(id string) bool {
	if id == "" || len(id) > MaxIDSize {
		return false
	}
	for i := 0; i < len(id); i++ {
		if id[i] < 0x20 || id[i] > 0x7e {
			return false
		}
	}
	return true
}
