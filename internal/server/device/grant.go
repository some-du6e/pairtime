// SPDX-License-Identifier: AGPL-3.0-or-later

package device

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/devtoken"
	"github.com/some-du6e/pairtime/internal/server/accounts"
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
	// RedeemGrace is how long after it expires a code answered in time
	// still gets its answer: an approved code can still be redeemed, and a
	// denied or stopped one still gets ErrDenied or ErrTaken. So an answer
	// in the code's last seconds isn't lost to the wait before the app's
	// next poll, which a network error can stretch to a minute.
	RedeemGrace = time.Minute
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
	// statusTaken is a code that an account other than the one its device
	// id belongs to tried to approve.
	statusTaken    = "taken"
	statusRedeemed = "redeemed"
)

// The outcomes of a poll other than a token, RFC 8628 3.5's error codes and
// one of this server's.
var (
	ErrPending  = errors.New("device: authorization_pending")
	ErrSlowDown = errors.New("device: slow_down")
	ErrDenied   = errors.New("device: access_denied")
	ErrExpired  = errors.New("device: expired_token")
	// ErrTaken means an account other than the one the code's device id
	// belongs to tried to approve the code. The id stays with its account,
	// so the app has to sign in as a new device, with a new device id and
	// key (D-device-account-binding).
	ErrTaken = errors.New("device: device_taken")
	// ErrInvalidGrant means no such device code, or its token was handed
	// out already.
	ErrInvalidGrant = errors.New("device: invalid_grant")
)

// The errors of the /device page's lookups and decisions.
var (
	// ErrNoCode means no live code has the user code: never issued,
	// expired, already decided, or from before a restart.
	ErrNoCode = errors.New("device: no code is waiting under that user code")
	// ErrOtherAccount means the code's device id belongs to another
	// account, which keeps it (docs/spec/device-auth-v1.md section 1). An
	// approval or a Stop then ends the code, and the app gets ErrTaken.
	ErrOtherAccount = errors.New("device: the device is linked to another account")
)

// A Decision is an answer to a code on the /device page.
type Decision int

// The answers the /device page offers.
const (
	// Approve links the code's device to the account that answers,
	// unless its device id belongs to another account.
	Approve Decision = iota + 1
	// Deny ends the code, and the app gets ErrDenied.
	Deny
	// Stop is the one answer the page offers for a code whose device id
	// belongs to another account. It never links anything.
	Stop
)

// Request is what an app sends with its device authorization request.
type Request struct {
	// DeviceID is the app's ledger chain id.
	DeviceID string
	// OS is one of the OS constants.
	OS string
	// ClientBuild is the app's version.
	ClientBuild string
	// PublicKey is the device key, which the app has shown it holds. The
	// token the code yields works only with it (D-key-at-grant).
	PublicKey ed25519.PublicKey
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
	// Owner is the account the device id belongs to, or "" when it is new.
	Owner string
	// Linked is set when the device is linked now, not removed, so
	// approving the code links it again and signs out the copy of pairtime
	// that has its token. LinkedAt and LastSeenAt are when that token was
	// handed out and last used.
	Linked               bool
	LinkedAt, LastSeenAt time.Time
}

// Token is what a redeemed device code gets.
type Token struct {
	// AccessToken is the device's ptd_ token.
	AccessToken string
	DeviceID    string
	// PublicKey is the device key the code named, the only key the token
	// works with.
	PublicKey ed25519.PublicKey
	// SlackID and HCASub name the account that approved the code, so the
	// app can say whose account it is linked to: someone who got hold of
	// a teen's code and approved it would otherwise get the teen's
	// uploads unnoticed (RFC 8628 5.4).
	SlackID, HCASub string
}

// maxCodeTries bounds how many user codes NewCode draws before giving up,
// which happens only if nearly every code is taken.
const maxCodeTries = 8

