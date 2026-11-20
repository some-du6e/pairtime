// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package linux

// The D-Bus helpers the package shares: finding the session and system
// buses, and connecting to one without waiting past a deadline. Mutter and
// the Secret Service are on the session bus, logind on the system bus.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/user"
	"strings"

	"github.com/godbus/dbus/v5"
)

// sessionBusAddress returns the address of the session bus:
// DBUS_SESSION_BUS_ADDRESS unless it is empty or "autolaunch:", else the
// socket systemd's user manager keeps at /run/user/<uid>/bus. It never
// starts a bus. dbus.ConnectSessionBus runs dbus-launch when it finds
// none, and the bus that starts outlives the agent.
//
// Mutter's idle time dials the address itself. The Secret Service can't:
// go-keyring connects through dbus.SessionBus, which looks for the bus on
// its own and runs dbus-launch when it finds none. So this looks where
// godbus v5.2.4 looks (getSessionBusAddress and
// tryDiscoverDbusSessionBusAddress), and SecretService asks go-keyring
// nothing when it finds no bus here. Two things follow from that:
//
//   - uid is os/user.Current's Uid, as godbus has it, not os.Getuid. Without
//     cgo, Current fails for a user /etc/passwd doesn't list when $USER or
//     $HOME is unset, and godbus then looks in no /run/user directory at
//     all. Current caches its answer for the life of the process, so
//     godbus gets the same one.
//   - $XDG_RUNTIME_DIR isn't read, because godbus doesn't read it. Where
//     systemd or elogind set it, it is /run/user/<uid> anyway.
//
// It finds no bus in some places godbus would, and so errs toward the key
// file: a /run/user/<uid>/bus that isn't a socket, and
// /run/user/<uid>/dbus-session, which Ubuntu 16.04 wrote and later
// releases don't.
func sessionBusAddress(getenv func(string) string, uid func() (string, error), isSocket func(string) bool) (string, error) {
	if a := getenv("DBUS_SESSION_BUS_ADDRESS"); a != "" && a != "autolaunch:" {
		return a, nil
	}
	id, err := uid()
	if err != nil {
		return "", fmt.Errorf("no session bus: DBUS_SESSION_BUS_ADDRESS is unset, and without the user's id there is no /run/user directory to look in: %w", err)
	}
	path := "/run/user/" + id + "/bus"
	if !isSocket(path) {
		return "", fmt.Errorf("no session bus: DBUS_SESSION_BUS_ADDRESS is unset and %s isn't a socket", path)
	}
	return "unix:path=" + dbus.EscapeBusAddressValue(path), nil
}

// currentUID is this user's id as os/user.Current gives it, which is how
// godbus finds /run/user/<uid>.
func currentUID() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	return u.Uid, nil
}

// isSocket reports whether path is a Unix socket.
func isSocket(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().Type() == fs.ModeSocket
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
// return. dialBus bounds the dial by ctx. opts go to godbus's connection,
// as Dial's do.
func connectBus(ctx context.Context, address string, opts ...dbus.ConnOption) (*dbus.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn, err := dialBus(ctx, address, opts...)
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

// dialBus opens a connection to the bus at address before ctx ends,
// without authenticating, as godbus's Dial does. An address may list
// several, separated by semicolons, which are tried in turn, as godbus
// tries them. Dial takes no context, and its tcp and nonce-tcp transports
// call net.Dial, which waits as long as the kernel resends a connection
// request that gets no answer, about two minutes on Linux, for a host that
// drops it. So those two are dialed here with ctx, and the socket is handed
// to godbus. Every other transport is godbus's own: a Unix socket connects
// or fails at once.
func dialBus(ctx context.Context, address string, opts ...dbus.ConnOption) (*dbus.Conn, error) {
	var err error
	for _, a := range strings.Split(address, ";") {
		if ctx.Err() != nil {
			break
		}
		var conn *dbus.Conn
		if conn, err = dialBusAddress(ctx, a, opts); err == nil {
			return conn, nil
		}
		// A dial that gave up on its deadline reports an i/o timeout. That
		// deadline is ctx's: the socket's write deadline is set from it,
		// and net.Dialer has no timeout of its own here. But the socket's
		// deadline and ctx's timer are separate, so the dial can report the
		// timeout a moment before ctx is marked done. Wait for ctx to
		// settle, so the caller gets its error and not the i/o timeout.
		if _, hasDeadline := ctx.Deadline(); hasDeadline && os.IsTimeout(err) {
			<-ctx.Done()
			break
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return nil, err
}

// dialBusAddress dials one bus address, as dialBus describes.
func dialBusAddress(ctx context.Context, address string, opts []dbus.ConnOption) (*dbus.Conn, error) {
	transport, keys, _ := strings.Cut(address, ":")
	if transport != "tcp" && transport != "nonce-tcp" {
		return dbus.Dial(address, opts...)
	}
	// The checks and the messages are godbus v5.2.4's (transport_tcp.go,
	// transport_nonce_tcp.go).
	host, port := busAddressKey(keys, "host"), busAddressKey(keys, "port")
	noncefile := busAddressKey(keys, "noncefile")
	switch {
	case transport == "tcp" && (host == "" || port == ""):
		return nil, errors.New("dbus: unsupported address (must set host and port)")
	case transport == "nonce-tcp" && (host == "" || port == "" || noncefile == ""):
		return nil, errors.New("dbus: unsupported address (must set host, port and noncefile)")
	}
	network := "tcp"
	switch busAddressKey(keys, "family") {
	case "":
	case "ipv4":
		network = "tcp4"
	case "ipv6":
		network = "tcp6"
	default:
		return nil, errors.New("dbus: invalid tcp family (must be ipv4 or ipv6)")
	}
	var d net.Dialer
	c, err := d.DialContext(ctx, network, net.JoinHostPort(host, port))
	if err != nil {
		return nil, err
	}
	if transport == "nonce-tcp" {
		// The nonce goes first, before authentication.
		nonce, err := os.ReadFile(noncefile)
		if err == nil {
			stop := context.AfterFunc(ctx, func() { c.Close() })
			_, err = c.Write(nonce)
			if !stop() {
				err = ctx.Err()
			}
		}
		if err != nil {
			c.Close()
			return nil, err
		}
	}
	conn, err := dbus.NewConn(c, opts...)
	if err != nil {
		c.Close()
		return nil, err
	}
	return conn, nil
}

// busAddressKey returns the value of key in a bus address's
// comma-separated key=value list, unescaped, or "" when it isn't there or
// can't be unescaped, as godbus's getKey reads it.
func busAddressKey(keys, key string) string {
	for _, kv := range strings.Split(keys, ",") {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			val, err := dbus.UnescapeBusAddressValue(v)
			if err != nil {
				return ""
			}
			return val
		}
	}
	return ""
}
