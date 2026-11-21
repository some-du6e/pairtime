// SPDX-License-Identifier: AGPL-3.0-or-later

package device

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/some-du6e/pairtime/internal/server/writer"
	"github.com/some-du6e/pairtime/pkg/ledger"
)

// State is ingest's state for one device. It is kept on the device's row,
// so it lives exactly as long as the device does, removed or not
// (AGENTS.md, Retention; D-device-row-chain-state; DESIGN 4.4). Package
// ingest reads it with LoadState and writes it with SaveState, inside its
// own writer transaction, and never with SQL against devices
// (docs/spec/device-auth-v1.md section 9). A device ingest hasn't accepted
// anything from has the zero State but for Generation.
type State struct {
	// Generation is the device's link generation, as on Device. Only a
	// link changes it, so SaveState leaves it alone.
	Generation int64
	// Req is the X-Pairtime-Req counter of the last request ingest
	// accepted from the device, 0 before the first.
	Req int64
	// HeadB and HeadHash are b and H_b of the chain's head, the last batch
	// root ingest stored. HeadB is 0 while there is none.
	HeadB    uint64
	HeadHash ledger.Hash
	// HeadGeneration is the link generation the head was stored under.
	// While it is below Generation, the device has been linked again since
	// the head was stored, so its next batch may have any b above HeadB,
	// with its prev taken as given, and ingest records the batch numbers
	// it skipped as a Gap (D-local-gap; ingest-v1 5.3). Without a new link
	// a skipped batch is still a chain gap.
	HeadGeneration int64
	// HeadReceivedAt is when the head arrived.
	HeadReceivedAt time.Time
	// ChainStart is b of the first batch root ingest stored for the device.
	ChainStart uint64
	// Gaps are the batches the device sealed in local-only mode between
	// two links, in order. Their roots were never sent, and ingest never
	// asks for them or takes them (D-local-gap; DESIGN 4.2).
	Gaps []Gap
}

// Gap is the batch numbers First to Last, both included.
type Gap struct {
	First uint64 `json:"first"`
	Last  uint64 `json:"last"`
}

// stateColumns are the columns LoadState reads, in its order.
const stateColumns = `link_generation, ingest_req, ingest_head_b, ingest_head_hash, ingest_head_generation,
ingest_head_received_at_ms, ingest_chain_start, ingest_gaps`

// LoadState returns d's ingest state, read inside tx, the writer
// transaction ingest stores a request in. d comes from Authenticate. When
// its row no longer holds the token Authenticate found it by, because the
// device was removed or linked again since, LoadState returns ErrRevoked,
// so a request can't land on a device that changed under it.
func (s *Store) LoadState(ctx context.Context, tx writer.Tx, d Device) (State, error) {
	var (
		st                                 State
		req, headB, headGen, headAt, start sql.NullInt64
		headHash, gaps                     sql.NullString
	)
	err := tx.QueryRowContext(ctx, `SELECT `+stateColumns+`
FROM devices WHERE device_id = $1 AND token_hash = $2 AND removed_at_ms IS NULL`, d.ID, d.tokenHash).Scan(
		&st.Generation, &req, &headB, &headHash, &headGen, &headAt, &start, &gaps)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return State{}, ErrRevoked
	case err != nil:
		return State{}, fmt.Errorf("device: reading the device's ingest state: %w", err)
	}
	st.Req = req.Int64
	if headB.Valid && headB.Int64 > 0 {
		st.HeadB = uint64(headB.Int64)
		if err := st.HeadHash.UnmarshalText([]byte(headHash.String)); err != nil {
			return State{}, fmt.Errorf("device: the device's stored head: %w", err)
		}
		st.HeadGeneration = headGen.Int64
		st.HeadReceivedAt = time.UnixMilli(headAt.Int64)
		st.ChainStart = uint64(start.Int64)
	}
	if gaps.Valid && gaps.String != "" {
		if err := json.Unmarshal([]byte(gaps.String), &st.Gaps); err != nil {
			return State{}, fmt.Errorf("device: the device's stored gaps: %w", err)
		}
	}
	return st, nil
}

// SaveState stores st as d's ingest state inside tx, under the same
// ErrRevoked rule as LoadState. It leaves the device's link generation
// alone, and refuses a state that can't be right: a head below the chain's
// start or stored under a link generation d hasn't had, or a gap outside
// the chain or out of order.
func (s *Store) SaveState(ctx context.Context, tx writer.Tx, d Device, st State) error {
	if err := st.check(d.Generation); err != nil {
		return err
	}
	// Nil interfaces, not typed zeros, so every driver writes NULL.
	var headB, headHash, headGen, headAt, start, gaps any
	if st.HeadB > 0 {
		headB, headHash, headGen = int64(st.HeadB), st.HeadHash.String(), st.HeadGeneration
		headAt, start = st.HeadReceivedAt.UnixMilli(), int64(st.ChainStart)
	}
	if len(st.Gaps) > 0 {
		b, err := json.Marshal(st.Gaps)
		if err != nil {
			return fmt.Errorf("device: writing the device's gaps: %w", err)
		}
		gaps = string(b)
	}
	res, err := tx.ExecContext(ctx, `UPDATE devices
SET ingest_req = $3, ingest_head_b = $4, ingest_head_hash = $5, ingest_head_generation = $6,
ingest_head_received_at_ms = $7, ingest_chain_start = $8, ingest_gaps = $9
WHERE device_id = $1 AND token_hash = $2 AND removed_at_ms IS NULL`,
		d.ID, d.tokenHash, st.Req, headB, headHash, headGen, headAt, start, gaps)
	if err != nil {
		return fmt.Errorf("device: storing the device's ingest state: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("device: storing the device's ingest state: %w", err)
	}
	if n == 0 {
		return ErrRevoked
	}
	return nil
}

// check returns what is wrong with st for a device at link generation
// gen, or nil.
func (st State) check(gen int64) error {
	bad := func(what string) error { return fmt.Errorf("device: an ingest state with %s", what) }
	switch {
	case st.Req < 0:
		return bad("a negative request counter")
	case st.HeadB > math.MaxInt64 || st.ChainStart > math.MaxInt64:
		return bad("a batch number the database can't hold")
	case st.HeadB == 0 && (st.ChainStart != 0 || st.HeadGeneration != 0 || len(st.Gaps) > 0):
		return bad("a chain but no head")
	case st.HeadB == 0:
		return nil
	case st.ChainStart == 0 || st.ChainStart > st.HeadB:
		return bad("a chain start that isn't at or below its head")
	case st.HeadGeneration < 1 || st.HeadGeneration > gen:
		return bad("a head stored under a link generation the device hasn't had")
	case st.HeadReceivedAt.IsZero():
		return bad("a head without the time it arrived")
	}
	after := st.ChainStart
	for _, g := range st.Gaps {
		if g.First <= after || g.First > g.Last || g.Last >= st.HeadB {
			return bad("gaps that aren't in order inside the chain")
		}
		after = g.Last
	}
	return nil
}
