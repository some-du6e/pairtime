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
			creds := Credentials{Server: srv.URL, Token: token, DeviceID: w.DeviceID(), Sign: KeySigner(devKey), LinkedAt: rows[0].SealedAt.Add(-time.Minute)}
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
	creds := Credentials{Server: srv.URL, Token: testToken, DeviceID: w.DeviceID(), Sign: KeySigner(devKey), LinkedAt: end.Add(-30 * time.Minute)}
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
	creds := Credentials{Server: srv.URL, Token: testToken, DeviceID: w.DeviceID(), Sign: KeySigner(devKey), LinkedAt: start}
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
	creds := Credentials{Server: srv.URL, Token: testToken, DeviceID: w.DeviceID(), Sign: KeySigner(devKey), LinkedAt: start}
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
		{"they wait in state.json, unchecked, until their key checks them", func() bool {
			var sv saved
			if _, err := st.Get(StateKey, &sv); err != nil {
				return false
			}
			return len(sv.Unchecked) == 3
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

// rig is a signed-in uploader against a fake server, over a small chain
// and a bucket store, for the tests that follow.
type rig struct {
	dirs  home.Dirs
	w     *ledger.Writer
	rows  []ledger.Row
	start time.Time
	f     *fakeIngest
	st    *state.Store
	creds Credentials
	u     *Uploader
}

// newRig makes a chain of n batches a quarter hour apart from start, and
// a bucket store holding the given minutes of start, credited 30 s each.
func newRig(t *testing.T, n int, minutes []int, linkedAfter time.Duration) *rig {
	t.Helper()
	dir := t.TempDir()
	r := &rig{dirs: home.Dirs{Data: dir, Config: dir, Runtime: dir}}
	r.w, r.rows = smallChain(t, r.dirs, n)
	r.start = r.rows[0].SealedAt.Add(-15 * time.Minute)
	bs, err := store.OpenBuckets(r.dirs.Buckets())
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range minutes {
		if _, _, err := bs.Put(testBucket(newestTable(), r.w.DeviceID(), r.start.Add(time.Duration(m)*time.Minute), 30, event.StateFinal)); err != nil {
			t.Fatal(err)
		}
	}
	if err := bs.Close(); err != nil {
		t.Fatal(err)
	}
	devKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{11}, 32))
	r.f = newFakeIngest(r.w.DeviceID(), testToken, devKey.Public().(ed25519.PublicKey))
	srv := httptest.NewServer(r.f)
	t.Cleanup(srv.Close)
	if r.st, _, err = state.Open(r.dirs.StateFile()); err != nil {
		t.Fatal(err)
	}
	r.creds = Credentials{Server: srv.URL, Token: testToken, DeviceID: r.w.DeviceID(), Sign: KeySigner(devKey), LinkedAt: r.start.Add(linkedAfter)}
	r.u, err = New(Options{
		Ledger: r.w, LedgerDir: r.dirs.Ledger(), BucketDir: r.dirs.Buckets(), DataDir: r.dirs.Data, State: r.st,
		Login:  LoginFunc(func() (Credentials, bool) { return r.creds, true }),
		Params: newestTable(), Client: srv.Client(), Draw: func(int64) int64 { return 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.u.Close)
	return r
}

// run starts the uploader, waits until cond holds under the fake's lock,
// and stops it.
func (r *rig) run(t *testing.T, what string, cond func(f *fakeIngest) bool) {
	t.Helper()
	r.u.Start()
	waitFor(t, what, func() bool {
		r.f.mu.Lock()
		defer r.f.mu.Unlock()
		return cond(r.f)
	})
	for range 2 {
		r.u.Wake()
		time.Sleep(100 * time.Millisecond)
	}
	r.u.Close()
}

func TestLocalOnly(t *testing.T) {
	// Six batches, the sign-in between the third and the fourth: batches 1
	// to 3 and the minutes they hold were sealed in local-only mode.
	minutes := make([]int, 90)
	for i := range minutes {
		minutes[i] = i
	}
	r := newRig(t, 6, minutes, 50*time.Minute)
	r.run(t, "the batches after the sign-in", func(f *fakeIngest) bool { return f.head != nil && f.head.b == 6 && len(f.buckets) == 45 })
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	tests := []struct {
		name string
		ok   bool
	}{
		{"the server's chain starts at the first batch sealed after the sign-in", r.f.start == 4},
		{"no root of a batch sealed in local-only mode went", r.f.rows[1].b == 0 && r.f.rows[2].b == 0 && r.f.rows[3].b == 0},
		{"no minute whose records local-only mode sealed went", func() bool {
			for ms := range r.f.buckets {
				if ms < r.start.Add(45*time.Minute).UnixMilli() {
					return false
				}
			}
			return true
		}()},
		{"every minute after the sign-in went", len(r.f.buckets) == 45},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Error("no")
			}
		})
	}
}

