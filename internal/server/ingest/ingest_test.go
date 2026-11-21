// SPDX-License-Identifier: AGPL-3.0-or-later

package ingest

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/devtoken"
	"github.com/some-du6e/pairtime/internal/server/backup"
	"github.com/some-du6e/pairtime/internal/server/device"
	"github.com/some-du6e/pairtime/internal/server/migrations"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/ledger"
)

// testDevice is a linked device and what its client keeps: its token, its
// key and its counter.
type testDevice struct {
	ID        string
	Account   string
	PublicKey ed25519.PublicKey
	token     string
	key       ed25519.PrivateKey
	req       int64
}

// testDevices is WP-SV3's device store, the one pairtimed runs, with
// ptd_broken standing for the store failing.
type testDevices struct{ *device.Store }

func (d testDevices) Authenticate(ctx context.Context, token string) (device.Device, error) {
	if token == "ptd_broken" {
		return device.Device{}, errors.New("the database is down")
	}
	return d.Store.Authenticate(ctx, token)
}

// hashOf is a token as the devices row stores it, its SHA-256 in hex.
func hashOf(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// rollupCall is one call of the Rollup hook.
type rollupCall struct {
	agentOnly string // next's agent_only, as JSON
	account   string
	prevRev   int64 // -1 for no prev
	nextRev   int64
	storedRev int64 // the rev minute_buckets held, as the hook's tx saw it
	minuteMS  int64
}

type harness struct {
	t        testing.TB
	db       *store.DB
	writer   *writer.Writer
	svc      *Service
	mux      *http.ServeMux
	clock    time.Time
	server   ed25519.PrivateKey
	devices  testDevices
	accounts int
	keys     int
	rollups  []rollupCall
	// observed is what each Observe call reported as stored.
	observed []int
}

func seedKey(name string) ed25519.PrivateKey {
	seed := sha256.Sum256([]byte(name))
	return ed25519.NewKeyFromSeed(seed[:])
}

// exampleClock is the worked example's time, 2026-11-19T12:01:00.250Z.
var exampleClock = time.UnixMilli(1795089660250).UTC()

func newHarness(t *testing.T, engine store.Engine) *harness {
	t.Helper()
	ctx := context.Background()
	db := storetest.Open(t, engine)
	ms, err := migrations.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(ctx, db, ms, time.Now); err != nil {
		t.Fatal(err)
	}
	w := writer.Start(db.Write, writer.Options{Window: time.Millisecond})
	t.Cleanup(w.Close)
	h := &harness{t: t, db: db, writer: w, clock: exampleClock, server: seedKey("server")}
	ds, err := device.New(device.Options{DB: db, Writer: w, Now: func() time.Time { return h.clock }})
	if err != nil {
		t.Fatal(err)
	}
	h.devices = testDevices{ds}
	h.svc = New(Options{
		Devices: h.devices, Writer: w, Read: db.Read, Key: h.server,
		Now: func() time.Time { return h.clock },
		Observe: func(stored int, took time.Duration) {
			if took < 0 {
				t.Errorf("Observe got a latency of %v", took)
			}
			h.observed = append(h.observed, stored)
		},
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		RateBurst: 1000,
		Rollup: func(ctx context.Context, tx writer.Tx, account string, prev *event.MinuteBucket, next event.MinuteBucket) error {
			c := rollupCall{account: account, prevRev: -1, nextRev: int64(next.Rev), minuteMS: next.Minute.UnixMilli()}
			if next.AgentOnly != nil {
				c.agentOnly = string(marshal(t, next.AgentOnly))
			}
			if prev != nil {
				c.prevRev = int64(prev.Rev)
			}
			if err := tx.QueryRowContext(ctx, `SELECT rev FROM minute_buckets WHERE device_id = $1 AND minute_ms = $2`,
				next.Device, next.Minute.UnixMilli()).Scan(&c.storedRev); err != nil {
				return err
			}
			h.rollups = append(h.rollups, c)
			return nil
		},
	})
	h.mux = http.NewServeMux()
	h.svc.Register(h.mux)
	return h
}

// exec runs a statement through the writer, for a test to set up or
// change rows that only other packages write in pairtimed.
func (h *harness) exec(t testing.TB, query string, args ...any) {
	t.Helper()
	if err := h.writer.Do(context.Background(), 1, func(ctx context.Context, tx writer.Tx) error {
		_, err := tx.ExecContext(ctx, query, args...)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// device links a device with a key; keyless leaves it without one.
func (h *harness) device(id string, keyless bool) *testDevice {
	h.t.Helper()
	var key ed25519.PrivateKey
	if !keyless {
		key = seedKey("device " + id)
	}
	return h.link(h.t, id, key)
}

// link makes a new account and links device id to it with key, writing
// the rows WP-SV3's device grant writes: a users row and a devices row at
// link generation 1 with no ingest state. A nil key stores an empty one,
// which device.Store reads as none.
func (h *harness) link(t testing.TB, id string, key ed25519.PrivateKey) *testDevice {
	t.Helper()
	h.accounts++
	d := &testDevice{ID: id, Account: fmt.Sprintf("0190f4b2-0000-7000-8000-%012x", h.accounts), token: devtoken.New(), key: key}
	pub := []byte{}
	if key != nil {
		d.PublicKey = key.Public().(ed25519.PublicKey)
		pub = d.PublicKey
	}
	now := h.clock.UnixMilli()
	h.exec(t, `INSERT INTO users (id, hca_sub, verification_status, created_at_ms, updated_at_ms, signed_in_at_ms) VALUES ($1, $2, 'verified', $3, $3, $3)`,
		d.Account, "hca-"+d.Account, now)
	h.exec(t, `INSERT INTO devices (device_id, user_id, token_hash, public_key, os_family, client_build, linked_at_ms, last_seen_at_ms, link_generation)
VALUES ($1, $2, $3, $4, 'linux', 'v0.0.0-dev', $5, $5, 1)`, id, d.Account, hashOf(d.token), pub, now)
	return d
}

// relink links d again with a new token, as WP-SV3's grant does after
// pairtime logout and pairtime login: the same device_id and its ingest
// state, one link generation more (D-local-gap).
func (h *harness) relink(t testing.TB, d *testDevice) {
	t.Helper()
	d.token = devtoken.New()
	h.exec(t, `UPDATE devices SET token_hash = $2, link_generation = link_generation + 1 WHERE device_id = $1`, d.ID, hashOf(d.token))
}

func (h *harness) newKey() string {
	h.keys++
	return fmt.Sprintf("0190f4b2-5a1c-7000-8000-%012x", h.keys)
}

// upload is one POST /api/v1/ingest/batches. Zero fields take the
// defaults: a JSON body from body, gzipped; a new key; the device's next
// counter; the server's clock; the device's key.
type upload struct {
	dev    *testDevice
	body   any
	json   []byte
	raw    []byte
	key    string
	req    int64
	time   int64
	signer ed25519.PrivateKey
	edit   func(r *http.Request)
}

func gz(t testing.TB, data []byte) []byte { return gzipped(t, data) }

func (h *harness) request(t testing.TB, u upload) *http.Request {
	t.Helper()
	data := u.json
	if data == nil {
		data = []byte(`{}`)
		if u.body != nil {
			data = marshal(t, u.body)
		}
	}
	raw := u.raw
	if raw == nil {
		raw = gz(t, data)
	}
	sum := sha256.Sum256(raw)
	bodySHA := hex.EncodeToString(sum[:])
	if u.req == 0 {
		u.dev.req++
		u.req = u.dev.req
	}
	if u.time == 0 {
		u.time = h.clock.UnixMilli()
	}
	if u.key == "" {
		u.key = h.newKey()
	}
	signer := u.signer
	if signer == nil {
		signer = u.dev.key
	}
	reqText, timeText := strconv.FormatInt(u.req, 10), strconv.FormatInt(u.time, 10)
	var sig []byte
	if signer != nil {
		sig = ed25519.Sign(signer, Message(u.dev.ID, reqText, timeText, bodySHA))
	} else {
		sig = make([]byte, ed25519.SignatureSize)
	}
	r := httptest.NewRequest(http.MethodPost, BatchesPath, bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+u.dev.token)
	r.Header.Set(HeaderIdempotencyKey, u.key)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Content-Encoding", "gzip")
	r.Header.Set(HeaderDevice, u.dev.ID)
	r.Header.Set(HeaderReq, reqText)
	r.Header.Set(HeaderTime, timeText)
	r.Header.Set(HeaderBodySHA256, bodySHA)
	r.Header.Set(HeaderSignature, signaturePrefix+base64.StdEncoding.EncodeToString(sig))
	if u.edit != nil {
		u.edit(r)
	}
	return r
}

func (h *harness) serve(t testing.TB, r *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, r)
	if got := rec.Header().Get(HeaderTime); got != strconv.FormatInt(h.clock.UnixMilli(), 10) {
		t.Errorf("%s %s: X-Pairtime-Time = %q, want the server's clock", r.Method, r.URL.Path, got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("%s %s: Cache-Control = %q, want no-store", r.Method, r.URL.Path, got)
	}
	return rec
}

func (h *harness) send(t testing.TB, u upload) *httptest.ResponseRecorder {
	t.Helper()
	return h.serve(t, h.request(t, u))
}

// ok sends u and decodes its 200 response.
func (h *harness) ok(t testing.TB, u upload) response {
	t.Helper()
	rec := h.send(t, u)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body)
	}
	var resp response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

// refusalOf returns a refused request's status and code.
func refusalOf(t *testing.T, rec *httptest.ResponseRecorder) (int, string) {
	t.Helper()
	var e errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("status %d with a body that isn't an error: %s", rec.Code, rec.Body)
	}
	return rec.Code, e.Error
}

// state reads GET /api/v1/ingest/state for d.
// stateRequest is a GET /api/v1/ingest/state for d, signed with signer
// at the server's clock.
func (h *harness) stateRequest(d *testDevice, signer ed25519.PrivateKey) *http.Request {
	r := httptest.NewRequest(http.MethodGet, StatePath, nil)
	r.Header.Set("Authorization", "Bearer "+d.token)
	r.Header.Set(HeaderDevice, d.ID)
	tm := strconv.FormatInt(h.clock.UnixMilli(), 10)
	r.Header.Set(HeaderTime, tm)
	r.Header.Set(HeaderSignature, signaturePrefix+base64.StdEncoding.EncodeToString(ed25519.Sign(signer, StateMessage(d.ID, tm))))
	return r
}

func (h *harness) state(t testing.TB, d *testDevice) stateJSON {
	t.Helper()
	rec := h.serve(t, h.stateRequest(d, d.key))
	if rec.Code != http.StatusOK {
		t.Fatalf("state: status %d: %s", rec.Code, rec.Body)
	}
	var st stateJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	return st
}

// root is a batch root on a test chain.
type root struct {
	b, n          uint64
	r, head, prev ledger.Hash
}

func (x root) json() map[string]any {
	return map[string]any{"b": x.b, "n": x.n, "R_b": x.r.String(), "H_b": x.head.String(), "prev": x.prev.String()}
}

// relink sets x's head from its other fields.
func (x root) relink() root {
	x.head = ledger.BatchHead(x.prev, x.r, x.n, x.b)
	return x
}

// chain returns count batch roots from b, linked from prev, with random
// roots and counts.
func chain(rng *rand.Rand, prev ledger.Hash, b uint64, count int) []root {
	var out []root
	for i := 0; i < count; i++ {
		var r ledger.Hash
		for j := range r {
			r[j] = byte(rng.Uint32())
		}
		x := root{b: b + uint64(i), n: 1 + rng.Uint64N(500), r: r, prev: prev}.relink()
		out = append(out, x)
		prev = x.head
	}
	return out
}

func batchesBody(roots ...root) map[string]any {
	items := make([]any, len(roots))
	for i, x := range roots {
		items[i] = x.json()
	}
	return map[string]any{"batches": items}
}

func engines(t *testing.T, test func(t *testing.T, engine store.Engine)) {
	for _, engine := range storetest.Engines() {
		t.Run(string(engine), func(t *testing.T) { test(t, engine) })
	}
}

// TestWorkedExampleAccepted sends the spec's worked example, byte for
// byte, to a server whose clock reads its time.
func TestWorkedExampleAccepted(t *testing.T) {
	engines(t, func(t *testing.T, engine store.Engine) {
		h := newHarness(t, engine)
		// The token isn't in M, so any token of the example's device does.
		ex := h.link(t, exampleDevice, exampleKey())
		r := httptest.NewRequest(http.MethodPost, BatchesPath, bytes.NewReader(mustHex(t, exampleRaw)))
		for name, v := range map[string]string{
			"Authorization": "Bearer " + ex.token, HeaderIdempotencyKey: "0190f4b2-5a1c-7d3e-8f00-123456789abc",
			"Content-Type": "application/json", "Content-Encoding": "gzip", HeaderDevice: exampleDevice,
			HeaderReq: exampleReq, HeaderTime: exampleTime, HeaderBodySHA256: exampleBodySHA, HeaderSignature: exampleHeader,
		} {
			r.Header.Set(name, v)
		}
		rec := h.serve(t, r)
		var resp response
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
		tests := []struct {
			name string
			ok   bool
		}{
			{"its one batch root is accepted", len(resp.Batches) == 1 && resp.Batches[0].Code == CodeAccepted},
			{"its receipt verifies under the server key", len(resp.Batches) == 1 && verifyReceipt(h, exampleDevice, 1, exampleHead, resp.Batches[0].Receipt) == nil},
			{"the response gives the counter and the new head", resp.Req == 42 && resp.Head != nil && resp.Head.B == 1 && resp.Head.Head.String() == exampleHead},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if !tt.ok {
					t.Errorf("no: %s", rec.Body)
				}
			})
		}
	})
}

