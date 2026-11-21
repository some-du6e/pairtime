// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/state"
	"github.com/some-du6e/pairtime/internal/agent/store"
	"github.com/some-du6e/pairtime/pkg/event"
	pkgledger "github.com/some-du6e/pairtime/pkg/ledger"
)

func TestLocalRuns(t *testing.T) {
	type check struct {
		gaps      []gap
		localFrom uint64
	}
	tests := []struct {
		name string
		st   saved
		do   func(*saved) bool
		want check
		// changed is what mode reports, for the cases that call it.
		changed bool
	}{
		{"the first batch sealed in local mode opens a run", saved{}, func(s *saved) bool { return s.mode(5, true) }, check{localFrom: 5}, true},
		{"more batches sealed in local mode change nothing", saved{LocalFrom: 5}, func(s *saved) bool { return s.mode(6, true) }, check{localFrom: 5}, false},
		{"the first batch sealed online again closes the run, itself included", saved{LocalFrom: 5}, func(s *saved) bool { return s.mode(9, false) }, check{gaps: []gap{{5, 9}}}, true},
		{"a batch sealed online with no run open changes nothing", saved{Gaps: []gap{{2, 3}}}, func(s *saved) bool { return s.mode(9, false) }, check{gaps: []gap{{2, 3}}}, false},
		{"a run that touches a gap becomes one with it", saved{Gaps: []gap{{1, 4}}, LocalFrom: 5}, func(s *saved) bool { return s.mode(7, false) }, check{gaps: []gap{{1, 7}}}, true},
		{"gaps that overlap merge", saved{Gaps: []gap{{1, 4}, {10, 12}}}, func(s *saved) bool { s.addGap(3, 6); return false }, check{gaps: []gap{{1, 6}, {10, 12}}}, false},
		{"a gap between two that it touches joins them", saved{Gaps: []gap{{1, 4}, {8, 9}}}, func(s *saved) bool { s.addGap(5, 7); return false }, check{gaps: []gap{{1, 9}}}, false},
		{"a gap inside one changes nothing", saved{Gaps: []gap{{1, 9}}}, func(s *saved) bool { s.addGap(3, 4); return false }, check{gaps: []gap{{1, 9}}}, false},
		{"gaps stay in order", saved{Gaps: []gap{{10, 12}}}, func(s *saved) bool { s.addGap(2, 3); return false }, check{gaps: []gap{{2, 3}, {10, 12}}}, false},
		{"a re-link ends an open run that started before it at the batch before its first", saved{LocalFrom: 5}, func(s *saved) bool { s.cutLocal(8); return false }, check{gaps: []gap{{5, 7}}}, false},
		{"a re-link ends a run that runs past its first batch before it", saved{Gaps: []gap{{5, 9}}}, func(s *saved) bool { s.cutLocal(8); return false }, check{gaps: []gap{{5, 7}}}, false},
		{"a run that started after the re-link stays", saved{Gaps: []gap{{9, 10}}, LocalFrom: 12}, func(s *saved) bool { s.cutLocal(8); return false }, check{gaps: []gap{{9, 10}}, localFrom: 12}, false},
		{"a run before the re-link stays as it was", saved{Gaps: []gap{{2, 3}}}, func(s *saved) bool { s.cutLocal(8); return false }, check{gaps: []gap{{2, 3}}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := tc.st.clone()
			if changed := tc.do(&st); changed != tc.changed {
				t.Errorf("reported a change: %v, want %v", changed, tc.changed)
			}
			if !slices.Equal(st.Gaps, tc.want.gaps) || st.LocalFrom != tc.want.localFrom {
				t.Errorf("gaps %v, local from %d; want %v, %d", st.Gaps, st.LocalFrom, tc.want.gaps, tc.want.localFrom)
			}
		})
	}

	t.Run("past gapsMax runs, the two oldest become one", func(t *testing.T) {
		var st saved
		for i := range gapsMax + 1 {
			b := uint64(3*i + 1)
			st.addGap(b, b)
		}
		if len(st.Gaps) != gapsMax {
			t.Fatalf("%d runs, want %d", len(st.Gaps), gapsMax)
		}
		if st.Gaps[0] != (gap{1, 4}) {
			t.Errorf("the first run is %v, want the two oldest as one, {1 4}", st.Gaps[0])
		}
	})

	stops := []struct {
		name string
		st   saved
		last uint64
		want uint64
	}{
		{"nothing sealed in local-only mode stops nothing", saved{Next: 4}, 9, 0},
		{"an open run after the server's head stops the roots at its first batch", saved{Next: 4, LocalFrom: 6}, 9, 6},
		{"a closed run after the server's head stops them too", saved{Next: 4, Gaps: []gap{{6, 7}}}, 9, 6},
		{"a run the next root is inside of stops them at once", saved{Next: 6, Gaps: []gap{{5, 7}}}, 9, 6},
		{"a re-link's gap, before where the roots go on from, stops nothing", saved{Next: 8, From: 8, Gaps: []gap{{4, 7}}}, 9, 0},
		{"of two runs, the first stops them", saved{Next: 2, Gaps: []gap{{6, 7}, {3, 4}}}, 9, 3},
	}
	for _, tc := range stops {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.st.localStop(tc.last); got != tc.want {
				t.Errorf("localStop = %d, want %d", got, tc.want)
			}
		})
	}

	only := []struct {
		name string
		st   saved
		b    uint64
		want bool
	}{
		{"a batch at an open run's first is local-only", saved{LocalFrom: 6}, 6, true},
		{"a batch after it is too", saved{LocalFrom: 6}, 40, true},
		{"a batch before it isn't", saved{LocalFrom: 6, Next: 3}, 5, false},
	}
	for _, tc := range only {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.st.localOnly(tc.b); got != tc.want {
				t.Errorf("localOnly(%d) = %v", tc.b, got)
			}
		})
	}
}

