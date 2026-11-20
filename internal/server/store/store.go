// SPDX-License-Identifier: AGPL-3.0-or-later

// Package store opens pairtimed's database: SQLite in WAL mode by default,
// and Postgres as the scale-out path (DESIGN 3.10; SP 7.4, 9.5).
//
// Every write goes through the one writer goroutine (package writer), so an
// open database has two handles. Write is a single connection that only the
// writer and the migrations use. Read is a pool for everything else, and it
// refuses writes (PRAGMA query_only on SQLite, default_transaction_read_only
// on Postgres), so a write that bypasses the writer fails at once instead of
// racing it for the lock.
//
// SQL that pairtimed runs must work on both engines, so that moving to
// Postgres is a configuration change and not a port (SP 7.4):
//
//   - Placeholders are $1, $2, and so on. modernc.org/sqlite binds $NNN by
//     number, as Postgres does.
//   - Upserts are INSERT ... ON CONFLICT (...) DO UPDATE SET col =
//     excluded.col. Never INSERT OR REPLACE or INSERT OR IGNORE. A
//     migration may not upsert, since that rewrites rows the previous
//     release wrote; package migrations allows ON CONFLICT DO NOTHING.
//   - Times are BIGINT epoch milliseconds, and ids are UUIDv7 as TEXT.
//   - Column types are TEXT and BIGINT, and BYTEA for raw bytes. No
//     AUTOINCREMENT, SERIAL, STRICT or WITHOUT ROWID.
//
// postgres.yml runs the server tests against Postgres 18 nightly and on every
// PR that touches this package, the migrations or the writer.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
)

// Engine names a database engine.
type Engine string

// The engines pairtimed runs on.
const (
	SQLite   Engine = "sqlite"
	Postgres Engine = "postgres"
)

// Options say which database to open.
type Options struct {
	// Engine is SQLite or Postgres. Empty means SQLite.
	Engine Engine
	// Path is the SQLite database file. A missing file is created with
	// mode 0600, and a missing directory with mode 0700, unless NoCreate
	// is set. An existing file that group or others can read, as a
	// Litestream restore leaves it, loses that access, and so do its -wal
	// and -shm files (not on Windows).
	Path string
	// NoCreate makes Open fail with ErrNoDatabase, and create nothing,
	// when the SQLite file is missing. Only serve creates the database:
	// Litestream restores from the replica when the file is missing and
	// only then starts serve, so a file another command created would
	// stop the restore (DESIGN 10.1). Postgres ignores it.
	NoCreate bool
	// URL is the Postgres connection URL,
	// postgres://user:password@host:port/database?sslmode=....
	URL string
	// Logger gets a warning when Open changes a database file's mode.
	// Nil means slog.Default().
	Logger *slog.Logger
	// SQLiteVersion, when set, stands in for what sqlite_version() reports.
	// It exists for the test that checks the refusal of an old SQLite
	// (WP-SV1); leave it empty everywhere else.
	SQLiteVersion string
}

// DB is an open database.
type DB struct {
	Engine Engine
	// Version is the engine's version: "3.53.4" for SQLite, the
	// server_version setting for Postgres.
	Version string
	// Write is the one connection that writes go through: the writer
	// goroutine's batches and the migrations. On SQLite its transactions
	// start with BEGIN IMMEDIATE.
	Write *sql.DB
	// Read is the pool for reads. It refuses writes.
	Read *sql.DB
}

// Open opens the database o names. For SQLite it first checks the library
// version against MinSQLite, before it touches the database file.
func Open(ctx context.Context, o Options) (*DB, error) {
	switch o.Engine {
	case SQLite, "":
		return openSQLite(ctx, o)
	case Postgres:
		return openPostgres(ctx, o)
	default:
		return nil, fmt.Errorf("store: unknown engine %q; use %q or %q", o.Engine, SQLite, Postgres)
	}
}

// Close closes both handles.
func (db *DB) Close() error {
	return errors.Join(db.Write.Close(), db.Read.Close())
}

// Execer is the part of *sql.DB and *sql.Tx that WriteProbe needs. The
// writer's transactions satisfy it.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// WriteProbe records nowMs in ops_probe, the one-row table /readyz writes to
// prove the database takes writes (DESIGN 10.4). Run it through the writer,
// so a ready answer also means the writer is committing.
func WriteProbe(ctx context.Context, tx Execer, nowMs int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO ops_probe (id, written_at_ms) VALUES (1, $1)
ON CONFLICT (id) DO UPDATE SET written_at_ms = excluded.written_at_ms`, nowMs)
	if err != nil {
		return fmt.Errorf("store: writing the readiness probe: %w", err)
	}
	return nil
}
