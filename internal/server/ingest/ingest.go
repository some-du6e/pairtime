// SPDX-License-Identifier: AGPL-3.0-or-later

package ingest

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/server/backup"
	"github.com/some-du6e/pairtime/internal/server/device"
	"github.com/some-du6e/pairtime/internal/server/writer"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/ledger"
)

// The paths of ingest-v1.
const (
	BatchesPath = "/api/v1/ingest/batches"
	StatePath   = "/api/v1/ingest/state"
	TimePath    = "/api/v1/time"
)

// Devices is WP-SV3's device store, which *device.Store is. Authenticate
// finds the device a "ptd_" token belongs to, checking the token's
// checksum before any lookup (the dashboard wave's meeting point 1), and
// returns device.ErrBadToken, device.ErrUnknownToken or device.ErrRevoked
// for a token it refuses; any other error is the store failing, and the
// request gets 503. The device's counter and chain head live on its row,
// which ingest reads with LoadState and writes with SaveState inside its
// own writer transaction, and never with SQL of its own
// (D-device-row-chain-state).
type Devices interface {
	Authenticate(ctx context.Context, token string) (device.Device, error)
	LoadState(ctx context.Context, tx writer.Tx, d device.Device) (device.State, error)
	SaveState(ctx context.Context, tx writer.Tx, d device.Device, st device.State) error
}

// RollupFunc moves an account's rollups from prev, the revision a bucket
// replaces or nil for the minute's first, to next, inside the writer
// transaction tx that stores next (the dashboard wave's meeting point 3,
// WP-SV7). An error fails the request, and nothing it stored stays.
type RollupFunc func(ctx context.Context, tx writer.Tx, account string, prev *event.MinuteBucket, next event.MinuteBucket) error

// Doer runs a write through the writer goroutine. *writer.Writer is one.
type Doer interface {
	Do(ctx context.Context, rows int, op writer.Op) error
}

// Options configure a Service.
type Options struct {
	// Devices finds the device behind a token and keeps ingest's state on
	// its row. Without it, ingest is off.
	Devices Devices
	// Writer is the writer goroutine every write goes through.
	Writer Doer
	// Read is the read pool, for the read that spares the writer a
	// replay. Everything else, GET /api/v1/ingest/state included, reads in
	// the writer's transaction, where the device's row is read.
	Read *sql.DB
	// Key is the server key that signs receipts. Without it, ingest is
	// off.
	Key ed25519.PrivateKey
	// Rollup, when set, is called for every bucket revision stored.
	// TODO(WP-SV7): serve.go passes rollup.Apply through it, converting
	// each bucket with rollup.FromEvent(account, b, b.AgentOnly), once
	// package rollup merges.
	Rollup RollupFunc
	// Observe, when set, hears of each upload the writer processed, not
	// counting replays: how many batch roots it stored, and how long the
	// request took from arrival to the response with their receipts
	// (DESIGN 10.4's ingest rate and batch latency).
	Observe func(stored int, took time.Duration)
	// RateBurst and RateEvery are each device's upload rate: a bucket of
	// RateBurst requests that refills one every RateEvery. Zero values take
	// DefaultRateBurst and DefaultRateEvery (SP 8.6).
	RateBurst int
	RateEvery time.Duration
	// Now is the server's clock. Nil means time.Now.
	Now func() time.Time
	// Logger gets the errors behind a 503. Nil means slog.Default().
	Logger *slog.Logger
}

// Service is the ingest API.
type Service struct {
	o     Options
	pub   ed25519.PublicKey
	keyID string
	rate  *limiter
}

// New returns the ingest API. Ingest answers ingest_off until Devices,
// Writer, Read and Key are all set; GET /api/v1/time always answers.
func New(o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.RateBurst <= 0 {
		o.RateBurst = DefaultRateBurst
	}
	if o.RateEvery <= 0 {
		o.RateEvery = DefaultRateEvery
	}
	s := &Service{o: o, rate: newLimiter(o.RateBurst, o.RateEvery)}
	if len(o.Key) == ed25519.PrivateKeySize {
		s.pub = o.Key.Public().(ed25519.PublicKey)
		s.keyID = backup.KeyID(s.pub)
	}
	return s
}

// Register adds the three endpoints to mux.
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST "+BatchesPath, s.batches)
	mux.HandleFunc("GET "+StatePath, s.state)
	mux.HandleFunc("GET "+TimePath, s.time)
}

