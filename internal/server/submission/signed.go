// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/server/backup"
	"github.com/some-du6e/pairtime/internal/server/device"
	"github.com/some-du6e/pairtime/internal/server/ingest"
)

// signatureContext starts the message a device's submissions request
// signs (D-sb1-submissions, Q4). It differs from ingest's, so neither
// kind of signature passes for the other.
const signatureContext = "pairtime-submissions-v1\n"

// Message returns M, the bytes a device's request to the submissions API
// signs with the device key (docs/spec/submissions-v1.md 2): ingest-v1
// 2.2's message with pairtime-submissions-v1 in place of
// pairtime-ingest-v1 and, in place of the request counter, the method, a
// space and the request target, its path and query, exactly as sent:
//
//	"pairtime-submissions-v1\n" || device_id || "\n" || method || " " || target || "\n" || time || "\n" || hex(SHA-256(raw body))
//
// A GET or a withdrawal signs the SHA-256 of an empty body. No request
// counter is sent or moved: a create and a withdrawal are idempotent, so a
// replay within the 300 s the clock allows gets the first request's answer
// and changes nothing.
func Message(deviceID, method, target, time, bodySHA256 string) []byte {
	m := make([]byte, 0, len(signatureContext)+len(deviceID)+len(method)+len(target)+len(time)+len(bodySHA256)+4)
	m = append(m, signatureContext...)
	m = append(m, deviceID...)
	m = append(m, '\n')
	m = append(m, method...)
	m = append(m, ' ')
	m = append(m, target...)
	m = append(m, '\n')
	m = append(m, time...)
	m = append(m, '\n')
	return append(m, bodySHA256...)
}

// refusal is a request refused as a whole, with its status and the body
// docs/spec/submissions-v1.md 1 gives it.
type refusal struct {
	status       int
	code, detail string
	// retryAfter, when set, is the Retry-After in seconds.
	retryAfter int
	// deviceKeyID names the device's key, on signature_invalid.
	deviceKeyID string
	// loginURL is where a web session gets a fresh login, on
	// fresh_login_required.
	loginURL string
	// submissions are the submissions the overlap refusal found.
	submissions []Ref
}

func refuse(status int, code, detail string) *refusal {
	return &refusal{status: status, code: code, detail: detail}
}

// unavailable is the refusal of a request the server couldn't read or
// write, which the client sends again after Retry-After: 5.
func unavailable() *refusal {
	return &refusal{status: http.StatusServiceUnavailable, code: ingest.CodeUnavailable, retryAfter: 5,
		detail: "The server couldn't do that just now; send it again in 5 seconds."}
}

// single returns the one value of header name, or false when it is absent
// or repeated.
func single(h http.Header, name string) (string, bool) {
	vs := h.Values(name)
	if len(vs) != 1 {
		return "", false
	}
	return vs[0], true
}

// decimal parses a decimal integer with no sign and no leading zeros that
// fits an int64, as ingest-v1 2.1 writes X-Pairtime-Time.
func decimal(s string) (int64, bool) {
	if s == "" || len(s) > 19 || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	v, err := strconv.ParseInt(s, 10, 64)
	return v, err == nil
}

// isHex reports whether s is n lowercase hex digits.
func isHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}

// signaturePrefix starts X-Pairtime-Signature's value.
const signaturePrefix = "ed25519="

// parseSignature reads "ed25519=" and a signature in padded standard
// base64, in its one canonical spelling, as ingest does.
func parseSignature(v string) ([]byte, bool) {
	b64, ok := strings.CutPrefix(v, signaturePrefix)
	if !ok || len(b64) != base64.StdEncoding.EncodedLen(ed25519.SignatureSize) {
		return nil, false
	}
	sig, err := base64.StdEncoding.Strict().DecodeString(b64)
	if err != nil || len(sig) != ed25519.SignatureSize || base64.StdEncoding.EncodeToString(sig) != b64 {
		return nil, false
	}
	return sig, true
}

// token finds the device of r's bearer token, as ingest-v1 2.5 step 2
// does: token_missing, token_invalid, device_revoked, or 503 when the
// device store fails.
func (s *Service) token(r *http.Request) (device.Device, *refusal) {
	auth, ok := single(r.Header, "Authorization")
	scheme, tok, found := strings.Cut(auth, " ")
	tok = strings.TrimSpace(tok)
	if !ok || !found || !strings.EqualFold(scheme, "Bearer") || tok == "" {
		return device.Device{}, refuse(http.StatusUnauthorized, ingest.CodeTokenMissing, "Send the device token as Authorization: Bearer ptd_...")
	}
	dev, err := s.o.Devices.Authenticate(r.Context(), tok)
	switch {
	case errors.Is(err, device.ErrBadToken), errors.Is(err, device.ErrUnknownToken):
		return device.Device{}, refuse(http.StatusUnauthorized, ingest.CodeTokenInvalid, "The device token fails its checksum or names no device; sign in again with pairtime login.")
	case errors.Is(err, device.ErrRevoked), err == nil && dev.Revoked:
		return device.Device{}, refuse(http.StatusForbidden, ingest.CodeDeviceRevoked, "The device was removed or linked again, or its account can no longer sign in; pairtime login links it again.")
	case err != nil:
		s.o.Logger.Error("submission: the device store failed", "err", err)
		return device.Device{}, unavailable()
	}
	return dev, nil
}

