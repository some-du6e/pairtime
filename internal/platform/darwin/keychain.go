// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build darwin

package darwin

import (
	"encoding/hex"
	"errors"

	"github.com/zalando/go-keyring"
)

var (
	// ErrNotFound means the Keychain has no item for the service and
	// account.
	ErrNotFound = errors.New("keychain: no such item")
	// ErrBadItem means an item exists but doesn't hold what Set writes.
	// It never says what the item holds.
	ErrBadItem = errors.New("keychain: the item isn't in the form pairtime writes")
)

// Keychain keeps secrets as generic password items in the user's default
// keychain, normally the login keychain (DESIGN 3.6; CP 5.2). go-keyring
// does the work by running /usr/bin/security, which macOS ships, so there
// is no cgo; it hands Set's secret to security on standard input, never on
// the command line where other processes could read it.
//
// An item is found by its service and account. Secrets are stored as
// lowercase hex, as on Linux, so every item holds plain text. go-keyring
// passes them as Go strings, which can't be zeroed, so a copy of the hex
// stays in memory until the garbage collector reuses it.
type Keychain struct {
	Service string
}

// Get returns the secret saved under account. It returns ErrNotFound when
// there is none.
func (k Keychain) Get(account string) ([]byte, error) {
	s, err := keyring.Get(k.Service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		// hex's error quotes the offending character, which is part of
		// the secret, so it isn't passed on.
		return nil, ErrBadItem
	}
	return b, nil
}

// Set saves secret under account, replacing any item saved there.
func (k Keychain) Set(account string, secret []byte) error {
	return keyring.Set(k.Service, account, hex.EncodeToString(secret))
}

// Delete removes the item saved under account. It returns ErrNotFound when
// there is none.
func (k Keychain) Delete(account string) error {
	err := keyring.Delete(k.Service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return ErrNotFound
	}
	return err
}
