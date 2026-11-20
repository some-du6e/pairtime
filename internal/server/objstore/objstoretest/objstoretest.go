// SPDX-License-Identifier: AGPL-3.0-or-later

// Package objstoretest is an in-process S3 server for tests: the part of
// the S3 API that package objstore calls, kept in memory. It checks every
// request the way S3 does, the SigV4 signature (with package sigv4's
// Verify) and the body against X-Amz-Content-Sha256, so a client that
// sends something other than what it signed fails here. Tests that need a
// bucket use it, and no test calls a real cloud bucket. CI's objstore.yml
// also runs the client against MinIO, an S3 implementation this
// repository didn't write.
//
// It answers path-style requests, /<bucket>/<key>, and virtual-hosted
// ones whose host starts with "<bucket>.".
package objstoretest

import (
	"bytes"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/objstore/sigv4"
)

// The credentials and region the server accepts.
const (
	AccessKeyID     = "AKIDPAIRTIMETEST"
	SecretAccessKey = "pairtime-test-secret-do-not-use"
	Region          = "auto"
)

// Credentials are the credentials the server accepts.
var Credentials = sigv4.Credentials{AccessKeyID: AccessKeyID, SecretAccessKey: SecretAccessKey}

// maxBody bounds a request body the server reads.
const maxBody = 128 << 20

// MaxSkew is how far X-Amz-Date may be from the server's clock, as in S3.
const MaxSkew = 15 * time.Minute

// Server is a running fake S3 server.
type Server struct {
	*httptest.Server

	mu       sync.Mutex
	buckets  map[string]map[string]*object
	requests []Request
	faults   []fault
	nextID   int
	// Now is the server's clock, for checking X-Amz-Date. It defaults to
	// time.Now; set it before the first request.
	Now func() time.Time
}

type object struct {
	data        []byte
	etag        string
	modified    time.Time
	contentType string
}

// Request is one request the server received.
type Request struct {
	Method   string
	Host     string
	Path     string
	RawQuery string
	Header   http.Header
	// Status is the status the server answered with.
	Status int
}

// fault is an answer forced on the next request: an S3 error, or, with
// status 0, a connection closed without an answer.
type fault struct {
	status int
	code   string
}

// New starts a server holding the named, empty buckets, and closes it when
// the test ends.
func New(t testing.TB, buckets ...string) *Server {
	t.Helper()
	return start(t, httptest.NewServer, buckets)
}

// NewTLS is New over https. The server presents httptest's certificate,
// which covers example.com and *.example.com, so a client can check a
// virtual-hosted name against it. Certificate returns it for a client's
// RootCAs.
func NewTLS(t testing.TB, buckets ...string) *Server {
	t.Helper()
	return start(t, httptest.NewTLSServer, buckets)
}

func start(t testing.TB, newServer func(http.Handler) *httptest.Server, buckets []string) *Server {
	s := &Server{buckets: make(map[string]map[string]*object), Now: time.Now}
	for _, b := range buckets {
		s.buckets[b] = make(map[string]*object)
	}
	s.Server = newServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// Fail makes the next n requests fail with status and the S3 error code,
// before the server looks at them. A status of 0 closes the connection
// without an answer instead.
func (s *Server) Fail(n, status int, code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for range n {
		s.faults = append(s.faults, fault{status, code})
	}
}

// Requests returns the requests received so far, oldest first.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

// Object returns the bytes stored under key in bucket.
func (s *Server) Object(bucket, key string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.buckets[bucket][key]
	if !ok {
		return nil, false
	}
	return slices.Clone(o.data), true
}

// SetObject stores data under key in bucket, behind the client's back, as
// someone with the bucket's keys could.
func (s *Server) SetObject(bucket, key string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.buckets[bucket] == nil {
		s.buckets[bucket] = make(map[string]*object)
	}
	s.store(bucket, key, data, "application/octet-stream")
}

// Keys returns the keys in bucket, sorted.
func (s *Server) Keys(bucket string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Sorted(maps.Keys(s.buckets[bucket]))
}

func (s *Server) store(bucket, key string, data []byte, contentType string) *object {
	sum := md5.Sum(data)
	o := &object{data: slices.Clone(data), etag: `"` + hex.EncodeToString(sum[:]) + `"`, modified: s.Now().UTC().Truncate(time.Second), contentType: contentType}
	s.buckets[bucket][key] = o
	return o
}

// answer is what the handler writes.
type answer struct {
	status int
	header http.Header
	body   []byte
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.nextID++
	id := "fake-" + strconv.Itoa(s.nextID)
	var f *fault
	if len(s.faults) > 0 {
		f = &s.faults[0]
		s.faults = s.faults[1:]
	}
	s.mu.Unlock()

	var a answer
	switch {
	case f != nil && f.status == 0:
		s.record(r, 0)
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				conn.Close()
				return
			}
		}
		panic(http.ErrAbortHandler)
	case f != nil:
		a = s3error(f.status, f.code, "injected by the test", id)
	default:
		a = s.handle(r, id)
	}
	s.record(r, a.status)
	for k, vs := range a.header {
		w.Header()[k] = vs
	}
	w.Header().Set("X-Amz-Request-Id", id)
	if a.body != nil && r.Method != http.MethodHead {
		w.Header().Set("Content-Length", strconv.Itoa(len(a.body)))
	}
	w.WriteHeader(a.status)
	if r.Method != http.MethodHead {
		w.Write(a.body)
	}
}

