// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package linux

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// silentBus listens on an abstract Unix socket and accepts connections
// without ever answering, as a wedged dbus-daemon does. It returns the
// D-Bus address of the socket.
func silentBus(t *testing.T) string {
	t.Helper()
	var b [8]byte
	rand.Read(b[:])
	name := "pairtime-silent-bus-" + hex.EncodeToString(b[:])
	ln, err := net.Listen("unix", "@"+name)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
	})
	return "unix:abstract=" + name
}

// unansweredTCP returns a loopback TCP port that never answers a
// connection request, as a host behind a firewall that drops packets
// doesn't: a listening socket with a backlog of 0, whose accept queue holds
// one connection nobody accepts. Linux drops every SYN that comes to a full
// accept queue, so a connect to the port waits as long as the kernel
// resends its SYN, about two minutes.
func unansweredTCP(t *testing.T) string {
	t.Helper()
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Close(fd) })
	if err := syscall.Bind(fd, &syscall.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}}); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Listen(fd, 0); err != nil {
		t.Fatal(err)
	}
	sa, err := syscall.Getsockname(fd)
	if err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(sa.(*syscall.SockaddrInet4).Port)
	addr := net.JoinHostPort("127.0.0.1", port)
	filler, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { filler.Close() })
	// The oracle's own check: with the queue full, a connect must hang.
	if c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond); err == nil {
		c.Close()
		t.Fatal("a connect to a full accept queue got through, so this kernel can't stand in for a host that never answers")
	}
	return port
}

// acceptingTCP listens on a loopback TCP port and accepts every connection,
// reading up to want bytes from each, which it sends on the channel. It
// never answers, so a dial succeeds and nothing after it does.
func acceptingTCP(t *testing.T, want int) (port string, first <-chan []byte) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan []byte, 8)
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
			go func() {
				buf := make([]byte, want)
				c.SetReadDeadline(time.Now().Add(5 * time.Second))
				n, _ := io.ReadFull(c, buf)
				got <- buf[:n]
			}()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
	})
	_, port, _ = net.SplitHostPort(ln.Addr().String())
	return port, got
}

// godbus's Dial takes no context, and its tcp transport calls net.Dial,
// which a host that never answers holds for about two minutes. dialBus
// dials TCP itself, with ctx (#86).
func TestDialBus(t *testing.T) {
	silent := unansweredTCP(t)
	nonce := []byte("0123456789abcdef")
	noncefile := filepath.Join(t.TempDir(), "nonce")
	if err := os.WriteFile(noncefile, nonce, 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		// address is the bus address; PORT in it is a port that accepts.
		address string
		// wantNonce: the bus reads the nonce file's bytes first.
		wantNonce bool
		wantErr   bool
		// wantCtxErr: dialBus gives up, with ctx's error, at its deadline.
		wantCtxErr bool
	}{
		{
			name:       "a tcp address whose host never answers is given up at ctx's deadline",
			address:    "tcp:host=127.0.0.1,port=" + silent,
			wantCtxErr: true,
		},
		{
			name:       "a nonce-tcp address whose host never answers is given up at ctx's deadline",
			address:    "nonce-tcp:host=127.0.0.1,port=" + silent + ",noncefile=" + dbus.EscapeBusAddressValue(noncefile),
			wantCtxErr: true,
		},
		{
			name:       "an IPv4 tcp address whose host never answers is given up at ctx's deadline",
			address:    "tcp:host=127.0.0.1,port=" + silent + ",family=ipv4",
			wantCtxErr: true,
		},
		{name: "a tcp address whose host answers is dialed", address: "tcp:host=127.0.0.1,port=PORT"},
		{
			name:      "a nonce-tcp address gets the nonce file's bytes first",
			address:   "nonce-tcp:host=127.0.0.1,port=PORT,noncefile=" + dbus.EscapeBusAddressValue(noncefile),
			wantNonce: true,
		},
		{
			name:    "the next address is tried when one can't be reached",
			address: "unix:path=" + dbus.EscapeBusAddressValue(filepath.Join(t.TempDir(), "no-bus-here")) + ";tcp:host=127.0.0.1,port=PORT",
		},
		{name: "a tcp address without a port is refused", address: "tcp:host=127.0.0.1", wantErr: true},
		{name: "a nonce-tcp address without a nonce file is refused", address: "nonce-tcp:host=127.0.0.1,port=PORT", wantErr: true},
		{name: "a tcp address with a family that isn't ipv4 or ipv6 is refused", address: "tcp:host=127.0.0.1,port=PORT,family=ipx", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port, first := acceptingTCP(t, len(nonce))
			address := strings.ReplaceAll(tt.address, "PORT", port)
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			type result struct {
				conn *dbus.Conn
				err  error
			}
			done := make(chan result, 1)
			go func() {
				conn, err := dialBus(ctx, address)
				done <- result{conn, err}
			}()
			var r result
			select {
			case r = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("dialBus was still dialing 5 s after a deadline of 300 ms")
			}
			if r.conn != nil {
				defer r.conn.Close()
			}
			switch {
			case tt.wantCtxErr:
				if !errors.Is(r.err, context.DeadlineExceeded) {
					t.Errorf("dialBus() error = %v, want ctx's deadline", r.err)
				}
			case tt.wantErr:
				if r.err == nil {
					t.Error("dialBus() succeeded, want an error")
				}
			case r.err != nil:
				t.Fatalf("dialBus() error = %v", r.err)
			}
			if tt.wantNonce {
				select {
				case got := <-first:
					if !bytes.Equal(got, nonce) {
						t.Errorf("the bus read %q first, want the nonce %q", got, nonce)
					}
				case <-time.After(5 * time.Second):
					t.Error("the bus got no nonce")
				}
			}
		})
	}
}

