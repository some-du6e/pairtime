// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

// Package fakenotify is a notification server for tests. It owns the name
// org.freedesktop.Notifications on the session bus and implements the
// Desktop Notifications Specification's methods: Notify, GetCapabilities,
// GetServerInformation and CloseNotification. It shows nothing. It records
// every Notify call with the D-Bus signature the call arrived with, so a
// test can check the call against the specification's
// Notify(susssasa{sv}i) and not only against what godbus would accept.
//
// The tests that use it are *_desktop_test.go files, which platform.yml
// runs inside a private bus from dbus-run-session (AGENTS.md), so it never
// meets the desktop's real notification server. Start refuses to run when
// another process owns the name.
package fakenotify

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"sync"

	"github.com/godbus/dbus/v5"
)

const (
	busName = "org.freedesktop.Notifications"
	path    = dbus.ObjectPath("/org/freedesktop/Notifications")
	iface   = "org.freedesktop.Notifications"
)

// NotifySignature is the signature of Notify's arguments in the
// specification: app_name, replaces_id, app_icon, summary, body, actions,
// hints and expire_timeout.
const NotifySignature = "susssasa{sv}i"

// Notification is one Notify call, as the server received it.
type Notification struct {
	// Signature is the call's body signature from its message header.
	Signature     string
	AppName       string
	ReplacesID    uint32
	AppIcon       string
	Summary       string
	Body          string
	Actions       []string
	Hints         map[string]dbus.Variant
	ExpireTimeout int32
	// ID is the id the server returned.
	ID uint32
}

// Server is a running fake. Its methods are safe to call from any
// goroutine.
type Server struct {
	conn *dbus.Conn
	caps []string

	mu   sync.Mutex
	got  []Notification
	next uint32
}

// Start connects to the bus in DBUS_SESSION_BUS_ADDRESS, takes the name
// org.freedesktop.Notifications and serves until Close. GetCapabilities
// answers caps.
func Start(caps ...string) (*Server, error) {
	addr := os.Getenv("DBUS_SESSION_BUS_ADDRESS")
	if addr == "" {
		return nil, errors.New("fakenotify: DBUS_SESSION_BUS_ADDRESS isn't set")
	}
	conn, err := dbus.Connect(addr)
	if err != nil {
		return nil, fmt.Errorf("fakenotify: connecting to the session bus: %w", err)
	}
	s := &Server{conn: conn, caps: slices.Clone(caps)}
	// Export before taking the name, so no call arrives at a missing object.
	if err := conn.Export(serverObj{s}, path, iface); err != nil {
		conn.Close()
		return nil, fmt.Errorf("fakenotify: exporting %s: %w", iface, err)
	}
	reply, err := conn.RequestName(busName, dbus.NameFlagDoNotQueue)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("fakenotify: requesting %s: %w", busName, err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		conn.Close()
		return nil, fmt.Errorf("fakenotify: another process owns %s on this bus", busName)
	}
	return s, nil
}

// Notifications returns the Notify calls received so far, oldest first.
func (s *Server) Notifications() []Notification {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Notification, len(s.got))
	for i, n := range s.got {
		n.Actions = slices.Clone(n.Actions)
		n.Hints = maps.Clone(n.Hints)
		out[i] = n
	}
	return out
}

// Close gives up the name and disconnects. Releasing the name first, and
// waiting for the bus to confirm it, lets the next Start take it at once.
func (s *Server) Close() error {
	s.conn.ReleaseName(busName)
	return s.conn.Close()
}

// serverObj is the exported object. godbus fills a dbus.Message parameter
// with the call's message, and it doesn't count toward the method's
// signature.
type serverObj struct{ s *Server }

func (o serverObj) Notify(msg dbus.Message, appName string, replacesID uint32, appIcon, summary, body string,
	actions []string, hints map[string]dbus.Variant, expireTimeout int32) (uint32, *dbus.Error) {
	var sig string
	if v, ok := msg.Headers[dbus.FieldSignature]; ok {
		if s, ok := v.Value().(dbus.Signature); ok {
			sig = s.String()
		}
	}
	o.s.mu.Lock()
	defer o.s.mu.Unlock()
	o.s.next++
	o.s.got = append(o.s.got, Notification{
		Signature:     sig,
		AppName:       appName,
		ReplacesID:    replacesID,
		AppIcon:       appIcon,
		Summary:       summary,
		Body:          body,
		Actions:       actions,
		Hints:         hints,
		ExpireTimeout: expireTimeout,
		ID:            o.s.next,
	})
	return o.s.next, nil
}

func (o serverObj) GetCapabilities() ([]string, *dbus.Error) {
	return slices.Clone(o.s.caps), nil
}

func (o serverObj) GetServerInformation() (name, vendor, version, specVersion string, err *dbus.Error) {
	return "fakenotify", "pairtime", "1", "1.2", nil
}

func (o serverObj) CloseNotification(id uint32) *dbus.Error {
	return nil
}
