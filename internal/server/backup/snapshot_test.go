// SPDX-License-Identifier: AGPL-3.0-or-later

package backup

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/some-du6e/pairtime/internal/server/objstore"
	"github.com/some-du6e/pairtime/internal/server/objstore/objstoretest"
)

const snapshotBucket = "pairtime-snapshots"

// newDB makes an SQLite database in WAL mode with rows of blobs that add
// up to about size bytes, and returns its path and the sum of its ids.
func newDB(t *testing.T, size int) (string, int64) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pairtime.db")
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE rows (id BIGINT PRIMARY KEY, blob BYTEA)"); err != nil {
		t.Fatal(err)
	}
	const blob = 64 << 10
	var sum int64
	for id := int64(1); id == 1 || int(id-1)*blob < size; id++ {
		b := make([]byte, blob)
		rand.Read(b)
		if _, err := db.Exec("INSERT INTO rows (id, blob) VALUES ($1, $2)", id, b); err != nil {
			t.Fatal(err)
		}
		sum += id
	}
	return path, sum
}

func TestSnapshot(t *testing.T) {
	karim, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	night := time.Date(2026, 11, 20, 3, 10, 0, 0, time.UTC)
	tests := []struct {
		name   string
		dbSize int
		setup  func(t *testing.T, srv *objstoretest.Server, s *objstore.Snapshots)
		wantIs error
		// wantCalls is how many requests the upload makes: 1 for a single
		// put.
		wantCalls int
	}{
		{name: "a small database is one encrypted put that decrypts to an intact copy",
			dbSize: 256 << 10, wantCalls: 1},
		{name: "a database over one part goes up in parts and decrypts to an intact copy",
			dbSize: objstore.MinPartSize + 1<<20, wantCalls: 4},
		{name: "a snapshot already taken at the same second is kept, and the second fails with ErrExists",
			dbSize: 64 << 10,
			setup: func(t *testing.T, _ *objstoretest.Server, s *objstore.Snapshots) {
				if _, err := s.Put(context.Background(), night, bytes.NewReader([]byte("the first"))); err != nil {
					t.Fatal(err)
				}
			},
			wantIs: objstore.ErrExists},
		{name: "a part that keeps failing aborts the upload and leaves no plaintext copy",
			dbSize: objstore.MinPartSize + 1<<20,
			setup: func(_ *testing.T, srv *objstoretest.Server, _ *objstore.Snapshots) {
				second := func(r *http.Request) bool {
					return r.Method == http.MethodPut && r.URL.Query().Get("partNumber") == "2"
				}
				srv.FailMatching(3, second, 500, "InternalError", false)
			},
			wantIs: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := objstoretest.New(t, snapshotBucket)
			c, err := objstore.New(objstore.Config{Endpoint: srv.URL, Region: objstoretest.Region, Credentials: objstoretest.Credentials})
			if err != nil {
				t.Fatal(err)
			}
			b, err := c.Bucket(snapshotBucket)
			if err != nil {
				t.Fatal(err)
			}
			snaps := objstore.NewSnapshots(b)
			if tt.setup != nil {
				tt.setup(t, srv, snaps)
			}
			dbPath, sum := newDB(t, tt.dbSize)
			dir := SnapshotDir(filepath.Dir(dbPath))
			before := len(srv.Requests())

			_, err = Snapshot(context.Background(), SnapshotOptions{
				DBPath: dbPath, Dir: dir, Recipient: karim.Recipient(), Snapshots: snaps,
				PartSize: objstore.MinPartSize, Now: func() time.Time { return night.Add(400 * time.Millisecond) },
			})
			if left, _ := os.ReadDir(dir); len(left) > 0 {
				t.Errorf("the snapshot left %d files in %s", len(left), dir)
			}
			if open := srv.Uploads(); len(open) > 0 {
				t.Errorf("uploads left open: %q", open)
			}
			if tt.wantCalls == 0 {
				if err == nil {
					t.Fatal("Snapshot succeeded")
				}
				if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
					t.Errorf("error = %v, want %v", err, tt.wantIs)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if n := len(srv.Requests()) - before; n != tt.wantCalls {
				t.Errorf("the upload made %d requests, want %d", n, tt.wantCalls)
			}
			sealed, ok := srv.Object(snapshotBucket, objstore.SnapshotKey(night))
			if !ok {
				t.Fatalf("no snapshot under %s", objstore.SnapshotKey(night))
			}
			checkSnapshot(t, sealed, karim, sum)
		})
	}
}

// checkSnapshot decrypts a snapshot, and checks that it is an intact copy
// of a database newDB made.
func checkSnapshot(t *testing.T, sealed []byte, id age.Identity, wantSum int64) {
	t.Helper()
	plain, err := age.Decrypt(bytes.NewReader(sealed), id)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "restored.db")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(f, plain); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var check string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&check); err != nil || check != "ok" {
		t.Errorf("integrity_check = %q, %v", check, err)
	}
	var sum int64
	if err := db.QueryRow("SELECT SUM(id) FROM rows").Scan(&sum); err != nil || sum != wantSum {
		t.Errorf("the restored rows' ids sum to %d (%v), want %d", sum, err, wantSum)
	}
}

func TestRemoveStaleCopies(t *testing.T) {
	tests := []struct {
		name  string
		files []string
		want  []string
	}{
		{name: "copies a killed snapshot left are removed, and other files kept",
			files: []string{"pairtime-20261120T031000Z.db", "pairtime-20261120T031000Z.db-journal", "notes.txt"},
			want:  []string{"notes.txt"}},
		{name: "an empty directory stays empty", files: nil, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tt.files {
				write(t, filepath.Join(dir, f), "x", 0o600)
			}
			if err := RemoveStaleCopies(dir); err != nil {
				t.Fatal(err)
			}
			var got []string
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				got = append(got, e.Name())
			}
			if len(got) != len(tt.want) || (len(got) > 0 && got[0] != tt.want[0]) {
				t.Errorf("left %q, want %q", got, tt.want)
			}
		})
	}
	if err := RemoveStaleCopies(filepath.Join(t.TempDir(), "missing")); err != nil {
		t.Errorf("a missing directory: %v", err)
	}
}
