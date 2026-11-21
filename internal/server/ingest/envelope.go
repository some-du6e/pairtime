// SPDX-License-Identifier: AGPL-3.0-or-later

package ingest

import (
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The headers of ingest-v1 2.1.
const (
	HeaderIdempotencyKey = "Idempotency-Key"
	HeaderDevice         = "X-Pairtime-Device"
	HeaderReq            = "X-Pairtime-Req"
	HeaderTime           = "X-Pairtime-Time"
	HeaderBodySHA256     = "X-Pairtime-Body-SHA256"
	HeaderSignature      = "X-Pairtime-Signature"
	// HeaderReplay marks a response the server stored and sent again
	// (ingest-v1 2.10).
	HeaderReplay = "X-Pairtime-Replay"
)

// signatureContext starts the signed message (DESIGN 4.5).
const signatureContext = "pairtime-ingest-v1\n"

// signaturePrefix starts X-Pairtime-Signature's value.
const signaturePrefix = "ed25519="

// Message returns M, the bytes an ingest request's signature covers
// (ingest-v1 2.2; DESIGN 4.5):
//
//	"pairtime-ingest-v1\n" || device_id || "\n" || req || "\n" || time || "\n" || hex(SHA-256(raw body))
//
// req and time are the decimal text of X-Pairtime-Req and X-Pairtime-Time
// as sent, and bodySHA256 the 64 lowercase hex digits of
// X-Pairtime-Body-SHA256.
func Message(deviceID, req, time, bodySHA256 string) []byte {
	m := make([]byte, 0, len(signatureContext)+len(deviceID)+len(req)+len(time)+len(bodySHA256)+3)
	m = append(m, signatureContext...)
	m = append(m, deviceID...)
	m = append(m, '\n')
	m = append(m, req...)
	m = append(m, '\n')
	m = append(m, time...)
	m = append(m, '\n')
	return append(m, bodySHA256...)
}

// envelope is a request's signed headers, each checked for its form.
type envelope struct {
	key       string
	req       int64
	reqText   string
	time      int64
	timeText  string
	bodySHA   string
	signature []byte
}

// refusal is a request refused as a whole (ingest-v1 2.6).
type refusal struct {
	code   string
	detail string
	// retryAfter, when set, is the Retry-After in seconds.
	retryAfter int
	// deviceKeyID names the device's key, on signature_invalid.
	deviceKeyID string
}

func refuse(code, detail string) *refusal { return &refusal{code: code, detail: detail} }

// single returns the one value of header name, or false when it is absent
// or repeated.
func single(h http.Header, name string) (string, bool) {
	vs := h.Values(name)
	if len(vs) != 1 {
		return "", false
	}
	return vs[0], true
}

// parseEnvelope checks the five signed headers' forms, in ingest-v1 2.5
// step 5's order.
func parseEnvelope(h http.Header) (envelope, *refusal) {
	var e envelope
	var ok bool
	if e.key, ok = single(h, HeaderIdempotencyKey); !ok || !isUUIDv7(e.key) {
		return e, refuse(CodeIdempotencyKeyMalformed, "Idempotency-Key must be one lowercase UUIDv7.")
	}
	if e.reqText, ok = single(h, HeaderReq); ok {
		e.req, ok = decimal(e.reqText)
	}
	if !ok || e.req < 1 {
		return e, refuse(CodeCounterMalformed, "X-Pairtime-Req must be one decimal integer from 1 to 9223372036854775807, without a sign or leading zeros.")
	}
	var ref *refusal
	if e.timeText, e.time, ref = parseTime(h); ref != nil {
		return e, ref
	}
	if e.bodySHA, ok = single(h, HeaderBodySHA256); !ok || !isHex(e.bodySHA, 64) {
		return e, refuse(CodeBodyHashMalformed, "X-Pairtime-Body-SHA256 must be 64 lowercase hex digits.")
	}
	if e.signature, ref = parseSignatureHeader(h); ref != nil {
		return e, ref
	}
	return e, nil
}

// parseTime reads X-Pairtime-Time, as text and as Unix ms.
func parseTime(h http.Header) (string, int64, *refusal) {
	text, ok := single(h, HeaderTime)
	var t int64
	if ok {
		t, ok = decimal(text)
	}
	if !ok {
		return "", 0, refuse(CodeTimeMalformed, "X-Pairtime-Time must be one decimal integer of Unix milliseconds, without a sign or leading zeros.")
	}
	return text, t, nil
}

// parseSignatureHeader reads X-Pairtime-Signature.
func parseSignatureHeader(h http.Header) ([]byte, *refusal) {
	v, ok := single(h, HeaderSignature)
	var sig []byte
	if ok {
		sig, ok = parseSignature(v)
	}
	if !ok {
		return nil, refuse(CodeSignatureMalformed, "X-Pairtime-Signature must be ed25519= and a 64-byte signature in padded base64.")
	}
	return sig, nil
}

// stateContext starts the message a GET /api/v1/ingest/state request
// signs (ingest-v1 3). It differs from an upload's, so neither kind of
// signature passes for the other.
const stateContext = "pairtime-ingest-state-v1\n"

// StateMessage returns M_state, the bytes a GET /api/v1/ingest/state
// request's signature covers (ingest-v1 3):
//
//	"pairtime-ingest-state-v1\n" || device_id || "\n" || time
func StateMessage(deviceID, time string) []byte {
	m := make([]byte, 0, len(stateContext)+len(deviceID)+1+len(time))
	m = append(m, stateContext...)
	m = append(m, deviceID...)
	m = append(m, '\n')
	return append(m, time...)
}

// decimal parses a decimal integer with no sign and no leading zeros that
// fits an int64.
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

// parseSignature reads "ed25519=" and a signature in padded standard
// base64, in its one canonical spelling.
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

// isUUIDv7 reports whether s is a UUIDv7 in lowercase canonical form
// (RFC 9562 sections 4 and 5.7).
func isUUIDv7(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
				return false
			}
		}
	}
	return s[14] == '7' && strings.IndexByte("89ab", s[19]) >= 0
}

