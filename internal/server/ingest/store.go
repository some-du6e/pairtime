// SPDX-License-Identifier: AGPL-3.0-or-later

package ingest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/some-du6e/pairtime/internal/server/device"
	"github.com/some-du6e/pairtime/internal/server/writer"
	"github.com/some-du6e/pairtime/pkg/ledger"
)

// The SQL follows package store's portable rules, and runs inside the
// writer's transaction but for the replay check before it, which reads
// ingest_replies through the read pool. A device's counter and chain head
// are on its devices row, which ingest reads and writes only through
// device.Store (D-device-row-chain-state); no SQL here names devices.
// Every write checks first and then inserts or updates, because the
// writer runs one op at a time: an op never needs a constraint error to
// find a row, and Postgres would abort the whole transaction on one. No
// upsert changes a row's account_id (D-device-account-binding).

// queryer is the read half of writer.Tx, which *sql.DB has too.
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// reply is a device's most recent processed request, which a retry of it
// gets back byte for byte (ingest-v1 2.10): its Idempotency-Key, the raw
// body's SHA-256 and the response. The device's counter and chain head are
// on its devices row (D-device-row-chain-state), never here; a reply that
// is gone only makes a retry a new request, which batch roots and bucket
// revisions are idempotent for.
type reply struct {
	found    bool
	key      string
	bodySHA  string
	response []byte
}

// loadReply reads deviceID's reply, if it is account's.
func loadReply(ctx context.Context, q queryer, deviceID, account string) (reply, error) {
	var rp reply
	var resp string
	err := q.QueryRowContext(ctx, `SELECT idempotency_key, body_sha256, response FROM ingest_replies WHERE device_id = $1 AND account_id = $2`,
		deviceID, account).Scan(&rp.key, &rp.bodySHA, &resp)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return rp, nil
	case err != nil:
		return rp, fmt.Errorf("ingest: reading the device's last reply: %w", err)
	}
	rp.found, rp.response = true, []byte(resp)
	return rp, nil
}

// putReply stores rp as deviceID's most recent processed request.
func putReply(ctx context.Context, tx writer.Tx, deviceID, account string, rp reply, now int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO ingest_replies (device_id, account_id, idempotency_key, body_sha256, response, received_at_ms)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (device_id) DO UPDATE SET idempotency_key = excluded.idempotency_key, body_sha256 = excluded.body_sha256,
response = excluded.response, received_at_ms = excluded.received_at_ms`,
		deviceID, account, rp.key, rp.bodySHA, string(rp.response), now)
	if err != nil {
		return fmt.Errorf("ingest: storing the device's last reply: %w", err)
	}
	return nil
}

// deviceTables are the tables whose rows ingest reads back for a device,
// each with its rows' account.
var deviceTables = []string{"witness_rows", "minute_buckets", "minute_bucket_revisions", "commit_records", "device_labels", "ingest_replies"}

// foreignRows reports whether the server holds a row of deviceID under an
// account other than account (D-device-account-binding). A device's rows
// all share one account: ingest refuses a request that finds otherwise
// before it writes anything, and no upsert here changes account_id. So
// one row of each table settles it, through the index every table has on
// device_id.
func foreignRows(ctx context.Context, q queryer, deviceID, account string) (bool, error) {
	for _, table := range deviceTables {
		var owner string
		err := q.QueryRowContext(ctx, `SELECT account_id FROM `+table+` WHERE device_id = $1 LIMIT 1`, deviceID).Scan(&owner)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return false, fmt.Errorf("ingest: reading %s: %w", table, err)
		case owner != account:
			return true, nil
		}
	}
	return false, nil
}

// witnessRow is a stored batch root with its receipt (DESIGN 3.6).
type witnessRow struct {
	n                uint64
	root, head, prev ledger.Hash
	receivedAt       int64
	sig              string
	keyID            string
}

func loadWitness(ctx context.Context, q queryer, deviceID string, b uint64) (witnessRow, bool, error) {
	var w witnessRow
	var n int64
	var root, head, prev string
	err := q.QueryRowContext(ctx, `SELECT n, root, head, prev, received_at_ms, receipt_sig, receipt_key_id FROM witness_rows WHERE device_id = $1 AND b = $2`,
		deviceID, int64(b)).Scan(&n, &root, &head, &prev, &w.receivedAt, &w.sig, &w.keyID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return w, false, nil
	case err != nil:
		return w, false, fmt.Errorf("ingest: reading a witness row: %w", err)
	}
	w.n = uint64(n)
	for _, h := range []struct {
		dst *ledger.Hash
		src string
	}{{&w.root, root}, {&w.head, head}, {&w.prev, prev}} {
		if err := h.dst.UnmarshalText([]byte(h.src)); err != nil {
			return w, false, fmt.Errorf("ingest: a stored witness row: %w", err)
		}
	}
	return w, true, nil
}

func insertWitness(ctx context.Context, tx writer.Tx, deviceID, account string, b uint64, w witnessRow) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO witness_rows (device_id, b, account_id, n, root, head, prev, received_at_ms, receipt_sig, receipt_key_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		deviceID, int64(b), account, int64(w.n), w.root.String(), w.head.String(), w.prev.String(), w.receivedAt, w.sig, w.keyID)
	if err != nil {
		return fmt.Errorf("ingest: storing a witness row: %w", err)
	}
	return nil
}

// storedBucket is a minute's current revision in minute_buckets, and how
// many revisions of the minute ingest stored.
type storedBucket struct {
	rev        int64
	receivedAt int64
	bucket     string
	revisions  int64
}

func loadBucket(ctx context.Context, q queryer, deviceID string, minuteMS int64) (storedBucket, bool, error) {
	var s storedBucket
	err := q.QueryRowContext(ctx, `SELECT rev, received_at_ms, bucket, revisions FROM minute_buckets WHERE device_id = $1 AND minute_ms = $2`,
		deviceID, minuteMS).Scan(&s.rev, &s.receivedAt, &s.bucket, &s.revisions)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return s, false, nil
	case err != nil:
		return s, false, fmt.Errorf("ingest: reading a minute bucket: %w", err)
	}
	return s, true, nil
}

// putBucket stores bucket as the minute's current revision, the
// revisions-th, and adds it to minute_bucket_revisions, which keeps every
// revision ingest stored.
func putBucket(ctx context.Context, tx writer.Tx, deviceID, account string, minuteMS, rev int64, state string, bucket []byte, receivedAt, revisions int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO minute_buckets (device_id, minute_ms, account_id, rev, state, received_at_ms, bucket, revisions)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (device_id, minute_ms) DO UPDATE SET rev = excluded.rev, state = excluded.state,
received_at_ms = excluded.received_at_ms, bucket = excluded.bucket, revisions = excluded.revisions`,
		deviceID, minuteMS, account, rev, state, receivedAt, string(bucket), revisions)
	if err != nil {
		return fmt.Errorf("ingest: storing a minute bucket: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO minute_bucket_revisions (device_id, minute_ms, rev, account_id, received_at_ms, bucket)
VALUES ($1, $2, $3, $4, $5, $6)`, deviceID, minuteMS, rev, account, receivedAt, string(bucket))
	if err != nil {
		return fmt.Errorf("ingest: storing a minute bucket's revision: %w", err)
	}
	return nil
}

func loadCommit(ctx context.Context, q queryer, deviceID, sha string) (string, bool, error) {
	var body string
	err := q.QueryRowContext(ctx, `SELECT body FROM commit_records WHERE device_id = $1 AND sha = $2`, deviceID, sha).Scan(&body)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("ingest: reading a commit record: %w", err)
	}
	return body, true, nil
}

// putCommit stores a commit record, keeping first_seen_at_ms from the
// first time the server saw it from this device.
func putCommit(ctx context.Context, tx writer.Tx, deviceID, account string, c commitRecord, body []byte, now int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO commit_records (device_id, sha, account_id, project, body, first_seen_at_ms, updated_at_ms)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (device_id, sha) DO UPDATE SET project = excluded.project, body = excluded.body,
updated_at_ms = excluded.updated_at_ms`,
		deviceID, c.SHA, account, c.Project, string(body), now, now)
	if err != nil {
		return fmt.Errorf("ingest: storing a commit record: %w", err)
	}
	return nil
}

