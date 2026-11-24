// SPDX-License-Identifier: AGPL-3.0-or-later

// Package writer is pairtimed's one writer goroutine (DESIGN 3.10; SP 7.4).
//
// Every write the server makes is an op handed to Do or DoDurable. The
// writer gathers ops into batches and runs each batch in one transaction,
// so the number of commits doesn't grow with the request rate. A
// transaction begins at least Window (50 ms) after the previous one did
// and holds at most MaxRows (500) rows, so the writer commits at most 20
// times a second under any load, the bound SP 7.4 and DESIGN 10.6 give. An
// op whose rows don't fit waits for the next batch, and a single op larger
// than MaxRows gets a batch to itself.
//
// SP 7.4 words the rule as "every 50 ms or 500 rows, whichever comes
// first". Read literally, a batch that fills before 50 ms commits at once,
// and a burst of more than 10,000 rows a second commits more than 20 times
// a second. WP-SV1's acceptance test, 10,000 single-row writes in at most 20
// transactions per second of wall time, rules that reading out, so a full
// batch waits for its turn instead.
//
// Each op runs inside a savepoint. An op that fails or panics is rolled
// back alone, its caller gets its error, and the rest of the batch commits.
// When the transaction itself fails, every op in it gets that error.
//
// An op handed to DoDurable is on disk before DoDurable returns nil, so a
// power cut can't roll it back (DESIGN 8.9, 10.1). SQLite runs with
// synchronous=NORMAL, which syncs the WAL only at checkpoints, so the writer
// sets synchronous=FULL on its connection before a batch that holds a
// durable op begins, and NORMAL again once the batch ends. Every op in that
// batch then commits with the WAL synced. On Postgres the batch's
// transaction sets synchronous_commit on, so its COMMIT waits for the WAL to
// reach the disk whatever the server's default is. When the writer can't
// make a batch durable, the batch fails and none of its ops run.
package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/some-du6e/pairtime/internal/server/store"
)

// Defaults for Options.
const (
	DefaultWindow       = 50 * time.Millisecond
	DefaultMaxRows      = 500
	DefaultQueueSize    = 10000
	DefaultBatchTimeout = 30 * time.Second
)

// Tx is the part of *sql.Tx an op may use. Ops never commit or roll back:
// the writer does that, for the whole batch. Ops write with ExecContext,
// which is how the writer counts the rows they change (Commit.Changed).
type Tx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Op is one unit of work, run inside a batch's transaction. It may run
// alongside other callers' ops in the same transaction, so it must not
// depend on rows that a later op in the batch writes.
type Op func(ctx context.Context, tx Tx) error

// Commit describes one transaction the writer finished, for metrics.
type Commit struct {
	// Start is when the batch's database calls began.
	Start time.Time
	// Duration runs from Start to the end of COMMIT, and for a durable
	// batch on SQLite to the end of the reset to NORMAL after it.
	Duration time.Duration
	// Ops is how many ops ran in the transaction, and Rows how many rows
	// they declared.
	Ops, Rows int
	// Failed is how many of those ops failed and were rolled back alone.
	Failed int
	// Changed is how many rows the ops that succeeded changed, as their
	// ExecContext statements' RowsAffected report it; a statement whose
	// driver can't tell counts as one. A transaction that changed no row,
	// such as a DELETE that matched nothing, wrote no page, so Litestream
	// has nothing of it to copy. It is 0 when Err is set.
	Changed int64
	// Durable is whether the transaction held an op handed to DoDurable.
	// When Err is nil, its commit was on disk before any caller was told.
	Durable bool
	// Err is the error that failed the whole transaction, if one did.
	Err error
}