func (s *Server) record(r *http.Request, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, Request{
		Method: r.Method, Host: r.Host, Path: r.URL.Path, RawQuery: r.URL.RawQuery,
		Header: r.Header.Clone(), Status: status,
	})
}

func (s *Server) handle(r *http.Request, id string) answer {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		return s3error(http.StatusBadRequest, "IncompleteBody", "the body couldn't be read", id)
	}
	if len(body) > maxBody {
		return s3error(http.StatusRequestEntityTooLarge, "EntityTooLarge", "the test server takes 128 MiB at most", id)
	}
	payload := r.Header.Get("X-Amz-Content-Sha256")
	switch {
	case payload == "":
		return s3error(http.StatusBadRequest, "InvalidRequest", "Missing required header for this request: x-amz-content-sha256", id)
	case payload == sigv4.UnsignedPayload:
	case payload != sigv4.PayloadHash(body):
		return s3error(http.StatusBadRequest, "XAmzContentSHA256Mismatch", "The provided 'x-amz-content-sha256' header does not match what was computed.", id)
	}
	if err := sigv4.Verify(r, payload, Credentials, Region, "s3", s.Now(), MaxSkew); err != nil {
		return signatureError(r, id)
	}

	bucket, key := s.route(r)
	s.mu.Lock()
	defer s.mu.Unlock()
	objects, ok := s.buckets[bucket]
	if !ok {
		return s3error(http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist", id)
	}
	if key == "" {
		if r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2" {
			return s.list(objects, bucket, r.URL.Query(), id)
		}
		return s3error(http.StatusNotImplemented, "NotImplemented", "the test server lists objects and nothing else on a bucket", id)
	}
	switch r.Method {
	case http.MethodPut:
		switch m := r.Header.Get("If-None-Match"); m {
		case "":
		case "*":
			if _, exists := objects[key]; exists {
				return s3error(http.StatusPreconditionFailed, "PreconditionFailed", "At least one of the pre-conditions you specified did not hold", id)
			}
		default:
			return s3error(http.StatusNotImplemented, "NotImplemented", "If-None-Match takes only *", id)
		}
		ctype := r.Header.Get("Content-Type")
		if ctype == "" {
			ctype = "binary/octet-stream"
		}
		o := s.store(bucket, key, body, ctype)
		return answer{status: http.StatusOK, header: http.Header{"Etag": {o.etag}}, body: []byte{}}
	case http.MethodGet, http.MethodHead:
		o, ok := objects[key]
		if !ok {
			return s3error(http.StatusNotFound, "NoSuchKey", "The specified key does not exist.", id)
		}
		return answer{status: http.StatusOK, body: o.data, header: http.Header{
			"Etag":           {o.etag},
			"Last-Modified":  {o.modified.Format(http.TimeFormat)},
			"Content-Type":   {o.contentType},
			"Content-Length": {strconv.Itoa(len(o.data))},
		}}
	case http.MethodDelete:
		delete(objects, key)
		return answer{status: http.StatusNoContent}
	}
	return s3error(http.StatusMethodNotAllowed, "MethodNotAllowed", "The specified method is not allowed against this resource.", id)
}

// route splits a request into bucket and key, virtual-hosted when the
// host's first label is a bucket, path-style otherwise. Call it without
// s.mu held.
func (s *Server) route(r *http.Request) (bucket, key string) {
	if label, _, ok := strings.Cut(r.Host, "."); ok {
		s.mu.Lock()
		_, isBucket := s.buckets[label]
		s.mu.Unlock()
		if isBucket {
			return label, strings.TrimPrefix(r.URL.Path, "/")
		}
	}
	bucket, key, _ = strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	return bucket, key
}

