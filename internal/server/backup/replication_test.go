// SPDX-License-Identifier: AGPL-3.0-or-later

package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/objstore"
	"github.com/some-du6e/pairtime/internal/server/objstore/objstoretest"
)

const mainBucket = "pairtime-data"

// ltx is an LTX file's name for the transaction range lo to hi.
func ltx(lo, hi uint64) string { return fmt.Sprintf("%016x-%016x.ltx", lo, hi) }

func TestReplicationLag(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	ago := func(s int) time.Time { return now.Add(-time.Duration(s) * time.Second) }
	type local struct {
		name     string
		modified time.Time
	}
	tests := []struct {
		name string
		// noState leaves out Litestream's directory altogether.
		noState bool
		local   []local
		replica []string
		want    time.Duration
		wantErr string
		// wrote is when serve last told Replication of a commit; zero for
		// none.
		wrote time.Time
		// wantCheck is what Check says, "" for a pass.
		wantCheck string
	}{
		{name: "a replica that holds every local file is caught up",
			local:   []local{{ltx(1, 1), ago(100)}, {ltx(2, 2), ago(50)}},
			replica: []string{ltx(1, 1), ltx(2, 2)}, want: 0},
		{name: "the oldest local file the replica lacks sets the lag",
			local:   []local{{ltx(1, 1), ago(100)}, {ltx(2, 2), ago(50)}, {ltx(3, 3), ago(20)}},
			replica: []string{ltx(1, 1)}, want: 50 * time.Second},
		{name: "a replica ahead of the local files, after Litestream's retention, is caught up",
			local:   []local{{ltx(5, 9), ago(400)}},
			replica: []string{ltx(10, 12)}, want: 0},
		{name: "an empty replica lacks every local file",
			local: []local{{ltx(1, 4), ago(30)}, {ltx(5, 5), ago(10)}}, want: 30 * time.Second},
		{name: "a database whose level-0 files are all gone is caught up",
			local: nil, replica: []string{ltx(1, 1)}, want: 0},
		{name: "files that aren't LTX files are ignored on both sides",
			local:   []local{{"0000000000000003-0000000000000003.ltx.tmp", ago(500)}, {ltx(2, 2), ago(5)}},
			replica: []string{ltx(2, 2), "notes.txt", "00000000000000ff.ltx"}, want: 0},
		{name: "no Litestream state next to the database fails, naming the directory it looked in",
			noState: true, wantErr: ".pairtime.db-litestream"},
		{name: "a replica more than a minute behind fails the readiness check",
			local:   []local{{ltx(1, 1), ago(61)}},
			replica: nil, want: 61 * time.Second, wantCheck: "behind the database"},
		{name: "a commit no local file has followed for over a minute fails, though the files match, since Litestream stopped reading",
			local:   []local{{ltx(1, 1), ago(100)}},
			replica: []string{ltx(1, 1)}, wrote: ago(90), want: 90 * time.Second, wantCheck: "isn't reading"},
		{name: "a commit that a newer local file followed was read",
			local:   []local{{ltx(1, 1), ago(100)}, {ltx(2, 2), ago(10)}},
			replica: []string{ltx(1, 1), ltx(2, 2)}, wrote: ago(30), want: 0},
		{name: "a commit a few seconds old that no file has followed yet is a short wait, not a failure",
			local:   []local{{ltx(1, 1), ago(100)}},
			replica: []string{ltx(1, 1)}, wrote: ago(3), want: 3 * time.Second},
		{name: "a commit with no local files at all waits too",
			local: nil, replica: []string{ltx(1, 1)}, wrote: ago(70), want: 70 * time.Second, wantCheck: "behind the database"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := objstoretest.New(t, mainBucket)
			c, err := objstore.New(objstore.Config{Endpoint: srv.URL, Region: objstoretest.Region, Credentials: objstoretest.Credentials})
			if err != nil {
				t.Fatal(err)
			}
			b, err := c.Bucket(mainBucket)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range tt.replica {
				srv.SetObject(mainBucket, "litestream/0000/"+name, []byte("ltx"))
			}
			// A level-1 file, which never counts: Lag reads level 0 alone.
			srv.SetObject(mainBucket, "litestream/0001/"+ltx(1, 1000), []byte("ltx"))
			dbPath := filepath.Join(t.TempDir(), "pairtime.db")
			r := NewReplication(dbPath, b, "litestream")
			r.Now = func() time.Time { return now }
			if !tt.noState {
				if err := os.MkdirAll(r.LocalDir(), 0o700); err != nil {
					t.Fatal(err)
				}
				for _, f := range tt.local {
					p := filepath.Join(r.LocalDir(), f.name)
					write(t, p, "ltx", 0o644)
					if err := os.Chtimes(p, f.modified, f.modified); err != nil {
						t.Fatal(err)
					}
				}
			}
			if got := r.LastLag(); got != -1 {
				t.Errorf("LastLag before any measure = %v, want -1", got)
			}
			if !tt.wrote.IsZero() {
				r.Wrote(tt.wrote)
				r.Wrote(now) // only the oldest waiting commit counts
			}
			got, err := r.Lag(context.Background())
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want it to say %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("lag = %s, want %s", got, tt.want)
			}
			if r.LastLag() != tt.want.Seconds() {
				t.Errorf("LastLag = %v, want %v", r.LastLag(), tt.want.Seconds())
			}
			err = r.Check(context.Background())
			switch {
			case tt.wantCheck == "" && err != nil:
				t.Errorf("Check: %v", err)
			case tt.wantCheck != "" && (err == nil || !strings.Contains(err.Error(), tt.wantCheck)):
				t.Errorf("Check = %v, want it to say %q", err, tt.wantCheck)
			}
		})
	}
}

func TestReplicationLocalDir(t *testing.T) {
	tests := []struct {
		name   string
		dbPath string
		want   string
	}{
		{name: "Litestream's level-0 directory sits next to the database, under a dot directory named for it",
			dbPath: "/data/pairtime.db", want: "/data/.pairtime.db-litestream/ltx/0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := filepath.ToSlash(NewReplication(tt.dbPath, nil, "litestream").LocalDir()); got != tt.want {
				t.Errorf("LocalDir = %s, want %s", got, tt.want)
			}
		})
	}
}
