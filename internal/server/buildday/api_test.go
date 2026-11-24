// SPDX-License-Identifier: AGPL-3.0-or-later

package buildday

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/device"
	"github.com/some-du6e/pairtime/internal/server/store"
)

// fakeDevices authenticates the tokens it maps, as *device.Store does, and
// fails on ptd_down, as a device store whose database is down does.
type fakeDevices map[string]device.Device

func (f fakeDevices) Authenticate(_ context.Context, token string) (device.Device, error) {
	d, ok := f[token]
	switch {
	case token == "ptd_down":
		return device.Device{}, errors.New("device store: the database is locked")
	case !ok:
		return device.Device{}, device.ErrUnknownToken
	case d.Revoked:
		return d, device.ErrRevoked
	}
	return d, nil
}

// TestAPI checks GET /api/v1/build-days: a device's token reads its
// account's summary, and nothing else gets past the token check
// (docs/spec/teen-layer-v1.md).
func TestAPI(t *testing.T) {
	devices := fakeDevices{
		"ptd_good":    {ID: "d1", Account: "u1"},
		"ptd_removed": {ID: "d2", Account: "u1", Revoked: true},
	}
	tests := []struct {
		name     string
		auth     string
		wantCode int
		wantBody string
		// broken reads through a read pool that fails.
		broken bool
		// retryAfter is the Retry-After the answer must carry.
		retryAfter string
	}{
		{name: "a request without a device token is refused with token_missing", wantCode: http.StatusUnauthorized, wantBody: `"error":"token_missing"`},
		{name: "a token no device has is refused with token_invalid", auth: "Bearer ptd_unknown", wantCode: http.StatusUnauthorized, wantBody: `"error":"token_invalid"`},
		{name: "a removed device's token is refused with token_invalid", auth: "Bearer ptd_removed", wantCode: http.StatusUnauthorized, wantBody: `"error":"token_invalid"`},
		{name: "a device's token reads its account's summary", auth: "Bearer ptd_good", wantCode: http.StatusOK, wantBody: `"build_days":1,"window_days":30`},
		{name: "a device store that fails answers 503 with Retry-After: 5, as ingest does", auth: "Bearer ptd_down", wantCode: http.StatusServiceUnavailable, wantBody: `"error":"unavailable"`, retryAfter: "5"},
		{name: "a read that fails answers 503 with Retry-After: 5, as ingest does", auth: "Bearer ptd_good", broken: true, wantCode: http.StatusServiceUnavailable, wantBody: `"error":"unavailable"`, retryAfter: "5"},
	}
	now := func() time.Time { return utc(2026, 11, 20, 12, 0) }
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := newWorld(t, store.SQLite, "")
	buildOn(w, 2026, 11, 20)
	api := API(w.db.Read, devices, now, "0.6.0", logger)
	down := newWorld(t, store.SQLite, "")
	if err := down.db.Read.Close(); err != nil {
		t.Fatal(err)
	}
	broken := API(down.db.Read, devices, now, "0.6.0", logger)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, SummaryPath, nil)
			if tt.auth != "" {
				r.Header.Set("Authorization", tt.auth)
			}
			rec := httptest.NewRecorder()
			if tt.broken {
				broken(rec, r)
			} else {
				api(rec, r)
			}
			body := rec.Body.String()
			if rec.Code != tt.wantCode || !strings.Contains(body, tt.wantBody) {
				t.Fatalf("GET %s = %d %s, want %d with %s", SummaryPath, rec.Code, body, tt.wantCode, tt.wantBody)
			}
			if got := rec.Header().Get("Retry-After"); got != tt.retryAfter {
				t.Errorf("Retry-After = %q, want %q", got, tt.retryAfter)
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}
			if rec.Code == http.StatusOK {
				var s Summary
				if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil || s.Today != "2026-11-20" || s.Days[len(s.Days)-1].State != BuildDay {
					t.Errorf("the summary %s doesn't read back with today a build day (%v)", body, err)
				}
			}
		})
	}
}
