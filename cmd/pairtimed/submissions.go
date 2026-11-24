// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"log/slog"
	"time"

	"github.com/some-du6e/pairtime/internal/server/auth"
	"github.com/some-du6e/pairtime/internal/server/config"
	"github.com/some-du6e/pairtime/internal/server/device"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/submission"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// newSubmissions returns the submissions API (WP-SB1; DESIGN 7.5 steps 1
// and 2; docs/spec/submissions-v1.md), for the programs pairtime.toml's
// [[programs]] lists. A web session creates and withdraws with a fresh
// login, through sign-in, and a device with its token and a request
// signed by the device key, through WP-SV3's device store. A freeze
// records scoring and paramsHash, what submission.Scoring made of
// scoring_version at start.
//
// Three hooks stay as they are until their work packages connect them: no
// program credential reaches the program view until WP-PG6 sets Program,
// the freeze's repo_overlap finds no shared fingerprint until WP-SV10's
// Fingerprints replace NoFingerprints, and WP-SB4 writes its holds through
// OnFreeze and WP-SB5 marks drawn items withheld through OnWithdraw.
func newSubmissions(cfg *config.Config, db *store.DB, w *writer.Writer, devices *device.Store, signIn *auth.Service, scoring, paramsHash string, logger *slog.Logger) (*submission.Service, error) {
	programs := make([]submission.Program, len(cfg.Programs))
	for i, p := range cfg.Programs {
		programs[i] = submission.Program{Name: p.Name, DisplayName: p.DisplayName, TakesHours: p.Hours()}
	}
	return submission.New(submission.Options{
		Devices:        devices,
		Sessions:       signIn,
		SameOrigin:     signIn.SameOrigin,
		Programs:       programs,
		Writer:         w,
		Read:           db.Read,
		Now:            time.Now,
		BaseURL:        cfg.BaseURL,
		ScoringVersion: scoring,
		Params:         paramsHash,
		Fingerprints:   submission.NoFingerprints{},
		Logger:         logger,
	})
}