// On reports whether ingest takes uploads.
func (s *Service) On() bool {
	return s.o.Devices != nil && s.pub != nil && s.o.Writer != nil && s.o.Read != nil
}

func (s *Service) nowMS() int64 { return s.o.Now().UnixMilli() }

// stamp sets the headers every response carries (ingest-v1 1).
func (s *Service) stamp(w http.ResponseWriter) {
	w.Header().Set(HeaderTime, strconv.FormatInt(s.nowMS(), 10))
	w.Header().Set("Cache-Control", "no-store")
}

func (s *Service) writeJSON(w http.ResponseWriter, status int, body []byte) {
	s.stamp(w)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(body)
}

// errorBody is a refused request's JSON (ingest-v1 2.6).
type errorBody struct {
	Error  string `json:"error"`
	Detail string `json:"detail"`
	// DeviceKeyID names the device's registered key on signature_invalid,
	// so a client can tell a key the server doesn't have from a fault.
	DeviceKeyID string `json:"device_key_id,omitempty"`
}

func (s *Service) refuse(w http.ResponseWriter, r *refusal) {
	status, ok := refusals[r.code]
	if !ok {
		status = http.StatusInternalServerError
	}
	switch {
	case r.retryAfter > 0:
		w.Header().Set("Retry-After", strconv.Itoa(r.retryAfter))
	case status == http.StatusServiceUnavailable:
		w.Header().Set("Retry-After", "5")
	}
	b, _ := json.Marshal(errorBody{Error: r.code, Detail: r.detail, DeviceKeyID: r.deviceKeyID})
	s.writeJSON(w, status, b)
}

var refusalOff = refuse(CodeIngestOff, "Ingest isn't set up on this server yet.")

func revoked() *refusal {
	return refuse(CodeDeviceRevoked, "The device was removed, or its account can no longer sign in; pairtime login links it again.")
}

// anotherAccount refuses a device_id whose stored rows belong to an account
// other than the token's (D-device-account-binding).
func anotherAccount() *refusal {
	return refuse(CodeDeviceMismatch, "This server holds uploads from this device_id under another account; sign in again as a new device.")
}

func unavailable() *refusal {
	return refuse(CodeUnavailable, "The server couldn't store the upload; send it again later.")
}

// badSignature refuses a signature that doesn't verify under dev's key, and
// names that key by its id.
func badSignature(dev device.Device) *refusal {
	r := refuse(CodeSignatureInvalid, "The signature doesn't verify under the device's registered key; device_key_id names that key.")
	r.deviceKeyID = backup.KeyID(dev.PublicKey)
	return r
}

// authenticate runs ingest-v1 2.5 steps 2 and 3.
func (s *Service) authenticate(r *http.Request) (device.Device, *refusal) {
	auth, ok := single(r.Header, "Authorization")
	scheme, token, found := strings.Cut(auth, " ")
	if !ok || !found || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return device.Device{}, refuse(CodeTokenMissing, "Send the device token as Authorization: Bearer ptd_...")
	}
	dev, err := s.o.Devices.Authenticate(r.Context(), strings.TrimSpace(token))
	switch {
	case errors.Is(err, device.ErrBadToken), errors.Is(err, device.ErrUnknownToken):
		return device.Device{}, refuse(CodeTokenInvalid, "The device token fails its checksum or names no device; sign in again.")
	case errors.Is(err, device.ErrRevoked), err == nil && dev.Revoked:
		return device.Device{}, revoked()
	case err != nil:
		s.o.Logger.Error("ingest: the device store failed", "err", err)
		return device.Device{}, unavailable()
	}
	if id, ok := single(r.Header, HeaderDevice); !ok || id != dev.ID {
		return device.Device{}, refuse(CodeDeviceMismatch, "X-Pairtime-Device must be the device the token belongs to.")
	}
	return dev, nil
}

func keyMissing() *refusal {
	return refuse(CodeDeviceKeyMissing, "The device has no registered key; sign in again with pairtime login.")
}

// checkClock refuses a time more than MaxSkewMS from the server's clock
// (ingest-v1 2.5 step 7).
func (s *Service) checkClock(t int64) *refusal {
	if skew := t - s.nowMS(); skew > MaxSkewMS || skew < -MaxSkewMS {
		return refuse(CodeClockSkew, "X-Pairtime-Time is "+strconv.FormatInt(skew, 10)+" ms from the server's clock; correct the clock from this response's X-Pairtime-Time and sign again.")
	}
	return nil
}