func verifyReceipt(h *harness, deviceID string, b uint64, head string, rc *receiptJSON) error {
	if rc == nil {
		return errors.New("no receipt")
	}
	var hh ledger.Hash
	if err := hh.UnmarshalText([]byte(head)); err != nil {
		return err
	}
	sig, err := hex.DecodeString(rc.Sig)
	if err != nil || len(sig) != ledger.SignatureSize {
		return errors.New("the receipt's sig isn't 128 hex digits")
	}
	r := ledger.Receipt{ReceivedAt: uint64(rc.ReceivedAt)}
	copy(r.Signature[:], sig)
	pub := h.server.Public().(ed25519.PublicKey)
	if rc.KeyID != backup.KeyID(pub) {
		return fmt.Errorf("the receipt names key %s, not the server key %s", rc.KeyID, backup.KeyID(pub))
	}
	return ledger.VerifyReceipt(pub, deviceID, b, hh, r)
}

// TestRefusals checks each refusal of a whole request, with its status and
// code (ingest-v1 2.5, 2.6), and that a refusal moves no counter.
func TestRefusals(t *testing.T) {
	engines(t, func(t *testing.T, engine store.Engine) {
		h := newHarness(t, engine)
		dev := h.device("d_test", false)
		revoked := h.device("d_revoked", false)
		h.exec(t, `UPDATE users SET verification_status = 'ineligible' WHERE id = $1`, revoked.Account)
		removed := h.device("d_removed", false)
		h.exec(t, `UPDATE devices SET removed_at_ms = $2, token_hash = NULL WHERE device_id = $1`, removed.ID, h.clock.UnixMilli())
		keyless := h.device("d_keyless", true)
		other := seedKey("another key")
		header := func(name, v string) func(*http.Request) {
			return func(r *http.Request) {
				if v == "" {
					r.Header.Del(name)
				} else {
					r.Header.Set(name, v)
				}
			}
		}
		items := func(name string, n int) []byte {
			return []byte(`{"` + name + `":[` + strings.TrimSuffix(strings.Repeat("0,", n), ",") + `]}`)
		}
		big := make([]byte, MaxRawBody+1)
		tests := []struct {
			name   string
			u      upload
			status int
			code   string
		}{
			{"no Authorization header is token_missing", upload{dev: dev, edit: header("Authorization", "")}, 401, CodeTokenMissing},
			{"a Basic credential is token_missing", upload{dev: dev, edit: header("Authorization", "Basic ZGV2OnB3")}, 401, CodeTokenMissing},
			{"a token that fails its checksum is token_invalid", upload{dev: dev, edit: header("Authorization", "Bearer ptd_nobody")}, 401, CodeTokenInvalid},
			{"a well-formed token no device has is token_invalid", upload{dev: dev, edit: header("Authorization", "Bearer "+devtoken.New())}, 401, CodeTokenInvalid},
			{"a removed device's token names no device, so it is token_invalid", upload{dev: removed}, 401, CodeTokenInvalid},
			{"the device store failing is unavailable", upload{dev: dev, edit: header("Authorization", "Bearer ptd_broken")}, 503, CodeUnavailable},
			{"a device whose account can no longer sign in is device_revoked", upload{dev: revoked}, 403, CodeDeviceRevoked},
			{"another device's id in X-Pairtime-Device is device_mismatch", upload{dev: dev, edit: header(HeaderDevice, "d_revoked")}, 403, CodeDeviceMismatch},
			{"a missing X-Pairtime-Device is device_mismatch", upload{dev: dev, edit: header(HeaderDevice, "")}, 403, CodeDeviceMismatch},
			{"a device that registered no key is device_key_missing", upload{dev: keyless}, 403, CodeDeviceKeyMissing},
			{"a UUIDv4 key is idempotency_key_malformed", upload{dev: dev, key: "0190f4b2-5a1c-4000-8000-000000000001"}, 400, CodeIdempotencyKeyMalformed},
			{"a counter with a leading zero is counter_malformed", upload{dev: dev, edit: header(HeaderReq, "07")}, 400, CodeCounterMalformed},
			{"a time in seconds with a fraction is time_malformed", upload{dev: dev, edit: header(HeaderTime, "1795089660.25")}, 400, CodeTimeMalformed},
			{"an uppercase body hash is body_hash_malformed", upload{dev: dev, edit: func(r *http.Request) {
				r.Header.Set(HeaderBodySHA256, strings.ToUpper(r.Header.Get(HeaderBodySHA256)))
			}}, 400, CodeBodyHashMalformed},
			{"a signature without ed25519= is signature_malformed", upload{dev: dev, edit: func(r *http.Request) {
				r.Header.Set(HeaderSignature, strings.TrimPrefix(r.Header.Get(HeaderSignature), signaturePrefix))
			}}, 400, CodeSignatureMalformed},
			{"text/plain is content_type_unsupported", upload{dev: dev, edit: header("Content-Type", "text/plain")}, 415, CodeContentTypeUnsupported},
			{"a body without Content-Encoding is encoding_unsupported", upload{dev: dev, edit: header("Content-Encoding", "")}, 415, CodeEncodingUnsupported},
			{"a clock 301 s ahead is clock_skew", upload{dev: dev, time: exampleClock.UnixMilli() + 301_000}, 401, CodeClockSkew},
			{"a clock 301 s behind is clock_skew", upload{dev: dev, time: exampleClock.UnixMilli() - 301_000}, 401, CodeClockSkew},
			{"a raw body one byte over 256 KiB is body_too_large", upload{dev: dev, raw: big}, 413, CodeBodyTooLarge},
			{"a body whose hash differs from X-Pairtime-Body-SHA256 is body_hash_mismatch", upload{dev: dev, edit: func(r *http.Request) {
				r.Body = io.NopCloser(bytes.NewReader(gz(t, []byte(`{"batches":[]}`))))
				r.ContentLength = -1
			}}, 400, CodeBodyHashMismatch},
			{"a body signed by another key is signature_invalid", upload{dev: dev, signer: other}, 401, CodeSignatureInvalid},
			{"a signature over another counter is signature_invalid", upload{dev: dev, edit: header(HeaderReq, "999999")}, 401, CodeSignatureInvalid},
			{"a signature over another time is signature_invalid", upload{dev: dev, edit: header(HeaderTime, strconv.FormatInt(exampleClock.UnixMilli()+1, 10))}, 401, CodeSignatureInvalid},
			{"a raw body that isn't gzip is gzip_invalid", upload{dev: dev, raw: []byte(`{}`)}, 400, CodeGzipInvalid},
			{"bytes after the gzip member are gzip_invalid", upload{dev: dev, raw: append(gz(t, []byte(`{}`)), '\n')}, 400, CodeGzipInvalid},
			{"a gzip bomb is decompressed_too_large", upload{dev: dev, raw: bomb(t, 64<<20)}, 413, CodeDecompressedTooLarge},
			{"text that isn't JSON is json_invalid", upload{dev: dev, json: []byte(`{"batches":`)}, 400, CodeJSONInvalid},
			{"a member outside ingest-v1 2.3 is body_shape", upload{dev: dev, json: []byte(`{"prompts":["fix it"]}`)}, 422, CodeBodyShape},
			{"a member that appears twice is body_shape", upload{dev: dev, json: []byte(`{"batches":[],"batches":[]}`)}, 422, CodeBodyShape},
			{"2,001 batch roots are too_many_batches", upload{dev: dev, json: items("batches", MaxBatches+1)}, 413, CodeTooManyBatches},
			{"2,001 buckets are too_many_buckets", upload{dev: dev, json: items("buckets", MaxBuckets+1)}, 413, CodeTooManyBuckets},
			{"501 commit records are too_many_commits", upload{dev: dev, json: items("commits", MaxCommits+1)}, 413, CodeTooManyCommits},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				rec := h.send(t, tt.u)
				status, code := refusalOf(t, rec)
				if status != tt.status || code != tt.code {
					t.Errorf("got %d %s, want %d %s: %s", status, code, tt.status, tt.code, rec.Body)
				}
				if status == http.StatusServiceUnavailable && rec.Header().Get("Retry-After") == "" {
					t.Error("a 503 without Retry-After")
				}
			})
		}
		t.Run("no refusal moved the counter or stored anything", func(t *testing.T) {
			if st := h.state(t, dev); st.Req != 0 || st.Head != nil {
				t.Errorf("state = %+v, want counter 0 and no head", st)
			}
		})
		t.Run("a clock 300 s off is still on time", func(t *testing.T) {
			h.ok(t, upload{dev: dev, time: exampleClock.UnixMilli() - MaxSkewMS})
			h.ok(t, upload{dev: dev, time: exampleClock.UnixMilli() + MaxSkewMS})
		})
	})
}

