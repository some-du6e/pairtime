// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"log/slog"
	"time"

	"github.com/some-du6e/pairtime/internal/server/backup"
	"github.com/some-du6e/pairtime/internal/server/config"
	"github.com/some-du6e/pairtime/internal/server/device"
	"github.com/some-du6e/pairtime/internal/server/ingest"
	"github.com/some-du6e/pairtime/internal/server/ops"
	"github.com/some-du6e/pairtime/internal/server/rollup"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// newIngest returns the ingest API (WP-SV4; docs/spec/ingest-v1.md). It
// signs receipts with the server key that pairtimed init made, and finds
// devices by their tokens through WP-SV3's device store, which also keeps
// each device's chain state on its row (D-device-row-chain-state). Until
// the server key is there, uploads get 503 ingest_off, and GET
// /api/v1/time answers all the same. Each processed upload counts in
// /metrics' ingest series (DESIGN 10.4).
func newIngest(cfg *config.Config, db *store.DB, w *writer.Writer, devices *device.Store, series *ops.Series, logger *slog.Logger) *ingest.Service {
	o := ingest.Options{Devices: devices, Writer: w, Read: db.Read, Logger: logger}
	o.Observe = func(stored int, took time.Duration) {
		series.IngestBatches.Add(uint64(stored))
		series.IngestBatchSeconds.Observe(took.Seconds())
	}
	keys, err := backup.Load(backup.KeysDir(cfg.DataDir))
	if err != nil {
		logger.Warn("ingest is off: the server key didn't load; pairtimed init makes it (DESIGN 10.2)", "err", err)
	} else {
		o.Key = keys.Server
	}
	// Every bucket revision ingest stores moves the owner's rollups in the
	// same writer transaction (WP-SV7).
	o.Rollup = rollup.ApplyEvent
	s := ingest.New(o)
	if !s.On() {
		logger.Info("ingest is off until the server key is there; GET /api/v1/time still answers")
	}
	return s
}
