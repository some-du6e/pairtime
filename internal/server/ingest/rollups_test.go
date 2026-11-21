// SPDX-License-Identifier: AGPL-3.0-or-later

package ingest

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/rollup"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/pkg/event"
)

// TestRollupsFromUploads runs ingest as pairtimed wires it, with
// rollup.ApplyEvent as its Rollup (WP-SV7): an accepted upload reaches
// rollups_daily in the transaction that stores it, agent_only included,
// and a newer revision of the minute moves the rollups to it.
func TestRollupsFromUploads(t *testing.T) {
	engines(t, func(t *testing.T, engine store.Engine) {
		h := newHarness(t, engine)
		h.svc = New(Options{
			Devices: h.devices, Writer: h.writer, Read: h.db.Read, Key: h.server,
			Now:       func() time.Time { return h.clock },
			Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
			RateBurst: 1000,
			Rollup:    rollup.ApplyEvent,
		})
		h.mux = http.NewServeMux()
		h.svc.Register(h.mux)
		dev := h.device("d_test", false)
		minute := exampleClock.Truncate(time.Minute)
		// The account's days count in UTC, the zone a new account has.
		day := minute.UTC().Format("2006-01-02")

		// rows reads the account's rollups_daily rows of day, by the device
		// key behind each row's project and the row's kind, "" for the
		// agent's time.
		rows := func(t *testing.T) map[[2]string]rollup.Day {
			t.Helper()
			ctx := context.Background()
			days, err := rollup.Read(ctx, h.db.Read, dev.Account, day, day)
			if err != nil {
				t.Fatal(err)
			}
			keys, err := rollup.Projects(ctx, h.db.Read, dev.Account)
			if err != nil {
				t.Fatal(err)
			}
			out := make(map[[2]string]rollup.Day)
			for _, d := range days {
				k := keys[d.ProjectID]
				if len(k) != 1 {
					t.Fatalf("project %s has keys %v, want one", d.ProjectID, k)
				}
				out[[2]string{k[0], d.Kind}] = d
			}
			return out
		}
		upload1 := asMap(t, shareableJSON(t, sampleBucket(dev.ID, minute, 1)))
		upload1["agent_only"] = map[string]any{"p_two": 7}
		first := h.ok(t, upload{dev: dev, body: map[string]any{"buckets": []any{upload1}}})
		got1 := rows(t)

		// The minute's next revision drops its second slot and its agent
		// time.
		rev2 := sampleBucket(dev.ID, minute, 2)
		rev2.Slots[1] = event.BucketSlot{}
		rev2.CreditedS = 12.5
		second := h.ok(t, upload{dev: dev, body: map[string]any{"buckets": []any{asMap(t, shareableJSON(t, rev2))}}})
		got2 := rows(t)

		accepted := func(r response) bool { return len(r.Buckets) == 1 && r.Buckets[0].Code == CodeAccepted }
		tests := []struct {
			name string
			ok   bool
		}{
			{"both revisions are accepted", accepted(first) && accepted(second)},
			{"the upload's credit reaches rollups_daily by project, kind and level, as final",
				got1[[2]string{"p_one", "prompting"}].FinalMS == [3]int64{12500, 0, 0} && got1[[2]string{"p_one", "reading_output"}].FinalMS == [3]int64{0, 15000, 0}},
			{"its agent_only reaches the agent's row of its own project (D-bucket-agent-only)",
				got1[[2]string{"p_two", ""}].AgentOnlyFinalMS == 7000},
			{"a newer revision of the minute moves the rollups to it",
				got2[[2]string{"p_one", "prompting"}].FinalMS == [3]int64{12500, 0, 0} && got2[[2]string{"p_one", "reading_output"}].FinalMS == [3]int64{} &&
					got2[[2]string{"p_two", ""}].AgentOnlyFinalMS == 0},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if !tt.ok {
					t.Errorf("no: responses %+v and %+v, rollups after the first %+v, after the second %+v", first.Buckets, second.Buckets, got1, got2)
				}
			})
		}
	})
}