// TestCounterAndReplay checks X-Pairtime-Req and Idempotency-Key
// (ingest-v1 2.5 step 13, 2.10).
func TestCounterAndReplay(t *testing.T) {
	engines(t, func(t *testing.T, engine store.Engine) {
		h := newHarness(t, engine)
		dev := h.device("d_test", false)
		rng := rand.New(rand.NewPCG(1, 2))
		roots := chain(rng, ledger.Hash{}, 1, 3)
		dev.req = 10
		first := upload{dev: dev, body: batchesBody(roots[0]), key: h.newKey()}
		firstRec := h.send(t, first)
		if firstRec.Code != http.StatusOK {
			t.Fatalf("first upload: %d %s", firstRec.Code, firstRec.Body)
		}
		firstReq := dev.req
		var signed *http.Request
		tests := []struct {
			name  string
			check func(t *testing.T)
		}{
			{"a retry with the same key and body, signed again, gets the stored response byte for byte", func(t *testing.T) {
				rec := h.send(t, upload{dev: dev, body: first.body, key: first.key})
				if rec.Code != http.StatusOK || rec.Body.String() != firstRec.Body.String() || rec.Header().Get(HeaderReplay) != "true" {
					t.Errorf("got %d %q replay=%q, want 200, the first body and replay=true", rec.Code, rec.Body, rec.Header().Get(HeaderReplay))
				}
			}},
			{"re-sending the identical signed bytes gets the stored response too", func(t *testing.T) {
				signed = h.request(t, upload{dev: dev, body: first.body, key: first.key, req: firstReq})
				rec := h.serve(t, signed)
				if rec.Code != http.StatusOK || rec.Body.String() != firstRec.Body.String() {
					t.Errorf("got %d %s", rec.Code, rec.Body)
				}
			}},
			{"the same key with another body is idempotency_key_reused", func(t *testing.T) {
				rec := h.send(t, upload{dev: dev, body: batchesBody(roots[1]), key: first.key})
				if s, c := refusalOf(t, rec); s != 422 || c != CodeIdempotencyKeyReused {
					t.Errorf("got %d %s", s, c)
				}
			}},
			{"a replay doesn't move the counter", func(t *testing.T) {
				if st := h.state(t, dev); st.Req != firstReq {
					t.Errorf("counter %d, want %d", st.Req, firstReq)
				}
			}},
			{"a counter equal to the stored one is counter_stale", func(t *testing.T) {
				rec := h.send(t, upload{dev: dev, body: batchesBody(roots[1]), req: firstReq})
				if s, c := refusalOf(t, rec); s != 409 || c != CodeCounterStale {
					t.Errorf("got %d %s", s, c)
				}
			}},
			{"a counter below the stored one is counter_stale", func(t *testing.T) {
				rec := h.send(t, upload{dev: dev, body: batchesBody(roots[1]), req: firstReq - 1})
				if s, c := refusalOf(t, rec); s != 409 || c != CodeCounterStale {
					t.Errorf("got %d %s", s, c)
				}
			}},
			{"a counter far above the stored one is fine", func(t *testing.T) {
				dev.req += 1000
				resp := h.ok(t, upload{dev: dev, body: batchesBody(roots[1])})
				if resp.Req != dev.req || resp.Batches[0].Code != CodeAccepted {
					t.Errorf("got %+v", resp)
				}
			}},
			{"an older key is processed again, and its batch is a duplicate with the first receipt", func(t *testing.T) {
				var want response
				if err := json.Unmarshal(firstRec.Body.Bytes(), &want); err != nil {
					t.Fatal(err)
				}
				resp := h.ok(t, upload{dev: dev, body: first.body, key: first.key})
				got := resp.Batches[0]
				if got.Code != CodeDuplicate || got.Receipt == nil || want.Batches[0].Receipt == nil || *got.Receipt != *want.Batches[0].Receipt {
					t.Errorf("got %+v, want a duplicate with receipt %+v", got, want.Batches[0].Receipt)
				}
			}},
		}
		for _, tt := range tests {
			t.Run(tt.name, tt.check)
		}
	})
}