// batches is POST /api/v1/ingest/batches.
func (s *Service) batches(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	if !s.On() {
		s.refuse(w, refusalOff)
		return
	}
	dev, ref := s.authenticate(r)
	if ref == nil && len(dev.PublicKey) != ed25519.PublicKeySize {
		ref = keyMissing()
	}
	var env envelope
	if ref == nil {
		env, ref = parseEnvelope(r.Header)
	}
	if ref == nil {
		ref = checkContent(r.Header)
	}
	if ref == nil {
		ref = s.checkClock(env.time)
	}
	var raw []byte
	if ref == nil {
		// pairtimed has no read timeout, because live streams stay open
		// (WP-SV8), so the body gets one of its own. A recorder in tests
		// can't take it, and doesn't need it.
		_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(BodyTimeout))
		raw, ref = readRaw(r)
	}
	if ref == nil {
		sum := sha256.Sum256(raw)
		switch {
		case hex.EncodeToString(sum[:]) != env.bodySHA:
			ref = refuse(CodeBodyHashMismatch, "The body's SHA-256 isn't X-Pairtime-Body-SHA256.")
		case !ed25519.Verify(dev.PublicKey, Message(dev.ID, env.reqText, env.timeText, env.bodySHA), env.signature):
			ref = badSignature(dev)
		}
	}
	if ref == nil {
		// Only a signed request counts against the device's rate, so
		// nobody without its key can use it up.
		var done func()
		if done, ref = s.rate.take(dev.ID, s.o.Now()); ref == nil {
			defer done()
		}
	}
	var inflated []byte
	if ref == nil {
		inflated, ref = inflate(raw)
	}
	var b body
	if ref == nil {
		b, ref = parseBody(inflated)
	}
	if ref != nil {
		s.refuse(w, ref)
		return
	}
	if out, done := s.precheck(r.Context(), dev, env); done {
		s.answer(w, out, start)
		return
	}
	p, err := parseItems(b)
	if err != nil {
		s.o.Logger.Error("ingest: encoding an upload's items failed", "err", err)
		s.refuse(w, unavailable())
		return
	}
	ip, ja4 := origin(r)
	var out outcome
	err = s.o.Writer.Do(r.Context(), p.rows(), func(ctx context.Context, tx writer.Tx) error {
		var err error
		out, err = s.process(ctx, tx, dev, env, p, ip, ja4)
		return err
	})
	if err != nil {
		if r.Context().Err() == nil {
			s.o.Logger.Error("ingest: storing an upload failed", "err", err)
		}
		s.refuse(w, unavailable())
		return
	}
	s.answer(w, out, start)
}

// answer sends what the checks of ingest-v1 2.5 steps 13 and 14 decided.
func (s *Service) answer(w http.ResponseWriter, out outcome, start time.Time) {
	switch {
	case out.refusal != nil:
		s.refuse(w, out.refusal)
	case out.replay:
		w.Header().Set(HeaderReplay, "true")
		s.writeJSON(w, http.StatusOK, out.response)
	default:
		if s.o.Observe != nil {
			s.o.Observe(out.stored, time.Since(start))
		}
		s.writeJSON(w, http.StatusOK, out.response)
	}
}

// outcome is what step 13 or 14 decided: a refusal, or the response to
// send, which is a stored one for a replay.
type outcome struct {
	refusal  *refusal
	response []byte
	replay   bool
	// stored counts the batch roots the request stored.
	stored int
}

// checkReplay is ingest-v1 2.5 step 13's Idempotency-Key check against the
// device's last reply. It reports whether it decided the request.
func checkReplay(rp reply, env envelope) (outcome, bool) {
	if !rp.found || rp.key != env.key {
		return outcome{}, false
	}
	if rp.bodySHA != env.bodySHA {
		return outcome{refusal: refuse(CodeIdempotencyKeyReused, "This Idempotency-Key was used with another body; a new body takes a new key.")}, true
	}
	return outcome{response: rp.response, replay: true}, true
}

// checkCounter is step 13's counter check against the device's stored
// counter.
func checkCounter(st device.State, env envelope) (outcome, bool) {
	if env.req <= st.Req {
		return outcome{refusal: refuse(CodeCounterStale, "X-Pairtime-Req is at or below the stored counter; read "+StatePath+" and continue above its req.")}, true
	}
	return outcome{}, false
}

