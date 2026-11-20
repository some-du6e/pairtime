// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package linux

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"slices"
	"sync"
	"testing"
	"time"
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
