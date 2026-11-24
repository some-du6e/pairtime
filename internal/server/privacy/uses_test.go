// SPDX-License-Identifier: AGPL-3.0-or-later

package privacy_test

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/migrations"
	"github.com/some-du6e/pairtime/internal/server/privacy"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// replayed reports whether err is the 409 for a jti already taken.
func replayed(err error) bool {
	var e *privacy.Error
	return errors.As(err, &e) && e.Status == http.StatusConflict && e.Code == "replayed"
}

func TestTake(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	claims := func(jti string, exp time.Time) privacy.Claims { return privacy.Claims{JTI: jti, Expires: exp} }
	tests := []struct {
		name string
		run  func(t *testing.T, f *fixture)
	}{
		{name: "a jti is taken once, and a token that reuses it is refused, even when the first use's request failed", run: func(t *testing.T, f *fixture) {
			c := claims("jti-1", now.Add(5*time.Minute))
			if err := privacy.Take(context.Background(), f.w, c, now); err != nil {
				t.Fatal(err)
			}
			if err := privacy.Take(context.Background(), f.w, c, now.Add(time.Minute)); !replayed(err) {
				t.Errorf("the second Take = %v, want 409 replayed", err)
			}
		}},
		{name: "a jti is stored as its SHA-256, so the row holds no request_id or action", run: func(t *testing.T, f *fixture) {
			const jti = "01a1296f-request-pairtime-delete-1-3f9c2a7b1e04"
			if err := privacy.Take(context.Background(), f.w, claims(jti, now.Add(5*time.Minute)), now); err != nil {
				t.Fatal(err)
			}
			rows := f.dump(t)["privacy_token_uses"]
			if len(rows) != 1 || !strings.HasPrefix(rows[0], privacy.JTIKey(jti)+"|") || strings.Contains(rows[0], "request") {
				t.Errorf("privacy_token_uses holds %v, want the jti's SHA-256 and its exp", rows)
			}
		}},
		{name: "the rows of tokens past their exp, the leeway and PruneAfter go when the next jti is taken, and one short of that stays", run: func(t *testing.T, f *fixture) {
			gone := now.Add(-privacy.Leeway - privacy.PruneAfter - time.Second)
			kept := now.Add(-privacy.Leeway - privacy.PruneAfter + time.Second)
			for _, c := range []privacy.Claims{claims("gone", gone), claims("kept", kept)} {
				f.exec(t, `INSERT INTO privacy_token_uses (jti_sha256, exp_ms) VALUES ($1, $2)`, privacy.JTIKey(c.JTI), c.Expires.UnixMilli())
			}
			if err := privacy.Take(context.Background(), f.w, claims("new", now.Add(5*time.Minute)), now); err != nil {
				t.Fatal(err)
			}
			rows := f.dump(t)["privacy_token_uses"]
			if len(rows) != 2 || strings.Contains(strings.Join(rows, "\n"), privacy.JTIKey("gone")) {
				t.Fatalf("privacy_token_uses holds %v, want kept's row and new's", rows)
			}
			if err := privacy.Take(context.Background(), f.w, claims("kept", kept), now); !replayed(err) {
				t.Errorf("taking the kept jti again = %v, want 409 replayed", err)
			}
		}},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) {
				tt.run(t, newFixture(t, engine))
			})
		}
	}
}

// TestTakeAcrossRestart checks a replay inside the token's lifetime is
// refused after pairtimed reopens its database (D-sv13-jti): the jti is in
// the database, not in memory.
func TestTakeAcrossRestart(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	c := privacy.Claims{JTI: "jti-restart", Expires: now.Add(5 * time.Minute)}
	ms, err := migrations.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	// take starts pairtimed on the database at path, takes c's jti at at,
	// and stops it again.
	take := func(t *testing.T, path string, at time.Time) error {
		t.Helper()
		db, err := store.Open(context.Background(), store.Options{Engine: store.SQLite, Path: path})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := migrations.Apply(context.Background(), db, ms, time.Now); err != nil {
			t.Fatal(err)
		}
		w := writer.Start(db.Write, writer.Options{Window: time.Millisecond})
		err = privacy.Take(context.Background(), w, c, at)
		w.Close()
		if cerr := db.Close(); cerr != nil {
			t.Fatal(cerr)
		}
		return err
	}
	tests := []struct {
		name string
		at   time.Time
	}{
		{name: "a replay a minute later, after a restart, is refused", at: now.Add(time.Minute)},
		{name: "a replay at the last moment the leeway allows, after a restart, is refused", at: c.Expires.Add(privacy.Leeway)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "pairtime.db")
			if err := take(t, path, now); err != nil {
				t.Fatalf("the first start's Take = %v", err)
			}
			if err := take(t, path, tt.at); !replayed(err) {
				t.Errorf("Take after the restart = %v, want 409 replayed", err)
			}
		})
	}
}
