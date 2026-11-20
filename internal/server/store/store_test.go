// SPDX-License-Identifier: AGPL-3.0-or-later

package store_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/migrations"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
)

func TestParseVersion(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    store.Version
		wantErr bool
	}{
		{name: "X.Y.Z parses", in: "3.53.4", want: store.Version{Major: 3, Minor: 53, Patch: 4}},
		{name: "zeros parse", in: "3.0.0", want: store.Version{Major: 3}},
		{name: "two parts are refused", in: "3.51", wantErr: true},
		{name: "four parts are refused", in: "3.51.3.1", wantErr: true},
		{name: "a sign is refused", in: "3.+51.3", wantErr: true},
		{name: "a suffix is refused", in: "3.51.3-beta", wantErr: true},
		{name: "an empty part is refused", in: "3..3", wantErr: true},
		{name: "an empty string is refused", in: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := store.ParseVersion(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseVersion(%q) error = %v, want error %v", tt.in, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("ParseVersion(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestOpenRefusesOldSQLite(t *testing.T) {
	tests := []struct {
		name       string
		version    string
		wantRefuse bool
	}{
		{name: "3.51.2, the last release with the WAL-reset bug, is refused", version: "3.51.2", wantRefuse: true},
		{name: "3.7.0, the first release with it, is refused", version: "3.7.0", wantRefuse: true},
		{name: "3.44.6, a backport, is refused by the single floor", version: "3.44.6", wantRefuse: true},
		{name: "3.50.7, a backport, is refused by the single floor", version: "3.50.7", wantRefuse: true},
		{name: "a version that can't be read is refused", version: "three", wantRefuse: true},
		{name: "3.51.3, the fix, is accepted", version: "3.51.3"},
		{name: "3.52.0 is accepted", version: "3.52.0"},
		{name: "4.0.0 is accepted", version: "4.0.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "sub", "pairtime.db")
			db, err := store.Open(context.Background(), store.Options{Engine: store.SQLite, Path: path, SQLiteVersion: tt.version})
			if !tt.wantRefuse {
				if err != nil {
					t.Fatalf("Open with SQLite %s: %v", tt.version, err)
				}
				db.Close()
				return
			}
			if err == nil {
				db.Close()
				t.Fatalf("Open accepted SQLite %s", tt.version)
			}
			if !errors.Is(err, store.ErrSQLiteTooOld) {
				t.Errorf("Open error = %v, want one wrapping ErrSQLiteTooOld", err)
			}
			if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("Open created %s before refusing (stat: %v)", filepath.Dir(path), err)
			}
		})
	}
}

func TestOpenNoCreate(t *testing.T) {
	tests := []struct {
		name    string
		create  bool // create the database before opening it with NoCreate
		wantErr error
	}{
		{name: "NoCreate refuses a missing database and creates neither it nor its directory", wantErr: store.ErrNoDatabase},
		{name: "NoCreate opens a database that exists", create: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "sub", "pairtime.db")
			if tt.create {
				db, err := store.Open(ctx, store.Options{Engine: store.SQLite, Path: path})
				if err != nil {
					t.Fatal(err)
				}
				db.Close()
			}
			db, err := store.Open(ctx, store.Options{Engine: store.SQLite, Path: path, NoCreate: true})
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("Open: %v", err)
				}
				db.Close()
				return
			}
			if err == nil {
				db.Close()
				t.Fatal("Open succeeded")
			}
			if !errors.Is(err, tt.wantErr) || !strings.Contains(err.Error(), path) {
				t.Errorf("Open error = %v, want one wrapping %v that names %s", err, tt.wantErr, path)
			}
			if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("Open created %s (stat: %v)", filepath.Dir(path), err)
			}
		})
	}
}

