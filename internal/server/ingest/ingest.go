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
	"github.com/some-du6e/pairtime/internal/server/writer"
	"github.com/some-du6e/pairtime/pkg/ledger"
)

// The paths of ingest-v1.
const (
	BatchesPath = "/api/v1/ingest/batches"
	StatePath   = "/api/v1/ingest/state"
	TimePath    = "/api/v1/time"
)

// Device is a linked device, as the device store (WP-SV3) finds it by its
// token.
type Device struct {
	// ID is the opaque device_id, the per-install id the client's ledger
	// made.
	ID string
	// Account is the users row of the device's owner.
	Account string
	// PublicKey is the device's registered Ed25519 key, nil until it
	// registers one.
	PublicKey ed25519.PublicKey
	Revoked   bool
}

// ErrUnknownToken is what an Authenticator returns, wrapped, for a token
// that fails its checksum or names no device. Any other error is the store
// failing, and the request gets 503.
var ErrUnknownToken = errors.New("ingest: the token fails its checksum or names no device")

// Authenticator finds the device a "ptd_" token belongs to, checking the
// token's checksum before any lookup (the dashboard wave's meeting point
// 1). *device.Store meets it through AuthenticatorFunc.
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (Device, error)
}

// AuthenticatorFunc is a function that is an Authenticator.
type AuthenticatorFunc func(ctx context.Context, token string) (Device, error)

// Authenticate calls f.
func (f AuthenticatorFunc) Authenticate(ctx context.Context, token string) (Device, error) {
	return f(ctx, token)
}

// RollupFunc moves an account's rollups from prev, the revision a bucket
// replaces or nil for the minute's first, to next, inside the writer
// transaction tx that stores next (the dashboard wave's meeting point 3,
// WP-SV7). An error fails the request, and nothing it stored stays.
type RollupFunc func(ctx context.Context, tx writer.Tx, account string, prev *Revision, next Revision) error

// Doer runs a write through the writer goroutine. *writer.Writer is one.
type Doer interface {
	Do(ctx context.Context, rows int, op writer.Op) error
}

