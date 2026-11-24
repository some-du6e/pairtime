// SPDX-License-Identifier: AGPL-3.0-or-later

// Package accounts keeps pairtimed's accounts and web sessions (DESIGN 3.10,
// 4.4).
//
// An account is created the first time a person signs in with Hack Club
// Auth (HCA), and hca_sub, the HCA subject, is its identity key (SP 3.3,
// 3.4). Package auth checks the ID token and hands SignIn what it said.
// Each sign-in refreshes the Slack id, the email, which is stored encrypted
// (DESIGN 4.4), and the verification status, which decides what the person
// may do (DESIGN 8.2):
//
//   - ineligible: sign-in is refused. An identity without an account gets
//     no row. An account that turns ineligible keeps its row with the new
//     status, loses its email and Slack id, and loses every session it had,
//     with the display names they kept. It keeps its email's keyed index,
//     the HMAC DESIGN 4.4 keeps so a deletion request can find it, and the
//     privacy hub's eraser finds the account by it (WP-SV13).
//   - verified: the person may submit hours. When HCA's ysws_eligible isn't
//     true, the case HCA's own API reports as verified_but_over_18, the
//     account carries the "eligibility to confirm" note for programs,
//     because HCA doesn't separate 18 from 19 (DESIGN 8.2, 13.2).
//   - needs_submission and pending: the person may sign in, not submit.
//
// An identity without an account gets one only when Options.MayCreate
// admits its HCA subject. Otherwise it is refused and nothing is written,
// so the server holds no new person's data, apart from the operators',
// until the controller is named (DESIGN 8.6). cmd/pairtimed passes
// WP-SV14's gate, pages.Signups.MayCreate, which admits everyone while
// signups_open is true and only the operators list while it is false.
//
// A session is a random token in the browser's cookie, and the database
// keeps only its SHA-256. The display name HCA sent at the sign-in, if any,
// is kept with the session and never with the account, so it goes when the
// session's row does (D-display-name; DESIGN 4.4). Every write goes through
// the writer goroutine; lookups use the read pool.
package accounts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/some-du6e/pairtime/internal/server/config"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// VerificationStatus is HCA's verification_status claim (SP 3.1).
type VerificationStatus string

// The values HCA sends.
const (
	NeedsSubmission VerificationStatus = "needs_submission"
	Pending         VerificationStatus = "pending"
	Verified        VerificationStatus = "verified"
	Ineligible      VerificationStatus = "ineligible"
)

// ParseVerificationStatus returns the status s names. Any other value is an
// error, so a status HCA adds later is refused until pairtime knows what it
// means.
func ParseVerificationStatus(s string) (VerificationStatus, error) {
	switch v := VerificationStatus(s); v {
	case NeedsSubmission, Pending, Verified, Ineligible:
		return v, nil
	}
	return "", fmt.Errorf("accounts: unknown verification_status %q", s)
}

// CheckSub checks an HCA subject, such as "ident!abc123": 1 to 255
// printable ASCII characters without spaces, the rule config applies to the
// reviewer allowlist.
func CheckSub(sub string) error {
	if sub == "" || len(sub) > 255 || strings.ContainsFunc(sub, func(c rune) bool { return c <= ' ' || c > '~' }) {
		return errors.New("accounts: an HCA subject must be 1 to 255 printable ASCII characters without spaces")
	}
	return nil
}

// Identity is what HCA's ID token said about a person.
type Identity struct {
	// Sub is the HCA subject.
	Sub string
	// SlackID is the person's Hack Club Slack user id, or "".
	SlackID string
	// Email is the person's verified email address, or "".
	Email string
	// Status is the verification_status claim.
	Status VerificationStatus
	// YSWSEligible is the ysws_eligible claim, or nil when HCA sent none.
	YSWSEligible *bool
}

// User is an account.
type User struct {
	// ID is the account's UUIDv7.
	ID      string
	HCASub  string
	SlackID string
	Status  VerificationStatus
	// YSWSEligible is what HCA last said, or nil.
	YSWSEligible *bool
	// HasEmail reports whether an encrypted email is stored. Store.Email
	// decrypts it.
	HasEmail bool
	// SignedInAt is the last accepted sign-in.
	CreatedAt, UpdatedAt, SignedInAt time.Time

	emailSealed []byte
}

// CanSubmit reports whether the person may submit hours: only a verified
// identity may (DESIGN 8.2).
func (u *User) CanSubmit() bool {
	return u.Status == Verified
}

