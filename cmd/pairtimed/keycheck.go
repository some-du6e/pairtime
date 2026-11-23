// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"time"

	"github.com/some-du6e/pairtime/internal/server/backup"
	serverkeys "github.com/some-du6e/pairtime/internal/server/keys"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// ErrOtherKeys is what serve returns, wrapped, when the keys in data_dir
// aren't the ones its database was written with.
var ErrOtherKeys = errors.New("the keys aren't the ones this database was written with")

// checkServerKeys ties the database to the keys in <data_dir>/keys (#133).
// On the first start that has keys it records the server key's id and the
// pepper's fingerprint in server_keys; on every later start it refuses
// keys that aren't those. A host rebuilt or moved with fresh keys from
// pairtimed init, which Litestream then restores the old database onto,
// would otherwise sign receipts no device can check and key every project
// anew. A data directory without keys has nothing to compare, as on a
// server that signs nothing yet, so serve runs on with a warning.
func checkServerKeys(ctx context.Context, dataDir string, db *store.DB, w *writer.Writer, logger *slog.Logger) error {
	dir := backup.KeysDir(dataDir)
	keys, err := backup.Load(dir)
	if errors.Is(err, fs.ErrNotExist) {
		logger.Warn("there are no keys to check against the database", "dir", dir, "err", err)
		return nil
	}
	if err != nil {
		return err
	}
	id, pepper := keys.Public().ServerKeyID(), keys.PepperFingerprint()
	var recordedID, recordedPepper string
	err = db.Read.QueryRowContext(ctx, `SELECT server_key_id, pepper_fingerprint FROM server_keys WHERE id = 1`).Scan(&recordedID, &recordedPepper)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		err = w.Do(ctx, 1, func(ctx context.Context, tx writer.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO server_keys (id, server_key_id, pepper_fingerprint, recorded_at_ms) VALUES (1, $1, $2, $3) ON CONFLICT (id) DO NOTHING`,
				id, pepper, time.Now().UnixMilli())
			return err
		})
		if err != nil {
			return fmt.Errorf("recording the keys this database is written with: %w", err)
		}
		logger.Info("recorded the keys this database is written with", "server_key", id, "pepper", pepper)
		return nil
	case err != nil:
		return fmt.Errorf("reading the keys this database was written with: %w", err)
	case recordedID != id || recordedPepper != pepper:
		return fmt.Errorf("%w: it records server key %s and pepper %s, and %s holds server key %s and pepper %s. Restore the keys from their backup (deploy/runbook.md, Restoring the keys); keys made anew would break every receipt and every project's identity",
			ErrOtherKeys, recordedID, recordedPepper, dir, id, pepper)
	}
	return nil
}

// recordServerKey makes the key history hold the server key in data_dir
// as the current one (WP-SV17; serverkeys.RecordServer) and returns the
// history, which /.well-known/pairtime-keys.json publishes. It runs after
// checkServerKeys, so the key is the one the database was written with. A
// data directory without keys records nothing, and the document lists the
// keys the history holds from before.
func recordServerKey(ctx context.Context, dataDir string, db *store.DB, w *writer.Writer, logger *slog.Logger) (*serverkeys.History, error) {
	keys, err := backup.Load(backup.KeysDir(dataDir))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// checkServerKeys has said so.
	case err != nil:
		return nil, err
	default:
		pub := keys.Public().Server
		err := w.Do(ctx, 2, func(ctx context.Context, tx writer.Tx) error {
			return serverkeys.RecordServer(ctx, tx, pub, time.Now().UnixMilli())
		})
		if err != nil {
			return nil, fmt.Errorf("recording the server key's validity: %w", err)
		}
	}
	h, err := serverkeys.Load(ctx, db.Read)
	if err != nil {
		return nil, err
	}
	if k, ok := h.At(time.Now().UnixMilli()); ok {
		logger.Info("the keys document lists the server key", "server_key", k.ID, "valid_from", time.UnixMilli(k.ValidFrom).UTC().Format(time.RFC3339), "keys", len(h.Server))
	}
	return h, nil
}
