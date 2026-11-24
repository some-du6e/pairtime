// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"log/slog"

	"github.com/some-du6e/pairtime/internal/server/accounts"
	"github.com/some-du6e/pairtime/internal/server/config"
	"github.com/some-du6e/pairtime/internal/server/privacy"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// newPrivacyHub builds the privacy hub's deletion and export endpoints
// (WP-SV13; DESIGN 8.9), or returns nil, which leaves them off, while
// privacy_hub.aud is empty: the hub signs tokens only for an audience its
// staff registered for this server. With it, the email key must be set,
// or serve doesn't start, since the eraser finds an account by its
// email's keyed index as well as by its Slack id and Hack Club Auth
// identity. privacy_hub.jwks_url replaces the hub's key set only to point
// at a hub run for a test.
func newPrivacyHub(cfg *config.Config, db *store.DB, w *writer.Writer, logger *slog.Logger) (*privacy.Service, error) {
	if cfg.PrivacyHub.Aud == "" {
		return nil, nil
	}
	raw := cfg.Secrets.Get(config.EmailKey)
	if raw == "" {
		return nil, fmt.Errorf("privacy_hub.aud is set, so the secret %s must be too: the eraser finds an account by its email's keyed index (DESIGN 8.9)", config.EmailKey)
	}
	key, err := accounts.ParseEmailKey(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", config.EmailKey, err)
	}
	emails, err := accounts.NewEmailCipher(key)
	if err != nil {
		return nil, err
	}
	url := cfg.PrivacyHub.JWKSURL
	if url == "" {
		url = privacy.DefaultJWKSURL
	}
	svc, err := privacy.New(privacy.Options{
		Verifier: &privacy.Verifier{Audience: cfg.PrivacyHub.Aud, Keys: &privacy.JWKS{URL: url}},
		DB:       db,
		Writer:   w,
		Emails:   emails,
		Logger:   logger,
	})
	if err != nil {
		return nil, err
	}
	logger.Info("the privacy hub's deletion and export endpoints are on", "aud", cfg.PrivacyHub.Aud, "jwks_url", url)
	return svc, nil
}
