// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// MinSQLite is the oldest SQLite pairtimed opens a database with.
// sqlite.org/wal section 11 documents a WAL-reset bug, present in 3.7.0
// through 3.51.2 and fixed in 3.51.3, that can corrupt a database when two
// connections write or checkpoint at the same instant. Litestream is such a
// second connection (SP 7.4). The fix was also backported to 3.44.6 and
// 3.50.7, but WP-SV1 sets a single floor, and the SQLite that
// modernc.org/sqlite bundles is well above it.
var MinSQLite = Version{Major: 3, Minor: 51, Patch: 3}

// busyTimeoutMs is how long a connection waits for a lock another
// connection holds, such as Litestream's checkpoint, before it fails with
// SQLITE_BUSY.
const busyTimeoutMs = 5000

// Version is an SQLite release number, such as 3.53.4.
type Version struct {
	Major, Minor, Patch int
}

// ParseVersion reads the "X.Y.Z" form sqlite_version() returns.
func ParseVersion(s string) (Version, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("store: SQLite version %q isn't of the form X.Y.Z", s)
	}
	var n [3]int
	for i, p := range parts {
		if p == "" || strings.Trim(p, "0123456789") != "" {
			return Version{}, fmt.Errorf("store: SQLite version %q isn't of the form X.Y.Z", s)
		}
		v, err := strconv.Atoi(p)
		if err != nil {
			return Version{}, fmt.Errorf("store: SQLite version %q: %w", s, err)
		}
		n[i] = v
	}
	return Version{Major: n[0], Minor: n[1], Patch: n[2]}, nil
}

// Less reports whether v is an older release than w.
func (v Version) Less(w Version) bool {
	if v.Major != w.Major {
		return v.Major < w.Major
	}
	if v.Minor != w.Minor {
		return v.Minor < w.Minor
	}
	return v.Patch < w.Patch
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// ErrSQLiteTooOld is what Open returns, wrapped, for an SQLite older than
// MinSQLite or one whose version it can't read.
var ErrSQLiteTooOld = errors.New("SQLite is too old")

// ErrNoDatabase is what Open returns, wrapped, when Options.NoCreate is
// set and the SQLite file is missing.
var ErrNoDatabase = errors.New("no database")

// tooOldError explains a refused SQLite version.
type tooOldError struct {
	have   string
	reason string
}

func (e *tooOldError) Error() string {
	return fmt.Sprintf("store: refusing SQLite %q: %s. SQLite before %s has the WAL-reset bug that can corrupt a database Litestream replicates (sqlite.org/wal section 11)",
		e.have, e.reason, MinSQLite)
}

func (e *tooOldError) Unwrap() error { return ErrSQLiteTooOld }

// checkSQLiteVersion refuses any SQLite older than MinSQLite, and any
// version string it can't read, since that can't be shown to be new enough.
func checkSQLiteVersion(have string) error {
	v, err := ParseVersion(have)
	if err != nil {
		return &tooOldError{have: have, reason: "the version can't be read"}
	}
	if v.Less(MinSQLite) {
		return &tooOldError{have: have, reason: "it is older than " + MinSQLite.String()}
	}
	return nil
}

func openSQLite(ctx context.Context, o Options) (*DB, error) {
	if o.Path == "" {
		return nil, errors.New("store: no SQLite database path")
	}
	// modernc.org/sqlite reads everything after the first '?' as options,
	// and a "file:" prefix switches it to URI file names.
	if strings.Contains(o.Path, "?") || strings.HasPrefix(o.Path, "file:") {
		return nil, fmt.Errorf("store: SQLite path %q can't contain '?' or start with \"file:\"", o.Path)
	}

	// The version check comes first, on an in-memory database, so a refused
	// SQLite never opens, creates or converts the database file.
	have := o.SQLiteVersion
	if have == "" {
		v, err := librarySQLiteVersion(ctx)
		if err != nil {
			return nil, err
		}
		have = v
	}
	if err := checkSQLiteVersion(have); err != nil {
		return nil, err
	}

	// Create the file before SQLite does: SQLite gives the -wal and -shm
	// files the database file's mode, so this keeps all three at 0600.
	// With NoCreate, a missing file is an error and nothing is created.
	// The directory is checked first, so a refused Open creates no file.
	flag := os.O_RDWR | os.O_CREATE
	if o.NoCreate {
		flag = os.O_RDWR
	} else if err := os.MkdirAll(filepath.Dir(o.Path), 0o700); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	if err := checkDir(o.Path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(o.Path, flag, 0o600)
	if o.NoCreate && errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("store: %w at %s", ErrNoDatabase, o.Path)
	}
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	if runtime.GOOS != "windows" {
		logger := o.Logger
		if logger == nil {
			logger = slog.Default()
		}
		if err := tighten(o.Path, logger); err != nil {
			return nil, err
		}
	}

	w, err := sql.Open("sqlite", sqliteDSN(o.Path, true))
	if err != nil {
		return nil, fmt.Errorf("store: opening %s: %w", o.Path, err)
	}
	// One connection, never recycled: SQLite allows one writer at a time,
	// and pairtimed has one writer goroutine (SP 7.4).
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxLifetime(0)
	w.SetConnMaxIdleTime(0)
	if err := checkWAL(ctx, w); err != nil {
		w.Close()
		return nil, err
	}

	r, err := sql.Open("sqlite", sqliteDSN(o.Path, false))
	if err != nil {
		w.Close()
		return nil, fmt.Errorf("store: opening %s: %w", o.Path, err)
	}
	n := max(4, runtime.GOMAXPROCS(0))
	r.SetMaxOpenConns(n)
	r.SetMaxIdleConns(n)
	if err := r.PingContext(ctx); err != nil {
		w.Close()
		r.Close()
		return nil, fmt.Errorf("store: opening %s: %w", o.Path, err)
	}
	return &DB{Engine: SQLite, Version: have, Write: w, Read: r}, nil
}

// tighten takes group and other access away from the database file and
// from its -wal and -shm files where they exist, before SQLite opens them.
// A file pairtimed creates is already 0600, but one it didn't keeps its own
// mode: Litestream's restore writes the database with os.Create, which
// gives 0644 under the usual umask, on exactly the recovery path of DESIGN
// 10.1. That doesn't cover all of Litestream's own copies. The LTX files
// its syncs write take the database file's mode, but after a restore its
// first sync downloads the replica's latest LTX file with os.Create, once
// pairtimed has started. pairtimed can't chmod files Litestream writes
// later, so checkDir keeps them private through the directory instead.
func tighten(path string, logger *slog.Logger) error {
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		fi, err := os.Stat(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue // no -wal or -shm yet
		}
		if err != nil {
			return fmt.Errorf("store: %w", err)
		}
		perm := fi.Mode().Perm()
		if perm&0o077 == 0 {
			continue
		}
		if err := os.Chmod(p, perm&^0o077); err != nil {
			return fmt.Errorf("store: %s has mode %04o, so group or others can read it, and pairtimed can't change that: %w. Run chmod 600 %s", p, perm, err, p)
		}
		logger.Warn("tightened a database file that group or others could read, as a restore leaves it",
			"file", p, "was", fmt.Sprintf("%04o", perm), "now", fmt.Sprintf("%04o", perm&^0o077))
	}
	return nil
}

