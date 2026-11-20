// SPDX-License-Identifier: AGPL-3.0-or-later

package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/inbox"
)

// shortDir makes a directory whose path leaves room for a socket name.
// t.TempDir puts the test's name in the path, which on macOS can take it
// past sun_path's 104 bytes.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "pt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func endpointIn(t *testing.T, dir string) Endpoint {
	t.Helper()
	ep, err := EndpointFor(home.Dirs{Data: dir, Config: dir, Runtime: dir})
	if err != nil {
		t.Fatal(err)
	}
	return ep
}

// memSink keeps deliveries in memory, each id once, as a Sink must.
type memSink struct {
	mu   sync.Mutex
	got  []inbox.Delivery
	fail error // when set, Deliver fails with it
}

func (s *memSink) Deliver(d inbox.Delivery) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return s.fail
	}
	for _, g := range s.got {
		if g.ID == d.ID {
			return nil
		}
	}
	s.got = append(s.got, d)
	return nil
}

func (s *memSink) deliveries() []inbox.Delivery {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]inbox.Delivery(nil), s.got...)
}

func (s *memSink) setFail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail = err
}

// serve listens on ep and serves into a new memSink until the test ends.
// before, when set, sees the server before it serves.
func serve(t *testing.T, ep Endpoint, opts Options, before func(*Server)) (*Server, *memSink) {
	t.Helper()
	srv, err := Listen(ep, opts)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	if before != nil {
		before(srv)
	}
	sink := &memSink{}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(sink) }()
	t.Cleanup(func() {
		srv.Close()
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	return srv, sink
}

func sendOne(t *testing.T, ep Endpoint, m inbox.Message) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Dial(ctx, ep)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.Send(ctx, m)
}

func message(n int) inbox.Message {
	return inbox.New("test", json.RawMessage(fmt.Sprintf(`{"n":%d}`, n)))
}

