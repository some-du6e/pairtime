// SPDX-License-Identifier: AGPL-3.0-or-later

package objstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/objstore/objstoretest"
	"github.com/some-du6e/pairtime/internal/server/objstore/sigv4"
)

const (
	mainBucket     = "pairtime"
	snapshotBucket = "pairtime-snapshots"
)

// newTestClient returns a client of srv that doesn't wait between
// attempts.
func newTestClient(t *testing.T, srv *objstoretest.Server, edit ...func(*Config)) *Client {
	t.Helper()
	cfg := Config{Endpoint: srv.URL, Region: objstoretest.Region, Credentials: objstoretest.Credentials}
	for _, e := range edit {
		e(&cfg)
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.sleep = func(context.Context, time.Duration) error { return nil }
	return c
}

func bucket(t *testing.T, c *Client, name string) *Bucket {
	t.Helper()
	b, err := c.Bucket(name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseEndpoint(t *testing.T) {
	tests := []struct {
		name, raw, want string
		wantErr         string
	}{
		{name: "an R2 endpoint is kept as its origin", raw: "https://0123abcd.r2.cloudflarestorage.com", want: "https://0123abcd.r2.cloudflarestorage.com"},
		{name: "a trailing slash is dropped", raw: "https://fsn1.your-objectstorage.com/", want: "https://fsn1.your-objectstorage.com"},
		{name: "https's default port is dropped, so the signed host is the sent one", raw: "https://s3.example:443", want: "https://s3.example"},
		{name: "another port is kept", raw: "https://s3.example:8443", want: "https://s3.example:8443"},
		{name: "the host is lowercased", raw: "https://S3.Example", want: "https://s3.example"},
		{name: "http is allowed on 127.0.0.1, for MinIO in CI", raw: "http://127.0.0.1:9000", want: "http://127.0.0.1:9000"},
		{name: "http is allowed on localhost", raw: "http://localhost:9000", want: "http://localhost:9000"},
		{name: "http is allowed on the IPv6 loopback", raw: "http://[::1]:9000", want: "http://[::1]:9000"},
		{name: "http elsewhere is refused", raw: "http://s3.example", wantErr: "only https"},
		{name: "an empty endpoint is refused", raw: "", wantErr: "required"},
		{name: "a path is refused", raw: "https://s3.example/bucket", wantErr: "origin"},
		{name: "a query is refused", raw: "https://s3.example/?x=1", wantErr: "origin"},
		{name: "another scheme is refused", raw: "ftp://s3.example", wantErr: "https://"},
		{name: "a user part is refused", raw: "https://AKID:hunter2@s3.example", wantErr: "user or password"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := ParseEndpoint(tt.raw)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ParseEndpoint(%q) error = %v, want one containing %q", tt.raw, err, tt.wantErr)
				}
				if strings.Contains(err.Error(), "hunter2") {
					t.Errorf("the error quotes the password: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if u.String() != tt.want {
				t.Errorf("ParseEndpoint(%q) = %q, want %q", tt.raw, u, tt.want)
			}
		})
	}
}

func TestCheckNames(t *testing.T) {
	tests := []struct {
		name  string
		check func(string) error
		value string
		ok    bool
	}{
		{name: "a bucket of lowercase letters and a hyphen is fine", check: CheckBucketName, value: "pairtime-snapshots", ok: true},
		{name: "a bucket of three characters is fine", check: CheckBucketName, value: "abc", ok: true},
		{name: "a bucket of two characters is refused", check: CheckBucketName, value: "ab"},
		{name: "a bucket of 64 characters is refused", check: CheckBucketName, value: strings.Repeat("a", 64)},
		{name: "a bucket with an uppercase letter is refused", check: CheckBucketName, value: "Pairtime"},
		{name: "a bucket with a dot is refused, to keep virtual hosts under the TLS certificate", check: CheckBucketName, value: "pair.time"},
		{name: "a bucket starting with a hyphen is refused", check: CheckBucketName, value: "-pairtime"},
		{name: "a bucket ending with a hyphen is refused", check: CheckBucketName, value: "pairtime-"},
		{name: "an evidence key is fine", check: CheckKey, value: EvidenceKey(sha256.Sum256(nil)), ok: true},
		{name: "a snapshot key is fine", check: CheckKey, value: SnapshotKey(time.Date(2026, 11, 19, 3, 10, 0, 0, time.UTC)), ok: true},
		{name: "an empty key is refused", check: CheckKey, value: ""},
		{name: "a key over 1024 bytes is refused", check: CheckKey, value: strings.Repeat("a", 1025)},
		{name: "a key with a space is refused", check: CheckKey, value: "a b"},
		{name: "a key with a non-ASCII letter is refused", check: CheckKey, value: "évidence/x"},
		{name: "a key starting with a slash is refused", check: CheckKey, value: "/evidence/x"},
		{name: "a key ending with a slash is refused", check: CheckKey, value: "evidence/"},
		{name: "a key with two slashes in a row is refused", check: CheckKey, value: "evidence//x"},
		{name: "a key with a .. segment is refused", check: CheckKey, value: "evidence/../x"},
		{name: "a region of lowercase letters, digits and hyphens is fine", check: CheckRegion, value: "us-east-1", ok: true},
		{name: "R2's region is fine", check: CheckRegion, value: "auto", ok: true},
		{name: "an uppercase region is refused", check: CheckRegion, value: "US-EAST-1"},
		{name: "an empty region is refused", check: CheckRegion, value: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.check(tt.value); (err == nil) != tt.ok {
				t.Errorf("check(%q) = %v, want ok = %v", tt.value, err, tt.ok)
			}
		})
	}
}

func TestNew(t *testing.T) {
	creds := sigv4.Credentials{AccessKeyID: "a", SecretAccessKey: "s"}
	tests := []struct {
		name string
		cfg  Config
		// wantErr is a substring of New's error; "" means New succeeds.
		wantErr string
	}{
		{name: "no access key id is refused", wantErr: "both required",
			cfg: Config{Endpoint: "https://s3.example", Credentials: sigv4.Credentials{SecretAccessKey: "s"}}},
		{name: "no secret is refused", wantErr: "both required",
			cfg: Config{Endpoint: "https://s3.example", Credentials: sigv4.Credentials{AccessKeyID: "a"}}},
		{name: "path-style URLs over http on loopback are allowed, for MinIO in CI",
			cfg: Config{Endpoint: "http://127.0.0.1:9000", Credentials: creds}},
		{name: "virtual-hosted URLs over https on a host name are allowed",
			cfg: Config{Endpoint: "https://fsn1.your-objectstorage.com", VirtualHost: true, Credentials: creds}},
		{name: "virtual-hosted URLs over http are refused, since <bucket>.localhost isn't a loopback host", wantErr: "need an https endpoint",
			cfg: Config{Endpoint: "http://localhost:9000", VirtualHost: true, Credentials: creds}},
		{name: "virtual-hosted URLs over http are refused, since <bucket>.127.0.0.1 isn't a loopback host", wantErr: "need an https endpoint",
			cfg: Config{Endpoint: "http://127.0.0.1:9000", VirtualHost: true, Credentials: creds}},
		{name: "virtual-hosted URLs on an IPv4 address are refused", wantErr: "not the IP address",
			cfg: Config{Endpoint: "https://192.0.2.10", VirtualHost: true, Credentials: creds}},
		{name: "virtual-hosted URLs on an IPv6 address are refused", wantErr: "not the IP address",
			cfg: Config{Endpoint: "https://[2001:db8::1]:8443", VirtualHost: true, Credentials: creds}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.cfg)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("New error = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestCalls runs each S3 call against the test server, which checks every
// signature and body hash. Each step runs on the state the step before it
// left.
func TestCalls(t *testing.T) {
	srv := objstoretest.New(t, mainBucket)
	c := newTestClient(t, srv)
	b := bucket(t, c, mainBucket)
	b.pageSize = 2
	ctx := context.Background()
	keys := []string{"logs/a", "logs/b", "logs/c", "logs/d", "logs/e"}

	steps := []struct {
		name string
		run  func(t *testing.T)
	}{
		{name: "put stores the body and returns its size and ETag", run: func(t *testing.T) {
			info, err := b.Put(ctx, "notes/one", strings.NewReader("hello"), PutOptions{ContentType: "text/plain"})
			if err != nil {
				t.Fatal(err)
			}
			if got, _ := srv.Object(mainBucket, "notes/one"); string(got) != "hello" || info.Size != 5 || info.ETag == "" {
				t.Errorf("stored %q, info %+v; want hello, size 5 and an ETag", got, info)
			}
		}},
		{name: "put sends the body's SHA-256 and signs it", run: func(t *testing.T) {
			reqs := srv.Requests()
			last := reqs[len(reqs)-1]
			if got := last.Header.Get("X-Amz-Content-Sha256"); got != sigv4.PayloadHash([]byte("hello")) {
				t.Errorf("X-Amz-Content-Sha256 = %q", got)
			}
			if !strings.Contains(last.Header.Get("Authorization"), "x-amz-content-sha256") {
				t.Errorf("the payload hash isn't signed: %q", last.Header.Get("Authorization"))
			}
		}},
		{name: "put reads from the body's current offset", run: func(t *testing.T) {
			r := strings.NewReader("skip-kept")
			r.Seek(5, io.SeekStart)
			if _, err := b.Put(ctx, "notes/offset", r, PutOptions{}); err != nil {
				t.Fatal(err)
			}
			if got, _ := srv.Object(mainBucket, "notes/offset"); string(got) != "kept" {
				t.Errorf("stored %q, want kept", got)
			}
		}},
		{name: "get returns the stored bytes and their type", run: func(t *testing.T) {
			body, info, err := b.Get(ctx, "notes/one")
			if err != nil {
				t.Fatal(err)
			}
			defer body.Close()
			got, _ := io.ReadAll(body)
			if string(got) != "hello" || info.ContentType != "text/plain" || info.Size != 5 {
				t.Errorf("got %q with %+v", got, info)
			}
		}},
		{name: "head returns the size, ETag, time and the SHA-256 put stored, without the body", run: func(t *testing.T) {
			info, err := b.Head(ctx, "notes/one")
			if err != nil {
				t.Fatal(err)
			}
			if info.Size != 5 || info.ETag == "" || info.LastModified.IsZero() || info.SHA256 != sigv4.PayloadHash([]byte("hello")) {
				t.Errorf("head = %+v", info)
			}
		}},
		{name: "a write-once put of a new key stores it", run: func(t *testing.T) {
			if _, err := b.Put(ctx, "once/k", strings.NewReader("first"), PutOptions{IfNoneMatch: true}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "a write-once put of an existing key fails with ErrExists and keeps the first bytes", run: func(t *testing.T) {
			_, err := b.Put(ctx, "once/k", strings.NewReader("second"), PutOptions{IfNoneMatch: true})
			if !errors.Is(err, ErrExists) {
				t.Fatalf("second put error = %v, want ErrExists", err)
			}
			if got, _ := srv.Object(mainBucket, "once/k"); string(got) != "first" {
				t.Errorf("stored %q after the refused put, want first", got)
			}
			if reqs := srv.Requests(); reqs[len(reqs)-1].Header.Get("If-None-Match") != "*" {
				t.Error("the write-once put didn't send If-None-Match: *")
			}
		}},
		{name: "list yields every key under the prefix in order, across pages", run: func(t *testing.T) {
			for _, k := range keys {
				if _, err := b.Put(ctx, k, strings.NewReader(k), PutOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			var got []string
			for o, err := range b.List(ctx, "logs/") {
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, o.Key)
				if o.Size != int64(len(o.Key)) || o.ETag == "" || o.LastModified.IsZero() {
					t.Errorf("listed %+v", o)
				}
			}
			if !slices.Equal(got, keys) {
				t.Errorf("listed %q, want %q", got, keys)
			}
		}},
		{name: "list stops when the caller stops", run: func(t *testing.T) {
			n := 0
			for range b.List(ctx, "logs/") {
				n++
				break
			}
			if n != 1 {
				t.Errorf("yielded %d after break", n)
			}
		}},
		{name: "list decodes the URL-encoded keys it asks for", run: func(t *testing.T) {
			srv.SetObject(mainBucket, "odd/a b+c", []byte("x"))
			var got []string
			for o, err := range b.List(ctx, "odd/") {
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, o.Key)
			}
			if !slices.Equal(got, []string{"odd/a b+c"}) {
				t.Errorf("listed %q", got)
			}
		}},
		{name: "list of a prefix with nothing under it yields nothing", run: func(t *testing.T) {
			for o, err := range b.List(ctx, "none/") {
				t.Errorf("listed %+v, %v", o, err)
			}
		}},
		{name: "delete removes the key", run: func(t *testing.T) {
			if err := b.Delete(ctx, "notes/one"); err != nil {
				t.Fatal(err)
			}
			if _, ok := srv.Object(mainBucket, "notes/one"); ok {
				t.Error("the key is still stored")
			}
		}},
		{name: "delete of a missing key succeeds, as in S3", run: func(t *testing.T) {
			if err := b.Delete(ctx, "notes/one"); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "get of a missing key fails with ErrNotFound", run: func(t *testing.T) {
			if _, _, err := b.Get(ctx, "notes/one"); !errors.Is(err, ErrNotFound) {
				t.Errorf("error = %v, want ErrNotFound", err)
			}
		}},
		{name: "head of a missing key fails with ErrNotFound", run: func(t *testing.T) {
			if _, err := b.Head(ctx, "notes/one"); !errors.Is(err, ErrNotFound) {
				t.Errorf("error = %v, want ErrNotFound", err)
			}
		}},
		{name: "a call on a missing bucket fails with ErrNoSuchBucket", run: func(t *testing.T) {
			_, _, err := bucket(t, c, "no-such-bucket").Get(ctx, "notes/one")
			if !errors.Is(err, ErrNoSuchBucket) {
				t.Errorf("error = %v, want ErrNoSuchBucket", err)
			}
		}},
		{name: "a key outside the narrow rule is refused before any request", run: func(t *testing.T) {
			before := len(srv.Requests())
			if _, err := b.Put(ctx, "../x", strings.NewReader("x"), PutOptions{}); err == nil {
				t.Error("put accepted ../x")
			}
			if after := len(srv.Requests()); after != before {
				t.Errorf("%d requests were sent", after-before)
			}
		}},
	}
	for _, s := range steps {
		t.Run(s.name, s.run)
	}
}

// hugeBody claims a size past MaxPutSize without holding the bytes.
type hugeBody struct{ off int64 }

func (h *hugeBody) Read(p []byte) (int, error) { return 0, io.EOF }
func (h *hugeBody) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekEnd:
		h.off = MaxPutSize + 1 + offset
	case io.SeekStart:
		h.off = offset
	}
	return h.off, nil
}

func TestPutTooLarge(t *testing.T) {
	tests := []struct {
		name string
		put  func(*objstoretest.Server, *Bucket) error
		// wantRequests is how many requests reach the store.
		wantRequests int
	}{
		{name: "a body over MaxPutSize, R2's cap for one put, fails with ErrTooLarge before it is read", put: func(_ *objstoretest.Server, b *Bucket) error {
			_, err := b.Put(context.Background(), "big", &hugeBody{}, PutOptions{})
			return err
		}},
		{name: "a sealed blob over MaxEvidenceSize fails with ErrTooLarge", put: func(_ *objstoretest.Server, b *Bucket) error {
			_, err := NewEvidence(b).Put(context.Background(), make([]byte, MaxEvidenceSize+1))
			return err
		}},
		{name: "a store's EntityTooLarge answer fails with ErrTooLarge and isn't retried", wantRequests: 1, put: func(srv *objstoretest.Server, b *Bucket) error {
			srv.Fail(1, http.StatusBadRequest, "EntityTooLarge")
			_, err := b.Put(context.Background(), "big", strings.NewReader("over a smaller store's cap"), PutOptions{})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := objstoretest.New(t, mainBucket)
			b := bucket(t, newTestClient(t, srv), mainBucket)
			if err := tt.put(srv, b); !errors.Is(err, ErrTooLarge) {
				t.Errorf("error = %v, want ErrTooLarge", err)
			}
			if n := len(srv.Requests()); n != tt.wantRequests {
				t.Errorf("%d requests were sent, want %d", n, tt.wantRequests)
			}
		})
	}
}

func TestEvidence(t *testing.T) {
	srv := objstoretest.New(t, mainBucket)
	e := NewEvidence(bucket(t, newTestClient(t, srv), mainBucket))
	ctx := context.Background()
	blob := []byte("sealed: not readable without the evidence key")
	sum := sha256.Sum256(blob)
	other := sha256.Sum256([]byte("never stored"))

	steps := []struct {
		name string
		run  func(t *testing.T)
	}{
		{name: "put stores the blob under evidence/ and its hex SHA-256", run: func(t *testing.T) {
			got, err := e.Put(ctx, blob)
			if err != nil {
				t.Fatal(err)
			}
			if got != sum {
				t.Errorf("Put returned %x, want %x", got, sum)
			}
			if keys := srv.Keys(mainBucket); !slices.Equal(keys, []string{"evidence/" + objstoretest.SHA256(blob)}) {
				t.Errorf("bucket keys = %q", keys)
			}
		}},
		{name: "a second put of the same blob is refused with ErrExists", run: func(t *testing.T) {
			got, err := e.Put(ctx, blob)
			if !errors.Is(err, ErrExists) {
				t.Fatalf("error = %v, want ErrExists", err)
			}
			if got != sum {
				t.Errorf("Put returned %x, want %x", got, sum)
			}
		}},
		{name: "get returns the blob after checking its hash", run: func(t *testing.T) {
			got, err := e.Get(ctx, sum)
			if err != nil || !bytes.Equal(got, blob) {
				t.Errorf("Get = %q, %v", got, err)
			}
		}},
		{name: "has finds a stored blob", run: func(t *testing.T) {
			if ok, err := e.Has(ctx, sum); !ok || err != nil {
				t.Errorf("Has = %v, %v", ok, err)
			}
		}},
		{name: "has doesn't find a blob never stored", run: func(t *testing.T) {
			if ok, err := e.Has(ctx, other); ok || err != nil {
				t.Errorf("Has = %v, %v", ok, err)
			}
		}},
		{name: "get of a blob never stored fails with ErrNotFound", run: func(t *testing.T) {
			if _, err := e.Get(ctx, other); !errors.Is(err, ErrNotFound) {
				t.Errorf("error = %v, want ErrNotFound", err)
			}
		}},
		{name: "get of a blob changed in the bucket fails with ErrDigestMismatch", run: func(t *testing.T) {
			srv.SetObject(mainBucket, EvidenceKey(sum), []byte("swapped behind pairtimed's back"))
			if got, err := e.Get(ctx, sum); !errors.Is(err, ErrDigestMismatch) || got != nil {
				t.Errorf("Get = %q, %v; want no bytes and ErrDigestMismatch", got, err)
			}
		}},
	}
	for _, s := range steps {
		t.Run(s.name, s.run)
	}
}

func TestEvidenceDelete(t *testing.T) {
	blob := []byte("sealed: deleted by the delete pass")
	sum := sha256.Sum256(blob)
	tests := []struct {
		name string
		// versioning is the bucket's state, as SetVersioning takes it.
		versioning string
		stored     bool
		wantErr    error
	}{
		{name: "delete removes a stored blob from a bucket that keeps no versions, and Has is false after", stored: true},
		{name: "deleting a blob that's already gone succeeds, so a retried delete passes"},
		{name: "a delete on a bucket with versioning on fails with ErrKeepsVersions, since the blob stays behind a delete marker",
			versioning: "Enabled", stored: true, wantErr: ErrKeepsVersions},
		{name: "a delete on a bucket with versioning suspended fails with ErrKeepsVersions too, since such a bucket still adds a marker",
			versioning: "Suspended", stored: true, wantErr: ErrKeepsVersions},
		{name: "a retried delete on a bucket with versioning on fails as well, since each delete adds another marker",
			versioning: "Enabled", wantErr: ErrKeepsVersions},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := objstoretest.New(t, mainBucket)
			srv.SetVersioning(mainBucket, tt.versioning)
			if tt.stored {
				srv.SetObject(mainBucket, EvidenceKey(sum), blob)
			}
			e := NewEvidence(bucket(t, newTestClient(t, srv), mainBucket))
			ctx := context.Background()
			err := e.Delete(ctx, sum)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Delete error = %v, want %v", err, tt.wantErr)
			}
			var oe *Error
			if err != nil && (!errors.As(err, &oe) || oe.Op != "DeleteObject" || oe.Key != EvidenceKey(sum)) {
				t.Errorf("Delete error = %#v, want an *Error naming DeleteObject and %s", err, EvidenceKey(sum))
			}
			if ok, err := e.Has(ctx, sum); ok || err != nil {
				t.Errorf("Has after Delete = %v, %v; want false", ok, err)
			}
		})
	}
}

func TestEvidenceList(t *testing.T) {
	sum := func(s string) string { return objstoretest.SHA256([]byte(s)) }
	tests := []struct {
		name    string
		objects []string
		want    []string
	}{
		{name: "an empty store lists nothing"},
		{name: "every blob under evidence/ is listed in key order, across pages, and nothing outside it",
			objects: []string{"evidence/" + sum("b"), "notes/one", "evidence/" + sum("a"), "evidence/" + sum("c"), "snapshots/x"},
			want:    slices.Sorted(slices.Values([]string{"evidence/" + sum("a"), "evidence/" + sum("b"), "evidence/" + sum("c")}))},
		{name: "an object under evidence/ whose key isn't an evidence key is listed too, so a count of unnamed blobs sees it",
			objects: []string{"evidence/" + sum("a"), "evidence/not-a-blob"},
			want:    slices.Sorted(slices.Values([]string{"evidence/" + sum("a"), "evidence/not-a-blob"}))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := objstoretest.New(t, mainBucket)
			for _, k := range tt.objects {
				srv.SetObject(mainBucket, k, []byte(k))
			}
			b := bucket(t, newTestClient(t, srv), mainBucket)
			b.pageSize = 2
			var got []string
			for o, err := range NewEvidence(b).List(context.Background()) {
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, o.Key)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("List = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseEvidenceKey(t *testing.T) {
	sum := sha256.Sum256([]byte("x"))
	tests := []struct {
		name, key string
		ok        bool
	}{
		{name: "EvidenceKey's output parses back to the sum", key: EvidenceKey(sum), ok: true},
		{name: "a key outside evidence/ is refused", key: "snapshots/" + objstoretest.SHA256([]byte("x"))},
		{name: "uppercase hex is refused, so each blob has one key", key: "evidence/" + strings.ToUpper(objstoretest.SHA256([]byte("x")))},
		{name: "a short hash is refused", key: "evidence/abcd"},
		{name: "a non-hex hash is refused", key: "evidence/" + strings.Repeat("g", 64)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseEvidenceKey(tt.key)
			if (err == nil) != tt.ok {
				t.Fatalf("ParseEvidenceKey(%q) error = %v, want ok = %v", tt.key, err, tt.ok)
			}
			if tt.ok && got != sum {
				t.Errorf("got %x, want %x", got, sum)
			}
		})
	}
}

func TestSnapshots(t *testing.T) {
	srv := objstoretest.New(t, snapshotBucket)
	b := bucket(t, newTestClient(t, srv), snapshotBucket)
	b.pageSize = 1
	s := NewSnapshots(b)
	ctx := context.Background()
	night1 := time.Date(2026, 11, 18, 3, 10, 0, 0, time.UTC)
	night2 := night1.Add(24 * time.Hour)

	steps := []struct {
		name string
		run  func(t *testing.T)
	}{
		{name: "latest of an empty bucket fails with ErrNotFound", run: func(t *testing.T) {
			if _, err := s.Latest(ctx); !errors.Is(err, ErrNotFound) {
				t.Errorf("error = %v, want ErrNotFound", err)
			}
		}},
		{name: "put stores a snapshot under the UTC time it was taken", run: func(t *testing.T) {
			local := night1.In(time.FixedZone("UTC-5", -5*3600))
			info, err := s.Put(ctx, local, strings.NewReader("age-encryption.org/v1 night 1"))
			if err != nil {
				t.Fatal(err)
			}
			if want := "snapshots/pairtime-20261118T031000Z.db.age"; info.Key != want {
				t.Errorf("key = %q, want %q", info.Key, want)
			}
		}},
		{name: "a second snapshot with the same time is refused and the first kept", run: func(t *testing.T) {
			if _, err := s.Put(ctx, night1, strings.NewReader("overwrite")); !errors.Is(err, ErrExists) {
				t.Fatalf("error = %v, want ErrExists", err)
			}
			if got, _ := srv.Object(snapshotBucket, SnapshotKey(night1)); string(got) != "age-encryption.org/v1 night 1" {
				t.Errorf("stored %q", got)
			}
		}},
		{name: "latest returns the newest snapshot and skips keys it didn't make", run: func(t *testing.T) {
			if _, err := s.Put(ctx, night2, strings.NewReader("night 2")); err != nil {
				t.Fatal(err)
			}
			srv.SetObject(snapshotBucket, "snapshots/zz-not-a-snapshot", []byte("x"))
			got, err := s.Latest(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if got.Key != SnapshotKey(night2) || got.Size != int64(len("night 2")) {
				t.Errorf("latest = %+v, want %s", got, SnapshotKey(night2))
			}
		}},
		{name: "get returns a snapshot's bytes", run: func(t *testing.T) {
			body, _, err := s.Get(ctx, SnapshotKey(night2))
			if err != nil {
				t.Fatal(err)
			}
			defer body.Close()
			if got, _ := io.ReadAll(body); string(got) != "night 2" {
				t.Errorf("got %q", got)
			}
		}},
	}
	for _, st := range steps {
		t.Run(st.name, st.run)
	}
}

// mustPut stores data under key with a plain put.
func mustPut(t *testing.T, b *Bucket, key, data string) {
	t.Helper()
	if _, err := b.Put(context.Background(), key, strings.NewReader(data), PutOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestRetries(t *testing.T) {
	const key = "retry/k"
	writeOnce := PutOptions{IfNoneMatch: true}
	tests := []struct {
		name string
		// setup prepares the fresh server before the faults; its requests
		// aren't counted.
		setup func(*testing.T, *objstoretest.Server, *Bucket)
		// fail injects the faults.
		fail func(*objstoretest.Server)
		// opts are the options of the call, a put of "body" under key.
		opts PutOptions
		// call replaces the put.
		call func(*Bucket) error
		// wantMethods are the requests the call sends, in order.
		wantMethods []string
		wantErr     bool
		// wantIs is a sentinel the error must wrap.
		wantIs error
		// wantStored is what the key holds after the call; "" skips the
		// check.
		wantStored string
	}{
		{name: "a 503 SlowDown is retried and the put then succeeds", fail: func(s *objstoretest.Server) { s.Fail(1, 503, "SlowDown") },
			wantMethods: []string{"PUT", "PUT"}, wantStored: "body"},
		{name: "a dropped connection is retried", fail: func(s *objstoretest.Server) { s.Fail(1, 0, "") },
			wantMethods: []string{"PUT", "PUT"}, wantStored: "body"},
		{name: "a 500 is retried up to three attempts, then fails", fail: func(s *objstoretest.Server) { s.Fail(3, 500, "InternalError") },
			wantMethods: []string{"PUT", "PUT", "PUT"}, wantErr: true},
		{name: "a 403 isn't retried", fail: func(s *objstoretest.Server) { s.Fail(1, 403, "AccessDenied") },
			wantMethods: []string{"PUT"}, wantErr: true},
		{name: "a 400 isn't retried", fail: func(s *objstoretest.Server) { s.Fail(1, 400, "InvalidArgument") },
			wantMethods: []string{"PUT"}, wantErr: true},
		{name: "the readiness check is tried once", fail: func(s *objstoretest.Server) { s.Fail(1, 503, "SlowDown") },
			call:        func(b *Bucket) error { return b.Check(context.Background()) },
			wantMethods: []string{"GET"}, wantErr: true},
		{name: "a write-once put whose stored first attempt lost its answer succeeds once a HEAD finds its own body",
			fail: func(s *objstoretest.Server) { s.FailAfter(1, 0, "") }, opts: writeOnce,
			wantMethods: []string{"PUT", "PUT", "HEAD"}, wantStored: "body"},
		{name: "a write-once put whose stored first attempt was answered with a 500 succeeds once a HEAD finds its own body",
			fail: func(s *objstoretest.Server) { s.FailAfter(1, 500, "InternalError") }, opts: writeOnce,
			wantMethods: []string{"PUT", "PUT", "HEAD"}, wantStored: "body"},
		{name: "a retried write-once put that finds other bytes of the same size fails with ErrExists",
			setup: func(t *testing.T, _ *objstoretest.Server, b *Bucket) { mustPut(t, b, key, "BODY") },
			fail:  func(s *objstoretest.Server) { s.Fail(1, 503, "SlowDown") }, opts: writeOnce,
			wantMethods: []string{"PUT", "PUT", "HEAD"}, wantErr: true, wantIs: ErrExists, wantStored: "BODY"},
		{name: "a retried write-once put fails with ErrExists when the stored object has no SHA-256 to compare",
			setup: func(_ *testing.T, s *objstoretest.Server, _ *Bucket) { s.SetObject(mainBucket, key, []byte("body")) },
			fail:  func(s *objstoretest.Server) { s.Fail(1, 503, "SlowDown") }, opts: writeOnce,
			wantMethods: []string{"PUT", "PUT", "HEAD"}, wantErr: true, wantIs: ErrExists, wantStored: "body"},
		{name: "a write-once put refused on its first attempt fails with ErrExists and sends no HEAD",
			setup: func(t *testing.T, _ *objstoretest.Server, b *Bucket) { mustPut(t, b, key, "body") }, opts: writeOnce,
			wantMethods: []string{"PUT"}, wantErr: true, wantIs: ErrExists, wantStored: "body"},
		{name: "a retried write-once put whose HEAD fails returns that error, not ErrExists",
			fail: func(s *objstoretest.Server) {
				// The first attempt is stored and its answer lost, the
				// retry is refused, and the HEAD is denied.
				s.FailAfter(1, 0, "")
				s.Fail(1, 412, "PreconditionFailed")
				s.Fail(1, 403, "AccessDenied")
			},
			opts: writeOnce, wantMethods: []string{"PUT", "PUT", "HEAD"}, wantErr: true, wantStored: "body"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := objstoretest.New(t, mainBucket)
			b := bucket(t, newTestClient(t, srv), mainBucket)
			if tt.setup != nil {
				tt.setup(t, srv, b)
			}
			before := len(srv.Requests())
			if tt.fail != nil {
				tt.fail(srv)
			}
			call := tt.call
			if call == nil {
				call = func(b *Bucket) error {
					_, err := b.Put(context.Background(), key, strings.NewReader("body"), tt.opts)
					return err
				}
			}
			err := call(b)
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, want an error: %v", err, tt.wantErr)
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Errorf("error = %v, want %v", err, tt.wantIs)
			}
			if tt.wantIs == nil && errors.Is(err, ErrExists) {
				// ErrExists says the key held something else, which none
				// of these cases has shown.
				t.Errorf("error = %v, which wraps ErrExists", err)
			}
			var methods []string
			for _, r := range srv.Requests()[before:] {
				methods = append(methods, r.Method)
				if r.Method == http.MethodPut && tt.opts.IfNoneMatch && r.Header.Get("If-None-Match") != "*" {
					t.Error("a write-once attempt went without If-None-Match: *")
				}
			}
			if !slices.Equal(methods, tt.wantMethods) {
				t.Errorf("requests %q, want %q", methods, tt.wantMethods)
			}
			if tt.wantStored != "" {
				if got, _ := srv.Object(mainBucket, key); string(got) != tt.wantStored {
					t.Errorf("the key holds %q, want %q", got, tt.wantStored)
				}
			}
		})
	}
}

func TestCheck(t *testing.T) {
	srv := objstoretest.New(t, mainBucket)
	closed := objstoretest.New(t)
	closed.Close()
	tests := []struct {
		name    string
		client  func(t *testing.T) *Client
		bucket  string
		wantErr error
		// wantText is a substring the error must have.
		wantText string
	}{
		{name: "an existing bucket with the right credentials passes", client: func(t *testing.T) *Client { return newTestClient(t, srv) }, bucket: mainBucket},
		{name: "a missing bucket fails with ErrNoSuchBucket", client: func(t *testing.T) *Client { return newTestClient(t, srv) }, bucket: "pairtime-missing", wantErr: ErrNoSuchBucket},
		{name: "a wrong secret fails with the store's code", bucket: mainBucket, wantText: "403 SignatureDoesNotMatch",
			client: func(t *testing.T) *Client {
				return newTestClient(t, srv, func(c *Config) { c.Credentials.SecretAccessKey = "wrong-secret-value" })
			}},
		{name: "a wrong region fails", bucket: mainBucket, wantText: "403",
			client: func(t *testing.T) *Client { return newTestClient(t, srv, func(c *Config) { c.Region = "us-east-1" }) }},
		{name: "an endpoint nobody answers fails", bucket: mainBucket, wantText: "GetObject",
			client: func(t *testing.T) *Client {
				return newTestClient(t, srv, func(c *Config) { c.Endpoint = closed.URL })
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err := bucket(t, tt.client(t), tt.bucket).Check(ctx)
			ok := tt.wantErr == nil && tt.wantText == ""
			if (err == nil) != ok {
				t.Fatalf("Check error = %v, want ok = %v", err, ok)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("Check error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantText != "" && !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("Check error = %q, want it to contain %q", err, tt.wantText)
			}
		})
	}
}

// TestErrorsCarryNoSecrets reads the error of a request the store refused
// for a bad signature. The test server's refusal repeats the access key
// id, the signature it got and the string to sign, as S3's does; none of
// them, nor the secret, may reach the error.
func TestErrorsCarryNoSecrets(t *testing.T) {
	srv := objstoretest.New(t, mainBucket)
	const secret = "wrong-secret-value-0123456789"
	c := newTestClient(t, srv, func(c *Config) { c.Credentials.SecretAccessKey = secret })
	_, err := bucket(t, c, mainBucket).Put(context.Background(), "k", strings.NewReader("x"), PutOptions{})
	if err == nil {
		t.Fatal("a put with the wrong secret succeeded")
	}
	reqs := srv.Requests()
	auth := reqs[len(reqs)-1].Header.Get("Authorization")
	_, signature, _ := strings.Cut(auth, "Signature=")
	tests := []struct{ name, secret string }{
		{name: "the secret isn't in the error", secret: secret},
		{name: "the signature isn't in the error", secret: signature},
		{name: "the access key id isn't in the error", secret: objstoretest.AccessKeyID},
		{name: "the string to sign isn't in the error", secret: sigv4.Algorithm},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.secret == "" {
				t.Fatal("nothing to look for")
			}
			if strings.Contains(err.Error(), tt.secret) {
				t.Errorf("error %q contains %q", err, tt.secret)
			}
		})
	}
}

// TestClientNeverPrintsSecrets formats a client every way a caller or a
// log line might. sigv4's TestCredentialsNeverPrintSecrets covers bare
// Credentials; this covers the Client that holds them, where fmt can't
// call Credentials.String through the unexported signer field.
func TestClientNeverPrintsSecrets(t *testing.T) {
	creds := sigv4.Credentials{AccessKeyID: "AKIDPRINTCHECK", SecretAccessKey: "print-check-secret-value", SessionToken: "print-check-session-token"}
	c, err := New(Config{Endpoint: "https://s3.example", Credentials: creds})
	if err != nil {
		t.Fatal(err)
	}
	b := bucket(t, c, mainBucket)
	logged := func(handler func(io.Writer) slog.Handler) string {
		var buf bytes.Buffer
		slog.New(handler(&buf)).Info("object storage", "client", c)
		return buf.String()
	}
	const described = "objstore.Client{endpoint https://s3.example, region auto}"
	outputs := []struct {
		name, text string
		// names is text the output must hold, to show String ran.
		names string
	}{
		{name: "%v of a *Client prints no credential", text: fmt.Sprintf("%v", c), names: described},
		{name: "%+v of a *Client prints no credential", text: fmt.Sprintf("%+v", c), names: described},
		{name: "%#v of a *Client prints no credential", text: fmt.Sprintf("%#v", c), names: described},
		{name: "%s of a *Client prints no credential", text: fmt.Sprintf("%s", c), names: described},
		{name: "%+v of a Client value, which skips String, prints no credential", text: fmt.Sprintf("%+v", *c)},
		{name: "%#v of a Client value, which skips GoString, prints no credential", text: fmt.Sprintf("%#v", *c)},
		{name: "%+v of a *Bucket prints no credential", text: fmt.Sprintf("%+v", b)},
		{name: "slog's text handler prints no credential", names: described,
			text: logged(func(w io.Writer) slog.Handler { return slog.NewTextHandler(w, nil) })},
		{name: "slog's JSON handler prints no credential", names: described,
			text: logged(func(w io.Writer) slog.Handler { return slog.NewJSONHandler(w, nil) })},
	}
	secrets := []struct{ name, value string }{
		{name: "access key id", value: creds.AccessKeyID},
		{name: "secret access key", value: creds.SecretAccessKey},
		{name: "session token", value: creds.SessionToken},
	}
	for _, o := range outputs {
		t.Run(o.name, func(t *testing.T) {
			// The messages say what leaked without printing it.
			for _, s := range secrets {
				if strings.Contains(o.text, s.value) {
					t.Errorf("the output holds the %s", s.name)
				}
			}
			if o.names != "" && !strings.Contains(o.text, o.names) {
				t.Errorf("the output doesn't hold %q, so String didn't run", o.names)
			}
		})
	}
}

// TestVirtualHost sends every call over https to <bucket>.example.com,
// which a dialer routes to the test server, and checks the server saw the
// bucket in the host and only the key in the path. The server's
// certificate covers *.example.com and is the only root the client
// trusts, so TLS checks the virtual-hosted name, as it would on R2.
func TestVirtualHost(t *testing.T) {
	srv := objstoretest.NewTLS(t, mainBucket)
	addr := srv.Listener.Addr().String()
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}
	t.Cleanup(transport.CloseIdleConnections)
	c := newTestClient(t, srv, func(c *Config) {
		c.Endpoint = "https://example.com"
		c.VirtualHost = true
		c.HTTPClient = &http.Client{Transport: transport}
	})
	b := bucket(t, c, mainBucket)
	ctx := context.Background()
	if _, err := b.Put(ctx, "vh/key", strings.NewReader("v"), PutOptions{}); err != nil {
		t.Fatal(err)
	}
	var listed []string
	for o, err := range b.List(ctx, "vh/") {
		if err != nil {
			t.Fatal(err)
		}
		listed = append(listed, o.Key)
	}
	reqs := srv.Requests()
	tests := []struct {
		name      string
		got, want string
	}{
		{name: "the put went to the bucket's host", got: reqs[0].Host, want: mainBucket + ".example.com"},
		{name: "the put's path is the key alone", got: reqs[0].Path, want: "/vh/key"},
		{name: "the listing's path is the root", got: reqs[1].Path, want: "/"},
		{name: "the listing found the key", got: strings.Join(listed, ","), want: "vh/key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}
