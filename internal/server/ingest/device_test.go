// SPDX-License-Identifier: AGPL-3.0-or-later

package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/device"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/writer"
	"github.com/some-du6e/pairtime/pkg/ledger"
)

// TestLocalGap checks D-local-gap: only a sign-in resets what the chain
// expects next. The first batch stored under a new sign-in may skip the
// batches the device sealed in local-only mode, which become a gap the
// server records and never takes (ingest-v1 2.7 step 5, 3, 5.3).
func TestLocalGap(t *testing.T) {
	engines(t, func(t *testing.T, engine store.Engine) {
		h := newHarness(t, engine)
		dev := h.device("d_test", false)
		// The device's own chain, batches 1 to 12. It never sends 4 and 5,
		// nor 9, which it sealed in local-only mode.
		roots := chain(rand.New(rand.NewPCG(21, 22)), ledger.Hash{}, 1, 12)
		at := func(b uint64) root { return roots[b-1] }
		one := func(t *testing.T, x root) batchResult {
			t.Helper()
			return h.ok(t, upload{dev: dev, body: batchesBody(x)}).Batches[0]
		}
		want := func(t *testing.T, got batchResult, code string) {
			t.Helper()
			if got.Code != code {
				t.Errorf("got %d %s, want %s", got.Status, got.Code, code)
			}
		}
		h.ok(t, upload{dev: dev, body: batchesBody(roots[:3]...)})
		var oldToken string
		tests := []struct {
			name  string
			check func(t *testing.T)
		}{
			{"without a sign-in, a skipped batch is chain_gap", func(t *testing.T) {
				want(t, one(t, at(6)), CodeChainGap)
				if st := h.state(t, dev); st.Relinked || st.Head == nil || st.Head.B != 3 {
					t.Errorf("state %+v", st)
				}
			}},
			{"after a sign-in the state says relinked, and the head stays", func(t *testing.T) {
				oldToken = dev.token
				h.relink(t, dev)
				if st := h.state(t, dev); !st.Relinked || st.Head == nil || st.Head.B != 3 || len(st.Gaps) != 0 {
					t.Errorf("state %+v", st)
				}
			}},
			{"the token from before the sign-in names no device", func(t *testing.T) {
				rec := h.send(t, upload{dev: dev, edit: func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+oldToken) }})
				if s, c := refusalOf(t, rec); s != 401 || c != CodeTokenInvalid {
					t.Errorf("got %d %s", s, c)
				}
			}},
			{"the first batch under the new sign-in skips ahead with its prev as given, and the skipped batches are a gap", func(t *testing.T) {
				got := one(t, at(6))
				want(t, got, CodeAccepted)
				if err := verifyReceipt(h, dev.ID, 6, at(6).head.String(), got.Receipt); err != nil {
					t.Error(err)
				}
				st := h.state(t, dev)
				if st.Relinked || st.Head == nil || st.Head.B != 6 || st.ChainStart == nil || *st.ChainStart != 1 ||
					!slices.Equal(st.Gaps, []device.Gap{{First: 4, Last: 5}}) {
					t.Errorf("state %+v", st)
				}
			}},
			{"a batch in the gap is before_chain, for good", func(t *testing.T) {
				want(t, one(t, at(4)), CodeBeforeChain)
				want(t, one(t, at(5)), CodeBeforeChain)
			}},
			{"from there the chain links as ever: a skip is chain_gap again", func(t *testing.T) {
				want(t, one(t, at(8)), CodeChainGap)
				want(t, one(t, at(7)), CodeAccepted)
			}},
			{"after another sign-in, the batch right after the head still needs its prev, and records no gap", func(t *testing.T) {
				h.relink(t, dev)
				x := at(8)
				x.prev = ledger.Hash{3}
				want(t, one(t, x.relink()), CodePrevMismatch)
				want(t, one(t, at(8)), CodeAccepted)
				if st := h.state(t, dev); st.Relinked || len(st.Gaps) != 1 {
					t.Errorf("state %+v", st)
				}
			}},
			{"a sign-in, then a skip and the batch after it in one request, records one more gap", func(t *testing.T) {
				h.relink(t, dev)
				resp := h.ok(t, upload{dev: dev, body: batchesBody(at(10), at(11))})
				for i, got := range resp.Batches {
					if got.Code != CodeAccepted {
						t.Errorf("batch %d: %d %s", at(10).b+uint64(i), got.Status, got.Code)
					}
				}
				st := h.state(t, dev)
				if st.Relinked || st.Head == nil || st.Head.B != 11 || !slices.Equal(st.Gaps, []device.Gap{{First: 4, Last: 5}, {First: 9, Last: 9}}) {
					t.Errorf("state %+v", st)
				}
			}},
			{"batches stored before the gaps are still duplicates with their receipts", func(t *testing.T) {
				got := one(t, at(1))
				want(t, got, CodeDuplicate)
				if err := verifyReceipt(h, dev.ID, 1, at(1).head.String(), got.Receipt); err != nil {
					t.Error(err)
				}
			}},
		}
		for _, tt := range tests {
			t.Run(tt.name, tt.check)
		}
	})
}