// NewCode records a new device authorization for r and returns its codes.
// It checks nothing about r but its key's length; the HTTP handler does
// the rest, the key's signature included.
func (s *Store) NewCode(ctx context.Context, r Request) (Code, error) {
	if len(r.PublicKey) != ed25519.PublicKeySize {
		return Code{}, fmt.Errorf("device: a public key is %d bytes, not %d", ed25519.PublicKeySize, len(r.PublicKey))
	}
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
(device_code_hash, user_code_hash, run_id, device_id, os_family, client_build, public_key, status, user_id, created_at_ms, expires_at_ms, interval_s, last_poll_at_ms, decided_at_ms)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULL, $9, $10, $11, NULL, NULL)`,
			codeHash, s.userCodeHash(user), s.run, r.DeviceID, r.OS, r.ClientBuild, []byte(r.PublicKey), statusPending,
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
// NormalizeUserCode, for the /device page to show, with whom its device
// id belongs to and whether that device is linked now. It returns
// ErrNoCode when no code is waiting under it.
func (s *Store) Lookup(ctx context.Context, userCode string) (Pending, error) {
	var (
		p           Pending
		status, run string
		expiresMs   int64
	)
	err := s.db.Read.QueryRowContext(ctx, `SELECT device_id, os_family, client_build, status, run_id, expires_at_ms
FROM device_codes WHERE user_code_hash = $1`, s.userCodeHash(userCode)).Scan(&p.DeviceID, &p.OS, &p.ClientBuild, &status, &run, &expiresMs)
	if errors.Is(err, sql.ErrNoRows) {
		return Pending{}, ErrNoCode
	}
	if err != nil {
		return Pending{}, fmt.Errorf("device: looking up a code: %w", err)
	}
	if status != statusPending || !s.live(run, expiresMs) {
		return Pending{}, ErrNoCode
	}
	p.UserCode = FormatUserCode(userCode)
	p.ExpiresAt = time.UnixMilli(expiresMs)
	var removed, linked, seen sql.NullInt64
	err = s.db.Read.QueryRowContext(ctx, `SELECT user_id, removed_at_ms, linked_at_ms, last_seen_at_ms FROM devices WHERE device_id = $1`,
		p.DeviceID).Scan(&p.Owner, &removed, &linked, &seen)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return Pending{}, fmt.Errorf("device: looking up the code's device: %w", err)
	case !removed.Valid:
		p.Linked, p.LinkedAt, p.LastSeenAt = true, time.UnixMilli(linked.Int64), time.UnixMilli(seen.Int64)
	}
	return p, nil
}

// live reports whether a code that run made and that expires at expiresMs
// still works: this Store made it, since another one's user-code key is
// gone, and it hasn't expired.
func (s *Store) live(run string, expiresMs int64) bool {
	return run == s.run && s.now().UnixMilli() < expiresMs
}

// Decide records account's answer d to the code the teen typed. It
// returns ErrNoCode when no code is waiting under it any more.
//
// Decide checks again whether the code's device id belongs to another
// account, since that can change after the page was shown. If it does, an
// approval links nothing: like a Stop, it ends the code as taken, so the
// app hears at once that it has to sign in as a new device, and Decide
// returns ErrOtherAccount. A Stop never links anything. For an id no other
// account has any more, as when that account removed the device after the
// page was shown, it ends the code as denied, and the app gets ErrDenied.
//
// The same answer again from the same account, as from a double click,
// gets the first one's outcome.
func (s *Store) Decide(ctx context.Context, userCode, account string, d Decision) error {
	if d != Approve && d != Deny && d != Stop {
		return fmt.Errorf("device: %d isn't a decision", d)
	}
	nowMs := s.now().UnixMilli()
	var outcome error
	err := s.w.Do(ctx, 1, func(ctx context.Context, tx writer.Tx) error {
		outcome = nil
		status := statusDenied
		if d == Approve {
			status = statusApproved
		}
		var (
			codeHash, deviceID, current, run string
			decidedBy                        sql.NullString
			expiresMs                        int64
		)
		err := tx.QueryRowContext(ctx, `SELECT device_code_hash, device_id, status, user_id, run_id, expires_at_ms
