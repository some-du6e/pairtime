// SPDX-License-Identifier: AGPL-3.0-or-later

// Package device links pairtime apps to accounts and finds them again
// (WP-SV3; DESIGN 3.10, 4.5; SP 3.5, 8.3). docs/spec/device-auth-v1.md is
// the wire format.
//
// An app signs in with the OAuth 2.0 Device Authorization Grant (RFC
// 8628): it asks POST /oauth/device/code for a code, the teen types the
// code on /device in a browser signed in with Hack Club Auth and approves,
// and the app, polling POST /oauth/token meanwhile, gets a ptd_ device
// token (package devtoken). It then registers the Ed25519 key that signs
// its uploads with POST /api/v1/devices/me/key. The app never sees an HCA
// token. /account/devices lists a person's devices and removes them, and a
// removed device's token stops working at once.
//
// A device's id is the app's own ledger chain id, which the app sends with
// its code request: receipts and day leaves name it (ledger-v1 5, 7), so
// the server has to use the id the records carry. The ledger writer makes
// it at random on the device, so it is an opaque key that nobody can
// recompute (AGENTS.md).
//
// Tokens and device codes are stored as SHA-256 hashes, user codes as an
// HMAC under a key that lives only in this process, and every write goes
// through the writer goroutine. Nothing here logs a token, a code or a key.
package device

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/some-du6e/pairtime/internal/devtoken"
	"github.com/some-du6e/pairtime/internal/server/accounts"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// The OS families a device can report, the values of Device.OS.
const (
	OSWindows = "windows"
	OSMacOS   = "macos"
	OSLinux   = "linux"
	OSOther   = "other"
)

// Device is a linked device, found by its bearer token.
type Device struct {
	// ID is the opaque device_id: the app's ledger chain id, made at
	// random on the device (AGENTS.md: keyed or random, never a plain
	// hash).
	ID string
	// Account is the users row id (users.id) of the account the device is
	// linked to, a UUIDv7.
	Account string
	// PublicKey is the registered device key; nil until registered.
	PublicKey ed25519.PublicKey
	// Revoked is set when the device was removed, or Hack Club Auth has
	// since marked its account ineligible. Its token no longer works.
	Revoked bool
	// OS is the OS family the app reported: one of the OS constants.
	OS string
	// ClientBuild is the pairtime version the app reported.
	ClientBuild string
	// CreatedAt is when the device was linked, LastSeenAt when its token
	// was last used (to within TouchEvery), KeyRegisteredAt when its key
	// was registered and RemovedAt when it was removed. The last two are
	// zero until then.
	CreatedAt, LastSeenAt, KeyRegisteredAt, RemovedAt time.Time

	// tokenHash is the hash of the token Authenticate found the device
	// by, so a write made for that token can check it still holds.
	tokenHash string
}

// The errors Authenticate and the device writes return.
var (
	// ErrBadToken means the token isn't a well-formed ptd_ token, or its
	// checksum doesn't match. Nothing was looked up.
	ErrBadToken = errors.New("device: not a valid device token")
	// ErrUnknownToken means no device has the token.
	ErrUnknownToken = errors.New("device: no device has this token")
	// ErrRevoked means the token's device was removed, or its account is
	// closed to sign-in. Authenticate returns the device with Revoked set
	// along with it.
	ErrRevoked = errors.New("device: the device was removed")
	// ErrKeyRegistered means the device already has another key.
	ErrKeyRegistered = errors.New("device: the device already has a different key")
	// ErrNoDevice means the account has no such device.
	ErrNoDevice = errors.New("device: no such device")
)

// TouchEvery is how stale a device's last-seen time gets before
// Authenticate writes a new one, so a device that uploads every minute
// costs a write every few minutes, not every minute.
const TouchEvery = 5 * time.Minute

// Options configure a Store.
type Options struct {
	DB     *store.DB
	Writer *writer.Writer
	// Now returns the current time. Nil means time.Now.
	Now func() time.Time
}

// Store reads and writes devices and device codes.
type Store struct {
	db  *store.DB
	w   *writer.Writer
	now func() time.Time
	// userKey keys the HMAC of user codes. It lives only in memory, so a
	// stolen copy of the database can't be searched for a live user code,
	// and a restart ends every code in progress.
	userKey []byte
	// started is when this Store was made. A code from before it was made
	// by an earlier process, whose user-code key is gone.
	started time.Time
}

