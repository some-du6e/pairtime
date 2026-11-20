// SPDX-License-Identifier: AGPL-3.0-or-later

package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"
	_ "modernc.org/sqlite" // the "sqlite" driver VacuumInto opens

	"github.com/some-du6e/pairtime/internal/server/objstore"
)

// SnapshotDir is where Snapshot writes the plaintext copy of the database
// while it encrypts and uploads it: inside data_dir, which is closed to
// group and others, never /tmp, which compose mounts as tmpfs.
func SnapshotDir(dataDir string) string { return filepath.Join(dataDir, "snapshot-tmp") }

// SnapshotOptions say what Snapshot copies and where it goes.
type SnapshotOptions struct {
	// DBPath is the SQLite database.
	DBPath string
	// Dir holds the plaintext copy for as long as the upload takes.
	// SnapshotDir gives the usual one. It is created with mode 0700.
	Dir string
	// Recipient is Karim's offline age key (ParseRecipient).
	Recipient age.Recipient
	// Snapshots is the snapshot bucket.
	Snapshots *objstore.Snapshots
	// PartSize is the multipart part size; 0 means objstore's default.
	PartSize int64
	// Now is the clock the snapshot is named by; nil means time.Now.
	Now func() time.Time
}

// Snapshot makes the nightly snapshot (DESIGN 10.3): VACUUM INTO a new
// file, with mode 0600, then that file encrypted with age to o.Recipient
// as it streams to the snapshot bucket under objstore.SnapshotKey, write
// once. The plaintext copy is removed afterwards, whatever happened. It
// is SQLite only. Karim restores one offline:
//
//	age -d -i <identity file> pairtime-<time>.db.age > pairtime.db
func Snapshot(ctx context.Context, o SnapshotOptions) (objstore.ObjectInfo, error) {
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	takenAt := now().UTC().Truncate(time.Second)
	if err := os.MkdirAll(o.Dir, 0o700); err != nil {
		return objstore.ObjectInfo{}, fmt.Errorf("backup: %w", err)
	}
	if err := checkPrivateDir(o.Dir); err != nil {
		return objstore.ObjectInfo{}, err
	}
	copyPath := filepath.Join(o.Dir, "pairtime-"+takenAt.Format(keyBackupTime)+".db")
	// VACUUM INTO writes into an empty file that exists, and keeps its
	// mode, so the copy is never readable by anyone else, not even while
	// it is written.
	if err := writeNew(copyPath, nil); err != nil {
		return objstore.ObjectInfo{}, err
	}
	defer removeCopy(copyPath)
	if err := VacuumInto(ctx, o.DBPath, copyPath); err != nil {
		return objstore.ObjectInfo{}, err
	}
	f, err := os.Open(copyPath)
	if err != nil {
		return objstore.ObjectInfo{}, fmt.Errorf("backup: %w", err)
	}
	defer f.Close()

	pr, pw := io.Pipe()
	encrypted := make(chan error, 1)
	go func() {
		encrypted <- encryptTo(pw, f, o.Recipient)
	}()
	info, err := o.Snapshots.PutStream(ctx, takenAt, pr, o.PartSize)
	// When the upload stops early, this unblocks the encryption.
	pr.CloseWithError(errUploadStopped)
	if eerr := <-encrypted; eerr != nil && !errors.Is(eerr, errUploadStopped) && err == nil {
		err = eerr
	}
	if err != nil {
		return objstore.ObjectInfo{}, err
	}
	return info, nil
}

// errUploadStopped ends the encryption when the upload stopped reading.
var errUploadStopped = errors.New("backup: the upload stopped")

// encryptTo encrypts src to r into pw and closes pw, with the error if
// there was one, so the upload reading the other end sees it.
func encryptTo(pw *io.PipeWriter, src io.Reader, r age.Recipient) error {
	w, err := age.Encrypt(pw, r)
	if err == nil {
		if _, err = io.Copy(w, src); err == nil {
			err = w.Close()
		}
	}
	if err != nil {
		err = fmt.Errorf("backup: encrypting the snapshot: %w", err)
		pw.CloseWithError(err)
		return err
	}
	return pw.Close()
}

// removeCopy removes a snapshot's plaintext copy and whatever SQLite made
// next to it.
func removeCopy(path string) {
	for _, p := range []string{path, path + "-journal", path + "-wal", path + "-shm"} {
		os.Remove(p)
	}
}

// RemoveStaleCopies removes the plaintext copies a snapshot that never
// finished left in dir, as a crash or a kill during the upload would. serve
// calls it at start, before any snapshot runs.
func RemoveStaleCopies(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "pairtime-") && !e.IsDir() {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return fmt.Errorf("backup: %w", err)
			}
		}
	}
	return nil
}

// VacuumInto writes a consistent copy of the SQLite database at dbPath to
// out with VACUUM INTO, on a connection of its own, so the writer goroutine
// keeps committing meanwhile: in WAL mode the copy reads one snapshot of
// the database and blocks no writer. out must not exist, or be empty.
func VacuumInto(ctx context.Context, dbPath, out string) error {
	if strings.ContainsAny(dbPath, "?#") || strings.HasPrefix(dbPath, "file:") {
		return fmt.Errorf("backup: database path %q can't contain '?' or '#' or start with \"file:\"", dbPath)
	}
	if _, err := os.Stat(dbPath); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	db, err := sql.Open("sqlite", dbPath+"?"+q.Encode())
	if err != nil {
		return fmt.Errorf("backup: opening %s: %w", dbPath, err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, "VACUUM INTO $1", out); err != nil {
		return fmt.Errorf("backup: VACUUM INTO: %w", err)
	}
	return nil
}
