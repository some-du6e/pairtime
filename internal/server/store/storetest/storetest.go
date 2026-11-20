// SPDX-License-Identifier: AGPL-3.0-or-later

// Package storetest opens fresh databases for tests: SQLite always, and
// Postgres as well when PAIRTIME_TEST_POSTGRES_URL is set, as postgres.yml
// sets it. A test that loops over Engines runs on both engines there and
// on SQLite alone everywhere else.
package storetest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/some-du6e/pairtime/internal/server/store"
)

// PostgresEnv names the variable that holds the Postgres URL tests use.
// Each test gets a schema of its own on that database, dropped at the end.
const PostgresEnv = "PAIRTIME_TEST_POSTGRES_URL"

// Engines returns the engines to test against.
func Engines() []store.Engine {
	if os.Getenv(PostgresEnv) != "" {
		return []store.Engine{store.SQLite, store.Postgres}
	}
	return []store.Engine{store.SQLite}
}

// Open returns a fresh, empty database on engine, closed when the test
// ends.
func Open(t testing.TB, engine store.Engine) *store.DB {
	t.Helper()
	ctx := context.Background()
	var o store.Options
	switch engine {
	case store.SQLite:
		o = store.Options{Engine: store.SQLite, Path: filepath.Join(t.TempDir(), "pairtime.db")}
	case store.Postgres:
		o = store.Options{Engine: store.Postgres, URL: postgresSchema(t)}
	default:
		t.Fatalf("storetest: unknown engine %q", engine)
	}
	db, err := store.Open(ctx, o)
	if err != nil {
		t.Fatalf("storetest: opening %s: %v", engine, err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("storetest: closing %s: %v", engine, err)
		}
	})
	return db
}

// postgresSchema creates a schema for one test and returns a URL whose
// connections use it. The schema is dropped when the test ends, after the
// test's own Cleanup closed its connections.
func postgresSchema(t testing.TB) string {
	t.Helper()
	base := os.Getenv(PostgresEnv)
	if base == "" {
		t.Fatalf("storetest: %s isn't set", PostgresEnv)
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("storetest: %s isn't a URL", PostgresEnv)
	}
	admin, err := sql.Open("postgres", base)
	if err != nil {
		t.Fatalf("storetest: %v", err)
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	schema := "t_" + hex.EncodeToString(b[:])
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		admin.Close()
		t.Fatalf("storetest: creating schema %s: %v", schema, err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Errorf("storetest: dropping schema %s: %v", schema, err)
		}
		admin.Close()
	})
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}