// New returns a Store.
func New(o Options) (*Store, error) {
	if o.DB == nil || o.Writer == nil {
		return nil, errors.New("device: New needs a database and a writer")
	}
	s := &Store{db: o.DB, w: o.Writer, now: o.Now, userKey: make([]byte, 32)}
	if s.now == nil {
		s.now = time.Now
	}
	rand.Read(s.userKey)
	s.started = s.now()
	return s, nil
}

// tokenHash is what the database keeps of a token: its SHA-256 in hex.
func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// userCodeLabel starts the HMAC input of a user code.
const userCodeLabel = "pairtime device user code v1\x00"

// userCodeHash is what the database keeps of a normalized user code.
func (s *Store) userCodeHash(code string) string {
	m := hmac.New(sha256.New, s.userKey)
	m.Write([]byte(userCodeLabel))
	m.Write([]byte(code))
	return hex.EncodeToString(m.Sum(nil))
}

// deviceColumns are the columns scanDevice reads, from devices d joined to
// users u.
const deviceColumns = `d.device_id, d.user_id, d.token_hash, d.public_key, d.os_family, d.client_build,
d.created_at_ms, d.last_seen_at_ms, d.key_registered_at_ms, d.removed_at_ms, u.verification_status`

type scanner interface {
	Scan(dest ...any) error
}

// scanDevice reads one row of deviceColumns.
func scanDevice(row scanner) (Device, error) {
	var (
		d                   Device
		key                 []byte
		created, seen       int64
		registered, removed sql.NullInt64
		status              string
	)
	if err := row.Scan(&d.ID, &d.Account, &d.tokenHash, &key, &d.OS, &d.ClientBuild,
		&created, &seen, &registered, &removed, &status); err != nil {
		return Device{}, err
	}
	if len(key) == ed25519.PublicKeySize {
		d.PublicKey = ed25519.PublicKey(key)
	}
	d.CreatedAt = time.UnixMilli(created)
	d.LastSeenAt = time.UnixMilli(seen)
	if registered.Valid {
		d.KeyRegisteredAt = time.UnixMilli(registered.Int64)
	}
	if removed.Valid {
		d.RemovedAt = time.UnixMilli(removed.Int64)
	}
	d.Revoked = removed.Valid || accounts.VerificationStatus(status) == accounts.Ineligible
	return d, nil
}

