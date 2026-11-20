// SPDX-License-Identifier: AGPL-3.0-or-later

package ingest

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/server/backup"
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

// Device is a linked device, as the device store (WP-SV3) finds it by its
// token.
type Device struct {
	// ID is the opaque device_id, a random id.
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
type RollupFunc func(ctx context.Context, tx writer.Tx, account string, prev *event.MinuteBucket, next event.MinuteBucket) error

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
	// Read is the read pool, for GET /api/v1/ingest/state.
	Read *sql.DB
	// Key is the server key that signs receipts. Without it, ingest is
	// off.
	Key ed25519.PrivateKey
	// Rollup, when set, is called for every bucket revision stored.
	// TODO(WP-SV7): serve.go passes rollup.Apply through it, converting
	// event.MinuteBucket to rollup.Bucket, once package rollup merges.
	Rollup RollupFunc
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
	s := &Service{o: o}
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
}

func (s *Service) refuse(w http.ResponseWriter, r *refusal) {
	status, ok := refusals[r.code]
	if !ok {
		status = http.StatusInternalServerError
	}
	if status == http.StatusServiceUnavailable {
		w.Header().Set("Retry-After", "5")
	}
	b, _ := json.Marshal(errorBody{Error: r.code, Detail: r.detail})
	s.writeJSON(w, status, b)
}

var refusalOff = refuse(CodeIngestOff, "Ingest isn't set up on this server yet.")

