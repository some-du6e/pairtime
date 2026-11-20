// SPDX-License-Identifier: AGPL-3.0-or-later

package objstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"slices"
	"testing"

	"github.com/some-du6e/pairtime/internal/server/objstore/objstoretest"
)

// hookReader yields data and calls hook once, just before it yields the
// byte at offset at.
type hookReader struct {
	data []byte
	off  int
	at   int
	hook func()
	err  error
}

func (h *hookReader) Read(p []byte) (int, error) {
	if h.off >= len(h.data) {
		return 0, io.EOF
	}
	if h.hook != nil && h.off+len(p) > h.at {
		if h.off < h.at {
			p = p[:h.at-h.off]
		} else {
			hook := h.hook
			h.hook = nil
			hook()
			if h.err != nil {
				return 0, h.err
			}
		}
	}
	n := copy(p, h.data[h.off:])
	h.off += n
	return n, nil
}

// randomBytes is n bytes from a fixed seed.
func randomBytes(n int) []byte {
	r := rand.New(rand.NewPCG(1, 2))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	return b
}

// isComplete matches CompleteMultipartUpload.
func isComplete(r *http.Request) bool {
	return r.Method == http.MethodPost && r.URL.Query().Has("uploadId")
}

// call names a request by method and S3 operation, for comparing request
// sequences.
func call(r objstoretest.Request) string {
	switch {
	case r.Method == http.MethodPost && bytes.Contains([]byte(r.RawQuery), []byte("uploads")):
		return "create"
	case r.Method == http.MethodPut && bytes.Contains([]byte(r.RawQuery), []byte("partNumber")):
		return "part"
	case r.Method == http.MethodPost:
		return "complete"
	case r.Method == http.MethodDelete && bytes.Contains([]byte(r.RawQuery), []byte("uploadId")):
		return "abort"
	case r.Method == http.MethodPut:
		return "put"
	default:
		return r.Method
	}
}

