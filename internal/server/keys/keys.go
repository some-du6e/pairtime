// SPDX-License-Identifier: AGPL-3.0-or-later

// Package keys says which server key was valid when, and publishes the
// server's public keys with those times at /.well-known/pairtime-keys.json
// (WP-SV17; DESIGN 10.2; docs/spec/keys-v1.md).
//
// A receipt counts under the server key that was valid at its received_at,
// and a day head under the one valid at the last millisecond of its day
// (ledger-v1 7 to 9). So the server keeps a history of its keys, the table
// key_history (migration 0010), and never deletes from it: a retired key
// stays published, so what it signed keeps verifying. server_keys (0007)
// can't serve for this, since it holds only the current key's id, in one
// row a rotation overwrites.
//
// pairtimed serve records the server key in data_dir at every start
// (RecordServer), after checking it against server_keys. A key the history
// holds already changes nothing. A server's first key is valid from the
// start of the UTC day it was first used: its first receipt, or the start
// that first recorded it in server_keys, whichever is earlier. So it covers
// the receipts a release from before this history signed. A later key,
// which a rotation put in data_dir, is valid from its first receipt or the
// start that records it, whichever is earlier, and the key before it stops
// being valid the millisecond before, which must come after everything
// that key signed. At most one key is valid at any time, so a verifier
// finds it from the time alone (History.At).
//
// The evidence keys, which seal tier-2 content, have a history of their
// own in the same table under the kind "evidence" (WP-SB2; DESIGN 7.6;
// docs/spec/seal-v1.md 7). pairtimed serve records the evidence key in
// data_dir as current at every start (RecordEvidence), and refuses any
// other until WP-SB9's rotation switches keys. A Ring holds the private
// halves: it opens each blob with the key its key id names, says whether
// an upload's key is the current one, and gives the keys document its
// one evidence key.
package keys

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/some-du6e/pairtime/internal/server/backup"
	"github.com/some-du6e/pairtime/internal/server/writer"
	"github.com/some-du6e/pairtime/pkg/ledger"
)

// KindServer is key_history's kind for the keys of server.ed25519, which
// sign receipts, day heads and bundle manifests (DESIGN 10.2).
const KindServer = "server"

// Current is the ValidTo of a key that is still current: its validity has
// no end yet.
const Current int64 = math.MaxInt64

// ErrRetired is what RecordServer returns, wrapped, for a key the history
// holds as retired. pairtimed never signs with a retired key again: no
// client keeps a receipt a key signed after its validity ended (ingest-v1
// 7 item 24).
var ErrRetired = errors.New("keys: the server key in data_dir was retired")

// ServerKey is one server key and when it is valid, in Unix ms.
type ServerKey struct {
	// ID is the key's id, backup.KeyID of its public key.
	ID string
	// Public is the Ed25519 public key.
	Public ed25519.PublicKey
	// ValidFrom is the first millisecond the key is valid, and ValidTo
	// the last, both included, or Current while the key is current.
	ValidFrom, ValidTo int64
}

// ValidAt reports whether k is valid at t, Unix ms.
func (k ServerKey) ValidAt(t int64) bool { return k.ValidFrom <= t && t <= k.ValidTo }

// History is the keys a database was written with.
type History struct {
	// Server is every server key, oldest first. No two keys are valid at
	// the same millisecond.
	Server []ServerKey
	// Evidence is every evidence key, oldest first, the same way
	// (WP-SB2; docs/spec/seal-v1.md 7).
	Evidence []EvidenceKey
}

// At returns the server key that was valid at t, Unix ms, and false when
// none was.
func (h *History) At(t int64) (ServerKey, bool) {
	for _, k := range h.Server {
		if k.ValidAt(t) {
			return k, true
		}
	}
	return ServerKey{}, false
}