// Authenticate checks a "ptd_..." token's checksum before any lookup, then
// finds its device. A token that fails the check gets ErrBadToken, and one
// no device has gets ErrUnknownToken. A removed device, or one whose
// account Hack Club Auth has marked ineligible, comes back with Revoked
// set and ErrRevoked, so a caller that checks either refuses it.
//
// When the device's last-seen time is older than TouchEvery, Authenticate
// records the time, through the writer. A failure to record it doesn't
// fail the authentication.
func (s *Store) Authenticate(ctx context.Context, token string) (Device, error) {
	if err := devtoken.Check(token); err != nil {
		return Device{}, fmt.Errorf("%w: %v", ErrBadToken, err)
	}
	d, err := scanDevice(s.db.Read.QueryRowContext(ctx, `SELECT `+deviceColumns+`
FROM devices d JOIN users u ON u.id = d.user_id
WHERE d.token_hash = $1`, tokenHash(token)))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Device{}, ErrUnknownToken
	case err != nil:
		return Device{}, fmt.Errorf("device: looking up a token: %w", err)
	case d.Revoked:
		return d, ErrRevoked
	}
	if now := s.now(); now.Sub(d.LastSeenAt) >= TouchEvery {
		nowMs := now.UnixMilli()
		err := s.w.Do(ctx, 1, func(ctx context.Context, tx writer.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE devices SET last_seen_at_ms = $2
WHERE device_id = $1 AND last_seen_at_ms < $2`, d.ID, nowMs)
			return err
		})
		if err == nil {
			d.LastSeenAt = time.UnixMilli(nowMs)
		}
	}
	return d, nil
}

// RegisterKey registers key as the device's Ed25519 key, once. created
// reports whether this call registered it; registering the key the device
// already has does nothing and returns false, so a retry after a lost
// answer works. A different key gets ErrKeyRegistered. The device must
// come from Authenticate and still hold the token it was found by, or the
// call gets ErrRevoked.
func (s *Store) RegisterKey(ctx context.Context, d Device, key ed25519.PublicKey) (created bool, err error) {
	if len(key) != ed25519.PublicKeySize {
		return false, fmt.Errorf("device: a public key is %d bytes, not %d", ed25519.PublicKeySize, len(key))
	}
	nowMs := s.now().UnixMilli()
	var (
		outcome error
		did     bool
	)
	err = s.w.Do(ctx, 1, func(ctx context.Context, tx writer.Tx) error {
		outcome, did = nil, false
		var (
			have    []byte
			removed sql.NullInt64
		)
		err := tx.QueryRowContext(ctx, `SELECT public_key, removed_at_ms FROM devices WHERE device_id = $1 AND token_hash = $2`,
			d.ID, d.tokenHash).Scan(&have, &removed)
		switch {
		case errors.Is(err, sql.ErrNoRows), err == nil && removed.Valid:
			// Linked again since, with another token, or removed.
			outcome = ErrRevoked
			return nil
		case err != nil:
			return fmt.Errorf("device: reading the device's key: %w", err)
		case len(have) > 0 && !hmac.Equal(have, key):
			outcome = ErrKeyRegistered
			return nil
		case len(have) > 0:
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE devices SET public_key = $3, key_registered_at_ms = $4
WHERE device_id = $1 AND token_hash = $2 AND public_key IS NULL`, d.ID, d.tokenHash, []byte(key), nowMs); err != nil {
			return fmt.Errorf("device: registering the key: %w", err)
		}
		did = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return did, outcome
}

// List returns the devices of account, the linked ones first, newest
// first within each group.
func (s *Store) List(ctx context.Context, account string) ([]Device, error) {
	rows, err := s.db.Read.QueryContext(ctx, `SELECT `+deviceColumns+`
FROM devices d JOIN users u ON u.id = d.user_id
WHERE d.user_id = $1
ORDER BY CASE WHEN d.removed_at_ms IS NULL THEN 0 ELSE 1 END, d.created_at_ms DESC, d.device_id`, account)
	if err != nil {
		return nil, fmt.Errorf("device: listing devices: %w", err)
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, fmt.Errorf("device: listing devices: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("device: listing devices: %w", err)
	}
	return out, nil
}

// Remove removes account's device id, so its token stops working at once.
// The row stays until the retention job deletes it 90 days later (DESIGN
// 4.4). Removing a device that was already removed does nothing; one the
// account doesn't have gets ErrNoDevice.
func (s *Store) Remove(ctx context.Context, account, id string) error {
	nowMs := s.now().UnixMilli()
	var found bool
	err := s.w.Do(ctx, 1, func(ctx context.Context, tx writer.Tx) error {
		found = false
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM devices WHERE device_id = $1 AND user_id = $2`, id, account).Scan(&n); err != nil {
			return fmt.Errorf("device: finding the device: %w", err)
		}
		if n == 0 {
			return nil
		}
		found = true
		if _, err := tx.ExecContext(ctx, `UPDATE devices SET removed_at_ms = $3
WHERE device_id = $1 AND user_id = $2 AND removed_at_ms IS NULL`, id, account, nowMs); err != nil {
			return fmt.Errorf("device: removing the device: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !found {
		return ErrNoDevice
	}
	return nil
}

// RemoveSelf removes the device d, which Authenticate found, for the app
// that signs out with its own token (pairtime logout).
func (s *Store) RemoveSelf(ctx context.Context, d Device) error {
	nowMs := s.now().UnixMilli()
	return s.w.Do(ctx, 1, func(ctx context.Context, tx writer.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE devices SET removed_at_ms = $3
WHERE device_id = $1 AND token_hash = $2 AND removed_at_ms IS NULL`, d.ID, d.tokenHash, nowMs); err != nil {
			return fmt.Errorf("device: removing the device: %w", err)
		}
		return nil
	})
}
