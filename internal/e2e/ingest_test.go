// SPDX-License-Identifier: AGPL-3.0-or-later

// Package e2e runs the desktop agent's uploader against pairtimed's ingest
// in one process: WP-AG5's check that the client and the server agree.
package e2e

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/state"
	"github.com/some-du6e/pairtime/internal/agent/store"
	"github.com/some-du6e/pairtime/internal/agent/uploader"
	"github.com/some-du6e/pairtime/internal/server/ingest"
	"github.com/some-du6e/pairtime/internal/server/migrations"
	serverstore "github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
	"github.com/some-du6e/pairtime/pkg/event"
	pkgledger "github.com/some-du6e/pairtime/pkg/ledger"
	"github.com/some-du6e/pairtime/pkg/params"
)

const token = "ptd_0123456789abcdefghijABCDEFGHIJxyzw01ab"

func seed(name string) []byte {
	s := sha256.Sum256([]byte(name))
	return s[:]
}

func newestTable() *params.Table {
	tables := params.Tables()
	return tables[len(tables)-1]
}

func bucket(table *params.Table, device string, minute time.Time, credited float64, st event.BucketState) store.Bucket {
	slots := make([]event.BucketSlot, 4)
	left := credited
	for i := range slots {
		cr := min(left, 15)
		left -= cr
		if cr > 0 {
			slots[i] = event.BucketSlot{Project: "p_test", Credited: cr, Why: event.ReasonPrompt, Kind: event.ReasonPrompt.Activity(), Ev: event.EvidenceA}
		}
	}
	return store.Bucket{V: table.Version(), Params: table.Hash(), Minute: minute.UTC(), Device: device, Slots: slots,
		CreditedS: credited, Uncredited: map[event.Reason]float64{event.ReasonNoAnchor: 10}, AgentRunningS: 20, State: st,
		Anchors: []string{event.NewEventID(minute)}, Projects: map[string]store.ProjectMinute{"p_test": {AgentS: 20, AgentOnlyS: 20}}}
}

// faults wraps the ingest handler with what a real network does to some
// POSTs, by attempt from 1: a 503, a 429, an answer lost after the server
// stored the request, and a server down for two tries. It records the
// inflated body of every POST that reaches it.
type faults struct {
	next http.Handler
	mu   sync.Mutex
	by   map[int]string
	n    int
	js   [][]byte
}

func (f *faults) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		f.next.ServeHTTP(w, r)
		return
	}
	raw, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.n++
	fault := f.by[f.n]
	if zr, err := gzip.NewReader(bytes.NewReader(raw)); err == nil {
		if js, err := io.ReadAll(zr); err == nil {
			f.js = append(f.js, js)
		}
	}
	f.mu.Unlock()
	r.Body = io.NopCloser(bytes.NewReader(raw))
	switch fault {
	case "503":
		http.Error(w, `{"error":"unavailable","detail":"injected"}`, http.StatusServiceUnavailable)
	case "429":
		w.Header().Set("Retry-After", "1")
		http.Error(w, `{"error":"too_many_requests","detail":"injected"}`, http.StatusTooManyRequests)
	case "lost":
		f.next.ServeHTTP(httptest.NewRecorder(), r)
		drop(w)
	case "down":
		drop(w)
	default:
		f.next.ServeHTTP(w, r)
	}
}

func drop(w http.ResponseWriter) {
	if hj, ok := w.(http.Hijacker); ok {
		if conn, _, err := hj.Hijack(); err == nil {
			conn.Close()
		}
	}
}

