// SPDX-License-Identifier: AGPL-3.0-or-later

// Package objstore is pairtimed's client for S3-compatible object storage:
// Cloudflare R2 for the pilot, Hetzner Object Storage or AWS S3 after a
// move, MinIO in CI (DESIGN 10.1, 10.3, 10.5 item 4). It makes the few S3
// calls pairtimed needs, on the standard library, signed with SigV4
// (package sigv4):
//
//   - PutObject, GetObject, HeadObject, DeleteObject and ListObjectsV2;
//   - write-once puts with If-None-Match: *, which the evidence store uses
//     for sealed blobs keyed by their SHA-256 under evidence/ (Evidence;
//     DESIGN 7.6, 10.1);
//   - the nightly snapshot bucket, a second bucket with a 30-day
//     lifecycle for age-encrypted database snapshots (Snapshots; DESIGN
//     10.3);
//   - a readiness check for /readyz (Bucket.Check; DESIGN 10.4).
//
// Requests use path-style URLs, https://<endpoint>/<bucket>/<key>, unless
// the configuration asks for virtual-hosted ones. A single put is capped
// at 5 GiB, as S3 and R2 cap PutObject; there is no multipart upload yet.
//
// The package never logs. Its errors name the call, the bucket, the key,
// the HTTP status and the S3 error code, and never a credential, a
// signature or a header value.
package objstore

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/server/objstore/sigv4"
)

// Errors that the S3 calls wrap, for errors.Is.
var (
	// ErrNotFound: there is no object under the key. A HEAD can't tell a
	// missing key from a missing bucket, since its answer has no body, so
	// a HEAD of a missing bucket is ErrNotFound too.
	ErrNotFound = errors.New("objstore: no such key")
	// ErrNoSuchBucket: the bucket doesn't exist at the endpoint.
	ErrNoSuchBucket = errors.New("objstore: no such bucket")
	// ErrExists: a write-once put found the key already there, and the
	// store kept what it had.
	ErrExists = errors.New("objstore: the key already exists")
	// ErrTooLarge: the body is bigger than one PutObject may be.
	ErrTooLarge = errors.New("objstore: too large for a single put")
)

// MaxPutSize is the largest body one PutObject takes on S3 and R2: 5 GiB.
const MaxPutSize = 5 << 30

// DefaultRegion is R2's region, which it asks every client to sign with.
const DefaultRegion = "auto"

// DefaultMaxAttempts is how many times a call is tried when it fails in a
// way worth retrying: no answer, a 5xx, or S3's SlowDown.
const DefaultMaxAttempts = 3

// userAgent identifies pairtimed to the store. It isn't signed.
const userAgent = "pairtimed-objstore (+https://github.com/some-du6e/pairtime)"

// Config says how to reach the store.
type Config struct {
	// Endpoint is the S3 API's origin, such as
	// https://<account id>.r2.cloudflarestorage.com. It must be https,
	// except on a loopback host. ParseEndpoint says what else it checks.
	Endpoint string
	// Region is signed into every request: "auto" on R2, the bucket's
	// region on AWS, the configured region on MinIO. Empty means
	// DefaultRegion.
	Region string
	// VirtualHost puts the bucket in the host name,
	// https://<bucket>.<endpoint host>/<key>, instead of the path.
	VirtualHost bool
	// Credentials sign the requests.
	Credentials sigv4.Credentials

	// HTTPClient sends the requests. nil means a client with timeouts
	// for connecting, TLS and the response headers, but none for a whole
	// request, because snapshots are large; each call's context bounds
	// it instead. Redirects are never followed, so a signed request
	// can't be passed on to another host.
	HTTPClient *http.Client
	// Now is the clock requests are signed by. nil means time.Now.
	Now func() time.Time
	// MaxAttempts bounds the tries of one call. 0 means
	// DefaultMaxAttempts.
	MaxAttempts int
}

// Client makes S3 calls to one endpoint with one set of credentials.
type Client struct {
	endpoint    *url.URL
	virtualHost bool
	signer      sigv4.Signer
	http        *http.Client
	now         func() time.Time
	attempts    int
	// sleep waits between attempts; tests replace it.
	sleep func(context.Context, time.Duration) error
}

