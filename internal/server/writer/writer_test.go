// SPDX-License-Identifier: AGPL-3.0-or-later

package writer_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// commitLog records every transaction a writer reports.
type commitLog struct {
	mu   sync.Mutex
	list []writer.Commit
}

func (l *commitLog) record(c writer.Commit) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.list = append(l.list, c)
}

func (l *commitLog) all() []writer.Commit {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.list)
}

// open returns a fresh database with a table for test rows.
func open(t *testing.T, engine store.Engine) *store.DB {
	t.Helper()
	db := storetest.Open(t, engine)
	if _, err := db.Write.Exec("CREATE TABLE w_rows (id BIGINT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	return db
}

// start starts a writer on db that records its transactions in log, and
// closes it when the test ends, before the database closes.
func start(t *testing.T, db *store.DB, o writer.Options, log *commitLog) *writer.Writer {
	t.Helper()
	o.OnCommit = log.record
	w := writer.Start(db.Write, o)
	t.Cleanup(w.Close)
	return w
}

// insert is an op that writes one row.
func insert(id int) writer.Op {
	return func(ctx context.Context, tx writer.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO w_rows (id) VALUES ($1)", id)
		return err
	}
}

// ids returns the ids in w_rows.
func ids(t *testing.T, db *store.DB) map[int]bool {
	t.Helper()
	rows, err := db.Read.Query("SELECT id FROM w_rows")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := make(map[int]bool)
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		got[id] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return got
}

// doAll hands every op to w at once and returns their errors in order.
func doAll(ctx context.Context, w *writer.Writer, rows int, ops ...writer.Op) []error {
	errs := make([]error, len(ops))
	var wg sync.WaitGroup
	for i, op := range ops {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = w.Do(ctx, rows, op)
		}()
	}
	wg.Wait()
	return errs
}

// waitFor polls cond until it holds, or fails the test after 10 seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestCommitRate is WP-SV1's acceptance check: 10,000 single-row writes
// commit in at most 20 transactions per second of wall time (DESIGN 10.6;
// SP 7.4), with no transaction over 500 rows. It runs on every engine
// Engines returns: SQLite in ci.yml, both in postgres.yml.
func TestCommitRate(t *testing.T) {
	tests := []struct {
		name string
		// workers each hand writesEach writes to the writer in turn. The
		// race detector allows 8,128 live goroutines, so 10,000 writes
		// come from 1,000 workers.
		workers, writesEach int
		// rowsEach is the rows each write declares.
		rowsEach int
		// maxOps is the most writes one transaction may hold.
		maxOps int
	}{
		{name: "10,000 single-row writes commit at most 20 times a second, at most 500 rows each", workers: 1000, writesEach: 10, rowsEach: 1, maxOps: 500},
		{name: "200-row writes share a transaction only while they fit in 500 rows", workers: 40, writesEach: 1, rowsEach: 200, maxOps: 2},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				db := open(t, engine)
				var log commitLog
				w := start(t, db, writer.Options{}, &log)
				ctx := context.Background()
				total := tt.workers * tt.writesEach
				var failed atomic.Int64
				var wg sync.WaitGroup
				begin := time.Now()
				for worker := range tt.workers {
					wg.Add(1)
					go func() {
						defer wg.Done()
						for n := range tt.writesEach {
							if err := w.Do(ctx, tt.rowsEach, insert(worker*tt.writesEach+n)); err != nil {
								failed.Add(1)
							}
						}
					}()
				}
				wg.Wait()
				elapsed := time.Since(begin)

				if n := failed.Load(); n > 0 {
					t.Fatalf("%d of %d writes failed", n, total)
				}
				var n int
				if err := db.Read.QueryRow("SELECT count(*) FROM w_rows").Scan(&n); err != nil {
					t.Fatal(err)
				}
				if n != total {
					t.Errorf("%d rows committed, want %d", n, total)
				}
				cs := log.all()
				rate := float64(len(cs)) / elapsed.Seconds()
				t.Logf("%d writes in %d transactions over %v: %.1f transactions a second", total, len(cs), elapsed, rate)
				if rate > 20 {
					t.Errorf("%d transactions in %v is %.1f a second, over 20", len(cs), elapsed, rate)
				}
				for i, c := range cs {
					if c.Rows > writer.DefaultMaxRows || c.Ops > tt.maxOps {
						t.Errorf("transaction %d holds %d writes and %d rows; want at most %d writes and %d rows", i, c.Ops, c.Rows, tt.maxOps, writer.DefaultMaxRows)
					}
					if i > 0 {
						if gap := c.Start.Sub(cs[i-1].Start); gap < writer.DefaultWindow {
							t.Errorf("transactions %d and %d began %v apart, under the %v window", i-1, i, gap, writer.DefaultWindow)
						}
					}
				}
			})
		}
	}
}

