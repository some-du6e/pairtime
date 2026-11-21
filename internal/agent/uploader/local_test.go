// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
		gaps                   []gap
		localFrom, from, modes uint64
	}
	tests := []struct {
		name string
		st   saved
		do   func(*saved) bool
		want check
		// changed is what mode reports, for the cases that call it.
		changed bool
	}{
		{"the first batch sealed in local mode opens a run", saved{}, func(s *saved) bool { return s.mode(5, true, false) }, check{localFrom: 5}, true},
		{"more batches sealed in local mode change nothing", saved{LocalFrom: 5}, func(s *saved) bool { return s.mode(6, true, false) }, check{localFrom: 5}, false},
		{"the first batch sealed online again closes the run, itself included", saved{LocalFrom: 5}, func(s *saved) bool { return s.mode(9, false, false) }, check{gaps: []gap{{5, 9}}}, true},
		{"a batch marked as the first after a noted switch back online closes the run before itself", saved{LocalFrom: 5}, func(s *saved) bool { return s.mode(9, false, true) }, check{gaps: []gap{{5, 8}}}, true},
		{"a marked batch just after the run's first batch leaves a run of one", saved{LocalFrom: 5}, func(s *saved) bool { return s.mode(6, false, true) }, check{gaps: []gap{{5, 5}}}, true},
		{"a marked batch with no run open changes nothing", saved{Gaps: []gap{{2, 3}}}, func(s *saved) bool { return s.mode(9, false, true) }, check{gaps: []gap{{2, 3}}}, false},
		{"a batch sealed online with no run open changes nothing", saved{Gaps: []gap{{2, 3}}}, func(s *saved) bool { return s.mode(9, false, false) }, check{gaps: []gap{{2, 3}}}, false},
		{"a run that touches a gap becomes one with it", saved{Gaps: []gap{{1, 4}}, LocalFrom: 5}, func(s *saved) bool { return s.mode(7, false, false) }, check{gaps: []gap{{1, 7}}}, true},
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
		{"the modes of the batches after Modes go in from their rows, in order", saved{}, func(s *saved) bool { return s.foldModes(modeRows("ollo")) }, check{gaps: []gap{{2, 4}}, modes: 4}, true},
		{"a row marked as the first after a noted switch back online ends the run before it", saved{}, func(s *saved) bool { return s.foldModes(modeRows("ollbo")) }, check{gaps: []gap{{2, 3}}, modes: 5}, true},
		{"a row whose seal frame records no mode, as an older version's, opens a run", saved{}, func(s *saved) bool { return s.foldModes(modeRows("on")) }, check{localFrom: 2, modes: 2}, true},
		{"the rows up to Modes are in the runs already", saved{Modes: 2, LocalFrom: 2}, func(s *saved) bool { return s.foldModes(modeRows("oll")) }, check{localFrom: 2, modes: 3}, false},
		{"the rows before the next root went already", saved{Next: 3}, func(s *saved) bool { return s.foldModes(modeRows("llo")) }, check{modes: 3}, false},
		{"a batch sealed in local mode before an open run takes the run down to it", saved{LocalFrom: 10}, func(s *saved) bool { return s.mode(5, true, false) }, check{localFrom: 5}, true},
		{"a batch sealed online before an open run leaves it open", saved{LocalFrom: 10}, func(s *saved) bool { return s.mode(5, false, false) }, check{localFrom: 10}, false},
		{"after an upgrade, an older writer's rows below the run an older place recorded are local-only too", saved{Next: 5, LocalFrom: 10},
			func(s *saved) bool { return s.foldModes(modeRows("oooonnnnnn")) }, check{localFrom: 5, modes: 10}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := tc.st.clone()
			if changed := tc.do(&st); changed != tc.changed {
				t.Errorf("reported a change: %v, want %v", changed, tc.changed)
			}
			if !slices.Equal(st.Gaps, tc.want.gaps) || st.LocalFrom != tc.want.localFrom || st.From != tc.want.from || st.Modes != tc.want.modes {
				t.Errorf("gaps %v, local from %d, from %d, modes %d; want %v, %d, %d, %d", st.Gaps, st.LocalFrom, st.From, st.Modes, tc.want.gaps, tc.want.localFrom, tc.want.from, tc.want.modes)
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

	causes := []struct {
		name  string
		modes string
		stop  uint64
		want  string
	}{
		{"a stop at a batch sealed in local-only mode blames config.toml", "oollo", 3, localOnlyProblem},
		{"a stop at a batch an older version sealed, which recorded no mode, says so", "oonno", 3, noModeProblem},
	}
	for _, tc := range causes {
		t.Run(tc.name, func(t *testing.T) {
			if got := stopProblem(modeRows(tc.modes), tc.stop); got != tc.want {
				t.Errorf("doctor hears %q", got)
			}
		})
	}

	straddle := []struct {
		name string
		// sealed is when each batch was sealed, in minutes after day0.
		sealed []float64
		gaps   []gap
		minute int
		want   bool
	}{
		{"the minute a run's last batch is sealed in holds the run's end", []float64{1, 2, 3.5, 5}, []gap{{3, 3}}, 3, true},
		{"the minute before it doesn't", []float64{1, 2, 3.5, 5}, []gap{{3, 3}}, 2, false},
		{"nor does the minute after it", []float64{1, 2, 3.5, 5}, []gap{{3, 3}}, 4, false},
		{"a minute an earlier batch of the run is sealed in is left to its first batch sealed after it, the run's", []float64{1, 2.5, 3.5, 5}, []gap{{2, 3}}, 2, false},
		{"a minute that ends as the run's last batch is sealed is left to that batch", []float64{1, 2, 3, 5}, []gap{{3, 3}}, 2, false},
		{"the minute that starts then holds nothing of the run", []float64{1, 2, 3, 5}, []gap{{3, 3}}, 3, false},
	}
	for _, tc := range straddle {
		t.Run(tc.name, func(t *testing.T) {
			st := saved{Gaps: tc.gaps}
			if got := st.straddles(indexOf(t, tc.sealed, make([]float64, len(tc.sealed))), at(tc.minute)); got != tc.want {
				t.Errorf("straddles(minute %d) = %v", tc.minute, got)
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

// modeRows is the rows of batches 1 on, a letter of modes each: o sealed
// online, b online and marked as the first after a noted switch back
// online (ledger.Row.Back), l in local-only mode, and n by an older
// version, which recorded no mode (ledger.Row.NoMode).
func modeRows(modes string) *rowIndex {
	ix := &rowIndex{}
	for i, m := range modes {
		r := ledger.Row{BatchRow: pkgledger.BatchRow{B: uint64(i + 1)}, Online: m == 'o' || m == 'b', Back: m == 'b', NoMode: m == 'n'}
		if err := ix.add(r); err != nil {
			panic(err)
		}
	}
	return ix
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
	day := day0.Format(time.DateOnly)
	file, err := os.Stat(filepath.Join(dir, day+".jsonl"))
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
		{"the day's mark moved past every line", p.marks[day].Off == file.Size()},
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
			return slices.Equal(all, []sentBucket{{at(0), 15}, {at(1), 15}}) && in.st.Sent[at(0)].B == 3 && in.st.Marks[day].Off == file.Size()
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

// TestUnseenBatch is a revision whose credit rests on a batch sealed after
// the uploader took its rows, as when the scorer scores a batch between a
// cycle's takeSealed and its plan: the revision waits until the batch's
// row, with the mode its seal frame records, is in, and then goes only if
// the batch was sealed online.
func TestUnseenBatch(t *testing.T) {
	dir := t.TempDir()
	bs, err := store.OpenBuckets(dir)
	if err != nil {
		t.Fatal(err)
	}
	table := newestTable()
	rev := func(credited float64, batch uint64) store.Bucket {
		b := testBucket(table, "d_test", day0, credited, event.StatePending)
		b.Batch = batch
		return b
	}
	for _, b := range []store.Bucket{rev(15, 1), rev(30, 3)} {
		if _, _, err := bs.Put(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := bs.Close(); err != nil {
		t.Fatal(err)
	}
	day := day0.Format(time.DateOnly)
	file, err := os.Stat(filepath.Join(dir, day+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	credits := func(p plan) []float64 {
		var out []float64
		for _, b := range p.body.Buckets {
			out = append(out, b.CreditedS)
		}
		return out
	}
	// Batches 1 and 2 are in the rows, each received at once, and batch 3,
	// whose scoring gave the second revision, isn't yet.
	first, err := makePlan(planInput{rows: indexOf(t, []float64{1, 2}, []float64{1.1, 2.1}), st: saved{Next: 3}, bucketDir: dir, lag: 900_000})
	if err != nil {
		t.Fatal(err)
	}
	// The next plan, with batch 3's row in, sealed in local-only mode or
	// online.
	again := func(gaps []gap) plan {
		t.Helper()
		st := saved{Next: 4, Gaps: gaps, Marks: first.marks, Sent: first.sent}
		p, err := makePlan(planInput{rows: indexOf(t, []float64{1, 2, 3}, []float64{1.1, 2.1, 3.1}), st: st, bucketDir: dir, lag: 900_000})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	local, online := again([]gap{{3, 3}}), again(nil)
	tests := []struct {
		name string
		ok   bool
	}{
		{"the revision on a batch the rows don't hold waits, and the one before it goes", slices.Equal(credits(first), []float64{15}) && first.marks[day].Off < file.Size()},
		{"once the batch's row is in, sealed in local-only mode, the revision is withheld", len(credits(local)) == 0 && local.sent[at(0)].B == 3 && local.marks[day].Off == file.Size()},
		{"once the batch's row is in, sealed online, the revision goes", slices.Equal(credits(online), []float64{30})},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Errorf("no: first %v (mark %d of %d), local %v, online %v", credits(first), first.marks[day].Off, file.Size(), credits(local), credits(online))
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
	dirs  home.Dirs
	keys  *pkgledger.Keys
	w     *ledger.Writer
	f     *fakeIngest
	u     *Uploader
	up    atomic.Pointer[Uploader] // u, for the writer's Sealed
	// client is the way to f. gate, while set, holds the next POST that
	// reaches f, as a slow answer does, until it is opened, and then drops
	// it unanswered.
	client *http.Client
	gate   atomic.Pointer[gate]
	// local is config.toml's mode = "local", which the writer records in
	// each seal frame, and linked whether the computer is linked to the
	// server. As agent.SignedIn has it, nothing goes while either keeps it
	// local-only.
	local, linked atomic.Bool
	// sealing, when set, gets each batch the writer seals before the
	// uploader does, on the goroutine that sealed it.
	sealing func(ledger.Batch)

	clockMu sync.Mutex
	now     time.Time

	mu      sync.Mutex
	creds   Credentials
	problem string
}

// gate holds a POST: arrived closes once it holds one, and opening it lets
// the POST go, dropped.
type gate struct {
	arrived, release chan struct{}
	once             sync.Once
}

func (g *gate) open() { g.once.Do(func() { close(g.release) }) }

// hold sets a gate for the next POST, which the test opens.
func (r *liveRig) hold(t *testing.T) *gate {
	g := &gate{arrived: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(g.open) // before the server closes, which waits for the POST
	r.gate.Store(g)
	return g
}

// newLiveRig makes a liveRig whose bucket store holds minutes 0 to
// minutes-1 after its start, credited 30 s each. Batch k, which seal
// seals at sealedAt(k), holds minutes 15(k-1) to 15k-1.
func newLiveRig(t *testing.T, minutes int) *liveRig {
	t.Helper()
	dir := t.TempDir()
	kDev := sha256.Sum256([]byte("uploader local mode test K_dev"))
	keys, err := pkgledger.DeriveKeys(kDev[:])
	if err != nil {
		t.Fatal(err)
	}
	r := &liveRig{start: time.Now().UTC().Truncate(time.Hour).Add(-3 * time.Hour), dirs: home.Dirs{Data: dir, Config: dir, Runtime: dir}, keys: keys}
	r.now = r.start
	r.linked.Store(true)
	r.openWriter(t, r.dirs)
	bs, err := store.OpenBuckets(r.dirs.Buckets())
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if g := r.gate.Load(); g != nil && req.Method == http.MethodPost && r.gate.CompareAndSwap(g, nil) {
			close(g.arrived)
			<-g.release
			drop(w)
			return
		}
		r.f.ServeHTTP(w, req)
	}))
	t.Cleanup(srv.Close)
	r.client = srv.Client()
	r.creds = Credentials{Server: srv.URL, Token: testToken, DeviceID: r.w.DeviceID(), Sign: KeySigner(devKey), LinkedAt: r.start.Add(-time.Minute)}
	r.openUploader(t, r.dirs)
	return r
}

// openWriter opens the ledger writer in dirs, which records the mode local
// says in each seal frame and hands each batch to the uploader.
func (r *liveRig) openWriter(t *testing.T, dirs home.Dirs) {
	t.Helper()
	w, err := ledger.Open(ledger.Options{
		Dir: dirs.Ledger(), Keys: r.keys, KeyID: "0123456789abcdef", Params: newestTable(),
		Now:    func() time.Time { r.clockMu.Lock(); defer r.clockMu.Unlock(); return r.now },
		Online: func() bool { return !r.local.Load() },
		Sealed: func(b ledger.Batch) {
			if r.sealing != nil {
				r.sealing(b)
			}
			if u := r.up.Load(); u != nil {
				u.Sealed(b)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	r.w = w
}

// openUploader makes the uploader over dirs and the writer, which waits
// for the test to start it.
func (r *liveRig) openUploader(t *testing.T, dirs home.Dirs) {
	t.Helper()
	st, _, err := state.Open(dirs.StateFile())
	if err != nil {
		t.Fatal(err)
	}
	u, err := New(Options{
		Ledger: r.w, LedgerDir: dirs.Ledger(), BucketDir: dirs.Buckets(), DataDir: dirs.Data, State: st,
		Login: LoginFunc(func() (Credentials, bool) {
			r.mu.Lock()
			defer r.mu.Unlock()
			return r.creds, r.linked.Load() && !r.local.Load()
		}),
		Params: newestTable(), Client: r.client, Draw: func(int64) int64 { return 0 },
		Problem: func(msg string) {
			r.mu.Lock()
			r.problem = msg
			r.mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(u.Close)
	r.u = u
	r.up.Store(u)
}

// restart is the agent's next start after a kill -9 that left dir, a copy
// of the data directory: the killed uploader and writer stop, as the kill
// stopped them, and a new writer and uploader, which the test starts, go
// on from dir against the same server with the same sign-in.
func (r *liveRig) restart(t *testing.T, dir string) {
	t.Helper()
	r.up.Store(nil)
	r.u.Close()
	r.w.Close()
	r.sealing = nil
	r.mu.Lock()
	r.problem = ""
	r.mu.Unlock()
	r.dirs = home.Dirs{Data: dir, Config: dir, Runtime: dir}
	r.openWriter(t, r.dirs)
	r.openUploader(t, r.dirs)
}

// sealedAt is when seal seals batch k.
func (r *liveRig) sealedAt(k int) time.Time { return r.start.Add(time.Duration(k) * 15 * time.Minute) }

// seal seals batch k, with one checkpoint in it.
func (r *liveRig) seal(t *testing.T, k int) {
	t.Helper()
	r.record(t, k)
	if _, _, err := r.w.Seal(); err != nil {
		t.Fatal(err)
	}
}

// record writes batch k's checkpoint at sealedAt(k), and seals nothing.
func (r *liveRig) record(t *testing.T, k int) {
	t.Helper()
	q := r.sealedAt(k)
	r.clockMu.Lock()
	r.now = q
	r.clockMu.Unlock()
	if _, err := r.w.Checkpoint("p_test", uint64(q.UnixMilli()), json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
}

// recordAt writes a checkpoint with the clock at at, between the quarter
// hours seal keeps to, and seals nothing.
func (r *liveRig) recordAt(t *testing.T, at time.Time) {
	t.Helper()
	r.setClock(at)
	q := at.Truncate(15 * time.Minute)
	if _, err := r.w.Checkpoint("p_test", uint64(q.UnixMilli()), json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
}

// setClock sets the writer's clock, which a seal stamps its batch with.
func (r *liveRig) setClock(at time.Time) {
	r.clockMu.Lock()
	r.now = at
	r.clockMu.Unlock()
}

// put adds revisions to the bucket store, each resting on the batch its
// Batch names, as the scorer stamps them. The uploader must not be
// started yet.
func (r *liveRig) put(t *testing.T, revs ...store.Bucket) {
	t.Helper()
	bs, err := store.OpenBuckets(r.dirs.Buckets())
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range revs {
		if _, _, err := bs.Put(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := bs.Close(); err != nil {
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

// wait waits until cond holds of the fake server, under its lock, waking
// the uploader meanwhile as its ticker would, and says what the server and
// the uploader hold if it gives up.
func (r *liveRig) wait(t *testing.T, what string, cond func(f *fakeIngest) bool) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		r.u.Wake()
		r.f.mu.Lock()
		ok := cond(r.f)
		head := uint64(0)
		if r.f.head != nil {
			head = r.f.head.b
		}
		held := fmt.Sprintf("the server holds head %d from %d, gaps %v and %d minutes, after %d POSTs and %d state reads",
			head, r.f.start, r.f.gaps, len(r.f.buckets), r.f.posts, r.f.gets)
		r.f.mu.Unlock()
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("gave up waiting for %s: %s; the uploader says %+v, and doctor hears %q", what, held, r.u.Status(), r.doctor())
		}
		time.Sleep(20 * time.Millisecond)
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
	r.wait(t, "batches 1 and 2 and their minutes", func(f *fakeIngest) bool { return f.head != nil && f.head.b == 2 && len(f.buckets) == 30 })
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
	r.wait(t, "batch 6 and its minutes", func(f *fakeIngest) bool { return f.head.b == 6 && r.minutesIn(f, 75, 89) == 15 })
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
		r.wait(t, "batches 1 and 2 and their minutes", func(f *fakeIngest) bool { return f.head != nil && f.head.b == 2 && len(f.buckets) == 30 })
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
		r.wait(t, "batch 7 and its minutes", func(f *fakeIngest) bool { return f.head.b == 7 && r.minutesIn(f, 90, 104) == 15 })
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
		r.wait(t, "batches 1 and 2 and their minutes", func(f *fakeIngest) bool { return f.head != nil && f.head.b == 2 && len(f.buckets) == 30 })
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
		r.wait(t, "batch 5 and its minutes", func(f *fakeIngest) bool { return f.head.b == 5 && r.minutesIn(f, 60, 74) == 15 })
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
		r.wait(t, "batch 5 and its minutes", func(f *fakeIngest) bool { return f.head != nil && f.head.b == 5 && r.minutesIn(f, 60, 74) == 15 })
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

// TestFirstBatchAfterLogin is the first prompt after pairtime login, in
// the batch sealed just after it, when batches were sealed in local-only
// mode before the login: the config watch's note of the switch back online
// seals what is pending then, and marks the next batch as the first whose
// records all came after the note (ledger.Batch.Back), so its root and the
// minutes after the switch back reach the server, and nothing sealed
// before it does. Every batch here is sealed on a quarter hour, so no
// minute holds the switch; TestLoginMinute has one that does.
func TestFirstBatchAfterLogin(t *testing.T) {
	cases := []struct {
		name string
		// before runs what comes before the first prompt after the login,
		// and returns stop, the first batch none of whose roots or minutes
		// may go, and first, the batch of that prompt, which the test seals:
		// the batches from stop to first-1 hold only what came in local-only
		// mode or before the login.
		before func(t *testing.T, r *liveRig) (stop, first int)
		// signedIn is set for a device linked and online from its start,
		// and serverStart is the first batch the server's chain holds, for
		// a first sign-in.
		signedIn    bool
		serverStart uint64
	}{
		{"a first sign-in after batches sealed while config.toml said local, with nothing pending at the login", func(t *testing.T, r *liveRig) (int, int) {
			r.seal(t, 1)
			r.seal(t, 2)
			r.link(r.sealedAt(2).Add(time.Minute))
			r.local.Store(false) // pairtime login sets mode = "online", which the watch notes
			r.w.NoteMode(true)
			return 1, 3
		}, false, 3},
		{"a first sign-in after batches sealed while config.toml said local, with a record pending at the login", func(t *testing.T, r *liveRig) (int, int) {
			r.seal(t, 1)
			r.seal(t, 2)
			r.record(t, 3) // pending as the login finishes
			r.link(r.sealedAt(3).Add(-time.Minute))
			r.local.Store(false) // pairtime login sets mode = "online", which the watch notes
			r.w.NoteMode(true)
			waitFor(t, "the switch back online to seal what was pending", func() bool { return r.w.Status().Batches == 3 })
			return 1, 4
		}, false, 4},
		{"a re-link after a noted stretch of local-only mode, with a record pending at the login", func(t *testing.T, r *liveRig) (int, int) {
			r.seal(t, 1)
			r.seal(t, 2)
			r.wait(t, "batches 1 and 2 and their minutes", func(f *fakeIngest) bool { return f.head != nil && f.head.b == 2 && len(f.buckets) == 30 })
			// pairtime logout sets mode = "local", which the watch notes.
			r.linked.Store(false)
			r.local.Store(true)
			r.w.NoteMode(false)
			r.record(t, 3)
			waitFor(t, "the stretch's first record to seal at once", func() bool { return r.w.Status().Batches == 3 })
			r.record(t, 4) // pending as the login finishes
			r.link(r.sealedAt(4).Add(-time.Minute))
			r.local.Store(false)
			r.w.NoteMode(true)
			waitFor(t, "the switch back online to seal the stretch's last record", func() bool { return r.w.Status().Batches == 4 })
			return 3, 5
		}, true, 0},
		{"a re-link after a noted stretch that left nothing pending at the login", func(t *testing.T, r *liveRig) (int, int) {
			r.seal(t, 1)
			r.seal(t, 2)
			r.wait(t, "batches 1 and 2 and their minutes", func(f *fakeIngest) bool { return f.head != nil && f.head.b == 2 && len(f.buckets) == 30 })
			r.linked.Store(false)
			r.local.Store(true)
			r.w.NoteMode(false)
			r.record(t, 3)
			waitFor(t, "the stretch's first record to seal at once", func() bool { return r.w.Status().Batches == 3 })
			r.link(r.sealedAt(3).Add(time.Minute))
			r.local.Store(false)
			r.w.NoteMode(true)
			return 3, 4
		}, true, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newLiveRig(t, 90)
			if !c.signedIn {
				r.linked.Store(false)
				r.local.Store(true) // a new install's mode
			}
			r.u.Start()
			stop, first := c.before(t, r)
			r.seal(t, first) // the first prompt after the login
			r.u.Wake()
			r.wait(t, fmt.Sprintf("batch %d and its minutes", first), func(f *fakeIngest) bool {
				return f.head != nil && f.head.b == uint64(first) && r.minutesIn(f, 15*(first-1), 15*first-1) == 15
			})
			r.drain()
			r.f.mu.Lock()
			defer r.f.mu.Unlock()
			none := true
			for b := stop; b < first; b++ {
				none = none && r.f.rows[uint64(b)].b == 0
			}
			checks := []struct {
				name string
				ok   bool
			}{
				{"the first prompt's batch went, its root and its minutes", r.f.rows[uint64(first)].b == uint64(first) && r.minutesIn(r.f, 15*(first-1), 15*first-1) == 15},
				{"no root of a batch sealed before it after the run began went", none},
				{"no minute of those batches went", r.minutesIn(r.f, 15*(stop-1), 15*(first-1)-1) == 0},
				{"the server's chain starts where it should", c.serverStart == 0 || r.f.start == c.serverStart},
				{"the run is a gap that ends before the first prompt's batch", c.serverStart != 0 || slices.Contains(r.f.gaps, [2]uint64{uint64(stop), uint64(first - 1)})},
				{"doctor doesn't hear that uploads stopped", !strings.Contains(r.doctor(), "stopped uploading")},
			}
			for _, ch := range checks {
				t.Run(ch.name, func(t *testing.T) {
					if !ch.ok {
						t.Errorf("no: the server holds head %d from %d, gaps %v and %d minutes; the uploader says %+v", r.f.head.b, r.f.start, r.f.gaps, len(r.f.buckets), r.u.Status())
					}
				})
			}
		})
	}
}

// TestLoginMinute is the minute pairtime login lands in, after a re-link's
// stretch of local-only mode: the switch back online seals the stretch's
// last record 20.3 s into the minute, and the first prompt's batch, marked
// as the first after the switch (ledger.Batch.Back), 40 s into it. The
// minute's revisions rest on the batches the scorer stamps them with, the
// marked batch's alone, its credit reaching back into the stretch as a
// first prompt's lead does, or the switch-back batch's and then the marked
// batch's. The minute stays on the device (saved.straddles), and the
// marked batch's root and the minutes after the login's, with their
// credit, reach the server.
func TestLoginMinute(t *testing.T) {
	cases := []struct {
		name string
		// revs are the batches the login minute's revisions rest on, in the
		// order the scorer writes them.
		revs []uint64
	}{
		{"a first revision on the marked batch, its credit reaching back into the stretch", []uint64{5}},
		{"a revision on the switch-back batch, then one on the marked batch", []uint64{4, 5}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newLiveRig(t, 30) // minutes 0 to 29, batches 1 and 2
			login := r.start.Add(50 * time.Minute)
			var revs []store.Bucket
			for i, b := range c.revs {
				st := event.StatePending
				if i == len(c.revs)-1 {
					st = event.StateFinal
				}
				rev := testBucket(newestTable(), r.w.DeviceID(), login, float64(20*(i+1)), st)
				rev.Batch = b
				revs = append(revs, rev)
			}
			// The two minutes after the login's, which batch 6's scoring
			// credits.
			credits := map[int64]float64{login.Add(time.Minute).UnixMilli(): 45, login.Add(2 * time.Minute).UnixMilli(): 30}
			for ms, cr := range credits {
				rev := testBucket(newestTable(), r.w.DeviceID(), time.UnixMilli(ms), cr, event.StateFinal)
				rev.Batch = 6
				revs = append(revs, rev)
			}
			r.put(t, revs...)
			r.u.Start()
			r.seal(t, 1)
			r.seal(t, 2)
			r.wait(t, "batches 1 and 2 and their minutes", func(f *fakeIngest) bool { return f.head != nil && f.head.b == 2 && len(f.buckets) == 30 })
			// pairtime logout sets mode = "local", which the watch notes, and
			// the stretch's first record seals at once as batch 3.
			r.linked.Store(false)
			r.local.Store(true)
			r.w.NoteMode(false)
			r.record(t, 3)
			waitFor(t, "the stretch's first record to seal at once", func() bool { return r.w.Status().Batches == 3 })
			r.recordAt(t, login.Add(10*time.Second)) // pending as the login finishes
			// pairtime login finishes 20 s into the minute, and the watch
			// notes it 300 ms later: batch 4, sealed local-only mid-minute.
			r.link(login.Add(20 * time.Second))
			r.local.Store(false)
			r.setClock(login.Add(20300 * time.Millisecond))
			r.w.NoteMode(true)
			waitFor(t, "the switch back online to seal the stretch's last record", func() bool { return r.w.Status().Batches == 4 })
			// The first prompt, 40 s into the minute: batch 5, marked.
			r.recordAt(t, login.Add(40*time.Second))
			if _, _, err := r.w.Seal(); err != nil {
				t.Fatal(err)
			}
			// Batch 6, sealed after the two minutes after the login's end.
			r.recordAt(t, login.Add(3*time.Minute+5*time.Second))
			if _, _, err := r.w.Seal(); err != nil {
				t.Fatal(err)
			}
			r.u.Wake()
			r.wait(t, "batch 6 and the minutes after the login's", func(f *fakeIngest) bool {
				n := 0
				for ms := range credits {
					if _, ok := f.buckets[ms]; ok {
						n++
					}
				}
				return f.head.b == 6 && n == len(credits)
			})
			r.drain()
			r.f.mu.Lock()
			defer r.f.mu.Unlock()
			_, sent := r.f.buckets[login.UnixMilli()]
			credited := true
			for ms, cr := range credits {
				credited = credited && r.f.buckets[ms].CreditedS == cr
			}
			checks := []struct {
				name string
				ok   bool
			}{
				{"the batch marked as the first after the switch went, over the stretch's gap", r.f.rows[5].b == 5 && r.f.rows[3].b == 0 && r.f.rows[4].b == 0 && slices.Contains(r.f.gaps, [2]uint64{3, 4})},
				{"the minute the switch lands in stays on the device", !sent},
				{"the minutes after it reached the server with their credit", credited},
			}
			for _, ch := range checks {
				t.Run(ch.name, func(t *testing.T) {
					if !ch.ok {
						t.Errorf("no: the server holds head %d, gaps %v and %d minutes", r.f.head.b, r.f.gaps, len(r.f.buckets))
					}
				})
			}
		})
	}
}

// TestHeldRecord is a record made in a re-link's stretch of local-only
// mode that the writer gets only after the switch back online was noted:
// a git change the watcher held past the note, which the git host seals at
// once as a batch of its own, or a hook call parked behind its folder's
// first Track, which lands in the first prompt's batch. Its event came
// before the note, so its batch takes no mark (ledger.Batch.Back) and
// joins the stretch's run: neither its root, which carries its count of
// records, nor the harness it names reaches the server. The next batch
// takes the mark, and goes.
func TestHeldRecord(t *testing.T) {
	cases := []struct {
		name string
		// held is the record, made at made, that the writer gets after the
		// note.
		held func(made time.Time) ledger.Input
		// alone seals it at once in a batch of its own, as the git host
		// does, before the first prompt's batch; otherwise it waits for that
		// batch.
		alone bool
	}{
		{"a git change the watcher held past the note, sealed as a batch of its own", gitChange, true},
		{"a hook call parked behind its folder's first Track, sealed with the first prompt", hookCall, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newLiveRig(t, 30)
			r.u.Start()
			r.seal(t, 1)
			r.seal(t, 2)
			r.wait(t, "batches 1 and 2 and their minutes", func(f *fakeIngest) bool { return f.head != nil && f.head.b == 2 && len(f.buckets) == 30 })
			// pairtime logout sets mode = "local", which the watch notes, and
			// the stretch's first record seals at once as batch 3.
			r.linked.Store(false)
			r.local.Store(true)
			r.w.NoteMode(false)
			r.record(t, 3)
			waitFor(t, "the stretch's first record to seal at once", func() bool { return r.w.Status().Batches == 3 })
			made := r.sealedAt(3).Add(2 * time.Minute) // in the stretch
			login := made.Add(time.Minute)
			// pairtime login, which the watch notes 300 ms later, with nothing
			// pending.
			r.link(login)
			r.local.Store(false)
			r.setClock(login.Add(300 * time.Millisecond))
			r.w.NoteMode(true)
			// The held record reaches the writer 3 s after the note: batch 4,
			// alone or with the first prompt's record.
			r.setClock(login.Add(3300 * time.Millisecond))
			if _, err := r.w.Write(c.held(made)); err != nil {
				t.Fatal(err)
			}
			if c.alone {
				if _, _, err := r.w.Seal(); err != nil {
					t.Fatal(err)
				}
			}
			r.recordAt(t, login.Add(40*time.Second)) // the first prompt
			if _, _, err := r.w.Seal(); err != nil {
				t.Fatal(err)
			}
			r.recordAt(t, login.Add(2*time.Minute))
			if _, _, err := r.w.Seal(); err != nil {
				t.Fatal(err)
			}
			last := r.w.Status().Batches
			_, rows, err := ledger.ReadRows(r.dirs.Ledger(), 4)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) == 0 || rows[0].Back || !rows[0].Online || !rows[1].Back {
				t.Fatalf("the held record's batch and the next are sealed %+v, want the held one online and unmarked, and the next marked", rows)
			}
			held := rows[0].Root.String()
			r.u.Wake()
			r.wait(t, fmt.Sprintf("batch %d", last), func(f *fakeIngest) bool { return f.head.b == last })
			r.drain()
			r.f.mu.Lock()
			defer r.f.mu.Unlock()
			sent := slices.ContainsFunc(r.f.bodies, func(b []byte) bool { return bytes.Contains(b, []byte(held)) })
			named := slices.ContainsFunc(r.f.bodies, func(b []byte) bool { return bytes.Contains(b, []byte(`"`+string(event.HarnessClaudeCode)+`"`)) })
			checks := []struct {
				name string
				ok   bool
			}{
				{"the held record's batch joined the stretch's run, and its root and count of records never went", r.f.rows[4].b == 0 && !sent && slices.Contains(r.f.gaps, [2]uint64{3, 4})},
				{"the labels never named the harness its record names", !named},
				{"the batch after it took the mark and went, and so did the rest", r.f.rows[5].b == 5 && r.f.head.b == last},
			}
			for _, ch := range checks {
				t.Run(ch.name, func(t *testing.T) {
					if !ch.ok {
						t.Errorf("no: the server holds head %d and gaps %v, after %d POSTs", r.f.head.b, r.f.gaps, r.f.posts)
					}
				})
			}
		})
	}
}

// gitChange is a commit made at made, as the git watcher's sink writes one.
func gitChange(made time.Time) ledger.Input {
	return ledger.Input{Event: event.Event{
		Envelope: event.Envelope{EventID: event.NewEventID(made), T: made.UnixMilli(), Mono: 1, BootID: "b_test",
			Source: event.SourceGit, Via: event.ViaGit, Harness: event.HarnessNone, SessionKey: event.NoSession, ProjectKey: "p_test"},
		Data: &event.GitChange{Op: event.GitCommit, SHA: "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b", FilesChanged: 3,
			Insertions: 40, Deletions: 12, Actor: event.ActorHuman},
	}}
}

// hookCall is Claude Code's session.end made at made, as the adapters
// write a hook call once its folder's first Track lets it go.
func hookCall(made time.Time) ledger.Input {
	return ledger.Input{Event: event.Event{
		Envelope: event.Envelope{EventID: event.NewEventID(made), T: made.UnixMilli(), Mono: 1, BootID: "b_test",
			Source: event.SourceHook, Via: event.ViaHook, Harness: event.HarnessClaudeCode, HarnessVersion: "2.1.327",
			SessionKey: "s_test", ProjectKey: "p_test"},
		Data: &event.SessionEnd{Reason: "prompt_input_exit"},
	}}
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

// TestTakeSealed is what the loop takes of the batches Sealed queued while
// a cycle ran: their rows with the modes and marks their seal frames
// record, before anything is planned, and the harnesses of the batches not
// sealed in local-only mode, by their own mode as well as the runs.
func TestTakeSealed(t *testing.T) {
	rtype, ok := pkgledger.RTypeOf(event.KindAgentTool)
	if !ok {
		t.Fatal("agent.tool has no record type")
	}
	// batch is batch b, sealed online or not, with one record that names
	// harness h at version v.
	batch := func(t *testing.T, b uint64, online bool, h event.Harness, v string) ledger.Batch {
		t.Helper()
		hdr, err := pkgledger.NewHeader(b, rtype, "p_test", "s_test")
		if err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf(`{"e":{"harness":%q,"harness_version":%q}}`, h, v)
		return ledger.Batch{
			Row:      pkgledger.BatchRow{B: b, FirstSeq: b, N: 1},
			SealedAt: day0.Add(time.Duration(b) * time.Minute),
			Online:   online,
			Records:  []pkgledger.Record{{Header: hdr, Body: []byte(body)}},
		}
	}
	// newUploader is an uploader whose place is as a cycle's load leaves
	// it, linked before batch 1.
	newUploader := func(t *testing.T) *Uploader {
		t.Helper()
		u := bareUploader(t, nil)
		u.st, u.rows, u.loaded = saved{Chain: u.opts.Ledger.DeviceID(), From: 1}, &rowIndex{device: u.opts.Ledger.DeviceID()}, true
		return u
	}
	t.Run("a batch sealed in local mode during a cycle has its mode in the place before it is planned", func(t *testing.T) {
		u := newUploader(t)
		u.Sealed(batch(t, 1, false, event.HarnessCodex, "0.9.1"))
		u.takeSealed()
		if roots, _ := rootsOf(planInput{rows: u.rows, st: u.st}, 10); u.st.LocalFrom != 1 || len(roots) != 0 {
			t.Errorf("local from %d, and the roots %v go", u.st.LocalFrom, roots)
		}
	})
	t.Run("a harness named only in batches sealed in local-only mode stays out of the labels, and one named online goes", func(t *testing.T) {
		u := newUploader(t)
		u.Sealed(batch(t, 1, false, event.HarnessDevin, "1.0.0"))
		// Sealed online, but its records began in local mode: it ends the
		// run, and is in it.
		u.Sealed(batch(t, 2, true, event.HarnessCodex, "0.9.1"))
		u.Sealed(batch(t, 3, true, event.HarnessClaudeCode, "2.1.327"))
		u.takeSealed()
		if want := map[event.Harness]string{event.HarnessClaudeCode: "2.1.327"}; !maps.Equal(u.st.Harnesses, want) {
			t.Errorf("the labels' harnesses are %v, want %v", u.st.Harnesses, want)
		}
	})
	t.Run("a batch marked as the first after a noted switch back online ends the run before it, and its harness goes", func(t *testing.T) {
		u := newUploader(t)
		u.Sealed(batch(t, 1, false, event.HarnessDevin, "1.0.0"))
		marked := batch(t, 2, true, event.HarnessCodex, "0.9.1")
		marked.Back = true
		u.Sealed(marked)
		u.takeSealed()
		if want := map[event.Harness]string{event.HarnessCodex: "0.9.1"}; !maps.Equal(u.st.Harnesses, want) || !slices.Equal(u.st.Gaps, []gap{{1, 1}}) || u.st.LocalFrom != 0 {
			t.Errorf("the labels' harnesses are %v, want %v, with gaps %v and local from %d", u.st.Harnesses, want, u.st.Gaps, u.st.LocalFrom)
		}
	})
	t.Run("a batch sealed in local-only mode whose row the rows don't hold keeps its harness out of the labels", func(t *testing.T) {
		u := newUploader(t)
		// Batch 3 doesn't follow the rows, and batches.seg, read again,
		// holds batch 1 alone, as when a read fails: its row isn't in the
		// rows, so its mode never reaches the runs.
		u.Sealed(batch(t, 3, false, event.HarnessDevin, "1.0.0"))
		u.takeSealed()
		if len(u.st.Harnesses) != 0 || u.st.LocalFrom != 0 {
			t.Errorf("the labels' harnesses are %v, with local from %d", u.st.Harnesses, u.st.LocalFrom)
		}
	})
}

// bareUploader is an uploader over a ledger of one batch and a state.json
// of its own, with no server, that isn't started; edit sets its options.
func bareUploader(t *testing.T, edit func(*Options)) *Uploader {
	t.Helper()
	dir := t.TempDir()
	dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
	w, _ := smallChain(t, dirs, 1)
	st, _, err := state.Open(dirs.StateFile())
	if err != nil {
		t.Fatal(err)
	}
	o := Options{Ledger: w, LedgerDir: dirs.Ledger(), BucketDir: dirs.Buckets(), DataDir: dirs.Data, State: st, Params: newestTable()}
	if edit != nil {
		edit(&o)
	}
	u, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(u.Close)
	return u
}
