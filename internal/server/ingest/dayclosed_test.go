// SPDX-License-Identifier: AGPL-3.0-or-later

package ingest

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/pkg/ledger"
)

// TestDayClosed checks that ingest stores and receipts nothing while the
// server's clock reads a time at or before the end of the newest day whose
// head the publisher stored, as after the clock was set back across a
// published midnight, and takes the same body and key once the clock is
// past that day's end (ledger-v1 8, decision 38).
func TestDayClosed(t *testing.T) {
	engines(t, func(t *testing.T, engine store.Engine) {
		h := newHarness(t, engine)
		dev := h.device("d_test", false)
		x := chain(rand.New(rand.NewPCG(7, 8)), ledger.Hash{}, 1, 1)[0]
		d := ledger.DayOf(uint64(exampleClock.UnixMilli()))
		hash := strings.Repeat("ab", 32)
		h.exec(t, `INSERT INTO day_heads (day, n, root, prev, head, signature, key_id, written_at_ms) VALUES ($1, 0, $2, $2, $2, $3, $4, $5)`,
			int64(d), hash, strings.Repeat("01", 64), "0123456789abcdef", exampleClock.UnixMilli())
		end := time.UnixMilli(int64(d+1)*ledger.DayMS - 1).UTC()
		// The client sends the same body and key each time, as a retry
		// does; a refusal moves no counter, so the same one goes again.
		key, req := h.newKey(), dev.req+1
		stored := func(t *testing.T) int {
			t.Helper()
			var n int
			if err := h.db.Read.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM witness_rows`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		tests := []struct {
			name      string
			clock     time.Time
			wantRetry string
		}{
			{name: "inside a published day, ingest refuses with day_closed, stores nothing, and asks for at most an hour's wait", clock: exampleClock, wantRetry: "3600"},
			{name: "half a second before the day ends, Retry-After rounds up to one second", clock: end.Add(-500 * time.Millisecond), wantRetry: "1"},
			{name: "in the day's last millisecond, ingest still refuses", clock: end, wantRetry: "1"},
			{name: "a millisecond after the day ends, the same upload is taken, with a receipt on the next day"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				h.clock = tt.clock
				if tt.wantRetry == "" {
					h.clock = end.Add(time.Millisecond)
				}
				rec := h.send(t, upload{dev: dev, body: batchesBody(x), key: key, req: req})
				if tt.wantRetry != "" {
					if status, code := refusalOf(t, rec); status != http.StatusServiceUnavailable || code != CodeDayClosed || rec.Header().Get("Retry-After") != tt.wantRetry {
						t.Errorf("got %d %s with Retry-After %q, want 503 %s with %q", status, code, rec.Header().Get("Retry-After"), CodeDayClosed, tt.wantRetry)
					}
					if n := stored(t); n != 0 {
						t.Errorf("%d witness rows stored on a published day", n)
					}
					return
				}
				if rec.Code != http.StatusOK {
					t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body)
				}
				var resp response
				if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
					t.Fatal(err)
				}
				if len(resp.Batches) != 1 || resp.Batches[0].Code != CodeAccepted || resp.Batches[0].Receipt == nil ||
					ledger.DayOf(uint64(resp.Batches[0].Receipt.ReceivedAt)) != d+1 {
					t.Errorf("the batch got %+v, want it accepted with a receipt on the day after the published one", resp.Batches)
				}
				if n := stored(t); n != 1 {
					t.Errorf("%d witness rows stored, want 1", n)
				}
			})
		}
	})
}
