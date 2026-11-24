// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/devlog"
	"github.com/some-du6e/pairtime/internal/server/migrations"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// txDoer runs each op in a transaction of its own, as the writer does.
type txDoer struct{ db *store.DB }

func (d txDoer) Do(ctx context.Context, _ int, op writer.Op) error {
	tx, err := d.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := op(ctx, tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// TestPublishedOf checks the dashboard's list as pairtimed reads it: each
// devlog with its project called as the dashboard calls it, the badges,
// and the take-downs, which go through the writer.
func TestPublishedOf(t *testing.T) {
	at := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		// label is the name u1 turned on for d1's p_one, "" for none.
		label string
		do    func(t *testing.T, d txDoer)
		check func(t *testing.T, d txDoer)
	}{
		{
			name: "a devlog's project is called by its code when its owner named it nowhere",
			check: func(t *testing.T, d txDoer) {
				p, err := publishedOf(context.Background(), d.db.Read, "u1")
				if err != nil {
					t.Fatal(err)
				}
				if len(p.Devlogs) != 1 || !strings.HasPrefix(p.Devlogs[0].Project, "Project ") || p.Devlogs[0].Markdown != "# Day 3" || !p.Devlogs[0].AIAssisted || !p.Devlogs[0].ReceivedAt.Equal(at) {
					t.Errorf("devlogs %+v, want the one, its project called by its code, its Markdown, ai_assisted and its time", p.Devlogs)
				}
				if len(p.Badges) != 1 || p.Badges[0].ID != "b_1" || p.Badges[0].Code != "first_commit" {
					t.Errorf("badges %+v, want b_1, first_commit", p.Badges)
				}
			},
		},
		{
			name:  "a devlog's project is called by the name its owner turned on",
			label: "My game",
			check: func(t *testing.T, d txDoer) {
				p, err := publishedOf(context.Background(), d.db.Read, "u1")
				if err != nil {
					t.Fatal(err)
				}
				if len(p.Devlogs) != 1 || p.Devlogs[0].Project != "My game" {
					t.Errorf("devlogs %+v, want the one called My game", p.Devlogs)
				}
			},
		},
		{
			name: "the dashboard's delete and removal take the account's items down through the writer, keeping their removals",
			do: func(t *testing.T, d txDoer) {
				ctx := context.Background()
				if err := takeDownOp(ctx, d, devlog.DeleteHeld, "u1", "dl_1"); err != nil {
					t.Fatal(err)
				}
				if err := takeDownOp(ctx, d, devlog.RemoveHeld, "u1", "b_1"); err != nil {
					t.Fatal(err)
				}
			},
			check: func(t *testing.T, d txDoer) {
				p, err := publishedOf(context.Background(), d.db.Read, "u1")
				if err != nil {
					t.Fatal(err)
				}
				if len(p.Devlogs)+len(p.Badges) != 0 {
					t.Errorf("after the take-downs u1 still shows %+v", p)
				}
				var n int
				if err := d.db.Read.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM removed_items WHERE user_id = 'u1' AND id IN ('dl_1', 'b_1')`).Scan(&n); err != nil || n != 2 {
					t.Errorf("u1 keeps %d of the two removals (%v)", n, err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			db := storetest.Open(t, store.SQLite)
			ms, err := migrations.Embedded()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := migrations.Apply(ctx, db, ms, time.Now); err != nil {
				t.Fatal(err)
			}
			d := txDoer{db: db}
			if err := d.Do(ctx, 3, func(ctx context.Context, tx writer.Tx) error {
				if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, hca_sub, verification_status, created_at_ms, updated_at_ms, signed_in_at_ms) VALUES ('u1', 'sub-u1', 'verified', 0, 0, 0)`); err != nil {
					return err
				}
				if _, _, err := devlog.Publish(ctx, tx, "u1", "d1", devlog.Devlog{ID: "dl_1", ProjectKey: "p_one", Markdown: "# Day 3", AIAssisted: true}, at.UnixMilli()); err != nil {
					return err
				}
				_, _, err := devlog.Show(ctx, tx, "u1", devlog.Badge{ID: "b_1", Code: "first_commit"})
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if tt.label != "" {
				if _, err := db.Write.ExecContext(ctx, `INSERT INTO project_names (device_id, project_key, user_id, label, set_at_ms) VALUES ('d1', 'p_one', 'u1', $1, 0)`, tt.label); err != nil {
					t.Fatal(err)
				}
			}
			if tt.do != nil {
				tt.do(t, d)
			}
			tt.check(t, d)
		})
	}
}
