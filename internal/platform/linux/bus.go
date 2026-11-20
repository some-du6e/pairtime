// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package linux

// The D-Bus helpers the package shares: finding the session and system
// buses, and connecting to one without waiting past a deadline. Mutter is
// on the session bus, logind on the system bus.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/godbus/dbus/v5"
)

// sessionBusAddress is the session bus the desktop gave its programs:
// DBUS_SESSION_BUS_ADDRESS, else the socket systemd's user manager keeps
// at $XDG_RUNTIME_DIR/bus. Unlike dbus.ConnectSessionBus, it never runs
// dbus-launch to start a bus of its own when there is none.
func sessionBusAddress(getenv func(string) string) (string, error) {
	if a := getenv("DBUS_SESSION_BUS_ADDRESS"); a != "" && a != "autolaunch:" {
		return a, nil
	}
	if dir := getenv("XDG_RUNTIME_DIR"); dir != "" {
		path := filepath.Join(dir, "bus")
		if fi, err := os.Stat(path); err == nil && fi.Mode()&os.ModeSocket != 0 {
			return "unix:path=" + path, nil
		}
	}
	return "", errors.New("no session bus: DBUS_SESSION_BUS_ADDRESS is unset and $XDG_RUNTIME_DIR/bus doesn't exist")
}

// systemBusAddress is where the system bus listens, found as godbus finds
// it (conn_unix.go, getSystemBusPlatformAddress): DBUS_SYSTEM_BUS_ADDRESS,
// else the first of the two standard sockets that exists.
func systemBusAddress(getenv func(string) string) (string, error) {
	if a := getenv("DBUS_SYSTEM_BUS_ADDRESS"); a != "" {
		return a, nil
	}
	for _, path := range []string{"/var/run/dbus/system_bus_socket", "/run/dbus/system_bus_socket"} {
		if _, err := os.Stat(path); err == nil {
			return "unix:path=" + path, nil
		}
	}
	return "", errors.New("no system bus: DBUS_SYSTEM_BUS_ADDRESS is unset and there is no system bus socket")
}

// connectBus opens a private connection to the bus at address: dial,
// authenticate as this user, and say hello, all before ctx ends. godbus's
// Auth and Hello take no context and wait for as long as the bus stays
// silent, so the connection is closed when ctx ends, which makes them
// return. Dial doesn't wait: connecting to a Unix socket succeeds or fails
// at once.
func connectBus(ctx context.Context, address string) (*dbus.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn, err := dbus.Dial(address)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	err = conn.Auth(nil)
	if err == nil {
		err = conn.Hello()
	}
	if !stop() {
		conn.Close()
		return nil, fmt.Errorf("the bus didn't answer in time: %w", ctx.Err())
	}
	if err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}