func TestPutStream(t *testing.T) {
	const key = "snapshots/pairtime-20261120T031000Z.db.age"
	part := MinPartSize
	errBroken := errors.New("the encrypting pipe broke")
	tests := []struct {
		name string
		size int
		opts StreamOptions
		// setup runs on the fresh server before the call.
		setup func(*testing.T, *objstoretest.Server, *Bucket)
		// at and hook run hook just before the reader yields byte at; err
		// is what the reader returns then, if anything.
		at     int
		hook   func(srv *objstoretest.Server, cancel context.CancelFunc)
		err    error
		wantIs error
		// wantCalls are the requests the call sends, in order.
		wantCalls []string
		// wantStored is whether the key holds the body afterwards.
		wantStored bool
		// wantSHA256 is whether the stored object carries its SHA-256.
		wantSHA256 bool
	}{
		{name: "a body within one part is one PutObject that stores its SHA-256",
			size: part - 1, wantCalls: []string{"put"}, wantStored: true, wantSHA256: true},
		{name: "a body of exactly one part is still one PutObject",
			size: part, wantCalls: []string{"put"}, wantStored: true, wantSHA256: true},
		{name: "an empty body is one PutObject",
			size: 0, wantCalls: []string{"put"}, wantStored: true, wantSHA256: true},
		{name: "a body of several parts is a multipart upload that reads back byte for byte",
			size: 2*part + part/2, wantCalls: []string{"create", "part", "part", "part", "complete"}, wantStored: true},
		{name: "a body that ends on a part boundary sends no empty last part",
			size: 2 * part, wantCalls: []string{"create", "part", "part", "complete"}, wantStored: true},
		{name: "a write-once multipart upload sends If-None-Match with the completion and succeeds on a new key",
			size: part + 1, opts: StreamOptions{IfNoneMatch: true}, wantCalls: []string{"create", "part", "part", "complete"}, wantStored: true},
		{name: "a failed part aborts the upload, so no parts stay behind",
			size: 2*part + 1, at: part + 1,
			hook: func(srv *objstoretest.Server, _ context.CancelFunc) { srv.Fail(3, 500, "InternalError") },
			// The first part goes out before the reader reaches the second.
			wantCalls: []string{"create", "part", "part", "part", "part", "abort"}},
		{name: "a write-once upload to a key that exists fails with ErrExists, aborts, and leaves the key alone",
			size: part + 1, opts: StreamOptions{IfNoneMatch: true},
			setup:     func(t *testing.T, _ *objstoretest.Server, b *Bucket) { mustPut(t, b, key, "the first snapshot") },
			wantIs:    ErrExists,
			wantCalls: []string{"create", "part", "part", "complete", "abort"}},
		{name: "a body that fails to read partway aborts the upload",
			size: 3 * part, at: part + 10, err: errBroken, wantIs: errBroken,
			hook:      func(*objstoretest.Server, context.CancelFunc) {},
			wantCalls: []string{"create", "part", "abort"}},
		{name: "a context that ends partway still aborts the upload",
			size: 3 * part, at: part + 10, wantIs: context.Canceled,
			hook:      func(_ *objstoretest.Server, cancel context.CancelFunc) { cancel() },
			wantCalls: []string{"create", "part", "abort"}},
		{name: "a completion whose answer was lost succeeds once a HEAD finds this upload's object",
			size: part + 1,
			setup: func(_ *testing.T, srv *objstoretest.Server, _ *Bucket) {
				srv.FailMatching(1, isComplete, 0, "", true)
			},
			wantCalls: []string{"create", "part", "part", "complete", "complete", "HEAD"}, wantStored: true},
		{name: "a completion that answers 200 with an InternalError body is retried",
			size: part + 1,
			setup: func(_ *testing.T, srv *objstoretest.Server, _ *Bucket) {
				srv.FailMatching(1, isComplete, 200, "InternalError", false)
			},
			wantCalls: []string{"create", "part", "part", "complete", "complete"}, wantStored: true},
		{name: "a part size under the stores' minimum is refused before any request",
			size: 10, opts: StreamOptions{PartSize: MinPartSize - 1}, wantCalls: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := objstoretest.New(t, snapshotBucket)
			b := bucket(t, newTestClient(t, srv), snapshotBucket)
			if tt.setup != nil {
				tt.setup(t, srv, b)
			}
			before, _ := srv.Object(snapshotBucket, key)
			nreq := len(srv.Requests())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			data := randomBytes(tt.size)
			r := &hookReader{data: data, at: tt.at, err: tt.err}
			if tt.hook != nil {
				r.hook = func() { tt.hook(srv, cancel) }
			}
			opts := tt.opts
			if opts.PartSize == 0 {
				opts.PartSize = int64(part)
			}

			_, err := b.PutStream(ctx, key, r, opts)
			wantErr := !tt.wantStored
			if (err != nil) != wantErr {
				t.Fatalf("error = %v, want an error: %v", err, wantErr)
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Errorf("error = %v, want %v", err, tt.wantIs)
			}
			var calls []string
			for _, req := range srv.Requests()[nreq:] {
				calls = append(calls, call(req))
				if call(req) == "complete" && tt.opts.IfNoneMatch && req.Header.Get("If-None-Match") != "*" {
					t.Error("a write-once completion went without If-None-Match: *")
				}
			}
			if !slices.Equal(calls, tt.wantCalls) {
				t.Errorf("requests %q, want %q", calls, tt.wantCalls)
			}
			if left := srv.Uploads(); len(left) > 0 {
				t.Errorf("uploads left open: %q", left)
			}
			got, stored := srv.Object(snapshotBucket, key)
			switch {
			case tt.wantStored && !bytes.Equal(got, data):
				t.Errorf("the key holds %d bytes that aren't the %d sent", len(got), len(data))
			case !tt.wantStored && !bytes.Equal(got, before):
				t.Errorf("the key changed: it held %d bytes and holds %d, stored: %v", len(before), len(got), stored)
			}
			if tt.wantStored {
				head, err := b.Head(context.Background(), key)
				if err != nil {
					t.Fatal(err)
				}
				if (head.SHA256 != "") != tt.wantSHA256 {
					t.Errorf("SHA256 = %q, want one: %v", head.SHA256, tt.wantSHA256)
				}
			}
		})
	}
}

func TestVersioning(t *testing.T) {
	tests := []struct {
		name   string
		state  string
		want   string
		wantIs error
	}{
		{name: "a bucket whose versioning was never turned on reports no state", state: "", want: ""},
		{name: "a bucket with versioning on reports Enabled", state: VersioningEnabled, want: VersioningEnabled},
		{name: "a bucket with versioning suspended reports Suspended, since it still keeps old versions", state: VersioningSuspended, want: VersioningSuspended},
		{name: "a store without GetBucketVersioning, as R2, fails with ErrNotImplemented", state: objstoretest.NotImplemented, wantIs: ErrNotImplemented},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := objstoretest.New(t, mainBucket)
			srv.SetVersioning(mainBucket, tt.state)
			b := bucket(t, newTestClient(t, srv), mainBucket)
			got, err := b.Versioning(context.Background())
			if tt.wantIs != nil {
				if !errors.Is(err, tt.wantIs) {
					t.Fatalf("error = %v, want %v", err, tt.wantIs)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("state = %q, want %q", got, tt.want)
			}
		})
	}
}