// Querier reads: the read pool, or a writer transaction.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Load reads the history. It refuses one that doesn't hold together: a key
// whose stored public half isn't the key its id names, a validity that
// ends before it starts, and two keys of a kind valid at once, which no
// history RecordServer and RecordEvidence write can have.
func Load(ctx context.Context, q Querier) (*History, error) {
	server, err := loadServer(ctx, q)
	if err != nil {
		return nil, err
	}
	evidence, err := loadEvidence(ctx, q)
	if err != nil {
		return nil, err
	}
	return &History{Server: server, Evidence: evidence}, nil
}

// keyRow is one key_history row: a key's id, its public key in hex, and
// the first and last millisecond it is valid, Current while it is.
type keyRow struct {
	id, public string
	from, to   int64
}

// loadKind reads the rows of kind, oldest first, and refuses a validity
// that ends before it starts and two keys of the kind valid at once. So at
// most one key of a kind is current, and it is the newest.
func loadKind(ctx context.Context, q Querier, kind string) ([]keyRow, error) {
	rows, err := q.QueryContext(ctx, `SELECT key_id, public_key, valid_from_ms, valid_to_ms FROM key_history WHERE kind = $1 ORDER BY valid_from_ms, key_id`, kind)
	if err != nil {
		return nil, fmt.Errorf("keys: reading the key history: %w", err)
	}
	defer rows.Close()
	var out []keyRow
	for rows.Next() {
		var (
			r  keyRow
			to sql.NullInt64
		)
		if err := rows.Scan(&r.id, &r.public, &r.from, &to); err != nil {
			return nil, fmt.Errorf("keys: reading the key history: %w", err)
		}
		r.to = Current
		if to.Valid {
			r.to = to.Int64
		}
		if r.to < r.from {
			return nil, fmt.Errorf("keys: the key history has %s key %s valid until %s, before it became valid at %s", kind, r.id, msTime(r.to), msTime(r.from))
		}
		if n := len(out); n > 0 && out[n-1].to >= r.from {
			return nil, fmt.Errorf("keys: the key history has %s keys %s and %s both valid at %s", kind, out[n-1].id, r.id, msTime(r.from))
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("keys: reading the key history: %w", err)
	}
	return out, nil
}

// loadServer reads the server keys, oldest first, and checks them as Load
// says.
func loadServer(ctx context.Context, q Querier) ([]ServerKey, error) {
	rows, err := loadKind(ctx, q, KindServer)
	if err != nil {
		return nil, err
	}
	var out []ServerKey
	for _, r := range rows {
		b, err := hex.DecodeString(r.public)
		if err != nil || len(b) != ed25519.PublicKeySize || backup.KeyID(b) != r.id {
			return nil, fmt.Errorf("keys: the key history's public key for server key %s isn't that key", r.id)
		}
		out = append(out, ServerKey{ID: r.id, Public: ed25519.PublicKey(b), ValidFrom: r.from, ValidTo: r.to})
	}
	return out, nil
}

// RecordServer makes the history hold pub, the server key in data_dir, as
// the current server key, inside tx at now, Unix ms.
//
// A key the history holds as current changes nothing, and one it holds as
// retired is refused with ErrRetired. A server's first key is valid from
// the start of the UTC day of its first use: the earliest of its first
// receipt in witness_rows, the start that recorded it in server_keys, and
// now. The day and not the millisecond, because the document is public and
// the first receipt's time says when one person first uploaded.
//
// A later key is valid from the earlier of its first receipt and now, and
// the current key is retired the millisecond before. It is refused when
// that would leave something the current key signed outside its validity:
// a receipt at that millisecond or after, or a day head of a day that
// hadn't ended. Two keys are then never valid at once, and every receipt
// and day head stays under the key that signed it.
func RecordServer(ctx context.Context, tx writer.Tx, pub ed25519.PublicKey, now int64) error {
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("keys: a server key is %d bytes, not %d", ed25519.PublicKeySize, len(pub))
	}
	id := backup.KeyID(pub)
	have, err := loadServer(ctx, tx)
	if err != nil {
		return err
	}
	for _, k := range have {
		switch {
		case k.ID != id:
		case !k.Public.Equal(pub):
			return fmt.Errorf("keys: the key history holds another public key under server key %s's id", id)
		case k.ValidTo != Current:
			return fmt.Errorf("%w: server key %s stopped being valid at %s. Restore the current keys from their backup (deploy/runbook.md, Restoring the keys)", ErrRetired, id, msTime(k.ValidTo))
		default:
			return nil
		}
	}
	from := now
	first, err := firstReceipt(ctx, tx, id)
	if err != nil {
		return err
	}
	if first.Valid && first.Int64 < from {
		from = first.Int64
	}
	if len(have) == 0 {
		var recorded int64
		err := tx.QueryRowContext(ctx, `SELECT recorded_at_ms FROM server_keys WHERE id = 1 AND server_key_id = $1`, id).Scan(&recorded)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return fmt.Errorf("keys: reading when server key %s was recorded: %w", id, err)
		case recorded < from:
			from = recorded
		}
		from -= from % ledger.DayMS
	}
	for _, k := range have {
		last := k.ValidTo
		if k.ValidTo == Current {
			if last, err = lastUse(ctx, tx, k); err != nil {
				return err
			}
		}
		if last >= from {
			return fmt.Errorf("keys: server key %s would be valid from %s, and server key %s was valid or signed until %s, so the history can't take it: two keys would be valid at once. Check the server's clock", id, msTime(from), k.ID, msTime(last))
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE key_history SET valid_to_ms = $1 WHERE kind = $2 AND valid_to_ms IS NULL`, from-1, KindServer); err != nil {
		return fmt.Errorf("keys: retiring the current server key: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO key_history (kind, key_id, public_key, valid_from_ms, valid_to_ms, recorded_at_ms) VALUES ($1, $2, $3, $4, NULL, $5)`,
		KindServer, id, hex.EncodeToString(pub), from, now); err != nil {
		return fmt.Errorf("keys: recording server key %s: %w", id, err)
	}
	return nil
}