// TestAnotherAccount checks D-device-account-binding: when the server holds
// rows of a device_id under another account than the token's, the
// device's uploads and state are refused with 403 device_mismatch, and no
// row of the first account changes. Each case keeps the first account's
// rows in one table only, so every table is checked on its own.
func TestAnotherAccount(t *testing.T) {
	engines(t, func(t *testing.T, engine store.Engine) {
		h := newHarness(t, engine)
		minute := exampleClock.Truncate(time.Minute)
		first := chain(rand.New(rand.NewPCG(31, 32)), ledger.Hash{}, 1, 1)[0]
		// fill uploads one item of each kind from a.
		fill := func(t *testing.T, a *testDevice) {
			t.Helper()
			resp := h.ok(t, upload{dev: a, body: map[string]any{
				"batches": []any{first.json()},
				"buckets": []any{json.RawMessage(shareableJSON(t, sampleBucket(a.ID, minute, 1)))},
				"commits": []any{sampleCommit()},
				"labels":  sampleLabels(),
			}})
			if resp.Batches[0].Code != CodeAccepted || resp.Buckets[0].Code != CodeAccepted || resp.Commits[0].Code != CodeAccepted || resp.Labels == nil || resp.Labels.Code != CodeAccepted {
				t.Fatalf("filling: %+v", resp)
			}
		}
		// takeOver deletes a's devices row, as retention deletes an expired
		// tombstone, and links the same device_id to a new account.
		takeOver := func(t *testing.T, a *testDevice) *testDevice {
			t.Helper()
			h.exec(t, `DELETE FROM devices WHERE device_id = $1`, a.ID)
			return h.link(t, a.ID, seedKey("another install of "+a.ID))
		}
		for _, keep := range deviceTables {
			t.Run("rows left only in "+keep+" keep the device_id from another account", func(t *testing.T) {
				id := "d_" + keep
				a := h.link(t, id, seedKey("device "+id))
				fill(t, a)
				for _, table := range deviceTables {
					if table != keep {
						h.exec(t, `DELETE FROM `+table+` WHERE device_id = $1`, id)
					}
				}
				b := takeOver(t, a)
				rec := h.send(t, upload{dev: b, body: map[string]any{
					"batches": []any{first.json()},
					"buckets": []any{json.RawMessage(shareableJSON(t, sampleBucket(id, minute, 2)))},
					"commits": []any{sampleCommit()},
					"labels":  sampleLabels(),
				}})
				if s, c := refusalOf(t, rec); s != 403 || c != CodeDeviceMismatch {
					t.Errorf("upload: got %d %s", s, c)
				}
				if s, c := refusalOf(t, h.serve(t, h.stateRequest(b, b.key))); s != 403 || c != CodeDeviceMismatch {
					t.Errorf("state: got %d %s", s, c)
				}
				var mine, theirs int
				if err := h.db.Read.QueryRow(`SELECT COUNT(*) FROM `+keep+` WHERE device_id = $1 AND account_id = $2`, id, a.Account).Scan(&mine); err != nil {
					t.Fatal(err)
				}
				if err := h.db.Read.QueryRow(`SELECT COUNT(*) FROM `+keep+` WHERE device_id = $1 AND account_id <> $2`, id, a.Account).Scan(&theirs); err != nil {
					t.Fatal(err)
				}
				if mine == 0 || theirs != 0 {
					t.Errorf("%s holds %d rows of the first account and %d of others, want some and none", keep, mine, theirs)
				}
			})
		}
		t.Run("once ingest holds none of the first account's rows, the second account's upload is taken", func(t *testing.T) {
			id := "d_freed"
			a := h.link(t, id, seedKey("device "+id))
			fill(t, a)
			for _, table := range deviceTables {
				h.exec(t, `DELETE FROM `+table+` WHERE device_id = $1`, id)
			}
			b := takeOver(t, a)
			if got := h.ok(t, upload{dev: b, body: batchesBody(first)}).Batches[0]; got.Code != CodeAccepted {
				t.Errorf("got %d %s", got.Status, got.Code)
			}
		})
	})
}