// witnessHead fills st's head and chain start from the device's witness
// rows, for a device whose row holds no head while some of its witness
// rows stay, which only a row made afresh for the device_id can give
// (ingest-v1 2.7 step 4). The head it gives was stored under no link the
// row knows, generation 0, so the device counts as signed in again since
// (D-local-gap). It reports whether it found one.
func witnessHead(ctx context.Context, q queryer, deviceID string, st *device.State) (bool, error) {
	var first, last sql.NullInt64
	if err := q.QueryRowContext(ctx, `SELECT MIN(b), MAX(b) FROM witness_rows WHERE device_id = $1`, deviceID).Scan(&first, &last); err != nil {
		return false, fmt.Errorf("ingest: reading a device's witness rows: %w", err)
	}
	if !last.Valid {
		return false, nil
	}
	w, found, err := loadWitness(ctx, q, deviceID, uint64(last.Int64))
	if err != nil || !found {
		return false, err
	}
	st.HeadB, st.HeadHash, st.HeadReceivedAt = uint64(last.Int64), w.head, time.UnixMilli(w.receivedAt)
	st.HeadGeneration, st.ChainStart, st.Gaps = 0, uint64(first.Int64), nil
	return true, nil
}

// insertOrigin records where a processed upload came from (DESIGN 4.4's
// uploading IP and JA4, kept 30 days).
func insertOrigin(ctx context.Context, tx writer.Tx, id, deviceID, account, ip, ja4 string, now int64) error {
	var j any
	if ja4 != "" {
		j = ja4
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO request_origins (id, device_id, account_id, ip, ja4, seen_at_ms) VALUES ($1, $2, $3, $4, $5, $6)`,
		id, deviceID, account, ip, j, now)
	if err != nil {
		return fmt.Errorf("ingest: storing an upload's origin: %w", err)
	}
	return nil
}

func putLabels(ctx context.Context, tx writer.Tx, deviceID, account string, body []byte, now int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO device_labels (device_id, account_id, labels, received_at_ms)
VALUES ($1, $2, $3, $4)
ON CONFLICT (device_id) DO UPDATE SET labels = excluded.labels, received_at_ms = excluded.received_at_ms`,
		deviceID, account, string(body), now)
	if err != nil {
		return fmt.Errorf("ingest: storing labels: %w", err)
	}
	return nil
}