// EligibilityToConfirm reports whether the person's submissions carry the
// "eligibility to confirm" note: HCA verified the identity but didn't say
// it is eligible for YSWS programs, which HCA's API reports as
// verified_but_over_18 (DESIGN 8.2).
func (u *User) EligibilityToConfirm() bool {
	return u.Status == Verified && (u.YSWSEligible == nil || !*u.YSWSEligible)
}

// Session is a signed-in browser.
type Session struct {
	// Token is the cookie value. Only SignIn returns it; the database keeps
	// its SHA-256.
	Token string
	User  User
	// Roles are the reviewer roles pairtime.toml gives the account's HCA
	// subject (DESIGN 10.2), in the order the file lists them.
	Roles []string
	// CreatedAt is the sign-in that opened the session.
	CreatedAt, ExpiresAt time.Time
	// AuthTime is when HCA last authenticated the person, from the ID
	// token, or zero when HCA didn't say. For an ordinary sign-in it can
	// be the start of an HCA session days old.
	AuthTime time.Time
	// FreshLoginAt is when HCA authenticated the person again for this
	// sign-in, which asked for that (?fresh=1, prompt=login) and got an
	// auth_time from after it started. It is zero for an ordinary sign-in,
	// however recent its AuthTime.
	FreshLoginAt time.Time
	// DisplayName is the Login.DisplayName of the sign-in that opened the
	// session, or "". The pages show it as who is signed in.
	DisplayName string
}

// HasRole reports whether the session's account holds role.
func (s *Session) HasRole(role string) bool {
	return slices.Contains(s.Roles, role)
}

// FreshLoginWithin reports whether the session comes from a fresh login
// made at most d before now. A submission freeze sends the browser through
// /auth/hca/login?fresh=1 and then asks this of the session it comes back
// with (DESIGN 7.5 step 2). An ordinary sign-in never counts.
func (s *Session) FreshLoginWithin(d time.Duration, now time.Time) bool {
	return !s.FreshLoginAt.IsZero() && !s.FreshLoginAt.Before(now.Add(-d))
}

// Login is how HCA authenticated the person at one sign-in.
type Login struct {
	// AuthTime is the ID token's auth_time, or zero when HCA didn't say.
	AuthTime time.Time
	// Fresh is set when the sign-in asked HCA to authenticate the person
	// again (prompt=login) and AuthTime is from after the sign-in started,
	// which package auth checks.
	Fresh bool
	// DisplayName is the name HCA sent with the sign-in, which package auth
	// picks and cleans: given_name, or else nickname, at most
	// MaxDisplayName characters. "" means none. Only the session keeps it.
	DisplayName string
}

// MaxDisplayName is the most characters a display name may have.
const MaxDisplayName = 64

// DefaultSessionLifetime is how long a web session lasts. It doesn't slide:
// after it ends, signing in again is one click while the HCA session lives.
const DefaultSessionLifetime = 30 * 24 * time.Hour

// sessionGrace is how long DeleteExpiredSessions keeps an expired or
// revoked session's row.
const sessionGrace = 24 * time.Hour

// tokenBytes is the size of a session token's random part.
const tokenBytes = 32

// The errors SignIn and Session return.
var (
	ErrIneligible    = errors.New("accounts: Hack Club Auth marks this identity ineligible")
	ErrSignupsClosed = errors.New("accounts: this identity has no account, and sign-ups are closed to it")
	ErrNoSession     = errors.New("accounts: no such session")
)

// Options configure a Store.
type Options struct {
	DB     *store.DB
	Writer *writer.Writer
	// Emails encrypts email addresses at rest.
	Emails *EmailCipher
	// Reviewers is the reviewer allowlist of pairtime.toml (DESIGN 10.2).
	Reviewers []config.Reviewer
	// MayCreate reports whether the identity sub, which has no account,
	// may create one (DESIGN 8.6). SignIn asks it inside the transaction
	// that finds no account, so it must be quick and must not write. Nil
	// means sign-ups are closed to everyone.
	MayCreate func(sub string) bool
	// Ineligible runs inside the transaction that marks an account
	// ineligible, drops its contact details and revokes its sessions,
	// with the account's id, so whatever it does lands with them.
	// pairtimed passes device.Store.RemoveAccount, which removes the
	// account's devices, since the account can't sign in to remove them
	// (D-device-account-binding). Nil does nothing more.
	Ineligible func(ctx context.Context, tx writer.Tx, account string, now time.Time) error
	// SessionLifetime is how long a session lasts. Zero means
	// DefaultSessionLifetime.
	SessionLifetime time.Duration
	// Now returns the current time. Nil means time.Now.
	Now func() time.Time
}

