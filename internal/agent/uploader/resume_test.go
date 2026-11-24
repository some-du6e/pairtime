// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
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
	return smallChainWith(t, dirs, n, nil)
}

// smallChainWith is smallChain, with the batches older(k) names sealed
// as an older pairtime version sealed them, its rows recording no mode
// (ledger.Row.NoMode), when older isn't nil.
func smallChainWith(t *testing.T, dirs home.Dirs, n int, older func(k int) bool) (*ledger.Writer, []ledger.Row) {
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
	opts := ledger.Options{
		Dir: dirs.Ledger(), Keys: keys, KeyID: "0123456789abcdef", Params: newestTable(),
		Now:    func() time.Time { mu.Lock(); defer mu.Unlock(); return now },
		Online: func() bool { return true },
	}
	w, err := ledger.Open(opts)
	if err != nil {
		t.Fatal(err)
	}
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
	if older != nil {
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		dropModes(t, dirs.Ledger(), func(b uint64) bool { return older(int(b)) })
		if w, err = ledger.Open(opts); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { w.Close() })
	_, rows, err := ledger.ReadRows(dirs.Ledger(), 1)
	if err != nil {
		t.Fatal(err)
	}
	return w, rows
}

// dropModes rewrites the rows of the batches older names in the ledger
// directory dir's batches.seg without the modes their seal frames record,
// as pairtime 0.1.0-alpha.1's writer wrote rows, which recorded none. No
// ledger writer may have the directory open. It writes the frames as
// internal/agent/ledger lays them out (frame.go): u8(kind) || u32(len) ||
// payload || u32(CRC-32C of the rest), big-endian, a row's kind being 'b'.
func dropModes(t *testing.T, dir string, older func(b uint64) bool) {
	t.Helper()
	name := filepath.Join(dir, "batches.seg")
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	table := crc32.MakeTable(crc32.Castagnoli)
	var out []byte
	for len(data) > 0 {
		if len(data) < 9 || len(data) < 9+int(binary.BigEndian.Uint32(data[1:5])) {
			t.Fatal("batches.seg ends inside a frame")
		}
		kind, size := data[0], int(binary.BigEndian.Uint32(data[1:5]))
		payload := data[5 : 5+size]
		data = data[9+size:]
		if kind == 'b' {
			var row map[string]json.RawMessage
			var b uint64
			if err := json.Unmarshal(payload, &row); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(row["b"], &b); err != nil {
				t.Fatal(err)
			}
			if older(b) {
				delete(row, "online")
				delete(row, "back")
				if payload, err = json.Marshal(row); err != nil {
					t.Fatal(err)
				}
			}
		}
		at := len(out)
		out = append(out, kind)
		out = binary.BigEndian.AppendUint32(out, uint32(len(payload)))
		out = append(out, payload...)
		out = binary.BigEndian.AppendUint32(out, crc32.Checksum(out[at:], table))
	}
	if err := os.WriteFile(name, out, 0o600); err != nil {
		t.Fatal(err)
	}
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
		// commits, when set, writes commits/ in dir.
		commits func(t *testing.T, dir string, rows []ledger.Row)
		until   func(f *fakeIngest, u *Uploader) bool
		check   func(t *testing.T, f *fakeIngest, u *Uploader, dirs home.Dirs, problems func() string)
	}{
		{
			name: "a server head the ledger disagrees with stops every upload under the device's ID, and doctor hears why",
			prep: func(t *testing.T, f *fakeIngest, rows []ledger.Row) {
				forked := rows[0]
				forked.Head[0] ^= 0xff
				hold(t, f, []ledger.Row{forked}, 1)
			},
			until: func(f *fakeIngest, u *Uploader) bool { return u.Status().Fork != 0 },
			check: func(t *testing.T, f *fakeIngest, u *Uploader, _ home.Dirs, problems func() string) {
				if fork := u.Status().Fork; fork != 1 {
					t.Errorf("fork at %d", fork)
				}
				if f.posts != 0 {
					t.Errorf("%d uploads went after the fork: %q", f.posts, f.bodies)
				}
				if p := problems(); !strings.Contains(p, "batch 1") || !strings.Contains(p, "can't go on uploading under this computer's ID") {
					t.Errorf("doctor wasn't told: %q", p)
				}
			},
		},
		{
			name: "a fork found at the request that carries a commit's first batch stores no commit",
			prep: func(t *testing.T, f *fakeIngest, rows []ledger.Row) {
				// The server's head is batch 1, and it holds another batch 2,
				// which the request that carries the device's answers as
				// batch_conflict.
				hold(t, f, rows[:1], 1)
				forked := rows[1]
				forked.Head[0] ^= 0xff
				f.rows[2] = fakeRow{b: 2, n: forked.N, root: forked.Root.String(), head: forked.Head.String(), prev: forked.Prev.String(),
					receivedAt: f.head.receivedAt, sig: f.head.sig, keyID: f.keyID}
			},
			commits: func(t *testing.T, dir string, rows []ledger.Row) {
				seen := rows[1].SealedAt.Add(-time.Minute) // its first batch is 2
				writeCommitLines(t, dir, seen.UTC().Format(time.DateOnly), commitRecord(1, seen.UnixMilli()))
			},
			until: func(f *fakeIngest, u *Uploader) bool { return u.Status().Fork != 0 },
			check: func(t *testing.T, f *fakeIngest, u *Uploader, _ home.Dirs, _ func() string) {
				if fork := u.Status().Fork; fork != 2 {
					t.Errorf("fork at %d, want 2", fork)
				}
				sent := slices.ContainsFunc(f.bodies, func(b []byte) bool { return bytes.Contains(b, []byte(testSHA(1))) })
				if len(f.commitRecs) != 0 || sent {
					t.Errorf("the server stores commits %v, and a body carried it: %v", f.commitRecs, sent)
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
			if tc.commits != nil {
				tc.commits(t, dirs.Commits(), rows)
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
	oldest, newest := end.Add(-20*time.Minute).UnixMilli(), end.Add(-time.Minute).UnixMilli()
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"the minutes the server's clock has reached go up", func(t *testing.T) {
			waitFor(t, "the oldest minute", func() bool {
				u.Wake() // as the ticker would
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
		{"doctor hears that this computer's clock runs ahead of the server's", func(t *testing.T) {
			pmu.Lock()
			defer pmu.Unlock()
			if !strings.Contains(problem, "minutes ahead of the server's") {
				t.Errorf("doctor hears %q", problem)
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
		u.Wake() // as the ticker would
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
	dirs   home.Dirs
	w      *ledger.Writer
	rows   []ledger.Row
	start  time.Time
	f      *fakeIngest
	st     *state.Store
	creds  Credentials
	client *http.Client
	u      *Uploader

	pmu     sync.Mutex
	problem string // what the uploader last told doctor
}

// doctor is what the uploader last told doctor.
func (r *rig) doctor() string {
	r.pmu.Lock()
	defer r.pmu.Unlock()
	return r.problem
}

// newRig makes a chain of n batches a quarter hour apart from start, and
// a bucket store holding the given minutes of start, credited 30 s each.
func newRig(t *testing.T, n int, minutes []int, linkedAfter time.Duration) *rig {
	t.Helper()
	return newRigWith(t, n, minutes, linkedAfter, nil)
}

// newRigWith is newRig, with the batches older(k) names sealed as an older
// pairtime version sealed them (smallChainWith).
func newRigWith(t *testing.T, n int, minutes []int, linkedAfter time.Duration, older func(k int) bool) *rig {
	t.Helper()
	dir := t.TempDir()
	r := &rig{dirs: home.Dirs{Data: dir, Config: dir, Runtime: dir}}
	r.w, r.rows = smallChainWith(t, r.dirs, n, older)
	return r.finish(t, minutes, linkedAfter)
}

// finish makes the rest of a rig over the chain in r.w and r.rows: its
// bucket store holding the given minutes of start, a quarter hour before
// batch 1's seal, the fake server, and an uploader that isn't started.
func (r *rig) finish(t *testing.T, minutes []int, linkedAfter time.Duration) *rig {
	t.Helper()
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
	r.client = srv.Client()
	r.u = r.newUploader(t)
	return r
}

// newUploader makes an uploader over the rig's files that isn't started.
func (r *rig) newUploader(t *testing.T) *Uploader {
	t.Helper()
	u, err := New(Options{
		Ledger: r.w, LedgerDir: r.dirs.Ledger(), BucketDir: r.dirs.Buckets(), DataDir: r.dirs.Data, State: r.st,
		Login:  LoginFunc(func() (Credentials, bool) { return r.creds, true }),
		Params: newestTable(), Client: r.client, Draw: func(int64) int64 { return 0 },
		Problem: func(msg string) {
			r.pmu.Lock()
			r.problem = msg
			r.pmu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(u.Close)
	return u
}

// restart stops the uploader and runs another over the same files for a
// few wakes, as the next agent does: it reads state.json from disk again.
func (r *rig) restart(t *testing.T) {
	t.Helper()
	r.u.Close()
	st, _, err := state.Open(r.dirs.StateFile())
	if err != nil {
		t.Fatal(err)
	}
	r.st = st
	r.u = r.newUploader(t)
	r.u.Start()
	for range 3 {
		r.u.Wake()
		time.Sleep(100 * time.Millisecond)
	}
	r.u.Close()
}

// run starts the uploader, waits until cond holds under the fake's lock,
// waking it meanwhile as its ticker would, and stops it.
func (r *rig) run(t *testing.T, what string, cond func(f *fakeIngest) bool) {
	t.Helper()
	r.u.Start()
	waitFor(t, what, func() bool {
		r.u.Wake()
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
	r.run(t, "the fork", func(f *fakeIngest) bool { return r.u.Status().Fork != 0 })
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	tests := []struct {
		name string
		ok   bool
	}{
		{"the fork is at the device's next batch, which the server already holds", r.u.Status().Fork == 4},
		{"nothing more went under the device's ID: no root, no minute and no labels (#150 R5)", r.f.posts == 0 && len(r.f.buckets) == 0 && r.f.labels == 0},
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
		linking  bool
	}{
		{"a first sign-in starts at the first batch sealed after it", saved{}, la(2.5), 3, true},
		{"the same sign-in keeps its place", saved{From: 3, LinkedAt: la(2.5).UnixMilli()}, la(2.5), 3, false},
		{"a re-link moves to the first batch sealed after it", saved{From: 1, LinkedAt: la(0).UnixMilli()}, la(3.5), 4, true},
		{"a re-link after the last batch starts at the next one", saved{From: 1, LinkedAt: la(0).UnixMilli()}, la(9), 5, true},
		{"a sign-in with no linked_at keeps the place it has", saved{From: 2}, time.Time{}, 2, false},
		{"a first sign-in with no linked_at starts at the next batch", saved{}, time.Time{}, 5, true},
		{"a batch sealed in the second a linked_at of whole seconds names counts as sealed before the link", saved{}, la(3), 4, true},
		{"a linked_at with milliseconds is compared at full precision", saved{}, la(3).Add(-500 * time.Millisecond), 3, true},
		{"a re-link starts after the batches sealed in local-only mode after it", saved{From: 1, LinkedAt: la(0).UnixMilli(), Gaps: []gap{{2, 3}}}, la(1.5), 4, true},
		{"a re-link while config.toml still says local starts at the next batch to be sealed", saved{From: 1, LinkedAt: la(0).UnixMilli(), LocalFrom: 2}, la(1.5), 5, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := tc.st.clone()
			relink(&st, ix, Credentials{LinkedAt: tc.linkedAt})
			if st.From != tc.from || st.Linking != tc.linking {
				t.Errorf("From = %d, linking %v; want %d, %v", st.From, st.Linking, tc.from, tc.linking)
			}
			if !slices.Equal(st.Gaps, tc.st.Gaps) || st.LocalFrom != tc.st.LocalFrom {
				t.Errorf("the runs are %v from %d, want them as recorded, %v from %d", st.Gaps, st.LocalFrom, tc.st.Gaps, tc.st.LocalFrom)
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
		{"after a state read, the batches between the server's head and From are", saved{Head: 7, Next: 5, From: 12}, 9, true},
		{"but not those at or below the server's head", saved{Head: 7, Next: 5, From: 12}, 6, false},
	}
	for _, tc := range gaps {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.st.localOnly(tc.b); got != tc.want {
				t.Errorf("localOnly(%d) = %v", tc.b, got)
			}
		})
	}
	t.Run("after a re-link the roots at or below the server's head with no receipt go again, over the link's gap, with the link's first root alone", func(t *testing.T) {
		sealed, got := make([]float64, 13), make([]float64, 13)
		for i := range sealed {
			sealed[i], got[i] = float64(i+1), -1
		}
		for i := range 4 {
			got[i] = float64(i + 1)
		}
		st := saved{Head: 7, Next: 5, From: 12, Linking: true, Gaps: []gap{{8, 11}}}
		roots, more := rootsOf(planInput{rows: indexOf(t, sealed, got), st: st}, 100)
		var bs []uint64
		for _, r := range roots {
			bs = append(bs, r.B)
		}
		if !slices.Equal(bs, []uint64{5, 6, 7, 12}) || !more {
			t.Errorf("roots %v, more %v; want [5 6 7 12], true", bs, more)
		}
	})
}

// TestRelinkRecovery is a re-link after a request the server stored but
// whose answer was lost: the server holds batches 1 to 3, the device the
// receipt of batch 1 alone, batch 4 was sealed in local-only mode, and the
// re-link was before batch 5. Batches 2 and 3 go again, and their
// duplicates bring the receipts their minutes wait for; the chain goes on
// from batch 5 over batch 4's gap.
func TestRelinkRecovery(t *testing.T) {
	minutes := make([]int, 75)
	for i := range minutes {
		minutes[i] = i
	}
	r := newRig(t, 5, minutes, 65*time.Minute) // the re-link, between batches 4 and 5
	hold(t, r.f, r.rows[:3], 1)
	fr := r.f.rows[1]
	sig, _ := hex.DecodeString(fr.sig)
	rc := pkgledger.Receipt{ReceivedAt: fr.receivedAt}
	copy(rc.Signature[:], sig)
	if err := r.w.StoreReceipt(1, rc); err != nil {
		t.Fatal(err)
	}
	r.f.relinked = true
	pub := r.f.key.Public().(ed25519.PublicKey)
	if err := r.st.Put(StateKey, saved{
		Chain: r.w.DeviceID(), Server: r.creds.Server, Device: r.w.DeviceID(), Req: 64, Next: 2, From: 1, ChainStart: 1, Head: 1,
		LinkedAt:  r.start.Add(-time.Minute).UnixMilli(), // the first link, before batch 1
		Gaps:      []gap{{4, 4}},                         // sealed after pairtime logout
		ServerKey: &ServerKey{KeyID: r.f.keyID, PublicKey: hex.EncodeToString(pub)},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.st.Save(); err != nil {
		t.Fatal(err)
	}
	r.run(t, "every minute but batch 4's", func(f *fakeIngest) bool { return len(f.buckets) == 60 })
	_, rows, err := ledger.ReadRows(r.dirs.Ledger(), 1)
	if err != nil {
		t.Fatal(err)
	}
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	in := func(from, to int) int {
		n := 0
		for ms := range r.f.buckets {
			if m := int((ms - r.start.UnixMilli()) / 60_000); m >= from && m <= to {
				n++
			}
		}
		return n
	}
	tests := []struct {
		name string
		ok   bool
	}{
		{"batches 2 and 3 got their receipts back from their duplicates", len(rows) == 5 && rows[1].Receipt != nil && rows[2].Receipt != nil},
		{"the chain went on from batch 5, over batch 4's gap", r.f.rows[5].b == 5 && r.f.rows[4].b == 0 && slices.Contains(r.f.gaps, [2]uint64{4, 4})},
		{"the minutes of batches 2 and 3 went", in(15, 44) == 30},
		{"no minute of batch 4 went", in(45, 59) == 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Error("no")
			}
		})
	}
}

// TestRelinkRestart is a restart after a re-link was seen and before its
// first batch was stored, with that batch's root waiting for its retry:
// Next is above the server's head by design, which says nothing of a
// server that lost writes, so the request goes as it waited.
func TestRelinkRestart(t *testing.T) {
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
	row := r.rows[5]
	js, err := encodeBody(Body{Batches: []Root{{B: row.B, N: row.N, Root: row.Root, Head: row.Head, Prev: row.Prev}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.dirs.Data, OutboxName), js, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(js)
	pub := r.f.key.Public().(ed25519.PublicKey)
	if err := r.st.Put(StateKey, saved{
		Chain: r.w.DeviceID(), Server: r.creds.Server, Device: r.w.DeviceID(), Req: 64, Next: 6, From: 6, Linking: true, Head: 3, ChainStart: 1,
		LinkedAt:  r.creds.LinkedAt.UnixMilli(), // the re-link, seen before the restart
		Gaps:      []gap{{4, 5}},
		Modes:     6, // this version's place, which takes the modes from the rows
		ServerKey: &ServerKey{KeyID: r.f.keyID, PublicKey: hex.EncodeToString(pub)},
		Outbox:    &outbox{Key: event.NewEventID(time.Now()), Hash: hex.EncodeToString(sum[:])},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.st.Save(); err != nil {
		t.Fatal(err)
	}
	r.run(t, "batch 6", func(f *fakeIngest) bool { return f.head != nil && f.head.b == 6 })
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	tests := []struct {
		name string
		ok   bool
	}{
		{"the request that waited went first, as it waited", len(r.f.bodies) > 0 && bytes.Equal(r.f.bodies[0], js)},
		{"the chain went on from batch 6, over the gap of batches 4 and 5", slices.Contains(r.f.gaps, [2]uint64{4, 5})},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Errorf("no: the requests were %q", r.f.bodies)
			}
		})
	}
}

// TestOutboxAfterUpgrade is a request an older version left waiting for
// its retry, after an upgrade, in a place that took no modes from the
// rows: its root is of batch 4, which an older writer sealed, recording no
// mode, so it counts as sealed in local-only mode now (foldModes). privacy
// show shows the plan made again, not the request, and the uploader never
// sends it, nor any root from batch 4 on. Both say the cause, batches an
// older version sealed, and the way out, pairtime logout and pairtime
// login.
func TestOutboxAfterUpgrade(t *testing.T) {
	r := newRigWith(t, 6, nil, -time.Minute, func(k int) bool { return k >= 4 })
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
	row := r.rows[3]
	js, err := encodeBody(Body{Batches: []Root{{B: row.B, N: row.N, Root: row.Root, Head: row.Head, Prev: row.Prev}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.dirs.Data, OutboxName), js, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(js)
	pub := r.f.key.Public().(ed25519.PublicKey)
	// The place an older version saved, which kept no modes from the rows.
	if err := r.st.Put(StateKey, saved{
		Chain: r.w.DeviceID(), Server: r.creds.Server, Device: r.w.DeviceID(), Req: 64, Next: 4, From: 1, Head: 3, ChainStart: 1,
		LinkedAt:  r.creds.LinkedAt.UnixMilli(),
		ServerKey: &ServerKey{KeyID: r.f.keyID, PublicKey: hex.EncodeToString(pub)},
		Outbox:    &outbox{Key: event.NewEventID(time.Now()), Hash: hex.EncodeToString(sum[:])},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.st.Save(); err != nil {
		t.Fatal(err)
	}
	pv, err := Next(PreviewOptions{
		LedgerDir: r.dirs.Ledger(), BucketDir: r.dirs.Buckets(), DataDir: r.dirs.Data, StateFile: r.dirs.StateFile(),
		Params: newestTable(), Credentials: r.creds,
	})
	if err != nil {
		t.Fatal(err)
	}
	r.run(t, "a state read, and doctor to hear why uploads stopped", func(f *fakeIngest) bool {
		return f.gets > 0 && strings.Contains(r.doctor(), "stopped uploading")
	})
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	sent := slices.ContainsFunc(r.f.bodies, func(b []byte) bool { return bytes.Equal(b, js) })
	_, kept := os.Stat(filepath.Join(r.dirs.Data, OutboxName))
	tests := []struct {
		name string
		ok   bool
	}{
		{"privacy show shows the plan made again, with no root and no minute, not the waiting request", !pv.Retry && pv.Batches == 0 && pv.Buckets == 0 && !bytes.Equal(pv.JSON, js) && pv.Stopped == 4},
		{"the uploader never sent the waiting request, nor any root from batch 4 on", !sent && r.f.head.b == 3 && r.f.rows[4].b == 0},
		{"the waiting request is gone", errors.Is(kept, fs.ErrNotExist)},
		{"privacy show says an older version sealed the batch the roots stop at", pv.NoMode},
		{"doctor hears that cause and the way out", strings.Contains(r.doctor(), noModeProblem)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Errorf("no: privacy show %+v; the server holds head %d after %q; doctor hears %q", pv, r.f.head.b, r.f.bodies, r.doctor())
			}
		})
	}
}

// TestOneRequestAWake is a steady session under ingest's rate (#150 R3):
// a wake sends what can go, and the buckets that wait for the receipts its
// request brings go with the next wake, not in a second request straight
// after it.
func TestOneRequestAWake(t *testing.T) {
	minutes := make([]int, 45)
	for i := range minutes {
		minutes[i] = i
	}
	r := newRig(t, 3, minutes, -time.Minute)
	seen := func() (posts, buckets int) {
		r.f.mu.Lock()
		defer r.f.mu.Unlock()
		return r.f.posts, len(r.f.buckets)
	}
	r.u.Start()
	waitFor(t, "the first wake's requests", func() bool { posts, _ := seen(); return posts >= 2 })
	time.Sleep(300 * time.Millisecond) // a request straight after would have gone by now
	firstPosts, firstBuckets := seen()
	r.u.Wake()
	waitFor(t, "the next wake's request", func() bool { _, buckets := seen(); return buckets == len(minutes) })
	lastPosts, _ := seen()
	tests := []struct {
		name string
		ok   bool
	}{
		{"the first wake sent the link's first root alone, then the other roots with the minutes its receipt let go", firstPosts == 2 && firstBuckets == 15},
		{"the minutes that waited for the second request's receipts went with the next wake, in one request", lastPosts == 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Errorf("no: %d requests and %d minutes after the first wake, %d requests after the next", firstPosts, firstBuckets, lastPosts)
			}
		})
	}
}

// receipts stores the fake server's receipts of rows in the device's
// batches.seg, as if their answers had come.
func receipts(t *testing.T, r *rig, rows []ledger.Row) {
	t.Helper()
	for _, row := range rows {
		fr := r.f.rows[row.B]
		sig, _ := hex.DecodeString(fr.sig)
		rc := pkgledger.Receipt{ReceivedAt: fr.receivedAt}
		copy(rc.Signature[:], sig)
		if err := r.w.StoreReceipt(row.B, rc); err != nil {
			t.Fatal(err)
		}
	}
}

// TestStaleFork is an upload refused counter_stale, whose state read then
// finds a fork, as when another computer with the same identity got there
// first: nothing more goes under the device id, not even the refused
// request's minutes (#150 R5).
func TestStaleFork(t *testing.T) {
	r := newRig(t, 3, []int{0, 1, 2}, -time.Minute)
	hold(t, r.f, r.rows[:1], 1)
	receipts(t, r, r.rows[:1])
	r.f.faults = map[int]string{1: "stale-fork"}
	r.run(t, "the fork", func(f *fakeIngest) bool { return r.u.Status().Fork != 0 })
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	tests := []struct {
		name string
		ok   bool
	}{
		{"the fork is at the batch the other computer stored", r.u.Status().Fork == 2},
		{"no request went after the refused one", r.f.posts == 1},
		{"no minute of it was stored", len(r.f.buckets) == 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Errorf("no: %d POSTs, %d minutes, fork %d", r.f.posts, len(r.f.buckets), r.u.Status().Fork)
			}
		})
	}
}

// TestMismatch is the server's device_mismatch: nothing more goes under
// the device id, after a restart too, when not even a state read goes,
// and privacy show says uploads are stopped (#150 R5).
func TestMismatch(t *testing.T) {
	r := newRig(t, 3, []int{0}, -time.Minute)
	r.f.faults = map[int]string{1: "mismatch"}
	r.run(t, "the refused upload", func(f *fakeIngest) bool { return f.posts == 1 })
	r.f.mu.Lock()
	gets := r.f.gets
	r.f.mu.Unlock()
	p, err := Next(PreviewOptions{
		LedgerDir: r.dirs.Ledger(), BucketDir: r.dirs.Buckets(), DataDir: r.dirs.Data, StateFile: r.dirs.StateFile(),
		Params: newestTable(), Credentials: r.creds,
	})
	if err != nil {
		t.Fatal(err)
	}
	r.restart(t)
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	tests := []struct {
		name string
		ok   bool
	}{
		{"nothing more went after the refusal", r.f.posts == 1},
		{"privacy show says uploads are stopped, and prints no upload", p.Mismatch && p.JSON == nil},
		{"a restarted uploader asked the server nothing under the device id", r.f.gets == gets},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Errorf("no: %d POSTs, %d state reads (%d before the restart), preview %+v", r.f.posts, r.f.gets, gets, p)
			}
		})
	}
}

// TestLostStateGap is a device that lost state.json after a re-link whose
// first batches were sealed in local mode: the server holds batches 1, 2,
// 5 and 6 and recorded 3 and 4 as the link's gap, and the device lacks
// batch 2's receipt. The link's start settles past the server's gap, so
// the receipt comes back over it and the roots don't stop there.
func TestLostStateGap(t *testing.T) {
	minutes := make([]int, 90)
	for i := range minutes {
		minutes[i] = i
	}
	r := newRig(t, 6, minutes, 35*time.Minute) // linked between batches 2 and 3, which config.toml still sealed local
	held := []ledger.Row{r.rows[0], r.rows[1], r.rows[4], r.rows[5]}
	hold(t, r.f, held, 1)
	receipts(t, r, []ledger.Row{r.rows[0], r.rows[4], r.rows[5]})
	r.f.gaps = [][2]uint64{{3, 4}}
	r.run(t, "every minute but those of batches 3 and 4", func(f *fakeIngest) bool { return len(f.buckets) == 60 })
	_, rows, err := ledger.ReadRows(r.dirs.Ledger(), 1)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		ok   bool
	}{
		{"batch 2 got its receipt back", len(rows) == 6 && rows[1].Receipt != nil},
		{"the roots don't stop at the server's gap", r.u.Status().Stopped == 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Errorf("no: status %+v", r.u.Status())
			}
		})
	}
}

// TestStateBlockPage is a state read a TLS-intercepting proxy answers with
// a 200 block page: with no X-Pairtime-Time it isn't pairtimed's answer,
// whatever its body, so it counts as a network error and leaves an
// upload-failure note, as an upload answered that way does (ingest-v1 5.5).
func TestStateBlockPage(t *testing.T) {
	dir := t.TempDir()
	dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
	w, rows := smallChain(t, dirs, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><body>This site is blocked on this network.</body></html>"))
	}))
	t.Cleanup(srv.Close)
	st, _, err := state.Open(dirs.StateFile())
	if err != nil {
		t.Fatal(err)
	}
	devKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, 32))
	creds := Credentials{Server: srv.URL, Token: testToken, DeviceID: w.DeviceID(), Sign: KeySigner(devKey), LinkedAt: rows[0].SealedAt.Add(-time.Minute)}
	u, err := New(Options{
		Ledger: w, LedgerDir: dirs.Ledger(), BucketDir: dirs.Buckets(), DataDir: dirs.Data, State: st,
		Login:  LoginFunc(func() (Credentials, bool) { return creds, true }),
		Params: newestTable(), Client: srv.Client(), Draw: func(int64) int64 { return 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	u.Start()
	t.Cleanup(u.Close)
	waitFor(t, "the failed state read", func() bool { return u.Status().LastFailure != "" })
	u.Close()
	if _, _, err := w.Seal(); err != nil {
		t.Fatal(err)
	}
	var reasons []string
	if err := ledger.ReadBatches(dirs.Ledger(), 2, func(b ledger.Batch) error {
		for _, rec := range b.Records {
			if rec.Header.RType() != pkgledger.RTypeUploadFailure {
				continue
			}
			var body struct {
				E struct {
					Reason string `json:"reason"`
				} `json:"e"`
			}
			if err := json.Unmarshal(rec.Body, &body); err != nil {
				return err
			}
			reasons = append(reasons, body.E.Reason)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		ok   bool
	}{
		{"the block page left a connect note, as a network error does", slices.Contains(reasons, string(ReasonConnect))},
		{"the uploader says why it failed", strings.HasPrefix(u.Status().LastFailure, string(ReasonConnect))},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Errorf("no: the notes say %q, and the status %q", reasons, u.Status().LastFailure)
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

// chainAt makes a chain whose writer opened at start, with a batch sealed at
// each of offsets after it, one checkpoint in each: start a quarter hour
// and each offset a whole number of them, as a checkpoint's t must be.
func chainAt(t *testing.T, dirs home.Dirs, start time.Time, offsets ...time.Duration) (*ledger.Writer, []ledger.Row) {
	t.Helper()
	kDev := sha256.Sum256([]byte("uploader resume test K_dev"))
	keys, err := pkgledger.DeriveKeys(kDev[:])
	if err != nil {
		t.Fatal(err)
	}
	var (
		mu  sync.Mutex
		now = start
	)
	w, err := ledger.Open(ledger.Options{
		Dir: dirs.Ledger(), Keys: keys, KeyID: "0123456789abcdef", Params: newestTable(),
		Now:    func() time.Time { mu.Lock(); defer mu.Unlock(); return now },
		Online: func() bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	for _, d := range offsets {
		q := start.Add(d)
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

// TestRewindCommits is a server restored to before the requests that
// carried the device's latest batches and a commit record: it took batches
// 1 to 5, whose receipts the device keeps, and lost 4 and 5, and the record
// of commit A, whose first batch is batch 2, two days before batch 3, went
// in a request it lost too. The rewind drops every commit mark, since a
// record goes once its first batch's root has been answered, often days
// after its observed_at, so A's record goes again, and the server answers
// what it kept as duplicates. A line withheld for good stays withheld.
func TestRewindCommits(t *testing.T) {
	dir := t.TempDir()
	r := &rig{dirs: home.Dirs{Data: dir, Config: dir, Runtime: dir}}
	start := time.Now().UTC().Truncate(time.Hour).Add(-72 * time.Hour)
	q := 15 * time.Minute
	r.w, r.rows = chainAt(t, r.dirs, start, q, 2*q, 48*time.Hour+q, 48*time.Hour+2*q, 48*time.Hour+3*q)
	r.finish(t, nil, 0)
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
	delete(r.f.rows, 4)
	delete(r.f.rows, 5)
	head := r.f.rows[3]
	r.f.head = &head
	// Commit A's line went. Commit W's, observed before the chain began, was
	// withheld for good. The device's mark is past both.
	day := r.start.Format(time.DateOnly)
	shaA, shaW := testSHA(1), testSHA(2)
	lineW := commitRecord(2, r.start.Add(-30*time.Minute).UnixMilli())
	lineA := commitRecord(1, r.start.Add(20*time.Minute).UnixMilli())
	ends := writeCommitLines(t, r.dirs.Commits(), day, lineW, lineA)
	pub := r.f.key.Public().(ed25519.PublicKey)
	if err := r.st.Put(StateKey, saved{
		Chain: r.w.DeviceID(), Server: r.creds.Server, Device: r.w.DeviceID(), Req: 64, Next: 6, From: 1, ChainStart: 1, Head: 5,
		LinkedAt:    r.creds.LinkedAt.UnixMilli(),
		Modes:       5, // this version's place, which takes the modes from the rows
		ServerKey:   &ServerKey{KeyID: r.f.keyID, PublicKey: hex.EncodeToString(pub)},
		CommitMarks: map[string]store.Mark{day: {Off: ends[1], Line: commitDigest([]byte(lineA))}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.st.Save(); err != nil {
		t.Fatal(err)
	}
	r.run(t, "commit A's record again", func(f *fakeIngest) bool { _, ok := f.commitRecs[shaA]; return ok })
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	wSent := slices.ContainsFunc(r.f.bodies, func(b []byte) bool { return bytes.Contains(b, []byte(shaW)) })
	tests := []struct {
		name string
		ok   bool
	}{
		{"batches 4 and 5 went again", r.f.rows[4].b == 4 && r.f.rows[5].b == 5 && r.f.head.b == 5},
		{"the line withheld for good stays withheld", !wSent},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Errorf("no: the requests were %q", r.f.bodies)
			}
		})
	}
}
