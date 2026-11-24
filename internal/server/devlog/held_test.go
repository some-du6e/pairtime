// SPDX-License-Identifier: AGPL-3.0-or-later

package devlog

import (
	"context"
	"errors"
	"testing"

	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// takeHeldAt runs a DeleteHeld or a RemoveHeld of the account's id at
// at(h, 0) in a transaction of its own.
func takeHeldAt(t *testing.T, db *store.DB, op func(context.Context, writer.Tx, string, string, int64) (bool, error), account, id string, h int) (bool, error) {
	t.Helper()
	var held bool
	err := in(t, db, func(ctx context.Context, tx writer.Tx) error {
		var err error
		held, err = op(ctx, tx, account, id, at(h, 0).UnixMilli())
		return err
	})
	return held, err
}

// TestTakeHeld checks the signed-in dashboard's deletes: DeleteHeld and
// RemoveHeld take down an item the account holds, keeping its removal
// record as Delete and Remove do, and keep nothing for an id it doesn't
// hold (orchestrator, 2026-11-24).
func TestTakeHeld(t *testing.T) {
	x := Devlog{ID: "dl_7f3a9c", ProjectKey: "p_one", Markdown: "# Day 3", AIAssisted: false}
	tests := []struct {
		name  string
		check func(t *testing.T, db *store.DB)
	}{
		{
			name: "a devlog the account holds is deleted, its removal is kept, and the call says it held it",
			check: func(t *testing.T, db *store.DB) {
				if _, _, err := publish(t, db, "u1", "d1", x, at(10, 0)); err != nil {
					t.Fatal(err)
				}
				held, err := takeHeldAt(t, db, DeleteHeld, "u1", x.ID, 11)
				if err != nil || !held {
					t.Fatalf("DeleteHeld = %v, %v; want held", held, err)
				}
				got := read(t, db, "u1")
				if len(got.devlogs) != 0 || got.removed[x.ID] != at(11, 0).UnixMilli() {
					t.Errorf("u1 holds %+v, want no devlog and its removal at 11:00", got)
				}
			},
		},
		{
			name: "a devlog id the account doesn't hold keeps no removal record",
			check: func(t *testing.T, db *store.DB) {
				held, err := takeHeldAt(t, db, DeleteHeld, "u1", "dl_never", 11)
				if err != nil || held {
					t.Fatalf("DeleteHeld = %v, %v; want not held", held, err)
				}
				if got := read(t, db, "u1"); len(got.removed) != 0 {
					t.Errorf("u1 keeps removals %v, want none", got.removed)
				}
			},
		},
		{
			name: "another account's devlog is neither deleted nor recorded",
			check: func(t *testing.T, db *store.DB) {
				if _, _, err := publish(t, db, "u2", "d2", x, at(10, 0)); err != nil {
					t.Fatal(err)
				}
				held, err := takeHeldAt(t, db, DeleteHeld, "u1", x.ID, 11)
				if err != nil || held {
					t.Fatalf("DeleteHeld = %v, %v; want not held", held, err)
				}
				if got := read(t, db, "u2"); len(got.devlogs) != 1 || len(got.removed) != 0 {
					t.Errorf("u2 holds %+v, want its devlog and no removal", got)
				}
				if got := read(t, db, "u1"); len(got.removed) != 0 {
					t.Errorf("u1 keeps removals %v, want none", got.removed)
				}
			},
		},
		{
			name: "a badge the account holds is removed with its removal kept",
			check: func(t *testing.T, db *store.DB) {
				if _, _, err := show(t, db, "u1", "b_1", "first_commit"); err != nil {
					t.Fatal(err)
				}
				held, err := takeHeldAt(t, db, RemoveHeld, "u1", "b_1", 11)
				if err != nil || !held {
					t.Fatalf("RemoveHeld = %v, %v; want held", held, err)
				}
				got := read(t, db, "u1")
				if len(got.badges) != 0 || got.removed["b_1"] != at(11, 0).UnixMilli() {
					t.Errorf("u1 holds %+v, want no badge and its removal at 11:00", got)
				}
			},
		},
		{
			name: "a badge id the account doesn't hold keeps no removal record",
			check: func(t *testing.T, db *store.DB) {
				held, err := takeHeldAt(t, db, RemoveHeld, "u1", "b_never", 11)
				if err != nil || held {
					t.Fatalf("RemoveHeld = %v, %v; want not held", held, err)
				}
				if got := read(t, db, "u1"); len(got.removed) != 0 {
					t.Errorf("u1 keeps removals %v, want none", got.removed)
				}
			},
		},
		{
			name: "a devlog the account holds is deleted with its record while the account keeps 1,000 removal records",
			check: func(t *testing.T, db *store.DB) {
				seedRemovals(t, db, "u1", MaxRemovals)
				if _, _, err := publish(t, db, "u1", "d1", x, at(10, 0)); err != nil {
					t.Fatal(err)
				}
				held, err := takeHeldAt(t, db, DeleteHeld, "u1", x.ID, 11)
				if err != nil || !held {
					t.Fatalf("DeleteHeld = %v, %v; want held", held, err)
				}
				if got := read(t, db, "u1"); len(got.devlogs) != 0 || len(got.removed) != MaxRemovals+1 {
					t.Errorf("u1 holds %d devlogs and %d removals, want none and %d", len(got.devlogs), len(got.removed), MaxRemovals+1)
				}
			},
		},
		{
			name: "a devlog deleted from the dashboard can't be published again under its id",
			check: func(t *testing.T, db *store.DB) {
				if _, _, err := publish(t, db, "u1", "d1", x, at(10, 0)); err != nil {
					t.Fatal(err)
				}
				if _, err := takeHeldAt(t, db, DeleteHeld, "u1", x.ID, 11); err != nil {
					t.Fatal(err)
				}
				if _, _, err := publish(t, db, "u1", "d1", x, at(12, 0)); !errors.Is(err, ErrRemoved) {
					t.Errorf("publishing it again = %v, want ErrRemoved", err)
				}
			},
		},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				tt.check(t, newDB(t, engine))
			})
		}
	}
}
