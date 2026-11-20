// SPDX-License-Identifier: AGPL-3.0-or-later

package migrations_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/some-du6e/pairtime/internal/server/migrations"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
)

func TestEmbedded(t *testing.T) {
	ms, err := migrations.Embedded()
	if err != nil {
		t.Fatalf("the embedded migrations don't parse: %v", err)
	}
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "there is at least one migration", ok: len(ms) > 0},
		{name: "the first is 0001_ops_probe", ok: len(ms) > 0 && ms[0].ID == "0001_ops_probe"},
		{name: "every migration has a checksum", ok: func() bool {
			for _, m := range ms {
				if len(m.SHA256) != 64 {
					return false
				}
			}
			return true
		}()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Errorf("embedded migrations: %+v", ms)
			}
		})
	}
}

func TestSplit(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []string
		wantErr bool
	}{
		{name: "a statement ends with a semicolon at the end of a line", in: "CREATE TABLE a (\n  id BIGINT\n);\n", want: []string{"CREATE TABLE a (\nid BIGINT\n)"}},
		{name: "two statements split", in: "CREATE TABLE a (id BIGINT);\nCREATE INDEX a_id ON a (id);\n", want: []string{"CREATE TABLE a (id BIGINT)", "CREATE INDEX a_id ON a (id)"}},
		{name: "comment lines are dropped", in: "-- a note\nCREATE TABLE a (\n  -- the key\n  id BIGINT\n);\n", want: []string{"CREATE TABLE a (\nid BIGINT\n)"}},
		{name: "CRLF line endings split the same", in: "CREATE TABLE a (id BIGINT);\r\nCREATE TABLE b (id BIGINT);\r\n", want: []string{"CREATE TABLE a (id BIGINT)", "CREATE TABLE b (id BIGINT)"}},
		{name: "a lone semicolon line ends the statement above", in: "CREATE TABLE a (id BIGINT)\n;\n", want: []string{"CREATE TABLE a (id BIGINT)"}},
		{name: "a file of only comments has no statements", in: "-- nothing yet\n", want: nil},
		{name: "a statement without its semicolon is refused", in: "CREATE TABLE a (id BIGINT)\n", wantErr: true},
		{name: "an empty statement is refused", in: ";\n", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := migrations.Split(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Split error = %v, want error %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Split = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCheckAdditive(t *testing.T) {
	tests := []struct {
		name   string
		stmt   string
		wantOK bool
	}{
		{name: "CREATE TABLE adds", stmt: "CREATE TABLE devices (id TEXT PRIMARY KEY)", wantOK: true},
		{name: "CREATE INDEX adds", stmt: "CREATE INDEX devices_user ON devices (user_id)", wantOK: true},
		{name: "CREATE UNIQUE INDEX adds", stmt: "create unique index d_tok on devices (token_hash)", wantOK: true},
		{name: "INSERT adds rows", stmt: "INSERT INTO ops_probe (id, written_at_ms) VALUES (1, 0)", wantOK: true},
		{name: "INSERT ... ON CONFLICT DO NOTHING adds rows and leaves existing ones alone", stmt: "INSERT INTO settings (k, v) VALUES ('mode', 'v1') ON CONFLICT (k) DO NOTHING", wantOK: true},
		{name: "INSERT ... ON CONFLICT DO UPDATE rewrites existing rows", stmt: "INSERT INTO settings (k, v) VALUES ('mode', 'v2') ON CONFLICT (k) DO UPDATE SET v = excluded.v"},
		{name: "an upsert split across lines rewrites existing rows too", stmt: "insert into settings (k, v)\nvalues ('mode', 'v2')\non conflict (k) do\nupdate set v = excluded.v"},
		{name: "a nullable column adds", stmt: "ALTER TABLE devices ADD COLUMN os TEXT", wantOK: true},
		{name: "a NOT NULL column with a default adds", stmt: "ALTER TABLE devices ADD COLUMN rev BIGINT NOT NULL DEFAULT 0", wantOK: true},
		{name: "a NOT NULL column without a default breaks the previous release's inserts", stmt: "ALTER TABLE devices ADD COLUMN rev BIGINT NOT NULL"},
		{name: "DROP TABLE removes", stmt: "DROP TABLE devices"},
		{name: "DROP COLUMN removes", stmt: "ALTER TABLE devices DROP COLUMN os"},
		{name: "RENAME rewrites", stmt: "ALTER TABLE devices RENAME TO phones"},
		{name: "RENAME COLUMN rewrites", stmt: "ALTER TABLE devices RENAME COLUMN os TO platform"},
		{name: "UPDATE rewrites", stmt: "UPDATE devices SET os = 'linux'"},
		{name: "DELETE removes", stmt: "DELETE FROM devices"},
		{name: "DROP INDEX removes", stmt: "DROP INDEX devices_user"},
		{name: "two statements on one line hide the second", stmt: "CREATE TABLE a (id BIGINT); DROP TABLE devices"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := migrations.CheckAdditive(tt.stmt)
			if (err == nil) != tt.wantOK {
				t.Errorf("CheckAdditive(%q) = %v, want ok %v", tt.stmt, err, tt.wantOK)
			}
		})
	}
}

func TestParse(t *testing.T) {
	good := &fstest.MapFile{Data: []byte("CREATE TABLE a (id BIGINT PRIMARY KEY);\n")}
	tests := []struct {
		name    string
		fsys    fstest.MapFS
		wantIDs []string
		wantErr string
	}{
		{name: "files apply in number order", fsys: fstest.MapFS{"0002_b.sql": good, "0001_a.sql": good, "README.md": good}, wantIDs: []string{"0001_a", "0002_b"}},
		{name: "a file not named NNNN_name.sql is refused", fsys: fstest.MapFS{"1_a.sql": good}, wantErr: "isn't named NNNN_name.sql"},
		{name: "an uppercase name is refused", fsys: fstest.MapFS{"0001_Ops.sql": good}, wantErr: "isn't named NNNN_name.sql"},
		{name: "two files sharing a number are refused", fsys: fstest.MapFS{"0001_a.sql": good, "0001_b.sql": good}, wantErr: "share the number 0001"},
		{name: "a file without statements is refused", fsys: fstest.MapFS{"0001_a.sql": {Data: []byte("-- empty\n")}}, wantErr: "no statements"},
		{name: "a statement that isn't additive is refused", fsys: fstest.MapFS{"0001_a.sql": {Data: []byte("DROP TABLE x;\n")}}, wantErr: "statement 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ms, err := migrations.Parse(tt.fsys)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Parse error = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, m := range ms {
				ids = append(ids, m.ID)
			}
			if !reflect.DeepEqual(ids, tt.wantIDs) {
				t.Errorf("ids = %v, want %v", ids, tt.wantIDs)
			}
		})
	}
}