// TestChain checks batch roots against the stored chain (ingest-v1 2.7;
// ledger-v1 6), and the receipts (ledger-v1 7).
func TestChain(t *testing.T) {
	engines(t, func(t *testing.T, engine store.Engine) {
		h := newHarness(t, engine)
		dev := h.device("d_test", false)
		late := h.device("d_late", false)
		rng := rand.New(rand.NewPCG(3, 4))
		roots := chain(rng, ledger.Hash{}, 1, 6)
		lateRoots := chain(rng, ledger.Hash{9}, 5, 2)
		var firstReceipt *receiptJSON
		one := func(t *testing.T, d *testDevice, x root) batchResult {
			t.Helper()
			resp := h.ok(t, upload{dev: d, body: batchesBody(x)})
			if len(resp.Batches) != 1 {
				t.Fatalf("%d batch results for 1 batch root", len(resp.Batches))
			}
			return resp.Batches[0]
		}
		want := func(t *testing.T, got batchResult, status int, code string) {
			t.Helper()
			if got.Status != status || got.Code != code {
				t.Errorf("got %d %s, want %d %s", got.Status, got.Code, status, code)
			}
		}
		tests := []struct {
			name  string
			check func(t *testing.T)
		}{
			{"batch 1 with a prev other than zero is prev_mismatch", func(t *testing.T) {
				x := roots[0]
				x.prev = ledger.Hash{1}
				want(t, one(t, dev, x.relink()), 409, CodePrevMismatch)
			}},
			{"batch 1 starts the chain, with a receipt that verifies under the server key", func(t *testing.T) {
				got := one(t, dev, roots[0])
				want(t, got, 200, CodeAccepted)
				if err := verifyReceipt(h, dev.ID, 1, roots[0].head.String(), got.Receipt); err != nil {
					t.Error(err)
				}
				if got.Receipt != nil && got.Receipt.ReceivedAt != h.clock.UnixMilli() {
					t.Errorf("received_at %d, want the server's clock", got.Receipt.ReceivedAt)
				}
				firstReceipt = got.Receipt
			}},
			{"batch 1 again is a duplicate with its first receipt", func(t *testing.T) {
				h.clock = h.clock.Add(time.Minute)
				got := one(t, dev, roots[0])
				want(t, got, 200, CodeDuplicate)
				if got.Receipt == nil || firstReceipt == nil || *got.Receipt != *firstReceipt {
					t.Errorf("receipt %+v, want the first, %+v", got.Receipt, firstReceipt)
				}
			}},
			{"batch 1 with another root is batch_conflict, and the stored row stays", func(t *testing.T) {
				x := roots[0]
				x.r = ledger.Hash{7}
				want(t, one(t, dev, x.relink()), 409, CodeBatchConflict)
				want(t, one(t, dev, roots[0]), 200, CodeDuplicate)
			}},
			{"batch 3 before batch 2 is chain_gap", func(t *testing.T) { want(t, one(t, dev, roots[2]), 409, CodeChainGap) }},
			{"batch 2 whose prev isn't the stored head is prev_mismatch", func(t *testing.T) {
				x := roots[1]
				x.prev = ledger.Hash{2}
				want(t, one(t, dev, x.relink()), 409, CodePrevMismatch)
			}},
			{"batch 2 whose head doesn't follow from its fields is head_mismatch", func(t *testing.T) {
				x := roots[1]
				x.head[0] ^= 1
				want(t, one(t, dev, x), 422, CodeHeadMismatch)
			}},
			{"batch 2 with no records is batch_empty", func(t *testing.T) {
				x := roots[1]
				x.n = 0
				want(t, one(t, dev, x.relink()), 422, CodeBatchEmpty)
			}},
			{"batches 2 to 5 in one request are each accepted, in order", func(t *testing.T) {
				resp := h.ok(t, upload{dev: dev, body: batchesBody(roots[1:5]...)})
				for i, got := range resp.Batches {
					want(t, got, 200, CodeAccepted)
					if got.B != roots[1+i].b {
						t.Errorf("result %d names batch %d, want %d", i, got.B, roots[1+i].b)
					}
				}
				if resp.Head == nil || resp.Head.B != 5 || resp.Head.Head != roots[4].head {
					t.Errorf("head %+v, want batch 5", resp.Head)
				}
			}},
			{"a batch out of place in a run is refused alone, and the run goes on from the stored head", func(t *testing.T) {
				resp := h.ok(t, upload{dev: dev, body: batchesBody(chain(rng, roots[4].head, 7, 1)[0], roots[5])})
				want(t, resp.Batches[0], 409, CodeChainGap)
				want(t, resp.Batches[1], 200, CodeAccepted)
			}},
			{"a batch root whose R_b fails its form still names its b", func(t *testing.T) {
				m := roots[5].json()
				m["b"], m["R_b"] = 9, "zz"
				resp := h.ok(t, upload{dev: dev, body: map[string]any{"batches": []any{m}}})
				if got := resp.Batches[0]; got.B != 9 || got.Code != CodeBadString || got.Field == nil || *got.Field != "R_b" {
					t.Errorf("got %+v", got)
				}
			}},
			{"a batch root that repeats b is bad_rule, and its result names no b", func(t *testing.T) {
				raw := strings.Replace(string(marshal(t, roots[5].json())), `"b":6`, `"b":6,"b":6`, 1)
				resp := h.ok(t, upload{dev: dev, json: []byte(`{"batches":[` + raw + `]}`)})
				if got := resp.Batches[0]; got.Status != 422 || got.Code != CodeBadRule || got.B != 0 || got.Field == nil || *got.Field != "" {
					t.Errorf("got %+v", got)
				}
			}},
			{"a device's first batch may be any b, its prev taken as given", func(t *testing.T) {
				want(t, one(t, late, lateRoots[0]), 200, CodeAccepted)
				if st := h.state(t, late); st.ChainStart == nil || *st.ChainStart != 5 {
					t.Errorf("chain_start %v, want 5", st.ChainStart)
				}
			}},
			{"after that, a batch below its chain's start is before_chain", func(t *testing.T) {
				x := root{b: 3, n: 1, prev: ledger.Hash{4}}.relink()
				want(t, one(t, late, x), 409, CodeBeforeChain)
			}},
			{"and its next batch links to the head as any other", func(t *testing.T) {
				want(t, one(t, late, lateRoots[1]), 200, CodeAccepted)
			}},
			{"the witness rows hold DESIGN 3.6's fields with received_at", func(t *testing.T) {
				var n, received int64
				var rootHex, headHex, prevHex, account string
				err := h.db.Read.QueryRow(`SELECT n, root, head, prev, received_at_ms, account_id FROM witness_rows WHERE device_id = $1 AND b = $2`, dev.ID, 1).
					Scan(&n, &rootHex, &headHex, &prevHex, &received, &account)
				if err != nil {
					t.Fatal(err)
				}
				if uint64(n) != roots[0].n || rootHex != roots[0].r.String() || headHex != roots[0].head.String() || prevHex != (ledger.Hash{}).String() ||
					received != firstReceipt.ReceivedAt || account != dev.Account {
					t.Errorf("row (%d, %s, %s, %s, %d, %s)", n, rootHex, headHex, prevHex, received, account)
				}
			}},
		}
		for _, tt := range tests {
			t.Run(tt.name, tt.check)
		}
	})
}

