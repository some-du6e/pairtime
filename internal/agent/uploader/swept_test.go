// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/state"
	pkgledger "github.com/some-du6e/pairtime/pkg/ledger"
)

// TestSweptLedger runs the uploads after a retention sweep has dropped the
// oldest batches, which the server's chain_start, and its head, may sit
// below (ledger.Writer.Sweep; WP-LG6).
func TestSweptLedger(t *testing.T) {
	// A day no batch of the rig is in yet, so the sweep keeps only the rows
	// from its floor, without their segments.
	later := time.Now().UTC().AddDate(0, 0, 2).Format(time.DateOnly)
	tests := []struct {
		name  string
		floor uint64 // the first row the sweep keeps
		held  int    // the batches the server holds, from batch 1
		// receipts says the device keeps the receipts of the batches the
		// server holds, and seal that a batch is sealed after the sweep.
		receipts, seal bool
		check          func(t *testing.T, r *rig)
	}{
		{
			name: "the roots after the server's head go, though its chain starts at a batch the ledger dropped",
			// The server holds batches 1 to 3, and the sweep kept the rows
			// of 4 to 6 for their roots.
			floor: 4, held: 3,
			check: func(t *testing.T, r *rig) {
				r.run(t, "the kept roots", func(f *fakeIngest) bool { return f.head != nil && f.head.b == 6 })
				if p := r.doctor(); p != "" {
					t.Errorf("doctor heard %q", p)
				}
			},
		},
		{
			name:  "with the server's head the device's newest kept batch, the next root is the one after it",
			floor: 4, held: 6, receipts: true, seal: true,
			check: func(t *testing.T, r *rig) {
				r.run(t, "the root of batch 7", func(f *fakeIngest) bool { return f.head != nil && f.head.b == 7 })
				r.f.mu.Lock()
				defer r.f.mu.Unlock()
				for _, body := range r.f.bodies {
					var req struct {
						Batches []struct {
							B uint64 `json:"b"`
						} `json:"batches"`
					}
					if err := json.Unmarshal(body, &req); err != nil {
						t.Fatal(err)
					}
					for _, b := range req.Batches {
						if b.B != 7 {
							t.Errorf("batch %d's root went again", b.B)
						}
					}
				}
			},
		},
		{
			name: "after a sweep that dropped every row, the batch sealed next goes, linked to the dropped head",
			// The server holds every batch, so the sweep kept no row.
			floor: 7, held: 6,
			check: func(t *testing.T, r *rig) {
				r.u.Start()
				waitFor(t, "the uploader's state read", func() bool {
					r.u.Wake()
					r.f.mu.Lock()
					defer r.f.mu.Unlock()
					return r.f.gets > 0
				})
				// Sealed after the uploader read the rows, so it comes through
				// Sealed, and its row takes the prev the index knows.
				at := uint64(time.Now().UnixMilli()) / pkgledger.CheckpointMS * pkgledger.CheckpointMS
				if _, err := r.w.Checkpoint("p_test", at, json.RawMessage(`{}`)); err != nil {
					t.Fatal(err)
				}
				b, ok, err := r.w.Seal()
				if err != nil || !ok {
					t.Fatalf("sealing batch 7: %v, %v", ok, err)
				}
				r.u.Sealed(b)
				waitFor(t, "the root of batch 7", func() bool {
					r.u.Wake()
					r.f.mu.Lock()
					defer r.f.mu.Unlock()
					return r.f.head != nil && r.f.head.b == 7
				})
				r.u.Close()
				if p := r.doctor(); p != "" {
					t.Errorf("doctor heard %q", p)
				}
			},
		},
		{
			name: "a root the ledger no longer keeps stops the uploads, and doctor asks for pairtime logout, then pairtime login",
			// The server holds batches 1 to 3, and the sweep dropped 4,
			// whose root the server needs next.
			floor: 5, held: 3,
			check: func(t *testing.T, r *rig) {
				r.u.Start()
				waitFor(t, "doctor's problem", func() bool {
					r.u.Wake()
					return strings.Contains(r.doctor(), "pairtime logout, then pairtime login")
				})
				r.u.Close()
				r.f.mu.Lock()
				defer r.f.mu.Unlock()
				if r.f.head.b != 3 || r.f.posts != 0 {
					t.Errorf("the server's head is %d after %d uploads, want 3 after none", r.f.head.b, r.f.posts)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, 6, nil, 0)
			hold(t, r.f, r.rows[:tt.held], 1)
			if tt.receipts {
				for _, row := range r.rows[:tt.held] {
					f := r.f.rows[row.B]
					sig, err := hex.DecodeString(f.sig)
					if err != nil {
						t.Fatal(err)
					}
					rc := pkgledger.Receipt{ReceivedAt: f.receivedAt}
					copy(rc.Signature[:], sig)
					if err := r.w.StoreReceipt(row.B, rc); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, _, err := r.w.Sweep(later, tt.floor); err != nil {
				t.Fatal(err)
			}
			if tt.seal {
				at := uint64(time.Now().UnixMilli()) / pkgledger.CheckpointMS * pkgledger.CheckpointMS
				if _, err := r.w.Checkpoint("p_test", at, json.RawMessage(`{}`)); err != nil {
					t.Fatal(err)
				}
				if _, _, err := r.w.Seal(); err != nil {
					t.Fatal(err)
				}
			}
			// Where the uploads stood before the sweep: the server's head
			// was batch held, and the chain went on from batch 1 under
			// this sign-in.
			st := saved{Chain: r.w.DeviceID(), Server: r.creds.Server, Device: r.creds.DeviceID, Next: uint64(tt.held) + 1,
				From: 1, LinkedAt: r.creds.LinkedAt.UnixMilli(), Modes: 6, Head: uint64(tt.held), ChainStart: 1}
			if err := r.st.Put(StateKey, st); err != nil {
				t.Fatal(err)
			}
			tt.check(t, r)
		})
	}
}

func TestHeldFrom(t *testing.T) {
	tests := []struct {
		name  string
		older func(k int) bool // the batches an older pairtime sealed, recording no mode
		place func(chain string) *saved
		want  uint64
	}{
		{name: "with no place in state.json, every row stays", place: func(string) *saved { return nil }, want: 1},
		{name: "the rows from the next root's batch stay", place: func(c string) *saved { return &saved{Chain: c, Next: 4, Modes: 6} }, want: 4},
		{name: "the batches before the link's first one, which never go, don't stay", place: func(c string) *saved { return &saved{Chain: c, Next: 2, From: 5, Modes: 6} }, want: 5},
		{name: "a run sealed in local-only mode is passed over", place: func(c string) *saved {
			return &saved{Chain: c, Next: 2, Modes: 6, Gaps: []gap{{From: 2, To: 3}}}
		}, want: 4},
		{name: "batches the place hasn't taken the modes of are passed over when their rows say local-only", older: func(k int) bool { return k <= 2 },
			place: func(string) *saved { return nil }, want: 4},
		{name: "with every batch from the next root on sealed in local-only mode, none stays", place: func(c string) *saved {
			return &saved{Chain: c, Next: 4, Modes: 6, LocalFrom: 3}
		}, want: 0},
		{name: "after a fork, none stays", place: func(c string) *saved { return &saved{Chain: c, Next: 4, Modes: 6, Fork: 3} }, want: 0},
		{name: "after the server's device_mismatch, none stays", place: func(c string) *saved { return &saved{Chain: c, Next: 4, Modes: 6, Mismatch: true} }, want: 0},
		{name: "a place of another chain says nothing, so every row stays", place: func(string) *saved { return &saved{Chain: "d_other", Next: 5, Modes: 6} }, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
			w, _ := smallChainWith(t, dirs, 6, tt.older)
			st, _, err := state.Open(dirs.StateFile())
			if err != nil {
				t.Fatal(err)
			}
			if p := tt.place(w.DeviceID()); p != nil {
				if err := st.Put(StateKey, *p); err != nil {
					t.Fatal(err)
				}
			}
			got, err := HeldFrom(st, dirs.Ledger())
			if err != nil || got != tt.want {
				t.Errorf("HeldFrom = %d, %v; want %d", got, err, tt.want)
			}
		})
	}
}