func TestChecksum(t *testing.T) {
	sum := func(t *testing.T, text string) string {
		t.Helper()
		ms, err := migrations.Parse(fstest.MapFS{"0001_a.sql": {Data: []byte(text)}})
		if err != nil {
			t.Fatal(err)
		}
		return ms[0].SHA256
	}
	const base = "CREATE TABLE a (\n  id BIGINT\n);\n"
	tests := []struct {
		name     string
		text     string
		wantSame bool
	}{
		{name: "a whole-line comment doesn't change the checksum", text: "-- why a exists\nCREATE TABLE a (\n  -- the key\n  id BIGINT\n);\n", wantSame: true},
		{name: "indentation doesn't change the checksum", text: "CREATE TABLE a (\n        id BIGINT\n);\n", wantSame: true},
		{name: "a comment after code is part of its statement, so it changes the checksum", text: "CREATE TABLE a (\n  id BIGINT -- the key\n);\n"},
		{name: "a changed column type changes the checksum", text: "CREATE TABLE a (\n  id TEXT\n);\n"},
	}
	want := sum(t, base)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if same := sum(t, tt.text) == want; same != tt.wantSame {
				t.Errorf("checksum unchanged = %v, want %v, for:\n%s", same, tt.wantSame, tt.text)
			}
		})
	}
}

