// SPDX-License-Identifier: AGPL-3.0-or-later

package device

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/devtoken"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// The device grant's timing (RFC 8628 3.2, 3.5).
const (
	// CodeLifetime is how long a device code and its user code work.
	CodeLifetime = 15 * time.Minute
	// Interval is how long the app waits between polls at first.
	Interval = 5 * time.Second
	// SlowDownStep is what each slow_down adds to a code's interval.
	SlowDownStep = 5 * time.Second
	// PollSlack is how much earlier than its interval a poll may come
	// without a slow_down, for network jitter between two requests.
	PollSlack = 500 * time.Millisecond
	// CodeGrace is how long a code's row stays after the code expires,
	// before DeleteExpiredCodes deletes it (docs/retention.json
	// server.device_codes).
	CodeGrace = 24 * time.Hour
)

// The states of a device code.
const (
	statusPending  = "pending"
	statusApproved = "approved"
	statusDenied   = "denied"
	statusRedeemed = "redeemed"
)

// The outcomes of a poll other than a token, RFC 8628 3.5's error codes.
var (
	ErrPending  = errors.New("device: authorization_pending")
	ErrSlowDown = errors.New("device: slow_down")
	ErrDenied   = errors.New("device: access_denied")
	ErrExpired  = errors.New("device: expired_token")
	// ErrInvalidGrant means no such device code, or its token was handed
	// out already.
	ErrInvalidGrant = errors.New("device: invalid_grant")
)

// The errors of the /device page's lookups and decisions.
var (
	// ErrNoCode means no live code has the user code: never issued,
	// expired, already decided, or from before a restart.
	ErrNoCode = errors.New("device: no code is waiting under that user code")
	// ErrOtherAccount means the code's device is linked to another
	// account, which keeps it (docs/spec/device-auth-v1.md section 1).
	ErrOtherAccount = errors.New("device: the device is linked to another account")
)

// Request is what an app sends with its device authorization request.
type Request struct {
	// DeviceID is the app's ledger chain id.
	DeviceID string
	// OS is one of the OS constants.
	OS string
	// ClientBuild is the app's version.
	ClientBuild string
}

// Code is a device authorization: the codes POST /oauth/device/code
// answers with.
type Code struct {
	// DeviceCode is what the app polls with. Only its hash is stored.
	DeviceCode string
	// UserCode is what the teen types, as XXXX-XXXX.
	UserCode  string
	ExpiresAt time.Time
	Interval  time.Duration
}

// Pending is a code waiting for the teen, as the /device page shows it.
type Pending struct {
	// UserCode is the code, as XXXX-XXXX.
	UserCode    string
	DeviceID    string
	OS          string
	ClientBuild string
	ExpiresAt   time.Time
	// Owner is the account the device is already linked to, or "" when
	// it is new.
	Owner string
}

// Token is what a redeemed device code gets.
type Token struct {
	// AccessToken is the device's ptd_ token.
	AccessToken string
	DeviceID    string
}

// maxCodeTries bounds how many user codes NewCode draws before giving up,
// which happens only if nearly every code is taken.
const maxCodeTries = 8

