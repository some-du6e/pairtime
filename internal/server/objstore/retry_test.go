// SPDX-License-Identifier: AGPL-3.0-or-later

package objstore

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/objstore/objstoretest"
	"github.com/some-du6e/pairtime/internal/server/objstore/sigv4"
)

// cutStore stands in front of a fake store and cuts the first request
// whose body is longer than cut, as a store does that gives up on a
// request partway through its body: it reads cut bytes, then answers 500
// InternalError and closes the connection. It records what that request
// and each retry of it sent. Every other request goes to the fake store,
// and so does each retry, which the fake checks and carries out.
type cutStore struct {
	store http.Handler
	cut   int

	mu sync.Mutex
	// target is the cut request's method and URI, "" before the cut.
	target   string
	attempts []attempt
}

// attempt is what one attempt at the cut request sent.
type attempt struct {
	// length is its Content-Length and payload its X-Amz-Content-Sha256.
	length  int64
	payload string
	// signErr is what checking its signature over payload found.
	signErr error
	// body is what the store read of the body: the first cut bytes of the
	// cut attempt, all of a retry.
	body []byte
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
		var body bytes.Buffer
		r.Body = struct {
			io.Reader
			io.Closer
		}{io.TeeReader(r.Body, &body), r.Body}
		s.store.ServeHTTP(w, r)
		a.body = body.Bytes()
		s.record(a)
		return
	}
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

// stallDialer dials connections that stall mid-body, as a socket does
// once its buffers are full and the store has stopped reading. On the
// first connection to have sent budget bytes, the next write waits until
// the transport closes the connection, and sends nothing. So the
// transport is still sending the cut request's body when the store's
// answer comes, as it would be over a real network, whatever size the
// kernel's buffers are. Every other connection passes everything.
//
// The held write sends none of its bytes because the race detector
// orders each socket write before every later socket read. Had the write
// reached the socket, the transport's last read of the body would count
// as coming before the store's answer, and so before the retry, and a
// retry that reuses the body without waiting for the transport would go
// unseen.
type stallDialer struct {
	budget int
	held   atomic.Bool
}

func (d *stallDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	return &stallConn{Conn: c, d: d, closed: make(chan struct{})}, nil
}

type stallConn struct {
	net.Conn
	d *stallDialer
	// sent counts the bytes written; only the transport's write loop
	// writes.
	sent   int
	closed chan struct{}
	once   sync.Once
}

func (c *stallConn) Write(p []byte) (int, error) {
	if c.sent >= c.d.budget && c.d.held.CompareAndSwap(false, true) {
		<-c.closed
		return 0, net.ErrClosed
	}
	c.sent += len(p)
	return c.Conn.Write(p)
}

func (c *stallConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

// TestRetryCutMidBody cuts a request's first attempt mid-body while the
// transport is still sending it. Under -race it also checks that the
// retry doesn't touch the body, or the part buffer behind it, while the
// transport may still be reading them for the cut attempt.
func TestRetryCutMidBody(t *testing.T) {
	const key = "retry/cut"
	const cut = 64 << 10
	tests := []struct {
		name string
		size int
		// send makes the call whose first big request the store cuts.
		send func(b *Bucket, data []byte) error
		// part is how many bytes of data the cut request carries.
		part int
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
			// The budget leaves room for the headers before the cut.
			stall := &stallDialer{budget: cut + 16<<10}
			transport := &http.Transport{DialContext: stall.DialContext}
			t.Cleanup(transport.CloseIdleConnections)
			b := bucket(t, newTestClient(t, srv, func(c *Config) {
				c.Endpoint = front.URL
				c.HTTPClient = &http.Client{Transport: transport}
			}), mainBucket)
			data := randomBytes(tt.size)

			if err := tt.send(b, data); err != nil {
				t.Fatal(err)
			}
			if !stall.held.Load() {
				t.Fatal("no write was held, so the cut attempt wasn't still sending when the store answered")
			}
			want := data[:tt.part]
			store.mu.Lock()
			attempts := store.attempts
			store.mu.Unlock()
			if len(attempts) != 2 {
				t.Fatalf("the cut request was sent %d times, want twice", len(attempts))
			}
			if !bytes.Equal(attempts[0].body, want[:cut]) {
				t.Errorf("the cut attempt's first %d bytes aren't the body's", len(attempts[0].body))
			}
			if !bytes.Equal(attempts[1].body, want) {
				t.Errorf("the retry sent %d bytes that aren't the %d of the body", len(attempts[1].body), len(want))
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