// New checks cfg and returns a client.
func New(cfg Config) (*Client, error) {
	u, err := ParseEndpoint(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	region := cfg.Region
	if region == "" {
		region = DefaultRegion
	}
	if err := CheckRegion(region); err != nil {
		return nil, err
	}
	if cfg.Credentials.AccessKeyID == "" || cfg.Credentials.SecretAccessKey == "" {
		return nil, errors.New("objstore: an access key id and a secret access key are both required")
	}
	hc := defaultHTTPClient()
	if cfg.HTTPClient != nil {
		c := *cfg.HTTPClient
		hc = &c
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	attempts := cfg.MaxAttempts
	if attempts <= 0 {
		attempts = DefaultMaxAttempts
	}
	return &Client{
		endpoint:    u,
		virtualHost: cfg.VirtualHost,
		signer:      sigv4.Signer{Credentials: cfg.Credentials, Region: region, Service: "s3"},
		http:        hc,
		now:         now,
		attempts:    attempts,
		sleep:       sleepCtx,
	}, nil
}

func defaultHTTPClient() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ResponseHeaderTimeout = time.Minute
	t.MaxIdleConnsPerHost = 16
	return &http.Client{Transport: t}
}

// ParseEndpoint checks an endpoint and returns it as a URL: https, or http
// on a loopback host such as MinIO in CI; a host; no user, path, query or
// fragment. A default port is dropped, so the signed host is the one Go
// sends.
func ParseEndpoint(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, errors.New("objstore: the endpoint is required, such as https://<account id>.r2.cloudflarestorage.com")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return nil, fmt.Errorf("objstore: endpoint %q isn't an http(s) URL with a host", raw)
	}
	if u.User != nil {
		// Never echo it: the user part may hold a key.
		return nil, errors.New("objstore: the endpoint must not carry a user or password; credentials are secrets, not part of the URL")
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, fmt.Errorf("objstore: endpoint %q must be an origin, with no path, query or fragment", raw)
	}
	host, port := u.Hostname(), u.Port()
	switch u.Scheme {
	case "https":
		if port == "443" {
			port = ""
		}
	case "http":
		if !isLoopback(host) {
			return nil, fmt.Errorf("objstore: endpoint %q uses http; only https is allowed, except on a loopback host", raw)
		}
		if port == "80" {
			port = ""
		}
	default:
		return nil, fmt.Errorf("objstore: endpoint %q must start with https://", raw)
	}
	host = strings.ToLower(host)
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return &url.URL{Scheme: u.Scheme, Host: host}, nil
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// CheckRegion checks a region name: lowercase letters, digits and '-'.
func CheckRegion(region string) error {
	if region == "" || len(region) > 64 || strings.Trim(region, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
		return fmt.Errorf("objstore: region %q must be lowercase letters, digits and '-', such as %q or \"us-east-1\"", region, DefaultRegion)
	}
	return nil
}

// CheckBucketName checks a bucket name the way R2 and S3 both accept it,
// without dots so a virtual-hosted name stays under the endpoint's TLS
// certificate: 3 to 63 lowercase letters, digits and '-', starting and
// ending with a letter or digit.
func CheckBucketName(name string) error {
	ok := len(name) >= 3 && len(name) <= 63 && name[0] != '-' && name[len(name)-1] != '-' &&
		strings.Trim(name, "abcdefghijklmnopqrstuvwxyz0123456789-") == ""
	if !ok {
		return fmt.Errorf("objstore: bucket name %q must be 3 to 63 lowercase letters, digits and '-', starting and ending with a letter or digit", name)
	}
	return nil
}

// CheckKey checks an object key pairtimed writes. The rule is narrower
// than S3's on purpose, so a key means the same thing on every store and
// in every URL: 1 to 1024 bytes of letters, digits, '-', '_', '.' and '/',
// with no empty, "." or ".." segment.
func CheckKey(key string) error {
	if key == "" || len(key) > 1024 {
		return fmt.Errorf("objstore: a key must be 1 to 1024 bytes, not %d", len(key))
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '-' || c == '_' || c == '.' || c == '/') {
			return fmt.Errorf("objstore: key %q has a character outside letters, digits, '-', '_', '.' and '/'", key)
		}
	}
	for seg := range strings.SplitSeq(key, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("objstore: key %q has an empty, \".\" or \"..\" segment", key)
		}
	}
	return nil
}

