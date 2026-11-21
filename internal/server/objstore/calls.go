// SPDX-License-Identifier: AGPL-3.0-or-later

package objstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/server/objstore/sigv4"
)

// ProbeKey is the key Check fetches. pairtimed never writes it.
const ProbeKey = "pairtime-readyz-probe"

// metaSHA256 is the user metadata header Put stores the body's hex
// SHA-256 in. S3, R2 and MinIO keep x-amz-meta-* headers with the object
// and return them on GET and HEAD.
const metaSHA256 = "X-Amz-Meta-Sha256"

// ObjectInfo describes a stored object.
type ObjectInfo struct {
	Key  string
	Size int64
	// ETag is the store's entity tag, without its quotes.
	ETag         string
	LastModified time.Time
	// ContentType is set by Get and Head only; listings don't carry it.
	ContentType string
	// SHA256 is the hex SHA-256 that Put stored with the object, in its
	// x-amz-meta-sha256. Get and Head set it; listings don't carry it,
	// and an object written some other way has none. The store doesn't
	// check it against the bytes, so it is only the writer's word, which
	// is why Evidence.Get hashes the bytes it reads.
	SHA256 string
	// upload is the token a multipart upload stored with the object
	// (metaUpload), set by Get and Head.
	upload string
}

// PutOptions are the choices of a put.
type PutOptions struct {
	// ContentType is stored with the object; empty means
	// application/octet-stream.
	ContentType string
	// IfNoneMatch makes the put write-once: it sends If-None-Match: *,
	// and when the key already exists the store keeps what it has and
	// Put fails with ErrExists.
	//
	// A retried put can find its own upload there. An attempt that got no
	// answer, or a 5xx, may have been stored all the same, with only its
	// answer lost, and then the retry gets ErrExists. So when ErrExists
	// follows a retry, Put sends a HEAD and succeeds if the key holds
	// this body: the same size and the same SHA-256 in ObjectInfo.SHA256.
	// ErrExists on the first attempt means the key was there before this
	// call, and Put doesn't check. A caller that repeats a failed
	// write-once put can get ErrExists for its own earlier upload; Head
	// and ObjectInfo.SHA256 tell whether the key holds the same body.
	IfNoneMatch bool
}

// Put stores body under key, from body's current offset to its end. It
// reads the body for its size and SHA-256, which is signed into the
// request so the store refuses a body that changed on the way, and then
// once for each attempt to send it. Nothing reads the body after Put
// returns, even when the store answered before it had all of it. The
// SHA-256 is also stored with the object, as x-amz-meta-sha256, and Get
// and Head return it in ObjectInfo.SHA256. Bodies over MaxPutSize fail
// with ErrTooLarge.
func (b *Bucket) Put(ctx context.Context, key string, body io.ReadSeeker, opts PutOptions) (ObjectInfo, error) {
	if err := CheckKey(key); err != nil {
		return ObjectInfo{}, err
	}
	fail := func(err error) (ObjectInfo, error) {
		return ObjectInfo{}, &Error{Op: "PutObject", Bucket: b.name, Key: key, Err: err}
	}
	start, err := body.Seek(0, io.SeekCurrent)
	if err != nil {
		return fail(fmt.Errorf("reading the body: %w", err))
	}
	end, err := body.Seek(0, io.SeekEnd)
	if err != nil {
		return fail(fmt.Errorf("reading the body: %w", err))
	}
	size := end - start
	if size > MaxPutSize {
		return fail(fmt.Errorf("%w: the body is %d bytes, over %d", ErrTooLarge, size, int64(MaxPutSize)))
	}
	if _, err := body.Seek(start, io.SeekStart); err != nil {
		return fail(fmt.Errorf("reading the body: %w", err))
	}
	h := sha256.New()
	if n, err := io.Copy(h, io.LimitReader(body, size)); err != nil || n != size {
		return fail(fmt.Errorf("reading the body: got %d of %d bytes: %v", n, size, err))
	}
	sum := hex.EncodeToString(h.Sum(nil))
	ctype := opts.ContentType
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	header := http.Header{"Content-Type": {ctype}, metaSHA256: {sum}}
	if opts.IfNoneMatch {
		header.Set("If-None-Match", "*")
	}
	var retried bool
	resp, err := b.c.do(ctx, request{
		op: "PutObject", method: http.MethodPut, bucket: b.name, key: key,
		header: header, body: body, start: start, size: size,
		payloadHash: sum, retried: &retried,
	})
	if err != nil {
		if !opts.IfNoneMatch || !retried || !errors.Is(err, ErrExists) {
			return ObjectInfo{}, err
		}
		// An attempt failed without saying whether the store carried it
		// out, and the retry found the key taken, perhaps by that attempt.
		head, ours, herr := b.holds(ctx, key, size, sum)
		if herr != nil {
			return fail(fmt.Errorf("a retry found the key taken, and checking whether an earlier attempt stored this body failed: %w", herr))
		}
		if !ours {
			return ObjectInfo{}, err
		}
		return head, nil
	}
	defer drain(resp)
	return ObjectInfo{Key: key, Size: size, ETag: strings.Trim(resp.Header.Get("ETag"), `"`), ContentType: ctype, SHA256: sum}, nil
}

// holds reports whether key holds size bytes whose SHA-256, as Put stored
// it, is sum, and returns what Head says about the object.
func (b *Bucket) holds(ctx context.Context, key string, size int64, sum string) (ObjectInfo, bool, error) {
	head, err := b.Head(ctx, key)
	if err != nil {
		return ObjectInfo{}, false, err
	}
	return head, head.Size == size && head.SHA256 == sum, nil
}

