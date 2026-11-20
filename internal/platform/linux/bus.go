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
	"os"
	"os/user"

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