func TestWriterRules(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		run  func(t *testing.T, db *store.DB)
	}{
		{
			name: "a failing op is rolled back alone and the rest of its batch commits",
			run: func(t *testing.T, db *store.DB) {
				var log commitLog
				w := start(t, db, writer.Options{Window: 300 * time.Millisecond}, &log)
				bad := func(ctx context.Context, tx writer.Tx) error {
					if err := insert(2)(ctx, tx); err != nil {
						return err
					}
					_, err := tx.ExecContext(ctx, "INSERT INTO w_missing (id) VALUES (1)")
					return err
				}
				errs := doAll(ctx, w, 1, insert(1), bad, insert(3))
				if errs[0] != nil || errs[1] == nil || errs[2] != nil {
					t.Errorf("errors = %v, want only the second op's", errs)
				}
				if got, want := ids(t, db), map[int]bool{1: true, 3: true}; !reflect.DeepEqual(got, want) {
					t.Errorf("rows = %v, want %v: the failed op's own row must roll back", got, want)
				}
				if cs := log.all(); len(cs) != 1 || cs[0].Ops != 3 || cs[0].Failed != 1 || cs[0].Err != nil {
					t.Errorf("transactions = %+v, want one holding 3 ops, 1 of them failed", cs)
				}
			},
		},
		{
			name: "a panicking op fails alone and the writer keeps going",
			run: func(t *testing.T, db *store.DB) {
				var log commitLog
				w := start(t, db, writer.Options{Window: 300 * time.Millisecond}, &log)
				panics := func(ctx context.Context, tx writer.Tx) error {
					if err := insert(11)(ctx, tx); err != nil {
						return err
					}
					panic("a bug in an op")
				}
				errs := doAll(ctx, w, 1, insert(10), panics)
				if errs[0] != nil || errs[1] == nil || !strings.Contains(errs[1].Error(), "panicked") {
					t.Errorf("errors = %v, want the second op's panic as an error", errs)
				}
				if err := w.Do(ctx, 1, insert(12)); err != nil {
					t.Errorf("a write after the panic: %v", err)
				}
				if got, want := ids(t, db), map[int]bool{10: true, 12: true}; !reflect.DeepEqual(got, want) {
					t.Errorf("rows = %v, want %v", got, want)
				}
			},
		},
		{
			name: "an op larger than 500 rows commits in a transaction of its own",
			run: func(t *testing.T, db *store.DB) {
				var log commitLog
				w := start(t, db, writer.Options{}, &log)
				errs := make([]error, 2)
				var wg sync.WaitGroup
				for i, rows := range []int{800, 1} {
					wg.Add(1)
					go func() {
						defer wg.Done()
						errs[i] = w.Do(ctx, rows, insert(20+i))
					}()
				}
				wg.Wait()
				if errs[0] != nil || errs[1] != nil {
					t.Fatalf("errors = %v", errs)
				}
				alone := false
				for _, c := range log.all() {
					if c.Rows == 800 && c.Ops == 1 {
						alone = true
					}
				}
				if !alone {
					t.Errorf("transactions = %+v, want the 800-row op alone in one", log.all())
				}
			},
		},
		{
			name: "a write waits at least one window for its commit",
			run: func(t *testing.T, db *store.DB) {
				var log commitLog
				window := 100 * time.Millisecond
				w := start(t, db, writer.Options{Window: window}, &log)
				begin := time.Now()
				if err := w.Do(ctx, 1, insert(30)); err != nil {
					t.Fatal(err)
				}
				if elapsed := time.Since(begin); elapsed < window {
					t.Errorf("Do returned after %v, under the %v window", elapsed, window)
				}
			},
		},
		{
			name: "Close writes every queued op before it returns",
			run: func(t *testing.T, db *store.DB) {
				var log commitLog
				// A window far past the test's end: only Close commits.
				w := start(t, db, writer.Options{Window: time.Hour}, &log)
				const n = 50
				errs := make(chan error, n)
				for i := range n {
					go func() { errs <- w.Do(ctx, 1, insert(100+i)) }()
				}
				waitFor(t, "50 queued writes", func() bool { return w.QueueDepth() == n })
				w.Close()
				for range n {
					if err := <-errs; err != nil {
						t.Errorf("a write queued before Close: %v", err)
					}
				}
				if got := len(ids(t, db)); got != n {
					t.Errorf("%d rows after Close, want %d", got, n)
				}
			},
		},
		{
			name: "Do after Close returns ErrClosed",
			run: func(t *testing.T, db *store.DB) {
				var log commitLog
				w := start(t, db, writer.Options{}, &log)
				w.Close()
				if err := w.Do(ctx, 1, insert(1)); !errors.Is(err, writer.ErrClosed) {
					t.Errorf("Do after Close = %v, want ErrClosed", err)
				}
			},
		},
		{
			name: "an op whose caller gave up before it ran never runs",
			run: func(t *testing.T, db *store.DB) {
				var log commitLog
				w := start(t, db, writer.Options{Window: 300 * time.Millisecond}, &log)
				var ran atomic.Bool
				gaveUp, cancel := context.WithCancel(ctx)
				errc := make(chan error, 1)
				go func() {
					errc <- w.Do(gaveUp, 1, func(ctx context.Context, tx writer.Tx) error {
						ran.Store(true)
						return insert(40)(ctx, tx)
					})
				}()
				waitFor(t, "the write to be queued", func() bool { return w.QueueDepth() == 1 })
				cancel()
				if err := <-errc; !errors.Is(err, context.Canceled) {
					t.Errorf("Do = %v, want context.Canceled", err)
				}
				// A later write commits after the skipped one's batch.
				if err := w.Do(ctx, 1, insert(41)); err != nil {
					t.Fatal(err)
				}
				if ran.Load() {
					t.Error("the op ran after its caller gave up")
				}
				if got, want := ids(t, db), map[int]bool{41: true}; !reflect.DeepEqual(got, want) {
					t.Errorf("rows = %v, want %v", got, want)
				}
			},
		},
		{
			name: "QueueDepth counts ops queued and in the batch being written",
			run: func(t *testing.T, db *store.DB) {
				var log commitLog
				w := start(t, db, writer.Options{Window: 10 * time.Millisecond}, &log)
				started, release := make(chan struct{}), make(chan struct{})
				blocker := func(ctx context.Context, tx writer.Tx) error {
					close(started)
					<-release
					return nil
				}
				done := make(chan []error, 1)
				go func() { done <- doAll(ctx, w, 1, blocker) }()
				<-started
				go func() { done <- doAll(ctx, w, 1, insert(50), insert(51)) }()
				waitFor(t, "a depth of 3", func() bool { return w.QueueDepth() == 3 })
				close(release)
				for range 2 {
					for _, err := range <-done {
						if err != nil {
							t.Error(err)
						}
					}
				}
				if d := w.QueueDepth(); d != 0 {
					t.Errorf("QueueDepth = %d after every write returned, want 0", d)
				}
			},
		},
		{
			name: "when a transaction can't begin, every op in it gets the error",
			run: func(t *testing.T, db *store.DB) {
				var log commitLog
				if err := db.Write.Close(); err != nil {
					t.Fatal(err)
				}
				w := start(t, db, writer.Options{Window: 100 * time.Millisecond}, &log)
				for i, err := range doAll(ctx, w, 1, insert(1), insert(2)) {
					if err == nil {
						t.Errorf("op %d succeeded on a closed database", i)
					}
				}
				if cs := log.all(); len(cs) == 0 || cs[0].Err == nil {
					t.Errorf("transactions = %+v, want a failed one", cs)
				}
			},
		},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				tt.run(t, open(t, engine))
			})
		}
	}
}
