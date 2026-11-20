// SPDX-License-Identifier: AGPL-3.0-or-later

package accounts

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/config"
	"github.com/some-du6e/pairtime/internal/server/migrations"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// clock is a fake clock tests move by hand.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newClock() *clock {
	return &clock{t: time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)}
}

var testKey = bytes.Repeat([]byte{7}, EmailKeySize)

func testCipher(t *testing.T) *EmailCipher {
	t.Helper()
	c, err := NewEmailCipher(testKey)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// open returns a Store on a fresh, migrated database.
func open(t *testing.T, engine store.Engine, o Options) (*Store, *store.DB) {
	t.Helper()
	db := storetest.Open(t, engine)
	ms, err := migrations.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(context.Background(), db, ms, time.Now); err != nil {
		t.Fatal(err)
	}
	w := writer.Start(db.Write, writer.Options{Window: time.Millisecond})
	t.Cleanup(w.Close)
	o.DB, o.Writer = db, w
	if o.Emails == nil {
		o.Emails = testCipher(t)
	}
	s, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	return s, db
}

func count(t *testing.T, db *store.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.Read.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func boolPtr(b bool) *bool { return &b }

// anyone is MayCreate with sign-ups open to every identity.
func anyone(string) bool { return true }

func verified() Identity {
	return Identity{
		Sub:          "ident!abc123",
		SlackID:      "U0ABC123",
		Email:        "teen@example.org",
		Status:       Verified,
		YSWSEligible: boolPtr(true),
	}
}

func TestSignIn(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		// closed makes MayCreate nil once the before sign-ins are done,
		// and mayCreate, when set, replaces it then. Otherwise anyone may
		// sign up.
		closed     bool
		mayCreate  func(sub string) bool
		before     []Identity // sign-ins that happen first, each accepted
		id         Identity
		login      Login
		wantErr    error // nil means the sign-in is accepted
		wantErrAny bool  // any error, not one of the sentinels
		check      func(t *testing.T, s *Store, db *store.DB, sess *Session, created bool)
	}{
		{
			name: "a first sign-in creates the account and opens a session",
			id:   verified(),
			check: func(t *testing.T, s *Store, db *store.DB, sess *Session, created bool) {
				if !created {
					t.Error("created = false for the first sign-in")
				}
				if sess.User.HCASub != "ident!abc123" || sess.User.SlackID != "U0ABC123" || sess.User.Status != Verified {
					t.Errorf("user = %+v", sess.User)
				}
				if n := count(t, db, `SELECT count(*) FROM users`); n != 1 {
					t.Errorf("%d users, want 1", n)
				}
				if n := count(t, db, `SELECT count(*) FROM sessions WHERE user_id = $1`, sess.User.ID); n != 1 {
					t.Errorf("%d sessions, want 1", n)
				}
			},
		},
		{
			name:   "a second sign-in updates the account and keeps its id",
			before: []Identity{verified()},
			id: Identity{Sub: "ident!abc123", SlackID: "U0NEWSLACK", Email: "new@example.org",
				Status: Verified, YSWSEligible: boolPtr(false)},
			check: func(t *testing.T, s *Store, db *store.DB, sess *Session, created bool) {
				if created {
					t.Error("created = true for a second sign-in")
				}
				if n := count(t, db, `SELECT count(*) FROM users`); n != 1 {
					t.Errorf("%d users, want 1", n)
				}
				got, err := s.Session(ctx, sess.Token)
				if err != nil {
					t.Fatal(err)
				}
				if got.User.SlackID != "U0NEWSLACK" || !got.User.EligibilityToConfirm() {
					t.Errorf("user after the second sign-in = %+v", got.User)
				}
				if email, err := s.Email(&got.User); err != nil || email != "new@example.org" {
					t.Errorf("Email = %q, %v; want the new address", email, err)
				}
				if n := count(t, db, `SELECT count(*) FROM sessions`); n != 2 {
					t.Errorf("%d sessions, want one per sign-in", n)
				}
			},
		},
		{
			name:    "an ineligible identity without an account is refused and nothing is written",
			id:      Identity{Sub: "ident!young1", Status: Ineligible, Email: "young@example.org"},
			wantErr: ErrIneligible,
			check: func(t *testing.T, s *Store, db *store.DB, _ *Session, _ bool) {
				if n := count(t, db, `SELECT count(*) FROM users`); n != 0 {
					t.Errorf("%d users after refusing an ineligible identity, want 0", n)
				}
			},
		},
		{
			name:    "an account that turns ineligible keeps its row with the new status and loses its sessions",
			before:  []Identity{verified(), verified()},
			id:      Identity{Sub: "ident!abc123", Status: Ineligible},
			wantErr: ErrIneligible,
			check: func(t *testing.T, s *Store, db *store.DB, _ *Session, _ bool) {
				if n := count(t, db, `SELECT count(*) FROM users WHERE verification_status = 'ineligible'`); n != 1 {
					t.Errorf("%d ineligible users, want the one account", n)
				}
				if n := count(t, db, `SELECT count(*) FROM sessions WHERE revoked_at_ms IS NULL`); n != 0 {
					t.Errorf("%d sessions still live, want 0", n)
				}
			},
		},
		{
			name:    "an account that turns ineligible loses its email and Slack id, though HCA sent both",
			before:  []Identity{verified()},
			id:      Identity{Sub: "ident!abc123", SlackID: "U0NEWSLACK", Email: "new@example.org", Status: Ineligible},
			wantErr: ErrIneligible,
			check: func(t *testing.T, s *Store, db *store.DB, _ *Session, _ bool) {
				if n := count(t, db, `SELECT count(*) FROM users WHERE verification_status = 'ineligible' AND slack_id IS NULL AND email_ct IS NULL AND email_index IS NULL`); n != 1 {
					t.Error("the ineligible account's row still holds an email or a Slack id")
				}
			},
		},
		{
			name:    "with sign-ups closed an identity without an account is refused and nothing is written",
			closed:  true,
			id:      verified(),
			wantErr: ErrSignupsClosed,
			check: func(t *testing.T, s *Store, db *store.DB, _ *Session, _ bool) {
				if n := count(t, db, `SELECT count(*) FROM users`); n != 0 {
					t.Errorf("%d users while sign-ups are closed, want 0", n)
				}
			},
		},
		{
			name:   "with sign-ups closed an existing account still signs in",
			closed: true,
			before: []Identity{verified()},
			id:     verified(),
		},
		{
			name:      "MayCreate admits a new identity by its HCA subject",
			mayCreate: func(sub string) bool { return sub == "ident!abc123" },
			id:        verified(),
			check: func(t *testing.T, s *Store, db *store.DB, sess *Session, created bool) {
				if !created || sess.User.HCASub != "ident!abc123" {
					t.Errorf("created = %v, user = %+v; want a new account", created, sess.User)
				}
			},
		},
		{
			name:      "a new identity MayCreate doesn't admit is refused and nothing is written",
			mayCreate: func(sub string) bool { return sub == "ident!someoneElse" },
			id:        verified(),
			wantErr:   ErrSignupsClosed,
			check: func(t *testing.T, s *Store, db *store.DB, _ *Session, _ bool) {
				if n := count(t, db, `SELECT count(*) FROM users`); n != 0 {
					t.Errorf("%d users, want 0", n)
				}
			},
		},
		{
			name:      "an existing account signs in whatever MayCreate says",
			mayCreate: func(string) bool { return false },
			before:    []Identity{verified()},
			id:        verified(),
			check: func(t *testing.T, s *Store, db *store.DB, _ *Session, created bool) {
				if created {
					t.Error("created = true for an existing account")
				}
			},
		},
		{name: "an unknown verification status is refused", id: Identity{Sub: "ident!abc123", Status: "verified_eventually"}, wantErrAny: true},
		{name: "a fresh login without the time HCA authenticated the person is refused", id: verified(), login: Login{Fresh: true}, wantErrAny: true},
		{name: "an empty subject is refused", id: Identity{Status: Verified}, wantErrAny: true},
		{name: "a subject with a space is refused", id: Identity{Sub: "ident! abc", Status: Verified}, wantErrAny: true},
	}
	for _, engine := range storetest.Engines() {
		t.Run(string(engine), func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					s, db := open(t, engine, Options{MayCreate: anyone})
					for _, id := range tt.before {
						if _, _, err := s.SignIn(ctx, id, Login{}); err != nil {
							t.Fatalf("an earlier sign-in: %v", err)
						}
					}
					switch {
					case tt.closed:
						s.mayCreate = nil
					case tt.mayCreate != nil:
						s.mayCreate = tt.mayCreate
					}
					sess, created, err := s.SignIn(ctx, tt.id, tt.login)
					switch {
					case tt.wantErrAny:
						if err == nil {
							t.Fatal("SignIn accepted it")
						}
					case tt.wantErr != nil:
						if !errors.Is(err, tt.wantErr) {
							t.Fatalf("SignIn error = %v, want %v", err, tt.wantErr)
						}
						if sess != nil {
							t.Error("a refused sign-in returned a session")
						}
					case err != nil:
						t.Fatalf("SignIn: %v", err)
					}
					if tt.check != nil {
						tt.check(t, s, db, sess, created)
					}
				})
			}
		})
	}
}

