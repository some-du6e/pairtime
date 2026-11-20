// SPDX-License-Identifier: AGPL-3.0-or-later

package objstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/server/objstore/sigv4"
)

// DefaultPartSize is the part size PutStream uses unless told otherwise.
// A 10 GB snapshot, about what DESIGN 10.6 expects at R11 scale, is 150
// parts of it, and one part at a time is all PutStream holds in memory.
const DefaultPartSize = 64 << 20

// MinPartSize is the smallest part S3, R2 and MinIO take, except for the
// last part of an upload.
const MinPartSize = 5 << 20

// MaxParts is the most parts one multipart upload may have on S3 and R2.
const MaxParts = 10000

// abortTimeout bounds AbortMultipartUpload after a failed upload. The
// abort runs even when the upload failed because its context ended.
const abortTimeout = 30 * time.Second

// metaUpload is the user metadata header a multipart upload carries a
// random token in, so a retried CompleteMultipartUpload that finds the
// key taken can tell whether its own earlier attempt took it. The parts
// are streamed, so no SHA-256 of the whole object is known when the
// upload starts, which is when S3 takes the metadata.
const metaUpload = "X-Amz-Meta-Pairtime-Upload"

// ErrNotImplemented: the store doesn't implement the call, as R2 doesn't
// GetBucketVersioning.
var ErrNotImplemented = errors.New("objstore: the store doesn't implement this call")

// errNoSuchUpload marks S3's NoSuchUpload, which a retried
// CompleteMultipartUpload can get after an earlier attempt finished it.
var errNoSuchUpload = errors.New("objstore: no such multipart upload")

// errCutAnswer marks a CompleteMultipartUpload answer that broke off after
// its headers. S3 sends them before it finishes, so the upload may or may
// not be complete, and the call is tried again.
var errCutAnswer = errors.New("the answer broke off")

// StreamOptions are the choices of PutStream.
type StreamOptions struct {
	// ContentType is stored with the object; empty means
	// application/octet-stream.
	ContentType string
	// IfNoneMatch makes the upload write-once, as PutOptions.IfNoneMatch
	// does: a single put sends If-None-Match: *, and a multipart upload
	// sends it with CompleteMultipartUpload, where S3 and R2 evaluate it
	// when the object would appear. When the key exists, PutStream
	// aborts the upload and fails with ErrExists.
	IfNoneMatch bool
	// PartSize is the size of every part but the last, and the most
	// PutStream sends in a single PutObject. 0 means DefaultPartSize. It
	// must be between MinPartSize and MaxPutSize. R2 needs every part but
	// the last to be the same size, so PutStream never varies it.
	PartSize int64
}

// PutStream stores everything r yields under key, without knowing its
// length in advance: an age-encrypted snapshot is written as it is read,
// so there is no file to seek in (DESIGN 10.3). It reads r one part at a
// time. When r ends within the first part, PutStream sends one PutObject
// with Put, which also stores the body's SHA-256. Otherwise it makes a
// multipart upload: CreateMultipartUpload, an UploadPart for each part,
// each retried like any call, then CompleteMultipartUpload. When any step
// fails, or ctx ends, it sends AbortMultipartUpload, so the store keeps
// no parts, and returns the error.
//
// A multipart object has no x-amz-meta-sha256, so ObjectInfo.SHA256 is
// empty for it, and its ETag isn't an MD5 of its bytes. age's
// authenticated encryption is what shows a snapshot is whole.
func (b *Bucket) PutStream(ctx context.Context, key string, r io.Reader, opts StreamOptions) (ObjectInfo, error) {
	if err := CheckKey(key); err != nil {
		return ObjectInfo{}, err
	}
	partSize := opts.PartSize
	if partSize == 0 {
		partSize = DefaultPartSize
	}
	if partSize < MinPartSize || partSize > MaxPutSize {
		return ObjectInfo{}, &Error{Op: "PutObject", Bucket: b.name, Key: key,
			Err: fmt.Errorf("the part size is %d bytes; it must be from %d to %d", partSize, MinPartSize, int64(MaxPutSize))}
	}
	buf := make([]byte, partSize)
	n, err := io.ReadFull(r, buf)
	switch {
	case errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF):
		return b.Put(ctx, key, bytes.NewReader(buf[:n]), PutOptions{ContentType: opts.ContentType, IfNoneMatch: opts.IfNoneMatch})
	case err != nil:
		return ObjectInfo{}, &Error{Op: "PutObject", Bucket: b.name, Key: key, Err: fmt.Errorf("reading the body: %w", err)}
	}
	// A full first part: one more byte says whether there is a second.
	var next [1]byte
	m, err := io.ReadFull(r, next[:])
	switch {
	case errors.Is(err, io.EOF):
		return b.Put(ctx, key, bytes.NewReader(buf), PutOptions{ContentType: opts.ContentType, IfNoneMatch: opts.IfNoneMatch})
	case err != nil:
		return ObjectInfo{}, &Error{Op: "PutObject", Bucket: b.name, Key: key, Err: fmt.Errorf("reading the body: %w", err)}
	}
	rest := io.MultiReader(bytes.NewReader(next[:m]), r)
	return b.multipart(ctx, key, buf, rest, partSize, opts)
}