// Options tune a Writer. Zero values take the defaults.
type Options struct {
	// Window is the shortest time between the starts of two transactions.
	Window time.Duration
	// MaxRows is the most rows one transaction holds, unless a single op
	// declares more.
	MaxRows int
	// QueueSize is how many ops can wait before Do blocks.
	QueueSize int
	// BatchTimeout bounds the database calls of one transaction.
	BatchTimeout time.Duration
	// OnCommit, when set, is called from the writer goroutine after every
	// transaction, before the ops' callers are told. It must not block.
	OnCommit func(Commit)
	// Engine is the database's engine, which decides how a batch that holds
	// a durable op reaches the disk. Empty means SQLite, as in
	// store.Options.
	Engine store.Engine
}

// ErrClosed is what Do and DoDurable return once Close has been called.
var ErrClosed = errors.New("writer: closed")

// Writer is the writer goroutine and its queue.
type Writer struct {
	db   *sql.DB
	opts Options

	// mu orders Close after every send in flight, so nothing is queued
	// once the goroutine starts draining.
	mu     sync.RWMutex
	closed bool

	queue chan *request
	stop  chan struct{}
	done  chan struct{}
	depth atomic.Int64

	// full is whether SQLite's connection may still be at
	// synchronous=FULL, from a durable batch whose reset to NORMAL hasn't
	// succeeded yet. Only the writer goroutine uses it.
	full bool
}

type request struct {
	ctx     context.Context
	rows    int
	op      Op
	durable bool
	res     chan error // buffered, so the writer never waits on a caller
}