func TestEmailAtRest(t *testing.T) {
	ctx := context.Background()
	for _, engine := range storetest.Engines() {
		t.Run(string(engine), func(t *testing.T) {
			s, db := open(t, engine, Options{MayCreate: anyone})
			id := verified()
			id.Email = "Teen@Example.org"
			sess, _, err := s.SignIn(ctx, id, Login{})
			if err != nil {
				t.Fatal(err)
			}
			var sealed []byte
			var index string
			if err := db.Read.QueryRow(`SELECT email_ct, email_index FROM users`).Scan(&sealed, &index); err != nil {
				t.Fatal(err)
			}
			got, err := s.Session(ctx, sess.Token)
			if err != nil {
				t.Fatal(err)
			}
			email, emailErr := s.Email(&got.User)
			tests := []struct {
				name string
				ok   bool
			}{
				{name: "the stored email is ciphertext that doesn't contain the address", ok: len(sealed) > 0 &&
					!bytes.Contains(bytes.ToLower(sealed), []byte("teen@example.org"))},
				{name: "the stored index is the keyed hash of the lowercased address", ok: index == s.emails.Index("teen@example.org")},
				{name: "Email decrypts the address as HCA sent it", ok: emailErr == nil && email == "Teen@Example.org"},
				{name: "HasEmail says an address is stored", ok: got.User.HasEmail},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					if !tt.ok {
						t.Errorf("sealed = %x, index = %q, email = %q (%v)", sealed, index, email, emailErr)
					}
				})
			}
		})
	}
}