// multipart uploads first, a full part, and then the rest of r.
func (b *Bucket) multipart(ctx context.Context, key string, first []byte, r io.Reader, partSize int64, opts StreamOptions) (ObjectInfo, error) {
	ctype := opts.ContentType
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	token, err := randomToken()
	if err != nil {
		return ObjectInfo{}, &Error{Op: "CreateMultipartUpload", Bucket: b.name, Key: key, Err: err}
	}
	id, err := b.createUpload(ctx, key, ctype, token)
	if err != nil {
		return ObjectInfo{}, err
	}
	info, err := b.uploadParts(ctx, key, id, token, first, r, partSize, opts.IfNoneMatch)
	if err != nil {
		actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), abortTimeout)
		defer cancel()
		if aerr := b.abortUpload(actx, key, id); aerr != nil {
			return ObjectInfo{}, errors.Join(err, fmt.Errorf("and aborting the upload failed, so its parts may stay in the bucket until a lifecycle rule removes them: %w", aerr))
		}
		return ObjectInfo{}, err
	}
	info.ContentType = ctype
	return info, nil
}

// completedPart is one part of a CompleteMultipartUpload body.
type completedPart struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

// uploadParts sends first and the rest of r as parts of upload id, then
// completes the upload.
func (b *Bucket) uploadParts(ctx context.Context, key, id, token string, first []byte, r io.Reader, partSize int64, ifNoneMatch bool) (ObjectInfo, error) {
	var parts []completedPart
	var size int64
	buf := first
	for n := 1; ; n++ {
		if n > MaxParts {
			return ObjectInfo{}, &Error{Op: "UploadPart", Bucket: b.name, Key: key,
				Err: fmt.Errorf("%w: more than %d parts of %d bytes", ErrTooLarge, MaxParts, partSize)}
		}
		etag, err := b.uploadPart(ctx, key, id, n, buf)
		if err != nil {
			return ObjectInfo{}, err
		}
		parts = append(parts, completedPart{PartNumber: n, ETag: etag})
		size += int64(len(buf))
		if int64(len(buf)) < partSize {
			break
		}
		if buf, err = readPart(r, first[:cap(first)], partSize); err != nil {
			return ObjectInfo{}, &Error{Op: "UploadPart", Bucket: b.name, Key: key, Err: fmt.Errorf("reading the body: %w", err)}
		}
		if len(buf) == 0 {
			break
		}
	}
	return b.completeUpload(ctx, key, id, token, parts, size, ifNoneMatch)
}

// readPart reads up to partSize bytes of r into buf, which it reuses, and
// returns them; fewer only at the end of r.
func readPart(r io.Reader, buf []byte, partSize int64) ([]byte, error) {
	n, err := io.ReadFull(r, buf[:partSize])
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return buf[:n], nil
	}
	return buf[:n], err
}