// precheck runs step 13's replay check on the read pool, so a replay never
// waits for the writer. The writer's op runs all of step 13 again on the
// rows it holds; this read only spares it the work. A read that fails
// decides nothing. The counter is on the device's row, which ingest reads
// only inside its writer transaction (D-device-row-chain-state).
func (s *Service) precheck(ctx context.Context, dev device.Device, env envelope) (outcome, bool) {
	rp, err := loadReply(ctx, s.o.Read, dev.ID, dev.Account)
	if err != nil {
		return outcome{}, false
	}
	return checkReplay(rp, env)
}

// parsed is a request's items after the checks that read no stored state:
// each item's forms and a bucket's MinuteBucket.Validate (ingest-v1 2.3).
// They run before the writer's op, which then only reads and writes rows.
type parsed struct {
	batches []parsedBatch
	buckets []parsedBucket
	commits []parsedCommit
	labels  *parsedLabels
}

type parsedBatch struct {
	br  batchRoot
	err *itemErr
}

type parsedBucket struct {
	rv   event.MinuteBucket
	data []byte
	err  *itemErr
}

type parsedCommit struct {
	c    commitRecord
	data []byte
	err  *itemErr
}

type parsedLabels struct {
	data []byte
	err  *itemErr
}

func parseItems(b body) (parsed, error) {
	var p parsed
	for _, raw := range b.batches {
		br, e := parseBatchRoot(raw)
		p.batches = append(p.batches, parsedBatch{br: br, err: e})
	}
	for _, raw := range b.buckets {
		pb := parsedBucket{}
		pb.rv, pb.err = checkBucket(raw)
		if pb.err == nil {
			var err error
			if pb.data, err = json.Marshal(pb.rv); err != nil {
				return p, err
			}
		}
		p.buckets = append(p.buckets, pb)
	}
	for _, raw := range b.commits {
		pc := parsedCommit{}
		pc.c, pc.err = parseCommit(raw)
		if pc.err == nil {
			var err error
			if pc.data, err = json.Marshal(pc.c); err != nil {
				return p, err
			}
		}
		p.commits = append(p.commits, pc)
	}
	if b.labels != nil {
		l, e := parseLabels(b.labels)
		pl := &parsedLabels{err: e}
		if e == nil {
			var err error
			if pl.data, err = json.Marshal(l); err != nil {
				return p, err
			}
		}
		p.labels = pl
	}
	return p, nil
}

// rows is how many rows the request's op may write, for the writer's
// MaxRows: the device's row, its reply and the request's origin, then
// each item, a bucket counting twice for its revision row.
func (p parsed) rows() int {
	n := 3 + len(p.batches) + 2*len(p.buckets) + len(p.commits)
	if p.labels != nil {
		n++
	}
	return n
}

// The response's JSON (ingest-v1 2.8).
type (
	response struct {
		ReceivedAt int64         `json:"received_at"`
		Req        int64         `json:"req"`
		Head       *headJSON     `json:"head"`
		Batches    []batchResult `json:"batches"`
		Buckets    []itemResult  `json:"buckets"`
		Commits    []itemResult  `json:"commits"`
		Labels     *itemResult   `json:"labels"`
	}
	headJSON struct {
		B    uint64      `json:"b"`
		Head ledger.Hash `json:"H_b"`
	}
	batchResult struct {
		Status  int          `json:"status"`
		Code    string       `json:"code"`
		Field   *string      `json:"field,omitempty"`
		B       uint64       `json:"b,omitempty"`
		Receipt *receiptJSON `json:"receipt,omitempty"`
	}
	itemResult struct {
		Status int     `json:"status"`
		Code   string  `json:"code"`
		Field  *string `json:"field,omitempty"`
		// Rev is the revision the server holds, on superseded,
		// rev_conflict and too_many_revisions.
		Rev *int64 `json:"rev,omitempty"`
	}
	receiptJSON struct {
		ReceivedAt int64  `json:"received_at"`
		KeyID      string `json:"key_id"`
		Sig        string `json:"sig"`
	}
)

func accepted(code string) itemResult { return itemResult{Status: http.StatusOK, Code: code} }

func resultOf(e *itemErr) itemResult {
	r := itemResult{Status: e.status, Code: e.code}
	if e.named {
		f := e.field
		r.Field = &f
	}
	return r
}

// storedRev names the revision the server holds in a bucket's result.
func storedRev(status int, code string, rev int64) itemResult {
	return itemResult{Status: status, Code: code, Rev: &rev}
}

