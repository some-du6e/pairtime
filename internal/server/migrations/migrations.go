// SPDX-License-Identifier: AGPL-3.0-or-later

// Package migrations holds pairtimed's schema and applies it at start
// (DESIGN 10.5 step 6).
//
// Each migration is one file in sql/, named NNNN_name.sql. They apply in
// that order, each in its own transaction, and later work packages add
// their tables as new files. Migrations are additive in v1, so the previous
// release still runs on a database the next one migrated, and a rollback is
// the previous image tag: a migration may create tables and indexes, add
// columns that are nullable or have a default, and insert rows, with ON
// CONFLICT DO NOTHING but never DO UPDATE, and nothing else. Parse refuses
// any other statement, so a test over the embedded files catches it in CI.
//
// The database records each applied migration with the SHA-256 of its
// statements. A migration whose statements changed after it was applied
// stops pairtimed at start. A migration the database has and this binary
// doesn't, because a newer release applied it, is reported and left alone.
// The tests pin every file's checksum, so CI fails an edit to a merged
// migration before any server sees it: a fix goes in a new file.
//
// In a file, a statement ends with a semicolon at the end of a line, and a
// line starting with "--" is a comment. A comment after code on the same
// line stays in its statement and counts toward the checksum. Statements
// follow the portable SQL rules in package store; postgres.yml applies
// every migration to Postgres 18 as well as SQLite.
package migrations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/server/store"
)

//go:embed sql/*.sql
var embedded embed.FS

// Migration is one schema change.
type Migration struct {
	// ID is the file name without ".sql", such as "0001_ops_probe".
	ID string
	// Statements are the SQL statements, without their semicolons.
	Statements []string
	// SHA256 is the hex SHA-256 of the statements as Split returns them.
	// Whole-line comments and indentation don't count, so editing those
	// is safe. A comment after code on the same line is part of its
	// statement, so it counts.
	SHA256 string
}

// fileName is NNNN_name.sql.
var fileName = regexp.MustCompile(`^([0-9]{4})_[a-z0-9_]+\.sql$`)

// Embedded returns the migrations built into pairtimed, in order.
func Embedded() ([]Migration, error) {
	sub, err := fs.Sub(embedded, "sql")
	if err != nil {
		return nil, fmt.Errorf("migrations: %w", err)
	}
	return Parse(sub)
}

// Parse reads every .sql file at the root of fsys as a migration and
// returns them in order. It refuses a file with a bad name, a number two
// files share, or a statement that isn't additive.
func Parse(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("migrations: %w", err)
	}
	var ms []Migration
	numbers := make(map[string]string)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		m := fileName.FindStringSubmatch(name)
		if m == nil {
			return nil, fmt.Errorf("migrations: %s isn't named NNNN_name.sql, with a lowercase name", name)
		}
		if other, dup := numbers[m[1]]; dup {
			return nil, fmt.Errorf("migrations: %s and %s share the number %s; renumber the newer one", other, name, m[1])
		}
		numbers[m[1]] = name
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("migrations: %w", err)
		}
		stmts, err := Split(string(data))
		if err != nil {
			return nil, fmt.Errorf("migrations: %s: %w", name, err)
		}
		if len(stmts) == 0 {
			return nil, fmt.Errorf("migrations: %s has no statements", name)
		}
		for i, s := range stmts {
			if err := CheckAdditive(s); err != nil {
				return nil, fmt.Errorf("migrations: %s, statement %d: %w", name, i+1, err)
			}
		}
		ms = append(ms, Migration{ID: strings.TrimSuffix(name, ".sql"), Statements: stmts, SHA256: digest(stmts)})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].ID < ms[j].ID })
	return ms, nil
}

// Split returns the statements of a migration file. A statement ends at a
// line whose last character is a semicolon. A line starting with "--" is a
// comment, which Split drops; a "--" after code stays in the statement,
// for the database to read as a comment. Each statement comes back with
// its lines trimmed and without the semicolon.
func Split(text string) ([]string, error) {
	var stmts, cur []string
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "--") {
			continue
		}
		if body, ok := strings.CutSuffix(t, ";"); ok {
			if body = strings.TrimSpace(body); body != "" {
				cur = append(cur, body)
			}
			if len(cur) == 0 {
				return nil, errors.New("a semicolon ends an empty statement")
			}
			stmts = append(stmts, strings.Join(cur, "\n"))
			cur = nil
			continue
		}
		cur = append(cur, t)
	}
	if len(cur) > 0 {
		return nil, errors.New("the last statement doesn't end with a semicolon at the end of a line")
	}
	return stmts, nil
}

