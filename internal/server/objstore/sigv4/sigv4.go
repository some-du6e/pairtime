// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sigv4 signs HTTP requests with AWS Signature Version 4, the
// scheme every S3-compatible object store accepts: Cloudflare R2, MinIO,
// Hetzner Object Storage and AWS S3 itself.
//
// It follows the steps of AWS's "Create a signed AWS API request" (IAM
// User Guide): a canonical request, a string to sign, a signing key derived
// from the secret through the date, region and service, and a signature,
// carried in the Authorization header (Sign) or in the query string of a
// presigned URL (Presign). The tests check every step against AWS's own
// signing test suite, copied unchanged from awslabs/aws-c-auth v1.1.0 into
// testdata/aws-signing-test-suite; its PROVENANCE.md says how.
//
// Nothing here logs. A Credentials value never prints its secret or its
// session token, and errors never quote a header value.
package sigv4

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Algorithm is the one signing algorithm this package implements.
const Algorithm = "AWS4-HMAC-SHA256"

// EmptyPayloadHash is the hex SHA-256 of an empty body.
const EmptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// UnsignedPayload stands in for the payload hash when the body isn't
// signed, as in an S3 presigned URL.
const UnsignedPayload = "UNSIGNED-PAYLOAD"

// MaxPresignExpiry is the longest a presigned URL may stay valid.
const MaxPresignExpiry = 7 * 24 * time.Hour

// The formats of X-Amz-Date and of the date in the credential scope.
const (
	amzDateFormat = "20060102T150405Z"
	dateFormat    = "20060102"
)

// Headers that are never signed: Authorization carries the signature, and
// proxies and transports add or rewrite the others, which would break the
// signature in transit. Names are lowercase.
var unsignedHeaders = []string{
	"authorization",
	"connection",
	"expect",
	"transfer-encoding",
	"user-agent",
	"x-amzn-trace-id",
}

// Credentials are an access key pair and, for temporary credentials, a
// session token. Formatting them prints neither the secret nor the token.
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

// String says which parts are set, never what they are.
func (c Credentials) String() string {
	set := func(v string) string {
		if v == "" {
			return "unset"
		}
		return "set"
	}
	return "sigv4.Credentials{access key id " + set(c.AccessKeyID) +
		", secret " + set(c.SecretAccessKey) + ", session token " + set(c.SessionToken) + "}"
}

// GoString is String, for %#v.
func (c Credentials) GoString() string { return c.String() }

// LogValue is String, for log/slog.
func (c Credentials) LogValue() slog.Value { return slog.StringValue(c.String()) }

// Signer signs requests for one service in one region.
type Signer struct {
	Credentials Credentials
	// Region is the region in the credential scope: the bucket's region
	// on AWS, "auto" on R2, and whatever the server is configured with
	// on MinIO ("us-east-1" by default).
	Region string
	// Service is the service in the credential scope, "s3" for object
	// storage.
	Service string
	// NormalizePath removes "." and ".." segments and repeated slashes
	// from the path before signing it, as every AWS service but S3
	// expects. Leave it off for S3, whose object keys may contain them.
	NormalizePath bool
}

// signed is what one signing produced. The tests compare each part with
// AWS's test suite.
type signed struct {
	canonicalRequest string
	stringToSign     string
	signature        string
	// signedHeaders is the SignedHeaders list, such as "host;x-amz-date".
	signedHeaders string
}

// Sign signs req in place as of t. It sets X-Amz-Date, X-Amz-Security-Token
// when the credentials have a session token, and Authorization. payloadHash
// is the lowercase hex SHA-256 of the body, or UnsignedPayload; S3 also
// wants it in an X-Amz-Content-Sha256 header, which the caller sets.
//
// Every header req has when Sign runs is signed, along with the host,
// except the few in unsignedHeaders. So set them all before signing and
// change none afterwards.
func (s *Signer) Sign(req *http.Request, payloadHash string, t time.Time) error {
	_, err := s.sign(req, payloadHash, t)
	return err
}

