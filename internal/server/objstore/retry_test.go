// SPDX-License-Identifier: AGPL-3.0-or-later

package objstore

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/objstore/objstoretest"
	"github.com/some-du6e/pairtime/internal/server/objstore/sigv4"
)

// cutStore sits in front of a fake store and cuts the first request whose
// body is longer than cut: it reads cut bytes of the body, then answers
// 500 InternalError and closes the connection, as a store does that gives
// up on a request partway through its body. It never reads the rest, so
// the client's write loop is still sending the body when the answer
// comes, as it would be over a real network. It hands the retry, and
// every other request, to the fake, which checks the signature and the
// payload hash and carries the request out. It records what the cut
// request and each retry of it sent.
type cutStore struct {
	store http.Handler
	cut   int

	mu sync.Mutex
	// target is the cut request's method and URI, "" before the cut.
	target   string
	attempts []attempt
}

// attempt is what one attempt at the cut request sent: its Content-Length
// and its X-Amz-Content-Sha256, what checking its signature over that
// hash found, and the bytes the store read of its body (the first cut of
// the cut attempt, none recorded for a retry, which goes to the fake).
type attempt struct {
	length  int64
	payload string
	signErr error
	body    []byte
}

func (s *cutStore) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	req := r.Method + " " + r.URL.RequestURI()
	s.mu.Lock()
	cut := s.target == "" && r.ContentLength > int64(s.cut)
	if cut {
		s.target = req
	}
	retry := !cut && req == s.target
	s.mu.Unlock()
	if !cut && !retry {
		s.store.ServeHTTP(w, r)
		return
	}
	a := attempt{length: r.ContentLength, payload: r.Header.Get("X-Amz-Content-Sha256")}
	a.signErr = sigv4.Verify(r, a.payload, objstoretest.Credentials, objstoretest.Region, "s3", time.Now(), objstoretest.MaxSkew)
	if retry {
		s.record(a)
		s.store.ServeHTTP(w, r)
		return
	}
	// Read the start of the body, then answer 500 and leave the rest
	// unread, so the write loop is still sending when the answer comes
	// and Go closes the connection on return. Reading nothing would race
	// a touch more surely, but reading cut bytes shows the cut was
	// mid-body and still leaves the write loop a long way from done.
	a.body = make([]byte, s.cut)
	n, _ := io.ReadFull(r.Body, a.body)
	a.body = a.body[:n]
	s.record(a)
	w.Header().Set("Connection", "close")
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusInternalServerError)
	io.WriteString(w, "<Error><Code>InternalError</Code><Message>cut mid-body by the test</Message></Error>")
}

func (s *cutStore) record(a attempt) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts = append(s.attempts, a)
}

// TestRetryCutMidBody cuts a request's first attempt after its body has
// begun, so the client's write loop is still reading the body when the
// 500 comes back and the retry starts. Before the fix the retry rewinds
// the shared reader, and refills the shared part buffer, while that write
// loop can still read them, which the race detector catches. The test
// also pins that the retry sends the same bytes, Content-Length and
// X-Amz-Content-Sha256 as the first attempt.
func TestRetryCutMidBody(t *testing.T) {
	const key = "retry/cut"
	const cut = 64 << 10
	tests := []struct {
		name string
		size int
		// part is how many bytes of data the cut request carries: the
		// whole body for a put, one part for a multipart upload.
		part int
		send func(b *Bucket, data []byte) error
	}{
		{name: "a put cut mid-body is sent again whole, with the same Content-Length and payload hash",
			size: 1 << 20, part: 1 << 20,
			send: func(b *Bucket, data []byte) error {
				_, err := b.Put(context.Background(), key, bytes.NewReader(data), PutOptions{})
				return err
			}},
		{name: "a part cut mid-body is sent again whole, and the next part, read into the same buffer, arrives intact",
			size: MinPartSize + MinPartSize/2, part: MinPartSize,
			send: func(b *Bucket, data []byte) error {
				_, err := b.PutStream(context.Background(), key, bytes.NewReader(data), StreamOptions{PartSize: MinPartSize})
				return err
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := objstoretest.New(t, mainBucket)
			store := &cutStore{store: srv.Config.Handler, cut: cut}
			front := httptest.NewServer(store)
			t.Cleanup(front.Close)
			b := bucket(t, newTestClient(t, srv, func(c *Config) { c.Endpoint = front.URL }), mainBucket)
			data := randomBytes(tt.size)

			if err := tt.send(b, data); err != nil {
				t.Fatal(err)
			}
			want := data[:tt.part]
			store.mu.Lock()
			attempts := store.attempts
			store.mu.Unlock()
			if len(attempts) != 2 {
				t.Fatalf("the cut request was sent %d times, want twice (a cut attempt and a retry)", len(attempts))
			}
			if len(attempts[0].body) != cut || !bytes.Equal(attempts[0].body, want[:cut]) {
				t.Errorf("the cut attempt sent %d bytes of body that aren't the first %d of the request's", len(attempts[0].body), cut)
			}
			for i, a := range attempts {
				if a.length != int64(len(want)) {
					t.Errorf("attempt %d has Content-Length %d, want %d", i+1, a.length, len(want))
				}
				if a.payload != sigv4.PayloadHash(want) {
					t.Errorf("attempt %d has X-Amz-Content-Sha256 %q, want the body's SHA-256", i+1, a.payload)
				}
				if a.signErr != nil {
					t.Errorf("attempt %d isn't signed over its payload hash: %v", i+1, a.signErr)
				}
			}
			if got, _ := srv.Object(mainBucket, key); !bytes.Equal(got, data) {
				t.Errorf("the key holds %d bytes that aren't the %d sent", len(got), len(data))
			}
		})
	}
}
