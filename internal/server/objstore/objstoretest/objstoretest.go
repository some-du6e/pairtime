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
// ones whose host starts with "<bucket>.". Like S3, it keeps the
// x-amz-meta-* headers a put carries and returns them on GET and HEAD.
// It takes multipart uploads with R2's rules: every part but the last at
// least MinPartSize and all of one size, and If-None-Match: * evaluated
// when CompleteMultipartUpload would make the object. GetBucketVersioning
// answers what SetVersioning set, and GetBucketLifecycleConfiguration what
// SetLifecycle set.
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
	uploads  map[string]*upload
	// matched holds FailMatching's faults.
	matched []fault
	// versioning holds what SetVersioning set, by bucket.
	versioning map[string]string
	// lifecycle holds what SetLifecycle set, by bucket.
	lifecycle map[string]string
	// ignoreIfNoneMatch holds the buckets that WriteOnceIgnored set.
	ignoreIfNoneMatch map[string]bool
	// Now is the server's clock, for checking X-Amz-Date. It defaults to
	// time.Now; set it before the first request.
	Now func() time.Time
	// MinPartSize is the smallest part but the last that a multipart
	// upload may have: S3's and R2's 5 MiB unless a test lowers it. Set it
	// before the first request.
	MinPartSize int
}

// NotImplemented is the versioning state that makes GetBucketVersioning
// answer 501 NotImplemented, as R2 does.
const NotImplemented = "not implemented"

// AccessDenied is the versioning state that makes GetBucketVersioning
// answer 403 AccessDenied, as a store does to a key scoped to objects.
const AccessDenied = "access denied"

// InternalError is the versioning state that makes GetBucketVersioning
// answer 500 InternalError every time, as a store does that is failing.
const InternalError = "internal error"

// CutBody, as the status of FailAfter or FailMatching with after set,
// answers 200 and then closes the connection halfway through the body, as
// when a store's answer is lost after its headers.
const CutBody = -1

// upload is a multipart upload in progress.
type upload struct {
	bucket, key string
	contentType string
	meta        http.Header
	parts       map[int][]byte
}

type object struct {
	data        []byte
	etag        string
	modified    time.Time
	contentType string
	// meta holds the x-amz-meta-* headers the object was put with.
	meta http.Header
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
// status 0, a connection closed without an answer. With after set, the
// server carries the request out first. With match set, it waits for the
// first request match accepts instead.
type fault struct {
	status int
	code   string
	after  bool
	match  func(*http.Request) bool
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
	s := &Server{
		buckets: make(map[string]map[string]*object), Now: time.Now, MinPartSize: 5 << 20,
		uploads: make(map[string]*upload), versioning: make(map[string]string), lifecycle: make(map[string]string),
		ignoreIfNoneMatch: make(map[string]bool),
	}
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
		s.faults = append(s.faults, fault{status: status, code: code})
	}
}

// FailAfter makes the next n requests do what they ask, and then answer
// with status and the S3 error code in place of their real answer. A
// status of 0 closes the connection without an answer instead, as when a
// store commits a put and the answer is lost on the way back. Faults from
// Fail and FailAfter apply in the order they were set.
func (s *Server) FailAfter(n, status int, code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for range n {
		s.faults = append(s.faults, fault{status: status, code: code, after: true})
	}
}