func TestSession(t *testing.T) {
	ctx := context.Background()
	reviewers := []config.Reviewer{{Sub: "ident!abc123", Roles: []string{"qi", "program_reviewer:stardance"}}}
	tests := []struct {
		name string
		// fresh signs in with a fresh login; either way HCA's auth_time is
		// a minute before the sign-in.
		fresh bool
		// act runs after the sign-in and returns the token to look up.
		act     func(t *testing.T, s *Store, c *clock, token string) string
		wantErr bool
		check   func(t *testing.T, sess *Session, c *clock)
	}{
		{
			name:  "a live session is found with its account, its roles, HCA's auth_time and its fresh login",
			fresh: true,
			act:   func(_ *testing.T, _ *Store, _ *clock, token string) string { return token },
			check: func(t *testing.T, sess *Session, c *clock) {
				if sess.User.HCASub != "ident!abc123" || !sess.User.CanSubmit() {
					t.Errorf("user = %+v", sess.User)
				}
				if !slices.Equal(sess.Roles, []string{"qi", "program_reviewer:stardance"}) || !sess.HasRole("qi") || sess.HasRole("admin") {
					t.Errorf("roles = %v", sess.Roles)
				}
				if at := c.t.Add(-time.Minute); !sess.AuthTime.Equal(at) || !sess.FreshLoginAt.Equal(at) {
					t.Errorf("auth time = %v, fresh login at %v; want both %v", sess.AuthTime, sess.FreshLoginAt, at)
				}
				if !sess.FreshLoginWithin(2*time.Minute, c.t) {
					t.Error("a fresh login a minute old isn't one within 2 minutes")
				}
				if !sess.ExpiresAt.Equal(c.t.Add(DefaultSessionLifetime)) {
					t.Errorf("expires at %v, want %v", sess.ExpiresAt, c.t.Add(DefaultSessionLifetime))
				}
			},
		},
		{
			name: "an ordinary sign-in's session is no fresh login, however recent HCA's auth_time",
			act:  func(_ *testing.T, _ *Store, _ *clock, token string) string { return token },
			check: func(t *testing.T, sess *Session, c *clock) {
				if !sess.AuthTime.Equal(c.t.Add(-time.Minute)) {
					t.Errorf("auth time = %v, want %v", sess.AuthTime, c.t.Add(-time.Minute))
				}
				if !sess.FreshLoginAt.IsZero() || sess.FreshLoginWithin(time.Hour, c.t) {
					t.Errorf("fresh login at %v, want none", sess.FreshLoginAt)
				}
			},
		},
		{
			name: "a session isn't found once it expires",
			act: func(_ *testing.T, _ *Store, c *clock, token string) string {
				c.t = c.t.Add(DefaultSessionLifetime)
				return token
			},
			wantErr: true,
		},
		{
			name: "a revoked session isn't found",
			act: func(t *testing.T, s *Store, _ *clock, token string) string {
				if err := s.Revoke(ctx, token); err != nil {
					t.Fatal(err)
				}
				return token
			},
			wantErr: true,
		},
		{
			name:    "a token one character off isn't found",
			act:     func(_ *testing.T, _ *Store, _ *clock, token string) string { return flip(token) },
			wantErr: true,
		},
		{
			name:    "a malformed token isn't found",
			act:     func(_ *testing.T, _ *Store, _ *clock, _ string) string { return "not a token" },
			wantErr: true,
		},
	}
	for _, engine := range storetest.Engines() {
		t.Run(string(engine), func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					c := newClock()
					s, _ := open(t, engine, Options{MayCreate: anyone, Reviewers: reviewers, Now: c.now})
					signed, _, err := s.SignIn(ctx, verified(), Login{AuthTime: c.t.Add(-time.Minute), Fresh: tt.fresh})
					if err != nil {
						t.Fatal(err)
					}
					sess, err := s.Session(ctx, tt.act(t, s, c, signed.Token))
					if tt.wantErr {
						if !errors.Is(err, ErrNoSession) {
							t.Fatalf("Session = %+v, %v; want ErrNoSession", sess, err)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					tt.check(t, sess, c)
				})
			}
		})
	}
}