// TestLocalCredit holds a day's revisions against batches 1 to 4, of which
// batch 3 was sealed in local-only mode and never went.
func TestLocalCredit(t *testing.T) {
	ix := indexOf(t, []float64{1, 2, 3, 4}, []float64{1.1, 2.1, -1, 4.1})
	dir := t.TempDir()
	bs, err := store.OpenBuckets(dir)
	if err != nil {
		t.Fatal(err)
	}
	table := newestTable()
	rev := func(minute int, credited float64, st event.BucketState, batch uint64) store.Bucket {
		b := testBucket(table, "d_test", day0.Add(time.Duration(minute)*time.Minute), credited, st)
		b.Batch = batch
		return b
	}
	for _, b := range []store.Bucket{
		rev(0, 15, event.StatePending, 1),
		rev(0, 30, event.StatePending, 3), // credit from batch 3's records
		rev(0, 30, event.StateFinal, 4),   // the flip, after them
		rev(1, 15, event.StatePending, 2), // another minute, after it in the file
		rev(0, 45, event.StateFinal, 4),   // new credit, still on top of batch 3's
	} {
		if _, _, err := bs.Put(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := bs.Close(); err != nil {
		t.Fatal(err)
	}
	st := saved{Next: 5, Gaps: []gap{{3, 3}}}
	p, err := makePlan(planInput{rows: ix, st: st, bucketDir: dir, lag: 900_000})
	if err != nil {
		t.Fatal(err)
	}
	type sentBucket struct {
		minute int64
		credit float64
	}
	var got []sentBucket
	for _, b := range p.body.Buckets {
		got = append(got, sentBucket{b.Minute.UnixMilli(), b.CreditedS})
	}
	tests := []struct {
		name string
		ok   bool
	}{
		{"the revision before batch 3 went, and the other minute's after it in the file", slices.Equal(got, []sentBucket{{at(0), 15}, {at(1), 15}})},
		{"no revision that rests on batch 3 went, nor any later one of its minute", len(got) == 2},
		{"the minute remembers that its credit rests on batch 3", p.sent[at(0)].B == 3},
		{"the day's mark moved past every line", len(p.marks) == 1},
		{"the next plan withholds the minute too", func() bool {
			next, err := makePlan(planInput{rows: ix, st: saved{Next: 5, Gaps: st.Gaps, Sent: p.sent}, bucketDir: dir, lag: 900_000})
			return err == nil && len(next.body.Buckets) == 2 && next.sent[at(0)].B == 3
		}()},
		{"the batch roots stop before batch 3", func() bool {
			roots := rootsOf(planInput{rows: ix, st: saved{Next: 2, Gaps: st.Gaps}}, 10)
			return len(roots) == 1 && roots[0].B == 2
		}()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Errorf("no: sent %v", got)
			}
		})
	}
}

