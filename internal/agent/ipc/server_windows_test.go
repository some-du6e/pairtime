// SPDX-License-Identifier: AGPL-3.0-or-later

package ipc

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/some-du6e/pairtime/internal/agent/inbox"
)

// otherUser is a user this test doesn't run as: SYSTEM.
func otherUser(t *testing.T) identity {
	t.Helper()
	return identity{sid: sidSystem}
}

// checkPrivateDir checks that dir has the protected DACL that secureDir
// gives it, as sameDACL sees it, so the next start finds nothing to change.
func checkPrivateDir(t *testing.T, dir string) {
	t.Helper()
	me, err := self()
	if err != nil {
		t.Fatal(err)
	}
	got, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	want, err := windows.SecurityDescriptorFromString(dirSDDL(me))
	if err != nil {
		t.Fatal(err)
	}
	if !sameDACL(got, want) {
		t.Errorf("%s has the DACL %s, want %s", dir, got, want)
	}
}

func TestPipe(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, ep Endpoint)
	}{
		{
			name: "a message sent over the forced pipe is acked, and arrives by pipe with the sender's pid",
			run: func(t *testing.T, ep Endpoint) {
				srv, sink := serve(t, ep, Options{ForcePipe: true}, nil)
				if srv.Via() != inbox.ViaPipe || srv.Fallback() == "" {
					t.Fatalf("Via() = %q, Fallback() = %q; want the pipe and a reason", srv.Via(), srv.Fallback())
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				c, err := Dial(ctx, ep)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				if c.Via() != inbox.ViaPipe {
					t.Errorf("the connection is %q, want the pipe", c.Via())
				}
				m := message(1)
				if err := c.Send(ctx, m); err != nil {
					t.Fatal(err)
				}
				got := sink.deliveries()
				if len(got) != 1 || got[0].ID != m.ID || got[0].Via != inbox.ViaPipe || got[0].PeerPID != os.Getpid() {
					t.Errorf("the sink has %+v, want only %s by pipe from pid %d", got, m.ID, os.Getpid())
				}
			},
		},
		{
			name: "a second agent can't take a pipe that one has already",
			run: func(t *testing.T, ep Endpoint) {
				serve(t, ep, Options{ForcePipe: true}, nil)
				if srv, err := Listen(ep, Options{ForcePipe: true}); err == nil {
					srv.Close()
					t.Error("a second Listen on the same pipe worked")
				}
			},
		},
		{
			name: "the pipe refuses a client of another user",
			run: func(t *testing.T, ep Endpoint) {
				srv, sink := serve(t, ep, Options{ForcePipe: true}, func(s *Server) { s.want = otherUser(t) })
				if err := sendOne(t, ep, message(1)); err == nil {
					t.Error("Send = nil, want the agent to hang up")
				}
				if len(sink.deliveries()) != 0 || srv.Refused() != 1 {
					t.Errorf("the sink has %d messages and %d connections were refused, want 0 and 1", len(sink.deliveries()), srv.Refused())
				}
			},
		},
		{
			name: "a sender refuses a pipe served by another user",
			run: func(t *testing.T, ep Endpoint) {
				serve(t, ep, Options{ForcePipe: true}, nil)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if c, err := dial(ctx, ep, otherUser(t)); !errors.Is(err, ErrNotAgent) {
					if c != nil {
						c.Close()
					}
					t.Errorf("dial = %v, want ErrNotAgent", err)
				}
			},
		},
		{
			name: "many clients at once all get through the pipe",
			run: func(t *testing.T, ep Endpoint) {
				_, sink := serve(t, ep, Options{ForcePipe: true}, nil)
				const clients, each = 16, 20
				var wg sync.WaitGroup
				for range clients {
					wg.Add(1)
					go func() {
						defer wg.Done()
						for n := range each {
							if via, err := Send(ep, message(n), 10*time.Second); err != nil || via != inbox.ViaPipe {
								t.Errorf("Send = %q, %v; want the pipe", via, err)
							}
						}
					}()
				}
				wg.Wait()
				if got := len(sink.deliveries()); got != clients*each {
					t.Errorf("the sink has %d messages, want %d", got, clients*each)
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
