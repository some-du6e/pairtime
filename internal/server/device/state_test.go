// SPDX-License-Identifier: AGPL-3.0-or-later

package device_test

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/device"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
	"github.com/some-du6e/pairtime/pkg/ledger"
)

// load reads d's ingest state in a writer transaction, as ingest does.
func (h *harness) load(d device.Device) (device.State, error) {
	var st device.State
	err := h.w.Do(context.Background(), 1, func(ctx context.Context, tx writer.Tx) error {
		var err error
		st, err = h.store.LoadState(ctx, tx, d)
		return err
	})
	return st, err
}

// save stores st as d's ingest state in a writer transaction.
func (h *harness) save(d device.Device, st device.State) error {
	return h.w.Do(context.Background(), 1, func(ctx context.Context, tx writer.Tx) error {
		return h.store.SaveState(ctx, tx, d, st)
	})
}

// TestState is ingest's state on the device row: what LoadState and
// SaveState keep, and the devices they refuse (D-device-row-chain-state,
// D-local-gap).
func TestState(t *testing.T) {
	at := time.UnixMilli(1_790_000_000_000)
	head := ledger.Hash{0xab, 0xcd}
	for _, engine := range storetest.Engines() {
		t.Run(string(engine), func(t *testing.T) {
			tests := []struct {
				name string
				run  func(t *testing.T, h *harness, d device.Device, tok string)
			}{
				{name: "a device ingest has taken nothing from has an empty state at link generation 1", run: func(t *testing.T, h *harness, d device.Device, _ string) {
					st, err := h.load(d)
					if err != nil || !reflect.DeepEqual(st, device.State{Generation: 1}) {
						t.Errorf("LoadState = %+v, %v; want only link generation 1", st, err)
					}
				}},
				{name: "a saved state reads back field for field, its gaps included", run: func(t *testing.T, h *harness, d device.Device, _ string) {
					want := device.State{Generation: 1, Req: 42, HeadB: 30, HeadHash: head, HeadGeneration: 1, HeadReceivedAt: at, ChainStart: 3,
						Gaps: []device.Gap{{First: 5, Last: 9}, {First: 12, Last: 12}}}
					if err := h.save(d, want); err != nil {
						t.Fatal(err)
					}
					got, err := h.load(d)
					if err != nil || got.Req != want.Req || got.HeadB != want.HeadB || got.HeadHash != want.HeadHash || got.HeadGeneration != want.HeadGeneration ||
						!got.HeadReceivedAt.Equal(want.HeadReceivedAt) || got.ChainStart != want.ChainStart || !reflect.DeepEqual(got.Gaps, want.Gaps) || got.Generation != 1 {
						t.Errorf("LoadState = %+v, %v; want %+v", got, err, want)
					}
				}},
				{name: "a state with a counter but no head keeps the head empty", run: func(t *testing.T, h *harness, d device.Device, _ string) {
					if err := h.save(d, device.State{Req: 7}); err != nil {
						t.Fatal(err)
					}
					if got, err := h.load(d); err != nil || !reflect.DeepEqual(got, device.State{Generation: 1, Req: 7}) {
						t.Errorf("LoadState = %+v, %v", got, err)
					}
				}},
				{name: "a device removed since it was authenticated is refused with ErrRevoked, and keeps the state it had", run: func(t *testing.T, h *harness, d device.Device, tok string) {
					first := device.State{Req: 1, HeadB: 1, HeadHash: head, HeadGeneration: 1, HeadReceivedAt: at, ChainStart: 1}
					if err := h.save(d, first); err != nil {
						t.Fatal(err)
					}
					if r := h.call(http.MethodDelete, device.MePath, tok, nil); r.status != http.StatusNoContent {
						t.Fatalf("DELETE answered %d", r.status)
					}
					if _, err := h.load(d); !errors.Is(err, device.ErrRevoked) {
						t.Errorf("LoadState = %v, want ErrRevoked", err)
					}
					next := first
					next.Req, next.HeadB = 2, 2
					if err := h.save(d, next); !errors.Is(err, device.ErrRevoked) {
						t.Errorf("SaveState = %v, want ErrRevoked", err)
					}
					if n := h.count(`SELECT count(*) FROM devices WHERE ingest_req = 1 AND ingest_head_b = 1`); n != 1 {
						t.Errorf("the tombstone has %d rows with the first state, want 1", n)
					}
				}},
				{name: "a device linked again since it was authenticated is refused with ErrRevoked", run: func(t *testing.T, h *harness, d device.Device, _ string) {
					b := h.signIn(verified("ident!teen1", "U0TEEN1"))
					h.link(b, testDeviceID)
					if _, err := h.load(d); !errors.Is(err, device.ErrRevoked) {
						t.Errorf("LoadState with the old link = %v, want ErrRevoked", err)
					}
					if err := h.save(d, device.State{Req: 1}); !errors.Is(err, device.ErrRevoked) {
						t.Errorf("SaveState with the old link = %v, want ErrRevoked", err)
					}
				}},
				{name: "a link again raises the generation and keeps the state, so ingest can tell a new link from a gap", run: func(t *testing.T, h *harness, d device.Device, _ string) {
					first := device.State{Req: 4, HeadB: 9, HeadHash: head, HeadGeneration: 1, HeadReceivedAt: at, ChainStart: 1}
					if err := h.save(d, first); err != nil {
						t.Fatal(err)
					}
					b := h.signIn(verified("ident!teen1", "U0TEEN1"))
					again, err := h.store.Authenticate(context.Background(), h.link(b, testDeviceID).AccessToken)
					if err != nil || again.Generation != 2 {
						t.Fatalf("Authenticate = %+v, %v; want link generation 2", again, err)
					}
					got, err := h.load(again)
					if err != nil || got.Generation != 2 || got.HeadGeneration != 1 || got.HeadB != 9 || got.Req != 4 {
						t.Errorf("LoadState = %+v, %v; want the old head under generation 1 of 2", got, err)
					}
					// Ingest takes batch 15 under the new link, prev as given, and
					// records 10 to 14 as kept local.
					got.Req, got.HeadB, got.HeadGeneration, got.Gaps = 5, 15, 2, []device.Gap{{First: 10, Last: 14}}
					if err := h.save(again, got); err != nil {
						t.Errorf("SaveState after the new link = %v", err)
					}
				}},
				{name: "a state that can't be right is refused, and nothing is stored", run: func(t *testing.T, h *harness, d device.Device, _ string) {
					good := device.State{Req: 3, HeadB: 10, HeadHash: head, HeadGeneration: 1, HeadReceivedAt: at, ChainStart: 2, Gaps: []device.Gap{{First: 4, Last: 6}}}
					bad := []struct {
						name string
						edit func(st *device.State)
					}{
						{name: "a negative counter", edit: func(st *device.State) { st.Req = -1 }},
						{name: "a chain start above the head", edit: func(st *device.State) { st.ChainStart = 11 }},
						{name: "a head without a chain start", edit: func(st *device.State) { st.ChainStart = 0 }},
						{name: "a chain without a head", edit: func(st *device.State) { st.HeadB = 0 }},
						{name: "a head under a link generation the device hasn't had", edit: func(st *device.State) { st.HeadGeneration = 2 }},
						{name: "a head under no link generation", edit: func(st *device.State) { st.HeadGeneration = 0 }},
						{name: "a head without its arrival time", edit: func(st *device.State) { st.HeadReceivedAt = time.Time{} }},
						{name: "a gap reaching the head", edit: func(st *device.State) { st.Gaps = []device.Gap{{First: 4, Last: 10}} }},
						{name: "a gap at the chain's start", edit: func(st *device.State) { st.Gaps = []device.Gap{{First: 2, Last: 3}} }},
						{name: "a gap that ends before it starts", edit: func(st *device.State) { st.Gaps = []device.Gap{{First: 6, Last: 4}} }},
						{name: "gaps out of order", edit: func(st *device.State) { st.Gaps = []device.Gap{{First: 7, Last: 8}, {First: 4, Last: 5}} }},
						{name: "a batch number the database can't hold", edit: func(st *device.State) { st.HeadB = 1 << 63 }},
					}
					for _, c := range bad {
						st := good
						c.edit(&st)
						if err := h.save(d, st); err == nil || !strings.Contains(err.Error(), "ingest state") {
							t.Errorf("SaveState with %s = %v, want a refusal", c.name, err)
						}
					}
					if got, err := h.load(d); err != nil || !reflect.DeepEqual(got, device.State{Generation: 1}) {
						t.Errorf("LoadState after the refusals = %+v, %v; want nothing stored", got, err)
					}
					if err := h.save(d, good); err != nil {
						t.Errorf("SaveState with the good state = %v", err)
					}
				}},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					h := newHarness(t, harnessConfig{engine: engine})
					b := h.signIn(verified("ident!teen1", "U0TEEN1"))
					tok := h.link(b, testDeviceID).AccessToken
					d, err := h.store.Authenticate(context.Background(), tok)
					if err != nil {
						t.Fatal(err)
					}
					tt.run(t, h, d, tok)
				})
			}
		})
	}
}