// TestRandomChains uploads random valid chains in requests of random size,
// and before each request tries one single change to its first batch
// root, which the server must refuse (PLAN WP-SV4). A device's first
// request never gets the skipped batch: a device with no head starts its
// chain at any b, its prev taken as given (ingest-v1 2.7 step 4), so a
// correct server accepts it.
func TestRandomChains(t *testing.T) {
	type mutation struct {
		name string
		edit func(x root) root
	}
	flip := func(h ledger.Hash) ledger.Hash { h[31] ^= 0x80; return h }
	mutations := []mutation{
		{"b one higher", func(x root) root { x.b++; return x }},
		{"b one lower", func(x root) root { x.b--; return x }},
		{"n one higher", func(x root) root { x.n++; return x }},
		{"n of 0", func(x root) root { x.n = 0; return x }},
		{"a bit of R_b", func(x root) root { x.r = flip(x.r); return x }},
		{"a bit of H_b", func(x root) root { x.head = flip(x.head); return x }},
		{"a bit of prev", func(x root) root { x.prev = flip(x.prev); return x }},
		{"a prev that isn't the head, with H_b made to follow", func(x root) root { x.prev = flip(x.prev); return x.relink() }},
		{"a skipped batch, with H_b made to follow", func(x root) root { x.b++; return x.relink() }},
		{"a stored batch rewritten, with H_b made to follow", func(x root) root { x.b--; x.r = flip(x.r); return x.relink() }},
	}
	// startsAChain is the mutation a device with no head takes as its
	// chain's start.
	const startsAChain = "a skipped batch, with H_b made to follow"
	first := slices.DeleteFunc(slices.Clone(mutations), func(m mutation) bool { return m.name == startsAChain })
	if len(first) != len(mutations)-1 {
		t.Fatalf("no mutation is named %q", startsAChain)
	}
	engines(t, func(t *testing.T, engine store.Engine) {
		h := newHarness(t, engine)
		rng := rand.New(rand.NewPCG(5, 6))
		for c := 0; c < 4; c++ {
			dev := h.device("d_chain"+strconv.Itoa(c), false)
			prev, b := ledger.Hash{}, uint64(1)
			for b <= 30 {
				roots := chain(rng, prev, b, 1+rng.IntN(5))
				pool := mutations
				if b == 1 {
					pool = first
				}
				m := pool[rng.IntN(len(pool))]
				t.Run(fmt.Sprintf("%s: batch %d with %s is refused", dev.ID, b, m.name), func(t *testing.T) {
					resp := h.ok(t, upload{dev: dev, body: batchesBody(m.edit(roots[0]))})
					if got := resp.Batches[0]; got.Status == http.StatusOK {
						t.Errorf("got %d %s, want a refusal", got.Status, got.Code)
					}
				})
				t.Run(fmt.Sprintf("%s: batches %d to %d are accepted", dev.ID, b, b+uint64(len(roots))-1), func(t *testing.T) {
					resp := h.ok(t, upload{dev: dev, body: batchesBody(roots...)})
					for i, got := range resp.Batches {
						if got.Code != CodeAccepted {
							t.Errorf("batch %d: %d %s", roots[i].b, got.Status, got.Code)
						} else if err := verifyReceipt(h, dev.ID, roots[i].b, roots[i].head.String(), got.Receipt); err != nil {
							t.Errorf("batch %d: %v", roots[i].b, err)
						}
					}
				})
				prev, b = roots[len(roots)-1].head, b+uint64(len(roots))
			}
		}
	})
}

// bucketsBody is a body of buckets as their Shareable JSON.
func bucketsBody(t testing.TB, mbs ...event.MinuteBucket) map[string]any {
	items := make([]any, len(mbs))
	for i, mb := range mbs {
		items[i] = json.RawMessage(shareableJSON(t, mb))
	}
	return map[string]any{"buckets": items}
}