// NewCode records a new device authorization for r and returns its codes.
// It checks nothing about r; the HTTP handler does.
func (s *Store) NewCode(ctx context.Context, r Request) (Code, error) {
	now := s.now()
	c := Code{DeviceCode: newDeviceCode(), ExpiresAt: now.Add(CodeLifetime), Interval: Interval}
	codeHash := tokenHash(c.DeviceCode)
	var user string
	err := s.w.Do(ctx, 1, func(ctx context.Context, tx writer.Tx) error {
		user = ""
		for range maxCodeTries {
			u := newUserCode()
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM device_codes WHERE user_code_hash = $1`, s.userCodeHash(u)).Scan(&n); err != nil {
				return fmt.Errorf("device: checking a user code: %w", err)
			}
			if n == 0 {
				user = u
				break
			}
		}
		if user == "" {
			return errors.New("device: every user code drawn was taken")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_codes
(device_code_hash, user_code_hash, device_id, os_family, client_build, status, user_id, created_at_ms, expires_at_ms, interval_s, last_poll_at_ms, decided_at_ms)
VALUES ($1, $2, $3, $4, $5, $6, NULL, $7, $8, $9, NULL, NULL)`,
			codeHash, s.userCodeHash(user), r.DeviceID, r.OS, r.ClientBuild, statusPending,
			now.UnixMilli(), c.ExpiresAt.UnixMilli(), int64(Interval/time.Second)); err != nil {
			return fmt.Errorf("device: recording the code: %w", err)
		}
		return nil
	})
	if err != nil {
		return Code{}, err
	}
	c.UserCode = FormatUserCode(user)
	return c, nil
}

// Lookup finds the live code the teen typed, normalized by
// NormalizeUserCode, for the /device page to show. It returns ErrNoCode
// when no code is waiting under it.
func (s *Store) Lookup(ctx context.Context, userCode string) (Pending, error) {
	var (
		p                  Pending
		status             string
		created, expiresMs int64
	)
	err := s.db.Read.QueryRowContext(ctx, `SELECT device_id, os_family, client_build, status, created_at_ms, expires_at_ms
FROM device_codes WHERE user_code_hash = $1`, s.userCodeHash(userCode)).Scan(&p.DeviceID, &p.OS, &p.ClientBuild, &status, &created, &expiresMs)
	if errors.Is(err, sql.ErrNoRows) {
		return Pending{}, ErrNoCode
	}
	if err != nil {
		return Pending{}, fmt.Errorf("device: looking up a code: %w", err)
	}
	if status != statusPending || !s.live(created, expiresMs) {
		return Pending{}, ErrNoCode
	}
	p.UserCode = FormatUserCode(userCode)
	p.ExpiresAt = time.UnixMilli(expiresMs)
	err = s.db.Read.QueryRowContext(ctx, `SELECT user_id FROM devices WHERE device_id = $1`, p.DeviceID).Scan(&p.Owner)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Pending{}, fmt.Errorf("device: looking up the code's device: %w", err)
	}
	return p, nil
}

// live reports whether a code made at created and expiring at expires
// still works: it hasn't expired, and this process made it, since an
// earlier one's user-code key is gone.
func (s *Store) live(createdMs, expiresMs int64) bool {
	return s.now().UnixMilli() < expiresMs && createdMs >= s.started.UnixMilli()
}

