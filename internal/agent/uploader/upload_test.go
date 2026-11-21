// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
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
	"github.com/some-du6e/pairtime/pkg/params"
)

const testToken = "ptd_0123456789abcdefghijABCDEFGHIJxyzw01ab"

func newestTable() *params.Table {
	tables := params.Tables()
	return tables[len(tables)-1]
}

// testBucket is a valid minute bucket of the scorer's store: credited
// seconds in slots of up to 15, 10 seconds left out, 20 of agent time that
// p_test wasn't credited, and an anchor, which stays on the device.
func testBucket(table *params.Table, device string, minute time.Time, credited float64, st event.BucketState) store.Bucket {
	slots := make([]event.BucketSlot, 4)
	left := credited
	for i := range slots {
		cr := min(left, 15)
		left -= cr
		if cr > 0 {
			slots[i] = event.BucketSlot{Project: "p_test", Credited: cr, Why: event.ReasonPrompt,
				Kind: event.ReasonPrompt.Activity(), Ev: event.EvidenceA}
		}
	}
	return store.Bucket{
		V: table.Version(), Params: table.Hash(), Minute: minute.UTC(), Device: device, Slots: slots,
		CreditedS: credited, Uncredited: map[event.Reason]float64{event.ReasonNoAnchor: 10},
		AgentRunningS: 20, State: st,
		// Anchors stay on the device, and agent_only goes (D-bucket-agent-only).
		Anchors:  []string{event.NewEventID(minute)},
		Projects: map[string]store.ProjectMinute{"p_test": {AgentS: 20, AgentOnlyS: 20}},
	}
}

// countingTransport counts the requests that leave the uploader.
type countingTransport struct {
	base http.RoundTripper
	n    *atomic.Int64
}

func (c countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.n.Add(1)
	return c.base.RoundTrip(r)
}