// TestBuckets checks how bucket revisions are stored (ingest-v1 2.7).
func TestBuckets(t *testing.T) {
	engines(t, func(t *testing.T, engine store.Engine) {
		h := newHarness(t, engine)
		dev := h.device("d_test", false)
		minute := exampleClock.Truncate(time.Minute)
		code := func(t *testing.T, mbs ...event.MinuteBucket) []string {
			t.Helper()
			resp := h.ok(t, upload{dev: dev, body: bucketsBody(t, mbs...)})
			var out []string
			for _, r := range resp.Buckets {
				out = append(out, r.Code)
			}
			return out
		}
		// stored returns the minute's current rev, its account, and every
		// revision minute_bucket_revisions holds, as "rev@received_at".
		stored := func(t *testing.T) (rev int64, account string, revisions []string) {
			t.Helper()
			var count int64
			if err := h.db.Read.QueryRow(`SELECT rev, account_id, revisions FROM minute_buckets WHERE device_id = $1 AND minute_ms = $2`,
				dev.ID, minute.UnixMilli()).Scan(&rev, &account, &count); err != nil {
				t.Fatal(err)
			}
			rows, err := h.db.Read.Query(`SELECT rev, received_at_ms FROM minute_bucket_revisions WHERE device_id = $1 AND minute_ms = $2 ORDER BY rev`,
				dev.ID, minute.UnixMilli())
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for rows.Next() {
				var r, at int64
				if err := rows.Scan(&r, &at); err != nil {
					t.Fatal(err)
				}
				revisions = append(revisions, fmt.Sprintf("%d@%d", r, at))
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if count != int64(len(revisions)) {
				t.Errorf("minute_buckets counts %d revisions, and minute_bucket_revisions holds %d", count, len(revisions))
			}
			return rev, account, revisions
		}
		results := func(t *testing.T, mbs ...event.MinuteBucket) []itemResult {
			t.Helper()
			return h.ok(t, upload{dev: dev, body: bucketsBody(t, mbs...)}).Buckets
		}
		rev3 := sampleBucket(dev.ID, minute, 3)
		other3 := sampleBucket(dev.ID, minute, 3)
		other3.AgentRunningS = 45
		wrongDevice := sampleBucket("d_someone_else", minute, 1)
		tests := []struct {
			name  string
			check func(t *testing.T)
		}{
			{"a minute's first revision is accepted under the device's account", func(t *testing.T) {
				if got := code(t, sampleBucket(dev.ID, minute, 2)); got[0] != CodeAccepted {
					t.Fatalf("got %q", got)
				}
				if rev, account, revisions := stored(t); rev != 2 || account != dev.Account || len(revisions) != 1 {
					t.Errorf("stored rev %d for %s with revisions %q", rev, account, revisions)
				}
			}},
			{"the same revision again is a duplicate", func(t *testing.T) {
				if got := code(t, sampleBucket(dev.ID, minute, 2)); got[0] != CodeDuplicate {
					t.Errorf("got %q", got)
				}
			}},
			{"a higher revision replaces it, and every revision is kept with its received_at", func(t *testing.T) {
				h.clock = h.clock.Add(30 * time.Second)
				if got := code(t, rev3); got[0] != CodeAccepted {
					t.Fatalf("got %q", got)
				}
				rev, _, revisions := stored(t)
				want := []string{fmt.Sprintf("2@%d", exampleClock.UnixMilli()), fmt.Sprintf("3@%d", h.clock.UnixMilli())}
				if rev != 3 || !slices.Equal(revisions, want) {
					t.Errorf("stored rev %d with revisions %q, want 3 and %q", rev, revisions, want)
				}
			}},
			{"a lower revision is superseded, changes nothing, and names the stored rev", func(t *testing.T) {
				got := results(t, sampleBucket(dev.ID, minute, 1))
				if got[0].Code != CodeSuperseded || got[0].Rev == nil || *got[0].Rev != 3 {
					t.Errorf("got %+v", got[0])
				}
				if rev, _, _ := stored(t); rev != 3 {
					t.Errorf("stored rev %d, want 3", rev)
				}
			}},
			{"the same rev with another bucket is rev_conflict, names the stored rev, and the first stays", func(t *testing.T) {
				got := results(t, other3)
				if got[0].Status != 409 || got[0].Code != CodeRevConflict || got[0].Rev == nil || *got[0].Rev != 3 {
					t.Errorf("got %+v", got[0])
				}
			}},
			{"the same bucket stored in another encoding, as an earlier release wrote it, is still a duplicate", func(t *testing.T) {
				// Through a map, the stored bucket is encoded again with its
				// members sorted by name, as a release whose field order
				// differs would have stored it.
				var cur string
				if err := h.db.Read.QueryRow(`SELECT bucket FROM minute_buckets WHERE device_id = $1 AND minute_ms = $2`, dev.ID, minute.UnixMilli()).Scan(&cur); err != nil {
					t.Fatal(err)
				}
				resorted := string(marshal(t, asMap(t, []byte(cur))))
				if resorted == cur {
					t.Fatal("the new encoding is the same bytes, so this tests nothing")
				}
				if err := h.writer.Do(context.Background(), 1, func(ctx context.Context, tx writer.Tx) error {
					_, err := tx.ExecContext(ctx, `UPDATE minute_buckets SET bucket = $3 WHERE device_id = $1 AND minute_ms = $2`, dev.ID, minute.UnixMilli(), resorted)
					return err
				}); err != nil {
					t.Fatal(err)
				}
				if got := code(t, rev3); got[0] != CodeDuplicate {
					t.Errorf("got %q, want duplicate", got)
				}
				if got := results(t, other3); got[0].Code != CodeRevConflict {
					t.Errorf("another bucket at the same rev: got %+v, want rev_conflict", got[0])
				}
			}},
			{"the rollups moved with each stored revision, inside its transaction", func(t *testing.T) {
				want := []rollupCall{
					{account: dev.Account, prevRev: -1, nextRev: 2, storedRev: 2, minuteMS: minute.UnixMilli()},
					{account: dev.Account, prevRev: 2, nextRev: 3, storedRev: 3, minuteMS: minute.UnixMilli()},
				}
				if len(h.rollups) != len(want) {
					t.Fatalf("rollup calls %+v, want %+v", h.rollups, want)
				}
				for i := range want {
					if h.rollups[i] != want[i] {
						t.Errorf("call %d: %+v, want %+v", i, h.rollups[i], want[i])
					}
				}
			}},
			{"a bucket of another device is wrong_device", func(t *testing.T) {
				if got := code(t, wrongDevice); got[0] != CodeWrongDevice {
					t.Errorf("got %q", got)
				}
			}},
			{"a minute starting more than 300 s ahead is future_minute", func(t *testing.T) {
				if got := code(t, sampleBucket(dev.ID, h.clock.Truncate(time.Minute).Add(6*time.Minute), 0)); got[0] != CodeFutureMinute {
					t.Errorf("got %q", got)
				}
			}},
			{"a minute 180 days old is expired_minute", func(t *testing.T) {
				old := h.clock.Add(-180 * 24 * time.Hour).Truncate(time.Minute)
				if got := code(t, sampleBucket(dev.ID, old, 0)); got[0] != CodeExpiredMinute {
					t.Errorf("got %q", got)
				}
			}},
			{"a bucket with free text is refused alone, and the one beside it stored", func(t *testing.T) {
				bad := asMap(t, shareableJSON(t, sampleBucket(dev.ID, minute.Add(time.Minute), 0)))
				slot(bad, 0)["p"] = "/home/teen/secret-project"
				resp := h.ok(t, upload{dev: dev, body: map[string]any{"buckets": []any{bad, json.RawMessage(shareableJSON(t, sampleBucket(dev.ID, minute.Add(2*time.Minute), 0)))}}})
				if len(resp.Buckets) != 2 {
					t.Fatalf("%d results", len(resp.Buckets))
				}
				if b := resp.Buckets[0]; b.Status != 422 || b.Code != CodeBadString || b.Field == nil || *b.Field != "slots[0].p" {
					t.Errorf("first: %+v", b)
				}
				if b := resp.Buckets[1]; b.Code != CodeAccepted {
					t.Errorf("second: %+v", b)
				}
				var n int
				if err := h.db.Read.QueryRow(`SELECT COUNT(*) FROM minute_buckets WHERE bucket LIKE '%secret%'`).Scan(&n); err != nil || n != 0 {
					t.Errorf("%d stored buckets hold the text (err %v)", n, err)
				}
			}},
		}
		for _, tt := range tests {
			t.Run(tt.name, tt.check)
		}
	})
}

// TestCommitsAndLabels checks how commit records and labels are stored
// (ingest-v1 2.7).
func TestCommitsAndLabels(t *testing.T) {
	engines(t, func(t *testing.T, engine store.Engine) {
		h := newHarness(t, engine)
		dev := h.device("d_test", false)
		changed := sampleCommit()
		changed["credited_seconds_since_parent"] = map[string]any{"prompting": 300}
		firstSeen := h.clock.UnixMilli()
		commit := func(t *testing.T, c map[string]any) string {
			t.Helper()
			resp := h.ok(t, upload{dev: dev, body: map[string]any{"commits": []any{c}}})
			return resp.Commits[0].Code
		}
		tests := []struct {
			name  string
			check func(t *testing.T)
		}{
			{"a commit record is accepted", func(t *testing.T) {
				if got := commit(t, sampleCommit()); got != CodeAccepted {
					t.Errorf("got %s", got)
				}
			}},
			{"the same record again is a duplicate", func(t *testing.T) {
				if got := commit(t, sampleCommit()); got != CodeDuplicate {
					t.Errorf("got %s", got)
				}
			}},
			{"a changed record of the same commit is updated, keeping its first sighting", func(t *testing.T) {
				h.clock = h.clock.Add(time.Hour)
				if got := commit(t, changed); got != CodeUpdated {
					t.Errorf("got %s", got)
				}
				var first, updated int64
				var body string
				if err := h.db.Read.QueryRow(`SELECT first_seen_at_ms, updated_at_ms, body FROM commit_records WHERE device_id = $1 AND sha = $2`,
					dev.ID, strings.Repeat("a", 40)).Scan(&first, &updated, &body); err != nil {
					t.Fatal(err)
				}
				if first != firstSeen || updated != h.clock.UnixMilli() || !strings.Contains(body, `"credited_seconds_since_parent":{"prompting":300}`) {
					t.Errorf("first %d, updated %d, body %s", first, updated, body)
				}
			}},
			{"labels are accepted and stored as the device's", func(t *testing.T) {
				resp := h.ok(t, upload{dev: dev, body: map[string]any{"labels": sampleLabels()}})
				if resp.Labels == nil || resp.Labels.Code != CodeAccepted {
					t.Fatalf("labels result %+v", resp.Labels)
				}
				var labels string
				if err := h.db.Read.QueryRow(`SELECT labels FROM device_labels WHERE device_id = $1`, dev.ID).Scan(&labels); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(labels, `"claude_code"`) {
					t.Errorf("stored labels %s", labels)
				}
			}},
			{"labels sent as {} mean none, and their result is null", func(t *testing.T) {
				if resp := h.ok(t, upload{dev: dev, json: []byte(`{"labels":{}}`)}); resp.Labels != nil {
					t.Errorf("labels result %+v, want null", *resp.Labels)
				}
			}},
			{"a request without labels answers null for them, and [] for arrays it didn't send", func(t *testing.T) {
				rec := h.send(t, upload{dev: dev})
				if !strings.Contains(rec.Body.String(), `"batches":[],"buckets":[],"commits":[],"labels":null`) {
					t.Errorf("body %s", rec.Body)
				}
			}},
		}
		for _, tt := range tests {
			t.Run(tt.name, tt.check)
		}
	})
}

// TestState checks GET /api/v1/ingest/state (ingest-v1 3).
func TestState(t *testing.T) {
	engines(t, func(t *testing.T, engine store.Engine) {
		h := newHarness(t, engine)
		dev := h.device("d_test", false)
		roots := chain(rand.New(rand.NewPCG(8, 9)), ledger.Hash{}, 1, 2)
		// get sends dev's signed state request after edit.
		get := func(t *testing.T, edit func(r *http.Request)) *httptest.ResponseRecorder {
			r := h.stateRequest(dev, dev.key)
			edit(r)
			return h.serve(t, r)
		}
		header := func(name, v string) func(*http.Request) {
			return func(r *http.Request) {
				if v == "" {
					r.Header.Del(name)
				} else {
					r.Header.Set(name, v)
				}
			}
		}
		tests := []struct {
			name  string
			check func(t *testing.T)
		}{
			{"before any upload the counter is 0 and there is no head", func(t *testing.T) {
				st := h.state(t, dev)
				if st.Device != dev.ID || st.Req != 0 || st.Head != nil || st.ChainStart != nil || st.Relinked || st.Gaps == nil || len(st.Gaps) != 0 {
					t.Errorf("state %+v", st)
				}
			}},
			{"the state names the server key receipts verify under", func(t *testing.T) {
				st := h.state(t, dev)
				pub := h.server.Public().(ed25519.PublicKey)
				if st.ServerKey.PublicKey != hex.EncodeToString(pub) || st.ServerKey.KeyID != backup.KeyID(pub) {
					t.Errorf("server key %+v", st.ServerKey)
				}
			}},
			{"after uploads it gives the counter, the head with its received_at, and the chain's start", func(t *testing.T) {
				h.ok(t, upload{dev: dev, body: batchesBody(roots...)})
				st := h.state(t, dev)
				if st.Req != dev.req || st.Head == nil || st.Head.B != 2 || st.Head.Head != roots[1].head ||
					st.Head.ReceivedAt != h.clock.UnixMilli() || st.ChainStart == nil || *st.ChainStart != 1 {
					t.Errorf("state %+v", st)
				}
			}},
			{"no token is token_missing", func(t *testing.T) {
				if s, c := refusalOf(t, get(t, header("Authorization", ""))); s != 401 || c != CodeTokenMissing {
					t.Errorf("got %d %s", s, c)
				}
			}},
			{"another device's id is device_mismatch", func(t *testing.T) {
				if s, c := refusalOf(t, get(t, header(HeaderDevice, "d_other"))); s != 403 || c != CodeDeviceMismatch {
					t.Errorf("got %d %s", s, c)
				}
			}},
			{"a state request without its signature is signature_malformed, so a token alone reads nothing", func(t *testing.T) {
				if s, c := refusalOf(t, get(t, header(HeaderSignature, ""))); s != 400 || c != CodeSignatureMalformed {
					t.Errorf("got %d %s", s, c)
				}
			}},
			{"a state request without its time is time_malformed", func(t *testing.T) {
				if s, c := refusalOf(t, get(t, header(HeaderTime, ""))); s != 400 || c != CodeTimeMalformed {
					t.Errorf("got %d %s", s, c)
				}
			}},
			{"a state request signed 301 s ago is clock_skew", func(t *testing.T) {
				tm := strconv.FormatInt(h.clock.UnixMilli()-301_000, 10)
				rec := get(t, func(r *http.Request) {
					r.Header.Set(HeaderTime, tm)
					r.Header.Set(HeaderSignature, signaturePrefix+base64.StdEncoding.EncodeToString(ed25519.Sign(dev.key, StateMessage(dev.ID, tm))))
				})
				if s, c := refusalOf(t, rec); s != 401 || c != CodeClockSkew {
					t.Errorf("got %d %s", s, c)
				}
			}},
			{"a state request signed by another key is signature_invalid, naming the device's key", func(t *testing.T) {
				rec := h.serve(t, h.stateRequest(dev, seedKey("another key")))
				var e errorBody
				if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil || rec.Code != 401 || e.Error != CodeSignatureInvalid || e.DeviceKeyID != backup.KeyID(dev.PublicKey) {
					t.Errorf("got %d %s", rec.Code, rec.Body)
				}
			}},
			{"an upload's signature doesn't pass for a state request", func(t *testing.T) {
				tm := strconv.FormatInt(h.clock.UnixMilli(), 10)
				m := Message(dev.ID, "1", tm, strings.Repeat("0", 64))
				rec := get(t, func(r *http.Request) {
					r.Header.Set(HeaderTime, tm)
					r.Header.Set(HeaderSignature, signaturePrefix+base64.StdEncoding.EncodeToString(ed25519.Sign(dev.key, m)))
				})
				if s, c := refusalOf(t, rec); s != 401 || c != CodeSignatureInvalid {
					t.Errorf("got %d %s", s, c)
				}
			}},
		}
		for _, tt := range tests {
			t.Run(tt.name, tt.check)
		}
	})
}

// TestTimeAndOff checks GET /api/v1/time, and that ingest without a device
// store or a server key answers ingest_off (ingest-v1 2.6, 4).
func TestTimeAndOff(t *testing.T) {
	clock := exampleClock
	off := New(Options{Now: func() time.Time { return clock }, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	mux := http.NewServeMux()
	off.Register(mux)
	do := func(method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		return rec
	}
	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"GET /api/v1/time answers the server's clock without a token", func(t *testing.T) {
			rec := do(http.MethodGet, TimePath)
			if rec.Code != 200 || rec.Body.String() != `{"server_time":1795089660250}` || rec.Header().Get(HeaderTime) != "1795089660250" {
				t.Errorf("got %d %s, X-Pairtime-Time %q", rec.Code, rec.Body, rec.Header().Get(HeaderTime))
			}
		}},
		{"an upload to a server without a device store is ingest_off", func(t *testing.T) {
			rec := do(http.MethodPost, BatchesPath)
			if s, c := refusalOf(t, rec); s != 503 || c != CodeIngestOff || rec.Header().Get("Retry-After") == "" {
				t.Errorf("got %d %s", s, c)
			}
		}},
		{"the state of a server without a device store is ingest_off", func(t *testing.T) {
			if s, c := refusalOf(t, do(http.MethodGet, StatePath)); s != 503 || c != CodeIngestOff {
				t.Errorf("got %d %s", s, c)
			}
		}},
		{"a server with devices, a writer and a read pool but no server key is off", func(t *testing.T) {
			h := newHarness(t, store.SQLite)
			dev := h.device("d_test", false)
			keyless := New(Options{Devices: h.devices, Writer: h.writer, Read: h.db.Read, Now: func() time.Time { return h.clock },
				Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
			if keyless.On() {
				t.Error("on without a key")
			}
			mux := http.NewServeMux()
			keyless.Register(mux)
			for _, r := range []*http.Request{h.request(t, upload{dev: dev}), h.stateRequest(dev, dev.key)} {
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, r)
				if s, c := refusalOf(t, rec); s != 503 || c != CodeIngestOff {
					t.Errorf("%s %s: got %d %s, want 503 %s", r.Method, r.URL.Path, s, c, CodeIngestOff)
				}
			}
		}},
		{"other methods get 405 from the router", func(t *testing.T) {
			if rec := do(http.MethodGet, BatchesPath); rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("got %d", rec.Code)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.check)
	}
}

// TestObserve checks what ingest reports for /metrics: the batch roots
// each processed upload stored, and nothing for a replay or a refusal.
func TestObserve(t *testing.T) {
	engines(t, func(t *testing.T, engine store.Engine) {
		h := newHarness(t, engine)
		dev := h.device("d_test", false)
		roots := chain(rand.New(rand.NewPCG(10, 11)), ledger.Hash{}, 1, 2)
		first := upload{dev: dev, body: batchesBody(roots...), key: h.newKey()}
		tests := []struct {
			name string
			send func(t *testing.T)
			want []int
		}{
			{"an upload that stores two batch roots reports 2", func(t *testing.T) { h.ok(t, first) }, []int{2}},
			{"a replay reports nothing", func(t *testing.T) { h.ok(t, upload{dev: dev, body: first.body, key: first.key}) }, []int{2}},
			{"the same roots under a new key are duplicates, and report 0", func(t *testing.T) { h.ok(t, upload{dev: dev, body: first.body}) }, []int{2, 0}},
			{"a refused request reports nothing", func(t *testing.T) { h.send(t, upload{dev: dev, signer: seedKey("another key")}) }, []int{2, 0}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				tt.send(t)
				if !slices.Equal(h.observed, tt.want) {
					t.Errorf("observed %v, want %v", h.observed, tt.want)
				}
			})
		}
	})
}

// TestSignatureNamesTheKey checks that signature_invalid names the
// device's key, so a client can tell a key the server lacks
// from a fault (ingest-v1 2.6).
func TestSignatureNamesTheKey(t *testing.T) {
	h := newHarness(t, store.SQLite)
	dev := h.device("d_test", false)
	rec := h.send(t, upload{dev: dev, signer: seedKey("another key")})
	var e errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, got, want string
	}{
		{"the refusal is signature_invalid", e.Error, CodeSignatureInvalid},
		{"device_key_id is the device key's id", e.DeviceKeyID, backup.KeyID(dev.PublicKey)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

// TestAgentOnly checks D-bucket-agent-only's agent_only{p: s}: a bucket
// carries it, the revision stores it, and the rollups get it.
func TestAgentOnly(t *testing.T) {
	engines(t, func(t *testing.T, engine store.Engine) {
		h := newHarness(t, engine)
		dev := h.device("d_test", false)
		m := asMap(t, shareableJSON(t, sampleBucket(dev.ID, exampleClock.Truncate(time.Minute), 1)))
		m["agent_only"] = map[string]any{"p_two": 7, "p_one": 12.5}
		resp := h.ok(t, upload{dev: dev, body: map[string]any{"buckets": []any{m}}})
		var stored string
		if err := h.db.Read.QueryRow(`SELECT bucket FROM minute_buckets WHERE device_id = $1`, dev.ID).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		const want = `{"p_one":12.5,"p_two":7}`
		tests := []struct {
			name string
			ok   bool
		}{
			{"a bucket with agent_only is accepted", len(resp.Buckets) == 1 && resp.Buckets[0].Code == CodeAccepted},
			{"the stored revision keeps agent_only", strings.Contains(stored, `"agent_only":`+want)},
			{"the rollups get agent_only with the revision", len(h.rollups) == 1 && h.rollups[0].agentOnly == want},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if !tt.ok {
					t.Errorf("no: response %+v, stored %s, rollups %+v", resp.Buckets, stored, h.rollups)
				}
			})
		}
	})
}

// TestRevisionCap checks that one minute keeps at most MaxRevisions
// revisions, so no client can make its history grow without end
// (ingest-v1 2.7).
func TestRevisionCap(t *testing.T) {
	engines(t, func(t *testing.T, engine store.Engine) {
		h := newHarness(t, engine)
		dev := h.device("d_test", false)
		minute := exampleClock.Truncate(time.Minute)
		var mbs []event.MinuteBucket
		for rev := uint32(1); rev <= MaxRevisions+1; rev++ {
			mbs = append(mbs, sampleBucket(dev.ID, minute, rev))
		}
		resp := h.ok(t, upload{dev: dev, body: bucketsBody(t, mbs...)})
		var rev, count int64
		if err := h.db.Read.QueryRow(`SELECT rev, revisions FROM minute_buckets WHERE device_id = $1 AND minute_ms = $2`, dev.ID, minute.UnixMilli()).Scan(&rev, &count); err != nil {
			t.Fatal(err)
		}
		last := resp.Buckets[len(resp.Buckets)-1]
		tests := []struct {
			name string
			ok   bool
		}{
			{"the first 128 revisions are accepted", func() bool {
				for _, r := range resp.Buckets[:MaxRevisions] {
					if r.Code != CodeAccepted {
						return false
					}
				}
				return true
			}()},
			{"the 129th is too_many_revisions, naming the stored rev", last.Status == 409 && last.Code == CodeTooManyRevisions && last.Rev != nil && *last.Rev == MaxRevisions},
			{"the minute keeps rev 128 and counts 128 revisions", rev == MaxRevisions && count == MaxRevisions},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if !tt.ok {
					t.Errorf("no: last result %+v, stored rev %d of %d", last, rev, count)
				}
			})
		}
	})
}