// flip changes the token's first character to another base64url one.
func flip(token string) string {
	if token[0] == 'A' {
		return "B" + token[1:]
	}
	return "A" + token[1:]
}

func TestTokenAtRest(t *testing.T) {
	ctx := context.Background()
	for _, engine := range storetest.Engines() {
		t.Run(string(engine), func(t *testing.T) {
			s, db := open(t, engine, Options{MayCreate: anyone})
			sess, _, err := s.SignIn(ctx, verified(), Login{})
			if err != nil {
				t.Fatal(err)
			}
			want, _ := hashToken(sess.Token)
			tests := []struct {
				name  string
				query string
				arg   string
				want  int
			}{
				{name: "the database holds the token's hash", query: `SELECT count(*) FROM sessions WHERE id_hash = $1`, arg: want, want: 1},
				{name: "the database never holds the token itself", query: `SELECT count(*) FROM sessions WHERE id_hash = $1`, arg: sess.Token, want: 0},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					if n := count(t, db, tt.query, tt.arg); n != tt.want {
						t.Errorf("%d rows, want %d", n, tt.want)
					}
				})
			}
		})
	}
}

func TestDeleteExpiredSessions(t *testing.T) {
	ctx := context.Background()
	for _, engine := range storetest.Engines() {
		t.Run(string(engine), func(t *testing.T) {
			c := newClock()
			s, db := open(t, engine, Options{MayCreate: anyone, Now: c.now, SessionLifetime: time.Hour})
			signIn := func() string {
				t.Helper()
				sess, _, err := s.SignIn(ctx, verified(), Login{})
				if err != nil {
					t.Fatal(err)
				}
				return sess.Token
			}
			revoke := func(token string) {
				t.Helper()
				if err := s.Revoke(ctx, token); err != nil {
					t.Fatal(err)
				}
			}
			// At T0 a session opens, and it expires at T0+1h.
			expired := signIn()
			// At T1 = T0+1h two more open, both expiring at T1+1h, and one
			// of them is revoked at once.
			c.t = c.t.Add(time.Hour)
			revoked, twin := signIn(), signIn()
			revoke(revoked)
			// A day and a millisecond later the cutoff is T1 plus 1 ms: the
			// first session expired before it, and the revoked one was
			// revoked before it but expires after it, so only its
			// revocation can delete it.
			c.t = c.t.Add(sessionGrace + time.Millisecond)
			revokedNow, live := signIn(), signIn()
			revoke(revokedNow)
			n, err := s.DeleteExpiredSessions(ctx)
			if err != nil {
				t.Fatal(err)
			}
			tests := []struct {
				name  string
				token string
				want  int
			}{
				{name: "a session that expired more than a day ago is deleted", token: expired, want: 0},
				{name: "a session revoked more than a day ago is deleted, though it expires later", token: revoked, want: 0},
				{name: "a session that expired less than a day ago is kept", token: twin, want: 1},
				{name: "a session revoked less than a day ago is kept", token: revokedNow, want: 1},
				{name: "a live session is kept", token: live, want: 1},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					hash, _ := hashToken(tt.token)
					if got := count(t, db, `SELECT count(*) FROM sessions WHERE id_hash = $1`, hash); got != tt.want {
						t.Errorf("%d rows, want %d", got, tt.want)
					}
				})
			}
			if n != 2 {
				t.Errorf("DeleteExpiredSessions = %d, want 2", n)
			}
		})
	}
}

