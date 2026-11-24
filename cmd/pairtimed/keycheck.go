// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"crypto/ecdh"
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

// recordKeys makes the key history hold the keys in data_dir as the
// current ones, the server key (WP-SV17; serverkeys.RecordServer) and the
// evidence key (WP-SB2; serverkeys.RecordEvidence), in one transaction.
// It returns the history and the ring of evidence keys, which
// /.well-known/pairtime-keys.json publishes. It runs after
// checkServerKeys, so the server key is the one the database was written
// with. A data directory without keys records nothing: the document lists
// the server keys the history holds from before, and no evidence key,
// since the ring then holds none.
func recordKeys(ctx context.Context, dataDir string, db *store.DB, w *writer.Writer, logger *slog.Logger) (*serverkeys.History, *serverkeys.Ring, error) {
	keys, err := backup.Load(backup.KeysDir(dataDir))
	var evidence []*ecdh.PrivateKey
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// checkServerKeys has said so.
	case err != nil:
		return nil, nil, err
	default:
		pub := keys.Public()
		err := w.Do(ctx, 3, func(ctx context.Context, tx writer.Tx) error {
			now := time.Now().UnixMilli()
			if err := serverkeys.RecordServer(ctx, tx, pub.Server, now); err != nil {
				return fmt.Errorf("recording the server key's validity: %w", err)
			}
			if err := serverkeys.RecordEvidence(ctx, tx, pub.Evidence, now); err != nil {
				return fmt.Errorf("recording the evidence key's validity: %w", err)
			}
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
		evidence = append(evidence, keys.Evidence)
	}
	h, err := serverkeys.Load(ctx, db.Read)
	if err != nil {
		return nil, nil, err
	}
	ring, err := serverkeys.NewRing(h, evidence...)
	if err != nil {
		return nil, nil, err
	}
	if k, ok := h.At(time.Now().UnixMilli()); ok {
		logger.Info("the keys document lists the server key", "server_key", k.ID, "valid_from", time.UnixMilli(k.ValidFrom).UTC().Format(time.RFC3339), "keys", len(h.Server))
	}
	if k, ok := ring.Current(); ok {
		logger.Info("the keys document lists the evidence key", "evidence_key", k.ID, "valid_from", time.UnixMilli(k.ValidFrom).UTC().Format(time.RFC3339))
	}
	return h, ring, nil
}