func (s *Signer) sign(req *http.Request, payloadHash string, t time.Time) (signed, error) {
	if err := s.check(req, payloadHash); err != nil {
		return signed{}, err
	}
	t = t.UTC()
	amzDate := t.Format(amzDateFormat)
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	req.Header.Del("Authorization")
	req.Header.Set("X-Amz-Date", amzDate)
	if tok := s.Credentials.SessionToken; tok != "" {
		req.Header.Set("X-Amz-Security-Token", tok)
	}
	query, err := parseQuery(req.URL.RawQuery)
	if err != nil {
		return signed{}, err
	}
	out := s.signParts(req, query, payloadHash, t)
	req.Header.Set("Authorization", Algorithm+" Credential="+s.Credentials.AccessKeyID+"/"+s.scope(t)+
		", SignedHeaders="+out.signedHeaders+", Signature="+out.signature)
	return out, nil
}

// Presign returns req's URL with a signature in its query string that is
// valid for expires after t, between one second and MaxPresignExpiry.
// The host and every header req has are signed, as in Sign, so whoever
// uses the URL must send the same headers. payloadHash is usually
// UnsignedPayload. req itself is left unchanged.
func (s *Signer) Presign(req *http.Request, payloadHash string, t time.Time, expires time.Duration) (*url.URL, error) {
	u, _, err := s.presign(req, payloadHash, t, expires)
	return u, err
}

func (s *Signer) presign(req *http.Request, payloadHash string, t time.Time, expires time.Duration) (*url.URL, signed, error) {
	if err := s.check(req, payloadHash); err != nil {
		return nil, signed{}, err
	}
	if expires < time.Second || expires > MaxPresignExpiry || expires%time.Second != 0 {
		return nil, signed{}, fmt.Errorf("sigv4: a presigned URL must expire after a whole number of seconds from 1 s to %s, not %s", MaxPresignExpiry, expires)
	}
	query, err := parseQuery(req.URL.RawQuery)
	if err != nil {
		return nil, signed{}, err
	}
	for _, p := range query {
		if strings.HasPrefix(strings.ToLower(p.name), "x-amz-") {
			return nil, signed{}, fmt.Errorf("sigv4: the URL to presign already has the parameter %s", p.name)
		}
	}
	t = t.UTC()
	signedHeaders := strings.Join(headerNames(req), ";")
	auth := []pair{
		{"X-Amz-Algorithm", Algorithm},
		{"X-Amz-Credential", s.Credentials.AccessKeyID + "/" + s.scope(t)},
		{"X-Amz-Date", t.Format(amzDateFormat)},
		{"X-Amz-Expires", strconv.FormatInt(int64(expires/time.Second), 10)},
		{"X-Amz-SignedHeaders", signedHeaders},
	}
	if tok := s.Credentials.SessionToken; tok != "" {
		auth = append(auth, pair{"X-Amz-Security-Token", tok})
	}
	out := s.signParts(req, append(query, auth...), payloadHash, t)

	var raw strings.Builder
	raw.WriteString(req.URL.RawQuery)
	for _, p := range append(auth, pair{"X-Amz-Signature", out.signature}) {
		if raw.Len() > 0 {
			raw.WriteByte('&')
		}
		raw.WriteString(uriEncode(p.name, true) + "=" + uriEncode(p.value, true))
	}
	u := *req.URL
	u.RawQuery = raw.String()
	return &u, out, nil
}