// Get returns key's body, which the caller reads and closes, and what the
// answer's headers say about the object. A missing key fails with
// ErrNotFound. Only getting the answer is retried, not reading the body.
func (b *Bucket) Get(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	if err := CheckKey(key); err != nil {
		return nil, ObjectInfo{}, err
	}
	resp, err := b.c.do(ctx, request{op: "GetObject", method: http.MethodGet, bucket: b.name, key: key})
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	return resp.Body, info(key, resp), nil
}

// Head returns what the store says about key without its body. A missing
// key fails with ErrNotFound.
func (b *Bucket) Head(ctx context.Context, key string) (ObjectInfo, error) {
	if err := CheckKey(key); err != nil {
		return ObjectInfo{}, err
	}
	resp, err := b.c.do(ctx, request{op: "HeadObject", method: http.MethodHead, bucket: b.name, key: key})
	if err != nil {
		return ObjectInfo{}, err
	}
	defer drain(resp)
	return info(key, resp), nil
}

// Delete removes key. Deleting a missing key succeeds, as it does in S3.
// pairtimed's buckets never keep versions (DESIGN 10.3), and pairtimed
// doctor fails on one that does, so Delete removes the object for good.
func (b *Bucket) Delete(ctx context.Context, key string) error {
	if err := CheckKey(key); err != nil {
		return err
	}
	resp, err := b.c.do(ctx, request{op: "DeleteObject", method: http.MethodDelete, bucket: b.name, key: key})
	if err != nil {
		return err
	}
	drain(resp)
	return nil
}

func info(key string, resp *http.Response) ObjectInfo {
	i := ObjectInfo{
		Key:         key,
		Size:        resp.ContentLength,
		ETag:        strings.Trim(resp.Header.Get("ETag"), `"`),
		ContentType: resp.Header.Get("Content-Type"),
		SHA256:      resp.Header.Get(metaSHA256),
		upload:      resp.Header.Get(metaUpload),
	}
	if t, err := http.ParseTime(resp.Header.Get("Last-Modified")); err == nil {
		i.LastModified = t
	}
	return i
}

// maxListPage bounds one ListObjectsV2 answer. A page of 1,000 keys is
// a few hundred KB.
const maxListPage = 16 << 20

// listPage is the part of a ListObjectsV2 answer that List reads.
type listPage struct {
	IsTruncated           bool   `xml:"IsTruncated"`
	NextContinuationToken string `xml:"NextContinuationToken"`
	EncodingType          string `xml:"EncodingType"`
	Contents              []struct {
		Key          string    `xml:"Key"`
		Size         int64     `xml:"Size"`
		ETag         string    `xml:"ETag"`
		LastModified time.Time `xml:"LastModified"`
	} `xml:"Contents"`
}

// List yields every object whose key starts with prefix, in the store's
// order, which S3 defines as ascending UTF-8 byte order. It asks for keys
// URL-encoded, as S3 recommends so that any key survives the XML, and
// decodes them. On an error it yields the error once and stops.
func (b *Bucket) List(ctx context.Context, prefix string) iter.Seq2[ObjectInfo, error] {
	return func(yield func(ObjectInfo, error) bool) {
		fail := func(err error) {
			yield(ObjectInfo{}, &Error{Op: "ListObjectsV2", Bucket: b.name, Key: prefix, Err: err})
		}
		cont := ""
		for {
			q := "encoding-type=url&list-type=2&max-keys=" + strconv.Itoa(b.pageSize) + "&prefix=" + sigv4.URIEncode(prefix, true)
			if cont != "" {
				q = "continuation-token=" + sigv4.URIEncode(cont, true) + "&" + q
			}
			resp, err := b.c.do(ctx, request{op: "ListObjectsV2", method: http.MethodGet, bucket: b.name, rawQuery: q})
			if err != nil {
				yield(ObjectInfo{}, err)
				return
			}
			var page listPage
			err = xml.NewDecoder(io.LimitReader(resp.Body, maxListPage)).Decode(&page)
			resp.Body.Close()
			if err != nil {
				fail(fmt.Errorf("reading the listing: %w", err))
				return
			}
			for _, o := range page.Contents {
				key := o.Key
				if page.EncodingType == "url" {
					if key, err = url.QueryUnescape(o.Key); err != nil {
						fail(fmt.Errorf("the listing has a key that isn't URL-encoded: %w", err))
						return
					}
				}
				if !yield(ObjectInfo{Key: key, Size: o.Size, ETag: strings.Trim(o.ETag, `"`), LastModified: o.LastModified}, nil) {
					return
				}
			}
			if !page.IsTruncated {
				return
			}
			if page.NextContinuationToken == "" || page.NextContinuationToken == cont {
				fail(errors.New("the listing is truncated but gives no new continuation token"))
				return
			}
			cont = page.NextContinuationToken
		}
	}
}

// Check reports whether the bucket answers these credentials. It fetches
// ProbeKey, which pairtimed never writes, and passes on S3's NoSuchKey,
// which only comes back when the endpoint, the signature, the bucket and
// the permission to read it are all right. On AWS that answer also needs
// the permission to list, which List needs anyway; without it S3 says
// AccessDenied and the check fails. A GET is a class B operation on R2,
// the cheapest kind (SP 9.2). The check is tried once: /readyz calls it
// often and bounds it with a short deadline.
func (b *Bucket) Check(ctx context.Context) error {
	resp, err := b.c.do(ctx, request{op: "GetObject", method: http.MethodGet, bucket: b.name, key: ProbeKey, attempts: 1})
	if err == nil {
		drain(resp)
		return nil
	}
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}
