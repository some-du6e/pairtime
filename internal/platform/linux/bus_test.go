// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package linux

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"path/filepath"
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

func TestBusAddresses(t *testing.T) {
	runtimeDir, emptyDir := t.TempDir(), t.TempDir()
	ln, err := net.Listen("unix", filepath.Join(runtimeDir, "bus"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	cases := []struct {
		name string
		bus  string // "session" or "system"
		env  map[string]string
		want string // "" for no bus
	}{
		{"DBUS_SESSION_BUS_ADDRESS names the session bus", "session", map[string]string{"DBUS_SESSION_BUS_ADDRESS": "unix:path=/run/user/1000/bus", "XDG_RUNTIME_DIR": runtimeDir}, "unix:path=/run/user/1000/bus"},
		{"without DBUS_SESSION_BUS_ADDRESS the session bus is the runtime directory's bus socket", "session", map[string]string{"XDG_RUNTIME_DIR": runtimeDir}, "unix:path=" + filepath.Join(runtimeDir, "bus")},
		{"autolaunch: starts a bus rather than naming one, so the runtime directory's socket is used", "session", map[string]string{"DBUS_SESSION_BUS_ADDRESS": "autolaunch:", "XDG_RUNTIME_DIR": runtimeDir}, "unix:path=" + filepath.Join(runtimeDir, "bus")},
		{"a runtime directory with no bus socket gives no session bus, and none is started", "session", map[string]string{"XDG_RUNTIME_DIR": emptyDir}, ""},
		{"an empty environment gives no session bus", "session", map[string]string{}, ""},
		{"DBUS_SYSTEM_BUS_ADDRESS names the system bus", "system", map[string]string{"DBUS_SYSTEM_BUS_ADDRESS": "unix:path=/tmp/system_bus_socket"}, "unix:path=/tmp/system_bus_socket"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(k string) string { return tc.env[k] }
			find := sessionBusAddress
			if tc.bus == "system" {
				find = systemBusAddress
			}
			got, err := find(getenv)
			if tc.want == "" {
				if err == nil {
					t.Fatalf("address %q, want none", got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("address %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
