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
	"github.com/some-du6e/pairtime/internal/agent/store"
	"github.com/some-du6e/pairtime/pkg/event"
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
			receivedAt: at, sig: hex.EncodeToString(rc.Signature[:]), keyID: f.keyID}
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

// TestFastClock is a device whose clock runs 10 minutes ahead of the
// server's: its uploads go with the server's time, which the answers
// carry, and its newest minutes wait until the server's clock reaches
// them rather than being refused as future_minute and lost.
func TestFastClock(t *testing.T) {
	dir := t.TempDir()
	dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
	table := newestTable()
	end := time.Now().UTC().Truncate(time.Minute)
	kDev := sha256.Sum256([]byte("uploader fast clock K_dev"))
	keys, err := pkgledger.DeriveKeys(kDev[:])
	if err != nil {
		t.Fatal(err)
	}
	var (
		mu  sync.Mutex
		now = end.Add(-30 * time.Minute)
	)
	w, err := ledger.Open(ledger.Options{
		Dir: dirs.Ledger(), Keys: keys, KeyID: "0123456789abcdef", Params: table,
		Now: func() time.Time { mu.Lock(); defer mu.Unlock(); return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	quarter := uint64(end.Truncate(time.Hour).UnixMilli())
	for _, ago := range []time.Duration{25 * time.Minute, 15 * time.Minute, 8 * time.Minute, 0} {
		mu.Lock()
		now = end.Add(-ago)
		mu.Unlock()
		if _, err := w.Checkpoint("p_test", quarter, json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := w.Seal(); err != nil {
			t.Fatal(err)
		}
	}
	bs, err := store.OpenBuckets(dirs.Buckets())
	if err != nil {
		t.Fatal(err)
	}
	for m := 20; m >= 1; m-- {
		if _, _, err := bs.Put(testBucket(table, w.DeviceID(), end.Add(-time.Duration(m)*time.Minute), 30, event.StateFinal)); err != nil {
			t.Fatal(err)
		}
	}
	if err := bs.Close(); err != nil {
		t.Fatal(err)
	}
	devKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{6}, 32))
	f := newFakeIngest(w.DeviceID(), testToken, devKey.Public().(ed25519.PublicKey))
	f.now = func() time.Time { return time.Now().Add(-10 * time.Minute) }
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	st, _, err := state.Open(dirs.StateFile())
	if err != nil {
		t.Fatal(err)
	}
	creds := Credentials{Server: srv.URL, Token: testToken, DeviceID: w.DeviceID(), Sign: KeySigner(devKey)}
	u, err := New(Options{
		Ledger: w, LedgerDir: dirs.Ledger(), BucketDir: dirs.Buckets(), DataDir: dirs.Data, State: st,
		Login:  LoginFunc(func() (Credentials, bool) { return creds, true }),
		Params: table, Client: srv.Client(), Draw: func(int64) int64 { return 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	u.Start()
	t.Cleanup(u.Close)
	oldest, newest := end.Add(-20*time.Minute).UnixMilli(), end.Add(-time.Minute).UnixMilli()
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"the minutes the server's clock has reached go up", func(t *testing.T) {
			waitFor(t, "the oldest minute", func() bool {
				f.mu.Lock()
				defer f.mu.Unlock()
				_, ok := f.buckets[oldest]
				return ok && len(f.buckets) >= 14
			})
		}},
		{"no minute is refused as future_minute, and the newest wait", func(t *testing.T) {
			for range 2 {
				u.Wake()
				time.Sleep(100 * time.Millisecond)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.futures != 0 {
				t.Errorf("%d buckets were refused as future_minute", f.futures)
			}
			if _, ok := f.buckets[newest]; ok {
				t.Error("the newest minute went before the server's clock reached it")
			}
		}},
		{"the offset the answers measured is kept for privacy show", func(t *testing.T) {
			var sv saved
			if _, err := st.Get(StateKey, &sv); err != nil {
				t.Fatal(err)
			}
			if d := time.Duration(sv.Offset) * time.Millisecond; d > -9*time.Minute || d < -11*time.Minute {
				t.Errorf("offset %v", d)
			}
		}},
	}
	for _, tc := range tests {
		if !t.Run(tc.name, tc.run) {
			break
		}
	}
}

// TestPreviewBuckets holds privacy show to a request that carries minutes:
// from where an agent left off, with every batch root and receipt already
// on both sides, what it prints is the body the uploader sends next, byte
// for byte.
func TestPreviewBuckets(t *testing.T) {
	dir := t.TempDir()
	dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
	table := newestTable()
	w, rows := smallChain(t, dirs, 3)
	start := rows[0].SealedAt.Add(-15 * time.Minute)
	devKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, 32))
	f := newFakeIngest(w.DeviceID(), testToken, devKey.Public().(ed25519.PublicKey))
	hold(t, f, rows, 1)
	for _, r := range rows {
		fr := f.rows[r.B]
		sig, err := hex.DecodeString(fr.sig)
		if err != nil {
			t.Fatal(err)
		}
		rc := pkgledger.Receipt{ReceivedAt: fr.receivedAt}
		copy(rc.Signature[:], sig)
		if err := w.StoreReceipt(r.B, rc); err != nil {
			t.Fatal(err)
		}
	}
	bs, err := store.OpenBuckets(dirs.Buckets())
	if err != nil {
		t.Fatal(err)
	}
	const minutes = 40
	for m := 0; m < minutes; m++ {
		if _, _, err := bs.Put(testBucket(table, w.DeviceID(), start.Add(time.Duration(m)*time.Minute), 30, event.StateFinal)); err != nil {
			t.Fatal(err)
		}
	}
	if err := bs.Close(); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	creds := Credentials{Server: srv.URL, Token: testToken, DeviceID: w.DeviceID(), Sign: KeySigner(devKey)}
	st, _, err := state.Open(dirs.StateFile())
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	pub := f.key.Public().(ed25519.PublicKey)
	for k, v := range map[string]any{
		"agent": map[string]any{"version": "dev", "starts": 2, "started": started.UTC()},
		StateKey: saved{Chain: w.DeviceID(), Server: srv.URL, Device: w.DeviceID(), Req: 64, Next: 4, ChainStart: 1,
			ServerKey: &ServerKey{KeyID: f.keyID, PublicKey: hex.EncodeToString(pub)}},
	} {
		if err := st.Put(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	var shown Preview
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"privacy show's next upload carries the minutes and no root", func(t *testing.T) {
			p, err := Next(PreviewOptions{
				LedgerDir: dirs.Ledger(), BucketDir: dirs.Buckets(), DataDir: dirs.Data, StateFile: dirs.StateFile(),
				Params: table, Credentials: creds,
			})
			if err != nil {
				t.Fatal(err)
			}
			if p.Batches != 0 || p.Buckets != minutes || !p.Labels {
				t.Fatalf("the preview holds %d roots, %d buckets, labels %v", p.Batches, p.Buckets, p.Labels)
			}
			if n := bytes.Count(p.JSON, []byte(`"agent_only":{"p_test":20}`)); n != minutes {
				t.Errorf("%d of %d minutes carry their agent-only time", n, minutes)
			}
			if bytes.Contains(p.JSON, []byte(`"anchors"`)) {
				t.Error("the preview names anchors")
			}
			shown = p
		}},
		{"the request the uploader sends is privacy show's, byte for byte", func(t *testing.T) {
			u, err := New(Options{
				Ledger: w, LedgerDir: dirs.Ledger(), BucketDir: dirs.Buckets(), DataDir: dirs.Data, State: st,
				Login:  LoginFunc(func() (Credentials, bool) { return creds, true }),
				Params: table, Started: started, Client: srv.Client(), Draw: func(int64) int64 { return 0 },
			})
			if err != nil {
				t.Fatal(err)
			}
			u.Start()
			t.Cleanup(u.Close)
			waitFor(t, "the first POST", func() bool { _, bodies, _ := f.snapshot(); return len(bodies) > 0 })
			_, bodies, _ := f.snapshot()
			if !bytes.Equal(bodies[0], shown.JSON) {
				t.Errorf("the request's body differs from privacy show's:\n%s\n%s", bodies[0], shown.JSON)
			}
		}},
	}
	for _, tc := range tests {
		if !t.Run(tc.name, tc.run) {
			break
		}
	}
}

// TestKeyRotation is a server that rotated its receipt key after it took
// the device's batches, while the device lost their receipts: the
// duplicates bring back receipts the old key signed, which the state's key
// can't check. They aren't kept, but the minutes they date still go, and
// the uploader asks for the state once for the answer, not once a root.
func TestKeyRotation(t *testing.T) {
	dir := t.TempDir()
	dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
	table := newestTable()
	w, rows := smallChain(t, dirs, 3)
	start := rows[0].SealedAt.Add(-15 * time.Minute)
	devKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{8}, 32))
	f := newFakeIngest(w.DeviceID(), testToken, devKey.Public().(ed25519.PublicKey))
	hold(t, f, rows, 1)
	f.rotate("fake ingest receipt key, rotated")
	bs, err := store.OpenBuckets(dirs.Buckets())
	if err != nil {
		t.Fatal(err)
	}
	const minutes = 40
	for m := 0; m < minutes; m++ {
		if _, _, err := bs.Put(testBucket(table, w.DeviceID(), start.Add(time.Duration(m)*time.Minute), 30, event.StateFinal)); err != nil {
			t.Fatal(err)
		}
	}
	if err := bs.Close(); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	st, _, err := state.Open(dirs.StateFile())
	if err != nil {
		t.Fatal(err)
	}
	creds := Credentials{Server: srv.URL, Token: testToken, DeviceID: w.DeviceID(), Sign: KeySigner(devKey)}
	var (
		pmu     sync.Mutex
		problem string
	)
	u, err := New(Options{
		Ledger: w, LedgerDir: dirs.Ledger(), BucketDir: dirs.Buckets(), DataDir: dirs.Data, State: st,
		Login:  LoginFunc(func() (Credentials, bool) { return creds, true }),
		Params: table, Client: srv.Client(), Draw: func(int64) int64 { return 0 },
		Problem: func(msg string) { pmu.Lock(); problem = msg; pmu.Unlock() },
	})
	if err != nil {
		t.Fatal(err)
	}
	u.Start()
	t.Cleanup(u.Close)
	waitFor(t, "every minute", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.buckets) == minutes
	})
	u.Close()
	tests := []struct {
		name string
		ok   func() bool
	}{
		{"the uploader read the state at its start and once for the answer the old receipts came in", func() bool {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.gets <= 2
		}},
		{"no receipt the state's key can't check is kept in batches.seg", func() bool {
			_, got, err := ledger.ReadRows(dirs.Ledger(), 1)
			if err != nil {
				return false
			}
			for _, r := range got {
				if r.Receipt != nil {
					return false
				}
			}
			return true
		}},
		{"doctor hears that the receipts don't check out", func() bool {
			pmu.Lock()
			defer pmu.Unlock()
			return strings.Contains(problem, "receipt")
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok() {
				t.Error("no")
			}
		})
	}
}