// TestOrigins checks request_origins: where each processed upload came
// from, as an address and a JA4 fingerprint and never as text a client
// wrote (DESIGN 4.4; SP 8.7).
func TestOrigins(t *testing.T) {
	engines(t, func(t *testing.T, engine store.Engine) {
		h := newHarness(t, engine)
		dev := h.device("d_test", false)
		count := func(t *testing.T) int {
			var n int
			if err := h.db.Read.QueryRow(`SELECT COUNT(*) FROM request_origins WHERE device_id = $1`, dev.ID).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		latest := func(t *testing.T, headers map[string]string) (string, string) {
			t.Helper()
			h.clock = h.clock.Add(time.Second)
			h.ok(t, upload{dev: dev, edit: func(r *http.Request) {
				for k, v := range headers {
					r.Header.Set(k, v)
				}
			}})
			var ip string
			var ja4 sql.NullString
			if err := h.db.Read.QueryRow(`SELECT ip, ja4 FROM request_origins WHERE device_id = $1 AND seen_at_ms = $2`, dev.ID, h.clock.UnixMilli()).Scan(&ip, &ja4); err != nil {
				t.Fatal(err)
			}
			return ip, ja4.String
		}
		tests := []struct {
			name            string
			headers         map[string]string
			wantIP, wantJA4 string
		}{
			{"without Cloudflare's headers, the connection's address is kept", nil, "192.0.2.1", ""},
			{"CF-Connecting-IP is kept", map[string]string{"CF-Connecting-IP": "203.0.113.7"}, "203.0.113.7", ""},
			{"an IPv6 address loses its zone, which could hold text", map[string]string{"CF-Connecting-IP": "fe80::1%fix-the-bug"}, "fe80::1", ""},
			{"a CF-Connecting-IP that isn't an address falls back to the connection's", map[string]string{"CF-Connecting-IP": "my laptop"}, "192.0.2.1", ""},
			{"a CF-JA4 in JA4's form is kept", map[string]string{"CF-JA4": "t13d1516h2_8daaf6152771_b186095e22b6"}, "192.0.2.1", "t13d1516h2_8daaf6152771_b186095e22b6"},
			{"a CF-JA4 in any other form is dropped", map[string]string{"CF-JA4": "Mozilla/5.0 (teen's laptop)"}, "192.0.2.1", ""},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if ip, ja4 := latest(t, tt.headers); ip != tt.wantIP || ja4 != tt.wantJA4 {
					t.Errorf("stored %q and %q, want %q and %q", ip, ja4, tt.wantIP, tt.wantJA4)
				}
			})
		}
		t.Run("a refused upload leaves no origin", func(t *testing.T) {
			before := count(t)
			h.send(t, upload{dev: dev, signer: seedKey("another key")})
			if after := count(t); after != before {
				t.Errorf("%d origins, then %d", before, after)
			}
		})
	})
}