// Store reads and writes accounts and sessions.
type Store struct {
	db        *store.DB
	w         *writer.Writer
	emails    *EmailCipher
	roles     map[string][]string
	mayCreate func(sub string) bool
	// ineligible is Options.Ineligible.
	ineligible func(ctx context.Context, tx writer.Tx, account string, now time.Time) error
	lifetime   time.Duration
	now        func() time.Time
}

// New returns a Store.
func New(o Options) (*Store, error) {
	if o.DB == nil || o.Writer == nil || o.Emails == nil {
		return nil, errors.New("accounts: New needs a database, a writer and an email cipher")
	}
	s := &Store{
		db:         o.DB,
		w:          o.Writer,
		emails:     o.Emails,
		roles:      make(map[string][]string, len(o.Reviewers)),
		mayCreate:  o.MayCreate,
		ineligible: o.Ineligible,
		lifetime:   o.SessionLifetime,
		now:        o.Now,
	}
	for _, r := range o.Reviewers {
		s.roles[r.Sub] = slices.Clone(r.Roles)
	}
	if s.lifetime <= 0 {
		s.lifetime = DefaultSessionLifetime
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s, nil
}

// Roles returns the reviewer roles pairtime.toml gives sub.
func (s *Store) Roles(sub string) []string {
	return slices.Clone(s.roles[sub])
}

// SignIn records a sign-in by id and opens a session for it, which records
// how HCA authenticated the person. created reports whether the sign-in
// created the account.
//
// SignIn returns ErrIneligible when HCA marks id ineligible, and
// ErrSignupsClosed when id has no account and Options.MayCreate doesn't
// admit it. Neither opens a session. An existing account that turns
// ineligible gets its new status, loses its email, its Slack id, its
// sessions and their display names, and keeps nothing HCA sent this time,
// but keeps its email's keyed index so a deletion request can still find
// it; an identity without an account is never written.
func (s *Store) SignIn(ctx context.Context, id Identity, login Login) (sess *Session, created bool, err error) {
	if err := CheckSub(id.Sub); err != nil {
		return nil, false, err
	}
	if _, err := ParseVerificationStatus(string(id.Status)); err != nil {
		return nil, false, err
	}
	if utf8.RuneCountInString(login.DisplayName) > MaxDisplayName {
		return nil, false, fmt.Errorf("accounts: a display name has at most %d characters", MaxDisplayName)
	}
	var freshAt time.Time
	if login.Fresh {
		if login.AuthTime.IsZero() {
			return nil, false, errors.New("accounts: a fresh login needs the time HCA authenticated the person")
		}
		freshAt = login.AuthTime
	}
	now := s.now()
	nowMs := now.UnixMilli()
	expires := now.Add(s.lifetime)
	token, hash := newSessionToken()

	// A nil interface, not a nil slice, so every driver writes NULL.
	var sealed, index any
	if id.Email != "" && id.Status != Ineligible {
		sealed = s.emails.Seal(id.Sub, id.Email)
		index = s.emails.Index(id.Email)
	}
	slack := nullString(id.SlackID)
	eligible := nullBool(id.YSWSEligible)

	// The op writes only these locals. When ctx ends while the op runs, Do
	// returns early and the op may still be writing them, so they are read
	// only after a nil error.
	var (
		userID    string
		createdMs int64
		isNew     bool
		refused   error
	)
	err = s.w.Do(ctx, 3, func(ctx context.Context, tx writer.Tx) error {
		userID, createdMs, isNew, refused = "", 0, false, nil
		err := tx.QueryRowContext(ctx, `SELECT id, created_at_ms FROM users WHERE hca_sub = $1`, id.Sub).Scan(&userID, &createdMs)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if id.Status == Ineligible {
				refused = ErrIneligible
				return nil
			}
			if s.mayCreate == nil || !s.mayCreate(id.Sub) {
				refused = ErrSignupsClosed
				return nil
			}
			userID, createdMs, isNew = newID(now), nowMs, true
			if _, err := tx.ExecContext(ctx, `INSERT INTO users
(id, hca_sub, slack_id, email_ct, email_index, verification_status, ysws_eligible, created_at_ms, updated_at_ms, signed_in_at_ms)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8, $8)`,
				userID, id.Sub, slack, sealed, index, string(id.Status), eligible, nowMs); err != nil {
				return fmt.Errorf("accounts: creating the account: %w", err)
			}
		case err != nil:
			return fmt.Errorf("accounts: looking up the account: %w", err)
		case id.Status == Ineligible:
			// The row keeps the status and drops the contact details: an
			// identity HCA calls ineligible may be a child pairtime has no
			// basis to hold them for (DESIGN 8.2, 13.2), and it can't sign
			// in to delete them. The email's keyed index stays, an HMAC
			// that reveals no address, so the privacy hub's deletion
			// request, which names the person by email, still finds the
			// row (DESIGN 4.4, 8.9).
			if _, err := tx.ExecContext(ctx, `UPDATE users
SET slack_id = NULL, email_ct = NULL, verification_status = $2, ysws_eligible = $3, updated_at_ms = $4
WHERE id = $1`,
				userID, string(id.Status), eligible, nowMs); err != nil {
				return fmt.Errorf("accounts: updating the account: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at_ms = $2 WHERE user_id = $1 AND revoked_at_ms IS NULL`,
				userID, nowMs); err != nil {
				return fmt.Errorf("accounts: revoking the account's sessions: %w", err)
			}
			// The names the sessions kept go now as well, with the contact
			// details, and not with the rows a day later.
			if _, err := tx.ExecContext(ctx, `UPDATE sessions SET display_name = NULL WHERE user_id = $1 AND display_name IS NOT NULL`,
				userID); err != nil {
				return fmt.Errorf("accounts: dropping the display names of the account's sessions: %w", err)
			}
			if s.ineligible != nil {
				if err := s.ineligible(ctx, tx, userID, now); err != nil {
					return fmt.Errorf("accounts: closing what the account kept: %w", err)
				}
			}
			refused = ErrIneligible
			return nil
		default:
			if _, err := tx.ExecContext(ctx, `UPDATE users
SET slack_id = $2, email_ct = $3, email_index = $4, verification_status = $5, ysws_eligible = $6, updated_at_ms = $7, signed_in_at_ms = $7
WHERE id = $1`,
				userID, slack, sealed, index, string(id.Status), eligible, nowMs); err != nil {
				return fmt.Errorf("accounts: updating the account: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO sessions
(id_hash, user_id, created_at_ms, expires_at_ms, auth_time_ms, fresh_login_at_ms, display_name, revoked_at_ms)
VALUES ($1, $2, $3, $4, $5, $6, $7, NULL)`,
			hash, userID, nowMs, expires.UnixMilli(), nullTime(login.AuthTime), nullTime(freshAt), nullString(login.DisplayName)); err != nil {
			return fmt.Errorf("accounts: opening the session: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if refused != nil {
		return nil, false, refused
	}
	sess = &Session{
		Token: token,
		User: User{
			ID:           userID,
			HCASub:       id.Sub,
			SlackID:      id.SlackID,
			Status:       id.Status,
			YSWSEligible: id.YSWSEligible,
			HasEmail:     sealed != nil,
			CreatedAt:    time.UnixMilli(createdMs),
			UpdatedAt:    time.UnixMilli(nowMs),
			SignedInAt:   time.UnixMilli(nowMs),
		},
		Roles:       s.Roles(id.Sub),
		CreatedAt:   time.UnixMilli(nowMs),
		ExpiresAt:   time.UnixMilli(expires.UnixMilli()),
		DisplayName: login.DisplayName,
	}
	if b, ok := sealed.([]byte); ok {
		sess.User.emailSealed = b
	}
	if !login.AuthTime.IsZero() {
		sess.AuthTime = time.UnixMilli(login.AuthTime.UnixMilli())
	}
	if !freshAt.IsZero() {
		sess.FreshLoginAt = time.UnixMilli(freshAt.UnixMilli())
	}
	return sess, isNew, nil
}

// Session returns the live session whose cookie value is token, with its
// account. It returns ErrNoSession for a token that is malformed, unknown,
// expired or revoked, and for an account HCA has since marked ineligible.
func (s *Store) Session(ctx context.Context, token string) (*Session, error) {
	hash, ok := hashToken(token)
	if !ok {
		return nil, ErrNoSession
	}
	var (
		sess                          Session
		createdMs, expiresMs          int64
		uCreated, uUpdated, uSignedIn int64
		authTime, freshAt, eligible   sql.NullInt64
		slack, name                   sql.NullString
		sealed                        []byte
		status                        string
	)
	err := s.db.Read.QueryRowContext(ctx, `SELECT s.created_at_ms, s.expires_at_ms, s.auth_time_ms, s.fresh_login_at_ms, s.display_name,
u.id, u.hca_sub, u.slack_id, u.email_ct, u.verification_status, u.ysws_eligible,
u.created_at_ms, u.updated_at_ms, u.signed_in_at_ms
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.id_hash = $1 AND s.revoked_at_ms IS NULL AND s.expires_at_ms > $2`,
		hash, s.now().UnixMilli()).Scan(&createdMs, &expiresMs, &authTime, &freshAt, &name,
		&sess.User.ID, &sess.User.HCASub, &slack, &sealed, &status, &eligible,
		&uCreated, &uUpdated, &uSignedIn)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoSession
	}
	if err != nil {
		return nil, fmt.Errorf("accounts: reading the session: %w", err)
	}
	sess.User.Status = VerificationStatus(status)
	if sess.User.Status == Ineligible {
		return nil, ErrNoSession
	}
	sess.User.SlackID = slack.String
	if eligible.Valid {
		v := eligible.Int64 == 1
		sess.User.YSWSEligible = &v
	}
	sess.User.HasEmail = len(sealed) > 0
	sess.User.emailSealed = sealed
	sess.User.CreatedAt = time.UnixMilli(uCreated)
	sess.User.UpdatedAt = time.UnixMilli(uUpdated)
	sess.User.SignedInAt = time.UnixMilli(uSignedIn)
	sess.Roles = s.Roles(sess.User.HCASub)
	sess.CreatedAt = time.UnixMilli(createdMs)
	sess.ExpiresAt = time.UnixMilli(expiresMs)
	if authTime.Valid {
		sess.AuthTime = time.UnixMilli(authTime.Int64)
	}
	if freshAt.Valid {
		sess.FreshLoginAt = time.UnixMilli(freshAt.Int64)
	}
	sess.DisplayName = name.String
	return &sess, nil
}

// Email decrypts u's email address. It returns "" when none is stored.
func (s *Store) Email(u *User) (string, error) {
	if len(u.emailSealed) == 0 {
		return "", nil
	}
	return s.emails.Open(u.HCASub, u.emailSealed)
}

// Revoke ends the session whose cookie value is token. Revoking a session
// that doesn't exist, or already ended, does nothing.
func (s *Store) Revoke(ctx context.Context, token string) error {
	hash, ok := hashToken(token)
	if !ok {
		return nil
	}
	nowMs := s.now().UnixMilli()
	return s.w.Do(ctx, 1, func(ctx context.Context, tx writer.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at_ms = $2, display_name = NULL WHERE id_hash = $1 AND revoked_at_ms IS NULL`,
			hash, nowMs); err != nil {
			return fmt.Errorf("accounts: revoking the session: %w", err)
		}
		return nil
	})
}

// DeleteExpiredSessions deletes the rows of sessions that expired or were
// revoked more than a day ago, and returns how many it deleted.
func (s *Store) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	cutoff := s.now().Add(-sessionGrace).UnixMilli()
	var n int64
	err := s.w.Do(ctx, 1, func(ctx context.Context, tx writer.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at_ms <= $1 OR revoked_at_ms <= $1`, cutoff)
		if err != nil {
			return fmt.Errorf("accounts: deleting old sessions: %w", err)
		}
		n, err = res.RowsAffected()
		return err
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// newSessionToken returns a fresh session token and the hash the database
// keeps.
func newSessionToken() (token, hash string) {
	b := make([]byte, tokenBytes)
	rand.Read(b)
	token = base64.RawURLEncoding.EncodeToString(b)
	hash, _ = hashToken(token)
	return token, hash
}

// hashToken returns the hex SHA-256 of a well-formed session token.
func hashToken(token string) (string, bool) {
	if len(token) != base64.RawURLEncoding.EncodedLen(tokenBytes) {
		return "", false
	}
	if _, err := base64.RawURLEncoding.DecodeString(token); err != nil {
		return "", false
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:]), true
}

// newID returns a UUIDv7 for now (RFC 9562 section 5.7), the id form the
// store package's portable SQL rules ask for.
func newID(now time.Time) string {
	var b [16]byte
	rand.Read(b[:])
	ms := uint64(now.UnixMilli())
	for i := range 6 {
		b[i] = byte(ms >> (40 - 8*i))
	}
	b[6] = b[6]&0x0f | 0x70
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func nullBool(b *bool) sql.NullInt64 {
	switch {
	case b == nil:
		return sql.NullInt64{}
	case *b:
		return sql.NullInt64{Int64: 1, Valid: true}
	default:
		return sql.NullInt64{Int64: 0, Valid: true}
	}
}

func nullTime(t time.Time) sql.NullInt64 {
	if t.IsZero() {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: t.UnixMilli(), Valid: true}
}
