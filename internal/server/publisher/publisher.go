// SPDX-License-Identifier: AGPL-3.0-or-later

// Package publisher publishes pairtimed's day roots (WP-SV17; DESIGN 7.4;
// ledger-v1 8): for each UTC day, one signed head over every batch the
// server stored that day, chained to the day before, written as a file that
// the witness sidecar commits to the public pairtime-witness repository
// (deploy/witness/push.sh).
//
// # The chain
//
// The chain starts at d_0, the day of the oldest witness row, and every
// day from there gets a head, an empty day too, so it has no gaps. A day
// is due once it has been over for Delay. Run publishes at start and at
// 00:10 UTC each day, so a day missed while the server was down goes out on
// the next run. Rows are read by received_at_ms and put in day order in Go
// (ledger.BuildDay), never by an ORDER BY, whose collation would order
// device ids otherwise and publish another T_d for good. Each head is
// signed with the server key valid at its day's last millisecond
// (keys.History), which may be older than the key that signs receipts now.
//
// The chain resumes from day_heads (migration 0010), which Litestream
// replicates with the rest of the database, and never from the files,
// which nothing backs up. Each head is stored through the writer before its
// file is written, so no file names a head the database lacks. A head
// follows from its day's rows and the key alone, and Ed25519 signatures
// are deterministic, so a database restored from a replica that lost the
// newest head stores the same head again. Delay is what makes that hold:
// when a day is published, its last rows are minutes old, and the replica
// has them.
//
// # The files
//
// Day d's file is FileName(d) under the directory, /witness in the deploy
// bundle. It is written under a name that starts with a dot in its final
// directory, synced, renamed, and the directory synced, since a rename into
// /witness from another mount can't be atomic and the sidecar publishes no
// dot-name. So no other name ever holds part of a root. A file that is
// there already is never rewritten, and each run writes the file of every
// stored head that has none, as on a new host whose /witness is empty.
//
// # Inclusion proofs
//
// Each head is stored with its day's leaves, the hashes T_d is the root of,
// in day_leaves, and kept for good. Proof gives a stored batch's leaf in
// its day's tree and the leaf's inclusion proof from them, which a bundle
// carries (DESIGN 7.8; WP-SB8), so a batch's proof outlives the day's other
// witness rows, which retention and an account's erasure delete.
package publisher

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/some-du6e/pairtime/internal/server/keys"
	"github.com/some-du6e/pairtime/internal/server/writer"
	"github.com/some-du6e/pairtime/pkg/ledger"
)

// Delay is how long after a UTC day ends its head is due: the publisher
// runs at 00:10 UTC.
const Delay = 10 * time.Minute

// RetryEvery is how soon Run tries again after a run that failed.
const RetryEvery = time.Hour

// Doer runs an op through the writer, as *writer.Writer does.
type Doer interface {
	Do(ctx context.Context, rows int, op writer.Op) error
}

// Querier reads: the read pool, or a writer transaction.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Options are what the publisher works with.
type Options struct {
	// Writer stores the heads, and Read reads the witness rows and the
	// heads.
	Writer Doer
	Read   Querier
	// History says which server key was valid when.
	History *keys.History
	// Keys are the server's private keys by key id: the current one, and
	// any retired key kept until it has signed every day that ended while
	// it was valid (ledger-v1 8).
	Keys map[string]ed25519.PrivateKey
	// Dir is where the files go. With none, the heads are stored and no
	// file is written.
	Dir string

	// create opens a file's temporary name for writing; tests stand in
	// for os.OpenFile.
	create func(name string) (file, error)
}

// Result says what one run did.
type Result struct {
	// Stored are the days whose heads the run stored, oldest first.
	Stored []uint64
	// Written are the days whose files the run wrote, oldest first.
	Written []uint64
}

// Day is a stored day head, a row of day_heads: what the server signed for
// the day, which key signed it, and when the publisher stored it.
type Day struct {
	ledger.PublishedDay
	// KeyID names the server key that signed it, the one valid at the
	// day's last millisecond.
	KeyID string
	// WrittenAt is when the publisher stored it, Unix ms.
	WrittenAt int64
}

// LastDue returns the last day that is due at now: the last that ended
// Delay or more before now. ok is false before any day is due.
func LastDue(now time.Time) (d uint64, ok bool) {
	t := now.Add(-Delay).UnixMilli()
	if t < ledger.DayMS {
		return 0, false
	}
	return ledger.DayOf(uint64(t)) - 1, true
}

// NextRun returns when Run publishes next after now: Delay past the next
// UTC midnight.
func NextRun(now time.Time) time.Time {
	next := now.UTC().Truncate(24 * time.Hour).Add(Delay)
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}