// checkContent checks Content-Type and Content-Encoding (ingest-v1 2.5
// step 6).
func checkContent(h http.Header) *refusal {
	ct, ok := single(h, "Content-Type")
	if ok {
		mt, params, err := mime.ParseMediaType(ct)
		ok = err == nil && mt == "application/json"
		for k, v := range params {
			if k != "charset" || !strings.EqualFold(v, "utf-8") {
				ok = false
			}
		}
	}
	if !ok {
		return refuse(CodeContentTypeUnsupported, "Content-Type must be application/json.")
	}
	if ce, ok := single(h, "Content-Encoding"); !ok || !strings.EqualFold(strings.TrimSpace(ce), "gzip") {
		return refuse(CodeEncodingUnsupported, "Content-Encoding must be gzip.")
	}
	return nil
}

// readRaw reads the raw body, at most MaxRawBody bytes. A Content-Length
// over the limit is refused before anything is read, and a body without
// one is read to one byte past the limit (ingest-v1 2.4).
func readRaw(r *http.Request) ([]byte, *refusal) {
	tooLarge := refuse(CodeBodyTooLarge, "The body is over 262144 bytes; split the upload.")
	if r.ContentLength > MaxRawBody {
		return nil, tooLarge
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, MaxRawBody+1))
	switch {
	case len(raw) > MaxRawBody:
		return nil, tooLarge
	case err != nil:
		return nil, refuse(CodeBodyUnreadable, "The body couldn't be read.")
	}
	return raw, nil
}