// chainState is a device's chain state while ingest works on it: the state on
// its row, and, when the row has no head, the head of its witness rows,
// which saving drops again unless a batch moved it (witnessHead).
type chainState struct {
	device.State
	fromWitness bool
	moved       bool
}

// loadChain reads dev's chain state inside tx. It refuses the request when
// rows the server holds for the device belong to another account
// (D-device-account-binding), or when the device's row no longer holds the
// token the request came with.
func (s *Service) loadChain(ctx context.Context, tx writer.Tx, dev device.Device) (chainState, *refusal, error) {
	foreign, err := foreignRows(ctx, tx, dev.ID, dev.Account)
	switch {
	case err != nil:
		return chainState{}, nil, err
	case foreign:
		return chainState{}, anotherAccount(), nil
	}
	st, err := s.o.Devices.LoadState(ctx, tx, dev)
	switch {
	case errors.Is(err, device.ErrRevoked):
		return chainState{}, revoked(), nil
	case err != nil:
		return chainState{}, nil, err
	}
	c := chainState{State: st}
	if st.HeadB == 0 {
		if c.fromWitness, err = witnessHead(ctx, tx, dev.ID, &c.State); err != nil {
			return chainState{}, nil, err
		}
	}
	return c, nil, nil
}

// relinked reports whether the device signed in again since its head was
// stored, so its next batch may skip ahead (D-local-gap).
func (c chainState) relinked() bool { return c.HeadB != 0 && c.HeadGeneration < c.Generation }

// saved is the state to store on the device's row: c's, without a head
// from the witness rows that no batch moved.
func (c chainState) saved() device.State {
	st := c.State
	if c.fromWitness && !c.moved {
		st.HeadB, st.HeadHash, st.HeadGeneration, st.HeadReceivedAt, st.ChainStart, st.Gaps = 0, ledger.Hash{}, 0, time.Time{}, 0, nil
	}
	return st
}

// process runs ingest-v1 2.5 steps 13 and 14 in the writer's transaction.
// It returns an error only when the database fails, which rolls back
// everything it wrote.
func (s *Service) process(ctx context.Context, tx writer.Tx, dev device.Device, env envelope, p parsed, ip, ja4 string) (outcome, error) {
	c, ref, err := s.loadChain(ctx, tx, dev)
	switch {
	case err != nil:
		return outcome{}, err
	case ref != nil:
		return outcome{refusal: ref}, nil
	}
	rp, err := loadReply(ctx, tx, dev.ID, dev.Account)
	if err != nil {
		return outcome{}, err
	}
	if out, done := checkReplay(rp, env); done {
		return out, nil
	}
	if out, done := checkCounter(c.State, env); done {
		return out, nil
	}

	now := s.nowMS()
	resp := response{
		ReceivedAt: now,
		Req:        env.req,
		Batches:    make([]batchResult, 0, len(p.batches)),
		Buckets:    make([]itemResult, 0, len(p.buckets)),
		Commits:    make([]itemResult, 0, len(p.commits)),
	}
	stored := 0
	for _, pb := range p.batches {
		r, err := s.batch(ctx, tx, dev, &c, pb, now)
		if err != nil {
			return outcome{}, err
		}
		if r.Code == CodeAccepted {
			stored++
		}
		resp.Batches = append(resp.Batches, r)
	}
	for _, pb := range p.buckets {
		r, err := s.bucket(ctx, tx, dev, pb, now)
		if err != nil {
			return outcome{}, err
		}
		resp.Buckets = append(resp.Buckets, r)
	}
	for _, pc := range p.commits {
		r, err := s.commit(ctx, tx, dev, pc, now)
		if err != nil {
			return outcome{}, err
		}
		resp.Commits = append(resp.Commits, r)
	}
	if pl := p.labels; pl != nil {
		r := accepted(CodeAccepted)
		if pl.err != nil {
			r = resultOf(pl.err)
		} else if err := putLabels(ctx, tx, dev.ID, dev.Account, pl.data, now); err != nil {
			return outcome{}, err
		}
		resp.Labels = &r
	}
	if c.HeadB != 0 {
		resp.Head = &headJSON{B: c.HeadB, Head: c.HeadHash}
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return outcome{}, err
	}
	st := c.saved()
	st.Req = env.req
	if err := s.o.Devices.SaveState(ctx, tx, dev, st); err != nil {
		return outcome{}, err
	}
	if err := putReply(ctx, tx, dev.ID, dev.Account, reply{key: env.key, bodySHA: env.bodySHA, response: data}, now); err != nil {
		return outcome{}, err
	}
	if err := insertOrigin(ctx, tx, newID(time.UnixMilli(now)), dev.ID, dev.Account, ip, ja4, now); err != nil {
		return outcome{}, err
	}
	return outcome{response: data, stored: stored}, nil
}