// CheckAdditive refuses a statement that could remove, rename or rewrite
// anything. It must create a table or an index, add a column that is
// nullable or has a default, or insert rows without touching the ones
// already there (DESIGN 10.5 step 6). It reads words, not SQL, so it
// catches mistakes and not a statement written to slip past it.
func CheckAdditive(stmt string) error {
	if strings.Contains(stmt, ";") {
		return errors.New("it holds a ';', so it may be two statements; end each statement with a semicolon at the end of a line")
	}
	words := strings.Fields(strings.ToUpper(stmt))
	starts := func(prefix ...string) bool {
		return len(words) >= len(prefix) && slices.Equal(words[:len(prefix)], prefix)
	}
	switch {
	case starts("CREATE", "TABLE"), starts("CREATE", "INDEX"), starts("CREATE", "UNIQUE", "INDEX"):
		return nil
	case starts("INSERT", "INTO"):
		// The upsert package store recommends for pairtimed's own writes
		// rewrites a row that is already there. ON CONFLICT DO NOTHING
		// leaves it alone, so that form stays additive.
		if hasPair(words, "DO", "UPDATE") {
			return errors.New("INSERT ... ON CONFLICT DO UPDATE rewrites rows that are already there, so the previous release can find its rows changed; use ON CONFLICT DO NOTHING (DESIGN 10.5)")
		}
		return nil
	case starts("ALTER", "TABLE"):
		// ALTER TABLE name ADD COLUMN column type ...
		if len(words) < 7 || words[3] != "ADD" || words[4] != "COLUMN" {
			return errors.New("ALTER TABLE may only ADD COLUMN in v1, so the previous release still runs on the new schema (DESIGN 10.5)")
		}
		if hasPair(words, "NOT", "NULL") && !slices.Contains(words, "DEFAULT") {
			return errors.New("a column added as NOT NULL needs a DEFAULT, or the previous release's inserts fail")
		}
		return nil
	default:
		first := strings.Join(words[:min(2, len(words))], " ")
		return fmt.Errorf("%q isn't additive: v1 migrations only create tables and indexes, add columns and insert rows (DESIGN 10.5)", first)
	}
}

// hasPair reports whether a is immediately followed by b somewhere in words.
func hasPair(words []string, a, b string) bool {
	for i := 0; i+1 < len(words); i++ {
		if words[i] == a && words[i+1] == b {
			return true
		}
	}
	return false
}

// digest is the SHA-256 recorded for a migration's statements.
func digest(stmts []string) string {
	sum := sha256.Sum256([]byte(strings.Join(stmts, "\n;\n")))
	return hex.EncodeToString(sum[:])
}

// Result says what Apply did.
type Result struct {
	// Applied lists the migrations Apply ran, in order.
	Applied []string
	// Unknown lists the migrations the database has and ms doesn't, which
	// a newer release applied. Apply leaves them alone.
	Unknown []string
}

// ErrChanged is what Apply returns, wrapped, when a migration's statements
// differ from the ones the database recorded applying.
var ErrChanged = errors.New("migration changed after it was applied")

// lockKey is the Postgres advisory lock taken around each migration, so two
// pairtimed processes starting at once don't both apply one. On SQLite the
// write connection's BEGIN IMMEDIATE serializes them instead.
const lockKey int64 = 0x7061697274696d65 // "pairtime"