func TestRevBump(t *testing.T) {
	r := newRig(t, 3, []int{0}, -time.Minute)
	hold(t, r.f, r.rows, 1)
	for _, row := range r.rows {
		fr := r.f.rows[row.B]
		sig, _ := hex.DecodeString(fr.sig)
		rc := pkgledger.Receipt{ReceivedAt: fr.receivedAt}
		copy(rc.Signature[:], sig)
		if err := r.w.StoreReceipt(row.B, rc); err != nil {
			t.Fatal(err)
		}
	}
	// The server holds rev 5 of the minute, from before the bucket store
	// lost its lines and wrote the minute again as rev 0.
	ms := r.start.UnixMilli()
	old := testBucket(newestTable(), r.w.DeviceID(), r.start, 15, event.StateFinal).MinuteBucket(event.Backfilled).Shareable()
	old.Rev = 5
	r.f.buckets[ms] = old
	r.run(t, "the minute above the server's rev", func(f *fakeIngest) bool { return f.buckets[ms].Rev > 5 })
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	if got := r.f.buckets[ms]; got.Rev != 6 || got.CreditedS != 30 {
		t.Errorf("the server holds rev %d with %v s, want rev 6 with the store's 30 s", got.Rev, got.CreditedS)
	}
}

func TestRestoredLedger(t *testing.T) {
	// The server holds batches 1 to 5, and the device's ledger, restored
	// from an old backup, ends at batch 3 with no receipts.
	r := newRig(t, 3, []int{0, 20, 40}, -time.Minute)
	hold(t, r.f, r.rows, 1)
	prev := r.rows[2].Head
	for b := uint64(4); b <= 5; b++ {
		var root, head pkgledger.Hash
		root[0], head[0] = byte(b), byte(0x80|b)
		row := fakeRow{b: b, n: 1, root: root.String(), head: head.String(), prev: prev.String(), receivedAt: 1, sig: "00", keyID: r.f.keyID}
		r.f.rows[b] = row
		r.f.head = &row
		prev = head
	}
	r.run(t, "every minute", func(f *fakeIngest) bool { return len(f.buckets) == 3 })
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	tests := []struct {
		name string
		ok   bool
	}{
		{"the fork is at the device's next batch, which the server already holds", r.u.Status().Fork == 4},
		{"no root went after the fork", func() bool {
			for _, b := range r.f.bodies {
				if bytes.Contains(b, []byte(`"batches"`)) {
					return false
				}
			}
			return true
		}()},
		{"the minutes still went, backfilled", r.f.witness[event.Backfilled] == 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Error("no")
			}
		})
	}
}