// Options configure a Service.
type Options struct {
	// Devices finds the device behind a token. Without it, ingest is off.
	Devices Authenticator
	// Writer is the writer goroutine every write goes through.
	Writer Doer
	// Read is the read pool, for GET /api/v1/ingest/state and the reads
	// that spare the writer a replay.
	Read *sql.DB
	// Key is the server key that signs receipts. Without it, ingest is
	// off.
	Key ed25519.PrivateKey
	// Rollup, when set, is called for every bucket revision stored.
	// TODO(WP-SV7): serve.go passes rollup.Apply through it, converting
	// each Revision to a rollup.Bucket, once package rollup merges.
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

// New returns the ingest API. Ingest answers ingest_off until both
// Devices and Key are set; GET /api/v1/time always answers.
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

func unavailable() *refusal {
	return refuse(CodeUnavailable, "The server couldn't store the upload; send it again later.")
}

// badSignature refuses a signature that doesn't verify under dev's key, and
// names that key by its id.
func badSignature(dev Device) *refusal {
	r := refuse(CodeSignatureInvalid, "The signature doesn't verify under the device's registered key; device_key_id names that key.")
	r.deviceKeyID = backup.KeyID(dev.PublicKey)
	return r
}

// authenticate runs ingest-v1 2.5 steps 2 and 3.
func (s *Service) authenticate(r *http.Request) (Device, *refusal) {
	auth, ok := single(r.Header, "Authorization")
	scheme, token, found := strings.Cut(auth, " ")
	if !ok || !found || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return Device{}, refuse(CodeTokenMissing, "Send the device token as Authorization: Bearer ptd_...")
	}
	dev, err := s.o.Devices.Authenticate(r.Context(), strings.TrimSpace(token))
	switch {
	case errors.Is(err, ErrUnknownToken):
		return Device{}, refuse(CodeTokenInvalid, "The device token fails its checksum or names no device; sign in again.")
	case err != nil:
		s.o.Logger.Error("ingest: the device store failed", "err", err)
		return Device{}, unavailable()
	case dev.Revoked:
		return Device{}, refuse(CodeDeviceRevoked, "This device was revoked.")
	}
	if id, ok := single(r.Header, HeaderDevice); !ok || id != dev.ID {
		return Device{}, refuse(CodeDeviceMismatch, "X-Pairtime-Device must be the device the token belongs to.")
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

// checkReplay is ingest-v1 2.5 step 13 against the device's stored state.
// It reports whether it decided the request.
func checkReplay(st deviceState, env envelope) (outcome, bool) {
	if st.found && st.lastKey == env.key {
		if st.lastSHA != env.bodySHA {
			return outcome{refusal: refuse(CodeIdempotencyKeyReused, "This Idempotency-Key was used with another body; a new body takes a new key.")}, true
		}
		return outcome{response: st.lastResp, replay: true}, true
	}
	if env.req <= st.req {
		return outcome{refusal: refuse(CodeCounterStale, "X-Pairtime-Req is at or below the stored counter; read "+StatePath+" and continue above its req.")}, true
	}
	return outcome{}, false
}

// precheck runs step 13 on the read pool, so a replay or a stale counter
// never waits for the writer. The writer's op runs it again on the rows it
// holds; this read only spares it the work. A read that fails decides
// nothing.
func (s *Service) precheck(ctx context.Context, dev Device, env envelope) (outcome, bool) {
	st, err := loadState(ctx, s.o.Read, dev.ID)
	if err != nil {
		return outcome{}, false
	}
	return checkReplay(st, env)
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
	rv   Revision
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
// MaxRows: the device's state and the request's origin, then each item, a
// bucket counting twice for its revision row.
func (p parsed) rows() int {
	n := 2 + len(p.batches) + 2*len(p.buckets) + len(p.commits)
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

// process runs ingest-v1 2.5 steps 13 and 14 in the writer's transaction.
// It returns an error only when the database fails, which rolls back
// everything it wrote.
func (s *Service) process(ctx context.Context, tx writer.Tx, dev Device, env envelope, p parsed, ip, ja4 string) (outcome, error) {
	st, err := loadState(ctx, tx, dev.ID)
	if err != nil {
		return outcome{}, err
	}
	if out, done := checkReplay(st, env); done {
		return out, nil
	}
	if !st.found {
		if err := witnessHead(ctx, tx, dev.ID, &st); err != nil {
			return outcome{}, err
		}
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
		r, err := s.batch(ctx, tx, dev, &st, pb, now)
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
	if st.headB != 0 {
		resp.Head = &headJSON{B: st.headB, Head: st.headHash}
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return outcome{}, err
	}
	st.found = true
	st.req, st.lastKey, st.lastSHA, st.lastResp = env.req, env.key, env.bodySHA, data
	if err := saveState(ctx, tx, dev.ID, dev.Account, st, now); err != nil {
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
// st's head (ingest-v1 2.7).
func (s *Service) batch(ctx context.Context, tx writer.Tx, dev Device, st *deviceState, pb parsedBatch, now int64) (batchResult, error) {
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
	switch {
	case st.headB == 0 && br.b == 1 && br.prev != zero:
		return fail(http.StatusConflict, CodePrevMismatch)
	case st.headB == 0:
		// A device with no stored head starts its witnessed chain at any b
		// (ingest-v1 7 item 6; ledger-v1 6).
		st.chainStart = br.b
	case br.b > st.headB+1:
		return fail(http.StatusConflict, CodeChainGap)
	case br.b <= st.headB:
		return fail(http.StatusConflict, CodeBeforeChain)
	case br.prev != st.headHash:
		return fail(http.StatusConflict, CodePrevMismatch)
	}
	rc, err := ledger.SignReceipt(s.o.Key, dev.ID, br.b, br.head, uint64(now))
	if err != nil {
		return res, err
	}
	row := witnessRow{n: br.n, root: br.root, head: br.head, prev: br.prev, receivedAt: now, sig: hex.EncodeToString(rc.Signature[:]), keyID: s.keyID}
	if err := insertWitness(ctx, tx, dev.ID, dev.Account, br.b, row); err != nil {
		return res, err
	}
	st.headB, st.headHash, st.headAt = br.b, br.head, now
	res.Status, res.Code, res.Receipt = http.StatusOK, CodeAccepted, receipt(row)
	return res, nil
}

// bucket stores one minute bucket as a revision (ingest-v1 2.7).
func (s *Service) bucket(ctx context.Context, tx writer.Tx, dev Device, pb parsedBucket, now int64) (itemResult, error) {
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
	var prev *Revision
	switch {
	case !found:
	case rev > cur.rev && cur.revisions >= MaxRevisions:
		return storedRev(http.StatusConflict, CodeTooManyRevisions, cur.rev), nil
	case rev > cur.rev:
		revisions = cur.revisions + 1
		if s.o.Rollup != nil {
			prev = new(Revision)
			if err := json.Unmarshal([]byte(cur.bucket), prev); err != nil {
				return itemResult{}, err
			}
		}
	case rev == cur.rev && cur.bucket == string(pb.data):
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

// commit stores one commit record (ingest-v1 2.7).
func (s *Service) commit(ctx context.Context, tx writer.Tx, dev Device, pc parsedCommit, now int64) (itemResult, error) {
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
	ServerKey  serverKeyJSON  `json:"server_key"`
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
	st, err := loadState(r.Context(), s.o.Read, dev.ID)
	if err == nil && !st.found {
		err = witnessHead(r.Context(), s.o.Read, dev.ID, &st)
	}
	if err != nil {
		s.o.Logger.Error("ingest: reading a device's state failed", "err", err)
		s.refuse(w, unavailable())
		return
	}
	out := stateJSON{Device: dev.ID, Req: st.req, ServerKey: serverKeyJSON{KeyID: s.keyID, PublicKey: hex.EncodeToString(s.pub)}}
	if st.headB != 0 {
		start := st.chainStart
		out.Head = &stateHeadJSON{B: st.headB, Head: st.headHash, ReceivedAt: st.headAt}
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
