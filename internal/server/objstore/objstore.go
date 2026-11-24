// SPDX-License-Identifier: AGPL-3.0-or-later

// Package objstore is pairtimed's client for S3-compatible object storage:
// Cloudflare R2 for the pilot, Hetzner Object Storage or AWS S3 after a
// move, MinIO in CI (DESIGN 10.1, 10.3, 10.5 item 4). It makes the few S3
// calls pairtimed needs, on the standard library, signed with SigV4
// (package sigv4):
//
//   - PutObject, GetObject, HeadObject, DeleteObject and ListObjectsV2;
//   - multipart uploads for bodies of unknown length (PutStream), and
//     GetBucketVersioning (Bucket.Versioning) for pairtimed doctor;
//   - write-once puts with If-None-Match: *, which the evidence store uses
//     for sealed blobs keyed by their SHA-256 under evidence/ (Evidence;
//     DESIGN 7.6, 10.1);
//   - the nightly snapshot bucket, a second bucket with a 30-day
//     lifecycle for age-encrypted database snapshots (Snapshots; DESIGN
//     10.3);
//   - a readiness check for /readyz (Bucket.Check; DESIGN 10.4).
//
// Requests use path-style URLs, https://<endpoint>/<bucket>/<key>, unless
// the configuration asks for virtual-hosted ones, which need https and a
// host name. A single put is capped at MaxPutSize, R2's cap, which is
// 5 MiB under S3's 5 GiB. PutStream sends a body of unknown length, such
// as a nightly snapshot as age encrypts it, in parts of DefaultPartSize
// with a multipart upload, which it aborts when any part fails.
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
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
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
	// store kept what it had. After a retry, Put first checks that the
	// key doesn't hold its own upload (PutOptions.IfNoneMatch).
	ErrExists = errors.New("objstore: the key already exists")
	// ErrTooLarge: the body is bigger than one PutObject may be. Put
	// refuses a body over MaxPutSize before sending it, and a store's own
	// EntityTooLarge answer wraps ErrTooLarge too.
	ErrTooLarge = errors.New("objstore: too large for a single put")
	// ErrKeepsVersions: a delete answered x-amz-delete-marker: true, which
	// only a bucket that keeps versions sends. The delete only hid the
	// object behind a delete marker, and its bytes are still in the bucket
	// as an older version. Neither of pairtimed's buckets may keep versions
	// (DESIGN 10.3; docs/errata/2026-11-20-evidence-versioning.md), and
	// Evidence.Delete fails with it.
	ErrKeepsVersions = errors.New("objstore: the bucket keeps versions, so the delete left the object's bytes behind a delete marker")
)

// MaxPutSize is the largest body one PutObject takes on every store
// pairtimed uses. R2 takes at most 5 MiB less than 5 GiB in one request
// (R2's limits page, modified 2026-10-30), which is under S3's 5 GiB.
const MaxPutSize = 5<<30 - 5<<20

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
	// https://<bucket>.<endpoint host>/<key>, instead of the path. It
	// needs an https endpoint with a host name (CheckVirtualHost).
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
// Formatting a *Client prints its endpoint and region, never a
// credential.
type Client struct {
	endpoint    *url.URL
	virtualHost bool
	// signer holds the credentials. It is a pointer so that formatting a
	// Client value, which skips String, prints an address here: fmt never
	// calls Credentials.String through an unexported field, so a Signer
	// value would print the secret.
	signer   *sigv4.Signer
	http     *http.Client
	now      func() time.Time
	attempts int
	// sleep waits between attempts; tests replace it.
	sleep func(context.Context, time.Duration) error
}

// New checks cfg and returns a client.
func New(cfg Config) (*Client, error) {
	u, err := ParseEndpoint(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	if cfg.VirtualHost {
		if err := CheckVirtualHost(u); err != nil {
			return nil, err
		}
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
		signer:      &sigv4.Signer{Credentials: cfg.Credentials, Region: region, Service: "s3"},
		http:        hc,
		now:         now,
		attempts:    attempts,
		sleep:       sleepCtx,
	}, nil
}

// String names the client's endpoint and region. It prints no credential,
// not even the access key id.
func (c *Client) String() string {
	return "objstore.Client{endpoint " + c.endpoint.String() + ", region " + c.signer.Region + "}"
}

// GoString is String, for %#v.
func (c *Client) GoString() string { return c.String() }

// LogValue is String, for log/slog.
func (c *Client) LogValue() slog.Value { return slog.StringValue(c.String()) }

func defaultHTTPClient() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ResponseHeaderTimeout = time.Minute
	t.MaxIdleConnsPerHost = 16
	return &http.Client{Transport: t}
}

