// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
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
		gaps            []gap
		localFrom, from uint64
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
		{"while the link's first batch isn't stored, From moves past a run sealed in local-only mode after the link", saved{Linking: true, From: 6, Gaps: []gap{{6, 7}}}, func(s *saved) bool { s.settle(9); return false }, check{gaps: []gap{{6, 7}}, from: 8}, false},
		{"an open run takes in every batch from it on, so From is the next batch to be sealed", saved{Linking: true, From: 6, LocalFrom: 6}, func(s *saved) bool { s.settle(9); return false }, check{localFrom: 6, from: 10}, false},
		{"a run From reaches past a closed one is passed over too", saved{Linking: true, From: 6, Gaps: []gap{{4, 7}}, LocalFrom: 8}, func(s *saved) bool { s.settle(9); return false }, check{gaps: []gap{{4, 7}}, localFrom: 8, from: 10}, false},
		{"a run that opens after From leaves it, since From's root goes before it", saved{Linking: true, From: 6, LocalFrom: 8}, func(s *saved) bool { s.settle(9); return false }, check{localFrom: 8, from: 6}, false},
		{"a run before From leaves it", saved{Linking: true, From: 6, Gaps: []gap{{2, 3}}}, func(s *saved) bool { s.settle(9); return false }, check{gaps: []gap{{2, 3}}, from: 6}, false},
		{"once the link's first batch is stored, From stays where it is", saved{From: 6, Gaps: []gap{{6, 7}}}, func(s *saved) bool { s.settle(9); return false }, check{gaps: []gap{{6, 7}}, from: 6}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := tc.st.clone()
			if changed := tc.do(&st); changed != tc.changed {
				t.Errorf("reported a change: %v, want %v", changed, tc.changed)
			}
			if !slices.Equal(st.Gaps, tc.want.gaps) || st.LocalFrom != tc.want.localFrom || st.From != tc.want.from {
				t.Errorf("gaps %v, local from %d, from %d; want %v, %d, %d", st.Gaps, st.LocalFrom, st.From, tc.want.gaps, tc.want.localFrom, tc.want.from)
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
		rev(1, 15, event.StatePending, 2), // another minute, after it in the file
		rev(0, 45, event.StatePending, 4), // new credit, still on top of batch 3's
		rev(0, 45, event.StateFinal, 4),   // the flip, after them
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
		{"across plans of one bucket each, the minute stays withheld", func() bool {
			var all []sentBucket
			in := planInput{rows: ix, st: saved{Next: 5, Gaps: st.Gaps}, bucketDir: dir, lag: 900_000, maxBuckets: 1}
			for range 4 {
				p, err := makePlan(in)
				if err != nil {
					return false
				}
				for _, b := range p.body.Buckets {
					all = append(all, sentBucket{b.Minute.UnixMilli(), b.CreditedS})
				}
				in.st.Marks, in.st.Sent = p.marks, p.sent
			}
			return slices.Equal(all, []sentBucket{{at(0), 15}, {at(1), 15}}) && in.st.Sent[at(0)].B == 3
		}()},
		{"the batch roots stop before batch 3", func() bool {
			roots, _ := rootsOf(planInput{rows: ix, st: saved{Next: 2, Gaps: st.Gaps}}, 10)
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

// liveRig is an uploader over a ledger writer that hands it each batch as
// it seals, against a fake server, with config.toml's mode and the
// sign-in as the test sets them: the tests of local-only mode. It is
// linked from before its first batch, and online, until the test says
// otherwise, and its uploader waits for the test to start it.
type liveRig struct {
	start time.Time
	w     *ledger.Writer
	f     *fakeIngest
	u     *Uploader
	// local is config.toml's mode = "local", and linked whether the
	// computer is linked to the server. As agent.SignedIn has it, nothing
	// goes while either keeps it local-only.
	local, linked atomic.Bool

	clockMu sync.Mutex
	now     time.Time

	mu      sync.Mutex
	creds   Credentials
	problem string
}

// newLiveRig makes a liveRig whose bucket store holds minutes 0 to
// minutes-1 after its start, credited 30 s each. Batch k, which seal
// seals at sealedAt(k), holds minutes 15(k-1) to 15k-1.
func newLiveRig(t *testing.T, minutes int) *liveRig {
	t.Helper()
	dir := t.TempDir()
	dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
	kDev := sha256.Sum256([]byte("uploader local mode test K_dev"))
	keys, err := pkgledger.DeriveKeys(kDev[:])
	if err != nil {
		t.Fatal(err)
	}
	r := &liveRig{start: time.Now().UTC().Truncate(time.Hour).Add(-3 * time.Hour)}
	r.now = r.start
	r.linked.Store(true)
	var up atomic.Pointer[Uploader]
	r.w, err = ledger.Open(ledger.Options{
		Dir: dirs.Ledger(), Keys: keys, KeyID: "0123456789abcdef", Params: newestTable(),
		Now: func() time.Time { r.clockMu.Lock(); defer r.clockMu.Unlock(); return r.now },
		Sealed: func(b ledger.Batch) {
			if u := up.Load(); u != nil {
				u.Sealed(b)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.w.Close() })
	bs, err := store.OpenBuckets(dirs.Buckets())
	if err != nil {
		t.Fatal(err)
	}
	for m := range minutes {
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
	st, _, err := state.Open(dirs.StateFile())
	if err != nil {
		t.Fatal(err)
	}
	r.creds = Credentials{Server: srv.URL, Token: testToken, DeviceID: r.w.DeviceID(), Sign: KeySigner(devKey), LinkedAt: r.start.Add(-time.Minute)}
	r.u, err = New(Options{
		Ledger: r.w, LedgerDir: dirs.Ledger(), BucketDir: dirs.Buckets(), DataDir: dirs.Data, State: st,
		Login: LoginFunc(func() (Credentials, bool) {
			r.mu.Lock()
			defer r.mu.Unlock()
			return r.creds, r.linked.Load() && !r.local.Load()
		}),
		LocalOnly: func() (bool, bool) { return r.local.Load(), true },
		Params:    newestTable(), Client: srv.Client(), Draw: func(int64) int64 { return 0 },
		Problem: func(msg string) {
			r.mu.Lock()
			r.problem = msg
			r.mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	up.Store(r.u)
	t.Cleanup(r.u.Close)
	return r
}

// sealedAt is when seal seals batch k.
func (r *liveRig) sealedAt(k int) time.Time { return r.start.Add(time.Duration(k) * 15 * time.Minute) }

// seal seals batch k, with one checkpoint in it.
func (r *liveRig) seal(t *testing.T, k int) {
	t.Helper()
	q := r.sealedAt(k)
	r.clockMu.Lock()
	r.now = q
	r.clockMu.Unlock()
	if _, err := r.w.Checkpoint("p_test", uint64(q.UnixMilli()), json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.w.Seal(); err != nil {
		t.Fatal(err)
	}
}

// link is pairtime login at at, the first or, after pairtime logout, again:
// a new token and linked_at for the same device id, after which the server
// takes the device's next batch at any b above its head.
func (r *liveRig) link(at time.Time) {
	r.mu.Lock()
	r.creds.LinkedAt = at
	r.mu.Unlock()
	r.f.mu.Lock()
	r.f.relinked = true
	r.f.mu.Unlock()
	r.linked.Store(true)
}

// served returns a condition on the fake server, which it checks under
// the server's lock after waking the uploader, as its ticker would.
func (r *liveRig) served(cond func(f *fakeIngest) bool) func() bool {
	return func() bool {
		r.u.Wake()
		r.f.mu.Lock()
		defer r.f.mu.Unlock()
		return cond(r.f)
	}
}

// doctor is what the uploader last told doctor.
func (r *liveRig) doctor() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.problem
}

// minutesIn counts the minutes from to to after the start that f holds.
// The caller holds f's lock.
func (r *liveRig) minutesIn(f *fakeIngest, from, to int) int {
	n := 0
	for ms := range f.buckets {
		if m := int((ms - r.start.UnixMilli()) / 60_000); m >= from && m <= to {
			n++
		}
	}
	return n
}

// drain wakes the uploader a few times, so anything it would still send
// goes before the test looks.
func (r *liveRig) drain() {
	for range 3 {
		r.u.Wake()
		time.Sleep(100 * time.Millisecond)
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
	r := newLiveRig(t, 90)
	r.u.Start()
	r.seal(t, 1)
	r.seal(t, 2)
	waitFor(t, "batches 1 and 2 and their minutes", r.served(func(f *fakeIngest) bool { return f.head != nil && f.head.b == 2 && len(f.buckets) == 30 }))
	r.local.Store(true)
	r.seal(t, 3)
	r.seal(t, 4)
	r.local.Store(false)
	r.seal(t, 5)
	r.u.Wake()
	waitFor(t, "doctor to hear that uploads stopped", func() bool { return strings.Contains(r.doctor(), "stopped uploading") })
	r.drain()
	r.f.mu.Lock()
	stopped := []struct {
		name string
		ok   bool
	}{
		{"no root from the first batch sealed in local-only mode on went", r.f.head.b == 2 && r.f.rows[3].b == 0 && r.f.rows[4].b == 0 && r.f.rows[5].b == 0},
		{"no minute of a batch sealed in local-only mode went", r.minutesIn(r.f, 30, 89) == 0},
		{"the uploader says where the roots stop", r.u.Status().Stopped == 3},
	}
	r.f.mu.Unlock()
	for _, tc := range stopped {
		t.Run("stopped/"+tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Error("no")
			}
		})
	}

	// pairtime logout and pairtime login: a new token and linked_at for the
	// same device id, between batches 5 and 6.
	r.link(r.start.Add(80 * time.Minute))
	r.seal(t, 6)
	r.u.Wake()
	waitFor(t, "batch 6 and its minutes", r.served(func(f *fakeIngest) bool { return f.head.b == 6 && r.minutesIn(f, 75, 89) == 15 }))
	r.drain()
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	relinked := []struct {
		name string
		ok   bool
	}{
		{"the first root after the re-link is the first batch sealed after it", r.f.rows[6].b == 6 && r.f.rows[5].b == 0},
		{"no minute of batches 3 to 5 went, batch 5's included", r.minutesIn(r.f, 30, 74) == 0},
		{"every other minute went", len(r.f.buckets) == 45},
		{"doctor no longer hears that uploads stopped", !strings.Contains(r.doctor(), "stopped uploading")},
		{"the uploader no longer says the roots stop", r.u.Status().Stopped == 0},
	}
	for _, tc := range relinked {
		t.Run("relinked/"+tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Error("no")
			}
		})
	}
}

// TestRelinkLocal is where the chain goes on from after a link, when
// batches were sealed in local-only mode after it: they join the link's
// gap, and the chain goes on from the first batch sealed online after
// them, not from linked_at, and nothing they hold goes (D-local-gap).
func TestRelinkLocal(t *testing.T) {
	type check struct {
		name string
		ok   bool
	}
	report := func(t *testing.T, checks []check) {
		t.Helper()
		for _, c := range checks {
			if !c.ok {
				t.Errorf("no: %s", c.name)
			}
		}
	}
	t.Run("a re-link while config.toml still says local sends nothing sealed before it went online", func(t *testing.T) {
		r := newLiveRig(t, 120)
		r.u.Start()
		r.seal(t, 1)
		r.seal(t, 2)
		waitFor(t, "batches 1 and 2 and their minutes", r.served(func(f *fakeIngest) bool { return f.head != nil && f.head.b == 2 && len(f.buckets) == 30 }))
		// pairtime logout sets mode = "local", and pairtime login couldn't
		// set it online again, so config.toml says local after the link.
		r.local.Store(true)
		r.seal(t, 3)
		r.link(r.sealedAt(3).Add(time.Minute))
		r.seal(t, 4)
		r.seal(t, 5)
		// The teen sets mode = "online" by hand.
		r.local.Store(false)
		r.seal(t, 6)
		r.seal(t, 7)
		r.u.Wake()
		waitFor(t, "batch 7 and its minutes", r.served(func(f *fakeIngest) bool { return f.head.b == 7 && r.minutesIn(f, 90, 104) == 15 }))
		r.drain()
		r.f.mu.Lock()
		defer r.f.mu.Unlock()
		report(t, []check{
			{"no root of a batch sealed while config.toml said local went, nor of the one sealed as it went online", r.f.rows[3].b == 0 && r.f.rows[4].b == 0 && r.f.rows[5].b == 0 && r.f.rows[6].b == 0},
			{"the chain went on from the first batch sealed online after them, over their gap", r.f.rows[7].b == 7 && slices.Contains(r.f.gaps, [2]uint64{3, 6})},
			{"no minute of those batches went", r.minutesIn(r.f, 30, 89) == 0},
			{"doctor doesn't hear that uploads stopped", !strings.Contains(r.doctor(), "stopped uploading")},
		})
	})
	t.Run("a batch sealed in local mode just after a quick re-link joins its gap, and uploads go on", func(t *testing.T) {
		r := newLiveRig(t, 90)
		r.u.Start()
		r.seal(t, 1)
		r.seal(t, 2)
		waitFor(t, "batches 1 and 2 and their minutes", r.served(func(f *fakeIngest) bool { return f.head != nil && f.head.b == 2 && len(f.buckets) == 30 }))
		// pairtime logout and pairtime login within one wake, with no batch
		// sealed between them, and batch 3 sealed before config.toml's
		// change to online reached the agent.
		r.link(r.sealedAt(2).Add(time.Minute))
		r.local.Store(true)
		r.seal(t, 3)
		r.local.Store(false)
		r.seal(t, 4)
		r.seal(t, 5)
		r.u.Wake()
		waitFor(t, "batch 5 and its minutes", r.served(func(f *fakeIngest) bool { return f.head.b == 5 && r.minutesIn(f, 60, 74) == 15 }))
		r.drain()
		r.f.mu.Lock()
		defer r.f.mu.Unlock()
		report(t, []check{
			{"neither the batch sealed in local mode nor the one that ended its run went", r.f.rows[3].b == 0 && r.f.rows[4].b == 0},
			{"the chain went on from the next batch, over their gap", r.f.rows[5].b == 5 && slices.Contains(r.f.gaps, [2]uint64{3, 4})},
			{"no minute of those two batches went", r.minutesIn(r.f, 30, 59) == 0},
			{"doctor doesn't hear that uploads stopped", !strings.Contains(r.doctor(), "stopped uploading")},
			{"the uploader doesn't say the roots stop", r.u.Status().Stopped == 0},
		})
	})
	t.Run("a first sign-in keeps the runs of batches sealed in local-only mode before it", func(t *testing.T) {
		r := newLiveRig(t, 90)
		r.linked.Store(false)
		r.local.Store(true) // a new install's mode
		r.u.Start()
		r.seal(t, 1)
		r.seal(t, 2)
		// pairtime login, which couldn't set config.toml online.
		r.link(r.sealedAt(2).Add(time.Minute))
		r.seal(t, 3)
		r.local.Store(false)
		r.seal(t, 4)
		r.seal(t, 5)
		r.u.Wake()
		waitFor(t, "batch 5 and its minutes", r.served(func(f *fakeIngest) bool { return f.head != nil && f.head.b == 5 && r.minutesIn(f, 60, 74) == 15 }))
		r.drain()
		r.f.mu.Lock()
		defer r.f.mu.Unlock()
		report(t, []check{
			{"the server's chain starts at the first batch sealed online after the sign-in", r.f.start == 5},
			{"no root of a batch sealed before it went", r.f.rows[1].b == 0 && r.f.rows[2].b == 0 && r.f.rows[3].b == 0 && r.f.rows[4].b == 0},
			{"no minute of those batches went", r.minutesIn(r.f, 0, 59) == 0},
		})
	})
}

// TestServerGaps is a device that lost state.json after a re-link: the
// server holds batches 1 to 3 and 6, and recorded 4 and 5 as a local-only
// gap. The gap comes back from the state answer, so the minutes of
// batches 4 and 5 never go, and they don't hold up the minutes after them
// by waiting for receipts that can't come.
func TestServerGaps(t *testing.T) {
	minutes := make([]int, 90)
	for i := range minutes {
		minutes[i] = i
	}
	r := newRig(t, 6, minutes, 80*time.Minute) // the re-link, between batches 5 and 6
	held := []ledger.Row{r.rows[0], r.rows[1], r.rows[2], r.rows[5]}
	hold(t, r.f, held, 1)
	for _, row := range held {
		fr := r.f.rows[row.B]
		sig, _ := hex.DecodeString(fr.sig)
		rc := pkgledger.Receipt{ReceivedAt: fr.receivedAt}
		copy(rc.Signature[:], sig)
		if err := r.w.StoreReceipt(row.B, rc); err != nil {
			t.Fatal(err)
		}
	}
	r.f.gaps = [][2]uint64{{4, 5}}
	r.run(t, "every minute but those of batches 4 and 5", func(f *fakeIngest) bool { return len(f.buckets) == 60 })
	var st saved
	if _, err := r.st.Get(StateKey, &st); err != nil {
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
		{"the server's gap is in the uploader's place again", slices.Contains(st.Gaps, gap{4, 5})},
		{"no minute of the gap went", in(45, 74) == 0},
		{"the minutes before and after it went", in(0, 44) == 45 && in(75, 89) == 15},
		{"no batch root went", func() bool {
			for _, b := range r.f.bodies {
				if bytes.Contains(b, []byte(`"batches"`)) {
					return false
				}
			}
			return true
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
