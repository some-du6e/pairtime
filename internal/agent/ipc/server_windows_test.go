// SPDX-License-Identifier: AGPL-3.0-or-later

package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

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

// editNote changes the note of the agent on ep's pipe as edit says.
func editNote(t *testing.T, ep Endpoint, edit func(n *pipeNote)) {
	t.Helper()
	n, err := readPipeNote(ep)
	if err != nil {
		t.Fatal(err)
	}
	edit(&n)
	b, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ep.PipeNote, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// dialAs dials ep as a sender of user want would, and wants it refused
// with ErrNotAgent for the reason why names.
func dialAs(t *testing.T, ep Endpoint, want identity, why string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := dial(ctx, ep, want)
	if c != nil {
		c.Close()
	}
	if !errors.Is(err, ErrNotAgent) || !strings.Contains(err.Error(), why) {
		t.Errorf("dial = %v, want ErrNotAgent because %s", err, why)
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
				dialAs(t, ep, otherUser(t), "belongs to")
			},
		},
		{
			name: "a sender refuses a pipe this user doesn't own, even one whose note names the process serving it",
			run: func(t *testing.T, ep Endpoint) {
				me, err := self()
				if err != nil {
					t.Fatal(err)
				}
				// A pipe that Administrators own stands for one another
				// account made. This process makes it and writes its note,
				// so the pid and start-time checks pass and only the owner
				// is wrong.
				sd, err := windows.SecurityDescriptorFromString("O:BAD:P(A;;GA;;;" + me.sid + ")")
				if err != nil {
					t.Fatal(err)
				}
				sa := &windows.SecurityAttributes{SecurityDescriptor: sd}
				sa.Length = uint32(unsafe.Sizeof(*sa))
				h, err := createPipe(ep.Pipe, sa, true)
				if errors.Is(err, windows.ERROR_INVALID_OWNER) {
					t.Skip("only an elevated process may make an object that Administrators own")
				}
				if err != nil {
					t.Fatal(err)
				}
				defer windows.CloseHandle(h)
				if err := writePipeNote(ep); err != nil {
					t.Fatal(err)
				}
				dialAs(t, ep, me, "belongs to")
			},
		},
		{
			name: "a sender refuses a pipe that a process other than the one in the note serves",
			run: func(t *testing.T, ep Endpoint) {
				me, err := self()
				if err != nil {
					t.Fatal(err)
				}
				serve(t, ep, Options{ForcePipe: true}, nil)
				editNote(t, ep, func(n *pipeNote) { n.PID += 4 })
				dialAs(t, ep, me, "the note names process")
			},
		},
		{
			name: "a sender refuses a pipe whose process has the note's pid but started at another time",
			run: func(t *testing.T, ep Endpoint) {
				me, err := self()
				if err != nil {
					t.Fatal(err)
				}
				serve(t, ep, Options{ForcePipe: true}, nil)
				editNote(t, ep, func(n *pipeNote) { n.Started-- })
				dialAs(t, ep, me, "started at another time")
			},
		},
		{
			name: "a sender doesn't try the pipe when no agent's note says it listens there",
			run: func(t *testing.T, ep Endpoint) {
				_, sink := serve(t, ep, Options{ForcePipe: true}, nil)
				if err := os.Remove(ep.PipeNote); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if c, err := Dial(ctx, ep); !errors.Is(err, errNoPipeNote) {
					if c != nil {
						c.Close()
					}
					t.Errorf("Dial = %v, want errNoPipeNote", err)
				}
				if via, err := Send(ep, message(1), 10*time.Second); err != nil || via != inbox.ViaInbox {
					t.Errorf("Send = %q, %v; want the message spooled", via, err)
				}
				if n := len(sink.deliveries()); n != 0 {
					t.Errorf("the pipe took %d messages", n)
				}
			},
		},
		{
			name: "the agent on the pipe writes a note naming itself, and deletes it when it closes",
			run: func(t *testing.T, ep Endpoint) {
				srv, _ := serve(t, ep, Options{ForcePipe: true}, nil)
				n, err := readPipeNote(ep)
				if err != nil {
					t.Fatal(err)
				}
				if n.Pipe != ep.Pipe || n.PID != windows.GetCurrentProcessId() {
					t.Errorf("the note is %+v, want pipe %s and pid %d", n, ep.Pipe, windows.GetCurrentProcessId())
				}
				if err := srv.Close(); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(ep.PipeNote); !os.IsNotExist(err) {
					t.Errorf("the note outlived the server: %v", err)
				}
			},
		},
		{
			name: "when accepting keeps failing, the agent on the pipe stops listening and deletes its note",
			run: func(t *testing.T, ep Endpoint) {
				srv, err := Listen(ep, Options{ForcePipe: true})
				if err != nil {
					t.Fatal(err)
				}
				defer srv.Close()
				giveUp(t, srv)
				if via := srv.Via(); via != "" {
					t.Errorf("Via() = %q after Serve gave up, want none", via)
				}
				if _, err := os.Stat(ep.PipeNote); !os.IsNotExist(err) {
					t.Errorf("the note outlived the listener: %v", err)
				}
				if via, err := Send(ep, message(1), 10*time.Second); err != nil || via != inbox.ViaInbox {
					t.Errorf("Send = %q, %v; want the message spooled", via, err)
				}
			},
		},
		{
			name: "an agent on the socket deletes the note that an agent on the pipe left",
			run: func(t *testing.T, ep Endpoint) {
				if err := os.WriteFile(ep.PipeNote, []byte(`{"pipe":"x","pid":4,"started":1}`), 0o600); err != nil {
					t.Fatal(err)
				}
				srv, _ := serve(t, ep, Options{}, nil)
				if srv.Via() != inbox.ViaSocket {
					t.Fatalf("Via() = %q, want the socket", srv.Via())
				}
				if _, err := os.Stat(ep.PipeNote); !os.IsNotExist(err) {
					t.Errorf("the old note is still there: %v", err)
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