// ParseEndpoint checks an endpoint and returns it as a URL: https, or http
// on a loopback host such as MinIO in CI; a host; no user, path, query or
// fragment. A default port is dropped, so the signed host is the one Go
// sends. CheckVirtualHost says what virtual-hosted URLs need on top.
func ParseEndpoint(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, errors.New("objstore: the endpoint is required, such as https://<account id>.r2.cloudflarestorage.com")
	}
	if strings.Contains(raw, "@") {
		// Checked before anything quotes raw: the user part may hold a key.
		return nil, errors.New("objstore: the endpoint must not carry a user or password; credentials are secrets, not part of the URL")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return nil, fmt.Errorf("objstore: endpoint %q isn't an http(s) URL with a host", raw)
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

// CheckVirtualHost checks that an endpoint from ParseEndpoint can take
// virtual-hosted URLs, which put the bucket in front of its host. That
// needs https and a host name. Over http the requests would go to
// <bucket>.localhost or <bucket>.127.0.0.1, which aren't the loopback
// host that ParseEndpoint allows http on. pairtimed builds without cgo,
// and Go's own resolver looks such a name up in DNS like any other, while
// a proxy set in HTTP_PROXY gets every request for it. Either way the
// signed requests and their bodies could leave the machine in plaintext.
// An IP address can't take a bucket in front of it at all.
func CheckVirtualHost(endpoint *url.URL) error {
	if endpoint.Scheme != "https" {
		return errors.New("objstore: virtual-hosted URLs need an https endpoint; over http they would go to <bucket>.<host>, which isn't a loopback host, so requests could leave the machine in plaintext")
	}
	if host := endpoint.Hostname(); net.ParseIP(host) != nil {
		return fmt.Errorf("objstore: virtual-hosted URLs need a host name to put the bucket in front of, not the IP address %s", host)
	}
	return nil
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
	// ErrExists, ErrTooLarge, or the transport's error when no answer
	// came.
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
	// nil for no body. The attempts read it one at a time, each through
	// its own attemptBody, and nothing reads it once do returns.
	body        io.ReadSeeker
	start, size int64
	payloadHash string
	// attempts overrides the client's MaxAttempts when positive.
	attempts int
	// retried, when not nil, is set when an attempt fails in a way worth
	// retrying and another is sent. The store may have carried out the
	// failed attempt anyway, with only its answer lost.
	retried *bool
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
		if r.retried != nil {
			*r.retried = true
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
		// The last attempt's body was stopped when it ended, so nothing
		// else reads r.body now.
		if _, err := r.body.Seek(r.start, io.SeekStart); err != nil {
			return nil, fail(0, fmt.Errorf("rewinding the body: %w", err))
		}
		b := &attemptBody{r: io.LimitReader(r.body, r.size)}
		defer b.stop()
		body = b
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

// attemptBody is what one attempt sends of a request's body. The
// transport can go on reading a body after Do returns: when the store
// answers before it has the whole body, as a store that fails a request
// at once does, net/http keeps sending the rest from the connection's own
// goroutine, and HTTP/2 closes the body from yet another one. So once
// calls stop when the attempt ends. stop waits out a Read in progress and
// makes every later Read end the body. After that the next attempt may
// rewind the shared body, and the caller may reuse its bytes, as
// uploadParts reuses its part buffer.
type attemptBody struct {
	mu      sync.Mutex
	r       io.Reader
	stopped bool
}

// Read reads the attempt's bytes, and after stop reports the end of the
// body. A transport still sending a stopped attempt then finds it shorter
// than its Content-Length and fails it, though once has already taken
// that attempt's answer.
func (b *attemptBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopped {
		return 0, io.EOF
	}
	return b.r.Read(p)
}

// Close is how the transport says it is done with the body.
func (b *attemptBody) Close() error {
	b.stop()
	return nil
}

func (b *attemptBody) stop() {
	b.mu.Lock()
	b.stopped = true
	b.mu.Unlock()
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
	case resp.StatusCode == http.StatusNotFound && e.Code == "NoSuchUpload":
		e.Err = errNoSuchUpload
	case resp.StatusCode == http.StatusPreconditionFailed:
		e.Err = ErrExists
	case resp.StatusCode == http.StatusNotImplemented || e.Code == "NotImplemented":
		e.Err = ErrNotImplemented
	case e.Code == "EntityTooLarge":
		// A store whose cap is under MaxPutSize says so this way.
		e.Err = ErrTooLarge
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