// inflate decodes the raw body: exactly one gzip member, with nothing
// after it, inflating to at most MaxInflatedBody bytes. It stops one byte
// past the limit, so a gzip bomb costs at most that much output.
func inflate(raw []byte) ([]byte, *refusal) {
	invalid := refuse(CodeGzipInvalid, "The body must be one gzip member with nothing after it.")
	src := bytes.NewReader(raw)
	zr, err := gzip.NewReader(src)
	if err != nil {
		return nil, invalid
	}
	zr.Multistream(false)
	out, err := io.ReadAll(io.LimitReader(zr, MaxInflatedBody+1))
	switch {
	case len(out) > MaxInflatedBody:
		return nil, refuse(CodeDecompressedTooLarge, "The body inflates past 4194304 bytes; split the upload.")
	case err != nil:
		return nil, invalid
	}
	// gzip.Reader reads a *bytes.Reader byte by byte, as a flate.Reader,
	// so what is left in src is exactly what follows the member.
	if src.Len() != 0 {
		return nil, invalid
	}
	return out, nil
}

// body is the inflated body's four members (ingest-v1 2.3), each item
// still raw for the shape check.
type body struct {
	batches []json.RawMessage
	buckets []json.RawMessage
	commits []json.RawMessage
	labels  json.RawMessage
}

// errShape is a top-level member that isn't in ingest-v1 2.3, or of the
// wrong JSON type.
var errShape = errors.New("ingest: body shape")

// parseBody reads the inflated body (ingest-v1 2.5 step 12). It reads the
// object's members in order and refuses the first that fails: a member
// outside 2.3, of the wrong type or there twice is body_shape, and an array
// is refused for its count at its first item past the limit, so a body of
// millions of tiny items costs no more than the limits allow.
func parseBody(data []byte) (body, *refusal) {
	var b body
	if !utf8.Valid(data) || !json.Valid(data) {
		return b, refuse(CodeJSONInvalid, "The body must be one JSON object in valid UTF-8.")
	}
	shape := refuse(CodeBodyShape, "The body is an object with batches, buckets and commits as arrays and labels as an object, each at most once.")
	if jsonKind(data) != '{' {
		return b, shape
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return b, shape
	}
	seen := map[string]bool{}
	for dec.More() {
		t, err := dec.Token()
		name, ok := t.(string)
		if err != nil || !ok || seen[name] {
			return body{}, shape
		}
		seen[name] = true
		var tooMany *refusal
		switch name {
		case "batches":
			b.batches, err = topArray(dec, MaxBatches)
			tooMany = refuse(CodeTooManyBatches, "A request holds at most 2000 batch roots; split the upload.")
		case "buckets":
			b.buckets, err = topArray(dec, MaxBuckets)
			tooMany = refuse(CodeTooManyBuckets, "A request holds at most 2000 minute buckets; split the upload.")
		case "commits":
			b.commits, err = topArray(dec, MaxCommits)
			tooMany = refuse(CodeTooManyCommits, "A request holds at most 500 commit records; split the upload.")
		case "labels":
			var raw json.RawMessage
			if err = dec.Decode(&raw); err != nil {
				break
			}
			switch jsonKind(raw) {
			case 'n':
			case '{':
				// An empty object means no labels, as an absent member
				// does (ingest-v1 2.3).
				some := false
				if !eachMember(raw, func(string, json.RawMessage) bool { some = true; return false }) {
					err = errShape
				} else if some {
					b.labels = raw
				}
			default:
				err = errShape
			}
		default:
			err = errShape
		}
		switch {
		case errors.Is(err, errTooMany):
			return body{}, tooMany
		case err != nil:
			return body{}, shape
		}
	}
	return b, nil
}

// errTooMany is a top-level array with more items than its limit.
var errTooMany = errors.New("ingest: too many items")

// topArray reads a top-level array member from dec, where null means
// none. It stops at item max + 1 with errTooMany, so it never holds more
// than max items.
func topArray(dec *json.Decoder, max int) ([]json.RawMessage, error) {
	t, err := dec.Token()
	switch {
	case err != nil:
		return nil, errShape
	case t == nil:
		return nil, nil
	case t != json.Delim('['):
		return nil, errShape
	}
	var items []json.RawMessage
	for dec.More() {
		if len(items) == max {
			return nil, errTooMany
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, errShape
		}
		items = append(items, v)
	}
	if t, err := dec.Token(); err != nil || t != json.Delim(']') {
		return nil, errShape
	}
	return items, nil
}