// firstReceipt returns when key id signed its first receipt in
// witness_rows, or NULL when it signed none there.
func firstReceipt(ctx context.Context, q Querier, id string) (sql.NullInt64, error) {
	var t sql.NullInt64
	if err := q.QueryRowContext(ctx, `SELECT MIN(received_at_ms) FROM witness_rows WHERE receipt_key_id = $1`, id).Scan(&t); err != nil {
		return t, fmt.Errorf("keys: finding server key %s's first receipt: %w", id, err)
	}
	return t, nil
}

// lastUse returns the last millisecond k, the current key, must stay valid
// at: its last receipt in witness_rows, or the last millisecond of the
// last day whose head it signed, whichever is later, and never before it
// became valid.
func lastUse(ctx context.Context, q Querier, k ServerKey) (int64, error) {
	last := k.ValidFrom
	var receipt, day sql.NullInt64
	if err := q.QueryRowContext(ctx, `SELECT MAX(received_at_ms) FROM witness_rows WHERE receipt_key_id = $1`, k.ID).Scan(&receipt); err != nil {
		return 0, fmt.Errorf("keys: finding server key %s's last receipt: %w", k.ID, err)
	}
	if err := q.QueryRowContext(ctx, `SELECT MAX(day) FROM day_heads WHERE key_id = $1`, k.ID).Scan(&day); err != nil {
		return 0, fmt.Errorf("keys: finding the last day server key %s signed: %w", k.ID, err)
	}
	if receipt.Valid && receipt.Int64 > last {
		last = receipt.Int64
	}
	if day.Valid {
		if end := (day.Int64+1)*ledger.DayMS - 1; end > last {
			last = end
		}
	}
	return last, nil
}

// msTime formats t, Unix ms, for an error message.
func msTime(t int64) string {
	if t == Current {
		return "no end"
	}
	return time.UnixMilli(t).UTC().Format("2006-01-02T15:04:05.000Z")
}