// initiateResult is CreateMultipartUpload's answer.
type initiateResult struct {
	UploadID string `xml:"UploadId"`
}

func (b *Bucket) createUpload(ctx context.Context, key, ctype, token string) (string, error) {
	resp, err := b.c.do(ctx, request{
		op: "CreateMultipartUpload", method: http.MethodPost, bucket: b.name, key: key, rawQuery: "uploads",
		header: http.Header{"Content-Type": {ctype}, metaUpload: {token}},
	})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var res initiateResult
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&res); err != nil {
		return "", &Error{Op: "CreateMultipartUpload", Bucket: b.name, Key: key, Status: resp.StatusCode, Err: fmt.Errorf("reading the answer: %w", err)}
	}
	if res.UploadID == "" || len(res.UploadID) > 1024 || strings.ContainsFunc(res.UploadID, func(r rune) bool { return r <= ' ' || r > '~' }) {
		return "", &Error{Op: "CreateMultipartUpload", Bucket: b.name, Key: key, Status: resp.StatusCode, Err: errors.New("the answer has no usable upload id")}
	}
	return res.UploadID, nil
}

func (b *Bucket) uploadPart(ctx context.Context, key, id string, n int, part []byte) (string, error) {
	sum := sha256.Sum256(part)
	resp, err := b.c.do(ctx, request{
		op: "UploadPart", method: http.MethodPut, bucket: b.name, key: key,
		rawQuery: "partNumber=" + strconv.Itoa(n) + "&uploadId=" + sigv4.URIEncode(id, true),
		body:     bytes.NewReader(part), size: int64(len(part)), payloadHash: hex.EncodeToString(sum[:]),
	})
	if err != nil {
		return "", err
	}
	defer drain(resp)
	etag := resp.Header.Get("ETag")
	if etag == "" {
		return "", &Error{Op: "UploadPart", Bucket: b.name, Key: key, Status: resp.StatusCode, Err: fmt.Errorf("part %d's answer has no ETag", n)}
	}
	return etag, nil
}

// completeResult is CompleteMultipartUpload's answer. S3 can answer 200
// and still fail, with an Error body instead, so the root element's name
// is read too.
type completeResult struct {
	XMLName   xml.Name
	ETag      string `xml:"ETag"`
	Code      string `xml:"Code"`
	Message   string `xml:"Message"`
	RequestID string `xml:"RequestId"`
}

func (b *Bucket) completeUpload(ctx context.Context, key, id, token string, parts []completedPart, size int64, ifNoneMatch bool) (ObjectInfo, error) {
	body, err := xml.Marshal(struct {
		XMLName xml.Name        `xml:"CompleteMultipartUpload"`
		Parts   []completedPart `xml:"Part"`
	}{Parts: parts})
	if err != nil {
		return ObjectInfo{}, &Error{Op: "CompleteMultipartUpload", Bucket: b.name, Key: key, Err: err}
	}
	header := http.Header{"Content-Type": {"application/xml"}}
	if ifNoneMatch {
		header.Set("If-None-Match", "*")
	}
	sum := sha256.Sum256(body)
	var retried bool
	var etag string
	for attempt := 1; ; attempt++ {
		var resp *http.Response
		resp, err = b.c.do(ctx, request{
			op: "CompleteMultipartUpload", method: http.MethodPost, bucket: b.name, key: key,
			rawQuery: "uploadId=" + sigv4.URIEncode(id, true), header: header,
			body: bytes.NewReader(body), size: int64(len(body)), payloadHash: hex.EncodeToString(sum[:]),
			retried: &retried,
		})
		if err == nil {
			etag, err = b.readComplete(resp, key)
		}
		// do retries what failed in the status line. S3 can also answer
		// 200 and fail in the body, or lose the body after the headers,
		// and both get the same tries.
		var e *Error
		if err == nil || !errors.As(err, &e) || e.Status != http.StatusOK || attempt >= b.c.attempts {
			break
		}
		if !errors.Is(err, errCutAnswer) && !retryable(ctx, err) {
			break
		}
		if ctx.Err() != nil {
			break
		}
		retried = true
		if serr := b.c.sleep(ctx, time.Second); serr != nil {
			break
		}
	}
	if err != nil {
		if !retried || !(errors.Is(err, ErrExists) || errors.Is(err, errNoSuchUpload) || errors.Is(err, errCutAnswer)) {
			return ObjectInfo{}, err
		}
		// An attempt failed without saying whether the store carried it
		// out, and the retry found the upload gone or the key taken:
		// perhaps that attempt completed the upload.
		head, herr := b.Head(ctx, key)
		if herr != nil || head.Size != size || head.upload != token {
			return ObjectInfo{}, err
		}
		return head, nil
	}
	return ObjectInfo{Key: key, Size: size, ETag: strings.Trim(etag, `"`)}, nil
}

