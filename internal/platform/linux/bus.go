// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package linux

import (
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