func TestOpenSQLite(t *testing.T) {
	db := storetest.Open(t, store.SQLite)
	ctx := context.Background()
	pragma := func(t *testing.T, name string) string {
		t.Helper()
		var v string
		if err := db.Write.QueryRowContext(ctx, "PRAGMA "+name).Scan(&v); err != nil {
			t.Fatalf("PRAGMA %s: %v", name, err)
		}
		return v
	}
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "the bundled SQLite is at least 3.51.3",
			run: func(t *testing.T) {
				v, err := store.ParseVersion(db.Version)
				if err != nil || v.Less(store.MinSQLite) {
					t.Errorf("bundled SQLite = %q (%v), want at least %s", db.Version, err, store.MinSQLite)
				}
			},
		},
		{
			name: "the database is in WAL mode",
			run: func(t *testing.T) {
				if got := pragma(t, "journal_mode"); !strings.EqualFold(got, "wal") {
					t.Errorf("journal_mode = %q, want wal", got)
				}
			},
		},
		{
			name: "the write connection syncs NORMAL",
			run: func(t *testing.T) {
				if got := pragma(t, "synchronous"); got != "1" {
					t.Errorf("synchronous = %s, want 1 (NORMAL)", got)
				}
			},
		},
		{
			name: "the write connection zeroes what it deletes (secure_delete)",
			run: func(t *testing.T) {
				if got := pragma(t, "secure_delete"); got != "1" {
					t.Errorf("secure_delete = %s, want 1", got)
				}
			},
		},
		{
			name: "a deleted row's bytes are gone from the database file",
			run: func(t *testing.T) {
				// The row reaches the file through a checkpoint before it is
				// deleted, so only secure_delete can take its bytes out.
				const marker = "pairtime-secure-delete-check-5c1e"
				for _, q := range []string{
					"CREATE TABLE secure_delete_check (v TEXT)",
					"INSERT INTO secure_delete_check (v) VALUES ('" + marker + "')",
					"PRAGMA wal_checkpoint(TRUNCATE)",
					"DELETE FROM secure_delete_check",
					"PRAGMA wal_checkpoint(TRUNCATE)",
				} {
					if _, err := db.Write.ExecContext(ctx, q); err != nil {
						t.Fatalf("%s: %v", q, err)
					}
				}
				path := sqlitePath(t, db)
				for _, f := range []string{path, path + "-wal"} {
					b, err := os.ReadFile(f)
					if err != nil && !errors.Is(err, fs.ErrNotExist) {
						t.Fatal(err)
					}
					if strings.Contains(string(b), marker) {
						t.Errorf("%s still holds the deleted row's text", filepath.Base(f))
					}
				}
			},
		},
		{
			name: "foreign keys are enforced, as on Postgres",
			run: func(t *testing.T) {
				if got := pragma(t, "foreign_keys"); got != "1" {
					t.Errorf("foreign_keys = %s, want 1", got)
				}
			},
		},
		{
			name: "a write transaction takes the lock when it begins",
			run: func(t *testing.T) {
				tx, err := db.Write.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				// A second connection that doesn't wait must find the
				// write lock already held: BEGIN IMMEDIATE, not DEFERRED.
				path := sqlitePath(t, db)
				other, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(0)")
				if err != nil {
					t.Fatal(err)
				}
				defer other.Close()
				if _, err := other.ExecContext(ctx, "BEGIN IMMEDIATE"); err == nil {
					other.ExecContext(ctx, "ROLLBACK")
					t.Error("a second connection took the write lock while a write transaction was open")
				}
			},
		},
		{
			name: "a new database file is created with mode 0600",
			run: func(t *testing.T) {
				if runtime.GOOS == "windows" {
					t.Skip("file modes are a Unix check")
				}
				fi, err := os.Stat(sqlitePath(t, db))
				if err != nil {
					t.Fatal(err)
				}
				if perm := fi.Mode().Perm(); perm != 0o600 {
					t.Errorf("mode = %04o, want 0600", perm)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

func TestOpenTightensModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are a Unix check")
	}
	tests := []struct {
		name    string
		mode    fs.FileMode // the three files' mode before Open
		want    fs.FileMode
		wantLog bool
	}{
		{name: "a database a restore left readable by everyone is made 0600, with its -wal and -shm", mode: 0o644, want: 0o600, wantLog: true},
		{name: "a database the group can read is made 0600, with its -wal and -shm", mode: 0o640, want: 0o600, wantLog: true},
		{name: "a 0600 database is left as it is, without a warning", mode: 0o600, want: 0o600},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "pairtime.db")
			// This handle stays open, so the -wal and -shm files exist when
			// the second Open finds the database, as they do when Litestream
			// holds it.
			first, err := store.Open(ctx, store.Options{Engine: store.SQLite, Path: path})
			if err != nil {
				t.Fatal(err)
			}
			defer first.Close()
			if _, err := first.Write.ExecContext(ctx, "CREATE TABLE t (id BIGINT)"); err != nil {
				t.Fatal(err)
			}
			files := []string{path, path + "-wal", path + "-shm"}
			for _, f := range files {
				if err := os.Chmod(f, tt.mode); err != nil {
					t.Fatalf("setting %s to %04o: %v", filepath.Base(f), tt.mode, err)
				}
			}

			var logged bytes.Buffer
			second, err := store.Open(ctx, store.Options{Engine: store.SQLite, Path: path, Logger: slog.New(slog.NewTextHandler(&logged, nil))})
			if err != nil {
				t.Fatal(err)
			}
			defer second.Close()
			for _, f := range files {
				fi, err := os.Stat(f)
				if err != nil {
					t.Fatal(err)
				}
				if perm := fi.Mode().Perm(); perm != tt.want {
					t.Errorf("%s has mode %04o after Open, want %04o", filepath.Base(f), perm, tt.want)
				}
			}
			if got := strings.Contains(logged.String(), "tightened"); got != tt.wantLog {
				t.Errorf("logged a tightening = %v, want %v; log:\n%s", got, tt.wantLog, logged.String())
			}
		})
	}
}