// Bucket is one bucket of a client's store.
type Bucket struct {
	c    *Client
	name string
	// pageSize is ListObjectsV2's max-keys; tests make it small.
	pageSize int
}

// Bucket returns the bucket called name.
func (c *Client) Bucket(name string) (*Bucket, error) {
	if err := CheckBucketName(name); err != nil {
		return nil, err
	}
	return &Bucket{c: c, name: name, pageSize: 1000}, nil
}

// Name is the bucket's name.
func (b *Bucket) Name() string { return b.name }

// Error is a failed S3 call.
type Error struct {
	// Op is the S3 operation, such as "PutObject".
	Op     string
	Bucket string
	// Key is the object key, or "" for a call on the bucket.
	Key string
	// Status is the HTTP status of a failed answer, or 0 when the
	// failure was elsewhere: no answer came, or the body was wrong.
	Status int
	// Code is S3's error code, such as "AccessDenied", when the answer
	// had one.
	Code string
	// Message is the store's message, cut to 200 bytes of printable
	// ASCII.
	Message string
	// RequestID is the store's id for the request, for its support.
	RequestID string
	// Err is what the error wraps: ErrNotFound, ErrNoSuchBucket,
	// ErrExists, or the transport's error when no answer came.
	Err error
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "objstore: %s %s", e.Op, e.Bucket)
	if e.Key != "" {
		b.WriteString("/" + e.Key)
	}
	switch {
	case e.Status == 0 && e.Err != nil:
		b.WriteString(": " + e.Err.Error())
	default:
		fmt.Fprintf(&b, ": %d", e.Status)
		if e.Code != "" {
			b.WriteString(" " + e.Code)
		}
		if e.Message != "" {
			b.WriteString(": " + e.Message)
		}
		if e.Err != nil && e.Code == "" && e.Message == "" {
			b.WriteString(": " + e.Err.Error())
		}
	}
	if e.RequestID != "" {
		b.WriteString(" (request id " + e.RequestID + ")")
	}
	return b.String()
}

func (e *Error) Unwrap() error { return e.Err }

// request is one S3 call.
type request struct {
	op, method  string
	bucket, key string
	// rawQuery is already encoded the SigV4 way.
	rawQuery string
	header   http.Header
	// body is read from its offset start for size bytes on each attempt;
	// nil for no body.
	body        io.ReadSeeker
	start, size int64
	payloadHash string
	// attempts overrides the client's MaxAttempts when positive.
	attempts int
}

// url returns the request's URL, path-style or virtual-hosted. The path
// is written with SigV4's encoding, so what is sent is what was signed.
func (c *Client) url(bucket, key, rawQuery string) *url.URL {
	u := &url.URL{Scheme: c.endpoint.Scheme, Host: c.endpoint.Host, RawQuery: rawQuery}
	path, raw := "/", "/"
	if c.virtualHost {
		u.Host = bucket + "." + c.endpoint.Host
	} else {
		path, raw = "/"+bucket, "/"+bucket
		if key != "" {
			path, raw = path+"/", raw+"/"
		}
	}
	u.Path = path + key
	u.RawPath = raw + sigv4.URIEncode(key, false)
	return u
}

// do sends r, retrying failures worth retrying, and returns the answer
// when its status is 2xx. Any other answer becomes an *Error, and its
// body is read and closed.
func (c *Client) do(ctx context.Context, r request) (*http.Response, error) {
	attempts := c.attempts
	if r.attempts > 0 {
		attempts = r.attempts
	}
	if r.payloadHash == "" {
		r.payloadHash = sigv4.EmptyPayloadHash
	}
	var err error
	for attempt := 1; ; attempt++ {
		var resp *http.Response
		resp, err = c.once(ctx, r)
		if err == nil {
			return resp, nil
		}
		if attempt >= attempts || !retryable(ctx, err) {
			return nil, err
		}
		// 200 ms, then 800 ms, give or take half.
		backoff := time.Duration(200<<(2*(attempt-1))) * time.Millisecond
		backoff = backoff/2 + rand.N(backoff)
		if serr := c.sleep(ctx, backoff); serr != nil {
			return nil, err
		}
	}
}

