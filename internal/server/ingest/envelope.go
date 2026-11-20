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
	if e.timeText, ok = single(h, HeaderTime); ok {
		e.time, ok = decimal(e.timeText)
	}
	if !ok {
		return e, refuse(CodeTimeMalformed, "X-Pairtime-Time must be one decimal integer of Unix milliseconds, without a sign or leading zeros.")
	}
	if e.bodySHA, ok = single(h, HeaderBodySHA256); !ok || !isHex(e.bodySHA, 64) {
		return e, refuse(CodeBodyHashMalformed, "X-Pairtime-Body-SHA256 must be 64 lowercase hex digits.")
	}
	sig, ok := single(h, HeaderSignature)
	if ok {
		e.signature, ok = parseSignature(sig)
	}
	if !ok {
		return e, refuse(CodeSignatureMalformed, "X-Pairtime-Signature must be ed25519= and a 64-byte signature in padded base64.")
	}
	return e, nil
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

// parseBody reads the inflated body (ingest-v1 2.5 step 12).
func parseBody(data []byte) (body, *refusal) {
	var b body
	if !utf8.Valid(data) || !json.Valid(data) {
		return b, refuse(CodeJSONInvalid, "The body must be one JSON object in valid UTF-8.")
	}
	shape := refuse(CodeBodyShape, "The body is an object with only batches, buckets and commits as arrays and labels as an object.")
	if jsonKind(data) != '{' {
		return b, shape
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return b, shape
	}
	for name, raw := range top {
		var err error
		switch name {
		case "batches":
			b.batches, err = topArray(raw)
		case "buckets":
			b.buckets, err = topArray(raw)
		case "commits":
			b.commits, err = topArray(raw)
		case "labels":
			switch jsonKind(raw) {
			case 'n':
			case '{':
				b.labels = raw
			default:
				err = errShape
			}
		default:
			err = errShape
		}
		if err != nil {
			return b, shape
		}
	}
	switch {
	case len(b.batches) > MaxBatches:
		return b, refuse(CodeTooManyBatches, "A request holds at most 2000 batch roots; split the upload.")
	case len(b.buckets) > MaxBuckets:
		return b, refuse(CodeTooManyBuckets, "A request holds at most 2000 minute buckets; split the upload.")
	case len(b.commits) > MaxCommits:
		return b, refuse(CodeTooManyCommits, "A request holds at most 500 commit records; split the upload.")
	}
	return b, nil
}

// topArray reads a top-level array member, where null means none.
func topArray(raw json.RawMessage) ([]json.RawMessage, error) {
	switch jsonKind(raw) {
	case 'n':
		return nil, nil
	case '[':
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, errShape
		}
		return items, nil
	}
	return nil, errShape
}