FROM device_codes WHERE user_code_hash = $1`, s.userCodeHash(userCode)).Scan(&codeHash, &deviceID, &current, &decidedBy, &run, &expiresMs)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			outcome = ErrNoCode
			return nil
		case err != nil:
			return fmt.Errorf("device: looking up a code: %w", err)
		case run == s.run && current != statusPending && decidedBy.Valid && decidedBy.String == account:
			outcome = repeated(current, d)
			return nil
		case current != statusPending || !s.live(run, expiresMs):
			outcome = ErrNoCode
			return nil
		}
		if d != Deny {
			owner, err := ownerOf(ctx, tx, deviceID)
			if err != nil {
				return err
			}
			if owner != "" && owner != account {
				status, outcome = statusTaken, ErrOtherAccount
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

// repeated is Decide's outcome when an account answers d to a code it
// answered already, which is now in the state current: the first answer's
// outcome when d is the same answer, and ErrNoCode when the account changed
// its mind, since a code takes one answer. A Stop ends a code as taken or
// as denied, so a second Stop gets whichever the first one did, and never
// an approval.
func repeated(current string, d Decision) error {
	switch {
	case d == Approve && (current == statusApproved || current == statusRedeemed):
		return nil
	case (d == Approve || d == Stop) && current == statusTaken:
		return ErrOtherAccount
	case (d == Deny || d == Stop) && current == statusDenied:
		return nil
	}
	return ErrNoCode
}

// ownerOf returns the account the device id belongs to, or "". A removed
// device's row, which stays only while ingest holds something from it,
// keeps the id with its account.
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
// ErrDenied, ErrTaken, ErrExpired or ErrInvalidGrant. A poll sooner than
// the code's interval after the last one, less PollSlack, gets ErrSlowDown
// and raises the code's interval by SlowDownStep for every poll after it;
// interval returns the interval in force after this poll. A code answered
// in time keeps its answer for RedeemGrace after it expires: an approved
// one can still be redeemed, and a denied or stopped one still gets
// ErrDenied or ErrTaken rather than ErrExpired.
//
// Redeeming an approved code links its device with the key the code named.
// A new device id gets a row at link generation 1. A device its account
// linked before, removed or not, is linked again: a new token, the code's
// key, and the next link generation, while ingest's state for it stays
// (D-local-gap). Any token it had stops working. An account Hack Club Auth
// has marked ineligible since it approved links nothing, and the app gets
// ErrDenied.
func (s *Store) Poll(ctx context.Context, deviceCode string) (tok Token, interval time.Duration, err error) {
	now := s.now()
	nowMs := now.UnixMilli()
	codeHash := tokenHash(deviceCode)
	token := devtoken.New()
	var (
		outcome         error
		linked          string
		key             []byte
		slackID, hcaSub sql.NullString
	)
	err = s.w.Do(ctx, 2, func(ctx context.Context, tx writer.Tx) error {
		outcome, interval, linked, key = nil, 0, "", nil
		slackID, hcaSub = sql.NullString{}, sql.NullString{}
		var (
			deviceID, osFamily, build, status, run string
			pub                                    []byte
			account                                sql.NullString
			expiresMs, intervalS                   int64
			lastPoll                               sql.NullInt64
		)
		err := tx.QueryRowContext(ctx, `SELECT device_id, os_family, client_build, public_key, status, user_id, run_id, expires_at_ms, interval_s, last_poll_at_ms
