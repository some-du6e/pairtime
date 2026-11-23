// SPDX-License-Identifier: AGPL-3.0-or-later

package witness

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/params"
)

// Queryer reads rows. Ingest passes the writer's transaction, so a
// request's batch roots are already there when its buckets are labeled.
// *sql.DB is one too.
type Queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Revision is a minute-bucket revision ingest is storing.
type Revision struct {
	// Bucket is the revision as ingest parsed it: its Minute is on the
	// device's clock, and its Witness is the client's reading.
	Bucket event.MinuteBucket
	// ReceivedAt is its request's received_at, in Unix ms on the server's
	// clock (ingest-v1 2.7).
	ReceivedAt int64
}

// Stored is the minute's current revision, the one a new revision
// replaces.
type Stored struct {
	Bucket event.MinuteBucket
	// Label is the server's label for it, "" for a revision stored before
	// the server labeled buckets.
	Label event.Witness
}

// Bucket returns the server's label for rev, which ingest is storing in
// the transaction q reads from. cur is the minute's current revision,
// which rev replaces, or nil for the minute's first. The package
// documentation gives the rule. WITNESS_LAG_S comes from the newest
// parameter table, as the server's other clock work takes its parameters
// (package rollup's settle job).
func Bucket(ctx context.Context, q Queryer, rev Revision, cur *Stored) (event.Witness, error) {
	tables := params.Tables()
	return bucket(ctx, q, tables[len(tables)-1].Int(params.WITNESS_LAG_S)*1000, rev, cur)
}

func bucket(ctx context.Context, q Queryer, lag int64, rev Revision, cur *Stored) (event.Witness, error) {
	client := rev.Bucket.Witness
	end := rev.Bucket.Minute.UnixMilli() + 60_000
	own := event.Backfilled
	switch {
	case rev.ReceivedAt-end <= lag:
		// The client sends a minute only once a batch sealed after it has a
		// receipt, and that batch reached the server after the minute ended,
		// give or take a device clock up to FutureMS fast. A minute sent
		// ahead of every batch of its time rests on nothing the server has.
		ok, err := reached(ctx, q, rev.Bucket.Device, end-FutureMS)
		if err != nil {
			return "", err
		}
		if ok {
			own = event.Witnessed
		}
	case client == event.Offline:
		ok, err := caughtUp(ctx, q, rev.Bucket.Device, end, rev.ReceivedAt, lag)
		if err != nil {
			return "", err
		}
		if ok {
			own = event.Offline
		}
	}
	if cur != nil && cur.Label != "" && SameCredit(rev.Bucket, cur.Bucket) {
		own = Better(own, cur.Label)
	}
	return Worse(client, own), nil
}

// reached reports whether the server holds a batch of device it received
// after the time after.
func reached(ctx context.Context, q Queryer, device string, after int64) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM witness_rows WHERE device_id = $1 AND received_at_ms > $2 LIMIT 1`, device, after).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("witness: reading a device's witness rows: %w", err)
	}
	return true, nil
}

// caughtUp reports whether the server saw device cut off around a minute
// that ended at end and catch up by now: the first batch it received from
// the device after end came more than lag after end, now is within lag
// after that, and the batch before it is on the device's chain, received
// by end. A device whose chain starts at that batch, or skips to it over a
// local-only gap, has no such batch.
func caughtUp(ctx context.Context, q Queryer, device string, end, now, lag int64) (bool, error) {
	var b, at int64
	err := q.QueryRowContext(ctx, `SELECT b, received_at_ms FROM witness_rows WHERE device_id = $1 AND received_at_ms > $2
ORDER BY received_at_ms, b LIMIT 1`, device, end).Scan(&b, &at)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("witness: reading a device's witness rows: %w", err)
	}
	if at-end <= lag || now-at > lag || b <= 1 {
		return false, nil
	}
	var before int64
	err = q.QueryRowContext(ctx, `SELECT received_at_ms FROM witness_rows WHERE device_id = $1 AND b = $2`, device, b-1).Scan(&before)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("witness: reading a device's witness rows: %w", err)
	}
	return before <= end, nil
}

// SameCredit reports whether a and b credit the same seconds in the same
// way: the same credited_s and the same slots, each with its project,
// seconds, reason, kind, level and labels. What a minute leaves
// uncredited, the agent's time, the state, the revision and the client's
// reading don't count.
func SameCredit(a, b event.MinuteBucket) bool {
	return a.CreditedS == b.CreditedS && slices.EqualFunc(a.Slots, b.Slots, func(x, y event.BucketSlot) bool {
		return x.Project == y.Project && x.Credited == y.Credited && x.Why == y.Why && x.Kind == y.Kind &&
			x.Ev == y.Ev && slices.Equal(x.Lbl, y.Lbl)
	})
}

// order is the labels from the strongest to the weakest.
var order = [...]event.Witness{event.Witnessed, event.Offline, event.Backfilled}

// rank is w's place in order. Anything that isn't a label ranks as
// backfilled.
func rank(w event.Witness) int {
	if i := slices.Index(order[:], w); i >= 0 {
		return i
	}
	return len(order) - 1
}

// Worse returns the weaker of two labels: backfilled before offline,
// and offline before witnessed.
func Worse(a, b event.Witness) event.Witness { return order[max(rank(a), rank(b))] }

// Better returns the stronger of two labels.
func Better(a, b event.Witness) event.Witness { return order[min(rank(a), rank(b))] }