func TestEmailCipher(t *testing.T) {
	c := testCipher(t)
	other, err := NewEmailCipher(bytes.Repeat([]byte{8}, EmailKeySize))
	if err != nil {
		t.Fatal(err)
	}
	sealed := c.Seal("ident!a", "a@example.org")
	tampered := bytes.Clone(sealed)
	tampered[len(tampered)-1] ^= 1
	tests := []struct {
		name    string
		cipher  *EmailCipher
		sub     string
		sealed  []byte
		want    string
		wantErr error // nil with want "" means any error
	}{
		{name: "an email opens for the account it was sealed for", cipher: c, sub: "ident!a", sealed: sealed, want: "a@example.org"},
		{name: "an email copied to another account doesn't open", cipher: c, sub: "ident!b", sealed: sealed},
		{name: "an email sealed under another key says so", cipher: other, sub: "ident!a", sealed: sealed, wantErr: ErrOtherEmailKey},
		{name: "an altered email doesn't open", cipher: c, sub: "ident!a", sealed: tampered},
		{name: "a truncated email doesn't open", cipher: c, sub: "ident!a", sealed: sealed[:10]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.cipher.Open(tt.sub, tt.sealed)
			if tt.want != "" {
				if err != nil || got != tt.want {
					t.Errorf("Open = %q, %v; want %q", got, err, tt.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("Open = %q, want an error", got)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("Open error = %v, want %v", err, tt.wantErr)
			}
		})
	}

	props := []struct {
		name string
		ok   bool
	}{
		{name: "sealing twice gives different ciphertexts", ok: !bytes.Equal(c.Seal("ident!a", "a@example.org"), c.Seal("ident!a", "a@example.org"))},
		{name: "the index ignores case and surrounding space", ok: c.Index(" A@Example.ORG ") == c.Index("a@example.org")},
		{name: "different addresses get different indexes", ok: c.Index("a@example.org") != c.Index("b@example.org")},
		{name: "another key gives another index", ok: c.Index("a@example.org") != other.Index("a@example.org")},
		{name: "the index is 64 hex digits", ok: regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(c.Index("a@example.org"))},
	}
	for _, p := range props {
		t.Run(p.name, func(t *testing.T) {
			if !p.ok {
				t.Error("doesn't hold")
			}
		})
	}
}

func TestParseEmailKey(t *testing.T) {
	key := bytes.Repeat([]byte{0xfb}, EmailKeySize)
	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{name: "64 hex digits are a key", in: "fbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfb"},
		{name: "padded standard base64 is a key", in: "+/v7+/v7+/v7+/v7+/v7+/v7+/v7+/v7+/v7+/v7+/s="},
		{name: "unpadded URL-safe base64 is a key", in: "-_v7-_v7-_v7-_v7-_v7-_v7-_v7-_v7-_v7-_v7-_s"},
		{name: "surrounding space is ignored", in: "  fbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfb\n"},
		{name: "16 bytes are too few", in: "fbfbfbfbfbfbfbfbfbfbfbfbfbfbfbfb", wantErr: true},
		{name: "a passphrase isn't a key", in: "correct horse battery staple", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseEmailKey(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseEmailKey accepted %q", tt.in)
				}
				if bytes.Contains([]byte(err.Error()), []byte(tt.in)) {
					t.Errorf("the error quotes the key: %v", err)
				}
				return
			}
			if err != nil || !bytes.Equal(got, key) {
				t.Errorf("ParseEmailKey = %x, %v; want %x", got, err, key)
			}
		})
	}
}