// Start starts the writer goroutine on db, which is store.DB.Write: the one
// connection writes go through.
func Start(db *sql.DB, o Options) *Writer {
	if o.Window <= 0 {
		o.Window = DefaultWindow
	}
	if o.MaxRows <= 0 {
		o.MaxRows = DefaultMaxRows
	}
	if o.QueueSize <= 0 {
		o.QueueSize = DefaultQueueSize
	}
	if o.BatchTimeout <= 0 {
		o.BatchTimeout = DefaultBatchTimeout
	}
	w := &Writer{
		db:    db,
		opts:  o,
		queue: make(chan *request, o.QueueSize),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	go w.loop()
	return w
}

// Do runs op in a coming batch and returns once that batch's transaction
// has finished: nil, op's own error, or the error that failed the
// transaction. rows is how many rows op writes, for MaxRows; less than 1
// counts as 1.
//
// When ctx ends before op runs, op is skipped and Do returns ctx.Err().
// When ctx ends after op ran, Do returns ctx.Err() at once, but op's batch
// may still commit, so callers treat that error as "unknown" and retry an
// idempotent op, as ingest does with its dedupe on (device_id, b)
// (DESIGN 4.5).
func (w *Writer) Do(ctx context.Context, rows int, op Op) error {
	return w.do(ctx, rows, op, false)
}

// DoDurable is Do for an op whose commit must survive a power cut, because
// nothing would send it again: an erasure the privacy hub hears is done
// (DESIGN 8.9), and the evidence index rows that guard a blob's PUT or
// delete (DESIGN 10.1). It returns nil only once op's batch has committed
// and the commit is on disk. Its batch commits as the package doc says,
// with every other op in it.
func (w *Writer) DoDurable(ctx context.Context, rows int, op Op) error {
	return w.do(ctx, rows, op, true)
}

func (w *Writer) do(ctx context.Context, rows int, op Op, durable bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if rows < 1 {
		rows = 1
	}
	r := &request{ctx: ctx, rows: rows, op: op, durable: durable, res: make(chan error, 1)}
	w.mu.RLock()
	if w.closed {
		w.mu.RUnlock()
		return ErrClosed
	}
	w.depth.Add(1)
	select {
	case w.queue <- r:
		w.mu.RUnlock()
	case <-ctx.Done():
		w.mu.RUnlock()
		w.depth.Add(-1)
		return ctx.Err()
	}
	select {
	case err := <-r.res:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// QueueDepth is how many ops handed to Do or DoDurable haven't finished:
// queued, or in the batch being written.
func (w *Writer) QueueDepth() int {
	return int(w.depth.Load())
}

// Close stops taking ops, writes every op already queued, and waits for the
// writer goroutine to end. Calling it again waits too. Never call it from
// an op.
func (w *Writer) Close() {
	w.mu.Lock()
	already := w.closed
	w.closed = true
	w.mu.Unlock()
	if !already {
		close(w.stop)
	}
	<-w.done
}

func (w *Writer) loop() {
	defer close(w.done)
	var carry *request // an op that didn't fit the previous batch
	stopping := false
	for {
		first := carry
		carry = nil
		if first == nil {
			if stopping {
				select {
				case first = <-w.queue:
				default:
					return
				}
			} else {
				select {
				case first = <-w.queue:
				case <-w.stop:
					stopping = true
					continue
				}
			}
		}

		// The batch opens now, after the previous transaction finished,
		// and commits Window from now: so transactions begin at least
		// Window apart. While stopping, it commits as soon as it is full
		// or the queue is empty.
		timer := time.NewTimer(w.opts.Window)
		batch := []*request{first}
		rows := first.rows
		add := func(r *request) bool {
			if rows+r.rows > w.opts.MaxRows {
				carry = r
				return false
			}
			batch = append(batch, r)
			rows += r.rows
			return true
		}
		due := false
	collect:
		for rows < w.opts.MaxRows {
			if stopping {
				select {
				case r := <-w.queue:
					if !add(r) {
						break collect
					}
				default:
					break collect
				}
				continue
			}
			select {
			case r := <-w.queue:
				if !add(r) {
					break collect
				}
			case <-timer.C:
				due = true
				break collect
			case <-w.stop:
				stopping = true
			}
		}
		if !due && !stopping {
			// Full before its time: wait for it.
			select {
			case <-timer.C:
			case <-w.stop:
				stopping = true
			}
		}
		timer.Stop()
		w.run(batch)
	}
}

// run writes one batch and tells each op's caller how it went.
func (w *Writer) run(batch []*request) {
	var live []*request
	rows := 0
	durable := false
	for _, r := range batch {
		if err := r.ctx.Err(); err != nil {
			// The caller gave up before the op ran.
			w.finish(r, err)
			continue
		}
		live = append(live, r)
		rows += r.rows
		durable = durable || r.durable
	}
	if len(live) == 0 {
		return
	}

	c := Commit{Start: time.Now(), Ops: len(live), Rows: rows, Durable: durable}
	ctx, cancel := context.WithTimeout(context.Background(), w.opts.BatchTimeout)
	errs, changed, err := w.write(ctx, live, durable)
	cancel()
	c.Changed = changed
	c.Duration = time.Since(c.Start)
	c.Err = err
	for _, e := range errs {
		if e != nil {
			c.Failed++
		}
	}
	if w.opts.OnCommit != nil {
		w.opts.OnCommit(c)
	}
	for i, r := range live {
		if err != nil {
			w.finish(r, err)
		} else {
			w.finish(r, errs[i])
		}
	}
}

// savepoint names the savepoint around each op. Each is released before
// the next op starts, so the name never nests.
const savepoint = "pairtime_op"

// write runs ops in one transaction, each inside a savepoint, and commits,
// durably when durable is set (see the package doc). It returns each op's
// error and how many rows the ops that succeeded changed, or the error
// that failed the transaction.
func (w *Writer) write(ctx context.Context, ops []*request, durable bool) ([]error, int64, error) {
	var tx *sql.Tx
	var err error
	if w.opts.Engine == store.Postgres || (!durable && !w.full) {
		// Nothing runs before BEGIN, so the pool begins the transaction,
		// and retries it on a new connection when the one it had has
		// gone bad, as when a Postgres server dropped it.
		tx, err = w.db.BeginTx(ctx, nil)
	} else {
		// SQLite's level must be set on the connection the batch runs
		// on, so the batch holds the one connection from that statement
		// to its reset. modernc.org/sqlite reports a bad connection only
		// when the pool hands it out, which Conn retries.
		conn, cerr := w.db.Conn(ctx)
		if cerr != nil {
			return nil, 0, fmt.Errorf("writer: connection: %w", cerr)
		}
		defer conn.Close()
		if err := w.setLevel(ctx, conn, durable); err != nil {
			return nil, 0, fmt.Errorf("writer: making the batch durable: %w", err)
		}
		if durable {
			// Runs before conn.Close, deferred above.
			defer w.setLevel(ctx, conn, false)
		}
		tx, err = conn.BeginTx(ctx, nil)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("writer: begin: %w", err)
	}
	abort := func(what string, err error) ([]error, int64, error) {
		tx.Rollback()
		return nil, 0, fmt.Errorf("writer: %s: %w", what, err)
	}
	if durable && w.opts.Engine == store.Postgres {
		// Before the first op's savepoint, so no op's rollback undoes
		// it. SET LOCAL lasts only until the transaction ends.
		if _, err := tx.ExecContext(ctx, "SET LOCAL synchronous_commit TO on"); err != nil {
			return abort("making the batch durable", err)
		}
	}
	errs := make([]error, len(ops))
	var changed int64
	for i, r := range ops {
		if _, err := tx.ExecContext(ctx, "SAVEPOINT "+savepoint); err != nil {
			return abort("savepoint", err)
		}
		ct := &countingTx{tx: tx}
		if err := call(ctx, ct, r.op); err != nil {
			errs[i] = err
			if _, err := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT "+savepoint); err != nil {
				return abort("rolling back a failed op", err)
			}
		} else {
			changed += ct.changed
		}
		if _, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT "+savepoint); err != nil {
			return abort("releasing the savepoint", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, fmt.Errorf("writer: commit: %w", err)
	}
	return errs, changed, nil
}

// setLevel sets SQLite's synchronous level on conn: FULL before a durable
// batch, and NORMAL after one or, when that reset failed, before the next
// plain batch. Only a failure to set FULL is an error, which fails the
// durable batch. A failed reset isn't one: FULL is the stricter level, so
// the batch runs at it, and the next batch tries again. Only SQLite's
// batches call it: Postgres has no level to set outside a transaction.
func (w *Writer) setLevel(ctx context.Context, conn *sql.Conn, durable bool) error {
	switch {
	case durable:
		// Set first: when the statement fails, the batch fails before
		// its deferred reset, and the next batch resets the level instead.
		w.full = true
		_, err := conn.ExecContext(ctx, "PRAGMA synchronous = FULL")
		return err
	case w.full:
		if _, err := conn.ExecContext(ctx, "PRAGMA synchronous = NORMAL"); err == nil {
			w.full = false
		}
	}
	return nil
}

// countingTx is the Tx an op gets: the batch's transaction, counting the
// rows the op's ExecContext statements change. It hides Commit and
// Rollback, which only the writer calls.
type countingTx struct {
	tx      *sql.Tx
	changed int64
}

func (t *countingTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	res, err := t.tx.ExecContext(ctx, query, args...)
	if err == nil {
		n, rerr := res.RowsAffected()
		if rerr != nil || n < 0 {
			// The driver can't tell, so the statement may have written.
			n = 1
		}
		t.changed += n
	}
	return res, err
}

func (t *countingTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return t.tx.QueryContext(ctx, query, args...)
}

func (t *countingTx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return t.tx.QueryRowContext(ctx, query, args...)
}

// call runs op and turns a panic into an error, so one bad op fails alone
// instead of taking the server down.
func call(ctx context.Context, tx Tx, op Op) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("writer: op panicked: %v", p)
		}
	}()
	return op(ctx, tx)
}

// finish reports an op's result to its caller.
func (w *Writer) finish(r *request, err error) {
	w.depth.Add(-1)
	r.res <- err
}
