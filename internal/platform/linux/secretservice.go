// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package linux

import (
	"encoding/hex"
	"errors"
	"os"
	"os/user"
	"path"
	"strings"

	"github.com/zalando/go-keyring"
)

var (
	// ErrNotFound means the Secret Service has no item for the service and
	// account.
	ErrNotFound = errors.New("secret service: no such item")
	// ErrNoSessionBus means there is no session D-Bus to find a Secret
	// Service on, as on a headless box or a bare window manager (CP 5.2).
	ErrNoSessionBus = errors.New("secret service: no D-Bus session bus")
	// ErrBadItem means an item exists but doesn't hold what Set writes.
	// It never says what the item holds.
	ErrBadItem = errors.New("secret service: the item isn't in the form pairtime writes")
)

// SecretService keeps secrets in the desktop's Secret Service, the
// freedesktop.org D-Bus API that GNOME Keyring, KWallet and KeePassXC
// provide (DESIGN 3.6; CP 5.2). go-keyring does the work over godbus, so
// there is no cgo. Items go in the default collection, normally "login",
// found by the attributes service and username, where username is the
// account given here.
//
// Secrets are stored as lowercase hex, because go-keyring labels every
// secret text/plain. go-keyring passes them as Go strings, which can't be
// zeroed, so a copy of the hex stays in memory until the garbage collector
// reuses it.
//
// A call may wait for the user: a locked collection makes the Secret
// Service ask for its password, and go-keyring waits for the answer with no
// time limit. Callers that can't wait run it with their own deadline.
type SecretService struct {
	Service string
}

// Available reports whether there is a session bus to ask, without asking
// it anything. It returns ErrNoSessionBus when there is none. Each method
// checks this first, because go-keyring would otherwise run dbus-launch,
// which starts a bus of its own that outlives the agent.
func (SecretService) Available() error {
	if _, ok := sessionBusAddress(thisSystem); !ok {
		return ErrNoSessionBus
	}
	return nil
}

// busFinder is what godbus reads to find the session bus, so tests can
// give sessionBusAddress another system.
type busFinder struct {
	getenv func(string) string
	// uid is the Uid of user.Current, which godbus reads instead of
	// os.Getuid. Without cgo, user.Current fails for a uid that
	// /etc/passwd doesn't list when $USER or $HOME is unset, as in a
	// container run under an arbitrary uid.
	uid func() (string, error)
	// exists is godbus's fileExists: true unless stat says the file
	// doesn't exist, so a file stat can't reach counts.
	exists   func(string) bool
	readFile func(string) ([]byte, error)
}

// thisSystem finds the bus the way godbus does on this system.
var thisSystem = busFinder{
	getenv: os.Getenv,
	uid: func() (string, error) {
		u, err := user.Current()
		if err != nil {
			return "", err
		}
		return u.Uid, nil
	},
	exists: func(name string) bool {
		_, err := os.Stat(name)
		return !os.IsNotExist(err)
	},
	readFile: os.ReadFile,
}

// sessionBusAddress returns the session bus address godbus connects to
// without starting a bus (godbus/dbus v5.2.4: getSessionBusAddress in
// conn.go; tryDiscoverDbusSessionBusAddress and getRuntimeDirectory in
// conn_other.go). That is DBUS_SESSION_BUS_ADDRESS unless it is empty or
// "autolaunch:"; else, in the runtime directory /run/user/<uid> with the
// uid from user.Current, the socket "bus", or the address the file
// "dbus-session" gives after "DBUS_SESSION_BUS_ADDRESS=", which Ubuntu
// 16.04 wrote. When it reports false, godbus would run dbus-launch, and so
// it does when user.Current fails, without looking in /run/user at all.
func sessionBusAddress(f busFinder) (string, bool) {
	if a := f.getenv("DBUS_SESSION_BUS_ADDRESS"); a != "" && a != "autolaunch:" {
		return a, true
	}
	uid, err := f.uid()
	if err != nil {
		return "", false
	}
	dir := path.Join("/run/user", uid)
	if bus := path.Join(dir, "bus"); f.exists(bus) {
		return "unix:path=" + bus, true
	}
	if file := path.Join(dir, "dbus-session"); f.exists(file) {
		if b, err := f.readFile(file); err == nil {
			if a, ok := strings.CutPrefix(string(b), "DBUS_SESSION_BUS_ADDRESS="); ok {
				if a = strings.TrimRight(a, "\r\n"); a != "" {
					return a, true
				}
			}
		}
	}
	return "", false
}

// Get returns the secret saved under account. It returns ErrNotFound when
// there is none.
func (s SecretService) Get(account string) ([]byte, error) {
	if err := s.Available(); err != nil {
		return nil, err
	}
	v, err := keyring.Get(s.Service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	b, err := hex.DecodeString(v)
	if err != nil {
		// hex's error quotes the offending character, which is part of
		// the secret, so it isn't passed on.
		return nil, ErrBadItem
	}
	return b, nil
}

// Set saves secret under account, replacing any item saved there.
func (s SecretService) Set(account string, secret []byte) error {
	if err := s.Available(); err != nil {
		return err
	}
	return keyring.Set(s.Service, account, hex.EncodeToString(secret))
}

// Delete removes the item saved under account. It returns ErrNotFound when
// there is none.
//
// A nil error doesn't prove the item is gone. A Secret Service may ask the
// user to allow a delete, and go-keyring v0.2.8 throws away the prompt's
// dismissed flag, so a delete the user declined also returns nil. A caller
// that must know reads the item back. KeePassXC, by default, moves a
// deleted item to its recycle bin, where reading it back can't see it.
func (s SecretService) Delete(account string) error {
	if err := s.Available(); err != nil {
		return err
	}
	err := keyring.Delete(s.Service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return ErrNotFound
	}
	return err
}