// rawExchange writes frames to the socket by hand and returns the reply to
// each, until the agent hangs up.
func rawExchange(t *testing.T, ep Endpoint, frames ...[]byte) (replies []reply, err error) {
	t.Helper()
	c, err := net.DialTimeout("unix", ep.Socket, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	for _, f := range frames {
		if _, err := c.Write(f); err != nil {
			return replies, err
		}
		b, err := ReadFrame(c, maxReply)
		if err != nil {
			return replies, err
		}
		var r reply
		if err := json.Unmarshal(b, &r); err != nil {
			t.Fatal(err)
		}
		replies = append(replies, r)
	}
	_, err = ReadFrame(c, maxReply)
	return replies, err
}

func frame(body []byte) []byte {
	var buf strings.Builder
	WriteFrame(&buf, body, inbox.MaxMessage)
	return []byte(buf.String())
}

func TestServer(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, ep Endpoint)
	}{
		{
			name: "an acked message is in the sink, with the sender's pid and the socket as its way in",
			run: func(t *testing.T, ep Endpoint) {
				_, sink := serve(t, ep, Options{}, nil)
				m := message(1)
				if err := sendOne(t, ep, m); err != nil {
					t.Fatal(err)
				}
				got := sink.deliveries()
				if len(got) != 1 || got[0].ID != m.ID || got[0].Via != inbox.ViaSocket || got[0].PeerPID != os.Getpid() {
					t.Fatalf("the sink has %+v, want only %s via the socket from pid %d", got, m.ID, os.Getpid())
				}
				if got[0].Received.IsZero() || got[0].Received.Location() != time.UTC {
					t.Errorf("Received = %v, want the UTC time it arrived", got[0].Received)
				}
			},
		},
		{
			name: "several messages on one connection are each acked in turn",
			run: func(t *testing.T, ep Endpoint) {
				_, sink := serve(t, ep, Options{}, nil)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				c, err := Dial(ctx, ep)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				for n := range 5 {
					if err := c.Send(ctx, message(n)); err != nil {
						t.Fatalf("message %d: %v", n, err)
					}
				}
				if got := len(sink.deliveries()); got != 5 {
					t.Errorf("the sink has %d messages, want 5", got)
				}
			},
		},
		{
			name: "a message the sink can't store is refused, not acked",
			run: func(t *testing.T, ep Endpoint) {
				_, sink := serve(t, ep, Options{}, nil)
				sink.setFail(errors.New("disk full"))
				if err := sendOne(t, ep, message(1)); !errors.Is(err, ErrRefused) {
					t.Errorf("Send = %v, want ErrRefused", err)
				}
			},
		},
		{
			name: "a frame that isn't a message gets an error reply, and the agent hangs up",
			run: func(t *testing.T, ep Endpoint) {
				_, sink := serve(t, ep, Options{}, nil)
				replies, err := rawExchange(t, ep, frame([]byte(`{"id":"nope","kind":"hook"}`)))
				if len(replies) != 1 || replies[0].OK || replies[0].Error == "" {
					t.Errorf("replies = %+v, want one error", replies)
				}
				if err == nil {
					t.Error("the agent kept the connection open")
				}
				if len(sink.deliveries()) != 0 {
					t.Error("the sink got a message")
				}
			},
		},
		{
			name: "a frame longer than a message may be is refused unread, and the agent hangs up",
			run: func(t *testing.T, ep Endpoint) {
				serve(t, ep, Options{}, nil)
				header := []byte{0x7f, 0xff, 0xff, 0xff}
				if replies, err := rawExchange(t, ep, header); len(replies) != 0 || err == nil {
					t.Errorf("replies = %+v, err = %v; want the agent to hang up with no reply", replies, err)
				}
			},
		},
		{
			name: "the agent refuses a connection from another user",
			run: func(t *testing.T, ep Endpoint) {
				srv, sink := serve(t, ep, Options{}, func(s *Server) { s.want = otherUser(t) })
				if err := sendOne(t, ep, message(1)); err == nil {
					t.Error("Send = nil, want the agent to hang up")
				}
				if len(sink.deliveries()) != 0 {
					t.Error("the sink got the message")
				}
				if n := srv.Refused(); n != 1 {
					t.Errorf("Refused() = %d, want 1", n)
				}
			},
		},
		{
			name: "a sender refuses an agent that runs as another user",
			run: func(t *testing.T, ep Endpoint) {
				_, sink := serve(t, ep, Options{}, nil)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if c, err := dial(ctx, ep, otherUser(t)); !errors.Is(err, ErrNotAgent) {
					if c != nil {
						c.Close()
					}
					t.Errorf("dial = %v, want ErrNotAgent", err)
				}
				if len(sink.deliveries()) != 0 {
					t.Error("the sink got a message")
				}
			},
		},
		{
			name: "Send spools the message when no agent listens",
			run: func(t *testing.T, ep Endpoint) {
				m := message(1)
				via, err := Send(ep, m, HookTimeout)
				if err != nil || via != inbox.ViaInbox {
					t.Fatalf("Send = %q, %v; want the inbox", via, err)
				}
				sink := &memSink{}
				if _, err := inbox.Drain(ep.Inbox, sink, time.Now); err != nil {
					t.Fatal(err)
				}
				if got := sink.deliveries(); len(got) != 1 || got[0].ID != m.ID {
					t.Errorf("the spool had %+v, want %s", got, m.ID)
				}
			},
		},
		{
			name: "Send spools a message that the agent refused",
			run: func(t *testing.T, ep Endpoint) {
				_, sink := serve(t, ep, Options{}, nil)
				sink.setFail(errors.New("disk full"))
				if via, err := Send(ep, message(1), 10*time.Second); err != nil || via != inbox.ViaInbox {
					t.Errorf("Send = %q, %v; want the inbox", via, err)
				}
			},
		},
		{
			name: "Send hands the message over the socket when the agent acks it",
			run: func(t *testing.T, ep Endpoint) {
				_, sink := serve(t, ep, Options{}, nil)
				if via, err := Send(ep, message(1), 10*time.Second); err != nil || via != inbox.ViaSocket {
					t.Errorf("Send = %q, %v; want the socket", via, err)
				}
				if len(sink.deliveries()) != 1 {
					t.Error("the sink didn't get the message")
				}
				if _, err := os.Stat(filepath.Join(ep.Inbox, inbox.SpoolName)); !os.IsNotExist(err) {
					t.Errorf("an acked message was spooled too: %v", err)
				}
			},
		},
		{
			name: "Close hangs up open connections and Serve returns",
			run: func(t *testing.T, ep Endpoint) {
				srv, err := Listen(ep, Options{})
				if err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- srv.Serve(&memSink{}) }()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				c, err := Dial(ctx, ep)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				if err := c.Send(ctx, message(1)); err != nil {
					t.Fatal(err)
				}
				if err := srv.Close(); err != nil {
					t.Errorf("Close: %v", err)
				}
				if err := <-done; err != nil {
					t.Errorf("Serve after Close = %v, want nil", err)
				}
				if err := c.Send(ctx, message(2)); err == nil {
					t.Error("a connection still took a message after Close")
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.run(t, endpointIn(t, shortDir(t)))
		})
	}
}

