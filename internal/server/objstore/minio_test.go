// SPDX-License-Identifier: AGPL-3.0-or-later

package objstore

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/objstore/sigv4"
)

// The variables that point TestMinIO at an S3 server. objstore.yml sets
// them and starts MinIO as a service container.
const (
	envEndpoint       = "PAIRTIME_TEST_S3_ENDPOINT"
	envRegion         = "PAIRTIME_TEST_S3_REGION"
	envAccessKeyID    = "PAIRTIME_TEST_S3_ACCESS_KEY_ID"
	envSecretKey      = "PAIRTIME_TEST_S3_SECRET_ACCESS_KEY"
	envBucket         = "PAIRTIME_TEST_S3_BUCKET"
	envSnapshotBucket = "PAIRTIME_TEST_S3_SNAPSHOT_BUCKET"
)

// TestMinIO runs the client against MinIO, an S3 implementation this
// repository didn't write: PLAN WP-SV15's oracle, next to AWS's signing
// test suite. Without PAIRTIME_TEST_S3_ENDPOINT it skips, so ci.yml's
// go test ./... passes over it; objstore.yml runs it and fails unless it
// ran and passed. Keys are under a random prefix and blobs are random, so
// a rerun against the same server starts clean.
func TestMinIO(t *testing.T) {
	endpoint := os.Getenv(envEndpoint)
	if endpoint == "" {
		t.Skip(envEndpoint + " is unset; objstore.yml sets it and starts MinIO")
	}
	cfg := Config{
		Endpoint:    endpoint,
		Region:      os.Getenv(envRegion),
		Credentials: sigv4.Credentials{AccessKeyID: os.Getenv(envAccessKeyID), SecretAccessKey: os.Getenv(envSecretKey)},
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	main, snaps := bucket(t, c, os.Getenv(envBucket)), bucket(t, c, os.Getenv(envSnapshotBucket))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	for _, b := range []*Bucket{main, snaps} {
		ensureBucket(t, ctx, b)
	}

	run := "minio-test-" + randomHex(t, 6) + "/"
	blob := []byte("sealed blob " + randomHex(t, 16))
	night := time.Now().UTC().Truncate(time.Second)

	steps := []struct {
		name string
		run  func(t *testing.T)
	}{
		{name: "put then get returns the same bytes", run: func(t *testing.T) {
			if _, err := main.Put(ctx, run+"hello", strings.NewReader("hello, minio"), PutOptions{ContentType: "text/plain"}); err != nil {
				t.Fatal(err)
			}
			body, info, err := main.Get(ctx, run+"hello")
			if err != nil {
				t.Fatal(err)
			}
			defer body.Close()
			if got, _ := io.ReadAll(body); string(got) != "hello, minio" || info.ContentType != "text/plain" {
				t.Errorf("got %q, %+v", got, info)
			}
		}},
		{name: "head reports the size, the ETag and the SHA-256 put stored", run: func(t *testing.T) {
			info, err := main.Head(ctx, run+"hello")
			if err != nil {
				t.Fatal(err)
			}
			if info.Size != int64(len("hello, minio")) || info.ETag == "" || info.LastModified.IsZero() || info.SHA256 != sigv4.PayloadHash([]byte("hello, minio")) {
				t.Errorf("head = %+v", info)
			}
		}},
		{name: "a key of every allowed character round-trips", run: func(t *testing.T) {
			key := run + "AZaz09-_./x.y_z-0"
			if _, err := main.Put(ctx, key, strings.NewReader("k"), PutOptions{}); err != nil {
				t.Fatal(err)
			}
			if _, err := main.Head(ctx, key); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "list pages through every key under a prefix in order", run: func(t *testing.T) {
			want := []string{run + "list/a", run + "list/b", run + "list/c", run + "list/d", run + "list/e"}
			for _, k := range slices.Backward(want) {
				if _, err := main.Put(ctx, k, strings.NewReader(k), PutOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			paged := *main
			paged.pageSize = 2
			var got []string
			for o, err := range paged.List(ctx, run+"list/") {
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, o.Key)
			}
			if !slices.Equal(got, want) {
				t.Errorf("listed %q, want %q", got, want)
			}
		}},
		{name: "a write-once put stores a new key", run: func(t *testing.T) {
			if _, err := main.Put(ctx, run+"once", strings.NewReader("first"), PutOptions{IfNoneMatch: true}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "a write-once put to an existing key is refused and the first bytes kept", run: func(t *testing.T) {
			if _, err := main.Put(ctx, run+"once", strings.NewReader("second"), PutOptions{IfNoneMatch: true}); !errors.Is(err, ErrExists) {
				t.Fatalf("error = %v, want ErrExists", err)
			}
			body, _, err := main.Get(ctx, run+"once")
			if err != nil {
				t.Fatal(err)
			}
			defer body.Close()
			if got, _ := io.ReadAll(body); string(got) != "first" {
				t.Errorf("stored %q, want first", got)
			}
		}},
		{name: "the HEAD after a retried write-once put tells its own body from other bytes of the same size", run: func(t *testing.T) {
			for _, body := range []struct {
				text string
				ours bool
			}{{text: "first", ours: true}, {text: "other", ours: false}} {
				_, ours, err := main.holds(ctx, run+"once", int64(len(body.text)), sigv4.PayloadHash([]byte(body.text)))
				if err != nil || ours != body.ours {
					t.Errorf("holds(%q) = %v, %v; want %v", body.text, ours, err, body.ours)
				}
			}
		}},
		{name: "an evidence put stores the blob under its SHA-256", run: func(t *testing.T) {
			sum, err := NewEvidence(main).Put(ctx, blob)
			if err != nil {
				t.Fatal(err)
			}
			if sum != sha256.Sum256(blob) {
				t.Errorf("sum = %x", sum)
			}
		}},
		{name: "a second put to the same evidence key is refused", run: func(t *testing.T) {
			if _, err := NewEvidence(main).Put(ctx, blob); !errors.Is(err, ErrExists) {
				t.Fatalf("error = %v, want ErrExists", err)
			}
		}},
		{name: "an evidence get returns the blob after checking its hash", run: func(t *testing.T) {
			got, err := NewEvidence(main).Get(ctx, sha256.Sum256(blob))
			if err != nil || !bytes.Equal(got, blob) {
				t.Errorf("got %q, %v", got, err)
			}
		}},
		{name: "the newest snapshot is found in the snapshot bucket", run: func(t *testing.T) {
			s := NewSnapshots(snaps)
			for _, at := range []time.Time{night.Add(-24 * time.Hour), night} {
				if _, err := s.Put(ctx, at, strings.NewReader("snapshot "+at.Format(time.RFC3339))); err != nil {
					t.Fatal(err)
				}
			}
			latest, err := s.Latest(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if latest.Key != SnapshotKey(night) {
				t.Errorf("latest = %q, want %q", latest.Key, SnapshotKey(night))
			}
		}},
		{name: "a snapshot of several parts reads back byte for byte", run: func(t *testing.T) {
			// The threshold lowered to MinIO's smallest part, so 12 MiB
			// takes three parts (PLAN WP-OP1).
			data := randomBytes(2*MinPartSize + 2<<20)
			key := run + "multipart/whole.db.age"
			if _, err := snaps.PutStream(ctx, key, bytes.NewReader(data), StreamOptions{PartSize: MinPartSize, IfNoneMatch: true}); err != nil {
				t.Fatal(err)
			}
			body, info, err := snaps.Get(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			defer body.Close()
			got, err := io.ReadAll(body)
			if err != nil || !bytes.Equal(got, data) {
				t.Errorf("read back %d bytes (%v), want the %d sent", len(got), err, len(data))
			}
			if !strings.HasSuffix(info.ETag, "-3") {
				t.Errorf("ETag %q isn't a three-part upload's", info.ETag)
			}
			if left := openUploads(t, ctx, snaps, key); len(left) > 0 {
				t.Errorf("uploads left open: %q", left)
			}
		}},
		{name: "a write-once multipart upload to an existing key is refused and aborted", run: func(t *testing.T) {
			key := run + "multipart/whole.db.age"
			_, err := snaps.PutStream(ctx, key, bytes.NewReader(randomBytes(MinPartSize+1)), StreamOptions{PartSize: MinPartSize, IfNoneMatch: true})
			if !errors.Is(err, ErrExists) {
				t.Fatalf("error = %v, want ErrExists", err)
			}
			if left := openUploads(t, ctx, snaps, key); len(left) > 0 {
				t.Errorf("uploads left open: %q", left)
			}
		}},
		{name: "a failed part aborts the upload, so MinIO keeps no parts", run: func(t *testing.T) {
			key := run + "multipart/broken.db.age"
			broken := errors.New("the encrypting pipe broke")
			r := &hookReader{data: randomBytes(3 * MinPartSize), at: MinPartSize + 10, hook: func() {}, err: broken}
			if _, err := snaps.PutStream(ctx, key, r, StreamOptions{PartSize: MinPartSize}); !errors.Is(err, broken) {
				t.Fatalf("error = %v, want the reader's", err)
			}
			if left := openUploads(t, ctx, snaps, key); len(left) > 0 {
				t.Errorf("uploads left open: %q", left)
			}
			if _, err := snaps.Head(ctx, key); !errors.Is(err, ErrNotFound) {
				t.Errorf("head of the aborted key: %v, want ErrNotFound", err)
			}
		}},
		{name: "a bucket nobody turned versioning on for reports no versioning state", run: func(t *testing.T) {
			for _, b := range []*Bucket{main, snaps} {
				if state, err := b.Versioning(ctx); err != nil || state != "" {
					t.Errorf("%s: versioning %q, %v; want none", b.Name(), state, err)
				}
			}
		}},
		{name: "a scratch bucket the test turns versioning on for reports Enabled, and Suspended once the test suspends it", run: func(t *testing.T) {
			// MinIO's answers, not ones pairtime wrote, are what doctor's
			// versioning check reads (D-evidence-versioning).
			b := scratchBucket(t, ctx, c, "pairtime-versioning-")
			for _, state := range []string{VersioningEnabled, VersioningSuspended} {
				putBucketConfig(t, ctx, b, "PutBucketVersioning", "versioning",
					`<VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Status>`+state+`</Status></VersioningConfiguration>`)
				if got, err := b.Versioning(ctx); err != nil || got != state {
					t.Errorf("after PutBucketVersioning %s: versioning %q, %v", state, got, err)
				}
			}
		}},
		{name: "a bucket nobody gave a lifecycle configuration reports no rules", run: func(t *testing.T) {
			for _, b := range []*Bucket{main, snaps} {
				if rules, err := b.Lifecycle(ctx); err != nil || len(rules) != 0 {
					t.Errorf("%s: rules %+v, %v; want none", b.Name(), rules, err)
				}
			}
		}},
		{name: "the lifecycle rules MinIO keeps for a bucket read back as they were put", run: func(t *testing.T) {
			b := scratchBucket(t, ctx, c, "pairtime-lifecycle-")
			putBucketConfig(t, ctx, b, "PutBucketLifecycleConfiguration", "lifecycle",
				`<LifecycleConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`+
					`<Rule><ID>expire snapshots</ID><Filter><Prefix>snapshots/</Prefix></Filter><Status>Enabled</Status><Expiration><Days>30</Days></Expiration></Rule>`+
					`<Rule><ID>tagged scratch</ID><Filter><And><Prefix>tmp/</Prefix><Tag><Key>kind</Key><Value>scratch</Value></Tag></And></Filter><Status>Disabled</Status><Expiration><Days>1</Days></Expiration></Rule>`+
					`</LifecycleConfiguration>`)
			rules, err := b.Lifecycle(ctx)
			if err != nil {
				t.Fatal(err)
			}
			want := []LifecycleRule{
				{ID: "expire snapshots", Enabled: true, Prefix: "snapshots/", ExpireDays: 30},
				{ID: "tagged scratch", Prefix: "tmp/", Narrowed: true, ExpireDays: 1},
			}
			if !reflect.DeepEqual(rules, want) {
				t.Fatalf("rules = %+v, want %+v", rules, want)
			}
			if !rules[0].Expires() || !rules[0].Covers(SnapshotPrefix) || rules[1].Expires() {
				t.Errorf("rules = %+v: the first should delete every snapshot, and the second nothing", rules)
			}
		}},
		{name: "delete removes a key", run: func(t *testing.T) {
			if err := main.Delete(ctx, run+"hello"); err != nil {
				t.Fatal(err)
			}
			if _, err := main.Head(ctx, run+"hello"); !errors.Is(err, ErrNotFound) {
				t.Errorf("head after delete: %v, want ErrNotFound", err)
			}
		}},
		{name: "get of a missing key is ErrNotFound", run: func(t *testing.T) {
			if _, _, err := main.Get(ctx, run+"never"); !errors.Is(err, ErrNotFound) {
				t.Errorf("error = %v, want ErrNotFound", err)
			}
		}},
		{name: "the readiness check passes on both buckets", run: func(t *testing.T) {
			for _, b := range []*Bucket{main, snaps} {
				if err := b.Check(ctx); err != nil {
					t.Errorf("%s: %v", b.Name(), err)
				}
			}
		}},
		{name: "the readiness check fails on a missing bucket", run: func(t *testing.T) {
			if err := bucket(t, c, "pairtime-missing-"+randomHex(t, 4)).Check(ctx); !errors.Is(err, ErrNoSuchBucket) {
				t.Errorf("error = %v, want ErrNoSuchBucket", err)
			}
		}},
		{name: "the readiness check fails on a wrong secret without printing it", run: func(t *testing.T) {
			bad := cfg
			bad.Credentials.SecretAccessKey = "wrong-" + randomHex(t, 8)
			bc, err := New(bad)
			if err != nil {
				t.Fatal(err)
			}
			err = bucket(t, bc, main.Name()).Check(ctx)
			var e *Error
			if !errors.As(err, &e) || e.Status != http.StatusForbidden {
				t.Fatalf("error = %v, want a 403", err)
			}
			if strings.Contains(err.Error(), bad.Credentials.SecretAccessKey) || strings.Contains(err.Error(), cfg.Credentials.AccessKeyID) {
				t.Errorf("error %q prints a credential", err)
			}
		}},
	}
	for _, s := range steps {
		t.Run(s.name, s.run)
	}
}

// ensureBucket creates b on the server unless it exists, retrying while
// MinIO finishes starting, and waits until Check passes.
func ensureBucket(t *testing.T, ctx context.Context, b *Bucket) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	var err error
	for time.Now().Before(deadline) {
		var resp *http.Response
		resp, err = b.c.do(ctx, request{op: "CreateBucket", method: http.MethodPut, bucket: b.name})
		var e *Error
		if err == nil {
			drain(resp)
		}
		if err == nil || errors.As(err, &e) && e.Code == "BucketAlreadyOwnedByYou" {
			if err = b.Check(ctx); err == nil {
				return
			}
		}
		if errors.As(err, &e) && e.Status == http.StatusForbidden {
			t.Fatalf("bucket %s: %v", b.name, err)
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("bucket %s isn't ready after 90 s: %v", b.name, err)
}

// scratchBucket makes a bucket of its own for a step that changes a
// bucket's configuration, which the steps that share main and snaps
// mustn't see, and deletes it when the test ends.
func scratchBucket(t *testing.T, ctx context.Context, c *Client, prefix string) *Bucket {
	t.Helper()
	b := bucket(t, c, prefix+randomHex(t, 4))
	ensureBucket(t, ctx, b)
	t.Cleanup(func() {
		resp, err := b.c.do(context.Background(), request{op: "DeleteBucket", method: http.MethodDelete, bucket: b.name})
		if err != nil {
			t.Logf("deleting the scratch bucket %s: %v", b.name, err)
			return
		}
		drain(resp)
	})
	return b
}

// putBucketConfig sends one of the calls that set a bucket's
// configuration, which pairtimed itself never makes, with body as its
// XML. Content-MD5 goes with it, since MinIO wants one on
// PutBucketLifecycleConfiguration.
func putBucketConfig(t *testing.T, ctx context.Context, b *Bucket, op, query, body string) {
	t.Helper()
	sum := md5.Sum([]byte(body))
	resp, err := b.c.do(ctx, request{
		op: op, method: http.MethodPut, bucket: b.name, rawQuery: query,
		header: http.Header{"Content-Md5": {base64.StdEncoding.EncodeToString(sum[:])}, "Content-Type": {"application/xml"}},
		body:   strings.NewReader(body), size: int64(len(body)), payloadHash: sigv4.PayloadHash([]byte(body)),
	})
	if err != nil {
		t.Fatalf("%s: %v", op, err)
	}
	drain(resp)
}

// openUploads lists the multipart uploads of key that are still open,
// with ListMultipartUploads, which pairtimed itself never needs.
func openUploads(t *testing.T, ctx context.Context, b *Bucket, key string) []string {
	t.Helper()
	resp, err := b.c.do(ctx, request{op: "ListMultipartUploads", method: http.MethodGet, bucket: b.name,
		rawQuery: "prefix=" + sigv4.URIEncode(key, true) + "&uploads"})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var res struct {
		Uploads []struct {
			Key      string `xml:"Key"`
			UploadID string `xml:"UploadId"`
		} `xml:"Upload"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	var open []string
	for _, u := range res.Uploads {
		if u.Key == key {
			open = append(open, u.UploadID)
		}
	}
	return open
}

func randomHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}
