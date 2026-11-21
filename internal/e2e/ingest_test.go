// SPDX-License-Identifier: AGPL-3.0-or-later

// Package e2e runs the desktop agent's uploader against pairtimed's ingest
// in one process, with the server's own device store, rate limiter and
// rollups: WP-AG5's check that the client and the server agree.
package e2e

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
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
	"github.com/some-du6e/pairtime/internal/agent/uploader"
	"github.com/some-du6e/pairtime/internal/devtoken"
	"github.com/some-du6e/pairtime/internal/server/device"
	"github.com/some-du6e/pairtime/internal/server/ingest"
	"github.com/some-du6e/pairtime/internal/server/migrations"
	"github.com/some-du6e/pairtime/internal/server/rollup"
	serverstore "github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
	"github.com/some-du6e/pairtime/pkg/event"
	pkgledger "github.com/some-du6e/pairtime/pkg/ledger"
	"github.com/some-du6e/pairtime/pkg/params"
	"github.com/some-du6e/pairtime/pkg/score"
)

// account is the device owner's account id.
const account = "0190f4b2-0000-7000-8000-000000000001"

func seed(name string) []byte {
	s := sha256.Sum256([]byte(name))
	return s[:]
}

func newestTable() *params.Table {
	tables := params.Tables()
	return tables[len(tables)-1]
}

// bucket is a minute bucket of the scorer's store: credited seconds in
// slots of up to 15, 10 seconds left out, 20 of agent time that p_test
// wasn't credited, and an anchor, which stays on the device.
func bucket(table *params.Table, deviceID string, minute time.Time, credited float64, st event.BucketState) store.Bucket {
	slots := make([]event.BucketSlot, 4)
	left := credited
	for i := range slots {
		cr := min(left, 15)
		left -= cr
		if cr > 0 {
			slots[i] = event.BucketSlot{Project: "p_test", Credited: cr, Why: event.ReasonPrompt, Kind: event.ReasonPrompt.Activity(), Ev: event.EvidenceA}
		}
	}
	return store.Bucket{V: score.Version(), Params: table.Hash(), Minute: minute.UTC(), Device: deviceID, Slots: slots,
		CreditedS: credited, Uncredited: map[event.Reason]float64{event.ReasonNoAnchor: 10}, AgentRunningS: 20, State: st,
		Anchors: []string{event.NewEventID(minute)}, Projects: map[string]store.ProjectMinute{"p_test": {AgentS: 20, AgentOnlyS: 20}}}
}

// server is pairtimed's ingest over one database, as cmd/pairtimed wires
// it: WP-SV3's device store finds the device and keeps its chain on its
// row, and every bucket revision moves the account's rollups. restart
// replaces the service with a new one over the same database, as a
// pairtimed restart does, whose rate limiter starts afresh.
type server struct {
	db      *serverstore.DB
	w       *writer.Writer
	devices *device.Store
	key     ed25519.PrivateKey
	mux     atomic.Pointer[http.ServeMux]
}

func (s *server) restart() {
	svc := ingest.New(ingest.Options{
		Devices: s.devices, Writer: s.w, Read: s.db.Read, Key: s.key,
		Rollup: rollup.ApplyEvent, Logger: slog.New(slog.DiscardHandler),
	})
	mux := http.NewServeMux()
	svc.Register(mux)
	s.mux.Store(mux)
}

// faults is the way to the server, with what a network and a restart do
// to some POSTs, by attempt from 1: a 503; a restart, when pairtimed went
// down before it read the request and came back; down, a connection
// dropped as pairtimed starts; a 429; and an answer lost after the server
// stored the request. net/http sends a POST with an Idempotency-Key again
// once by itself when a reused connection drops, so a restart and a lost
// answer are each followed by down, and a lost answer also closes the
// client's idle connections, so the transport's own resend dials afresh
// and isn't sent again: the uploader sees the failure, and its own retry
// is what resends the request (go-idempotency-key-retry). It keeps the
// inflated body of every POST that reaches it, and counts the server's own
// 429s and its replays, with the Idempotency-Keys of the lost answer and
// of the replays.
type faults struct {
	srv *server
	// closeIdle closes the idle connections of the uploader's client.
	closeIdle   func()
	mu          sync.Mutex
	by          map[int]string
	n           int
	js          [][]byte
	rateLimited int
	replays     int
	lost        string   // the Idempotency-Key of the request whose answer was lost
	replayed    []string // the Idempotency-Keys the server answered as replays
}