FROM device_codes WHERE device_code_hash = $1`, codeHash).Scan(&deviceID, &osFamily, &build, &pub, &status, &account, &run, &expiresMs, &intervalS, &lastPoll)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			outcome = ErrInvalidGrant
			return nil
		case err != nil:
			return fmt.Errorf("device: looking up a device code: %w", err)
		}
		interval = time.Duration(intervalS) * time.Second
		// A code that was answered keeps its answer for RedeemGrace past
		// its expiry; a pending one ends on time.
		endsMs := expiresMs
		if status != statusPending {
			endsMs += RedeemGrace.Milliseconds()
		}
		switch {
		case status == statusRedeemed:
			outcome = ErrInvalidGrant
			return nil
		case run != s.run:
			// Made before a restart, whose user-code key is gone.
			outcome = ErrExpired
			return nil
		case nowMs >= endsMs:
			outcome = ErrExpired
			return nil
		case status == statusApproved:
			// Redeemed below.
		case status == statusDenied:
			outcome = ErrDenied
			return nil
		case status == statusTaken:
			outcome = ErrTaken
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
		default:
			return fmt.Errorf("device: a device code is in the state %q", status)
		}
		if !account.Valid || len(pub) != ed25519.PublicKeySize {
			return errors.New("device: an approved device code names no account or no key")
		}
		var verification string
		if err := tx.QueryRowContext(ctx, `SELECT slack_id, hca_sub, verification_status FROM users WHERE id = $1`, account.String).Scan(&slackID, &hcaSub, &verification); err != nil {
			return fmt.Errorf("device: reading the approving account: %w", err)
		}
		if accounts.VerificationStatus(verification) == accounts.Ineligible {
			// Hack Club Auth marked the account ineligible after it
			// approved, and sign-in has removed its devices since; this one
			// isn't linked either (D-device-account-binding).
			if _, err := tx.ExecContext(ctx, `UPDATE device_codes SET status = $2 WHERE device_code_hash = $1`, codeHash, statusDenied); err != nil {
				return fmt.Errorf("device: refusing the code: %w", err)
			}
			outcome = ErrDenied
			return nil
		}
		owner, err := ownerOf(ctx, tx, deviceID)
		if err != nil {
			return err
		}
		switch owner {
		case "":
			if _, err := tx.ExecContext(ctx, `INSERT INTO devices
(device_id, user_id, token_hash, public_key, os_family, client_build, linked_at_ms, last_seen_at_ms, removed_at_ms, link_generation,
ingest_req, ingest_head_b, ingest_head_hash, ingest_head_generation, ingest_head_received_at_ms, ingest_chain_start, ingest_gaps)
VALUES ($1, $2, $3, $4, $5, $6, $7, $7, NULL, 1, NULL, NULL, NULL, NULL, NULL, NULL, NULL)`,
				deviceID, account.String, tokenHash(token), pub, osFamily, build, nowMs); err != nil {
				return fmt.Errorf("device: linking the device: %w", err)
			}
		case account.String:
			if _, err := tx.ExecContext(ctx, `UPDATE devices
SET token_hash = $2, public_key = $3, os_family = $4, client_build = $5, linked_at_ms = $6, last_seen_at_ms = $6,
removed_at_ms = NULL, link_generation = link_generation + 1
WHERE device_id = $1`,
				deviceID, tokenHash(token), pub, osFamily, build, nowMs); err != nil {
				return fmt.Errorf("device: linking the device again: %w", err)
			}
		default:
			// The device id came to belong to another account between the
			// approval and this poll.
			if _, err := tx.ExecContext(ctx, `UPDATE device_codes SET status = $2 WHERE device_code_hash = $1`, codeHash, statusTaken); err != nil {
				return fmt.Errorf("device: refusing the code: %w", err)
			}
			outcome = ErrTaken
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE device_codes SET status = $2, last_poll_at_ms = $3 WHERE device_code_hash = $1`,
			codeHash, statusRedeemed, nowMs); err != nil {
			return fmt.Errorf("device: redeeming the code: %w", err)
		}
		linked, key = deviceID, pub
		return nil
	})
	if err != nil {
		return Token{}, 0, err
	}
	if outcome != nil {
		return Token{}, interval, outcome
	}
	return Token{AccessToken: token, DeviceID: linked, PublicKey: ed25519.PublicKey(key), SlackID: slackID.String, HCASub: hcaSub.String}, interval, nil
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
