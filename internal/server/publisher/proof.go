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

// ErrNotInTree is what Proof returns, wrapped, for a batch the server
// stored on a day after it published the day, which only a clock set back
// can do. The day's tree stays as published, without it.
var ErrNotInTree = errors.New("publisher: the batch isn't in its day's published tree")

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
// tree. It reads the batch's own witness row and the day's stored leaves,
// never the day's other rows, so the proof stands after retention or an
// account's erasure deletes them.
func Proof(ctx context.Context, q Querier, device string, b uint64) (Inclusion, error) {
	if b > math.MaxInt64 {
		return Inclusion{}, fmt.Errorf("%w: batch %d of %s", ErrNoBatch, b, device)
	}
	var (
		head string
		at   int64
	)
	err := q.QueryRowContext(ctx, `SELECT head, received_at_ms FROM witness_rows WHERE device_id = $1 AND b = $2`, device, int64(b)).Scan(&head, &at)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Inclusion{}, fmt.Errorf("%w: batch %d of %s", ErrNoBatch, b, device)
	case err != nil:
		return Inclusion{}, fmt.Errorf("publisher: reading batch %d of %s: %w", b, device, err)
	case at < 0:
		return Inclusion{}, fmt.Errorf("publisher: batch %d of %s was received at %d", b, device, at)
	}
	r := ledger.DayRow{DeviceID: device, B: b, ReceivedAt: uint64(at)}
	if err := r.Head.UnmarshalText([]byte(head)); err != nil {
		return Inclusion{}, fmt.Errorf("publisher: batch %d of %s: %w", b, device, err)
	}
	leaf, err := r.Leaf()
	if err != nil {
		return Inclusion{}, fmt.Errorf("publisher: batch %d of %s: %w", b, device, err)
	}
	d := ledger.DayOf(r.ReceivedAt)
	dh, ok, err := Head(ctx, q, d)
	if err != nil {
		return Inclusion{}, err
	}
	if !ok {
		return Inclusion{}, fmt.Errorf("%w: batch %d of %s came on %s", ErrNotPublished, b, device, Date(d))
	}
	leaves, err := Leaves(ctx, q, dh)
	if err != nil {
		return Inclusion{}, err
	}
	i := slices.Index(leaves, ledger.LeafHash(leaf))
	if i < 0 {
		return Inclusion{}, fmt.Errorf("%w: batch %d of %s came on %s after that day was published", ErrNotInTree, b, device, Date(d))
	}
	path, err := ledger.InclusionProof(i, leaves)
	if err != nil {
		return Inclusion{}, fmt.Errorf("publisher: day %s: %w", Date(d), err)
	}
	return Inclusion{Day: dh, Index: uint64(i), Path: path}, nil
}

// Leaves returns the leaves of h's day, LEAF of each row's leaf in leaf
// order, as day_leaves keeps them: none for a day with no rows. It checks
// them against h first: count_d of them, with T_d their root.
func Leaves(ctx context.Context, q Querier, h Day) ([]ledger.Hash, error) {
	leaves := []ledger.Hash{}
	if h.Count > 0 {
		if h.D > math.MaxInt64 {
			return nil, fmt.Errorf("publisher: day %d doesn't fit in the database", h.D)
		}
		var blob []byte
		err := q.QueryRowContext(ctx, `SELECT leaves FROM day_leaves WHERE day = $1`, int64(h.D)).Scan(&blob)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return nil, fmt.Errorf("publisher: day %s has a head and no stored leaves", Date(h.D))
		case err != nil:
			return nil, fmt.Errorf("publisher: reading day %s's leaves: %w", Date(h.D), err)
		case uint64(len(blob)) != h.Count*ledger.HashSize:
			return nil, fmt.Errorf("publisher: day %s's stored leaves are %d bytes, and its head counts %d leaves", Date(h.D), len(blob), h.Count)
		}
		leaves = make([]ledger.Hash, h.Count)
		for i := range leaves {
			copy(leaves[i][:], blob[i*ledger.HashSize:])
		}
	}
	if root := ledger.Root(leaves); root != h.Root {
		return nil, fmt.Errorf("publisher: day %s's stored leaves give the root %s, and its head says %s", Date(h.D), root, h.Root)
	}
	return leaves, nil
}