func TestRelink(t *testing.T) {
	ix := indexOf(t, []float64{1, 2, 3, 4}, []float64{-1, -1, -1, -1})
	la := func(m float64) time.Time { return day0.Add(time.Duration(m * float64(time.Minute))) }
	tests := []struct {
		name     string
		st       saved
		linkedAt time.Time
		from     uint64
	}{
		{"a first sign-in starts at the first batch sealed after it", saved{}, la(2.5), 3},
		{"the same sign-in keeps its place", saved{From: 3, LinkedAt: la(2.5).UnixMilli()}, la(2.5), 3},
		{"a re-link moves to the first batch sealed after it", saved{From: 1, LinkedAt: la(0).UnixMilli()}, la(3.5), 4},
		{"a re-link after the last batch starts at the next one", saved{From: 1, LinkedAt: la(0).UnixMilli()}, la(9), 5},
		{"a sign-in with no linked_at keeps the place it has", saved{From: 2}, time.Time{}, 2},
		{"a first sign-in with no linked_at starts at the next batch", saved{}, time.Time{}, 5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := tc.st
			relink(&st, ix, Credentials{LinkedAt: tc.linkedAt})
			if st.From != tc.from {
				t.Errorf("From = %d, want %d", st.From, tc.from)
			}
		})
	}
	gaps := []struct {
		name string
		st   saved
		b    uint64
		want bool
	}{
		{"a batch in a recorded gap is local-only", saved{Gaps: []gap{{4, 5}}, Next: 7, From: 6}, 5, true},
		{"a batch after the gap isn't", saved{Gaps: []gap{{4, 5}}, Next: 7, From: 6}, 6, false},
		{"a batch before the gap, of the link before it, isn't", saved{Gaps: []gap{{4, 5}}, Next: 7, From: 6}, 3, false},
		{"before the first state read, the batches before From are", saved{From: 4}, 2, true},
	}
	for _, tc := range gaps {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.st.localOnly(tc.b); got != tc.want {
				t.Errorf("localOnly(%d) = %v", tc.b, got)
			}
		})
	}
}

// TestRelinkGap is a device that went local-only after batch 3 and
// re-linked before batch 6: batches 4 and 5 were sealed in local-only
// mode. Only batch 6's root goes, and every minute goes but theirs.
func TestRelinkGap(t *testing.T) {
	minutes := make([]int, 90)
	for i := range minutes {
		minutes[i] = i
	}
	r := newRig(t, 6, minutes, 80*time.Minute) // the re-link, between batches 5 and 6
	hold(t, r.f, r.rows[:3], 1)
	for _, row := range r.rows[:3] {
		fr := r.f.rows[row.B]
		sig, _ := hex.DecodeString(fr.sig)
		rc := pkgledger.Receipt{ReceivedAt: fr.receivedAt}
		copy(rc.Signature[:], sig)
		if err := r.w.StoreReceipt(row.B, rc); err != nil {
			t.Fatal(err)
		}
	}
	r.f.relinked = true
	pub := r.f.key.Public().(ed25519.PublicKey)
	if err := r.st.Put(StateKey, saved{
		Chain: r.w.DeviceID(), Server: r.creds.Server, Device: r.w.DeviceID(), Req: 64, Next: 4, From: 1, ChainStart: 1,
		LinkedAt:  r.start.Add(-time.Minute).UnixMilli(), // the first link, before batch 1
		ServerKey: &ServerKey{KeyID: r.f.keyID, PublicKey: hex.EncodeToString(pub)},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.st.Save(); err != nil {
		t.Fatal(err)
	}
	r.run(t, "batch 6 and its minutes", func(f *fakeIngest) bool { return f.head != nil && f.head.b == 6 && len(f.buckets) == 60 })
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	gapFrom, gapTo := r.start.Add(45*time.Minute).UnixMilli(), r.start.Add(75*time.Minute).UnixMilli()
	tests := []struct {
		name string
		ok   bool
	}{
		{"no root of a batch sealed in local-only mode went", r.f.rows[4].b == 0 && r.f.rows[5].b == 0},
		{"the first root after the re-link is the first batch sealed after it", r.f.rows[6].b == 6},
		{"no minute of the local-only gap went", func() bool {
			for ms := range r.f.buckets {
				if ms >= gapFrom && ms < gapTo {
					return false
				}
			}
			return true
		}()},
		{"the minutes of the link before the gap still went", func() bool {
			_, ok := r.f.buckets[r.start.UnixMilli()]
			return ok
		}()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Error("no")
			}
		})
	}
}
