// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"log/slog"

	"github.com/some-du6e/pairtime/internal/server/accounts"
	"github.com/some-du6e/pairtime/internal/server/auth"
	"github.com/some-du6e/pairtime/internal/server/config"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// newSignIn builds Hack Club Auth sign-in from cfg (WP-SV2). Without
// hca.client_id, sign-in is off and its pages say so. With it, the client
// secret and the email key must be set, or serve doesn't start.
func newSignIn(cfg *config.Config, db *store.DB, w *writer.Writer, logger *slog.Logger) (*auth.Service, error) {
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
		DB:          db,
		Writer:      w,
		Emails:      emails,
		Reviewers:   cfg.Reviewers,
		SignupsOpen: cfg.SignupsOpen,
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
