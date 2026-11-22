// SPDX-License-Identifier: AGPL-3.0-or-later

package device_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/some-du6e/pairtime/internal/server/device"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// TestProjectNamesFollowTheDevice checks what removing a device and linking
// it again do to the project names its owner turned on (D-owner-names):
// removal marks them with its time, for the retention job to delete them
// 90 days later with the device's other project rows
// (D-project-map-lifetime), and a link again before then keeps them.
func TestProjectNamesFollowTheDevice(t *testing.T) {
	for _, engine := range storetest.Engines() {
		t.Run(string(engine), func(t *testing.T) {
			// named links the test device to a teen's account, has ingest
			// take something from it, so removing it leaves a tombstone, and
			// gives it a name; it returns the browser, the token and the
			// account.
			named := func(t *testing.T, h *harness) (*browser, string, string) {
				b := h.signIn(verified("ident!teen1", "U0TEEN1"))
				tok := h.link(b, testDeviceID).AccessToken
				h.ingest(tok)
				var account string
				if err := h.db.Read.QueryRow(`SELECT user_id FROM devices WHERE device_id = $1`, testDeviceID).Scan(&account); err != nil {
					t.Fatal(err)
				}
				if err := h.w.Do(context.Background(), 1, func(ctx context.Context, tx writer.Tx) error {
					_, err := tx.ExecContext(ctx, `INSERT INTO project_names (device_id, project_key, user_id, label, set_at_ms) VALUES ($1, 'k1', $2, 'chip8', 0)`, testDeviceID, account)
					return err
				}); err != nil {
					t.Fatal(err)
				}
				return b, tok, account
			}
			marked := func(h *harness) int {
				return h.count(`SELECT COUNT(*) FROM project_names WHERE device_id = $1 AND device_removed_at_ms IS NOT NULL`, testDeviceID)
			}
			tests := []struct {
				name string
				run  func(t *testing.T, h *harness)
			}{
				{name: "a linked device's names carry no removal time", run: func(t *testing.T, h *harness) {
					named(t, h)
					if n := marked(h); n != 0 {
						t.Errorf("%d names are marked removed, want none", n)
					}
				}},
				{name: "removing the device marks its names with the removal time, and keeps them for now", run: func(t *testing.T, h *harness) {
					_, tok, _ := named(t, h)
					if r := h.call(http.MethodDelete, device.MePath, tok, nil); r.status != http.StatusNoContent {
						t.Fatalf("DELETE answered %d", r.status)
					}
					if n := marked(h); n != 1 {
						t.Errorf("%d names are marked removed, want 1", n)
					}
					if n := h.count(`SELECT COUNT(*) FROM project_names p JOIN devices d ON d.device_id = p.device_id WHERE p.device_removed_at_ms = d.removed_at_ms`); n != 1 {
						t.Errorf("%d names carry the device's removal time, want 1", n)
					}
				}},
				{name: "linking the device to the same account again clears the mark, so its names stay", run: func(t *testing.T, h *harness) {
					b, tok, _ := named(t, h)
					if r := h.call(http.MethodDelete, device.MePath, tok, nil); r.status != http.StatusNoContent {
						t.Fatalf("DELETE answered %d", r.status)
					}
					h.link(b, testDeviceID)
					if n := marked(h); n != 0 {
						t.Errorf("%d names are still marked removed, want none", n)
					}
					if n := h.count(`SELECT COUNT(*) FROM project_names WHERE device_id = $1`, testDeviceID); n != 1 {
						t.Errorf("%d names, want the one kept", n)
					}
				}},
				{name: "an account whose devices are removed together marks every one of their names", run: func(t *testing.T, h *harness) {
					_, _, account := named(t, h)
					if err := h.w.Do(context.Background(), 1, func(ctx context.Context, tx writer.Tx) error {
						return h.store.RemoveAccount(ctx, tx, account, h.clock.Now())
					}); err != nil {
						t.Fatal(err)
					}
					if n := marked(h); n != 1 {
						t.Errorf("%d names are marked removed, want 1", n)
					}
				}},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) { tt.run(t, newHarness(t, harnessConfig{engine: engine})) })
			}
		})
	}
}