// sqlitePath finds the file behind an open SQLite database.
func sqlitePath(t *testing.T, db *store.DB) string {
	t.Helper()
	var seq int
	var name, file string
	if err := db.Read.QueryRow("PRAGMA database_list").Scan(&seq, &name, &file); err != nil {
		t.Fatalf("PRAGMA database_list: %v", err)
	}
	return file
}

func TestOpenRefusesBadOptions(t *testing.T) {
	tests := []struct {
		name string
		o    store.Options
	}{
		{name: "an unknown engine is refused", o: store.Options{Engine: "mysql"}},
		{name: "SQLite needs a path", o: store.Options{Engine: store.SQLite}},
		{name: "an SQLite path can't hold a '?'", o: store.Options{Engine: store.SQLite, Path: "/tmp/x?mode=ro"}},
		{name: "an SQLite path can't be a file: URI", o: store.Options{Engine: store.SQLite, Path: "file:/tmp/x.db"}},
		{name: "Postgres needs a URL", o: store.Options{Engine: store.Postgres}},
		{name: "a Postgres URL must be postgres://", o: store.Options{Engine: store.Postgres, URL: "mysql://u:p@h/db"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, err := store.Open(context.Background(), tt.o)
			if err == nil {
				db.Close()
				t.Fatal("Open succeeded")
			}
			if strings.Contains(err.Error(), "u:p@") {
				t.Errorf("the error quotes the URL's password: %v", err)
			}
		})
	}
}

// TestPortableSQL runs the SQL forms package store's rules allow on every
// engine Engines returns: SQLite here and in ci.yml, both in postgres.yml.
func TestPortableSQL(t *testing.T) {
	for _, engine := range storetest.Engines() {
		t.Run(string(engine), func(t *testing.T) {
			db := storetest.Open(t, engine)
			ctx := context.Background()
			ms, err := migrations.Embedded()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := migrations.Apply(ctx, db, ms, time.Now); err != nil {
				t.Fatal(err)
			}
			tests := []struct {
				name string
				run  func(t *testing.T)
			}{
				{
					name: "$n placeholders bind by number, in any order",
					run: func(t *testing.T) {
						var first, second string
						err := db.Read.QueryRowContext(ctx, "SELECT CAST($2 AS TEXT), CAST($1 AS TEXT)", "one", "two").Scan(&first, &second)
						if err != nil {
							t.Fatal(err)
						}
						if first != "two" || second != "one" {
							t.Errorf("SELECT $2, $1 = %q, %q; want \"two\", \"one\"", first, second)
						}
					},
				},
				{
					name: "the readiness probe upserts its one row with ON CONFLICT DO UPDATE",
					run: func(t *testing.T) {
						for _, at := range []int64{1_763_596_800_000, 1_763_596_800_123} {
							if err := store.WriteProbe(ctx, db.Write, at); err != nil {
								t.Fatal(err)
							}
						}
						var n int
						var at int64
						if err := db.Read.QueryRowContext(ctx, "SELECT count(*), max(written_at_ms) FROM ops_probe").Scan(&n, &at); err != nil {
							t.Fatal(err)
						}
						if n != 1 || at != 1_763_596_800_123 {
							t.Errorf("ops_probe has %d rows, latest %d; want 1 row, 1763596800123", n, at)
						}
					},
				},
				{
					name: "the read pool refuses writes",
					run: func(t *testing.T) {
						if _, err := db.Read.ExecContext(ctx, "CREATE TABLE read_pool_write (id BIGINT)"); err == nil {
							t.Error("the read pool created a table")
						}
						if err := store.WriteProbe(ctx, db.Read, 1); err == nil {
							t.Error("the read pool wrote the probe row")
						}
					},
				},
			}
			for _, tt := range tests {
				t.Run(tt.name, tt.run)
			}
		})
	}
}