// Run publishes at once, then at Delay past each UTC midnight until ctx
// ends, and RetryEvery after a run that failed. report, when set, hears
// each run's result and error.
func Run(ctx context.Context, o Options, now func() time.Time, report func(Result, error)) {
	for {
		res, err := Publish(ctx, o, now())
		if ctx.Err() != nil {
			return
		}
		if report != nil {
			report(res, err)
		}
		wait := NextRun(now()).Sub(now())
		if err != nil {
			wait = min(wait, RetryEvery)
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

// Publish stores the head of every day due at now that has none, oldest
// first, and then writes the file of every stored head that has none
// under o.Dir. A day it can't publish stops the chain there, since each
// head needs the one before, and the files of the heads before it are
// written all the same.
func Publish(ctx context.Context, o Options, now time.Time) (Result, error) {
	var res Result
	var errs []error
	if err := publishDue(ctx, o, now, &res); err != nil {
		errs = append(errs, err)
	}
	if o.Dir != "" {
		written, err := writeFiles(ctx, o)
		res.Written = written
		if err != nil {
			errs = append(errs, err)
		}
	}
	return res, errors.Join(errs...)
}

// publishDue stores the heads of the days due at now, oldest first, into
// res.Stored, and stops at the first day it can't publish.
func publishDue(ctx context.Context, o Options, now time.Time, res *Result) error {
	last, ok := LastDue(now)
	if !ok {
		return nil
	}
	d, prev, ok, err := next(ctx, o.Read)
	if err != nil || !ok {
		return err
	}
	for ; d <= last; d++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		head, err := publishDay(ctx, o, d, prev, now)
		if err != nil {
			return err
		}
		res.Stored = append(res.Stored, d)
		prev = head
	}
	return nil
}

// Pending returns the days due at now that have no head yet, first to
// last, which a healthy publisher leaves none of once its run at now is
// done; ok is false when there are none. pairtimed doctor asks it.
func Pending(ctx context.Context, q Querier, now time.Time) (first, last uint64, ok bool, err error) {
	last, due := LastDue(now)
	if !due {
		return 0, 0, false, nil
	}
	first, _, ok, err = next(ctx, q)
	if err != nil || !ok || first > last {
		return 0, 0, false, err
	}
	return first, last, true, nil
}

// next returns the next day to publish and the head before it: the day
// after the newest stored head, and that head, or, before the first, d_0,
// the day of the oldest witness row, and zero(32). ok is false while
// there is neither a head nor a witness row.
func next(ctx context.Context, q Querier) (d uint64, prev ledger.Hash, ok bool, err error) {
	newest, ok, err := Newest(ctx, q)
	if err != nil {
		return 0, prev, false, err
	}
	if ok {
		return newest.D + 1, newest.Head, true, nil
	}
	d, ok, err = FirstDay(ctx, q)
	return d, prev, ok, err
}

// FirstDay returns the day of the oldest witness row, which is d_0 until
// the first head is stored; ok is false while there is no witness row.
func FirstDay(ctx context.Context, q Querier) (d uint64, ok bool, err error) {
	var first sql.NullInt64
	if err := q.QueryRowContext(ctx, `SELECT MIN(received_at_ms) FROM witness_rows`).Scan(&first); err != nil {
		return 0, false, fmt.Errorf("publisher: finding the first witness row: %w", err)
	}
	switch {
	case !first.Valid:
		return 0, false, nil
	case first.Int64 < 0:
		return 0, false, fmt.Errorf("publisher: a witness row was received at %d, before 1970", first.Int64)
	}
	return ledger.DayOf(uint64(first.Int64)), true, nil
}

// DueAt returns when day d's head is due: Delay after the day ends.
func DueAt(d uint64) time.Time {
	if d >= math.MaxInt64/ledger.DayMS-1 {
		return time.UnixMilli(math.MaxInt64).UTC()
	}
	return time.UnixMilli(int64(d+1) * ledger.DayMS).UTC().Add(Delay)
}

// publishDay builds day d's tree from its witness rows, chained to prev,
// signs its head with the key valid at the day's last millisecond, stores
// it through the writer, and returns G_d.
func publishDay(ctx context.Context, o Options, d uint64, prev ledger.Hash, now time.Time) (ledger.Hash, error) {
	rows, err := DayRows(ctx, o.Read, d)
	if err != nil {
		return ledger.Hash{}, err
	}
	day, err := ledger.BuildDay(d, prev, rows)
	if err != nil {
		return ledger.Hash{}, fmt.Errorf("publisher: day %s: %w", Date(d), err)
	}
	key, id, err := o.signer(d)
	if err != nil {
		return ledger.Hash{}, err
	}
	p, err := day.Publish(key)
	if err != nil {
		return ledger.Hash{}, fmt.Errorf("publisher: signing day %s: %w", Date(d), err)
	}
	leaves, err := leafHashes(day.Rows)
	if err != nil {
		return ledger.Hash{}, fmt.Errorf("publisher: day %s: %w", Date(d), err)
	}
	h := Day{PublishedDay: p, KeyID: id, WrittenAt: now.UnixMilli()}
	if err := o.Writer.Do(ctx, 2, func(ctx context.Context, tx writer.Tx) error { return storeHead(ctx, tx, h, leaves) }); err != nil {
		return ledger.Hash{}, err
	}
	return p.Head, nil
}

// leafHashes returns LEAF of each row's leaf, in the rows' order, joined:
// what day_leaves keeps of a day.
func leafHashes(rows []ledger.DayRow) ([]byte, error) {
	out := make([]byte, 0, ledger.HashSize*len(rows))
	for _, r := range rows {
		leaf, err := r.Leaf()
		if err != nil {
			return nil, err
		}
		h := ledger.LeafHash(leaf)
		out = append(out, h[:]...)
	}
	return out, nil
}

// signer returns the private key that signs day d's head, the server key
// valid at the day's last millisecond whenever the head is signed
// (ledger-v1 8), and its id.
func (o Options) signer(d uint64) (ed25519.PrivateKey, string, error) {
	end, ok := ledger.DayEnd(d)
	if !ok || end > math.MaxInt64 {
		return nil, "", fmt.Errorf("publisher: day %d ends past what a time can say", d)
	}
	if o.History == nil {
		return nil, "", errors.New("publisher: there is no key history to pick a day's key from")
	}
	k, ok := o.History.At(int64(end))
	if !ok {
		return nil, "", fmt.Errorf("publisher: no server key was valid at the end of day %s, so nothing can sign its head; the keys document says when each key is valid (docs/spec/keys-v1.md)", Date(d))
	}
	priv, ok := o.Keys[k.ID]
	if !ok {
		return nil, "", fmt.Errorf("publisher: day %s's head needs server key %s, which was valid at the day's end, and this server doesn't hold it. A retired key is kept until it has signed every day that ended while it was valid (ledger-v1 8)", Date(d), k.ID)
	}
	if pub, isEd := priv.Public().(ed25519.PublicKey); !isEd || !pub.Equal(k.Public) {
		return nil, "", fmt.Errorf("publisher: the private key held for server key %s isn't that key", k.ID)
	}
	return priv, k.ID, nil
}

// storeHead stores h inside tx as the next head of the chain, with leaves,
// its day's leaf hashes joined in leaf order: the first, with a zero prev,
// while day_heads is empty, and otherwise the day after the newest, linked
// to its head. A head already stored for h's day must be h, and stays as
// it was stored. A day with no rows has no day_leaves row.
func storeHead(ctx context.Context, tx writer.Tx, h Day, leaves []byte) error {
	newest, ok, err := Newest(ctx, tx)
	if err != nil {
		return err
	}
	switch {
	case ok && newest.D == h.D:
		if newest.PublishedDay != h.PublishedDay || newest.KeyID != h.KeyID {
			return fmt.Errorf("publisher: day %s has a stored head other than the one just signed", Date(h.D))
		}
		return nil
	case ok && (newest.D+1 != h.D || newest.Head != h.Prev):
		return fmt.Errorf("publisher: day %s's head doesn't follow the newest stored head, day %s's", Date(h.D), Date(newest.D))
	case !ok && h.Prev != ledger.Hash{}:
		return fmt.Errorf("publisher: day %s's head would be the first, and its prev isn't zero", Date(h.D))
	}
	if h.D > math.MaxInt64 || h.Count > math.MaxInt64 {
		return fmt.Errorf("publisher: day %d doesn't fit in the database", h.D)
	}
	if uint64(len(leaves)) != h.Count*ledger.HashSize {
		return fmt.Errorf("publisher: day %s has %d rows and %d bytes of leaves", Date(h.D), h.Count, len(leaves))
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO day_heads (day, n, root, prev, head, signature, key_id, written_at_ms) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		int64(h.D), int64(h.Count), h.Root.String(), h.Prev.String(), h.Head.String(), hex.EncodeToString(h.Signature[:]), h.KeyID, h.WrittenAt)
	if err != nil {
		return fmt.Errorf("publisher: storing day %s's head: %w", Date(h.D), err)
	}
	if h.Count == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO day_leaves (day, leaves) VALUES ($1, $2)`, int64(h.D), leaves); err != nil {
		return fmt.Errorf("publisher: storing day %s's leaves: %w", Date(h.D), err)
	}
	return nil
}

// DayRows returns the witness rows the server stored on day d, in no
// order: ledger.BuildDay puts them in day order.
func DayRows(ctx context.Context, q Querier, d uint64) ([]ledger.DayRow, error) {
	end, ok := ledger.DayEnd(d)
	if !ok || end >= math.MaxInt64 {
		return nil, fmt.Errorf("publisher: day %d ends past what a time can say", d)
	}
	rows, err := q.QueryContext(ctx, `SELECT device_id, b, head, received_at_ms FROM witness_rows WHERE received_at_ms >= $1 AND received_at_ms <= $2`,
		int64(d*ledger.DayMS), int64(end))
	if err != nil {
		return nil, fmt.Errorf("publisher: reading day %s's witness rows: %w", Date(d), err)
	}
	defer rows.Close()
	var out []ledger.DayRow
	for rows.Next() {
		var (
			r     ledger.DayRow
			b, at int64
			head  string
		)
		if err := rows.Scan(&r.DeviceID, &b, &head, &at); err != nil {
			return nil, fmt.Errorf("publisher: reading day %s's witness rows: %w", Date(d), err)
		}
		if b < 1 || at < 0 {
			return nil, fmt.Errorf("publisher: witness row %d of %s has received_at %d", b, r.DeviceID, at)
		}
		if err := r.Head.UnmarshalText([]byte(head)); err != nil {
			return nil, fmt.Errorf("publisher: witness row %d of %s: %w", b, r.DeviceID, err)
		}
		r.B, r.ReceivedAt = uint64(b), uint64(at)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("publisher: reading day %s's witness rows: %w", Date(d), err)
	}
	return out, nil
}

// headColumns are a day_heads row's columns, in scanHead's order.
const headColumns = `day, n, root, prev, head, signature, key_id, written_at_ms`

// scanHead reads one day_heads row of headColumns through scan.
func scanHead(scan func(dest ...any) error) (Day, error) {
	var (
		h                     Day
		day, n                int64
		root, prev, head, sig string
	)
	if err := scan(&day, &n, &root, &prev, &head, &sig, &h.KeyID, &h.WrittenAt); err != nil {
		return h, err
	}
	if day < 0 || n < 0 {
		return h, fmt.Errorf("publisher: day_heads has day %d with %d rows", day, n)
	}
	h.D, h.Count = uint64(day), uint64(n)
	for _, f := range []struct {
		dst *ledger.Hash
		src string
	}{{&h.Root, root}, {&h.Prev, prev}, {&h.Head, head}} {
		if err := f.dst.UnmarshalText([]byte(f.src)); err != nil {
			return h, fmt.Errorf("publisher: day %s's stored head: %w", Date(h.D), err)
		}
	}
	b, err := hex.DecodeString(sig)
	if err != nil || len(b) != ledger.SignatureSize {
		return h, fmt.Errorf("publisher: day %s's stored signature isn't %d bytes in hex", Date(h.D), ledger.SignatureSize)
	}
	copy(h.Signature[:], b)
	return h, nil
}

// Newest returns the newest stored day head; ok is false before the
// first.
func Newest(ctx context.Context, q Querier) (h Day, ok bool, err error) {
	h, err = scanHead(q.QueryRowContext(ctx, `SELECT `+headColumns+` FROM day_heads ORDER BY day DESC LIMIT 1`).Scan)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Day{}, false, nil
	case err != nil:
		return Day{}, false, fmt.Errorf("publisher: reading the newest day head: %w", err)
	}
	return h, true, nil
}

// Head returns day d's stored head; ok is false when it has none.
func Head(ctx context.Context, q Querier, d uint64) (h Day, ok bool, err error) {
	if d > math.MaxInt64 {
		return Day{}, false, nil
	}
	h, err = scanHead(q.QueryRowContext(ctx, `SELECT `+headColumns+` FROM day_heads WHERE day = $1`, int64(d)).Scan)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Day{}, false, nil
	case err != nil:
		return Day{}, false, fmt.Errorf("publisher: reading day %s's head: %w", Date(d), err)
	}
	return h, true, nil
}

// Heads returns every stored day head, oldest first.
func Heads(ctx context.Context, q Querier) ([]Day, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+headColumns+` FROM day_heads ORDER BY day`)
	if err != nil {
		return nil, fmt.Errorf("publisher: reading the day heads: %w", err)
	}
	defer rows.Close()
	var out []Day
	for rows.Next() {
		h, err := scanHead(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("publisher: reading the day heads: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("publisher: reading the day heads: %w", err)
	}
	return out, nil
}

// Date returns day d's UTC date, YYYY-MM-DD.
func Date(d uint64) string {
	if d > math.MaxInt64/ledger.DayMS {
		return fmt.Sprintf("day %d", d)
	}
	return time.UnixMilli(int64(d) * ledger.DayMS).UTC().Format(time.DateOnly)
}