// createTable is the bookkeeping table. Apply creates it before the first
// migration.
const createTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
id TEXT PRIMARY KEY,
sha256 TEXT NOT NULL,
applied_at_ms BIGINT NOT NULL
)`

// Apply applies every migration of ms the database lacks, in order, each in
// its own transaction on the write connection. now stamps applied_at_ms.
func Apply(ctx context.Context, db *store.DB, ms []Migration, now func() time.Time) (Result, error) {
	var res Result
	if err := ensureTable(ctx, db); err != nil {
		return res, err
	}
	applied, err := appliedSums(ctx, db.Write)
	if err != nil {
		return res, err
	}
	known := make(map[string]bool, len(ms))
	for _, m := range ms {
		known[m.ID] = true
		if sum, ok := applied[m.ID]; ok {
			if sum != m.SHA256 {
				return res, fmt.Errorf("migrations: %s: %w; restore its statements and put the change in a new migration", m.ID, ErrChanged)
			}
			continue
		}
		did, err := applyOne(ctx, db, m, now)
		if err != nil {
			return res, fmt.Errorf("migrations: %s: %w", m.ID, err)
		}
		if did {
			res.Applied = append(res.Applied, m.ID)
		}
	}
	for id := range applied {
		if !known[id] {
			res.Unknown = append(res.Unknown, id)
		}
	}
	sort.Strings(res.Unknown)
	return res, nil
}

func ensureTable(ctx context.Context, db *store.DB) error {
	tx, err := db.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	defer tx.Rollback()
	if err := lock(ctx, tx, db.Engine); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, createTable); err != nil {
		return fmt.Errorf("migrations: creating schema_migrations: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	return nil
}

// applyOne applies m unless another process got there first, and reports
// whether it did.
func applyOne(ctx context.Context, db *store.DB, m Migration, now func() time.Time) (bool, error) {
	tx, err := db.Write.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err := lock(ctx, tx, db.Engine); err != nil {
		return false, err
	}
	var sum string
	err = tx.QueryRowContext(ctx, `SELECT sha256 FROM schema_migrations WHERE id = $1`, m.ID).Scan(&sum)
	switch {
	case err == nil:
		if sum != m.SHA256 {
			return false, ErrChanged
		}
		return false, nil
	case !errors.Is(err, sql.ErrNoRows):
		return false, err
	}
	for i, s := range m.Statements {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return false, fmt.Errorf("statement %d: %w", i+1, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (id, sha256, applied_at_ms) VALUES ($1, $2, $3)`,
		m.ID, m.SHA256, now().UnixMilli()); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func lock(ctx context.Context, tx *sql.Tx, engine store.Engine) error {
	if engine != store.Postgres {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, lockKey); err != nil {
		return fmt.Errorf("migrations: taking the migration lock: %w", err)
	}
	return nil
}

func appliedSums(ctx context.Context, q *sql.DB) (map[string]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, sha256 FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("migrations: %w", err)
	}
	defer rows.Close()
	sums := make(map[string]string)
	for rows.Next() {
		var id, sum string
		if err := rows.Scan(&id, &sum); err != nil {
			return nil, fmt.Errorf("migrations: %w", err)
		}
		sums[id] = sum
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("migrations: %w", err)
	}
	return sums, nil
}

// State is one migration's status.
type State struct {
	ID string
	// AppliedAt is when the migration was applied, or zero while it is
	// pending.
	AppliedAt time.Time
	// Known is false for a migration only the database has.
	Known bool
}

// Status lists every migration of ms and every migration the database has,
// applied or not, in id order. It only reads.
func Status(ctx context.Context, db *store.DB, ms []Migration) ([]State, error) {
	exists, err := tableExists(ctx, db)
	if err != nil {
		return nil, err
	}
	appliedAt := make(map[string]time.Time)
	if exists {
		rows, err := db.Read.QueryContext(ctx, `SELECT id, applied_at_ms FROM schema_migrations`)
		if err != nil {
			return nil, fmt.Errorf("migrations: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var atMs int64
			if err := rows.Scan(&id, &atMs); err != nil {
				return nil, fmt.Errorf("migrations: %w", err)
			}
			appliedAt[id] = time.UnixMilli(atMs).UTC()
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("migrations: %w", err)
		}
	}
	var out []State
	for _, m := range ms {
		out = append(out, State{ID: m.ID, AppliedAt: appliedAt[m.ID], Known: true})
		delete(appliedAt, m.ID)
	}
	for id, at := range appliedAt {
		out = append(out, State{ID: id, AppliedAt: at})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// tableExists reports whether schema_migrations exists yet.
func tableExists(ctx context.Context, db *store.DB) (bool, error) {
	var q string
	switch db.Engine {
	case store.Postgres:
		q = `SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = 'schema_migrations'`
	default:
		q = `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`
	}
	var n int
	if err := db.Read.QueryRowContext(ctx, q).Scan(&n); err != nil {
		return false, fmt.Errorf("migrations: %w", err)
	}
	return n > 0, nil
}
