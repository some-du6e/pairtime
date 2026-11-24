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
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/sched"
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

// linkedServer is pairtimed's ingest over a new database, as cmd/pairtimed
// wires it, with the rows WP-SV3's device grant writes for a linked device:
// its account, and the device deviceID at link generation 1 with its
// token's hash and its key, and no ingest state yet. It returns the
// device's token and key.
func linkedServer(t *testing.T, deviceID string) (*server, string, ed25519.PrivateKey) {
	t.Helper()
	ctx := context.Background()
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
	return sv, token, devKey
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

// writeCommits appends lines to commits/'s file of day in dir as the git
// host's commitStore does, each with a newline before it and after it.
// Package e2e can't call the unexported store.
func writeCommits(t *testing.T, dir string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		var c struct {
			ObservedAt int64 `json:"observed_at"`
		}
		if err := json.Unmarshal([]byte(l), &c); err != nil {
			t.Fatal(err)
		}
		day := time.UnixMilli(c.ObservedAt).UTC().Format(time.DateOnly)
		f, err := os.OpenFile(filepath.Join(dir, day+".jsonl"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.WriteString("\n" + l + "\n")
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

// commitLine is a commits/ line as internal/adapters/git's Commit marshals
// it, members in its order, for the commit sha seen at, with parents and the
// rest as given, each a JSON member list ending in a comma.
func commitLine(sha, parents string, at time.Time, counts, rest string) string {
	s := at.Unix()
	return fmt.Sprintf(`{"sha":"%s","parents":%s,"project":"p_test","observed_at":%d,"author_time":%d,"committer_time":%d,%s%s}`,
		sha, parents, at.UnixMilli(), s-90, s-60, counts, rest)
}

// TestUploadToIngest is WP-AG5's client and server in one Linux test
// (#150 R4): a day of minute buckets and the batches beside them go from
// the uploader to pairtimed's ingest handler through a 503, a server
// restart, a 429 and a lost answer, which the uploader's own retry fetches
// again, under the server's own rate limiter, and both sides end with the
// same totals and DESIGN 7.4's readings; privacy show prints the first
// request's body byte for byte; nothing goes out before the sign-in. The
// commit records of commits/ reach commit_records once per sha, under the
// account, as their first lines say, after the roots of their batches, and
// one seen before the sign-in never does (D-ag5-D7).
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
	// A batch sealed before the sign-in, as the agent seals while the
	// computer isn't linked yet: the link's gap holds it, so its root never
	// goes, nor a commit record seen before it.
	clockMu.Lock()
	now = start.Add(-5 * time.Minute)
	clockMu.Unlock()
	if _, err := w.Checkpoint("p_test", uint64(start.Add(-5*time.Minute).UnixMilli()), json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.Seal(); err != nil {
		t.Fatal(err)
	}
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

	// commits/: a record seen before the sign-in, a root commit, one whose
	// shortstat couldn't be read, a crash's second line of it, with a later
	// observed_at and actor unknown, and one with members a record never
	// has, which stay on the device.
	shaGap, shaRoot, shaNoStat, shaMain := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40), strings.Repeat("d", 40)
	const counts = `"files_changed":1,"insertions":12,"deletions":0,`
	lineGap := commitLine(shaGap, "[]", start.Add(-10*time.Minute), counts, `"actor":"human"`)
	lineRoot := commitLine(shaRoot, "null", start.Add(5*time.Minute), counts, `"actor":"human"`)
	lineNoStat := commitLine(shaNoStat, `["`+shaRoot+`"]`, start.Add(3*time.Hour), "", `"actor":"agent","agent_trailer":"claude"`)
	lineAgain := commitLine(shaNoStat, `["`+shaRoot+`"]`, start.Add(20*time.Hour), "", `"actor":"unknown","agent_trailer":"claude"`)
	lineMain := commitLine(shaMain, `["`+shaNoStat+`"]`, start.Add(6*time.Hour), counts,
		`"message":"fix the emulator's timer","author_tz":"-0600","actor":"human"`)
	writeCommits(t, dirs.Commits(), lineGap, lineRoot, lineNoStat, lineMain, lineAgain)

	sv, token, devKey := linkedServer(t, deviceID)
	db := sv.db
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
	// post is a POST the uploader sent, a retry of its own included, and
	// whether it got no answer. net/http's own resends happen inside one.
	type post struct {
		key    string
		failed bool
	}
	var (
		signedIn atomic.Bool
		sent     atomic.Int64
		postsMu  sync.Mutex
		posted   []post
	)
	creds := uploader.Credentials{Server: srv.URL, Token: token, DeviceID: deviceID, Sign: uploader.KeySigner(devKey), LinkedAt: start}
	client := &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		sent.Add(1)
		resp, err := transport.RoundTrip(r)
		if r.Method == http.MethodPost {
			postsMu.Lock()
			posted = append(posted, post{key: r.Header.Get("Idempotency-Key"), failed: err != nil})
			postsMu.Unlock()
		}
		return resp, err
	})}
	// A scheduler that never runs takes the place of the uploader's own
	// ticker, so only the test wakes it, as the steps' waits do, and nothing
	// goes between privacy show's preview and the wake that sends it.
	u, err := uploader.New(uploader.Options{
		Ledger: w, LedgerDir: dirs.Ledger(), BucketDir: dirs.Buckets(), DataDir: dirs.Data, State: st,
		Login:  uploader.LoginFunc(func() (uploader.Credentials, bool) { return creds, signedIn.Load() }),
		Params: table, Started: started, Client: client, Draw: func(int64) int64 { return 0 }, Sched: sched.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	up.Store(u)
	u.Start()
	t.Cleanup(u.Close)
	preview := func(t *testing.T) uploader.Preview {
		t.Helper()
		p, err := uploader.Next(uploader.PreviewOptions{
			LedgerDir: dirs.Ledger(), BucketDir: dirs.Buckets(), DataDir: dirs.Data, StateFile: dirs.StateFile(),
			Params: table, Credentials: creds,
		})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}

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
			// The server's own label agrees, for every minute and every
			// revision it stored (WP-SV6).
			if n := count(`SELECT COUNT(*) FROM minute_buckets WHERE device_id = $1 AND server_witness = 'backfilled'`); n != 24*60 {
				t.Errorf("the server labeled %d of its %d minutes backfilled", n, 24*60)
			}
			if n := count(`SELECT COUNT(*) FROM minute_bucket_revisions WHERE device_id = $1 AND server_witness = 'backfilled'`); n != int64(lines) {
				t.Errorf("the server labeled %d of its %d revisions backfilled", n, lines)
			}
			ft.mu.Lock()
			rateLimited, lost, replayed := ft.rateLimited, ft.lost, slices.Clone(ft.replayed)
			ft.mu.Unlock()
			if rateLimited != 0 {
				t.Errorf("the server's own limiter refused %d requests as rate_limited", rateLimited)
			}
			postsMu.Lock()
			posts := slices.Clone(posted)
			postsMu.Unlock()
			// The lost answer reached the uploader as a failure, and a POST
			// of its own after it sent the key again. Had net/http fetched
			// the answer again by itself, the POST would have its answer.
			i := slices.IndexFunc(posts, func(p post) bool { return p.key == lost && p.failed })
			retried := i >= 0 && slices.ContainsFunc(posts[i+1:], func(p post) bool { return p.key == lost })
			if lost == "" || !retried || !slices.Contains(replayed, lost) {
				t.Errorf("the answer the server lost, to %q, wasn't fetched again by the uploader's own retry: its POSTs %+v, and the server replayed %q", lost, posts, replayed)
			}
		}},
		{"every receipt in batches.seg checks out under the server key, and the batch sealed before the sign-in has none", func(t *testing.T) {
			_, rows, err := ledger.ReadRows(dirs.Ledger(), 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != batches+1 {
				t.Fatalf("%d rows", len(rows))
			}
			pub := sv.key.Public().(ed25519.PublicKey)
			for _, r := range rows {
				if r.B == 1 {
					if r.Receipt != nil {
						t.Error("the batch sealed before the sign-in has a receipt")
					}
					continue
				}
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
		{"commit records reach commit_records once per sha, under the account, as their first lines say, and one seen before the sign-in never does", func(t *testing.T) {
			waitFor(t, "the commit records", func() bool {
				u.Wake()
				return count(`SELECT COUNT(*) FROM commit_records WHERE device_id = $1`) == 3
			})
			rows, err := db.Read.QueryContext(ctx, `SELECT sha, account_id, project, body FROM commit_records WHERE device_id = $1`, deviceID)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			stored := map[string]string{}
			for rows.Next() {
				var sha, acct, project, body string
				if err := rows.Scan(&sha, &acct, &project, &body); err != nil {
					t.Fatal(err)
				}
				if acct != account || project != "p_test" {
					t.Errorf("commit %s is stored under account %q and project %q", sha, acct, project)
				}
				stored[sha] = body
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			// The server stores its own re-encoding, a root commit's null
			// parents as [], so each body is read as JSON.
			want := map[string]string{
				shaRoot:   strings.Replace(lineRoot, `"parents":null`, `"parents":[]`, 1),
				shaNoStat: lineNoStat,
				shaMain:   strings.Replace(lineMain, `"message":"fix the emulator's timer","author_tz":"-0600",`, "", 1),
			}
			for sha, line := range want {
				var got, exp any
				if err := json.Unmarshal([]byte(stored[sha]), &got); err != nil {
					t.Errorf("commit %s: %v", sha, err)
					continue
				}
				if err := json.Unmarshal([]byte(line), &exp); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, exp) {
					t.Errorf("commit %s is stored as\n%s\nnot as its first line says\n%s", sha, stored[sha], line)
				}
			}
			if _, ok := stored[shaGap]; ok {
				t.Error("the commit seen before the sign-in reached the server")
			}
			for sha, body := range stored {
				for _, local := range []string{"message", "emulator", "author_tz", "-0600"} {
					if strings.Contains(body, local) {
						t.Errorf("commit %s's stored record holds %q: %s", sha, local, body)
					}
				}
			}
			past, err := uploader.PastUploads(dirs.Data)
			if err != nil {
				t.Fatal(err)
			}
			commits, accepted := 0, 0
			for _, p := range past {
				if p.Status == http.StatusOK {
					commits += p.Commits
					if p.Results != nil {
						accepted += p.Results.Commits["accepted"]
					}
				}
			}
			if commits != 3 || accepted != 3 {
				t.Errorf("uploads.jsonl counts %d commit records in answered uploads, %d accepted: want 3", commits, accepted)
			}
		}},
		{"privacy show prints the request that carries a commit record byte for byte, after the request that carries its batch's root", func(t *testing.T) {
			late := start.Add(24*time.Hour + 5*time.Minute)
			shaLate := strings.Repeat("e", 40)
			writeCommits(t, dirs.Commits(), commitLine(shaLate, `["`+shaMain+`"]`, late, counts, `"actor":"human"`))
			clockMu.Lock()
			now = late.Add(5 * time.Minute)
			clockMu.Unlock()
			if _, err := w.Checkpoint("p_test", uint64(late.Add(5*time.Minute).UnixMilli()), json.RawMessage(`{}`)); err != nil {
				t.Fatal(err)
			}
			if _, _, err := w.Seal(); err != nil {
				t.Fatal(err)
			}
			last := int64(w.Status().Batches) - 1 // batch 1 never goes
			u.Wake()
			waitFor(t, "the new batch's root", func() bool {
				return count(`SELECT COUNT(*) FROM witness_rows WHERE device_id = $1`) == last
			})
			var pv uploader.Preview
			waitFor(t, "the record in privacy show", func() bool { pv = preview(t); return pv.Commits == 1 })
			if pv.Batches != 0 {
				t.Errorf("the record's request carries %d roots: want its batch's root to have gone before it", pv.Batches)
			}
			before := len(ft.bodies())
			for _, body := range ft.bodies() {
				if bytes.Contains(body, []byte(shaLate)) {
					t.Fatal("the record went beside its batch's root")
				}
			}
			u.Wake()
			waitFor(t, "the record's request", func() bool { return len(ft.bodies()) > before })
			if got := ft.bodies()[before]; !bytes.Equal(got, pv.JSON) {
				t.Errorf("the request's body differs from privacy show's:\n%s\n%s", got, pv.JSON)
			}
			waitFor(t, "the record on the server", func() bool {
				return count(`SELECT COUNT(*) FROM commit_records WHERE device_id = $1`) == 4
			})
		}},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.run) {
			break
		}
	}
}

// TestBusySession is a session busy with stops, one a second for 45 s,
// against pairtimed's ingest with no restart and no fault (#150 R3): the
// server's own limiter, a burst of 30 refilled one every 10 s, refuses none
// of the uploader's requests, and every root gets there. The test wakes
// the uploader at each stop, more often than SoonDelay's coalescing does,
// so an uploader that let its requests through, as one with paceBurst =
// 1000, would empty the server's bucket within the session. It is a test
// of its own because the catch-up in TestUploadToIngest answers a 429,
// which drains the uploader's pace, and after that the burst makes no
// difference for minutes.
func TestBusySession(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dirs := home.Dirs{Data: dir, Config: dir, Runtime: dir}
	table := newestTable()
	keys, err := pkgledger.DeriveKeys(seed("e2e busy session K_dev"))
	if err != nil {
		t.Fatal(err)
	}
	var up atomic.Pointer[uploader.Uploader]
	w, err := ledger.Open(ledger.Options{
		Dir: dirs.Ledger(), Keys: keys, KeyID: "0123456789abcdef", Params: table,
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
	if err := os.MkdirAll(dirs.Buckets(), 0o700); err != nil {
		t.Fatal(err)
	}
	sv, token, devKey := linkedServer(t, w.DeviceID())
	ft := &faults{srv: sv, closeIdle: func() {}, by: map[int]string{}}
	srv := httptest.NewServer(ft)
	t.Cleanup(srv.Close)
	st, _, err := state.Open(dirs.StateFile())
	if err != nil {
		t.Fatal(err)
	}
	creds := uploader.Credentials{Server: srv.URL, Token: token, DeviceID: w.DeviceID(), Sign: uploader.KeySigner(devKey), LinkedAt: time.Now().Add(-time.Minute)}
	u, err := uploader.New(uploader.Options{
		Ledger: w, LedgerDir: dirs.Ledger(), BucketDir: dirs.Buckets(), DataDir: dirs.Data, State: st,
		Login:  uploader.LoginFunc(func() (uploader.Credentials, bool) { return creds, true }),
		Params: table, Started: time.Now(), Client: srv.Client(), Draw: func(int64) int64 { return 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	up.Store(u)
	u.Start()
	t.Cleanup(u.Close)

	const busy = 45
	for i := range busy {
		if _, err := w.Write(stop(time.Now(), int64(i+1)*1000)); err != nil {
			t.Fatal(err)
		}
		// The stop seals at once on the writer's goroutine, and Seal waits
		// for that seal.
		if _, _, err := w.Seal(); err != nil {
			t.Fatal(err)
		}
		u.Wake()
		time.Sleep(time.Second)
	}
	last := int64(w.Status().Batches)
	roots := func() int64 {
		var n int64
		if err := sv.db.Read.QueryRowContext(ctx, `SELECT COUNT(*) FROM witness_rows WHERE device_id = $1`, w.DeviceID()).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	deadline := time.Now().Add(time.Minute)
	for roots() != last && time.Now().Before(deadline) {
		u.Wake()
		time.Sleep(25 * time.Millisecond)
	}
	ft.mu.Lock()
	rateLimited := ft.rateLimited
	ft.mu.Unlock()
	checks := []struct {
		name string
		ok   bool
	}{
		{"every root of the session reaches the server", roots() == last && last >= busy},
		{"the server's own limiter refuses none of the uploader's requests", rateLimited == 0},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Errorf("no: the server holds %d of %d roots, and refused %d requests as rate_limited", roots(), last, rateLimited)
			}
		})
	}
}