// once sends r one time.
func (c *Client) once(ctx context.Context, r request) (*http.Response, error) {
	fail := func(status int, err error) *Error {
		return &Error{Op: r.op, Bucket: r.bucket, Key: r.key, Status: status, Err: err}
	}
	var body io.Reader
	if r.body != nil {
		if _, err := r.body.Seek(r.start, io.SeekStart); err != nil {
			return nil, fail(0, fmt.Errorf("rewinding the body: %w", err))
		}
		body = io.LimitReader(r.body, r.size)
	}
	u := c.url(r.bucket, r.key, r.rawQuery)
	req, err := http.NewRequestWithContext(ctx, r.method, u.String(), body)
	if err != nil {
		return nil, fail(0, err)
	}
	switch {
	case r.body == nil:
	case r.size == 0:
		req.Body, req.ContentLength = http.NoBody, 0
	default:
		req.ContentLength = r.size
	}
	for k, vs := range r.header {
		req.Header[k] = vs
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("X-Amz-Content-Sha256", r.payloadHash)
	if err := c.signer.Sign(req, r.payloadHash, c.now()); err != nil {
		return nil, fail(0, err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// url.Error repeats the URL, which holds nothing secret: the
		// signature travels in a header.
		return nil, fail(0, err)
	}
	if resp.StatusCode/100 == 2 {
		return resp, nil
	}
	return nil, c.answerError(r, resp)
}

// answerError turns a non-2xx answer into an *Error, reading at most
// 64 KiB of its XML body. Only the code, message and request id are kept:
// some stores put the canonical request and the access key id in a
// signature error's body, and those never leave this function.
func (c *Client) answerError(r request, resp *http.Response) *Error {
	defer resp.Body.Close()
	e := &Error{Op: r.op, Bucket: r.bucket, Key: r.key, Status: resp.StatusCode}
	var body struct {
		Code      string `xml:"Code"`
		Message   string `xml:"Message"`
		RequestID string `xml:"RequestId"`
	}
	if r.method != http.MethodHead {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if xml.Unmarshal(data, &body) == nil {
			e.Code, e.Message, e.RequestID = token(body.Code), printable(body.Message, 200), token(body.RequestID)
		}
	}
	if e.RequestID == "" {
		e.RequestID = token(resp.Header.Get("X-Amz-Request-Id"))
	}
	switch {
	case resp.StatusCode == http.StatusNotFound && e.Code == "NoSuchBucket":
		e.Err = ErrNoSuchBucket
	case resp.StatusCode == http.StatusNotFound && (e.Code == "NoSuchKey" || r.method == http.MethodHead):
		e.Err = ErrNotFound
	case resp.StatusCode == http.StatusPreconditionFailed:
		e.Err = ErrExists
	}
	return e
}

// retryable reports whether a failed call is worth another try: no
// answer at all (unless the context ended), a 5xx, a 429, or one of S3's
// codes that say "try again".
func retryable(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	if e.Status == 0 {
		// A request that couldn't be built or signed fails the same way
		// again.
		var uerr *url.Error
		return errors.As(e.Err, &uerr)
	}
	switch e.Code {
	case "SlowDown", "RequestTimeout", "InternalError", "ServiceUnavailable", "ConditionalRequestConflict":
		return true
	}
	return e.Status >= 500 || e.Status == http.StatusTooManyRequests
}

// token keeps v when it is a short run of printable ASCII without spaces,
// as error codes and request ids are, and drops it otherwise.
func token(v string) string {
	if len(v) > 128 || strings.ContainsFunc(v, func(r rune) bool { return r <= ' ' || r > '~' }) {
		return ""
	}
	return v
}

// printable keeps the printable ASCII of v, with runs of anything else
// turned into one space, cut to n bytes.
func printable(v string, n int) string {
	var b strings.Builder
	gap := false
	for _, r := range v {
		if r > ' ' && r <= '~' {
			if gap && b.Len() > 0 {
				b.WriteByte(' ')
			}
			gap = false
			b.WriteRune(r)
		} else {
			gap = true
		}
		if b.Len() >= n {
			break
		}
	}
	s := b.String()
	if len(s) > n {
		s = s[:n]
	}
	return s
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// drain reads a little of an answer's body and closes it, so the
// connection can be reused.
func drain(resp *http.Response) {
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	resp.Body.Close()
}
