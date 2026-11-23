// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/backup"
	"github.com/some-du6e/pairtime/internal/server/publisher"
	"github.com/some-du6e/pairtime/pkg/ledger"
)

// TestServeDayRoots checks that serve publishes the day roots at start
// (WP-SV17) once the server key loads and PAIRTIME_WITNESS_REPO names the
// witness repository, writing every due day's file under -witness-dir, and
// that the privacy notice says whether it does.
func TestServeDayRoots(t *testing.T) {
	tests := []struct {
		name string
		// repo sets PAIRTIME_WITNESS_REPO.
		repo bool
	}{
		{name: "with the server key and PAIRTIME_WITNESS_REPO, serve publishes the root of every due day at start, and the notice no longer says it doesn't publish day roots", repo: true},
		{name: "without PAIRTIME_WITNESS_REPO serve writes no day root, and the notice says it doesn't publish them yet"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath, dbPath := writeConfig(t)
			keys, err := backup.Generate(backup.KeysDir(filepath.Dir(dbPath)), rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			createDatabase(t, dbPath)
			e, _, stderr := testEnv()
			if code := run(context.Background(), []string{"migrate", "-config", configPath}, e); code != 0 {
				t.Fatalf("migrate: %s", stderr)
			}
			// A batch that came three days ago, signed by this server's key.
			now := time.Now()
			first := now.Add(-72 * time.Hour)
			db, err := sql.Open("sqlite", dbPath)
			if err != nil {
				t.Fatal(err)
			}
			_, err = db.Exec(`INSERT INTO witness_rows (device_id, b, account_id, n, root, head, prev, received_at_ms, receipt_sig, receipt_key_id) VALUES ('d1', 1, 'u1', 1, $1, $1, $1, $2, $3, $4)`,
				strings.Repeat("ab", 32), first.UnixMilli(), strings.Repeat("01", 64), keys.Public().ServerKeyID())
			db.Close()
			if err != nil {
				t.Fatal(err)
			}

			dir := filepath.Join(t.TempDir(), "witness")
			var environ []string
			if tt.repo {
				environ = append(environ, "PAIRTIME_WITNESS_REPO=some-du6e/pairtime-witness")
			}
			e, _, stderr = testEnv(environ...)
			up := make(chan string, 1)
			e.listening = func(public, _ string) { up <- public }
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan int, 1)
			go func() { done <- run(ctx, []string{"serve", "-config", configPath, "-witness-dir", dir}, e) }()
			t.Cleanup(func() {
				cancel()
				select {
				case code := <-done:
					if code != 0 {
						t.Errorf("serve exited %d once stopped; stderr:\n%s", code, stderr)
					}
				case <-time.After(60 * time.Second):
					t.Error("serve didn't stop within a minute of its context ending")
				}
			})
			var public string
			select {
			case public = <-up:
			case code := <-done:
				t.Fatalf("serve exited %d before listening; stderr:\n%s", code, stderr)
			case <-time.After(60 * time.Second):
				t.Fatalf("serve didn't start within a minute; stderr:\n%s", stderr)
			}

			last, _ := publisher.LastDue(now)
			if tt.repo {
				newest := filepath.Join(dir, filepath.FromSlash(publisher.FileName(last)))
				for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
					if _, err := os.Stat(newest); err == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("serve wrote no %s within 30 s of its start; stderr:\n%s", newest, stderr)
					}
				}
				for d := ledger.DayOf(uint64(first.UnixMilli())); d <= last; d++ {
					if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(publisher.FileName(d)))); err != nil {
						t.Errorf("no file for %s: %v", publisher.Date(d), err)
					}
				}
			}

			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Get("http://" + public + "/privacy")
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || resp.StatusCode != http.StatusOK {
				t.Fatalf("GET /privacy = %d, %v", resp.StatusCode, err)
			}
			if says := strings.Contains(string(body), "publish day roots"); says == tt.repo {
				t.Errorf("the notice says this server doesn't publish day roots yet: %v, with PAIRTIME_WITNESS_REPO set: %v", says, tt.repo)
			}
			if !tt.repo {
				if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("serve wrote to %s without a witness repository: %v", dir, err)
				}
			}
		})
	}
}
