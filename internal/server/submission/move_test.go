// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// insertSubmission stores a submission of u1 in state, updated at 100 and,
// once decided, decided at 500.
func insertSubmission(t *testing.T, db *store.DB, id string, state State) {
	t.Helper()
	var decided any
	if state == Decided || state == Revoked {
		decided = int64(500)
	}
	if _, err := db.Write.ExecContext(context.Background(), `INSERT INTO submissions
(id, user_id, program, takes_hours, request_id, request_sha256, created_by, mode, state, created_at_ms, updated_at_ms, decided_at_ms)
VALUES ($1, 'u1', 'hard-mode', 1, $1, 'sha', 'device', 'window', $2, 100, 100, $3)`, id, string(state), decided); err != nil {
		t.Fatal(err)
	}
}

// moved is what Move left of a submission: its state, its update time,
// each time column and its event rows.
type moved struct {
	state                                       State
	updated                                     int64
	frozen, revealed, challenged, decided, gone sql.NullInt64
	revoked                                     sql.NullInt64
	events                                      []string
}

func readMoved(t *testing.T, db *store.DB, id string) moved {
	t.Helper()
	ctx := context.Background()
	var m moved
	if err := db.Read.QueryRowContext(ctx, `SELECT state, updated_at_ms, frozen_at_ms, metadata_revealed_at_ms, challenged_at_ms, decided_at_ms, withdrawn_at_ms, revoked_at_ms
FROM submissions WHERE id = $1`, id).Scan(&m.state, &m.updated, &m.frozen, &m.revealed, &m.challenged, &m.decided, &m.gone, &m.revoked); err != nil {
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
// decision's time, and a revocation leaves it; and the move to challenged
// sets challenged_at_ms, from which r can be read (D-sb1-submissions, Q9).
func TestMove(t *testing.T) {
	const at = 1000
	columnOf := func(m moved, s State) (sql.NullInt64, bool) {
		switch s {
		case Frozen:
			return m.frozen, true
		case MetadataRevealed:
			return m.revealed, true
		case Challenged:
			return m.challenged, true
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
		t.Run(string(engine)+"/a second move adds the next event after the first", func(t *testing.T) {
			insertSubmission(t, db, "s-chain", Frozen)
			for i, to := range []State{MetadataRevealed, Challenged} {
				if err := in(t, db, func(ctx context.Context, tx writer.Tx) error { return Move(ctx, tx, "s-chain", to, int64(2000+i)) }); err != nil {
					t.Fatal(err)
				}
			}
			got := readMoved(t, db, "s-chain")
			want := []string{"1 frozen>metadata_revealed @2000", "2 metadata_revealed>challenged @2001"}
			if fmt.Sprint(got.events) != fmt.Sprint(want) {
				t.Errorf("events %q, want %q", got.events, want)
			}
		})
		t.Run(string(engine)+"/a submission the server doesn't hold gets ErrNotFound", func(t *testing.T) {
			err := in(t, db, func(ctx context.Context, tx writer.Tx) error { return Move(ctx, tx, "s-none", Frozen, at) })
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("Move = %v, want ErrNotFound", err)
			}
		})
		t.Run(string(engine)+"/Touch moves the update time alone, and a submission the server doesn't hold gets ErrNotFound", func(t *testing.T) {
			insertSubmission(t, db, "s-touch", Frozen)
			if err := in(t, db, func(ctx context.Context, tx writer.Tx) error { return Touch(ctx, tx, "s-touch", 3000) }); err != nil {
				t.Fatal(err)
			}
			if got := readMoved(t, db, "s-touch"); got.updated != 3000 || got.state != Frozen || len(got.events) != 0 {
				t.Errorf("after Touch: %+v, want frozen, updated at 3000, with no event", got)
			}
			if err := in(t, db, func(ctx context.Context, tx writer.Tx) error { return Touch(ctx, tx, "s-none", 3000) }); !errors.Is(err, ErrNotFound) {
				t.Errorf("Touch = %v, want ErrNotFound", err)
			}
		})
	}
}