func TestListen(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, dir string)
	}{
		{
			name: "a socket file that a dead agent left is replaced",
			run: func(t *testing.T, dir string) {
				ep := endpointIn(t, dir)
				ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: ep.Socket, Net: "unix"})
				if err != nil {
					t.Fatal(err)
				}
				ln.SetUnlinkOnClose(false)
				ln.Close()
				if _, err := os.Lstat(ep.Socket); err != nil {
					t.Fatalf("the dead socket file isn't there: %v", err)
				}
				srv, _ := serve(t, ep, Options{}, nil)
				if srv.Via() != inbox.ViaSocket {
					t.Fatalf("Via() = %q, want the socket", srv.Via())
				}
				if err := sendOne(t, ep, message(1)); err != nil {
					t.Error(err)
				}
			},
		},
		{
			name: "a socket that a live agent answers on is left alone",
			run: func(t *testing.T, dir string) {
				ep := endpointIn(t, dir)
				serve(t, ep, Options{}, nil)
				if srv, err := Listen(ep, Options{}); !errors.Is(err, ErrInUse) {
					if srv != nil {
						srv.Close()
					}
					t.Fatalf("a second Listen = %v, want ErrInUse", err)
				}
				if err := sendOne(t, ep, message(1)); err != nil {
					t.Errorf("the first agent stopped answering: %v", err)
				}
			},
		},
		{
			name: "the socket's directory is made this user's alone",
			run: func(t *testing.T, dir string) {
				run := filepath.Join(dir, "run")
				if err := os.MkdirAll(run, 0o755); err != nil {
					t.Fatal(err)
				}
				ep, err := EndpointFor(home.Dirs{Data: dir, Config: dir, Runtime: run})
				if err != nil {
					t.Fatal(err)
				}
				serve(t, ep, Options{}, nil)
				checkPrivateDir(t, run)
			},
		},
		{
			name: "a socket directory owned by another user is refused",
			run: func(t *testing.T, dir string) {
				if err := secureDir(dir, otherUser(t)); err == nil {
					t.Error("secureDir took a directory that isn't the wanted user's")
				}
			},
		},
		{
			name: "a socket path too long for the OS fails clearly, or moves to the pipe on Windows",
			run: func(t *testing.T, dir string) {
				run := filepath.Join(dir, strings.Repeat("d", 110))
				ep, err := EndpointFor(home.Dirs{Data: dir, Config: dir, Runtime: run})
				if err != nil {
					t.Fatal(err)
				}
				srv, err := Listen(ep, Options{})
				if runtime.GOOS == "windows" {
					if err != nil || srv.Via() != inbox.ViaPipe {
						t.Fatalf("Listen = %v; want the pipe", err)
					}
					srv.Close()
					return
				}
				if err == nil {
					srv.Close()
					t.Fatal("Listen took a socket path over sun_path's size")
				}
				if !strings.Contains(err.Error(), "bytes") {
					t.Errorf("Listen = %v, want it to say how long the path is", err)
				}
			},
		},
		{
			name: "the self-test's probe never reaches the sink",
			run: func(t *testing.T, dir string) {
				ep := endpointIn(t, dir)
				_, sink := serve(t, ep, Options{}, nil)
				if err := sendOne(t, ep, message(1)); err != nil {
					t.Fatal(err)
				}
				for _, d := range sink.deliveries() {
					if d.Kind != "test" {
						t.Errorf("the sink got a %q message", d.Kind)
					}
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.run(t, shortDir(t))
		})
	}
}
