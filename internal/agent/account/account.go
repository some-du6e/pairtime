// SPDX-License-Identifier: AGPL-3.0-or-later

// Package account links this computer to a pairtime server: `pairtime
// login` and `pairtime logout` (WP-SV3; DESIGN 3.2, 3.10;
// docs/spec/device-auth-v1.md).
//
// Login runs the OAuth 2.0 device grant of RFC 8628 against the server:
// it asks for a code, shows it, and polls at the interval the server
// gives, slower after each slow_down, until the teen approves on the
// server's /device page in a browser. The ptd_ device token it gets and the
// seed of a new Ed25519 device key go in the device keystore (package
// keys), the public key is registered with the server, account.json in
// the data directory records which server and device id they belong to,
// and config.toml switches to mode "online". The computer never holds a
// Hack Club Auth token: the teen signs in to HCA on the server's pages.
//
// The device id is the install's own ledger chain id, so a local-only
// install's history stays its own when it goes online. The ledger writer
// records it in keystore.json when it starts its chain.
//
// Uploads use Open, which gives the credentials only while account.json
// and the keystore both have them, and only for the server account.json
// names, so a token never goes to a server it didn't come from.
//
// The pairtime binary links this package into every hook process, so it
// does no work at init.
package account

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/devtoken"
)

// FileName is the account file in the data directory.
const FileName = "account.json"

// fileVersion is account.json's format.
const fileVersion = 1

// ErrNotLinked means this install isn't linked to a server: there is no
// account.json, or the keystore has no sign-in.
var ErrNotLinked = errors.New("account: this computer isn't linked to a pairtime server")

// Linked says which server this install is linked to. It holds no secret:
// the token and the device key are in the keystore.
type Linked struct {
	V int `json:"v"`
	// Server is the server's base URL, as config.CheckServerURL writes it.
	Server string `json:"server"`
	// DeviceID is the install's ledger chain id, the device's id on the
	// server.
	DeviceID string `json:"device_id"`
	// PublicKey is the registered Ed25519 public key, in standard base64.
	PublicKey string    `json:"public_key"`
	LinkedAt  time.Time `json:"linked_at"`
}

// File is account.json in the data directory dataDir.
func File(dataDir string) string { return filepath.Join(dataDir, FileName) }

// Load reads account.json from the data directory dataDir. Its error
// wraps ErrNotLinked and fs.ErrNotExist when there is none.
func Load(dataDir string) (Linked, error) {
	data, err := os.ReadFile(File(dataDir))
	if errors.Is(err, fs.ErrNotExist) {
		return Linked{}, fmt.Errorf("%w: %w", ErrNotLinked, err)
	}
	if err != nil {
		return Linked{}, err
	}
	var l Linked
	if err := json.Unmarshal(data, &l); err != nil {
		return Linked{}, fmt.Errorf("account: %s isn't valid JSON: %v", FileName, err)
	}
	if l.V != fileVersion || l.Server == "" || l.DeviceID == "" {
		return Linked{}, fmt.Errorf("account: %s isn't one this pairtime wrote", FileName)
	}
	return l, nil
}

// save writes account.json, readable by this user only.
func save(dataDir string, l Linked) error {
	l.V = fileVersion
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	return home.WritePrivate(File(dataDir), append(data, '\n'))
}

// remove deletes account.json. A missing file is no error.
func remove(dataDir string) error {
	err := os.Remove(File(dataDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Secrets is where the device's sign-in is kept: the part of
// *keys.Keystore this package uses.
type Secrets interface {
	SetAccount(token, seed []byte) error
	Account() (token, seed []byte, err error)
	ForgetAccount() error
}

// Credentials are what a linked install uploads with (DESIGN 4.5).
type Credentials struct {
	Linked
	// Token is the ptd_ device token, for Authorization: Bearer.
	Token string
	// Key is the device key, which signs every upload.
	Key ed25519.PrivateKey
}

// Format writes a placeholder whatever the verb, so printing or logging
// Credentials never shows the token or the key.
func (c Credentials) Format(f fmt.State, _ rune) {
	fmt.Fprintf(f, "account.Credentials{server %s, device %s, redacted}", c.Server, c.DeviceID)
}

// Open returns the install's credentials from account.json in dataDir and
// from s. Its error wraps ErrNotLinked when either has nothing, as after
// pairtime logout, so the uploader sends nothing.
func Open(dataDir string, s Secrets) (Credentials, error) {
	l, err := Load(dataDir)
	if err != nil {
		return Credentials{}, err
	}
	token, seed, err := s.Account()
	if err != nil {
		if isNotFound(err) {
			return Credentials{}, fmt.Errorf("%w: the keystore has no sign-in for it", ErrNotLinked)
		}
		return Credentials{}, err
	}
	defer clear(seed)
	if err := devtoken.Check(string(token)); err != nil {
		return Credentials{}, fmt.Errorf("account: the keystore's device token: %w", err)
	}
	key := ed25519.NewKeyFromSeed(seed)
	if pub := key.Public().(ed25519.PublicKey); l.PublicKey != "" && encodeKey(pub) != l.PublicKey {
		return Credentials{}, fmt.Errorf("account: the keystore's device key isn't the one %s says was registered", FileName)
	}
	return Credentials{Linked: l, Token: string(token), Key: key}, nil
}
