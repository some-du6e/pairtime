// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/server/backup"
	"github.com/some-du6e/pairtime/internal/server/config"
	serverkeys "github.com/some-du6e/pairtime/internal/server/keys"
	"github.com/some-du6e/pairtime/internal/server/ops"
	"github.com/some-du6e/pairtime/internal/server/publisher"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// dayRootsOn says whether serve publishes the day roots (WP-SV17). That
// takes the server key, which signs them, and PAIRTIME_WITNESS_REPO, which
// names the public repository the witness sidecar pushes them to. compose
// sets the variable and mounts the directory (deploy/compose.yml).
func dayRootsOn(cfg *config.Config, keyErr error) bool {
	return keyErr == nil && cfg.Secrets.Get(envWitnessRepo) != ""
}

// dayRootsOptions returns the publisher's options: the database through
// the writer, the key history, the server key in data_dir, and dir, where
// the witness sidecar pushes from. The publisher holds the current key
// alone: pairtimed has no rotation yet, and the work package that adds one
// keeps the retired key here until it has signed its last day (ledger-v1
// 8).
func dayRootsOptions(cfg *config.Config, db *store.DB, w *writer.Writer, history *serverkeys.History, dir string) (publisher.Options, error) {
	keys, err := backup.Load(backup.KeysDir(cfg.DataDir))
	if err != nil {
		return publisher.Options{}, err
	}
	return publisher.Options{
		Writer:  w,
		Read:    db.Read,
		History: history,
		Keys:    map[string]ed25519.PrivateKey{keys.Public().ServerKeyID(): keys.Server},
		Dir:     dir,
	}, nil
}

// runDayRoots runs the publisher until ctx ends: at once, then at 00:10
// UTC each day, within the hour after a run that failed, and within five
// minutes after one the replica's lag held back. It logs what each run
// stored and wrote, and counts and logs each failure.
func runDayRoots(ctx context.Context, o publisher.Options, logger *slog.Logger, failed *ops.Counter) {
	logger.Info("day roots are on: the publisher runs now and at 00:10 UTC", "dir", o.Dir)
	publisher.Run(ctx, o, time.Now, func(r publisher.Result, err error) {
		if len(r.Stored) > 0 || len(r.Written) > 0 {
			logger.Info("published day roots", "heads", dayList(r.Stored), "files", dayList(r.Written))
		}
		switch {
		case errors.Is(err, publisher.ErrNotReady):
			failed.Inc()
			logger.Warn("the day roots wait for the database's replica to catch up; the publisher tries again within 5 minutes", "err", err)
		case err != nil:
			failed.Inc()
			logger.Error("publishing the day roots failed; the publisher tries again within the hour", "err", err)
		}
	})
}

// dayList names days by their dates, for the log.
func dayList(days []uint64) string {
	dates := make([]string, len(days))
	for i, d := range days {
		dates[i] = publisher.Date(d)
	}
	return strings.Join(dates, " ")
}