// Decide records account's answer to the code the teen typed: approve or
// deny. It returns ErrNoCode when no code is waiting under it any more,
// and ErrOtherAccount, approving nothing, when the code's device is linked
// to another account.
func (s *Store) Decide(ctx context.Context, userCode, account string, approve bool) error {
	nowMs := s.now().UnixMilli()
	status := statusDenied
	if approve {
		status = statusApproved
	}
	var outcome error
	err := s.w.Do(ctx, 1, func(ctx context.Context, tx writer.Tx) error {
		outcome = nil
		var (
			codeHash, deviceID, current string
			created, expiresMs          int64
		)
		err := tx.QueryRowContext(ctx, `SELECT device_code_hash, device_id, status, created_at_ms, expires_at_ms
FROM device_codes WHERE user_code_hash = $1`, s.userCodeHash(userCode)).Scan(&codeHash, &deviceID, &current, &created, &expiresMs)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			outcome = ErrNoCode
			return nil
		case err != nil:
			return fmt.Errorf("device: looking up a code: %w", err)
		case current != statusPending || !s.live(created, expiresMs):
			outcome = ErrNoCode
			return nil
		}
		if approve {
			owner, err := ownerOf(ctx, tx, deviceID)
			if err != nil {
				return err
			}
			if owner != "" && owner != account {
				outcome = ErrOtherAccount
				return nil
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE device_codes SET status = $2, user_id = $3, decided_at_ms = $4
WHERE device_code_hash = $1 AND status = $5`, codeHash, status, account, nowMs, statusPending); err != nil {
			return fmt.Errorf("device: recording the decision: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return outcome
}

// ownerOf returns the account the device id is linked to, or "".
func ownerOf(ctx context.Context, tx writer.Tx, deviceID string) (string, error) {
	var owner string
	err := tx.QueryRowContext(ctx, `SELECT user_id FROM devices WHERE device_id = $1`, deviceID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("device: looking up the device: %w", err)
	}
	return owner, nil
}

// Poll answers the app's token request for deviceCode (RFC 8628 3.4, 3.5):
// the device's new token once the teen approved, or ErrPending, ErrSlowDown,
// ErrDenied, ErrExpired or ErrInvalidGrant. A poll sooner than the code's
// interval after the last one, less PollSlack, gets ErrSlowDown and raises
// the code's interval by SlowDownStep for every poll after it; interval
// returns the interval in force after this poll.
//
// Redeeming an approved code links its device: a new device gets a row,
// and a device its account linked before, removed or not, is linked again
// with the new token and no key, so the app registers the key it made for
// this login. Any token the device had stops working.
func (s *Store) Poll(ctx context.Context, deviceCode string) (tok Token, interval time.Duration, err error) {
	now := s.now()
	nowMs := now.UnixMilli()
	codeHash := tokenHash(deviceCode)
	token := devtoken.New()
	var (
		outcome error
		linked  string
	)
	err = s.w.Do(ctx, 2, func(ctx context.Context, tx writer.Tx) error {
		outcome, interval, linked = nil, 0, ""
		var (
			deviceID, osFamily, build, status string
			account                           sql.NullString
			created, expiresMs                int64
			intervalS                         int64
			lastPoll                          sql.NullInt64
		)
		err := tx.QueryRowContext(ctx, `SELECT device_id, os_family, client_build, status, user_id, created_at_ms, expires_at_ms, interval_s, last_poll_at_ms
FROM device_codes WHERE device_code_hash = $1`, codeHash).Scan(&deviceID, &osFamily, &build, &status, &account, &created, &expiresMs, &intervalS, &lastPoll)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			outcome = ErrInvalidGrant
			return nil
		case err != nil:
			return fmt.Errorf("device: looking up a device code: %w", err)
		}
		interval = time.Duration(intervalS) * time.Second
		switch {
		case status == statusRedeemed:
			outcome = ErrInvalidGrant
			return nil
		case !s.live(created, expiresMs):
			outcome = ErrExpired
			return nil
		case status == statusDenied:
			outcome = ErrDenied
			return nil
		case status == statusPending:
			if lastPoll.Valid && nowMs-lastPoll.Int64 < (interval-PollSlack).Milliseconds() {
				interval += SlowDownStep
				outcome = ErrSlowDown
			} else {
				outcome = ErrPending
			}
			if _, err := tx.ExecContext(ctx, `UPDATE device_codes SET last_poll_at_ms = $2, interval_s = $3 WHERE device_code_hash = $1`,
				codeHash, nowMs, int64(interval/time.Second)); err != nil {
				return fmt.Errorf("device: recording a poll: %w", err)
			}
			return nil
		case status != statusApproved || !account.Valid:
			return fmt.Errorf("device: a device code is in the state %q", status)
		}
		owner, err := ownerOf(ctx, tx, deviceID)
		if err != nil {
			return err
		}
		switch owner {
		case "":
			if _, err := tx.ExecContext(ctx, `INSERT INTO devices
(device_id, user_id, token_hash, public_key, os_family, client_build, created_at_ms, last_seen_at_ms, key_registered_at_ms, removed_at_ms)
VALUES ($1, $2, $3, NULL, $4, $5, $6, $6, NULL, NULL)`,
				deviceID, account.String, tokenHash(token), osFamily, build, nowMs); err != nil {
				return fmt.Errorf("device: linking the device: %w", err)
			}
		case account.String:
			if _, err := tx.ExecContext(ctx, `UPDATE devices
SET token_hash = $2, public_key = NULL, key_registered_at_ms = NULL, removed_at_ms = NULL, os_family = $3, client_build = $4, last_seen_at_ms = $5
WHERE device_id = $1`,
				deviceID, tokenHash(token), osFamily, build, nowMs); err != nil {
				return fmt.Errorf("device: linking the device again: %w", err)
			}
		default:
			// Linked to another account since the teen approved.
			if _, err := tx.ExecContext(ctx, `UPDATE device_codes SET status = $2 WHERE device_code_hash = $1`, codeHash, statusDenied); err != nil {
				return fmt.Errorf("device: refusing the code: %w", err)
			}
			outcome = ErrDenied
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE device_codes SET status = $2, last_poll_at_ms = $3 WHERE device_code_hash = $1`,
			codeHash, statusRedeemed, nowMs); err != nil {
			return fmt.Errorf("device: redeeming the code: %w", err)
		}
		linked = deviceID
		return nil
	})
	if err != nil {
		return Token{}, 0, err
	}
	if outcome != nil {
		return Token{}, interval, outcome
	}
	return Token{AccessToken: token, DeviceID: linked}, interval, nil
}