// receipt returns a stored row's receipt in JSON.
func receipt(w witnessRow) *receiptJSON {
	return &receiptJSON{ReceivedAt: w.receivedAt, KeyID: w.keyID, Sig: w.sig}
}

// batch checks one batch root against the chain and stores it, moving
// c's head (ingest-v1 2.7).
func (s *Service) batch(ctx context.Context, tx writer.Tx, dev device.Device, c *chainState, pb parsedBatch, now int64) (batchResult, error) {
	br := pb.br
	if e := pb.err; e != nil {
		r := resultOf(e)
		return batchResult{Status: r.Status, Code: r.Code, Field: r.Field, B: br.b}, nil
	}
	res := batchResult{B: br.b}
	fail := func(status int, code string) (batchResult, error) {
		res.Status, res.Code = status, code
		return res, nil
	}
	if br.n == 0 {
		return fail(http.StatusUnprocessableEntity, CodeBatchEmpty)
	}
	if ledger.BatchHead(br.prev, br.root, br.n, br.b) != br.head {
		return fail(http.StatusUnprocessableEntity, CodeHeadMismatch)
	}
	stored, found, err := loadWitness(ctx, tx, dev.ID, br.b)
	if err != nil {
		return res, err
	}
	if found {
		if stored.n != br.n || stored.root != br.root || stored.head != br.head || stored.prev != br.prev {
			return fail(http.StatusConflict, CodeBatchConflict)
		}
		res.Status, res.Code, res.Receipt = http.StatusOK, CodeDuplicate, receipt(stored)
		return res, nil
	}
	var zero ledger.Hash
	var gap *device.Gap
	switch {
	case c.HeadB == 0 && br.b == 1 && br.prev != zero:
		return fail(http.StatusConflict, CodePrevMismatch)
	case c.HeadB == 0:
		// A device with no stored head starts its witnessed chain at any b
		// (ingest-v1 7 item 6; ledger-v1 6).
	case br.b == c.HeadB+1:
		if br.prev != c.HeadHash {
			return fail(http.StatusConflict, CodePrevMismatch)
		}
	case br.b > c.HeadB+1 && c.relinked():
		// The first batch under a new sign-in skips the batches the device
		// sealed in local-only mode, whose roots it never sends; they stay
		// a gap the server never takes (D-local-gap; ingest-v1 2.7 step 5).
		gap = &device.Gap{First: c.HeadB + 1, Last: br.b - 1}
	case br.b > c.HeadB+1:
		return fail(http.StatusConflict, CodeChainGap)
	default:
		// At or below the head with no stored row: before the chain's
		// start, in a local-only gap, or past its retention period.
		return fail(http.StatusConflict, CodeBeforeChain)
	}
	rc, err := ledger.SignReceipt(s.o.Key, dev.ID, br.b, br.head, uint64(now))
	if err != nil {
		return res, err
	}
	row := witnessRow{n: br.n, root: br.root, head: br.head, prev: br.prev, receivedAt: now, sig: hex.EncodeToString(rc.Signature[:]), keyID: s.keyID}
	if err := insertWitness(ctx, tx, dev.ID, dev.Account, br.b, row); err != nil {
		return res, err
	}
	if c.HeadB == 0 {
		c.ChainStart = br.b
	}
	if gap != nil {
		c.Gaps = append(c.Gaps, *gap)
	}
	c.HeadB, c.HeadHash, c.HeadReceivedAt, c.HeadGeneration, c.moved = br.b, br.head, time.UnixMilli(now), c.Generation, true
	res.Status, res.Code, res.Receipt = http.StatusOK, CodeAccepted, receipt(row)
	return res, nil
}