// TestLocalMode is a signed-in device whose config.toml said mode =
// "local" while batches 3 and 4 were sealed, and online again by batch 5,
// with no re-link. No root from batch 3 on goes and doctor says to link
// again. After the re-link, from a new linked_at with no signed-out wake
// in between, the chain goes on from batch 6, the first sealed after it,
// and no minute of batches 3 to 5 ever goes: batch 5's records began in
// local-only mode.
func TestLocalMode(t *testing.T) {
	dir := t.TempDir()
	dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
	kDev := sha256.Sum256([]byte("uploader local mode test K_dev"))
	keys, err := pkgledger.DeriveKeys(kDev[:])
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Hour).Add(-3 * time.Hour)
	var (
		mu    sync.Mutex
		now   = start
		up    atomic.Pointer[Uploader]
		local atomic.Bool
	)
	w, err := ledger.Open(ledger.Options{
		Dir: dirs.Ledger(), Keys: keys, KeyID: "0123456789abcdef", Params: newestTable(),
		Now: func() time.Time { mu.Lock(); defer mu.Unlock(); return now },
		Sealed: func(b ledger.Batch) {
			if u := up.Load(); u != nil {
				u.Sealed(b)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	seal := func(k int) {
		t.Helper()
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
	// Minutes 0 to 89, fifteen to a batch: batch k holds minutes 15(k-1)
	// to 15k-1.
	bs, err := store.OpenBuckets(dirs.Buckets())
	if err != nil {
		t.Fatal(err)
	}
	for m := range 90 {
		if _, _, err := bs.Put(testBucket(newestTable(), w.DeviceID(), start.Add(time.Duration(m)*time.Minute), 30, event.StateFinal)); err != nil {
			t.Fatal(err)
		}
	}
	if err := bs.Close(); err != nil {
		t.Fatal(err)
	}
	devKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{11}, 32))
	f := newFakeIngest(w.DeviceID(), testToken, devKey.Public().(ed25519.PublicKey))
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	st, _, err := state.Open(dirs.StateFile())
	if err != nil {
		t.Fatal(err)
	}
	var (
		credsMu sync.Mutex
		creds   = Credentials{Server: srv.URL, Token: testToken, DeviceID: w.DeviceID(), Sign: KeySigner(devKey), LinkedAt: start.Add(-time.Minute)}
		problem string
	)
	u, err := New(Options{
		Ledger: w, LedgerDir: dirs.Ledger(), BucketDir: dirs.Buckets(), DataDir: dirs.Data, State: st,
		// As agent.SignedIn has it, nothing goes while the mode is local.
		Login: LoginFunc(func() (Credentials, bool) {
			credsMu.Lock()
			defer credsMu.Unlock()
			return creds, !local.Load()
		}),
		LocalOnly: func() (bool, bool) { return local.Load(), true },
		Params:    newestTable(), Client: srv.Client(), Draw: func(int64) int64 { return 0 },
		Problem: func(msg string) {
			credsMu.Lock()
			problem = msg
			credsMu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	up.Store(u)
	t.Cleanup(u.Close)
	u.Start()
	served := func(cond func() bool) func() bool {
		return func() bool {
			f.mu.Lock()
			defer f.mu.Unlock()
			return cond()
		}
	}
	doctor := func() string {
		credsMu.Lock()
		defer credsMu.Unlock()
		return problem
	}
	minutesIn := func(from, to int) int {
		n := 0
		for ms := range f.buckets {
			if m := int((ms - start.UnixMilli()) / 60_000); m >= from && m <= to {
				n++
			}
		}
		return n
	}

	seal(1)
	seal(2)
	waitFor(t, "batches 1 and 2 and their minutes", served(func() bool { return f.head != nil && f.head.b == 2 && len(f.buckets) == 30 }))
	local.Store(true)
	seal(3)
	seal(4)
	local.Store(false)
	seal(5)
	u.Wake()
	waitFor(t, "doctor to hear that uploads stopped", func() bool { return strings.Contains(doctor(), "stopped uploading") })
	for range 3 {
		u.Wake()
		time.Sleep(100 * time.Millisecond)
	}
	f.mu.Lock()
	stopped := []struct {
		name string
		ok   bool
	}{
		{"no root from the first batch sealed in local-only mode on went", f.head.b == 2 && f.rows[3].b == 0 && f.rows[4].b == 0 && f.rows[5].b == 0},
		{"no minute of a batch sealed in local-only mode went", minutesIn(30, 89) == 0},
		{"the uploader says where the roots stop", u.Status().Stopped == 3},
	}
	f.mu.Unlock()
	for _, tc := range stopped {
		t.Run("stopped/"+tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Error("no")
			}
		})
	}

	// pairtime logout and pairtime login: a new token and linked_at for the
	// same device id, between batches 5 and 6.
	credsMu.Lock()
	creds.LinkedAt = start.Add(80 * time.Minute)
	credsMu.Unlock()
	f.mu.Lock()
	f.relinked = true
	f.mu.Unlock()
	seal(6)
	u.Wake()
	waitFor(t, "batch 6 and its minutes", served(func() bool { return f.head.b == 6 && minutesIn(75, 89) == 15 }))
	for range 3 {
		u.Wake()
		time.Sleep(100 * time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	relinked := []struct {
		name string
		ok   bool
	}{
		{"the first root after the re-link is the first batch sealed after it", f.rows[6].b == 6 && f.rows[5].b == 0},
		{"no minute of batches 3 to 5 went, batch 5's included", minutesIn(30, 74) == 0},
		{"every other minute went", len(f.buckets) == 45},
		{"doctor no longer hears that uploads stopped", !strings.Contains(doctor(), "stopped uploading")},
		{"the uploader no longer says the roots stop", u.Status().Stopped == 0},
	}
	for _, tc := range relinked {
		t.Run("relinked/"+tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Error("no")
			}
		})
	}
}