func TestConnectBusGivesUp(t *testing.T) {
	cases := []struct {
		name   string
		ctx    func() (context.Context, context.CancelFunc)
		within time.Duration
	}{
		{
			name: "a bus that accepts the connection and never answers is given up at ctx's deadline",
			ctx: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 300*time.Millisecond)
			},
			within: 2 * time.Second,
		},
		{
			name: "canceling ctx stops a handshake the bus never answers",
			ctx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				time.AfterFunc(300*time.Millisecond, cancel)
				return ctx, cancel
			},
			within: 2 * time.Second,
		},
		{
			name: "a ctx that has ended already doesn't dial",
			ctx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, cancel
			},
			within: 100 * time.Millisecond,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			address := silentBus(t)
			ctx, cancel := tc.ctx()
			defer cancel()
			start := time.Now()
			conn, err := connectBus(ctx, address)
			took := time.Since(start)
			if err == nil {
				conn.Close()
				t.Fatal("connected to a bus that never answered")
			}
			if took > tc.within {
				t.Errorf("gave up after %v, want within %v", took, tc.within)
			}
			if ctx.Err() == nil || !errors.Is(err, ctx.Err()) {
				t.Errorf("error %v, want ctx's error %v", err, ctx.Err())
			}
		})
	}
}

func TestSessionBusAddress(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		// uidErr is os/user.Current's error; without one the uid is 1000.
		uidErr  error
		sockets []string
		files   []string // that exist and aren't sockets
		want    string   // "" means no bus
	}{
		{
			name:    "DBUS_SESSION_BUS_ADDRESS is used as it is",
			env:     map[string]string{"DBUS_SESSION_BUS_ADDRESS": "unix:path=/tmp/dbus-test"},
			sockets: []string{"/run/user/1000/bus"},
			want:    "unix:path=/tmp/dbus-test",
		},
		{
			name:    "DBUS_SESSION_BUS_ADDRESS is used even when os/user can't say who this is",
			env:     map[string]string{"DBUS_SESSION_BUS_ADDRESS": "unix:path=/tmp/dbus-test"},
			uidErr:  errors.New("user: Current requires cgo or $USER set in environment"),
			sockets: []string{"/run/user/1000/bus"},
			want:    "unix:path=/tmp/dbus-test",
		},
		{
			name:    "with no variable, systemd's per-user socket is the bus",
			sockets: []string{"/run/user/1000/bus"},
			want:    "unix:path=/run/user/1000/bus",
		},
		{
			name:    "autolaunch: counts as no address, as godbus reads it",
			env:     map[string]string{"DBUS_SESSION_BUS_ADDRESS": "autolaunch:"},
			sockets: []string{"/run/user/1000/bus"},
			want:    "unix:path=/run/user/1000/bus",
		},
		{
			name:    "when os/user can't say who this is there is no bus, as godbus then looks in no /run/user directory",
			uidErr:  errors.New("user: Current requires cgo or $HOME set in environment"),
			sockets: []string{"/run/user/1000/bus"},
		},
		{
			name:    "another user's socket doesn't count",
			sockets: []string{"/run/user/1001/bus"},
		},
		{
			name:    "a socket in XDG_RUNTIME_DIR alone doesn't count, because godbus doesn't look there",
			env:     map[string]string{"XDG_RUNTIME_DIR": "/tmp/xdg-runtime"},
			sockets: []string{"/tmp/xdg-runtime/bus"},
		},
		{
			name:  "a bus file that isn't a socket doesn't count",
			files: []string{"/run/user/1000/bus"},
		},
		{
			name: "no variable and no socket means no bus, so nothing starts dbus-launch",
		},
		{
			name:  "autolaunch: with only Ubuntu 16.04's dbus-session file means no bus",
			env:   map[string]string{"DBUS_SESSION_BUS_ADDRESS": "autolaunch:"},
			files: []string{"/run/user/1000/dbus-session"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(k string) string { return tt.env[k] }
			uid := func() (string, error) {
				if tt.uidErr != nil {
					return "", tt.uidErr
				}
				return "1000", nil
			}
			isSocket := func(name string) bool { return slices.Contains(tt.sockets, name) }
			got, err := sessionBusAddress(getenv, uid, isSocket)
			if tt.want == "" {
				if err == nil {
					t.Fatalf("got %q, want no bus", got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("got (%q, %v), want %q", got, err, tt.want)
			}
		})
	}
}

func TestSystemBusAddress(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"DBUS_SYSTEM_BUS_ADDRESS names the system bus", map[string]string{"DBUS_SYSTEM_BUS_ADDRESS": "unix:path=/tmp/system_bus_socket"}, "unix:path=/tmp/system_bus_socket"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := systemBusAddress(func(k string) string { return tc.env[k] })
			if err != nil || got != tc.want {
				t.Fatalf("address %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