// bucket stores one minute bucket as a revision (ingest-v1 2.7).
func (s *Service) bucket(ctx context.Context, tx writer.Tx, dev device.Device, pb parsedBucket, now int64) (itemResult, error) {
	if pb.err != nil {
		return resultOf(pb.err), nil
	}
	rv := pb.rv
	minute := rv.Minute.UnixMilli()
	switch {
	case rv.Device != dev.ID:
		return resultOf(refused(http.StatusUnprocessableEntity, CodeWrongDevice)), nil
	case minute > now+MaxSkewMS:
		return resultOf(refused(http.StatusUnprocessableEntity, CodeFutureMinute)), nil
	case minute <= now-BucketHorizonMS:
		return resultOf(refused(http.StatusUnprocessableEntity, CodeExpiredMinute)), nil
	}
	cur, found, err := loadBucket(ctx, tx, dev.ID, minute)
	if err != nil {
		return itemResult{}, err
	}
	rev, revisions := int64(rv.Rev), int64(1)
	var prev *event.MinuteBucket
	same := false
	if found && rev == cur.rev {
		if same, err = sameBucket(cur.bucket, pb.data); err != nil {
			return itemResult{}, err
		}
	}
	switch {
	case !found:
	case rev > cur.rev && cur.revisions >= MaxRevisions:
		return storedRev(http.StatusConflict, CodeTooManyRevisions, cur.rev), nil
	case rev > cur.rev:
		revisions = cur.revisions + 1
		if s.o.Rollup != nil {
			prev = new(event.MinuteBucket)
			if err := json.Unmarshal([]byte(cur.bucket), prev); err != nil {
				return itemResult{}, err
			}
		}
	case rev == cur.rev && same:
		return accepted(CodeDuplicate), nil
	case rev == cur.rev:
		return storedRev(http.StatusConflict, CodeRevConflict, cur.rev), nil
	default:
		return storedRev(http.StatusOK, CodeSuperseded, cur.rev), nil
	}
	if err := putBucket(ctx, tx, dev.ID, dev.Account, minute, rev, string(rv.State), pb.data, now, revisions); err != nil {
		return itemResult{}, err
	}
	if s.o.Rollup != nil {
		if err := s.o.Rollup(ctx, tx, dev.Account, prev, rv); err != nil {
			return itemResult{}, err
		}
	}
	return accepted(CodeAccepted), nil
}

// sameBucket reports whether stored, a bucket's JSON as ingest stored it,
// is the bucket data now encodes. It decodes stored and encodes it again
// first, so a bucket stored by a release that wrote its members in another
// order, or without one this release leaves out when empty, is still the
// same bucket (ingest-v1 2.7), and its resend a duplicate.
func sameBucket(stored string, data []byte) (bool, error) {
	if stored == string(data) {
		return true, nil
	}
	var rv event.MinuteBucket
	if err := json.Unmarshal([]byte(stored), &rv); err != nil {
		return false, err
	}
	again, err := json.Marshal(rv)
	if err != nil {
		return false, err
	}
	return string(again) == string(data), nil
}

// commit stores one commit record (ingest-v1 2.7).
func (s *Service) commit(ctx context.Context, tx writer.Tx, dev device.Device, pc parsedCommit, now int64) (itemResult, error) {
	if pc.err != nil {
		return resultOf(pc.err), nil
	}
	stored, found, err := loadCommit(ctx, tx, dev.ID, pc.c.SHA)
	if err != nil {
		return itemResult{}, err
	}
	code := CodeAccepted
	switch {
	case found && stored == string(pc.data):
		return accepted(CodeDuplicate), nil
	case found:
		code = CodeUpdated
	}
	if err := putCommit(ctx, tx, dev.ID, dev.Account, pc.c, pc.data, now); err != nil {
		return itemResult{}, err
	}
	return accepted(code), nil
}

// ja4Form is a JA4 TLS fingerprint: ten letters and digits, then two runs
// of twelve hex digits.
var ja4Form = regexp.MustCompile(`^[a-z0-9]{10}_[0-9a-f]{12}_[0-9a-f]{12}$`)

// origin returns where r came from (DESIGN 4.4; SP 8.7): the IP Cloudflare
// saw, from CF-Connecting-IP, or the connection's own address when the
// request didn't come through Cloudflare, as in tests and local runs; and
// CF-JA4 when the plan sends one. Only an address netip reads, without an
// IPv6 zone, and a fingerprint in JA4's form are kept, so no text a client
// wrote reaches the table.
func origin(r *http.Request) (ip, ja4 string) {
	if v, ok := single(r.Header, "CF-Connecting-IP"); ok {
		if a, err := netip.ParseAddr(strings.TrimSpace(v)); err == nil {
			ip = a.WithZone("").String()
		}
	}
	if ip == "" {
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			if a, err := netip.ParseAddr(host); err == nil {
				ip = a.WithZone("").String()
			}
		}
	}
	if ip == "" {
		ip = "unknown"
	}
	if v, ok := single(r.Header, "CF-JA4"); ok && ja4Form.MatchString(v) {
		ja4 = v
	}
	return ip, ja4
}

