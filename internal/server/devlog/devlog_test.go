// SPDX-License-Identifier: AGPL-3.0-or-later

package devlog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/migrations"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// newDB returns a migrated database with accounts u1 and u2.
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
	for _, id := range []string{"u1", "u2"} {
		if _, err := db.Write.ExecContext(ctx, `INSERT INTO users (id, hca_sub, verification_status, created_at_ms, updated_at_ms, signed_in_at_ms)
VALUES ($1, $2, 'verified', 0, 0, 0)`, id, "sub-"+id); err != nil {
			t.Fatal(err)
		}
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

// at is a time on 2026-11-20 UTC, to the minute.
func at(h, m int) time.Time { return time.Date(2026, 11, 20, h, m, 0, 0, time.UTC) }

// A Markdown text with bytes a careless store would change: HTML, quotes,
// a tab, a carriage return, a NUL, a line separator, an emoji and no
// newline at the end.
const tricky = "# Day 3\r\n\nFixed the `nil` map <b>at last</b> & \"quoted\" it\t  \x00 \U0001F680 é"

// accountRows are the ids an account's rows in devlogs, badges and
// removed_items hold, with each removal's time.
type accountRows struct {
	devlogs, badges []string
	removed         map[string]int64
}

func read(t *testing.T, db *store.DB, account string) accountRows {
	t.Helper()
	ctx := context.Background()
	r := accountRows{removed: map[string]int64{}}
	for _, q := range []struct {
		table string
		into  *[]string
	}{{"devlogs", &r.devlogs}, {"badges", &r.badges}} {
		res, err := db.Read.QueryContext(ctx, `SELECT id FROM `+q.table+` WHERE user_id = $1`, account)
		if err != nil {
			t.Fatal(err)
		}
		for res.Next() {
			var id string
			if err := res.Scan(&id); err != nil {
				t.Fatal(err)
			}
			*q.into = append(*q.into, id)
		}
		res.Close()
		slices.Sort(*q.into)
	}
	res, err := db.Read.QueryContext(ctx, `SELECT id, removed_at_ms FROM removed_items WHERE user_id = $1`, account)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Close()
	for res.Next() {
		var id string
		var ms int64
		if err := res.Scan(&id, &ms); err != nil {
			t.Fatal(err)
		}
		r.removed[id] = ms
	}
	return r
}

// seedDevlogs stores n devlogs of the account directly, dl_seed_0 and on,
// each of size bytes of Markdown, as Publish stores them.
func seedDevlogs(t *testing.T, db *store.DB, account string, n, size int) {
	t.Helper()
	md := []byte(strings.Repeat("x", size))
	if err := in(t, db, func(ctx context.Context, tx writer.Tx) error {
		for i := range n {
			if _, err := tx.ExecContext(ctx, `INSERT INTO devlogs (user_id, id, device_id, project_key, received_at_ms, markdown, ai_assisted)
VALUES ($1, $2, 'd1', 'p_one', $3, $4, 0)`, account, fmt.Sprintf("dl_seed_%d", i), at(9, 0).UnixMilli(), md); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// seedRemovals keeps n removal records of the account directly, rm_seed_0
// and on, at 9:00.
func seedRemovals(t *testing.T, db *store.DB, account string, n int) {
	t.Helper()
	if err := in(t, db, func(ctx context.Context, tx writer.Tx) error {
		for i := range n {
			if _, err := tx.ExecContext(ctx, `INSERT INTO removed_items (user_id, id, removed_at_ms) VALUES ($1, $2, $3)`,
				account, fmt.Sprintf("rm_seed_%d", i), at(9, 0).UnixMilli()); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// show runs Show for the account's badge id and code in a transaction of
// its own.
func show(t *testing.T, db *store.DB, account, id, code string) (Badge, bool, error) {
	t.Helper()
	var b Badge
	var created bool
	err := in(t, db, func(ctx context.Context, tx writer.Tx) error {
		var err error
		b, created, err = Show(ctx, tx, account, Badge{ID: id, Code: code})
		return err
	})
	return b, created, err
}

// del runs a Delete or a Remove of the account's id at at(h, 0) in a
// transaction of its own.
func del(t *testing.T, db *store.DB, op func(context.Context, writer.Tx, string, string, int64) error, account, id string, h int) error {
	t.Helper()
	return in(t, db, func(ctx context.Context, tx writer.Tx) error {
		return op(ctx, tx, account, id, at(h, 0).UnixMilli())
	})
}

func publish(t *testing.T, db *store.DB, account, device string, d Devlog, now time.Time) (Devlog, bool, error) {
	t.Helper()
	var out Devlog
	var created bool
	err := in(t, db, func(ctx context.Context, tx writer.Tx) error {
		var err error
		out, created, err = Publish(ctx, tx, account, device, d, now.UnixMilli())
		return err
	})
	return out, created, err
}

func list(t *testing.T, db *store.DB, account string) []Devlog {
	t.Helper()
	got, err := List(context.Background(), db.Read, account)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestDevlogs(t *testing.T) {
	x := Devlog{ID: "dl_7f3a9c", ProjectKey: "p_one", Markdown: tricky, AIAssisted: true}
	tests := []struct {
		name  string
		check func(t *testing.T, db *store.DB)
	}{
		{
			name: "a published devlog reads back byte for byte, with its label, its device and key, and when the server received it",
			check: func(t *testing.T, db *store.DB) {
				got, created, err := publish(t, db, "u1", "d1", x, at(10, 0))
				if err != nil || !created {
					t.Fatalf("Publish = created %v, %v", created, err)
				}
				all := list(t, db, "u1")
				if len(all) != 1 {
					t.Fatalf("List = %+v, want the devlog", all)
				}
				d := all[0]
				if d.Markdown != tricky || !d.AIAssisted || d.Device != "d1" || d.ProjectKey != "p_one" || !d.ReceivedAt.Equal(at(10, 0)) || d.Project == "" {
					t.Errorf("read back %+v, want the Markdown byte for byte, ai_assisted, d1's p_one, received at 10:00 and a project", d)
				}
				if !reflect.DeepEqual(got, d) {
					t.Errorf("Publish answered %+v, List reads %+v", got, d)
				}
			},
		},
		{
			name: "a retry of a publish stores nothing new and answers with what the server stored",
			check: func(t *testing.T, db *store.DB) {
				first, _, _ := publish(t, db, "u1", "d1", x, at(10, 0))
				again := x
				again.Markdown, again.AIAssisted = "a different text", false
				got, created, err := publish(t, db, "u1", "d2", again, at(10, 5))
				if err != nil || created {
					t.Fatalf("the retry = created %v, %v; want the stored devlog", created, err)
				}
				if !reflect.DeepEqual(got, first) || !reflect.DeepEqual(list(t, db, "u1"), []Devlog{first}) {
					t.Errorf("the retry answered %+v and the account holds %+v, want only %+v", got, list(t, db, "u1"), first)
				}
			},
		},
		{
			name: "after a delete no read returns the devlog, its row is gone, and only its id and time remain",
			check: func(t *testing.T, db *store.DB) {
				publish(t, db, "u1", "d1", x, at(10, 0))
				if err := in(t, db, func(ctx context.Context, tx writer.Tx) error {
					return Delete(ctx, tx, "u1", x.ID, at(11, 0).UnixMilli())
				}); err != nil {
					t.Fatal(err)
				}
				if got := list(t, db, "u1"); len(got) != 0 {
					t.Errorf("List = %+v after the delete", got)
				}
				want := accountRows{removed: map[string]int64{x.ID: at(11, 0).UnixMilli()}}
				if got := read(t, db, "u1"); !reflect.DeepEqual(got, want) {
					t.Errorf("the account's rows are %+v, want only the removal %+v", got, want)
				}
			},
		},
		{
			name: "a publish of a deleted id sent again from any device gets ErrRemoved and stores nothing",
			check: func(t *testing.T, db *store.DB) {
				publish(t, db, "u1", "d1", x, at(10, 0))
				in(t, db, func(ctx context.Context, tx writer.Tx) error {
					return Delete(ctx, tx, "u1", x.ID, at(11, 0).UnixMilli())
				})
				for _, dev := range []string{"d1", "d2"} {
					if _, _, err := publish(t, db, "u1", dev, x, at(12, 0)); !errors.Is(err, ErrRemoved) {
						t.Errorf("a publish from %s after the delete = %v, want ErrRemoved", dev, err)
					}
				}
				if got := read(t, db, "u1"); len(got.devlogs) != 0 {
					t.Errorf("the account holds devlogs %v", got.devlogs)
				}
			},
		},
		{
			name: "a publish that arrives after a delete of an id the server never held gets ErrRemoved",
			check: func(t *testing.T, db *store.DB) {
				in(t, db, func(ctx context.Context, tx writer.Tx) error {
					return Delete(ctx, tx, "u1", x.ID, at(11, 0).UnixMilli())
				})
				if _, _, err := publish(t, db, "u1", "d1", x, at(12, 0)); !errors.Is(err, ErrRemoved) {
					t.Errorf("Publish = %v, want ErrRemoved", err)
				}
			},
		},
		{
			name: "a second delete of an id keeps the first removal's time",
			check: func(t *testing.T, db *store.DB) {
				for _, h := range []int{11, 12} {
					in(t, db, func(ctx context.Context, tx writer.Tx) error {
						return Delete(ctx, tx, "u1", x.ID, at(h, 0).UnixMilli())
					})
				}
				if got := read(t, db, "u1").removed[x.ID]; got != at(11, 0).UnixMilli() {
					t.Errorf("the removal is at %d, want the first one's %d", got, at(11, 0).UnixMilli())
				}
			},
		},
		{
			name: "a devlog's project follows its key's rollup_projects row, as project registration moves it",
			check: func(t *testing.T, db *store.DB) {
				publish(t, db, "u1", "d1", x, at(10, 0))
				if _, err := db.Write.ExecContext(context.Background(), `UPDATE rollup_projects SET project_id = 'linked-project'
WHERE user_id = 'u1' AND device_id = 'd1' AND project_key = 'p_one'`); err != nil {
					t.Fatal(err)
				}
				if got := list(t, db, "u1"); len(got) != 1 || got[0].Project != "linked-project" {
					t.Errorf("List = %+v, want the devlog on linked-project", got)
				}
			},
		},
		{
			name: "a devlog of a key the rollups already count is on that key's project",
			check: func(t *testing.T, db *store.DB) {
				if _, err := db.Write.ExecContext(context.Background(), `INSERT INTO rollup_projects (user_id, device_id, project_key, project_id)
VALUES ('u1', 'd1', 'p_one', 'rolled-project')`); err != nil {
					t.Fatal(err)
				}
				if got, _, _ := publish(t, db, "u1", "d1", x, at(10, 0)); got.Project != "rolled-project" {
					t.Errorf("Publish = %+v, want it on rolled-project", got)
				}
			},
		},
		{
			name: "another account's devlogs and removals are its own",
			check: func(t *testing.T, db *store.DB) {
				publish(t, db, "u2", "d9", x, at(10, 0))
				in(t, db, func(ctx context.Context, tx writer.Tx) error {
					return Delete(ctx, tx, "u1", x.ID, at(11, 0).UnixMilli())
				})
				if got := list(t, db, "u2"); len(got) != 1 {
					t.Errorf("u2 holds %+v after u1's delete of the same id, want its devlog", got)
				}
				if got := list(t, db, "u1"); len(got) != 0 {
					t.Errorf("u1 holds %+v", got)
				}
				if _, created, err := publish(t, db, "u2", "d9", Devlog{ID: "dl_other", ProjectKey: "p_one", Markdown: "x"}, at(12, 0)); err != nil || !created {
					t.Errorf("u2's next publish = created %v, %v", created, err)
				}
			},
		},
		{
			name: "a badge shows once, and a retry answers with the code the server stored",
			check: func(t *testing.T, db *store.DB) {
				show := func(code string) (Badge, bool, error) {
					var b Badge
					var created bool
					err := in(t, db, func(ctx context.Context, tx writer.Tx) error {
						var err error
						b, created, err = Show(ctx, tx, "u1", Badge{ID: "bd_1", Code: code})
						return err
					})
					return b, created, err
				}
				if _, created, err := show("fixed_by_hand"); err != nil || !created {
					t.Fatalf("Show = created %v, %v", created, err)
				}
				if b, created, err := show("long_fight"); err != nil || created || b.Code != "fixed_by_hand" {
					t.Errorf("the retry = %+v, created %v, %v; want the stored fixed_by_hand", b, created, err)
				}
			},
		},
		{
			name: "a removed badge's row is gone, only its id and time remain, and its show sent again gets ErrRemoved",
			check: func(t *testing.T, db *store.DB) {
				show := func() error {
					return in(t, db, func(ctx context.Context, tx writer.Tx) error {
						_, _, err := Show(ctx, tx, "u1", Badge{ID: "bd_1", Code: "comeback"})
						return err
					})
				}
				if err := show(); err != nil {
					t.Fatal(err)
				}
				if err := in(t, db, func(ctx context.Context, tx writer.Tx) error {
					return Remove(ctx, tx, "u1", "bd_1", at(11, 0).UnixMilli())
				}); err != nil {
					t.Fatal(err)
				}
				want := accountRows{removed: map[string]int64{"bd_1": at(11, 0).UnixMilli()}}
				if got := read(t, db, "u1"); !reflect.DeepEqual(got, want) {
					t.Errorf("the account's rows are %+v, want only the removal %+v", got, want)
				}
				if err := show(); !errors.Is(err, ErrRemoved) {
					t.Errorf("the show sent again = %v, want ErrRemoved", err)
				}
			},
		},
		{
			name: "a publish that would take the account past 1,000 devlogs gets ErrLimit and stores nothing, and a held id's republish still answers",
			check: func(t *testing.T, db *store.DB) {
				seedDevlogs(t, db, "u1", MaxDevlogs, 10)
				if _, _, err := publish(t, db, "u1", "d1", x, at(10, 0)); !errors.Is(err, ErrLimit) {
					t.Errorf("the 1,001st devlog = %v, want ErrLimit", err)
				}
				if got := read(t, db, "u1"); len(got.devlogs) != MaxDevlogs || slices.Contains(got.devlogs, x.ID) {
					t.Errorf("the account holds %d devlogs, want the %d seeded and not %s", len(got.devlogs), MaxDevlogs, x.ID)
				}
				held := Devlog{ID: "dl_seed_7", ProjectKey: "p_one", Markdown: "again"}
				if got, created, err := publish(t, db, "u1", "d1", held, at(10, 0)); err != nil || created || got.Markdown != strings.Repeat("x", 10) {
					t.Errorf("the republish of a held id = %+v, created %v, %v; want the stored devlog", got, created, err)
				}
				if _, created, err := publish(t, db, "u2", "d9", x, at(10, 0)); err != nil || !created {
					t.Errorf("another account's publish = created %v, %v", created, err)
				}
			},
		},
		{
			name: "a publish that would take the account past 8 MiB of Markdown gets ErrLimit, and one that reaches 8 MiB exactly is stored",
			check: func(t *testing.T, db *store.DB) {
				seedDevlogs(t, db, "u1", MaxMarkdownTotal/MaxMarkdown-1, MaxMarkdown)
				last := Devlog{ID: "dl_last", ProjectKey: "p_one", Markdown: strings.Repeat("y", MaxMarkdown)}
				if _, created, err := publish(t, db, "u1", "d1", last, at(10, 0)); err != nil || !created {
					t.Fatalf("the devlog that reaches 8 MiB = created %v, %v", created, err)
				}
				one := Devlog{ID: "dl_more", ProjectKey: "p_one", Markdown: "z"}
				if _, _, err := publish(t, db, "u1", "d1", one, at(10, 1)); !errors.Is(err, ErrLimit) {
					t.Errorf("a byte past 8 MiB = %v, want ErrLimit", err)
				}
				if got := read(t, db, "u1"); slices.Contains(got.devlogs, "dl_more") {
					t.Errorf("the account holds dl_more")
				}
			},
		},
		{
			name: "an account shows each code once: a new id of a code it shows gets ErrShown, the same id again is a repeat, and a removal frees the code",
			check: func(t *testing.T, db *store.DB) {
				if _, created, err := show(t, db, "u1", "bd_1", "comeback"); err != nil || !created {
					t.Fatalf("Show = created %v, %v", created, err)
				}
				if _, _, err := show(t, db, "u1", "bd_2", "comeback"); !errors.Is(err, ErrShown) {
					t.Errorf("a second id of the code = %v, want ErrShown", err)
				}
				if b, created, err := show(t, db, "u1", "bd_1", "comeback"); err != nil || created || b.ID != "bd_1" {
					t.Errorf("the same id again = %+v, created %v, %v; want the stored badge", b, created, err)
				}
				if _, created, err := show(t, db, "u2", "bd_2", "comeback"); err != nil || !created {
					t.Errorf("another account's show of the code = created %v, %v", created, err)
				}
				if err := del(t, db, Remove, "u1", "bd_1", 11); err != nil {
					t.Fatal(err)
				}
				if _, created, err := show(t, db, "u1", "bd_3", "comeback"); err != nil || !created {
					t.Errorf("a new id after the removal = created %v, %v; want it stored", created, err)
				}
				if got := read(t, db, "u1"); !reflect.DeepEqual(got.badges, []string{"bd_3"}) {
					t.Errorf("the account shows %v, want only bd_3", got.badges)
				}
			},
		},
		{
			name: "a delete of an id the account never held keeps its record until the account keeps 1,000, then gets ErrLimit and keeps nothing",
			check: func(t *testing.T, db *store.DB) {
				seedRemovals(t, db, "u1", MaxRemovals-1)
				if err := del(t, db, Delete, "u1", "dl_a", 10); err != nil {
					t.Fatalf("the 1,000th record = %v", err)
				}
				for _, op := range []struct {
					name string
					op   func(context.Context, writer.Tx, string, string, int64) error
				}{{"Delete", Delete}, {"Remove", Remove}} {
					if err := del(t, db, op.op, "u1", "never_held", 11); !errors.Is(err, ErrLimit) {
						t.Errorf("%s of a never-held id past 1,000 records = %v, want ErrLimit", op.name, err)
					}
				}
				got := read(t, db, "u1")
				if _, ok := got.removed["never_held"]; ok || len(got.removed) != MaxRemovals {
					t.Errorf("the account keeps %d records, never_held among them: %v; want %d without it", len(got.removed), ok, MaxRemovals)
				}
				if err := del(t, db, Delete, "u1", "dl_a", 12); err != nil {
					t.Errorf("a second delete of a kept id = %v, want it answered", err)
				}
				if got := read(t, db, "u1").removed["dl_a"]; got != at(10, 0).UnixMilli() {
					t.Errorf("dl_a's removal is at %d, want the first one's", got)
				}
				if err := del(t, db, Delete, "u2", "never_held", 11); err != nil {
					t.Errorf("another account's delete = %v", err)
				}
			},
		},
		{
			name: "a held devlog's delete and a held badge's removal are never refused, and keep their records past 1,000",
			check: func(t *testing.T, db *store.DB) {
				seedRemovals(t, db, "u1", MaxRemovals)
				publish(t, db, "u1", "d1", x, at(10, 0))
				show(t, db, "u1", "bd_1", "new_tool")
				if err := del(t, db, Delete, "u1", x.ID, 11); err != nil {
					t.Errorf("the held devlog's delete = %v", err)
				}
				if err := del(t, db, Remove, "u1", "bd_1", 11); err != nil {
					t.Errorf("the held badge's removal = %v", err)
				}
				got := read(t, db, "u1")
				_, devlogKept := got.removed[x.ID]
				_, badgeKept := got.removed["bd_1"]
				if len(got.devlogs) != 0 || len(got.badges) != 0 || !devlogKept || !badgeKept || len(got.removed) != MaxRemovals+2 {
					t.Errorf("the account holds %v and %v with %d records; want both gone and both removals kept", got.devlogs, got.badges, len(got.removed))
				}
			},
		},
		{
			name: "DeleteAccount leaves no devlog, badge or removal of the account, and another account's alone",
			check: func(t *testing.T, db *store.DB) {
				for _, acct := range []string{"u1", "u2"} {
					publish(t, db, acct, "d-"+acct, x, at(10, 0))
					publish(t, db, acct, "d-"+acct, Devlog{ID: "dl_2", ProjectKey: "p_one", Markdown: "two"}, at(10, 1))
					in(t, db, func(ctx context.Context, tx writer.Tx) error {
						if _, _, err := Show(ctx, tx, acct, Badge{ID: "bd_1", Code: "new_tool"}); err != nil {
							return err
						}
						return Delete(ctx, tx, acct, "dl_2", at(11, 0).UnixMilli())
					})
				}
				if err := in(t, db, func(ctx context.Context, tx writer.Tx) error { return DeleteAccount(ctx, tx, "u1") }); err != nil {
					t.Fatal(err)
				}
				if got := read(t, db, "u1"); !reflect.DeepEqual(got, accountRows{removed: map[string]int64{}}) {
					t.Errorf("u1 holds %+v after the erasure", got)
				}
				if got := read(t, db, "u2"); len(got.devlogs) != 1 || len(got.badges) != 1 || len(got.removed) != 1 {
					t.Errorf("u2 holds %+v, want its devlog, badge and removal", got)
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

func TestWindows(t *testing.T) {
	const t1, t2, t3, t4, t5 = 1_000, 2_000, 3_000, 4_000, 5_000
	tests := []struct {
		name      string
		times     []int64
		notBefore int64
		want      []Window
	}{
		{name: "devlog windows run between consecutive devlogs", times: []int64{t1, t2, t3}, want: []Window{{t1, t2}, {t2, t3}}},
		{name: "a project with no devlogs gets no window here, since D13's one window takes the submission's bounds, which the caller has", times: nil},
		{
			name:  "after a submission froze [t1, t2] and [t2, t3], deleting t3 and publishing it again at t4 makes the next list [t3, t4]",
			times: []int64{t1, t2, t4}, notBefore: t3, want: []Window{{t3, t4}},
		},
		{
			name:  "once that submission is withdrawn the next list can hold [t2, t4]",
			times: []int64{t1, t2, t4}, want: []Window{{t1, t2}, {t2, t4}},
		},
		{name: "a frozen explicit window's end bounds the next list the same way", times: []int64{t1, t5}, notBefore: t3, want: []Window{{t3, t5}}},
		{name: "a devlog at the frozen end itself starts no second window there", times: []int64{t1, t3, t4}, notBefore: t3, want: []Window{{t3, t4}}},
		{name: "two devlogs at the same time make no empty window", times: []int64{t1, t2, t2, t3}, want: []Window{{t1, t2}, {t2, t3}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Windows(tt.times, tt.notBefore); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Windows = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestFrozenWindows checks D13's worked case against the store: deleting or
// republishing a devlog never moves a frozen window or puts submitted time
// in a new one (DESIGN 6.2 D13, 7.5 step 2).
func TestFrozenWindows(t *testing.T) {
	tests := []struct {
		name string
		// withdrawn says the submission that froze the windows was withdrawn.
		withdrawn bool
		want      func(t1, t2, t3, t4 int64) []Window
	}{
		{
			name: "a devlog deleted and published again after a submission froze its windows starts the next list at the frozen end",
			want: func(_, _, t3, t4 int64) []Window { return []Window{{t3, t4}} },
		},
		{
			name: "after that submission is withdrawn, the time between the last kept devlog and the new one is open again", withdrawn: true,
			want: func(t1, t2, _, t4 int64) []Window { return []Window{{t1, t2}, {t2, t4}} },
		},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				db := newDB(t, engine)
				ctx := context.Background()
				var ts []int64
				for i, id := range []string{"dl_1", "dl_2", "dl_3"} {
					d, _, err := publish(t, db, "u1", "d1", Devlog{ID: id, ProjectKey: "p_one", Markdown: id}, at(10+i, 0))
					if err != nil {
						t.Fatal(err)
					}
					ts = append(ts, d.ReceivedAt.UnixMilli())
				}
				project := list(t, db, "u1")[0].Project
				times, err := Times(ctx, db.Read, "u1", project)
				if err != nil {
					t.Fatal(err)
				}
				frozen := Windows(times, 0)
				if want := []Window{{ts[0], ts[1]}, {ts[1], ts[2]}}; !reflect.DeepEqual(frozen, want) {
					t.Fatalf("the submitted list is %v, want %v", frozen, want)
				}
				// The teen deletes dl_3 to fix a typo and publishes it again.
				in(t, db, func(ctx context.Context, tx writer.Tx) error {
					return Delete(ctx, tx, "u1", "dl_3", at(14, 0).UnixMilli())
				})
				fixed, _, err := publish(t, db, "u1", "d1", Devlog{ID: "dl_3b", ProjectKey: "p_one", Markdown: "dl_3, fixed"}, at(14, 1))
				if err != nil {
					t.Fatal(err)
				}
				t4 := fixed.ReceivedAt.UnixMilli()
				times, err = Times(ctx, db.Read, "u1", project)
				if err != nil {
					t.Fatal(err)
				}
				if want := []int64{ts[0], ts[1], t4}; !reflect.DeepEqual(times, want) {
					t.Fatalf("the project's devlog times are %v, want %v", times, want)
				}
				end := frozen[len(frozen)-1].End
				if tt.withdrawn {
					end = 0
				}
				next := Windows(times, end)
				if want := tt.want(ts[0], ts[1], ts[2], t4); !reflect.DeepEqual(next, want) {
					t.Errorf("the next list is %v, want %v", next, want)
				}
				if !tt.withdrawn {
					for _, w := range next {
						if w.Start < frozen[len(frozen)-1].End {
							t.Errorf("window %v holds time the frozen list %v submitted", w, frozen)
						}
					}
				}
			})
		}
	}
}

// TestCodesAreDesign38 pins Codes to DESIGN 3.8's Moments row, so the badge
// list and the moment kinds can't drift apart without one of them failing
// (orchestrator, 2026-11-23).
func TestCodesAreDesign38(t *testing.T) {
	data, err := os.ReadFile("../../../docs/DESIGN.md")
	if err != nil {
		t.Fatal(err)
	}
	_, sec, ok := strings.Cut(string(data), "\n### 3.8 The coach\n")
	if !ok {
		t.Fatal("docs/DESIGN.md has no section 3.8")
	}
	sec, _, _ = strings.Cut(sec, "\n### ")
	var cells []string
	for _, line := range strings.Split(sec, "\n") {
		if strings.HasPrefix(line, "| Moments |") {
			cells = strings.Split(strings.Trim(line, "| "), " | ")
		}
	}
	if len(cells) != 4 {
		t.Fatalf("DESIGN 3.8's Moments row has %d cells, want 4", len(cells))
	}
	// The kinds are the cell's lowercase names in backquotes; a parameter
	// such as LONG_FIGHT_MIN_S is in capitals.
	var kinds []string
	for _, m := range regexp.MustCompile("`([a-z][a-z_]*)`").FindAllStringSubmatch(cells[2], -1) {
		kinds = append(kinds, m[1])
	}
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "the badge codes are DESIGN 3.8's moment kinds, in its order", ok: slices.Equal(kinds, Codes)},
		{name: "DESIGN 3.8 lists moment kinds", ok: len(kinds) > 0},
		{name: "every badge code is a key, as a request carries it", ok: !slices.ContainsFunc(Codes, func(c string) bool { return !isKey(c) })},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Errorf("badge codes %q, DESIGN 3.8's moment kinds %q", Codes, kinds)
			}
		})
	}
}

// TestExport checks the export's read (DESIGN 8.9; WP-SV13): it holds every
// devlog, badge and removal of the account, and changes nothing.
func TestExport(t *testing.T) {
	tests := []struct {
		name string
		do   func(t *testing.T, db *store.DB)
		// want is the ids the export holds: devlogs, badges and removals.
		want [3][]string
	}{
		{
			name: "an export holds the account's devlogs, badges and removals",
			do: func(t *testing.T, db *store.DB) {
				for i, id := range []string{"dl_1", "dl_2"} {
					publish(t, db, "u1", "d1", Devlog{ID: id, ProjectKey: "p_one", Markdown: id}, at(10+i, 0))
				}
				publish(t, db, "u2", "d9", Devlog{ID: "dl_9", ProjectKey: "p_one", Markdown: "u2's"}, at(10, 0))
				if err := in(t, db, func(ctx context.Context, tx writer.Tx) error {
					if _, _, err := Show(ctx, tx, "u1", Badge{ID: "bd_1", Code: "first_commit"}); err != nil {
						return err
					}
					return Delete(ctx, tx, "u1", "dl_2", at(12, 0).UnixMilli())
				}); err != nil {
					t.Fatal(err)
				}
			},
			want: [3][]string{{"dl_1"}, {"bd_1"}, {"dl_2"}},
		},
		{name: "an account with nothing published exports nothing", do: func(*testing.T, *store.DB) {}},
	}
	count := func(t *testing.T, db *store.DB) map[string]int {
		t.Helper()
		out := make(map[string]int)
		for _, table := range []string{"devlogs", "badges", "removed_items", "rollup_projects"} {
			var n int
			if err := db.Read.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil {
				t.Fatal(err)
			}
			out[table] = n
		}
		return out
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				db := newDB(t, engine)
				tt.do(t, db)
				before := count(t, db)
				got, err := Export(context.Background(), db.Read, "u1")
				if err != nil {
					t.Fatal(err)
				}
				var ids [3][]string
				for _, d := range got.Devlogs {
					ids[0] = append(ids[0], d.ID)
				}
				for _, b := range got.Badges {
					ids[1] = append(ids[1], b.ID)
				}
				for _, r := range got.Removed {
					ids[2] = append(ids[2], r.ID)
				}
				if !reflect.DeepEqual(ids, tt.want) {
					t.Errorf("the export holds %v, want %v", ids, tt.want)
				}
				if len(got.Devlogs) == 1 && got.Devlogs[0].Markdown != "dl_1" {
					t.Errorf("the exported devlog is %+v, want its Markdown", got.Devlogs[0])
				}
				if after := count(t, db); !reflect.DeepEqual(before, after) {
					t.Errorf("the export changed the tables' rows from %v to %v", before, after)
				}
			})
		}
	}
}