// check refuses to sign without what a signature needs.
func (s *Signer) check(req *http.Request, payloadHash string) error {
	switch {
	case s.Credentials.AccessKeyID == "" || s.Credentials.SecretAccessKey == "":
		return errors.New("sigv4: the access key id and the secret access key are both required")
	case strings.ContainsAny(s.Credentials.AccessKeyID, "/, \t\r\n"):
		return errors.New("sigv4: the access key id contains a character the credential scope can't carry")
	case !scopePart(s.Region) || !scopePart(s.Service):
		return fmt.Errorf("sigv4: region %q and service %q must be non-empty lowercase letters, digits and '-'", s.Region, s.Service)
	case req.URL == nil || req.Host == "" && req.URL.Host == "":
		return errors.New("sigv4: the request has no host")
	case payloadHash != UnsignedPayload && !isHexSHA256(payloadHash):
		return errors.New("sigv4: the payload hash must be a lowercase hex SHA-256 or UNSIGNED-PAYLOAD")
	}
	if v := req.Header.Get("X-Amz-Content-Sha256"); v != "" && v != payloadHash {
		return errors.New("sigv4: the X-Amz-Content-Sha256 header and the payload hash differ")
	}
	return nil
}

// signParts builds the canonical request and the string to sign, and
// signs it, from req's method, path and headers and the given query.
func (s *Signer) signParts(req *http.Request, query []pair, payloadHash string, t time.Time) signed {
	return s.signNamed(req, headerNames(req), query, payloadHash, t)
}

// signNamed is signParts with the signed headers given, as a server
// verifying a request takes them from its SignedHeaders list.
func (s *Signer) signNamed(req *http.Request, names []string, query []pair, payloadHash string, t time.Time) signed {
	path := req.URL.Path
	if path == "" {
		path = "/"
	}
	if s.NormalizePath {
		path = normalizePath(path)
	}
	headers := canonicalHeaders(req, names)
	signedHeaders := strings.Join(names, ";")
	creq := strings.Join([]string{
		req.Method,
		uriEncode(path, false),
		canonicalQuery(query),
		headers,
		signedHeaders,
		payloadHash,
	}, "\n")
	sum := sha256.Sum256([]byte(creq))
	sts := Algorithm + "\n" + t.Format(amzDateFormat) + "\n" + s.scope(t) + "\n" + hex.EncodeToString(sum[:])
	key := s.signingKey(t)
	return signed{
		canonicalRequest: creq,
		stringToSign:     sts,
		signature:        hex.EncodeToString(hmacSHA256(key, sts)),
		signedHeaders:    signedHeaders,
	}
}

// scope is the credential scope: date/region/service/aws4_request.
func (s *Signer) scope(t time.Time) string {
	return t.UTC().Format(dateFormat) + "/" + s.Region + "/" + s.Service + "/aws4_request"
}