// signed authenticates a device's request to the submissions API and
// reads its body, at most maxBody bytes (docs/spec/submissions-v1.md 2):
// the token, X-Pairtime-Device, the device's key, the forms of
// X-Pairtime-Time, X-Pairtime-Body-SHA256 and X-Pairtime-Signature, the
// clock, the body and its hash, and the signature over Message, in
// ingest-v1 2.5's order and with its codes.
func (s *Service) signed(w http.ResponseWriter, r *http.Request) (device.Device, []byte, *refusal) {
	dev, ref := s.token(r)
	if ref != nil {
		return device.Device{}, nil, ref
	}
	if id, ok := single(r.Header, ingest.HeaderDevice); !ok || id != dev.ID {
		return device.Device{}, nil, refuse(http.StatusForbidden, ingest.CodeDeviceMismatch, "X-Pairtime-Device must be the device the token belongs to.")
	}
	if len(dev.PublicKey) != ed25519.PublicKeySize {
		return device.Device{}, nil, refuse(http.StatusForbidden, ingest.CodeDeviceKeyMissing, "The device has no key on record; sign in again with pairtime login.")
	}
	timeText, ok := single(r.Header, ingest.HeaderTime)
	var t int64
	if ok {
		t, ok = decimal(timeText)
	}
	if !ok {
		return device.Device{}, nil, refuse(http.StatusBadRequest, ingest.CodeTimeMalformed, "X-Pairtime-Time must be one decimal integer of Unix milliseconds, without a sign or leading zeros.")
	}
	bodySHA, ok := single(r.Header, ingest.HeaderBodySHA256)
	if !ok || !isHex(bodySHA, 64) {
		return device.Device{}, nil, refuse(http.StatusBadRequest, ingest.CodeBodyHashMalformed, "X-Pairtime-Body-SHA256 must be 64 lowercase hex digits.")
	}
	sigText, ok := single(r.Header, ingest.HeaderSignature)
	var sig []byte
	if ok {
		sig, ok = parseSignature(sigText)
	}
	if !ok {
		return device.Device{}, nil, refuse(http.StatusBadRequest, ingest.CodeSignatureMalformed, "X-Pairtime-Signature must be ed25519= and a 64-byte signature in padded base64.")
	}
	if skew := t - s.o.Now().UnixMilli(); skew > ingest.MaxSkewMS || skew < -ingest.MaxSkewMS {
		return device.Device{}, nil, refuse(http.StatusUnauthorized, ingest.CodeClockSkew, "X-Pairtime-Time is "+strconv.FormatInt(skew, 10)+" ms from the server's clock; correct the clock from this response's X-Pairtime-Time and sign again.")
	}
	body, ref := readBody(w, r)
	if ref != nil {
		return device.Device{}, nil, ref
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != bodySHA {
		return device.Device{}, nil, refuse(http.StatusBadRequest, ingest.CodeBodyHashMismatch, "The body's SHA-256 isn't X-Pairtime-Body-SHA256.")
	}
	if !ed25519.Verify(dev.PublicKey, Message(dev.ID, r.Method, target(r), timeText, bodySHA), sig) {
		ref := refuse(http.StatusUnauthorized, ingest.CodeSignatureInvalid, "The signature doesn't verify under the device's key; device_key_id names that key.")
		ref.deviceKeyID = backup.KeyID(dev.PublicKey)
		return device.Device{}, nil, ref
	}
	return dev, body, nil
}

// target is r's request target as the request line sent it: the path and,
// when there is one, "?" and the query.
func target(r *http.Request) string {
	if r.RequestURI != "" {
		return r.RequestURI
	}
	return r.URL.RequestURI()
}

// maxBody bounds a request's body. A create names at most MaxProjects
// projects, each by an id or key of at most 128 bytes.
const maxBody = 16 << 10

// readBody reads r's body, at most maxBody bytes, which has
// ingest.BodyTimeout to arrive once the headers are in, as at ingest.
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, *refusal) {
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(ingest.BodyTimeout))
	tooLarge := refuse(http.StatusRequestEntityTooLarge, ingest.CodeBodyTooLarge, "The body is over 16384 bytes, more than any request to this API needs.")
	if r.ContentLength > maxBody {
		return nil, tooLarge
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	switch {
	case len(body) > maxBody:
		return nil, tooLarge
	case err != nil:
		return nil, refuse(http.StatusBadRequest, ingest.CodeBodyUnreadable, "The body couldn't be read, as when the connection broke off; send the same request again.")
	}
	return body, nil
}