// waitFor polls cond until it holds or a minute passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("gave up waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestUploadDay is WP-AG5's client and server in one Linux test, against
// fakeIngest until WP-SV4's handler is on main: a day of minute buckets
// and the batches beside them go up through a 503, a 429, an answer the
// server lost and a server that was down, and both sides end with the
// same totals; `pairtime privacy show` prints each next request's body
// byte for byte; and nothing goes out while the device isn't signed in.
func TestUploadDay(t *testing.T) {
	dir := t.TempDir()
	dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
	table := newestTable()
	start := time.Now().UTC().Truncate(time.Hour).Add(-30 * time.Hour)
	end := start.Add(24 * time.Hour)

	// The ledger: a batch every 15 minutes of the day, each one
	// checkpoint, sealed at its quarter hour.
	kDev := sha256.Sum256([]byte("uploader test K_dev"))
	keys, err := pkgledger.DeriveKeys(kDev[:])
	if err != nil {
		t.Fatal(err)
	}
	var (
		clockMu   sync.Mutex
		ledgerNow = start
		up        atomic.Pointer[Uploader]
	)
	w, err := ledger.Open(ledger.Options{
		Dir: dirs.Ledger(), Keys: keys, KeyID: "0123456789abcdef", Params: table,
		Now: func() time.Time {
			clockMu.Lock()
			defer clockMu.Unlock()
			return ledgerNow
		},
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
	const batches = 96
	for k := 1; k <= batches; k++ {
		q := start.Add(time.Duration(k) * 15 * time.Minute)
		clockMu.Lock()
		ledgerNow = q
		clockMu.Unlock()
		if _, err := w.Checkpoint("p_test", uint64(q.UnixMilli()), json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := w.Seal(); err != nil || !ok {
			t.Fatalf("seal %d: %v, %v", k, ok, err)
		}
	}
	device := w.DeviceID()

	// The scorer's store: every minute of the day, and every tenth minute
	// pending first and final after, so it has two revisions.
	bs, err := store.OpenBuckets(dirs.Buckets())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bs.Close() })
	lines := 0
	for m := 0; m < 24*60; m++ {
		minute := start.Add(time.Duration(m) * time.Minute)
		if m%10 == 0 {
			if _, _, err := bs.Put(testBucket(table, device, minute, 15, event.StatePending)); err != nil {
				t.Fatal(err)
			}
			lines++
		}
		if _, _, err := bs.Put(testBucket(table, device, minute, 30, event.StateFinal)); err != nil {
			t.Fatal(err)
		}
		lines++
	}
	if err := bs.Sync(); err != nil {
		t.Fatal(err)
	}

	// The server, with its faults by POST attempt: a 503, a 429, an answer
	// lost after the server stored it, and two tries at a server that is
	// down, so the transport's own retry of the first fails too.
	devKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, 32))
	fake := newFakeIngest(device, testToken, devKey.Public().(ed25519.PublicKey))
	fake.faults = map[int]string{1: "503", 3: "429", 5: "lost", 7: "down", 8: "down"}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	var sent atomic.Int64
	client := &http.Client{Transport: countingTransport{base: srv.Client().Transport, n: &sent}}

	st, _, err := state.Open(dirs.StateFile())
	if err != nil {
		t.Fatal(err)
	}
	// What the agent records when it starts (agent.markStarted): its first
	// request carries the labels.
	started := time.Now()
	if err := st.Put("agent", map[string]any{"version": "dev", "starts": 1, "started": started.UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	var signedIn atomic.Bool
	creds := Credentials{Server: srv.URL, Token: testToken, DeviceID: device, Sign: KeySigner(devKey), LinkedAt: start}
	u, err := New(Options{
		Ledger: w, LedgerDir: dirs.Ledger(), BucketDir: dirs.Buckets(), DataDir: dirs.Data, State: st,
		Login:  LoginFunc(func() (Credentials, bool) { return creds, signedIn.Load() }),
		Params: table, Started: started, Client: client,
		Draw: func(int64) int64 { return 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	u.maxRoots, u.maxBuckets = 30, 200
	u.pace = newPacer(1000, time.Millisecond) // the day goes in far more requests than one pace allows at once
	up.Store(u)
	u.Start()
	t.Cleanup(u.Close)

	preview := func(t *testing.T) Preview {
		t.Helper()
		p, err := Next(PreviewOptions{
			LedgerDir: dirs.Ledger(), BucketDir: dirs.Buckets(), DataDir: dirs.Data, StateFile: dirs.StateFile(),
			Params: table, Credentials: creds, maxRoots: 30, maxBuckets: 200,
		})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}

	var first Preview
	steps := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"nothing goes out while the device isn't signed in", func(t *testing.T) {
			for range 3 {
				u.Wake()
				time.Sleep(100 * time.Millisecond)
			}
			if n := sent.Load(); n != 0 {
				t.Errorf("%d requests went out without a sign-in", n)
			}
		}},
		{"privacy show's first upload is the first request's body, byte for byte", func(t *testing.T) {
			first = preview(t)
			// The first root under a new sign-in goes alone, and the rest
			// once it is stored (ingest-v1 5.3).
			if first.JSON == nil || first.Batches != 1 || first.Buckets != 0 || !first.Labels {
				t.Fatalf("the preview holds %d roots, %d buckets, labels %v", first.Batches, first.Buckets, first.Labels)
			}
			signedIn.Store(true)
			u.Wake()
			waitFor(t, "the first POST", func() bool { _, bodies, _ := fake.snapshot(); return len(bodies) > 0 })
			if _, bodies, _ := fake.snapshot(); !bytes.Equal(bodies[0], first.JSON) {
				t.Errorf("the first request's body differs from privacy show's:\n%s\n%s", bodies[0], first.JSON)
			}
		}},
		{"a day of buckets reaches the server with the store's totals, through a 503, a 429, a lost answer and a restart", func(t *testing.T) {
			waitFor(t, "every root and every minute", func() bool {
				u.Wake() // as the ticker would
				minutes, _, _ := fake.totals()
				_, _, head := fake.snapshot()
				return minutes == 24*60 && head == batches
			})
			latest, err := bs.Range(start.UnixMilli(), end.UnixMilli())
			if err != nil {
				t.Fatal(err)
			}
			var credited, agent float64
			for _, b := range latest {
				credited += b.CreditedS
				agent += b.AgentRunningS
			}
			minutes, sc, sa := fake.totals()
			if minutes != len(latest) || sc != credited || sa != agent {
				t.Errorf("server %d minutes, %v credited, %v agent; store %d, %v, %v", minutes, sc, sa, len(latest), credited, agent)
			}
			fake.mu.Lock()
			revs, replays, labels := fake.revs, fake.replays, fake.labels
			fake.mu.Unlock()
			if revs != lines {
				t.Errorf("the server took %d revisions, and the store holds %d lines", revs, lines)
			}
			if replays == 0 {
				t.Error("the answer the server lost wasn't fetched again with the same Idempotency-Key")
			}
			if labels != 1 {
				t.Errorf("the labels went %d times", labels)
			}
			// Nothing tried to send the day on time, and the failures of the
			// catch-up after don't change that: every minute reads
			// backfilled (DESIGN 7.4).
			fake.mu.Lock()
			witness := maps.Clone(fake.witness)
			fake.mu.Unlock()
			if witness[event.Backfilled] != lines || len(witness) != 1 {
				t.Errorf("the server took the readings %v, not %d backfilled", witness, lines)
			}
			// The failure notes aren't sealed yet, so once the uploader has
			// saved its place nothing is left to send.
			waitFor(t, "the uploader's place on disk", func() bool { return preview(t).JSON == nil })
		}},
		{"every receipt is in batches.seg and checks out under the server key", func(t *testing.T) {
			_, rows, err := ledger.ReadRows(dirs.Ledger(), 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != batches {
				t.Fatalf("%d rows", len(rows))
			}
			pub := fake.key.Public().(ed25519.PublicKey)
			for _, r := range rows {
				if r.Receipt == nil {
					t.Errorf("batch %d has no receipt", r.B)
					continue
				}
				if err := pkgledger.VerifyReceipt(pub, device, r.B, r.Head, *r.Receipt); err != nil {
					t.Errorf("batch %d: %v", r.B, err)
				}
			}
		}},
		{"each failure that reached the uploader left an upload-failure note", func(t *testing.T) {
			if _, _, err := w.Seal(); err != nil {
				t.Fatal(err)
			}
			var reasons []string
			err := ledger.ReadBatches(dirs.Ledger(), batches+1, func(b ledger.Batch) error {
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
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"http_5xx", "http_429", "connect"} {
				if !slices.Contains(reasons, want) {
					t.Errorf("no %s note among %q", want, reasons)
				}
			}
		}},
		{"privacy show's next upload is the next request's body, once more is sealed", func(t *testing.T) {
			next := preview(t)
			if next.JSON == nil || next.Batches != 1 || next.Buckets != 0 || next.Labels {
				t.Fatalf("the preview holds %d roots, %d buckets, labels %v", next.Batches, next.Buckets, next.Labels)
			}
			_, before, _ := fake.snapshot()
			u.Wake()
			waitFor(t, "the next POST", func() bool { _, bodies, _ := fake.snapshot(); return len(bodies) > len(before) })
			_, bodies, _ := fake.snapshot()
			if got := bodies[len(before)]; !bytes.Equal(got, next.JSON) {
				t.Errorf("the request's body differs from privacy show's:\n%s\n%s", got, next.JSON)
			}
		}},
		{"with everything sent, privacy show has nothing to print", func(t *testing.T) {
			waitFor(t, "the notes' batch", func() bool { _, _, head := fake.snapshot(); return head == batches+1 })
			waitFor(t, "the uploader's place on disk", func() bool { return preview(t).JSON == nil })
		}},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.run) {
			break
		}
	}
}
