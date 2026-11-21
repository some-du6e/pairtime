// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/backup"
)

// serveOnce runs pairtimed serve on configPath until it listens, then
// stops it, and returns its exit code and standard error. A serve that
// refuses to start returns without listening.
func serveOnce(t *testing.T, configPath string) (int, string) {
	t.Helper()
	e, _, stderr := testEnv()
	up := make(chan struct{}, 1)
	e.listening = func(string, string) { up <- struct{}{} }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- run(ctx, []string{"serve", "-config", configPath}, e) }()
	select {
	case <-up:
		cancel()
	case code := <-done:
		return code, stderr.String()
	case <-time.After(60 * time.Second):
		t.Fatalf("serve neither listened nor stopped within a minute; stderr:\n%s", stderr)
	}
	select {
	case code := <-done:
		return code, stderr.String()
	case <-time.After(60 * time.Second):
		t.Fatalf("serve didn't stop within a minute; stderr:\n%s", stderr)
	}
	return 0, ""
}

// TestServeChecksKeys checks that serve ties the database to the keys in
// data_dir (#133): the first start records them, and a later start with
// other keys, as after init on a rebuilt host, refuses to serve.
func TestServeChecksKeys(t *testing.T) {
	makeKeys := func(t *testing.T, dataDir string) *backup.Keys {
		t.Helper()
		keys, err := backup.Generate(backup.KeysDir(dataDir), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return keys
	}
	tests := []struct {
		name string
		// before runs between the first start and the second.
		before   func(t *testing.T, dataDir string)
		wantCode int
		wantErr  string
	}{
		{name: "the same keys serve again"},
		{name: "other keys are refused, naming the backup to restore them from",
			before: func(t *testing.T, dataDir string) {
				if err := os.RemoveAll(backup.KeysDir(dataDir)); err != nil {
					t.Fatal(err)
				}
				makeKeys(t, dataDir)
			},
			wantCode: 1, wantErr: "aren't the ones this database was written with"},
		{name: "the keys gone from data_dir serve on with a warning, since there is nothing to compare",
			before: func(t *testing.T, dataDir string) {
				if err := os.RemoveAll(backup.KeysDir(dataDir)); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "no keys to check against the database"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath, dbPath := writeConfig(t)
			dataDir := filepath.Dir(dbPath)
			keys := makeKeys(t, dataDir)
			if code, stderr := serveOnce(t, configPath); code != 0 {
				t.Fatalf("the first start exited %d; stderr:\n%s", code, stderr)
			}
			db, err := sql.Open("sqlite", dbPath)
			if err != nil {
				t.Fatal(err)
			}
			var id, pepper string
			err = db.QueryRow("SELECT server_key_id, pepper_fingerprint FROM server_keys WHERE id = 1").Scan(&id, &pepper)
			db.Close()
			if err != nil || id != keys.Public().ServerKeyID() || pepper != keys.PepperFingerprint() {
				t.Fatalf("server_keys holds %q, %q (%v); want %s and %s", id, pepper, err, keys.Public().ServerKeyID(), keys.PepperFingerprint())
			}
			if len(pepper) != 16 {
				t.Errorf("the pepper's fingerprint %q isn't 16 hex digits", pepper)
			}
			if tt.before != nil {
				tt.before(t, dataDir)
			}
			code, stderr := serveOnce(t, configPath)
			if code != tt.wantCode {
				t.Fatalf("the second start exited %d, want %d; stderr:\n%s", code, tt.wantCode, stderr)
			}
			if tt.wantErr != "" && !strings.Contains(stderr, tt.wantErr) {
				t.Errorf("stderr doesn't say %q:\n%s", tt.wantErr, stderr)
			}
		})
	}
}
