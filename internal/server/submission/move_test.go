// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/migrations"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// newDB returns a migrated database on engine with account u1.
func newDB(t *testing.T, engine store.Engine) *store.DB {
	t.Helper()
	ctx := context.Background()
	db := storetest.Open(t, engine)
	ms, err := migrations.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(ctx, db, ms, time.Now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx, `INSERT INTO users (id, hca_sub, verification_status, created_at_ms, updated_at_ms, signed_in_at_ms)
VALUES ('u1', 'sub-u1', 'verified', 0, 0, 0)`); err != nil {
		t.Fatal(err)
	}
	return db
}

// in runs op in a transaction of its own, as the writer does, and returns
// its error.
func in(t *testing.T, db *store.DB, op writer.Op) error {
	t.Helper()
	ctx := context.Background()
	tx, err := db.Write.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := op(ctx, tx); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return nil
}

// insertSubmission stores a submission of u1 in state, updated at 100 and,
// once decided, decided at 500.
func insertSubmission(t *testing.T, db *store.DB, id string, state State) {
	t.Helper()
	var decided any
	if state == Decided || state == Revoked {
		decided = int64(500)
	}
	if _, err := db.Write.ExecContext(context.Background(), `INSERT INTO submissions
(id, user_id, program, external_id, request_sha256, created_by, mode, state, eligibility_to_confirm, created_at_ms, updated_at_ms, decided_at_ms)
VALUES ($1, 'u1', 'test', $1, 'sha', 'device', 'window', $2, 0, 100, 100, $3)`, id, string(state), decided); err != nil {
		t.Fatal(err)
	}
}

// moved is what Move left of a submission: its state, its update time,
// each time column and its event rows.
type moved struct {
	state                           State
	updated                         int64
	frozen, revealed, decided, gone sql.NullInt64
	revoked                         sql.NullInt64
	events                          []string
}

func readMoved(t *testing.T, db *store.DB, id string) moved {
	t.Helper()
	ctx := context.Background()
	var m moved
	if err := db.Read.QueryRowContext(ctx, `SELECT state, updated_at_ms, frozen_at_ms, metadata_revealed_at_ms, decided_at_ms, withdrawn_at_ms, revoked_at_ms
FROM submissions WHERE id = $1`, id).Scan(&m.state, &m.updated, &m.frozen, &m.revealed, &m.decided, &m.gone, &m.revoked); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Read.QueryContext(ctx, `SELECT seq, from_state, to_state, at_ms FROM submission_events WHERE submission_id = $1 ORDER BY seq`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var seq, at int64
		var from, to string
		if err := rows.Scan(&seq, &from, &to, &at); err != nil {
			t.Fatal(err)
		}
		m.events = append(m.events, fmt.Sprintf("%d %s>%s @%d", seq, from, to, at))
	}
	return m
}

// TestMove checks Move on both engines, for every pair of states: a legal
// move sets the state, the update time, its state's time column and an
// event row; an illegal one changes nothing; a re-decision moves the
// decision's time, and a revocation leaves it.
func TestMove(t *testing.T) {
	const at = 1000
	columnOf := func(m moved, s State) (sql.NullInt64, bool) {
		switch s {
		case Frozen:
			return m.frozen, true
		case MetadataRevealed:
			return m.revealed, true
		case Decided:
			return m.decided, true
		case Withdrawn:
			return m.gone, true
		case Revoked:
			return m.revoked, true
		}
		return sql.NullInt64{}, false
	}
	for _, engine := range storetest.Engines() {
		db := newDB(t, engine)
		for _, from := range States {
			for _, to := range States {
				legal := Legal(from, to)
				name := fmt.Sprintf("%s/%s to %s, legal %v", engine, from, to, legal)
				t.Run(name, func(t *testing.T) {
					id := fmt.Sprintf("s-%s-%s", from, to)
					insertSubmission(t, db, id, from)
					before := readMoved(t, db, id)
					err := in(t, db, func(ctx context.Context, tx writer.Tx) error { return Move(ctx, tx, id, to, at) })
					got := readMoved(t, db, id)
					if !legal {
						if !errors.Is(err, ErrIllegal) {
							t.Errorf("Move = %v, want ErrIllegal", err)
						}
						if fmt.Sprint(got) != fmt.Sprint(before) {
							t.Errorf("an illegal move changed %+v to %+v", before, got)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if got.state != to || got.updated != at {
						t.Errorf("state %s updated %d, want %s at %d", got.state, got.updated, to, at)
					}
					if col, ok := columnOf(got, to); ok && (!col.Valid || col.Int64 != at) {
						t.Errorf("%s's time is %+v, want %d", to, col, at)
					}
					if to == Revoked && got.decided.Int64 != 500 {
						t.Errorf("a revocation moved the decision's time to %+v, want it left at 500", got.decided)
					}
					want := fmt.Sprintf("1 %s>%s @%d", from, to, at)
					if len(got.events) != 1 || got.events[0] != want {
						t.Errorf("events %q, want [%q]", got.events, want)
					}
				})
			}
		}
		t.Run(string(engine)+"/a submission the server doesn't hold gets ErrNotFound", func(t *testing.T) {
			err := in(t, db, func(ctx context.Context, tx writer.Tx) error { return Move(ctx, tx, "s-none", Frozen, at) })
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("Move = %v, want ErrNotFound", err)
			}
		})
	}
}
