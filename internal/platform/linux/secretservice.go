// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package linux

import (
	"encoding/hex"
	"errors"
	"os"
	"strconv"

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
	if _, ok := sessionBusAddress(os.Getenv, fileExists, os.Getuid()); !ok {
		return ErrNoSessionBus
	}
	return nil
}

// sessionBusAddress returns the session bus address godbus connects to
// (godbus/dbus v5.2.4, getSessionBusAddress): DBUS_SESSION_BUS_ADDRESS
// unless it is empty or "autolaunch:", else the socket systemd keeps at
// /run/user/<uid>/bus. Where it reports false, godbus would run
// dbus-launch. godbus also reads /run/user/<uid>/dbus-session, which
// Ubuntu 16.04 wrote and later releases don't; a system that has only that
// file gets the key file instead.
func sessionBusAddress(getenv func(string) string, exists func(string) bool, uid int) (string, bool) {
	if a := getenv("DBUS_SESSION_BUS_ADDRESS"); a != "" && a != "autolaunch:" {
		return a, true
	}
	if bus := "/run/user/" + strconv.Itoa(uid) + "/bus"; exists(bus) {
		return "unix:path=" + bus, true
	}
	return "", false
}

func fileExists(name string) bool {
	_, err := os.Stat(name)
	return err == nil
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