func (f *faults) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	next := f.srv.mux.Load()
	if r.Method != http.MethodPost {
		f.serve(next, w, r)
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
		http.Error(w, `{"error":"rate_limited","detail":"injected"}`, http.StatusTooManyRequests)
	case "restart":
		f.srv.restart()
		drop(w)
	case "down":
		drop(w)
	case "lost":
		next.ServeHTTP(httptest.NewRecorder(), r)
		f.mu.Lock()
		f.lost = r.Header.Get("Idempotency-Key")
		f.mu.Unlock()
		f.closeIdle()
		drop(w)
	default:
		f.serve(next, w, r)
	}
}

// serve passes r to the server, and notes a 429 or a replay in its answer.
func (f *faults) serve(next http.Handler, w http.ResponseWriter, r *http.Request) {
	rec := &recorder{ResponseWriter: w}
	next.ServeHTTP(rec, r)
	f.mu.Lock()
	defer f.mu.Unlock()
	if rec.status == http.StatusTooManyRequests {
		f.rateLimited++
	}
	if w.Header().Get(ingest.HeaderReplay) == "true" {
		f.replays++
		f.replayed = append(f.replayed, r.Header.Get("Idempotency-Key"))
	}
}

// recorder notes the status the server wrote.
type recorder struct {
	http.ResponseWriter
	status int
}