func unavailable() *refusal {
	return refuse(CodeUnavailable, "The server couldn't store the upload; send it again later.")
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

// batches is POST /api/v1/ingest/batches.
func (s *Service) batches(w http.ResponseWriter, r *http.Request) {
	if !s.On() {
		s.refuse(w, refusalOff)
		return
	}
	dev, ref := s.authenticate(r)
	if ref != nil {
		s.refuse(w, ref)
		return
	}
	if len(dev.PublicKey) != ed25519.PublicKeySize {
		s.refuse(w, refuse(CodeDeviceKeyMissing, "Register the device's key before uploading."))
		return
	}
	env, ref := parseEnvelope(r.Header)
	if ref == nil {
		ref = checkContent(r.Header)
	}
	if ref == nil {
		if skew := env.time - s.nowMS(); skew > MaxSkewMS || skew < -MaxSkewMS {
			ref = refuse(CodeClockSkew, "X-Pairtime-Time is "+strconv.FormatInt(skew, 10)+" ms from the server's clock; correct the clock from this response's X-Pairtime-Time and sign again.")
		}
	}
	var raw []byte
	if ref == nil {
		raw, ref = readRaw(r)
	}
	if ref == nil {
		sum := sha256.Sum256(raw)
		switch {
		case hex.EncodeToString(sum[:]) != env.bodySHA:
			ref = refuse(CodeBodyHashMismatch, "The body's SHA-256 isn't X-Pairtime-Body-SHA256.")
		case !ed25519.Verify(dev.PublicKey, Message(dev.ID, env.reqText, env.timeText, env.bodySHA), env.signature):
			ref = refuse(CodeSignatureInvalid, "The signature doesn't verify under the device's registered key.")
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

	var out outcome
	rows := 1 + len(b.batches) + len(b.buckets) + len(b.commits)
	if b.labels != nil {
		rows++
	}
	err := s.o.Writer.Do(r.Context(), rows, func(ctx context.Context, tx writer.Tx) error {
		var err error
		out, err = s.process(ctx, tx, dev, env, b)
		return err
	})
	switch {
	case err != nil:
		if r.Context().Err() == nil {
			s.o.Logger.Error("ingest: storing an upload failed", "err", err)
		}
		s.refuse(w, unavailable())
	case out.refusal != nil:
		s.refuse(w, out.refusal)
	default:
		if out.replay {
			w.Header().Set(HeaderReplay, "true")
		}
		s.writeJSON(w, http.StatusOK, out.response)
	}
}

// outcome is what the writer op decided: a refusal, or the response to
// send, which is a stored one for a replay.
type outcome struct {
	refusal  *refusal
	response []byte
	replay   bool
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

func batchResultOf(e *itemErr) batchResult {
	r := resultOf(e)
	return batchResult{Status: r.Status, Code: r.Code, Field: r.Field}
}

// process runs ingest-v1 2.5 steps 13 and 14 in the writer's transaction.
// It returns an error only when the database fails, which rolls back
// everything it wrote.
func (s *Service) process(ctx context.Context, tx writer.Tx, dev Device, env envelope, b body) (outcome, error) {
	st, err := loadState(ctx, tx, dev.ID)
	if err != nil {
		return outcome{}, err
	}
	if st.found && st.lastKey == env.key {
		if st.lastSHA != env.bodySHA {
			return outcome{refusal: refuse(CodeIdempotencyKeyReused, "This Idempotency-Key was used with another body; a new body takes a new key.")}, nil
		}
		return outcome{response: st.lastResp, replay: true}, nil
	}
	if env.req <= st.req {
		return outcome{refusal: refuse(CodeCounterStale, "X-Pairtime-Req is at or below the stored counter; read "+StatePath+" and continue above its req.")}, nil
	}

	now := s.nowMS()
	resp := response{
		ReceivedAt: now,
		Req:        env.req,
		Batches:    make([]batchResult, 0, len(b.batches)),
		Buckets:    make([]itemResult, 0, len(b.buckets)),
		Commits:    make([]itemResult, 0, len(b.commits)),
	}
	for _, raw := range b.batches {
		r, err := s.batch(ctx, tx, dev, &st, raw, now)
		if err != nil {
			return outcome{}, err
		}
		resp.Batches = append(resp.Batches, r)
	}
	for _, raw := range b.buckets {
		r, err := s.bucket(ctx, tx, dev, raw, now)
		if err != nil {
			return outcome{}, err
		}
		resp.Buckets = append(resp.Buckets, r)
	}
	for _, raw := range b.commits {
		r, err := s.commit(ctx, tx, dev, raw, now)
		if err != nil {
			return outcome{}, err
		}
		resp.Commits = append(resp.Commits, r)
	}
	if b.labels != nil {
		r, err := s.labels(ctx, tx, dev, b.labels, now)
		if err != nil {
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
	st.req, st.lastKey, st.lastSHA, st.lastResp = env.req, env.key, env.bodySHA, data
	if err := saveState(ctx, tx, dev.ID, dev.Account, st, now); err != nil {
		return outcome{}, err
	}
	return outcome{response: data}, nil
}

// receipt returns a stored row's receipt in JSON.
func receipt(w witnessRow) *receiptJSON {
	return &receiptJSON{ReceivedAt: w.receivedAt, KeyID: w.keyID, Sig: w.sig}
}

// batch checks one batch root against the chain and stores it, moving
// st's head (ingest-v1 2.7).
func (s *Service) batch(ctx context.Context, tx writer.Tx, dev Device, st *deviceState, raw json.RawMessage, now int64) (batchResult, error) {
	br, e := parseBatchRoot(raw)
	if e != nil {
		return batchResultOf(e), nil
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
		// The device's first batch starts its witnessed chain, at any b
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

// bucket checks one minute bucket and stores it as a revision (ingest-v1
// 2.7).
func (s *Service) bucket(ctx context.Context, tx writer.Tx, dev Device, raw json.RawMessage, now int64) (itemResult, error) {
	mb, e := parseBucket(raw)
	if e != nil {
		return resultOf(e), nil
	}
	if err := mb.Validate(); err != nil {
		f := ruleField(err)
		return itemResult{Status: http.StatusUnprocessableEntity, Code: CodeBadRule, Field: &f}, nil
	}
	minute := mb.Minute.UnixMilli()
	switch {
	case mb.Device != dev.ID:
		return resultOf(refused(http.StatusUnprocessableEntity, CodeWrongDevice)), nil
	case minute > now+MaxSkewMS:
		return resultOf(refused(http.StatusUnprocessableEntity, CodeFutureMinute)), nil
	case minute <= now-BucketHorizonMS:
		return resultOf(refused(http.StatusUnprocessableEntity, CodeExpiredMinute)), nil
	}
	data, err := json.Marshal(mb)
	if err != nil {
		return itemResult{}, err
	}
	cur, found, err := loadBucket(ctx, tx, dev.ID, minute)
	if err != nil {
		return itemResult{}, err
	}
	rev := int64(mb.Rev)
	var prev *event.MinuteBucket
	switch {
	case !found:
	case rev > cur.rev:
		if s.o.Rollup != nil {
			prev = new(event.MinuteBucket)
			if err := json.Unmarshal([]byte(cur.bucket), prev); err != nil {
				return itemResult{}, err
			}
		}
	case rev == cur.rev && cur.bucket == string(data):
		return accepted(CodeDuplicate), nil
	case rev == cur.rev:
		return resultOf(refused(http.StatusConflict, CodeRevConflict)), nil
	default:
		return accepted(CodeSuperseded), nil
	}
	var replaced *storedBucket
	if found {
		replaced = &cur
	}
	if err := putBucket(ctx, tx, dev.ID, dev.Account, minute, rev, string(mb.State), data, now, replaced); err != nil {
		return itemResult{}, err
	}
	if s.o.Rollup != nil {
		if err := s.o.Rollup(ctx, tx, dev.Account, prev, mb); err != nil {
			return itemResult{}, err
		}
	}
	return accepted(CodeAccepted), nil
}

// commit checks one commit record and stores it (ingest-v1 2.7).
func (s *Service) commit(ctx context.Context, tx writer.Tx, dev Device, raw json.RawMessage, now int64) (itemResult, error) {
	c, e := parseCommit(raw)
	if e != nil {
		return resultOf(e), nil
	}
	data, err := json.Marshal(c)
	if err != nil {
		return itemResult{}, err
	}
	stored, found, err := loadCommit(ctx, tx, dev.ID, c.SHA)
	if err != nil {
		return itemResult{}, err
	}
	code := CodeAccepted
	switch {
	case found && stored == string(data):
		return accepted(CodeDuplicate), nil
	case found:
		code = CodeUpdated
	}
	if err := putCommit(ctx, tx, dev.ID, dev.Account, c, data, now); err != nil {
		return itemResult{}, err
	}
	return accepted(code), nil
}

// labels checks the labels and stores them as the device's (ingest-v1
// 2.7).
func (s *Service) labels(ctx context.Context, tx writer.Tx, dev Device, raw json.RawMessage, now int64) (itemResult, error) {
	l, e := parseLabels(raw)
	if e != nil {
		return resultOf(e), nil
	}
	data, err := json.Marshal(l)
	if err != nil {
		return itemResult{}, err
	}
	if err := putLabels(ctx, tx, dev.ID, dev.Account, data, now); err != nil {
		return itemResult{}, err
	}
	return accepted(CodeAccepted), nil
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

// state is GET /api/v1/ingest/state.
func (s *Service) state(w http.ResponseWriter, r *http.Request) {
	if !s.On() {
		s.refuse(w, refusalOff)
		return
	}
	dev, ref := s.authenticate(r)
	if ref != nil {
		s.refuse(w, ref)
		return
	}
	st, err := loadState(r.Context(), s.o.Read, dev.ID)
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