// checkDir refuses a database that users other than pairtimed's could
// reach through its directory. Litestream writes copies of the database's
// pages there, in .<name>-litestream/, some with os.Create's 0644 (see
// tighten), so only the directories above them keep those private: the
// database's directory, or one above it, must shut out group and others.
// checkDir changes no directory, since database.path may point into a
// shared one such as /tmp. A missing directory passes, since it holds no
// database yet. Windows is skipped.
func checkDir(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	abs, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	dir, err := filepath.EvalSymlinks(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	closed, err := closedDir(dir, os.Stat)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	if closed == "" {
		return fmt.Errorf("store: group or others can enter %s, the database's directory, and every directory above it, so they could read the copies of the database that Litestream writes there in a mode of its own. Run chmod 700 %s (DESIGN 10.1)", dir, dir)
	}
	return nil
}

// closedDir returns the first directory, from dir up to the root, that
// neither group nor others can enter, or "" when each one lets them in.
func closedDir(dir string, stat func(string) (fs.FileInfo, error)) (string, error) {
	for {
		fi, err := stat(dir)
		if err != nil {
			return "", err
		}
		if fi.Mode().Perm()&0o011 == 0 {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

// librarySQLiteVersion asks the linked SQLite library for its version.
func librarySQLiteVersion(ctx context.Context) (string, error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return "", fmt.Errorf("store: %w", err)
	}
	defer db.Close()
	var v string
	if err := db.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&v); err != nil {
		return "", fmt.Errorf("store: reading the SQLite version: %w", err)
	}
	return v, nil
}

// sqliteDSN is the modernc.org/sqlite data source name for path. Each
// _pragma runs, in order, on every new connection.
func sqliteDSN(path string, write bool) string {
	q := url.Values{}
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMs))
	if write {
		// WAL lets readers run while the writer commits, and NORMAL syncs
		// only at checkpoints. The cost is that the last transactions can
		// roll back after a power cut; clients re-send them from their
		// queues (DESIGN 10.1; SP 7.4, 9.3). The exception is a write
		// nothing would send again, such as an erasure the privacy hub
		// hears is done: the writer's DoDurable sets synchronous=FULL for
		// its batch and NORMAL again after it (DESIGN 8.9, 10.1).
		q.Add("_pragma", "journal_mode(WAL)")
		q.Add("_pragma", "synchronous(NORMAL)")
		// Deletes overwrite what they remove with zeros, so a row the
		// retention jobs delete leaves no bytes in free space for the WAL,
		// the file or Litestream's replica to carry (DESIGN 4.4, 10.3).
		q.Add("_pragma", "secure_delete(1)")
	}
	// Postgres always enforces foreign keys, so SQLite does too.
	q.Add("_pragma", "foreign_keys(1)")
	if write {
		// Take the write lock when a transaction starts rather than at its
		// first write, so waiting on Litestream goes through busy_timeout
		// instead of failing at once.
		q.Set("_txlock", "immediate")
	} else {
		q.Add("_pragma", "query_only(1)")
	}
	return path + "?" + q.Encode()
}

// checkWAL confirms the settings sqliteDSN asked for took effect.
func checkWAL(ctx context.Context, db *sql.DB) error {
	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return fmt.Errorf("store: reading journal_mode: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("store: SQLite journal_mode is %q, want wal (DESIGN 10.1)", mode)
	}
	var sync int
	if err := db.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&sync); err != nil {
		return fmt.Errorf("store: reading synchronous: %w", err)
	}
	if sync != 1 {
		return fmt.Errorf("store: SQLite synchronous is %d, want 1 (NORMAL; DESIGN 10.1)", sync)
	}
	var secure int
	if err := db.QueryRowContext(ctx, "PRAGMA secure_delete").Scan(&secure); err != nil {
		return fmt.Errorf("store: reading secure_delete: %w", err)
	}
	if secure != 1 {
		return fmt.Errorf("store: SQLite secure_delete is %d, want 1, so deleted rows leave no bytes behind (DESIGN 4.4)", secure)
	}
	return nil
}
