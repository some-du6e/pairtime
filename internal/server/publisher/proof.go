// SPDX-License-Identifier: AGPL-3.0-or-later

package publisher

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/some-du6e/pairtime/pkg/ledger"
)

// ErrNoBatch is what Proof returns, wrapped, for a batch the server holds
// no witness row of.
var ErrNoBatch = errors.New("publisher: the server holds no witness row of that batch")

// ErrNotPublished is what Proof returns, wrapped, for a batch whose day
// has no stored head yet.
var ErrNotPublished = errors.New("publisher: the batch's day has no head yet")

// Inclusion is a stored batch's leaf in its day's published tree, which a
// bundle carries (DESIGN 7.8; WP-SB8).
type Inclusion struct {
	// Day is the day's stored head: T_d, count_d, G_d and the rest.
	Day Day
	// Index is the batch's leaf in the day's tree, from 0.
	Index uint64
	// Path is the leaf's inclusion proof (ledger-v1 5.2), which verifies
	// against Day.Root with Day.Count leaves.
	Path []ledger.Hash
}

// Proof returns the inclusion of batch b of device in its day's published
// tree. It rebuilds the tree from the day's witness rows and checks it
// against the stored head, so it fails once a row of that day is gone or
// changed, rather than give a proof the published root doesn't take.
func Proof(ctx context.Context, q Querier, device string, b uint64) (Inclusion, error) {
	if b > math.MaxInt64 {
		return Inclusion{}, fmt.Errorf("%w: batch %d of %s", ErrNoBatch, b, device)
	}
	var at int64
	err := q.QueryRowContext(ctx, `SELECT received_at_ms FROM witness_rows WHERE device_id = $1 AND b = $2`, device, int64(b)).Scan(&at)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Inclusion{}, fmt.Errorf("%w: batch %d of %s", ErrNoBatch, b, device)
	case err != nil:
		return Inclusion{}, fmt.Errorf("publisher: reading batch %d of %s: %w", b, device, err)
	case at < 0:
		return Inclusion{}, fmt.Errorf("publisher: batch %d of %s was received at %d", b, device, at)
	}
	d := ledger.DayOf(uint64(at))
	head, ok, err := Head(ctx, q, d)
	if err != nil {
		return Inclusion{}, err
	}
	if !ok {
		return Inclusion{}, fmt.Errorf("%w: batch %d of %s came on %s", ErrNotPublished, b, device, Date(d))
	}
	rows, err := DayRows(ctx, q, d)
	if err != nil {
		return Inclusion{}, err
	}
	day, err := ledger.BuildDay(d, head.Prev, rows)
	if err != nil {
		return Inclusion{}, fmt.Errorf("publisher: day %s: %w", Date(d), err)
	}
	if day.Root != head.Root || day.Count() != head.Count || day.Head != head.Head {
		return Inclusion{}, fmt.Errorf("publisher: day %s's witness rows no longer give its published root: %d rows, and the head counts %d", Date(d), day.Count(), head.Count)
	}
	i := slices.IndexFunc(day.Rows, func(r ledger.DayRow) bool { return r.DeviceID == device && r.B == b })
	if i < 0 {
		return Inclusion{}, fmt.Errorf("%w: batch %d of %s", ErrNoBatch, b, device)
	}
	path, err := day.Proof(i)
	if err != nil {
		return Inclusion{}, fmt.Errorf("publisher: day %s: %w", Date(d), err)
	}
	return Inclusion{Day: head, Index: uint64(i), Path: path}, nil
}