func (f *faults) bodies() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]byte(nil), f.js...)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("gave up waiting for %s", what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// TestUploadToIngest is WP-AG5's client and server in one Linux test: a
// day of minute buckets and the batches beside them go from the uploader
// to pairtimed's ingest handler through a 503, a 429, a lost answer and a
// server that was down, and both sides end with the same totals; privacy
// show prints the first request's body byte for byte; nothing goes out
// before the sign-in.
func TestUploadToIngest(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
	table := newestTable()
	start := time.Now().UTC().Truncate(time.Hour).Add(-30 * time.Hour)

	keys, err := pkgledger.DeriveKeys(seed("e2e K_dev"))
	if err != nil {
		t.Fatal(err)
	}
	var (
		clockMu sync.Mutex
		now     = start
		up      atomic.Pointer[uploader.Uploader]
	)
	w, err := ledger.Open(ledger.Options{
		Dir: dirs.Ledger(), Keys: keys, KeyID: "0123456789abcdef", Params: table,
		Now: func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return now },
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
		now = q
		clockMu.Unlock()
		if _, err := w.Checkpoint("p_test", uint64(q.UnixMilli()), json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := w.Seal(); err != nil {
			t.Fatal(err)
		}
	}
	device := w.DeviceID()
	bs, err := store.OpenBuckets(dirs.Buckets())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bs.Close() })
	for m := 0; m < 24*60; m++ {
		minute := start.Add(time.Duration(m) * time.Minute)
		if m%10 == 0 {
			if _, _, err := bs.Put(bucket(table, device, minute, 15, event.StatePending)); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, err := bs.Put(bucket(table, device, minute, 30, event.StateFinal)); err != nil {
			t.Fatal(err)
		}
	}
	if err := bs.Sync(); err != nil {
		t.Fatal(err)
	}

	db := storetest.Open(t, serverstore.SQLite)
	ms, err := migrations.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(ctx, db, ms, time.Now); err != nil {
		t.Fatal(err)
	}
	wr := writer.Start(db.Write, writer.Options{Window: time.Millisecond})
	t.Cleanup(wr.Close)
	devKey := ed25519.NewKeyFromSeed(seed("e2e device key"))
	serverKey := ed25519.NewKeyFromSeed(seed("e2e server key"))
	svc := ingest.New(ingest.Options{
		Devices: ingest.AuthenticatorFunc(func(_ context.Context, tok string) (ingest.Device, error) {
			if tok != token {
				return ingest.Device{}, ingest.ErrUnknownToken
			}
			return ingest.Device{ID: device, Account: "0190f4b2-0000-7000-8000-000000000001", PublicKey: devKey.Public().(ed25519.PublicKey)}, nil
		}),
		Writer: wr, Read: db.Read, Key: serverKey, Logger: slog.New(slog.DiscardHandler),
	})
	mux := http.NewServeMux()
	svc.Register(mux)
	ft := &faults{next: mux, by: map[int]string{1: "503", 3: "429", 5: "lost", 7: "down", 8: "down"}}
	srv := httptest.NewServer(ft)
	t.Cleanup(srv.Close)

	st, _, err := state.Open(dirs.StateFile())
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := st.Put("agent", map[string]any{"version": "dev", "starts": 1, "started": started.UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	var signedIn atomic.Bool
	var sent atomic.Int64
	creds := uploader.Credentials{Server: srv.URL, Token: token, DeviceID: device, Sign: uploader.KeySigner(devKey), LinkedAt: start}
	client := &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		sent.Add(1)
		return srv.Client().Transport.RoundTrip(r)
	})}
	u, err := uploader.New(uploader.Options{
		Ledger: w, LedgerDir: dirs.Ledger(), BucketDir: dirs.Buckets(), DataDir: dirs.Data, State: st,
		Login:  uploader.LoginFunc(func() (uploader.Credentials, bool) { return creds, signedIn.Load() }),
		Params: table, Started: started, Client: client, Draw: func(int64) int64 { return 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	up.Store(u)
	u.Start()
	t.Cleanup(u.Close)

	count := func(q string) int64 {
		var n int64
		if err := db.Read.QueryRowContext(ctx, q, device).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	steps := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"nothing goes out before the sign-in", func(t *testing.T) {
			for range 3 {
				u.Wake()
				time.Sleep(100 * time.Millisecond)
			}
			if n := sent.Load(); n != 0 {
				t.Errorf("%d requests", n)
			}
		}},
		{"privacy show's upload is the first request's body, byte for byte", func(t *testing.T) {
			p, err := uploader.Next(uploader.PreviewOptions{
				LedgerDir: dirs.Ledger(), BucketDir: dirs.Buckets(), DataDir: dirs.Data, StateFile: dirs.StateFile(),
				Params: table, Credentials: creds,
			})
			if err != nil || p.JSON == nil {
				t.Fatalf("preview %v, %v", p, err)
			}
			signedIn.Store(true)
			u.Wake()
			waitFor(t, "the first POST", func() bool { return len(ft.bodies()) > 0 })
			if got := ft.bodies()[0]; !bytes.Equal(got, p.JSON) {
				t.Errorf("first body differs from privacy show's:\n%s\n%s", got, p.JSON)
			}
		}},
		{"the server ends with the store's totals, every root and every revision", func(t *testing.T) {
			waitFor(t, "every minute and root", func() bool {
				return count(`SELECT COUNT(*) FROM minute_buckets WHERE device_id = $1`) == 24*60 &&
					count(`SELECT COUNT(*) FROM witness_rows WHERE device_id = $1`) == batches
			})
			latest, err := bs.Range(start.UnixMilli(), start.Add(24*time.Hour).UnixMilli())
			if err != nil {
				t.Fatal(err)
			}
			var want, got, agentOnly float64
			for _, b := range latest {
				want += b.CreditedS
			}
			rows, err := db.Read.QueryContext(ctx, `SELECT bucket FROM minute_buckets WHERE device_id = $1`, device)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for rows.Next() {
				var js string
				if err := rows.Scan(&js); err != nil {
					t.Fatal(err)
				}
				var mb event.MinuteBucket
				if err := json.Unmarshal([]byte(js), &mb); err != nil {
					t.Fatal(err)
				}
				got += mb.CreditedS
				agentOnly += mb.AgentOnly["p_test"]
			}
			if got != want {
				t.Errorf("server %v credited seconds, store %v", got, want)
			}
			if agentOnly != 20*24*60 {
				t.Errorf("the server holds %v agent-only seconds, not %v", agentOnly, 20*24*60)
			}
		}},
		{"every receipt in batches.seg checks out under the server key", func(t *testing.T) {
			_, rows, err := ledger.ReadRows(dirs.Ledger(), 1)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range rows {
				if r.Receipt == nil {
					t.Errorf("batch %d has no receipt", r.B)
				} else if err := pkgledger.VerifyReceipt(serverKey.Public().(ed25519.PublicKey), device, r.B, r.Head, *r.Receipt); err != nil {
					t.Errorf("batch %d: %v", r.B, err)
				}
			}
		}},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.run) {
			break
		}
	}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