// signingKey derives the day's key from the secret, as AWS specifies.
func (s *Signer) signingKey(t time.Time) []byte {
	k := hmacSHA256([]byte("AWS4"+s.Credentials.SecretAccessKey), t.UTC().Format(dateFormat))
	k = hmacSHA256(k, s.Region)
	k = hmacSHA256(k, s.Service)
	return hmacSHA256(k, "aws4_request")
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

// host is the Host header Go's client sends for req.
func host(req *http.Request) string {
	if req.Host != "" {
		return req.Host
	}
	return req.URL.Host
}

// headerNames lists the lowercase names of the headers that get signed,
// sorted, host included.
func headerNames(req *http.Request) []string {
	names := []string{"host"}
	for k := range req.Header {
		n := strings.ToLower(k)
		if n == "host" || slices.Contains(unsignedHeaders, n) || slices.Contains(names, n) {
			continue
		}
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// canonicalHeaders returns the canonical header block: each of the named
// headers as "name:value\n", in the order of names, which is sorted. A
// header sent more than once becomes one line whose values are joined by
// commas, in the order they were added.
func canonicalHeaders(req *http.Request, names []string) string {
	values := make(map[string][]string, len(names))
	values["host"] = []string{host(req)}
	// Sorted, so values stay in a fixed order even when two keys of the
	// map differ only in case, which Header.Add never produces.
	for _, k := range slices.Sorted(maps.Keys(req.Header)) {
		if n := strings.ToLower(k); n != "host" {
			values[n] = append(values[n], req.Header[k]...)
		}
	}
	var b strings.Builder
	for _, n := range names {
		b.WriteString(n)
		b.WriteByte(':')
		for i, v := range values[n] {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(trimAll(v))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// trimAll is AWS's "Trimall": it drops the whitespace around v and turns
// each run of whitespace inside it, line breaks of a folded header
// included, into one space.
func trimAll(v string) string {
	b := make([]byte, 0, len(v))
	gap := false
	for i := 0; i < len(v); i++ {
		switch c := v[i]; c {
		case ' ', '\t', '\n', '\r', '\v', '\f':
			gap = len(b) > 0
		default:
			if gap {
				b = append(b, ' ')
				gap = false
			}
			b = append(b, c)
		}
	}
	return string(b)
}

// pair is one query parameter, decoded.
type pair struct{ name, value string }

// parseQuery splits a raw query into its decoded parameters, in order. A
// parameter without "=" has an empty value. A '+' stays a '+': this
// package writes spaces as %20, as SigV4 does.
func parseQuery(raw string) ([]pair, error) {
	var ps []pair
	for part := range strings.SplitSeq(raw, "&") {
		if part == "" {
			continue
		}
		k, v, _ := strings.Cut(part, "=")
		dk, err1 := url.PathUnescape(k)
		dv, err2 := url.PathUnescape(v)
		if err1 != nil || err2 != nil {
			return nil, errors.New("sigv4: the query string has a malformed percent-escape")
		}
		ps = append(ps, pair{dk, dv})
	}
	return ps, nil
}

// canonicalQuery encodes each parameter and sorts them by encoded name,
// then encoded value.
func canonicalQuery(ps []pair) string {
	enc := make([]pair, len(ps))
	for i, p := range ps {
		enc[i] = pair{uriEncode(p.name, true), uriEncode(p.value, true)}
	}
	slices.SortFunc(enc, func(a, b pair) int {
		if c := strings.Compare(a.name, b.name); c != 0 {
			return c
		}
		return strings.Compare(a.value, b.value)
	})
	parts := make([]string, len(enc))
	for i, p := range enc {
		parts[i] = p.name + "=" + p.value
	}
	return strings.Join(parts, "&")
}

// URIEncode percent-encodes s the way SigV4 does: every byte but the
// unreserved characters A-Z, a-z, 0-9, '-', '.', '_' and '~' becomes %XY
// with uppercase hex. A slash is kept as it is unless encodeSlash is set,
// as it is for query parameters. S3 clients build request paths with it,
// so the path they send is the one they signed.
func URIEncode(s string, encodeSlash bool) string { return uriEncode(s, encodeSlash) }

func uriEncode(s string, encodeSlash bool) string {
	const hexdigits = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' ||
			c == '-' || c == '.' || c == '_' || c == '~' || c == '/' && !encodeSlash {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hexdigits[c>>4])
		b.WriteByte(hexdigits[c&15])
	}
	return b.String()
}

// normalizePath removes empty, "." and ".." segments, as RFC 3986's
// remove_dot_segments does and with repeated slashes collapsed. A path
// that ended in a slash, ".", or ".." keeps a trailing slash.
func normalizePath(p string) string {
	segs := strings.Split(p, "/")
	out := make([]string, 0, len(segs))
	for _, s := range segs {
		switch s {
		case "", ".":
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, s)
		}
	}
	n := "/" + strings.Join(out, "/")
	if last := segs[len(segs)-1]; len(out) > 0 && (last == "" || last == "." || last == "..") {
		n += "/"
	}
	return n
}

// scopePart reports whether v can be a region or service in the scope.
func scopePart(v string) bool {
	if v == "" || len(v) > 64 {
		return false
	}
	for i := 0; i < len(v); i++ {
		if c := v[i]; !('a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// isHexSHA256 reports whether v is 64 lowercase hex digits.
func isHexSHA256(v string) bool {
	if len(v) != 2*sha256.Size {
		return false
	}
	for i := 0; i < len(v); i++ {
		if c := v[i]; !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}

// PayloadHash is the hex SHA-256 of body, for an X-Amz-Content-Sha256
// header and the payload hash of Sign.
func PayloadHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