// TestUpsertsKeepTheAccount checks that writing a device's row again never
// changes the account it names, whatever account the write gives
// (D-device-account-binding).
func TestUpsertsKeepTheAccount(t *testing.T) {
	engines(t, func(t *testing.T, engine store.Engine) {
		h := newHarness(t, engine)
		const id, first, second = "d_test", "0190f4b2-0000-7000-8000-00000000aaaa", "0190f4b2-0000-7000-8000-00000000bbbb"
		minute, now := exampleClock.Truncate(time.Minute).UnixMilli(), exampleClock.UnixMilli()
		commit := commitRecord{SHA: strings.Repeat("a", 40), Project: "p_one", Actor: "human"}
		tests := []struct {
			name, table string
			put         func(ctx context.Context, tx writer.Tx, account string, n int64) error
		}{
			{"a minute bucket keeps its account", "minute_buckets", func(ctx context.Context, tx writer.Tx, account string, n int64) error {
				return putBucket(ctx, tx, id, account, minute, n, "final", []byte(`{"rev":`+strconv.FormatInt(n, 10)+`}`), now, n)
			}},
			{"a commit record keeps its account", "commit_records", func(ctx context.Context, tx writer.Tx, account string, n int64) error {
				return putCommit(ctx, tx, id, account, commit, []byte(`{"n":`+strconv.FormatInt(n, 10)+`}`), now)
			}},
			{"labels keep their account", "device_labels", func(ctx context.Context, tx writer.Tx, account string, n int64) error {
				return putLabels(ctx, tx, id, account, []byte(`{"n":`+strconv.FormatInt(n, 10)+`}`), now)
			}},
			{"a reply keeps its account", "ingest_replies", func(ctx context.Context, tx writer.Tx, account string, n int64) error {
				return putReply(ctx, tx, id, account, reply{key: "0190f4b2-5a1c-7000-8000-00000000000" + strconv.FormatInt(n, 10), bodySHA: strings.Repeat("0", 64), response: []byte(`{}`)}, now)
			}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				for n, account := range []string{first, second} {
					if err := h.writer.Do(context.Background(), 2, func(ctx context.Context, tx writer.Tx) error {
						return tt.put(ctx, tx, account, int64(n+1))
					}); err != nil {
						t.Fatal(err)
					}
				}
				var accounts []string
				rows, err := h.db.Read.Query(`SELECT account_id FROM `+tt.table+` WHERE device_id = $1`, id)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				for rows.Next() {
					var a string
					if err := rows.Scan(&a); err != nil {
						t.Fatal(err)
					}
					accounts = append(accounts, a)
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(accounts, []string{first}) {
					t.Errorf("%s rows name %q, want only the first account", tt.table, accounts)
				}
			})
		}
	})
}

// revokingDevices is the device store with LoadState or SaveState answering
// ErrRevoked, as they do for a device removed or linked again between
// Authenticate and ingest's writer transaction.
type revokingDevices struct {
	testDevices
	onLoad, onSave bool
}

func (d revokingDevices) LoadState(ctx context.Context, tx writer.Tx, dev device.Device) (device.State, error) {
	if d.onLoad {
		return device.State{}, device.ErrRevoked
	}
	return d.testDevices.LoadState(ctx, tx, dev)
}

func (d revokingDevices) SaveState(ctx context.Context, tx writer.Tx, dev device.Device, st device.State) error {
	if d.onSave {
		return device.ErrRevoked
	}
	return d.testDevices.SaveState(ctx, tx, dev, st)
}

// TestRevokedInFlight checks that a device removed or linked again while
// its request waits for the writer gets 403 device_revoked, and that
// nothing the request wrote stays (ingest-v1 2.5 step 13).
func TestRevokedInFlight(t *testing.T) {
	engines(t, func(t *testing.T, engine store.Engine) {
		h := newHarness(t, engine)
		root := chain(rand.New(rand.NewPCG(41, 42)), ledger.Hash{}, 1, 1)[0]
		tests := []struct {
			name           string
			onLoad, onSave bool
		}{
			{"a device gone by the time ingest reads its state is device_revoked, its state request too", true, false},
			{"a device gone by the time ingest stores its state is device_revoked, and its batch root doesn't stay", false, true},
		}
		for i, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				dev := h.link(t, fmt.Sprintf("d_gone%d", i), seedKey(fmt.Sprintf("gone %d", i)))
				svc := New(Options{Devices: revokingDevices{h.devices, tt.onLoad, tt.onSave}, Writer: h.writer, Read: h.db.Read, Key: h.server,
					Now: func() time.Time { return h.clock }, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), RateBurst: 1000})
				mux := http.NewServeMux()
				svc.Register(mux)
				reqs := []*http.Request{h.request(t, upload{dev: dev, body: batchesBody(root)})}
				if tt.onLoad {
					reqs = append(reqs, h.stateRequest(dev, dev.key))
				}
				for _, r := range reqs {
					rec := httptest.NewRecorder()
					mux.ServeHTTP(rec, r)
					if s, c := refusalOf(t, rec); s != 403 || c != CodeDeviceRevoked {
						t.Errorf("%s %s: got %d %s, want 403 %s", r.Method, r.URL.Path, s, c, CodeDeviceRevoked)
					}
				}
				for _, table := range deviceTables {
					var n int
					if err := h.db.Read.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE device_id = $1`, dev.ID).Scan(&n); err != nil {
						t.Fatal(err)
					}
					if n != 0 {
						t.Errorf("%s holds %d rows of the refused request", table, n)
					}
				}
			})
		}
	})
}
