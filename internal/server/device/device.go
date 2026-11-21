// SPDX-License-Identifier: AGPL-3.0-or-later

// Package device links pairtime apps to accounts and finds them again
// (WP-SV3; DESIGN 3.10, 4.5; SP 3.5, 8.3). docs/spec/device-auth-v1.md is
// the wire format.
//
// An app signs in with the OAuth 2.0 Device Authorization Grant (RFC
// 8628). It makes the Ed25519 key that will sign its uploads, asks POST
// /oauth/device/code for a code with the key's public half, and the teen
// types the code on /device in a browser signed in with Hack Club Auth and
// approves. The app, polling POST /oauth/token meanwhile, gets a ptd_
// device token (package devtoken) that works only with the key the code
// named, so a token alone can never add or swap a key (D-key-at-grant).
// The app never sees an HCA token. /account/devices lists a person's
// devices and removes them, and a removed device's token stops working at
// once.
//
// A device's id is the app's own ledger chain id, which the app sends with
// its code request: receipts and day leaves name it (ledger-v1 5, 7), so
// the server has to use the id the records carry. The ledger writer makes
// it at random on the device, so it is an opaque key that nobody can
// recompute (AGENTS.md). The id stays with the account that linked it for
// as long as ingest holds anything from the device; a device removed
// before it uploaded anything frees its id at once
// (D-device-account-binding).
//
// The device's row also holds ingest's state for it, the request counter
// and the chain's head (State), so that state lives exactly as long as the
// device does (AGENTS.md, Retention; D-device-row-chain-state).
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
	// PublicKey is the device key, which the app sent with the code that
	// linked it. The token works only with it (D-key-at-grant). A removed
	// device's row keeps none, so it is nil then.
	PublicKey ed25519.PublicKey
	// Revoked is set when the device was removed, or Hack Club Auth has
	// since marked its account ineligible. Its token no longer works.
	Revoked bool
	// OS is the OS family the app reported: one of the OS constants, or
	// "" for a removed device.
	OS string
	// ClientBuild is the pairtime version the app reported, or "" for a
	// removed device.
	ClientBuild string
	// Generation is the device's link generation: 1 for the link that made
	// its row, and one more each time a login linked it again with a new
	// token (D-local-gap).
	Generation int64
	// LinkedAt is when its token was handed out, LastSeenAt when the token
	// was last used (to within TouchEvery), and RemovedAt when the device
	// was removed, zero until then. A removed device's row keeps only
	// RemovedAt.
	LinkedAt, LastSeenAt, RemovedAt time.Time

	// tokenHash is the hash of the token Authenticate found the device
	// by, so a write made for that token can check it still holds.
	tokenHash string
}