// list answers ListObjectsV2.
func (s *Server) list(objects map[string]*object, bucket string, q url.Values, id string) answer {
	prefix := q.Get("prefix")
	maxKeys := 1000
	if v := q.Get("max-keys"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return s3error(http.StatusBadRequest, "InvalidArgument", "max-keys must be a non-negative integer", id)
		}
		maxKeys = min(n, 1000)
	}
	after := ""
	if tok := q.Get("continuation-token"); tok != "" {
		b, err := base64.StdEncoding.DecodeString(tok)
		if err != nil {
			return s3error(http.StatusBadRequest, "InvalidArgument", "The continuation token provided is incorrect", id)
		}
		after = string(b)
	}
	urlEncode := q.Get("encoding-type") == "url"
	type content struct {
		Key          string `xml:"Key"`
		LastModified string `xml:"LastModified"`
		ETag         string `xml:"ETag"`
		Size         int    `xml:"Size"`
		StorageClass string `xml:"StorageClass"`
	}
	type result struct {
		XMLName               xml.Name  `xml:"http://s3.amazonaws.com/doc/2006-03-01/ ListBucketResult"`
		Name                  string    `xml:"Name"`
		Prefix                string    `xml:"Prefix"`
		KeyCount              int       `xml:"KeyCount"`
		MaxKeys               int       `xml:"MaxKeys"`
		EncodingType          string    `xml:"EncodingType,omitempty"`
		IsTruncated           bool      `xml:"IsTruncated"`
		Contents              []content `xml:"Contents"`
		NextContinuationToken string    `xml:"NextContinuationToken,omitempty"`
	}
	res := result{Name: bucket, Prefix: prefix, MaxKeys: maxKeys}
	if urlEncode {
		res.EncodingType = "url"
		res.Prefix = url.QueryEscape(prefix)
	}
	last := ""
	for _, k := range slices.Sorted(maps.Keys(objects)) {
		if !strings.HasPrefix(k, prefix) || k <= after {
			continue
		}
		if len(res.Contents) == maxKeys {
			res.IsTruncated = true
			res.NextContinuationToken = base64.StdEncoding.EncodeToString([]byte(last))
			break
		}
		last = k
		o := objects[k]
		name := k
		if urlEncode {
			name = url.QueryEscape(k)
		}
		res.Contents = append(res.Contents, content{
			Key: name, LastModified: o.modified.Format("2006-01-02T15:04:05.000Z"),
			ETag: o.etag, Size: len(o.data), StorageClass: "STANDARD",
		})
	}
	res.KeyCount = len(res.Contents)
	out, err := xml.Marshal(res)
	if err != nil {
		return s3error(http.StatusInternalServerError, "InternalError", err.Error(), id)
	}
	return answer{status: http.StatusOK, header: http.Header{"Content-Type": {"application/xml"}}, body: append([]byte(xml.Header), out...)}
}

// s3error is an S3 error answer.
func s3error(status int, code, message, id string) answer {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	b.WriteString("<Error><Code>")
	xml.EscapeText(&b, []byte(code))
	b.WriteString("</Code><Message>")
	xml.EscapeText(&b, []byte(message))
	b.WriteString("</Message><RequestId>" + id + "</RequestId></Error>")
	return answer{status: status, header: http.Header{"Content-Type": {"application/xml"}}, body: b.Bytes()}
}

// signatureError is S3's SignatureDoesNotMatch, which, like S3's, repeats
// the access key id, the signature it was given and the string it
// expected, so tests can check that a client keeps them out of its errors.
func signatureError(r *http.Request, id string) answer {
	auth := r.Header.Get("Authorization")
	akid, sig := "", ""
	if _, cred, ok := strings.Cut(auth, "Credential="); ok {
		akid, _, _ = strings.Cut(cred, "/")
	}
	if _, s, ok := strings.Cut(auth, "Signature="); ok {
		sig = s
	}
	var b bytes.Buffer
	b.WriteString(xml.Header + "<Error><Code>SignatureDoesNotMatch</Code><Message>The request signature we calculated does not match the signature you provided. Check your key and signing method.</Message><AWSAccessKeyId>")
	xml.EscapeText(&b, []byte(akid))
	b.WriteString("</AWSAccessKeyId><SignatureProvided>")
	xml.EscapeText(&b, []byte(sig))
	b.WriteString("</SignatureProvided><StringToSign>")
	xml.EscapeText(&b, []byte(fmt.Sprintf("%s\n%s", sigv4.Algorithm, r.Header.Get("X-Amz-Date"))))
	b.WriteString("</StringToSign><RequestId>" + id + "</RequestId></Error>")
	return answer{status: http.StatusForbidden, header: http.Header{"Content-Type": {"application/xml"}}, body: b.Bytes()}
}

// SHA256 is the hex SHA-256 of data, for tests that name evidence keys.
func SHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
