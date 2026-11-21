// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/some-du6e/pairtime/internal/server/accounts"
	"github.com/some-du6e/pairtime/internal/server/auth"
	"github.com/some-du6e/pairtime/internal/server/config"
	"github.com/some-du6e/pairtime/internal/server/dashboard"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// newSignIn builds Hack Club Auth sign-in from cfg (WP-SV2). Without
// hca.client_id, sign-in is off and its pages say so. With it, the client
// secret and the email key must be set, or serve doesn't start. mayCreate
// is the sign-up gate, pages.Signups.MayCreate: everyone may create an
// account while signups_open is true, and only the operators list while it
// is false (DESIGN 8.6; WP-SV14). ineligible runs in the transaction that
// closes an account HCA turns ineligible: device.Store.RemoveAccount,
// which removes its devices (D-device-account-binding).
func newSignIn(cfg *config.Config, db *store.DB, w *writer.Writer, logger *slog.Logger, mayCreate func(sub string) bool,
	ineligible func(ctx context.Context, tx writer.Tx, account string, now time.Time) error) (*auth.Service, error) {
	if cfg.HCA.ClientID == "" {
		logger.Warn("hca.client_id isn't set, so nobody can sign in")
		return auth.New(auth.Config{BaseURL: cfg.BaseURL, Logger: logger})
	}
	raw := cfg.Secrets.Get(config.EmailKey)
	if raw == "" {
		return nil, fmt.Errorf("hca.client_id is set, so the secret %s must be too: it encrypts account emails at rest (DESIGN 4.4); make one with: openssl rand -hex 32", config.EmailKey)
	}
	key, err := accounts.ParseEmailKey(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", config.EmailKey, err)
	}
	emails, err := accounts.NewEmailCipher(key)
	if err != nil {
		return nil, err
	}
	acc, err := accounts.New(accounts.Options{
		DB:         db,
		Writer:     w,
		Emails:     emails,
		Reviewers:  cfg.Reviewers,
		MayCreate:  mayCreate,
		Ineligible: ineligible,
	})
	if err != nil {
		return nil, err
	}
	svc, err := auth.New(auth.Config{
		BaseURL:      cfg.BaseURL,
		Issuer:       cfg.HCA.Issuer,
		ClientID:     cfg.HCA.ClientID,
		ClientSecret: cfg.Secrets.Get(config.HCAClientSecret),
		Accounts:     acc,
		Logger:       logger,
	})
	if err != nil {
		return nil, err
	}
	logger.Info("sign-in with Hack Club Auth is on", "issuer", cfg.HCA.Issuer, "redirect_uri", svc.RedirectURI(), "signups_open", cfg.SignupsOpen)
	return svc, nil
}

// visitor tells the home page who a request is signed in as. A session
// that can't be read is logged, and the page shows the visitor as signed
// out, so / never fails because the database does.
func visitor(svc *auth.Service, logger *slog.Logger) func(*http.Request) (string, bool) {
	return func(r *http.Request) (string, bool) {
		sess, err := svc.Session(r)
		if err != nil {
			if !errors.Is(err, accounts.ErrNoSession) {
				logger.Error("reading a session for the home page", "err", err)
			}
			return "", false
		}
		return handle(&sess.User), true
	}
}

// accountOf tells the dashboard which account a request is signed in to:
// its users id, or dashboard.ErrNotSignedIn without a session. Unlike
// visitor, it passes on an error reading the session, which the dashboard
// answers with a 500: the page and the API need the account, and sending
// a signed-in owner to sign in again wouldn't help while the database
// fails.
func accountOf(svc *auth.Service) func(*http.Request) (string, error) {
	return func(r *http.Request) (string, error) {
		sess, err := svc.Session(r)
		switch {
		case errors.Is(err, accounts.ErrNoSession):
			return "", dashboard.ErrNotSignedIn
		case err != nil:
			return "", err
		}
		return sess.User.ID, nil
	}
}

// handle is the name the home page shows for an account: its Slack ID, or
// its HCA subject when HCA sent no Slack ID. pairtime has no handles of its
// own until profiles come (DESIGN 8.3). The page never shows the email
// address, which a screenshot of the page would otherwise carry.
func handle(u *accounts.User) string {
	if u.SlackID != "" {
		return u.SlackID
	}
	return u.HCASub
}
