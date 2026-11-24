// SPDX-License-Identifier: AGPL-3.0-or-later

package privacy_test

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
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
		{name: "the rows of tokens more than 30 s past their exp go when the next jti is taken, and one inside the leeway stays", run: func(t *testing.T, f *fixture) {
			for _, c := range []privacy.Claims{claims("gone", now.Add(-31*time.Second)), claims("kept", now.Add(-29*time.Second))} {
				f.exec(t, `INSERT INTO privacy_token_uses (jti, exp_ms) VALUES ($1, $2)`, c.JTI, c.Expires.UnixMilli())
			}
			if err := privacy.Take(context.Background(), f.w, claims("new", now.Add(5*time.Minute)), now); err != nil {
				t.Fatal(err)
			}
			rows := f.dump(t)["privacy_token_uses"]
			if len(rows) != 2 {
				t.Fatalf("privacy_token_uses holds %v, want kept and new", rows)
			}
			if err := privacy.Take(context.Background(), f.w, claims("kept", now.Add(-29*time.Second)), now); !replayed(err) {
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
	path := filepath.Join(t.TempDir(), "pairtime.db")
	ms, err := migrations.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	start := func(t *testing.T) (*store.DB, *writer.Writer) {
		t.Helper()
		db, err := store.Open(context.Background(), store.Options{Engine: store.SQLite, Path: path})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := migrations.Apply(context.Background(), db, ms, time.Now); err != nil {
			t.Fatal(err)
		}
		return db, writer.Start(db.Write, writer.Options{Window: time.Millisecond})
	}
	tests := []struct {
		name string
		at   time.Time
		want func(error) bool
	}{
		{name: "the first start takes the jti", at: now, want: func(err error) bool { return err == nil }},
		{name: "after a restart, a replay a minute later is refused", at: now.Add(time.Minute), want: replayed},
		{name: "after another restart, a replay just inside the leeway is still refused", at: now.Add(5*time.Minute + 29*time.Second), want: replayed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, w := start(t)
			err := privacy.Take(context.Background(), w, c, tt.at)
			w.Close()
			if cerr := db.Close(); cerr != nil {
				t.Fatal(cerr)
			}
			if !tt.want(err) {
				t.Errorf("Take = %v", err)
			}
		})
	}
}