func TestNewID(t *testing.T) {
	at := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	a, b := newID(at), newID(at.Add(time.Millisecond))
	uuidv7 := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "an id is a UUIDv7", ok: uuidv7.MatchString(a)},
		{name: "a later millisecond sorts later", ok: a < b},
		{name: "two ids in one millisecond differ", ok: newID(at) != newID(at)},
		{name: "the first 48 bits are the Unix milliseconds", ok: a[:8]+a[9:13] == "01a1f3ad8e00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Errorf("ids %s, %s", a, b)
			}
		})
	}
}

func TestEligibility(t *testing.T) {
	tests := []struct {
		name          string
		status        VerificationStatus
		eligible      *bool
		wantSubmit    bool
		wantToConfirm bool
	}{
		{name: "verified and YSWS-eligible may submit without a note", status: Verified, eligible: boolPtr(true), wantSubmit: true},
		{name: "verified but not YSWS-eligible may submit, with the eligibility note", status: Verified, eligible: boolPtr(false), wantSubmit: true, wantToConfirm: true},
		{name: "verified without ysws_eligible carries the note too", status: Verified, wantSubmit: true, wantToConfirm: true},
		{name: "pending may not submit", status: Pending},
		{name: "needs_submission may not submit", status: NeedsSubmission},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := User{Status: tt.status, YSWSEligible: tt.eligible}
			if u.CanSubmit() != tt.wantSubmit || u.EligibilityToConfirm() != tt.wantToConfirm {
				t.Errorf("CanSubmit = %v, EligibilityToConfirm = %v; want %v, %v",
					u.CanSubmit(), u.EligibilityToConfirm(), tt.wantSubmit, tt.wantToConfirm)
			}
		})
	}
}

func TestFreshLoginWithin(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		sess Session
		d    time.Duration
		want bool
	}{
		{name: "a fresh login a minute old is within 2 minutes", sess: Session{AuthTime: now.Add(-time.Minute), FreshLoginAt: now.Add(-time.Minute)}, d: 2 * time.Minute, want: true},
		{name: "a fresh login exactly d old is within d", sess: Session{FreshLoginAt: now.Add(-2 * time.Minute)}, d: 2 * time.Minute, want: true},
		{name: "a fresh login 3 minutes old isn't within 2 minutes", sess: Session{FreshLoginAt: now.Add(-3 * time.Minute)}, d: 2 * time.Minute},
		{name: "an ordinary sign-in isn't a fresh login, even with a recent auth_time", sess: Session{AuthTime: now}, d: time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.sess.FreshLoginWithin(tt.d, now); got != tt.want {
				t.Errorf("FreshLoginWithin(%v) = %v, want %v", tt.d, got, tt.want)
			}
		})
	}
}
