// SPDX-License-Identifier: AGPL-3.0-or-later

package stream

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestOverHTTP runs streams through net/http's own server, whose response
// writer buffers until the hub flushes it.
func TestOverHTTP(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, h *Hub, resp *http.Response, lines *bufio.Reader)
	}{
		{name: "the answer is 200 text/event-stream with no-cache, chunked, and nothing stores it", run: func(t *testing.T, _ *Hub, resp *http.Response, _ *bufio.Reader) {
			if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" || resp.Header.Get("Cache-Control") != "no-cache" {
				t.Errorf("got %d, Content-Type %q, Cache-Control %q", resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header.Get("Cache-Control"))
			}
			if len(resp.TransferEncoding) != 1 || resp.TransferEncoding[0] != "chunked" || resp.ContentLength != -1 {
				t.Errorf("transfer encoding %q, length %d, want a chunked stream", resp.TransferEncoding, resp.ContentLength)
			}
		}},
		{name: "an event published reaches the client at once, while its stream stays open", run: func(t *testing.T, h *Hub, _ *http.Response, lines *bufio.Reader) {
			publishTo(h, userKey+"a1", "a1", "credited", `{"live":true}`)
			block := readBlock(t, lines)
			if !strings.Contains(block, "\nevent: credited\ndata: {\"live\":true}") || !strings.HasPrefix(block, "id: user:me:") {
				t.Errorf("the event arrived as %q", block)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := New(Options{Account: testAccount})
			srv := httptest.NewServer(h)
			t.Cleanup(func() {
				h.Close()
				srv.Close()
			})
			req, err := http.NewRequest(http.MethodGet, srv.URL+meTopic, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("X-Test-Account", "a1")
			req.Header.Set("Accept", "text/event-stream")
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			lines := bufio.NewReader(resp.Body)
			// The first block, which says the stream is open.
			if first := readBlock(t, lines); !strings.HasPrefix(first, "retry: 5000\nid: user:me:") {
				t.Fatalf("the stream started with %q", first)
			}
			tt.run(t, h, resp, lines)
		})
	}
}

// readBlock reads one block of an event stream, without its blank line,
// within 10 s.
func readBlock(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	type result struct {
		block string
		err   error
	}
	done := make(chan result, 1)
	go func() {
		var b strings.Builder
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				done <- result{b.String(), err}
				return
			}
			if line == "\n" {
				done <- result{strings.TrimSuffix(b.String(), "\n"), nil}
				return
			}
			b.WriteString(line)
		}
	}()
	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("reading the stream: %v after %q", res.err, res.block)
		}
		return res.block
	case <-time.After(10 * time.Second):
		t.Fatal("no block within 10 s")
	}
	return ""
}

// inUse is the memory the Go runtime holds in use after a collection: the
// heap's spans, goroutine stacks and the runtime's own structures.
func inUse() uint64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapInuse + m.StackInuse + m.MSpanInuse + m.MCacheInuse + m.GCSys + m.OtherSys + m.BuckHashSys
}

// TestThousandStreams is PLAN's check of WP-SV8 that 1,000 open streams
// grow the process by less than 50 MB, measured by the runtime's memory
// statistics. Each stream is a real connection to net/http's server, a
// different account's, on its own topic, so the count holds the server's
// buffers, goroutines and the hub's topics. The clients are bare
// connections that send a request and read its first block, so they add
// almost nothing of their own.
func TestThousandStreams(t *testing.T) {
	tests := []struct {
		name    string
		streams int
		budget  int64
	}{
		{name: "1,000 open streams grow the process by less than 50 MB", streams: 1000, budget: 50 << 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := New(Options{Account: testAccount, MaxPerClient: tt.streams})
			srv := httptest.NewServer(h)
			var conns []net.Conn
			t.Cleanup(func() {
				h.Close()
				for _, c := range conns {
					c.Close()
				}
				srv.Close()
			})
			conns = make([]net.Conn, 0, tt.streams)
			buf := make([]byte, 4096)
			before := inUse()
			for i := range tt.streams {
				c, err := net.Dial("tcp", srv.Listener.Addr().String())
				if err != nil {
					t.Fatalf("stream %d: %v", i, err)
				}
				conns = append(conns, c)
				fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: pairtime.test\r\nAccept: text/event-stream\r\nX-Test-Account: a%d\r\n\r\n", meTopic, i)
				c.SetReadDeadline(time.Now().Add(30 * time.Second))
				var got []byte
				for !bytes.Contains(got, []byte("retry: 5000\n")) {
					n, err := c.Read(buf)
					if err != nil {
						t.Fatalf("stream %d: %v after %q", i, err, got)
					}
					got = append(got, buf[:n]...)
				}
				if !bytes.HasPrefix(got, []byte("HTTP/1.1 200 OK\r\n")) || !bytes.Contains(got, []byte("Content-Type: text/event-stream\r\n")) {
					t.Fatalf("stream %d started %q", i, got)
				}
			}
			if n := h.Streams(); n != tt.streams {
				t.Fatalf("the hub counts %d streams, want %d", n, tt.streams)
			}
			after := inUse()
			grew := int64(after) - int64(before)
			t.Logf("%d streams: %d bytes in use before, %d after, %.1f KiB a stream", tt.streams, before, after, float64(grew)/float64(tt.streams)/1024)
			if grew >= tt.budget {
				t.Errorf("%d streams grew the process by %d bytes, %.1f MB, over %d MB", tt.streams, grew, float64(grew)/(1<<20), tt.budget>>20)
			}
		})
	}
}