// The errors Authenticate and the device writes return.
var (
	// ErrBadToken means the token isn't a well-formed ptd_ token, or its
	// checksum doesn't match. Nothing was looked up.
	ErrBadToken = errors.New("device: not a valid device token")
	// ErrUnknownToken means no device has the token, as when its device
	// was removed or linked again with a new token.
	ErrUnknownToken = errors.New("device: no device has this token")
	// ErrRevoked means the token's account is closed to sign-in, so
	// Authenticate returns the device with Revoked set along with it. From
	// LoadState and SaveState, it means the device was removed or linked
	// again with a new token since Authenticate found it.
	ErrRevoked = errors.New("device: the device was removed")
	// ErrNoDevice means the device id belongs to another account, so the
	// account has no such device to remove.
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
	// run names this Store among every one that has used the database. A
	// code another one made can't be used, since that Store's user-code
	// key is gone. Comparing names rather than times keeps a clock that
	// steps back from ending new codes.
	run string
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
	run := make([]byte, 16)
	rand.Read(run)
	s.run = hex.EncodeToString(run)
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
d.link_generation, d.linked_at_ms, d.last_seen_at_ms, d.removed_at_ms, u.verification_status`

type scanner interface {
	Scan(dest ...any) error
}

// scanDevice reads one row of deviceColumns.
func scanDevice(row scanner) (Device, error) {
	var (
		d                     Device
		token, osFamily       sql.NullString
		build                 sql.NullString
		key                   []byte
		linked, seen, removed sql.NullInt64
		status                string
	)
	if err := row.Scan(&d.ID, &d.Account, &token, &key, &osFamily, &build,
		&d.Generation, &linked, &seen, &removed, &status); err != nil {
		return Device{}, err
	}
	d.tokenHash, d.OS, d.ClientBuild = token.String, osFamily.String, build.String
	if len(key) == ed25519.PublicKeySize {
		d.PublicKey = ed25519.PublicKey(key)
	}
	for _, t := range []struct {
		at  *time.Time
		col sql.NullInt64
	}{{&d.LinkedAt, linked}, {&d.LastSeenAt, seen}, {&d.RemovedAt, removed}} {
		if t.col.Valid {
			*t.at = time.UnixMilli(t.col.Int64)
		}
	}
	d.Revoked = removed.Valid || accounts.VerificationStatus(status) == accounts.Ineligible
	return d, nil
}

// Authenticate checks a "ptd_..." token's checksum before any lookup, then
// finds its device. A token that fails the check gets ErrBadToken, and one
// no device has gets ErrUnknownToken, which is what a removed device's
// token gets too, since its row keeps no token. A device whose account
// Hack Club Auth has marked ineligible comes back with Revoked set and
// ErrRevoked, so a caller that checks either refuses it.
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
			_, err := tx.ExecContext(ctx, `UPDATE devices SET last_seen_at_ms = $3
WHERE device_id = $1 AND token_hash = $2 AND last_seen_at_ms < $3`, d.ID, d.tokenHash, nowMs)
			return err
		})
		if err == nil {
			d.LastSeenAt = time.UnixMilli(nowMs)
		}
	}
	return d, nil
}

// List returns the devices of account: the linked ones first, newest link
// first, then the removed ones, newest removal first. A removed device is
// listed while its row stays, which is only when ingest holds something
// from it.
func (s *Store) List(ctx context.Context, account string) ([]Device, error) {
	rows, err := s.db.Read.QueryContext(ctx, `SELECT `+deviceColumns+`
FROM devices d JOIN users u ON u.id = d.user_id
WHERE d.user_id = $1
ORDER BY CASE WHEN d.removed_at_ms IS NULL THEN 0 ELSE 1 END, COALESCE(d.linked_at_ms, d.removed_at_ms) DESC, d.device_id`, account)
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

// Remove removes account's device id, which stops its token at once;
// unlink says what stays of it. Removing it again does nothing, whether
// the first removal left a tombstone or deleted the row, so a second
// Remove, as from a double click, gets the first one's answer. That makes
// an id no account has a no-op too. An id that belongs to another account
// gets ErrNoDevice.
func (s *Store) Remove(ctx context.Context, account, id string) error {
	nowMs := s.now().UnixMilli()
	var other bool
	err := s.w.Do(ctx, 1, func(ctx context.Context, tx writer.Tx) error {
		owner, err := ownerOf(ctx, tx, id)
		if err != nil {
			return err
		}
		other = owner != "" && owner != account
		if owner != account {
			return nil
		}
		return unlink(ctx, tx, nowMs, `device_id = $1 AND user_id = $2`, id, account)
	})
	if err != nil {
		return err
	}
	if other {
		return ErrNoDevice
	}
	return nil
}

// RemoveSelf removes the device d, which Authenticate found, for the app
// that signs out with its own token (pairtime logout). A device whose
// account turned ineligible can remove itself too.
func (s *Store) RemoveSelf(ctx context.Context, d Device) error {
	nowMs := s.now().UnixMilli()
	return s.w.Do(ctx, 1, func(ctx context.Context, tx writer.Tx) error {
		return unlink(ctx, tx, nowMs, `device_id = $1 AND token_hash = $2`, d.ID, d.tokenHash)
	})
}

// RemoveAccount removes every device of account inside tx: package
// accounts calls it, through accounts.Options.Ineligible, in the
// transaction that marks an account ineligible and revokes its sessions,
// since such an account can't sign in to remove them
// (D-device-account-binding).
func (s *Store) RemoveAccount(ctx context.Context, tx writer.Tx, account string, now time.Time) error {
	return unlink(ctx, tx, now.UnixMilli(), `user_id = $1`, account)
}

// unlink removes the linked devices that where matches, with args for its
// placeholders, at nowMs. A device ingest holds nothing from loses its
// row, which frees its id for whichever account links it next. Any other
// becomes a tombstone: it keeps its account, its link generation and
// ingest's state and gets its removal time, and loses its token, key, OS
// family, build and times. Its device id stays with the account until the
// retention job deletes the row, once ingest holds nothing from it
// (D-device-account-binding; docs/retention.json server.devices).
func unlink(ctx context.Context, tx writer.Tx, nowMs int64, where string, args ...any) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM devices WHERE `+where+` AND removed_at_ms IS NULL AND ingest_req IS NULL`, args...); err != nil {
		return fmt.Errorf("device: removing a device: %w", err)
	}
	at := fmt.Sprintf("$%d", len(args)+1)
	if _, err := tx.ExecContext(ctx, `UPDATE devices SET removed_at_ms = `+at+`, token_hash = NULL, public_key = NULL, os_family = NULL,
client_build = NULL, linked_at_ms = NULL, last_seen_at_ms = NULL WHERE `+where+` AND removed_at_ms IS NULL`,
		append(args, nowMs)...); err != nil {
		return fmt.Errorf("device: removing a device: %w", err)
	}
	return nil
}
