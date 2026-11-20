// SPDX-License-Identifier: AGPL-3.0-or-later

package ingest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/some-du6e/pairtime/internal/server/writer"
	"github.com/some-du6e/pairtime/pkg/ledger"
)

// The SQL follows package store's portable rules, and runs inside the
// writer's transaction except for GET /api/v1/ingest/state, which reads
// through the read pool. Every write checks first and then inserts or
// updates, because the writer runs one op at a time: an op never needs a
// constraint error to find a row, and Postgres would abort the whole
// transaction on one.

// queryer is the read half of writer.Tx, which *sql.DB has too.
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// deviceState is a device's row in ingest_devices: its counter, the head
// of its witnessed chain and where the chain started, and its most recent
// processed request (ingest-v1 2.10).
type deviceState struct {
	found      bool
	req        int64
	headB      uint64 // 0 while there is no head
	headHash   ledger.Hash
	headAt     int64
	chainStart uint64
	lastKey    string
	lastSHA    string
	lastResp   []byte
}

func loadState(ctx context.Context, q queryer, deviceID string) (deviceState, error) {
	var st deviceState
	var headB, headAt, chainStart sql.NullInt64
	var headHash, lastKey, lastSHA, lastResp sql.NullString
	err := q.QueryRowContext(ctx, `SELECT req, head_b, head_hash, head_received_at_ms, chain_start, last_key, last_body_sha256, last_response
FROM ingest_devices WHERE device_id = $1`, deviceID).Scan(&st.req, &headB, &headHash, &headAt, &chainStart, &lastKey, &lastSHA, &lastResp)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return st, nil
	case err != nil:
		return st, fmt.Errorf("ingest: reading the device's state: %w", err)
	}
	st.found = true
	if headB.Valid {
		st.headB, st.headAt, st.chainStart = uint64(headB.Int64), headAt.Int64, uint64(chainStart.Int64)
		if err := st.headHash.UnmarshalText([]byte(headHash.String)); err != nil {
			return st, fmt.Errorf("ingest: the device's stored head: %w", err)
		}
	}
	st.lastKey, st.lastSHA, st.lastResp = lastKey.String, lastSHA.String, []byte(lastResp.String)
	return st, nil
}

// nullable turns a zero into SQL NULL.
func nullable[T comparable](v T) any {
	var zero T
	if v == zero {
		return nil
	}
	return v
}

func saveState(ctx context.Context, tx writer.Tx, deviceID, account string, st deviceState, now int64) error {
	var headHash any
	if st.headB != 0 {
		headHash = st.headHash.String()
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO ingest_devices
(device_id, account_id, req, head_b, head_hash, head_received_at_ms, chain_start, last_key, last_body_sha256, last_response, updated_at_ms)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (device_id) DO UPDATE SET account_id = excluded.account_id, req = excluded.req, head_b = excluded.head_b,
head_hash = excluded.head_hash, head_received_at_ms = excluded.head_received_at_ms, chain_start = excluded.chain_start,
last_key = excluded.last_key, last_body_sha256 = excluded.last_body_sha256, last_response = excluded.last_response,
updated_at_ms = excluded.updated_at_ms`,
		deviceID, account, st.req, nullable(int64(st.headB)), headHash, nullable(st.headAt), nullable(int64(st.chainStart)),
		st.lastKey, st.lastSHA, string(st.lastResp), now)
	if err != nil {
		return fmt.Errorf("ingest: storing the device's state: %w", err)
	}
	return nil
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

// storedBucket is a minute's current revision in minute_buckets.
type storedBucket struct {
	rev        int64
	receivedAt int64
	bucket     string
	earlier    string
}

// earlierRevision is one entry of minute_buckets.earlier: a revision a
// later one replaced, kept for the bundle's buckets.json
// (E-check-readings).
type earlierRevision struct {
	Rev          int64           `json:"rev"`
	ReceivedAtMS int64           `json:"received_at_ms"`
	Bucket       json.RawMessage `json:"bucket"`
}

func loadBucket(ctx context.Context, q queryer, deviceID string, minuteMS int64) (storedBucket, bool, error) {
	var s storedBucket
	err := q.QueryRowContext(ctx, `SELECT rev, received_at_ms, bucket, earlier FROM minute_buckets WHERE device_id = $1 AND minute_ms = $2`,
		deviceID, minuteMS).Scan(&s.rev, &s.receivedAt, &s.bucket, &s.earlier)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return s, false, nil
	case err != nil:
		return s, false, fmt.Errorf("ingest: reading a minute bucket: %w", err)
	}
	return s, true, nil
}

// putBucket stores bucket as the minute's current revision. When cur holds
// the revision it replaces, that one moves to the end of earlier.
func putBucket(ctx context.Context, tx writer.Tx, deviceID, account string, minuteMS int64, rev int64, state string, bucket []byte, receivedAt int64, cur *storedBucket) error {
	earlier := "[]"
	if cur != nil {
		var list []earlierRevision
		if err := json.Unmarshal([]byte(cur.earlier), &list); err != nil {
			return fmt.Errorf("ingest: a stored bucket's earlier revisions: %w", err)
		}
		list = append(list, earlierRevision{Rev: cur.rev, ReceivedAtMS: cur.receivedAt, Bucket: json.RawMessage(cur.bucket)})
		b, err := json.Marshal(list)
		if err != nil {
			return fmt.Errorf("ingest: %w", err)
		}
		earlier = string(b)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO minute_buckets (device_id, minute_ms, account_id, rev, state, received_at_ms, bucket, earlier)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (device_id, minute_ms) DO UPDATE SET account_id = excluded.account_id, rev = excluded.rev, state = excluded.state,
received_at_ms = excluded.received_at_ms, bucket = excluded.bucket, earlier = excluded.earlier`,
		deviceID, minuteMS, account, rev, state, receivedAt, string(bucket), earlier)
	if err != nil {
		return fmt.Errorf("ingest: storing a minute bucket: %w", err)
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
ON CONFLICT (device_id, sha) DO UPDATE SET account_id = excluded.account_id, project = excluded.project, body = excluded.body,
updated_at_ms = excluded.updated_at_ms`,
		deviceID, c.SHA, account, c.Project, string(body), now, now)
	if err != nil {
		return fmt.Errorf("ingest: storing a commit record: %w", err)
	}
	return nil
}

func putLabels(ctx context.Context, tx writer.Tx, deviceID, account string, body []byte, now int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO device_labels (device_id, account_id, labels, received_at_ms)
VALUES ($1, $2, $3, $4)
ON CONFLICT (device_id) DO UPDATE SET account_id = excluded.account_id, labels = excluded.labels, received_at_ms = excluded.received_at_ms`,
		deviceID, account, string(body), now)
	if err != nil {
		return fmt.Errorf("ingest: storing labels: %w", err)
	}
	return nil
}