// TestRate checks each device's upload rate (SP 8.6; ingest-v1 2.5).
func TestRate(t *testing.T) {
	h := newHarness(t, store.SQLite)
	h.svc.rate = newLimiter(3, 10*time.Second)
	dev := h.device("d_test", false)
	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"three uploads fit a burst of three", func(t *testing.T) {
			for range 3 {
				h.ok(t, upload{dev: dev})
			}
		}},
		{"a fourth is rate_limited, with Retry-After of one refill", func(t *testing.T) {
			rec := h.send(t, upload{dev: dev})
			if s, c := refusalOf(t, rec); s != 429 || c != CodeRateLimited || rec.Header().Get("Retry-After") != "10" {
				t.Errorf("got %d %s, Retry-After %q", s, c, rec.Header().Get("Retry-After"))
			}
		}},
		{"an upload signed by another key is refused for its signature, before the rate", func(t *testing.T) {
			if s, c := refusalOf(t, h.send(t, upload{dev: dev, signer: seedKey("another key")})); s != 401 || c != CodeSignatureInvalid {
				t.Errorf("got %d %s", s, c)
			}
		}},
		{"ten seconds later one more fits", func(t *testing.T) {
			h.clock = h.clock.Add(10 * time.Second)
			h.ok(t, upload{dev: dev})
		}},
		{"a state request counts against the same rate, and fits once it refills", func(t *testing.T) {
			if s, c := refusalOf(t, h.serve(t, h.stateRequest(dev, dev.key))); s != 429 || c != CodeRateLimited {
				t.Errorf("got %d %s", s, c)
			}
			h.clock = h.clock.Add(10 * time.Second)
			h.state(t, dev)
		}},
		{"a second request while one is in flight is refused, and fits once the first ends", func(t *testing.T) {
			l := newLimiter(5, time.Second)
			done, r1 := l.take("d_x", exampleClock)
			_, r2 := l.take("d_x", exampleClock)
			if r1 != nil || r2 == nil || r2.code != CodeRateLimited || r2.retryAfter != 1 {
				t.Fatalf("first %v, second %v", r1, r2)
			}
			done()
			if _, r3 := l.take("d_x", exampleClock); r3 != nil {
				t.Errorf("after the first ended: %v", r3)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.check)
	}
}

// TestHeadFromWitnessRows checks a device whose ingest state is gone, as
// retention removes it 180 days after its last upload, while its witness
// rows stay, as a submission's hold keeps them: its chain goes on from the
// stored rows (ingest-v1 2.7 step 4).
func TestHeadFromWitnessRows(t *testing.T) {
	engines(t, func(t *testing.T, engine store.Engine) {
		h := newHarness(t, engine)
		dev := h.device("d_test", false)
		roots := chain(rand.New(rand.NewPCG(12, 13)), ledger.Hash{}, 1, 3)
		h.ok(t, upload{dev: dev, body: batchesBody(roots[:2]...)})
		// The device's row loses its ingest state, as a row linked afresh
		// after its tombstone expired has none, while its witness rows stay.
		h.exec(t, `UPDATE devices SET ingest_req = NULL, ingest_head_b = NULL, ingest_head_hash = NULL, ingest_head_generation = NULL,
ingest_head_received_at_ms = NULL, ingest_chain_start = NULL, ingest_gaps = NULL WHERE device_id = $1`, dev.ID)
		tests := []struct {
			name  string
			check func(t *testing.T)
		}{
			{"the state gives the head and the chain's start from the witness rows, as of a device signed in again", func(t *testing.T) {
				st := h.state(t, dev)
				if st.Head == nil || st.Head.B != 2 || st.Head.Head != roots[1].head || st.ChainStart == nil || *st.ChainStart != 1 || !st.Relinked {
					t.Errorf("state %+v", st)
				}
			}},
			{"a request that stores no batch leaves the row without a head, and the witness rows still give it", func(t *testing.T) {
				h.ok(t, upload{dev: dev})
				if st := h.state(t, dev); st.Req != dev.req || st.Head == nil || st.Head.B != 2 || !st.Relinked {
					t.Errorf("state %+v", st)
				}
			}},
			{"a batch that doesn't link to that head is refused", func(t *testing.T) {
				x := root{b: 3, n: 1, prev: ledger.Hash{5}}.relink()
				if got := h.ok(t, upload{dev: dev, body: batchesBody(x)}).Batches[0]; got.Code != CodePrevMismatch {
					t.Errorf("got %+v", got)
				}
			}},
			{"the next batch links to that head", func(t *testing.T) {
				if got := h.ok(t, upload{dev: dev, body: batchesBody(roots[2])}).Batches[0]; got.Code != CodeAccepted {
					t.Errorf("got %+v", got)
				}
			}},
			{"then the row holds the head again, with the chain's first start", func(t *testing.T) {
				if st := h.state(t, dev); st.Head == nil || st.Head.B != 3 || st.ChainStart == nil || *st.ChainStart != 1 || st.Relinked {
					t.Errorf("state %+v", st)
				}
			}},
		}
		for _, tt := range tests {
			t.Run(tt.name, tt.check)
		}
	})
}