func (r *recorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// drop closes the connection without an answer, as a server that stops
// does.
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
	deadline := time.Now().Add(3 * time.Minute)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("gave up waiting for %s", what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// stop is the agent.stop hook record of a turn at at, which the ledger
// writer seals at once, as it does each prompt's and stop's.
func stop(at time.Time, mono int64) ledger.Input {
	return ledger.Input{Event: event.Event{
		Envelope: event.Envelope{EventID: event.NewEventID(at), T: at.UnixMilli(), Mono: mono, BootID: "b_e2e",
			Source: event.SourceHook, Via: event.ViaHook, Harness: event.HarnessClaudeCode, HarnessVersion: "2.1.327",
			SessionKey: "s_busy", ProjectKey: "p_test"},
		Data: &event.AgentStop{TurnKey: "t_1", StopReason: event.StopEndTurn},
	}}
}

// TestUploadToIngest is WP-AG5's client and server in one Linux test
// (#150 R4): a day of minute buckets and the batches beside them go from
// the uploader to pairtimed's ingest handler through a 503, a server
// restart, a 429 and a lost answer, which the uploader's own retry fetches
// again, under the server's own rate limiter, and both sides end with the
// same totals and DESIGN 7.4's readings; a busy session after it, with no
// restart, stays under that limiter; privacy show prints the first
// request's body byte for byte; nothing goes out before the sign-in.
func TestUploadToIngest(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
	table := newestTable()
	start := time.Now().UTC().Truncate(time.Hour).Add(-30 * time.Hour)

	// The ledger: a batch every 15 minutes of the day, each one
	// checkpoint, sealed at its quarter hour.
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
	deviceID := w.DeviceID()

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
			if _, _, err := bs.Put(bucket(table, deviceID, minute, 15, event.StatePending)); err != nil {
				t.Fatal(err)
			}
			lines++
		}
		if _, _, err := bs.Put(bucket(table, deviceID, minute, 30, event.StateFinal)); err != nil {
			t.Fatal(err)
		}
		lines++
	}
	if err := bs.Sync(); err != nil {
		t.Fatal(err)
	}

	// The server, and the rows WP-SV3's device grant writes for a linked
	// device: its account, and the device at link generation 1 with its
	// token's hash and its key, and no ingest state yet.
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
	ds, err := device.New(device.Options{DB: db, Writer: wr})
	if err != nil {
		t.Fatal(err)
	}
	devKey := ed25519.NewKeyFromSeed(seed("e2e device key"))
	token := devtoken.New()
	exec := func(query string, args ...any) {
		t.Helper()
		if err := wr.Do(ctx, 1, func(ctx context.Context, tx writer.Tx) error {
			_, err := tx.ExecContext(ctx, query, args...)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	linked := time.Now().UnixMilli()
	exec(`INSERT INTO users (id, hca_sub, verification_status, created_at_ms, updated_at_ms, signed_in_at_ms) VALUES ($1, $2, 'verified', $3, $3, $3)`,
		account, "hca-e2e", linked)
	tokenHash := sha256.Sum256([]byte(token))
	exec(`INSERT INTO devices (device_id, user_id, token_hash, public_key, os_family, client_build, linked_at_ms, last_seen_at_ms, link_generation)
VALUES ($1, $2, $3, $4, 'linux', 'v0.0.0-dev', $5, $5, 1)`,
		deviceID, account, hex.EncodeToString(tokenHash[:]), []byte(devKey.Public().(ed25519.PublicKey)), linked)
	sv := &server{db: db, w: wr, devices: ds, key: ed25519.NewKeyFromSeed(seed("e2e server key"))}
	sv.restart()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	t.Cleanup(transport.CloseIdleConnections)
	ft := &faults{srv: sv, closeIdle: transport.CloseIdleConnections,
		by: map[int]string{1: "503", 2: "restart", 3: "down", 5: "429", 6: "lost", 7: "down"}}
	srv := httptest.NewServer(ft)
	t.Cleanup(srv.Close)

	// The agent, which records its start in state.json: its first request
	// carries the labels.
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
	var (
		signedIn atomic.Bool
		sent     atomic.Int64
		keysMu   sync.Mutex
		keys     []string // the Idempotency-Key of each POST the uploader sent, a retry of its own included
	)
	creds := uploader.Credentials{Server: srv.URL, Token: token, DeviceID: deviceID, Sign: uploader.KeySigner(devKey), LinkedAt: start}
	client := &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		sent.Add(1)
		if r.Method == http.MethodPost {
			keysMu.Lock()
			keys = append(keys, r.Header.Get("Idempotency-Key"))
			keysMu.Unlock()
		}
		return transport.RoundTrip(r)
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

	count := func(query string) int64 {
		var n int64
		if err := db.Read.QueryRowContext(ctx, query, deviceID).Scan(&n); err != nil {
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
				t.Errorf("%d requests went out without a sign-in", n)
			}
		}},
		{"privacy show's upload is the first request's body, byte for byte", func(t *testing.T) {
			p, err := uploader.Next(uploader.PreviewOptions{
				LedgerDir: dirs.Ledger(), BucketDir: dirs.Buckets(), DataDir: dirs.Data, StateFile: dirs.StateFile(),
				Params: table, Credentials: creds,
			})
			if err != nil || p.JSON == nil {
				t.Fatalf("preview %+v, %v", p, err)
			}
			signedIn.Store(true)
			u.Wake()
			waitFor(t, "the first POST", func() bool { return len(ft.bodies()) > 0 })
			if got := ft.bodies()[0]; !bytes.Equal(got, p.JSON) {
				t.Errorf("the first request's body differs from privacy show's:\n%s\n%s", got, p.JSON)
			}
		}},
		{"the server ends with the store's totals, every root and every revision, through a 503, a restart, a 429 and a lost answer", func(t *testing.T) {
			waitFor(t, "every minute, revision and root", func() bool {
				u.Wake() // as the ticker would
				return count(`SELECT COUNT(*) FROM minute_buckets WHERE device_id = $1`) == 24*60 &&
					count(`SELECT COUNT(*) FROM minute_bucket_revisions WHERE device_id = $1`) == int64(lines) &&
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
			readings := map[event.Witness]int{}
			rows, err := db.Read.QueryContext(ctx, `SELECT bucket FROM minute_buckets WHERE device_id = $1`, deviceID)
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
				readings[mb.Witness]++
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("the server holds %v credited seconds, and the store %v", got, want)
			}
			if agentOnly != 20*24*60 {
				t.Errorf("the server holds %v agent-only seconds, not %v", agentOnly, 20*24*60)
			}
			// Nothing tried to send the day on time, and the failures of the
			// catch-up after don't change that: every minute reads
			// backfilled (DESIGN 7.4).
			if readings[event.Backfilled] != 24*60 || len(readings) != 1 {
				t.Errorf("the server holds the readings %v, not %d backfilled", readings, 24*60)
			}
			ft.mu.Lock()
			rateLimited, lost, replayed := ft.rateLimited, ft.lost, slices.Clone(ft.replayed)
			ft.mu.Unlock()
			if rateLimited != 0 {
				t.Errorf("the server's own limiter refused %d requests as rate_limited", rateLimited)
			}
			keysMu.Lock()
			resent := 0
			for _, k := range keys {
				if k == lost {
					resent++
				}
			}
			keysMu.Unlock()
			// The client counts a POST once however often net/http sends it
			// again by itself, so a second POST with the key is the
			// uploader's own retry.
			if lost == "" || resent < 2 || !slices.Contains(replayed, lost) {
				t.Errorf("the answer the server lost, to %q, wasn't fetched again by the uploader's own retry: it sent the key %d times, and the server replayed %q", lost, resent, replayed)
			}
		}},
		{"every receipt in batches.seg checks out under the server key", func(t *testing.T) {
			_, rows, err := ledger.ReadRows(dirs.Ledger(), 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != batches {
				t.Fatalf("%d rows", len(rows))
			}
			pub := sv.key.Public().(ed25519.PublicKey)
			for _, r := range rows {
				if r.Receipt == nil {
					t.Errorf("batch %d has no receipt", r.B)
				} else if err := pkgledger.VerifyReceipt(pub, deviceID, r.B, r.Head, *r.Receipt); err != nil {
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
		{"a busy session after it, a stop every second for 45 s with no restart, gets no rate_limited from the server's own limiter", func(t *testing.T) {
			// The limiter has counted every request since the restart the
			// faults injected. The test wakes the uploader at each stop,
			// more often than SoonDelay's coalescing does, so a pace that
			// let the requests through, as paceBurst = 1000 does, would
			// empty ingest's bucket of 30, refilled one every 10 s, within
			// the session (#150 R3).
			const busy = 45
			clockMu.Lock()
			at := now
			clockMu.Unlock()
			for i := range busy {
				at = at.Add(time.Second)
				clockMu.Lock()
				now = at
				clockMu.Unlock()
				if _, err := w.Write(stop(at, int64(i+1)*1000)); err != nil {
					t.Fatal(err)
				}
				// The stop seals at once on the writer's goroutine, and Seal
				// waits for that seal.
				if _, _, err := w.Seal(); err != nil {
					t.Fatal(err)
				}
				u.Wake()
				time.Sleep(time.Second)
			}
			last := w.Status().Batches
			waitFor(t, "every root of the session", func() bool {
				u.Wake()
				return count(`SELECT COUNT(*) FROM witness_rows WHERE device_id = $1`) == int64(last)
			})
			ft.mu.Lock()
			rateLimited := ft.rateLimited
			ft.mu.Unlock()
			if rateLimited != 0 {
				t.Errorf("the server's own limiter refused %d requests as rate_limited", rateLimited)
			}
		}},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.run) {
			break
		}
	}
}