// pinned is the checksum of every embedded migration. A deployed database
// records each migration's checksum when it applies it, and pairtimed stops
// at start when a file no longer matches (ErrChanged), so a merged
// migration is never edited: its fix goes in a new file. Applying
// migrations to empty databases can't catch an edit, and this list does. A
// new migration adds its line. A changed line is an edit to a migration
// that servers may have applied, for a reviewer to refuse.
var pinned = []struct{ id, sha256 string }{
	{"0001_ops_probe", "03e43c38a5ae6670785d28dd6926750ec177a35eba1ec04a94537a7d07fd2738"},
	{"0002_accounts", "ca7374033c9d51e025ce6a6ee719b4d288d8896801ea811d83cb2e6a505a03b2"},
	{"0003_server_keys", "8c845044d294360fed77368260174dbd79f8993ace28c52af0906aab266b1080"},
}

func TestPinnedChecksums(t *testing.T) {
	ms, err := migrations.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	embedded := make(map[string]string, len(ms))
	for _, m := range ms {
		embedded[m.ID] = m.SHA256
	}
	for _, p := range pinned {
		t.Run(p.id+" keeps the checksum servers recorded when they applied it", func(t *testing.T) {
			got, ok := embedded[p.id]
			if !ok {
				t.Fatalf("%s is pinned but no longer embedded; servers that applied it would report it as a newer release's", p.id)
			}
			if got != p.sha256 {
				t.Errorf("%s has checksum %s, but %s is pinned. Every server that applied it would stop at start: restore the file and put the change in a new migration", p.id, got, p.sha256)
			}
		})
	}
	t.Run("every embedded migration is pinned", func(t *testing.T) {
		pins := make(map[string]bool, len(pinned))
		for _, p := range pinned {
			pins[p.id] = true
		}
		for _, m := range ms {
			if !pins[m.ID] {
				t.Errorf("%s isn't pinned: add {%q, %q} to pinned in migrations_test.go", m.ID, m.ID, m.SHA256)
			}
		}
	})
}