// DeleteExpiredCodes deletes the rows of codes that expired more than
// CodeGrace ago, and returns how many it deleted.
func (s *Store) DeleteExpiredCodes(ctx context.Context) (int64, error) {
	cutoff := s.now().Add(-CodeGrace).UnixMilli()
	var n int64
	err := s.w.Do(ctx, 1, func(ctx context.Context, tx writer.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM device_codes WHERE expires_at_ms <= $1`, cutoff)
		if err != nil {
			return fmt.Errorf("device: deleting old codes: %w", err)
		}
		n, err = res.RowsAffected()
		return err
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// newDeviceCode returns 32 random bytes as base64url, 43 characters.
func newDeviceCode() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// UserCodeAlphabet is RFC 8628 6.1's base-20 set: consonants only, so no
// code spells a word and none has a 0 to mistake for an O.
const UserCodeAlphabet = "BCDFGHJKLMNPQRSTVWXZ"

// UserCodeLen is how many characters a user code has, 20^8 codes in all.
const UserCodeLen = 8

// newUserCode returns a random user code, without the dash.
func newUserCode() string {
	b := make([]byte, 0, UserCodeLen)
	var buf [16]byte
	for len(b) < UserCodeLen {
		rand.Read(buf[:])
		for _, c := range buf {
			// 240 is the largest multiple of 20 a byte holds.
			if c < 240 && len(b) < UserCodeLen {
				b = append(b, UserCodeAlphabet[c%20])
			}
		}
	}
	return string(b)
}

// FormatUserCode writes a normalized user code as XXXX-XXXX.
func FormatUserCode(code string) string {
	if len(code) != UserCodeLen {
		return code
	}
	return code[:4] + "-" + code[4:]
}

// NormalizeUserCode turns what the teen typed into a user code: upper
// case, without spaces and dashes. It reports false when the rest isn't 8
// characters of UserCodeAlphabet, so a typo is caught before any lookup.
func NormalizeUserCode(typed string) (string, bool) {
	if len(typed) > 64 {
		return "", false
	}
	var b strings.Builder
	for _, r := range strings.ToUpper(typed) {
		switch {
		case r == '-' || r == ' ' || r == '\t':
		case r < 0x80 && strings.IndexByte(UserCodeAlphabet, byte(r)) >= 0:
			b.WriteRune(r)
		default:
			return "", false
		}
	}
	if b.Len() != UserCodeLen {
		return "", false
	}
	return b.String(), true
}