// newID returns a UUIDv7 for t (RFC 9562 section 5.7), the id form package
// store's portable SQL rules ask for.
func newID(t time.Time) string {
	var b [16]byte
	rand.Read(b[:])
	ms := uint64(t.UnixMilli())
	for i := range 6 {
		b[i] = byte(ms >> (40 - 8*i))
	}
	b[6] = b[6]&0x0f | 0x70
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// stateJSON is GET /api/v1/ingest/state's answer (ingest-v1 3).
type stateJSON struct {
	Device     string         `json:"device"`
	Req        int64          `json:"req"`
	Head       *stateHeadJSON `json:"head"`
	ChainStart *uint64        `json:"chain_start"`
	// Relinked is set while the device's next batch may skip ahead, since
	// it signed in again after the head was stored (D-local-gap).
	Relinked bool `json:"relinked"`
	// Gaps are the local-only gaps the server recorded, oldest first.
	Gaps      []device.Gap  `json:"gaps"`
	ServerKey serverKeyJSON `json:"server_key"`
}

type stateHeadJSON struct {
	B          uint64      `json:"b"`
	Head       ledger.Hash `json:"H_b"`
	ReceivedAt int64       `json:"received_at"`
}

type serverKeyJSON struct {
	KeyID     string `json:"key_id"`
	PublicKey string `json:"public_key"`
}

// state is GET /api/v1/ingest/state. The device signs it, as it signs an
// upload, because the counter and the head's time change with every upload
// and would tell whoever holds a leaked token when the device's owner
// works (ingest-v1 3).
func (s *Service) state(w http.ResponseWriter, r *http.Request) {
	if !s.On() {
		s.refuse(w, refusalOff)
		return
	}
	dev, ref := s.authenticate(r)
	if ref == nil && len(dev.PublicKey) != ed25519.PublicKeySize {
		ref = keyMissing()
	}
	var timeText string
	var t int64
	if ref == nil {
		timeText, t, ref = parseTime(r.Header)
	}
	var sig []byte
	if ref == nil {
		sig, ref = parseSignatureHeader(r.Header)
	}
	if ref == nil {
		ref = s.checkClock(t)
	}
	if ref == nil && !ed25519.Verify(dev.PublicKey, StateMessage(dev.ID, timeText), sig) {
		ref = badSignature(dev)
	}
	if ref != nil {
		s.refuse(w, ref)
		return
	}
	// The state is on the device's row, which ingest reads only inside its
	// writer transaction (D-device-row-chain-state).
	var c chainState
	err := s.o.Writer.Do(r.Context(), 1, func(ctx context.Context, tx writer.Tx) error {
		var err error
		c, ref, err = s.loadChain(ctx, tx, dev)
		return err
	})
	switch {
	case err != nil:
		if r.Context().Err() == nil {
			s.o.Logger.Error("ingest: reading a device's state failed", "err", err)
		}
		s.refuse(w, unavailable())
		return
	case ref != nil:
		s.refuse(w, ref)
		return
	}
	out := stateJSON{Device: dev.ID, Req: c.Req, Relinked: c.relinked(), Gaps: c.Gaps,
		ServerKey: serverKeyJSON{KeyID: s.keyID, PublicKey: hex.EncodeToString(s.pub)}}
	if out.Gaps == nil {
		out.Gaps = []device.Gap{}
	}
	if c.HeadB != 0 {
		start := c.ChainStart
		out.Head = &stateHeadJSON{B: c.HeadB, Head: c.HeadHash, ReceivedAt: c.HeadReceivedAt.UnixMilli()}
		out.ChainStart = &start
	}
	data, err := json.Marshal(out)
	if err != nil {
		s.refuse(w, unavailable())
		return
	}
	s.writeJSON(w, http.StatusOK, data)
}

// time is GET /api/v1/time (ingest-v1 4).
func (s *Service) time(w http.ResponseWriter, r *http.Request) {
	data, _ := json.Marshal(struct {
		ServerTime int64 `json:"server_time"`
	}{s.nowMS()})
	s.writeJSON(w, http.StatusOK, data)
}