// FailMatching is Fail, or FailAfter when after is set, for the next n
// requests that match accepts, wherever they come. It goes before the
// faults of Fail and FailAfter, which wait for the requests it doesn't
// match.
func (s *Server) FailMatching(n int, match func(*http.Request) bool, status int, code string, after bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for range n {
		s.matched = append(s.matched, fault{status: status, code: code, after: after, match: match})
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
// someone with the bucket's keys could. The object has no x-amz-meta-*
// headers.
func (s *Server) SetObject(bucket, key string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.buckets[bucket] == nil {
		s.buckets[bucket] = make(map[string]*object)
	}
	s.store(bucket, key, data, "application/octet-stream", nil)
}

// SetVersioning sets what GetBucketVersioning answers for bucket: "" for
// a bucket whose versioning was never turned on, "Enabled", "Suspended",
// NotImplemented, AccessDenied or InternalError. The server never keeps
// old versions either way.
func (s *Server) SetVersioning(bucket, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.versioning[bucket] = state
}

// SetLifecycle sets what GetBucketLifecycleConfiguration answers for
// bucket: rules, the XML of its Rule elements, which the answer wraps in a
// LifecycleConfiguration element; "" for a bucket without a lifecycle
// configuration, which answers 404 NoSuchLifecycleConfiguration as S3
// does; or NotImplemented, AccessDenied or InternalError. The server never
// applies the rules.
func (s *Server) SetLifecycle(bucket, rules string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lifecycle[bucket] = rules
}

// WriteOnceIgnored makes bucket ignore If-None-Match, as a store without
// conditional writes would: a second write-once put replaces the object.
func (s *Server) WriteOnceIgnored(bucket string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ignoreIfNoneMatch[bucket] = true
}

// Uploads returns the keys of the multipart uploads that were started and
// neither completed nor aborted, sorted.
func (s *Server) Uploads() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var keys []string
	for _, u := range s.uploads {
		keys = append(keys, u.bucket+"/"+u.key)
	}
	slices.Sort(keys)
	return keys
}

// Keys returns the keys in bucket, sorted.
func (s *Server) Keys(bucket string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Sorted(maps.Keys(s.buckets[bucket]))
}

func (s *Server) store(bucket, key string, data []byte, contentType string, meta http.Header) *object {
	sum := md5.Sum(data)
	o := &object{data: slices.Clone(data), etag: `"` + hex.EncodeToString(sum[:]) + `"`, modified: s.Now().UTC().Truncate(time.Second), contentType: contentType, meta: meta}
	s.buckets[bucket][key] = o
	return o
}

// userMeta returns the x-amz-meta-* headers of h.
func userMeta(h http.Header) http.Header {
	meta := http.Header{}
	for k, vs := range h {
		if strings.HasPrefix(k, "X-Amz-Meta-") {
			meta[k] = slices.Clone(vs)
		}
	}
	return meta
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
	if i := slices.IndexFunc(s.matched, func(f fault) bool { return f.match(r) }); i >= 0 {
		m := s.matched[i]
		f = &m
		s.matched = slices.Delete(s.matched, i, i+1)
	} else if len(s.faults) > 0 {
		f = &s.faults[0]
		s.faults = s.faults[1:]
	}
	s.mu.Unlock()

	var a answer
	if f == nil || f.after {
		a = s.handle(r, id)
	}
	if f != nil {
		if f.status == 0 {
			s.drop(w, r)
			return
		}
		if f.status == CutBody {
			s.record(r, http.StatusOK)
			w.Header().Set("Content-Length", strconv.Itoa(len(a.body)+64))
			w.WriteHeader(http.StatusOK)
			w.Write(a.body[:len(a.body)/2])
			panic(http.ErrAbortHandler)
		}
		msg := "injected by the test"
		if f.after {
			msg = "injected by the test after carrying out the request"
		}
		a = s3error(f.status, f.code, msg, id)
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

// drop records r and closes its connection without an answer.
func (s *Server) drop(w http.ResponseWriter, r *http.Request) {
	s.record(r, 0)
	if hj, ok := w.(http.Hijacker); ok {
		if conn, _, err := hj.Hijack(); err == nil {
			conn.Close()
			return
		}
	}
	panic(http.ErrAbortHandler)
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
	q := r.URL.Query()
	if key == "" {
		if r.Method == http.MethodGet && q.Get("list-type") == "2" {
			return s.list(objects, bucket, q, id)
		}
		if r.Method == http.MethodGet && q.Has("versioning") {
			return s.versioningAnswer(bucket, id)
		}
		if r.Method == http.MethodGet && q.Has("lifecycle") {
			return s.lifecycleAnswer(bucket, id)
		}
		return s3error(http.StatusNotImplemented, "NotImplemented", "the test server lists objects and answers GetBucketVersioning and GetBucketLifecycleConfiguration, and nothing else on a bucket", id)
	}
	if q.Has("uploads") || q.Has("uploadId") {
		return s.multipart(r, objects, bucket, key, body, id)
	}
	switch r.Method {
	case http.MethodPut:
		if a, refused := s.precondition(r, objects, bucket, key, id); refused {
			return a
		}
		ctype := r.Header.Get("Content-Type")
		if ctype == "" {
			ctype = "binary/octet-stream"
		}
		o := s.store(bucket, key, body, ctype, userMeta(r.Header))
		return answer{status: http.StatusOK, header: http.Header{"Etag": {o.etag}}, body: []byte{}}
	case http.MethodGet, http.MethodHead:
		o, ok := objects[key]
		if !ok {
			return s3error(http.StatusNotFound, "NoSuchKey", "The specified key does not exist.", id)
		}
		h := http.Header{
			"Etag":           {o.etag},
			"Last-Modified":  {o.modified.Format(http.TimeFormat)},
			"Content-Type":   {o.contentType},
			"Content-Length": {strconv.Itoa(len(o.data))},
		}
		for k, vs := range o.meta {
			h[k] = slices.Clone(vs)
		}
		return answer{status: http.StatusOK, body: o.data, header: h}
	case http.MethodDelete:
		delete(objects, key)
		return answer{status: http.StatusNoContent}
	}
	return s3error(http.StatusMethodNotAllowed, "MethodNotAllowed", "The specified method is not allowed against this resource.", id)
}

// precondition checks a write's If-None-Match against key, and reports
// the answer when the write may not go ahead. Call it with s.mu held.
func (s *Server) precondition(r *http.Request, objects map[string]*object, bucket, key, id string) (answer, bool) {
	switch m := r.Header.Get("If-None-Match"); m {
	case "":
	case "*":
		if _, exists := objects[key]; exists && !s.ignoreIfNoneMatch[bucket] {
			return s3error(http.StatusPreconditionFailed, "PreconditionFailed", "At least one of the pre-conditions you specified did not hold", id), true
		}
	default:
		return s3error(http.StatusNotImplemented, "NotImplemented", "If-None-Match takes only *", id), true
	}
	return answer{}, false
}

// versioningAnswer answers GetBucketVersioning. Call it with s.mu held.
func (s *Server) versioningAnswer(bucket, id string) answer {
	state := s.versioning[bucket]
	switch state {
	case NotImplemented:
		return s3error(http.StatusNotImplemented, "NotImplemented", "GetBucketVersioning not implemented", id)
	case AccessDenied:
		return s3error(http.StatusForbidden, "AccessDenied", "Access Denied", id)
	case InternalError:
		return s3error(http.StatusInternalServerError, "InternalError", "We encountered an internal error. Please try again.", id)
	}
	var b bytes.Buffer
	b.WriteString(xml.Header + `<VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`)
	if state != "" {
		b.WriteString("<Status>")
		xml.EscapeText(&b, []byte(state))
		b.WriteString("</Status>")
	}
	b.WriteString("</VersioningConfiguration>")
	return answer{status: http.StatusOK, header: http.Header{"Content-Type": {"application/xml"}}, body: b.Bytes()}
}

// lifecycleAnswer answers GetBucketLifecycleConfiguration. Call it with
// s.mu held.
func (s *Server) lifecycleAnswer(bucket, id string) answer {
	switch rules := s.lifecycle[bucket]; rules {
	case "":
		return s3error(http.StatusNotFound, "NoSuchLifecycleConfiguration", "The lifecycle configuration does not exist", id)
	case NotImplemented:
		return s3error(http.StatusNotImplemented, "NotImplemented", "GetBucketLifecycleConfiguration not implemented", id)
	case AccessDenied:
		return s3error(http.StatusForbidden, "AccessDenied", "Access Denied", id)
	case InternalError:
		return s3error(http.StatusInternalServerError, "InternalError", "We encountered an internal error. Please try again.", id)
	default:
		body := xml.Header + `<LifecycleConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">` + rules + `</LifecycleConfiguration>`
		return answer{status: http.StatusOK, header: http.Header{"Content-Type": {"application/xml"}}, body: []byte(body)}
	}
}

// multipart answers CreateMultipartUpload, UploadPart,
// CompleteMultipartUpload and AbortMultipartUpload. Call it with s.mu
// held.
func (s *Server) multipart(r *http.Request, objects map[string]*object, bucket, key string, body []byte, id string) answer {
	q := r.URL.Query()
	if r.Method == http.MethodPost && q.Has("uploads") {
		s.nextID++
		// A '+' and a '/', which the client must percent-encode in the
		// query, as S3's upload ids can hold both.
		uploadID := "up+load/" + strconv.Itoa(s.nextID) + "~x.y"
		ctype := r.Header.Get("Content-Type")
		if ctype == "" {
			ctype = "binary/octet-stream"
		}
		s.uploads[uploadID] = &upload{bucket: bucket, key: key, contentType: ctype, meta: userMeta(r.Header), parts: make(map[int][]byte)}
		var b bytes.Buffer
		b.WriteString(xml.Header + `<InitiateMultipartUploadResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Bucket>`)
		xml.EscapeText(&b, []byte(bucket))
		b.WriteString("</Bucket><Key>")
		xml.EscapeText(&b, []byte(key))
		b.WriteString("</Key><UploadId>")
		xml.EscapeText(&b, []byte(uploadID))
		b.WriteString("</UploadId></InitiateMultipartUploadResult>")
		return answer{status: http.StatusOK, header: http.Header{"Content-Type": {"application/xml"}}, body: b.Bytes()}
	}
	u, ok := s.uploads[q.Get("uploadId")]
	if !ok || u.bucket != bucket || u.key != key {
		return s3error(http.StatusNotFound, "NoSuchUpload", "The specified multipart upload does not exist.", id)
	}
	switch r.Method {
	case http.MethodPut:
		n, err := strconv.Atoi(q.Get("partNumber"))
		if err != nil || n < 1 || n > 10000 {
			return s3error(http.StatusBadRequest, "InvalidArgument", "Part number must be an integer between 1 and 10000, inclusive", id)
		}
		u.parts[n] = slices.Clone(body)
		sum := md5.Sum(body)
		return answer{status: http.StatusOK, header: http.Header{"Etag": {`"` + hex.EncodeToString(sum[:]) + `"`}}, body: []byte{}}
	case http.MethodDelete:
		delete(s.uploads, q.Get("uploadId"))
		return answer{status: http.StatusNoContent}
	case http.MethodPost:
		var req struct {
			Parts []struct {
				PartNumber int    `xml:"PartNumber"`
				ETag       string `xml:"ETag"`
			} `xml:"Part"`
		}
		if err := xml.Unmarshal(body, &req); err != nil || len(req.Parts) == 0 {
			return s3error(http.StatusBadRequest, "MalformedXML", "The XML you provided was not well-formed or did not validate against our published schema", id)
		}
		var data []byte
		var sums []byte
		size := -1
		for i, p := range req.Parts {
			part, ok := u.parts[p.PartNumber]
			sum := md5.Sum(part)
			if !ok || p.ETag != `"`+hex.EncodeToString(sum[:])+`"` {
				return s3error(http.StatusBadRequest, "InvalidPart", "One or more of the specified parts could not be found.", id)
			}
			if i > 0 && p.PartNumber <= req.Parts[i-1].PartNumber {
				return s3error(http.StatusBadRequest, "InvalidPartOrder", "The list of parts was not in ascending order.", id)
			}
			last := i == len(req.Parts)-1
			if !last && len(part) < s.MinPartSize {
				return s3error(http.StatusBadRequest, "EntityTooSmall", "Your proposed upload is smaller than the minimum allowed object size.", id)
			}
			if !last && size >= 0 && len(part) != size {
				return s3error(http.StatusBadRequest, "InvalidPart", "All non-trailing parts must have the same length.", id)
			}
			size = len(part)
			data = append(data, part...)
			sums = append(sums, sum[:]...)
		}
		if a, refused := s.precondition(r, objects, bucket, key, id); refused {
			// The upload stays open, as on S3: the client aborts it.
			return a
		}
		o := s.store(bucket, key, data, u.contentType, u.meta)
		all := md5.Sum(sums)
		o.etag = `"` + hex.EncodeToString(all[:]) + "-" + strconv.Itoa(len(req.Parts)) + `"`
		delete(s.uploads, q.Get("uploadId"))
		var b bytes.Buffer
		b.WriteString(xml.Header + `<CompleteMultipartUploadResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Bucket>`)
		xml.EscapeText(&b, []byte(bucket))
		b.WriteString("</Bucket><Key>")
		xml.EscapeText(&b, []byte(key))
		b.WriteString("</Key><ETag>")
		xml.EscapeText(&b, []byte(o.etag))
		b.WriteString("</ETag></CompleteMultipartUploadResult>")
		return answer{status: http.StatusOK, header: http.Header{"Content-Type": {"application/xml"}}, body: b.Bytes()}
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