// readComplete reads CompleteMultipartUpload's answer, closes it and
// returns the object's ETag.
func (b *Bucket) readComplete(resp *http.Response, key string) (string, error) {
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", &Error{Op: "CompleteMultipartUpload", Bucket: b.name, Key: key, Status: resp.StatusCode, Err: fmt.Errorf("%w: %v", errCutAnswer, err)}
	}
	var res completeResult
	if err := xml.Unmarshal(data, &res); err != nil {
		return "", &Error{Op: "CompleteMultipartUpload", Bucket: b.name, Key: key, Status: resp.StatusCode, Err: fmt.Errorf("reading the answer: %w", err)}
	}
	if res.XMLName.Local == "Error" {
		return "", &Error{Op: "CompleteMultipartUpload", Bucket: b.name, Key: key, Status: resp.StatusCode,
			Code: token(res.Code), Message: printable(res.Message, 200), RequestID: token(res.RequestID),
			Err: errors.New("the store answered 200 with an error")}
	}
	return res.ETag, nil
}

func (b *Bucket) abortUpload(ctx context.Context, key, id string) error {
	resp, err := b.c.do(ctx, request{
		op: "AbortMultipartUpload", method: http.MethodDelete, bucket: b.name, key: key,
		rawQuery: "uploadId=" + sigv4.URIEncode(id, true),
	})
	if err != nil {
		if errors.Is(err, errNoSuchUpload) {
			return nil
		}
		return err
	}
	drain(resp)
	return nil
}

// randomToken is 128 random bits in hex.
func randomToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// The versioning states GetBucketVersioning reports (S3 API reference).
// A bucket whose versioning was never turned on reports no state, "".
const (
	VersioningEnabled   = "Enabled"
	VersioningSuspended = "Suspended"
)

// Versioning returns the bucket's versioning state: "" when versioning
// was never turned on, VersioningEnabled or VersioningSuspended. A
// suspended bucket still holds the versions it kept while it was on. A
// store without GetBucketVersioning, such as R2, fails with
// ErrNotImplemented. Neither of pairtimed's buckets may keep versions
// (docs/errata/2026-11-20-evidence-versioning.md), and pairtimed doctor
// fails on either state.
func (b *Bucket) Versioning(ctx context.Context) (string, error) {
	resp, err := b.c.do(ctx, request{op: "GetBucketVersioning", method: http.MethodGet, bucket: b.name, rawQuery: "versioning"})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var res struct {
		Status string `xml:"Status"`
	}
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&res); err != nil {
		return "", &Error{Op: "GetBucketVersioning", Bucket: b.name, Status: resp.StatusCode, Err: fmt.Errorf("reading the answer: %w", err)}
	}
	switch res.Status {
	case "", VersioningEnabled, VersioningSuspended:
		return res.Status, nil
	default:
		return "", &Error{Op: "GetBucketVersioning", Bucket: b.name, Status: resp.StatusCode, Err: fmt.Errorf("unknown versioning state %q", printable(res.Status, 40))}
	}
}
