// SPDX-License-Identifier: AGPL-3.0-or-later

package rollup

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/some-du6e/pairtime/internal/server/writer"
	"github.com/some-du6e/pairtime/pkg/params"
)

// PendingWindow is how long after its minute ends a bucket stays pending:
// PENDING_WINDOW_S of the newest parameter table pairtimed embeds (DESIGN
// 3.7, 6.2 D1).
var PendingWindow = sync.OnceValue(func() time.Duration {
	tables := params.Tables()
	return time.Duration(tables[len(tables)-1].Int(params.PENDING_WINDOW_S)) * time.Second
})

// SettleEvery is how often pairtimed runs the settle job. A minute its
// device leaves pending settles at most this long after its pending
// window closes.
const SettleEvery = time.Minute

// settleBatch is how many of an account's minutes one settle op takes at
// most, and maxSettleOps how many ops one Settle runs at most, so a
// backlog can't keep the writer busy for long. The next run goes on where
// this one stopped. settleRowsPerMinute is the rows a settled minute
// writes, its pending device rows and the rollups_daily rows its time
// moves between, as a typical minute has them; Settle declares that many
// per due minute to the writer.
const (
	settleBatch         = 100
	maxSettleOps        = 100
	settleRowsPerMinute = 4
)

// Doer runs a write through the writer goroutine. *writer.Writer is one.
type Doer interface {
	Do(ctx context.Context, rows int, op writer.Op) error
}

// Settle settles by the server's clock (D-server-settles-pending): every
// per-device minute still pending whose minute ended more than
// PendingWindow before now counts as final from then on, so a device that
// sleeps or never returns doesn't leave its last minutes pending for ever.
// Each settled minute goes through the merge and delta path Apply takes,
// so its share moves from rollups_daily's pending columns to the final
// ones, and a later revision from the device still replaces it as usual.
// Settle runs through d, in ops of at most settleBatch minutes, and
// returns how many of the accounts' minutes it settled.
//
// Settle counts the due minutes on q, the read pool, before each op, and
// runs none when nothing is due. So an idle server makes no transaction
// for it: serve counts every transaction that declares rows as a write
// Litestream has to copy (backup.Replication.Wrote).
func Settle(ctx context.Context, d Doer, q Querier, now time.Time) (int, error) {
	// A minute ends 60 s after it starts.
	cutoffMS := now.Add(-PendingWindow() - time.Minute).UnixMilli()
	total := 0
	for range maxSettleOps {
		due, err := countDue(ctx, q, cutoffMS, settleBatch)
		if err != nil {
			return total, fmt.Errorf("rollup: counting the pending minutes due to settle: %w", err)
		}
		if due == 0 {
			break
		}
		var n int
		err = d.Do(ctx, due*settleRowsPerMinute, func(ctx context.Context, tx writer.Tx) error {
			var err error
			n, err = settle(ctx, tx, cutoffMS, settleBatch)
			return err
		})
		if err != nil {
			return total, fmt.Errorf("rollup: settling pending minutes: %w", err)
		}
		total += n
		if n < settleBatch {
			break
		}
	}
	return total, nil
}

// countDue counts, up to limit, the accounts' minutes that start before
// cutoffMS and have a pending device row.
func countDue(ctx context.Context, q Querier, cutoffMS int64, limit int) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT DISTINCT user_id, minute_ms FROM rollup_minutes
WHERE state = 'pending' AND minute_ms < $1 LIMIT $2) due`, cutoffMS, limit).Scan(&n)
	return n, err
}

// settle settles at most limit of the accounts' minutes that start before
// cutoffMS and have a pending device row, oldest first, and returns how
// many it settled.
func settle(ctx context.Context, tx writer.Tx, cutoffMS int64, limit int) (int, error) {
	// The literal 'pending' lets SQLite use rollup_minutes_pending, a
	// partial index, which a placeholder wouldn't.
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT user_id, minute_ms FROM rollup_minutes
WHERE state = 'pending' AND minute_ms < $1 ORDER BY minute_ms, user_id LIMIT $2`, cutoffMS, limit)
	if err != nil {
		return 0, err
	}
	type minute struct {
		account string
		ms      int64
	}
	var due []minute
	for rows.Next() {
		var m minute
		if err := rows.Scan(&m.account, &m.ms); err != nil {
			rows.Close()
			return 0, err
		}
		due = append(due, m)
	}
	// The rows close before the writes, which share the transaction's
	// connection.
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, m := range due {
		if err := settleMinute(ctx, tx, m.account, m.ms); err != nil {
			return 0, err
		}
	}
	return len(due), nil
}

// settleMinute makes every pending device row of the account's minute
// final, and moves the rollups to match through remerge, the path Apply
// takes. The minute stays on the local day it counts toward: settling
// counts nothing anew.
func settleMinute(ctx context.Context, tx writer.Tx, account string, minuteMS int64) error {
	before, err := loadMinute(ctx, tx, account, minuteMS)
	if err != nil || len(before) == 0 {
		return err
	}
	day := before[0].day
	after := slices.Clone(before)
	for i := range after {
		if !after[i].pending {
			continue
		}
		after[i].pending = false
		if _, err := tx.ExecContext(ctx, `UPDATE rollup_minutes SET state = 'final'
WHERE user_id = $1 AND minute_ms = $2 AND device_id = $3`, account, minuteMS, after[i].id); err != nil {
			return fmt.Errorf("rollup: settling %s's minute %d on %s: %w", account, minuteMS, after[i].id, err)
		}
	}
	return remerge(ctx, tx, account, before, day, after, day)
}

// RunSettle runs Settle through d, reading q, at once, then every
// interval, until ctx ends. report, when set, hears how many minutes each
// run settled, and its error.
func RunSettle(ctx context.Context, d Doer, q Querier, every time.Duration, now func() time.Time, report func(settled int, err error)) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		n, err := Settle(ctx, d, q, now())
		if report != nil {
			report(n, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
