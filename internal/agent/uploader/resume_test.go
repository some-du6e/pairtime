// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/state"
	pkgledger "github.com/some-du6e/pairtime/pkg/ledger"
)

// smallChain writes n batches of one checkpoint each to a new ledger in
// dirs, a quarter of an hour apart, and returns the writer and the rows.
func smallChain(t *testing.T, dirs home.Dirs, n int) (*ledger.Writer, []ledger.Row) {
	t.Helper()
	kDev := sha256.Sum256([]byte("uploader resume test K_dev"))
	keys, err := pkgledger.DeriveKeys(kDev[:])
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Hour).Add(-2 * time.Hour)
	var (
		mu  sync.Mutex
		now = start
	)
	w, err := ledger.Open(ledger.Options{
		Dir: dirs.Ledger(), Keys: keys, KeyID: "0123456789abcdef", Params: newestTable(),
		Now: func() time.Time { mu.Lock(); defer mu.Unlock(); return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	for k := 1; k <= n; k++ {
		q := start.Add(time.Duration(k) * 15 * time.Minute)
		mu.Lock()
		now = q
		mu.Unlock()
		if _, err := w.Checkpoint("p_test", uint64(q.UnixMilli()), json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := w.Seal(); err != nil {
			t.Fatal(err)
		}
	}
	_, rows, err := ledger.ReadRows(dirs.Ledger(), 1)
	if err != nil {
		t.Fatal(err)
	}
	return w, rows
}

// hold makes the fake server hold rows as if it had taken them before,
// with their receipts.
func hold(t *testing.T, f *fakeIngest, rows []ledger.Row, start uint64) {
	t.Helper()
	at := uint64(time.Now().Add(-time.Hour).UnixMilli())
	for _, r := range rows {
		rc, err := pkgledger.SignReceipt(f.key, f.device, r.B, r.Head, at)
		if err != nil {
			t.Fatal(err)
		}
		row := fakeRow{b: r.B, n: r.N, root: r.Root.String(), head: r.Head.String(), prev: r.Prev.String(),
			receivedAt: at, sig: hex.EncodeToString(rc.Signature[:])}
		f.rows[r.B] = row
		f.head = &row
	}
	f.start = start
}

func TestResume(t *testing.T) {
	tests := []struct {
		name  string
		token string // the sign-in's token, testToken when ""
		prep  func(t *testing.T, f *fakeIngest, rows []ledger.Row)
		until func(f *fakeIngest, u *Uploader) bool
		check func(t *testing.T, f *fakeIngest, u *Uploader, dirs home.Dirs, problems func() string)
	}{
		{
			name: "a server head the ledger disagrees with stops every batch root",
			prep: func(t *testing.T, f *fakeIngest, rows []ledger.Row) {
				forked := rows[0]
				forked.Head[0] ^= 0xff
				hold(t, f, []ledger.Row{forked}, 1)
			},
			until: func(f *fakeIngest, u *Uploader) bool { return u.Status().Fork != 0 && f.labels > 0 },
			check: func(t *testing.T, f *fakeIngest, u *Uploader, _ home.Dirs, problems func() string) {
				if fork := u.Status().Fork; fork != 1 {
					t.Errorf("fork at %d", fork)
				}
				for _, b := range f.bodies {
					if bytes.Contains(b, []byte(`"batches"`)) {
						t.Errorf("a root went after the fork: %s", b)
					}
				}
				if !strings.Contains(problems(), "batch 1") {
					t.Errorf("doctor wasn't told: %q", problems())
				}
			},
		},
		{
			name:  "batches the server holds without the device's receipts go again, and their duplicates bring the receipts",
			prep:  func(t *testing.T, f *fakeIngest, rows []ledger.Row) { hold(t, f, rows, 1) },
			until: func(f *fakeIngest, u *Uploader) bool { return u.Status().Next == 4 },
			check: func(t *testing.T, f *fakeIngest, u *Uploader, dirs home.Dirs, _ func() string) {
				_, rows, err := ledger.ReadRows(dirs.Ledger(), 1)
				if err != nil {
					t.Fatal(err)
				}
				pub := f.key.Public().(ed25519.PublicKey)
				for _, r := range rows {
					if r.Receipt == nil {
						t.Errorf("batch %d got no receipt back", r.B)
					} else if err := pkgledger.VerifyReceipt(pub, f.device, r.B, r.Head, *r.Receipt); err != nil {
						t.Errorf("batch %d: %v", r.B, err)
					}
				}
			},
		},
		{
			name:  "batches before the server's chain start are never sent",
			prep:  func(t *testing.T, f *fakeIngest, rows []ledger.Row) { hold(t, f, rows[1:], 2) },
			until: func(f *fakeIngest, u *Uploader) bool { return u.Status().Next == 4 },
			check: func(t *testing.T, f *fakeIngest, u *Uploader, _ home.Dirs, _ func() string) {
				for _, b := range f.bodies {
					if bytes.Contains(b, []byte(`{"b":1,`)) {
						t.Errorf("batch 1 went: %s", b)
					}
				}
			},
		},
		{
			name:  "a counter the server holds higher goes on above it",
			prep:  func(t *testing.T, f *fakeIngest, _ []ledger.Row) { f.counter = 500 },
			until: func(f *fakeIngest, u *Uploader) bool { return f.head != nil && f.head.b == 3 },
			check: func(t *testing.T, f *fakeIngest, _ *Uploader, _ home.Dirs, _ func() string) {
				if f.counter <= 500 {
					t.Errorf("counter %d", f.counter)
				}
			},
		},
		{
			name:  "a sign-in the server refuses stops the uploads, and says to sign in again",
			token: "ptd_zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz000000",
			until: func(f *fakeIngest, u *Uploader) bool { return f.gets > 0 },
			check: func(t *testing.T, f *fakeIngest, u *Uploader, _ home.Dirs, problems func() string) {
				if f.posts != 0 || f.gets != 1 {
					t.Errorf("%d POSTs and %d state reads after the refusal", f.posts, f.gets)
				}
				if !strings.Contains(problems(), "pairtime login") {
					t.Errorf("doctor wasn't told: %q", problems())
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
			w, rows := smallChain(t, dirs, 3)
			devKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{5}, 32))
			f := newFakeIngest(w.DeviceID(), testToken, devKey.Public().(ed25519.PublicKey))
			if tc.prep != nil {
				tc.prep(t, f, rows)
			}
			srv := httptest.NewServer(f)
			t.Cleanup(srv.Close)
			st, _, err := state.Open(dirs.StateFile())
			if err != nil {
				t.Fatal(err)
			}
			token := tc.token
			if token == "" {
				token = testToken
			}
			creds := Credentials{Server: srv.URL, Token: token, DeviceID: w.DeviceID(), Sign: KeySigner(devKey)}
			var (
				pmu     sync.Mutex
				problem string
			)
			u, err := New(Options{
				Ledger: w, LedgerDir: dirs.Ledger(), BucketDir: dirs.Buckets(), DataDir: dirs.Data, State: st,
				Login:  LoginFunc(func() (Credentials, bool) { return creds, true }),
				Params: newestTable(), Client: srv.Client(), Draw: func(int64) int64 { return 0 },
				Problem: func(msg string) { pmu.Lock(); problem = msg; pmu.Unlock() },
			})
			if err != nil {
				t.Fatal(err)
			}
			u.Start()
			t.Cleanup(u.Close)
			waitFor(t, "the uploader to settle", func() bool {
				f.mu.Lock()
				defer f.mu.Unlock()
				return tc.until(f, u)
			})
			// Later wakes find nothing more to do, or are kept from doing it.
			for range 2 {
				u.Wake()
				time.Sleep(100 * time.Millisecond)
			}
			u.Close()
			f.mu.Lock()
			defer f.mu.Unlock()
			tc.check(t, f, u, dirs, func() string { pmu.Lock(); defer pmu.Unlock(); return problem })
		})
	}
}

func TestSignedOutQueue(t *testing.T) {
	dir := t.TempDir()
	dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
	w, _ := smallChain(t, dirs, 1)
	st, _, err := state.Open(dirs.StateFile())
	if err != nil {
		t.Fatal(err)
	}
	u, err := New(Options{Ledger: w, LedgerDir: dirs.Ledger(), BucketDir: dirs.Buckets(), DataDir: dirs.Data, State: st, Params: newestTable()})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"sealed batches the loop hasn't taken stay under the cap", func(t *testing.T) {
			for b := uint64(1); b <= 3*sealedQueueMax; b++ {
				u.Sealed(ledger.Batch{Row: pkgledger.BatchRow{B: b}})
			}
			u.mu.Lock()
			n, lost := len(u.sealed), u.lost
			u.mu.Unlock()
			if n > sealedQueueMax || !lost {
				t.Errorf("%d batches queued, lost %v", n, lost)
			}
		}},
		{"a cycle while signed out keeps nothing and sends nothing", func(t *testing.T) {
			if more := u.cycle(); more {
				t.Error("a signed-out cycle has more to send")
			}
			u.mu.Lock()
			n := len(u.sealed)
			u.mu.Unlock()
			if n != 0 || u.loaded {
				t.Errorf("%d batches queued, loaded %v", n, u.loaded)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}