// TestApply applies migrations from empty on every engine Engines returns:
// SQLite in ci.yml, and Postgres 18 as well in postgres.yml.
func TestApply(t *testing.T) {
	embedded, err := migrations.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	var embeddedIDs []string
	for _, m := range embedded {
		embeddedIDs = append(embeddedIDs, m.ID)
	}
	now := func() time.Time { return time.UnixMilli(1_763_596_800_000) }
	ctx := context.Background()

	for _, engine := range storetest.Engines() {
		t.Run(string(engine), func(t *testing.T) {
			tests := []struct {
				name string
				run  func(t *testing.T, db *store.DB)
			}{
				{
					name: "migrations apply from empty",
					run: func(t *testing.T, db *store.DB) {
						res, err := migrations.Apply(ctx, db, embedded, now)
						if err != nil {
							t.Fatal(err)
						}
						if !reflect.DeepEqual(res.Applied, embeddedIDs) || len(res.Unknown) != 0 {
							t.Errorf("Apply = %+v, want every embedded migration applied: %v", res, embeddedIDs)
						}
						if err := store.WriteProbe(ctx, db.Write, 1); err != nil {
							t.Errorf("ops_probe isn't usable after migrating: %v", err)
						}
					},
				},
				{
					name: "applying again changes nothing",
					run: func(t *testing.T, db *store.DB) {
						mustApply(t, db, embedded)
						res, err := migrations.Apply(ctx, db, embedded, now)
						if err != nil || len(res.Applied) != 0 {
							t.Errorf("second Apply = %+v, %v; want nothing applied", res, err)
						}
					},
				},
				{
					name: "a migration whose statements changed after it was applied stops Apply",
					run: func(t *testing.T, db *store.DB) {
						mustApply(t, db, embedded)
						if _, err := db.Write.ExecContext(ctx, "UPDATE schema_migrations SET sha256 = $1 WHERE id = $2", strings.Repeat("0", 64), embedded[0].ID); err != nil {
							t.Fatal(err)
						}
						_, err := migrations.Apply(ctx, db, embedded, now)
						if !errors.Is(err, migrations.ErrChanged) {
							t.Errorf("Apply error = %v, want ErrChanged", err)
						}
					},
				},
				{
					name: "a migration only the database has, from a newer release, is reported and left alone",
					run: func(t *testing.T, db *store.DB) {
						mustApply(t, db, embedded)
						if _, err := db.Write.ExecContext(ctx, "INSERT INTO schema_migrations (id, sha256, applied_at_ms) VALUES ($1, $2, $3)", "9999_from_a_newer_release", strings.Repeat("a", 64), int64(0)); err != nil {
							t.Fatal(err)
						}
						res, err := migrations.Apply(ctx, db, embedded, now)
						if err != nil {
							t.Fatalf("Apply refused to run on a newer schema: %v", err)
						}
						if !reflect.DeepEqual(res.Unknown, []string{"9999_from_a_newer_release"}) {
							t.Errorf("Unknown = %v, want the newer release's migration", res.Unknown)
						}
					},
				},
				{
					name: "a failing migration leaves nothing behind, and the ones before it stay applied",
					run: func(t *testing.T, db *store.DB) {
						ms, err := migrations.Parse(fstest.MapFS{
							"0001_a.sql": {Data: []byte("CREATE TABLE m_a (id BIGINT PRIMARY KEY);\n")},
							"0002_b.sql": {Data: []byte("CREATE TABLE m_b (id BIGINT PRIMARY KEY);\nINSERT INTO m_missing (id) VALUES (1);\n")},
						})
						if err != nil {
							t.Fatal(err)
						}
						res, err := migrations.Apply(ctx, db, ms, now)
						if err == nil || !strings.Contains(err.Error(), "0002_b") {
							t.Fatalf("Apply error = %v, want one naming 0002_b", err)
						}
						if !reflect.DeepEqual(res.Applied, []string{"0001_a"}) {
							t.Errorf("Applied = %v, want [0001_a]", res.Applied)
						}
						if _, err := db.Read.ExecContext(ctx, "SELECT count(*) FROM m_b"); err == nil {
							t.Error("m_b exists: the failed migration's first statement wasn't rolled back")
						}
						states, err := migrations.Status(ctx, db, ms)
						if err != nil {
							t.Fatal(err)
						}
						if len(states) != 2 || states[0].AppliedAt.IsZero() || !states[1].AppliedAt.IsZero() {
							t.Errorf("Status = %+v, want 0001_a applied and 0002_b pending", states)
						}
					},
				},
				{
					name: "Status lists pending, applied and unknown migrations without writing",
					run: func(t *testing.T, db *store.DB) {
						before, err := migrations.Status(ctx, db, embedded)
						if err != nil {
							t.Fatal(err)
						}
						for _, s := range before {
							if !s.AppliedAt.IsZero() || !s.Known {
								t.Errorf("before migrating, %+v isn't pending", s)
							}
						}
						if _, err := migrations.Apply(ctx, db, embedded, now); err != nil {
							t.Fatal(err)
						}
						if _, err := db.Write.ExecContext(ctx, "INSERT INTO schema_migrations (id, sha256, applied_at_ms) VALUES ($1, $2, $3)", "9999_newer", strings.Repeat("b", 64), int64(5)); err != nil {
							t.Fatal(err)
						}
						after, err := migrations.Status(ctx, db, embedded)
						if err != nil {
							t.Fatal(err)
						}
						if len(after) != len(embedded)+1 || after[len(after)-1].ID != "9999_newer" || after[len(after)-1].Known {
							t.Fatalf("Status = %+v, want the embedded migrations then 9999_newer, unknown", after)
						}
						for _, s := range after[:len(embedded)] {
							if !s.AppliedAt.Equal(now()) {
								t.Errorf("%s applied at %v, want %v", s.ID, s.AppliedAt, now())
							}
						}
					},
				},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					tt.run(t, storetest.Open(t, engine))
				})
			}
		})
	}
}

func mustApply(t *testing.T, db *store.DB, ms []migrations.Migration) {
	t.Helper()
	if _, err := migrations.Apply(context.Background(), db, ms, time.Now); err != nil {
		t.Fatal(err)
	}
}
