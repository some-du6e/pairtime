// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/ledger"
)

// fakeIngest is a pairtimed ingest written from docs/spec/ingest-v1.md
// alone, sharing none of the uploader's wire types, so a member the two
// spell differently fails here. It checks a request in 2.5's order and
// answers as 2.6 to 2.9 say, and keeps its state the way the server's
// database does, across the restarts the tests inject. WP-SV4's handler
// takes its place once it is on main.
type fakeIngest struct {
	device string
	token  string
	devKey ed25519.PublicKey
	key    ed25519.PrivateKey // the server's receipt key
	keyID  string
	now    func() time.Time

	mu                sync.Mutex
	faults            map[int]string // by POST attempt, from 1: "429", "503", "lost", "down", "mismatch", "stale-fork"
	posts             int            // POST attempts that reached the handler
	gets              int
	bodies            [][]byte // the inflated body of every signed POST attempt, in order
	counter           uint64
	head              *fakeRow
	start             uint64 // chain_start
	rows              map[uint64]fakeRow
	buckets           map[int64]event.MinuteBucket // current revision by minute
	revs              int                          // revisions accepted
	labels            int                          // labels accepted
	futures           int                          // buckets answered future_minute
	relinked          bool                         // the device re-linked, so its next batch may skip a gap
	gaps              [][2]uint64                  // the local-only gaps it recorded, first and last
	witness           map[event.Witness]int        // the readings of the revisions accepted
	lastKey, lastHash string
	lastResp          []byte
	replays           int
}

type fakeRow struct {
	b, n             uint64
	root, head, prev string
	receivedAt       uint64
	sig              string
	keyID            string // the key that signed sig, which a duplicate names, as WP-SV4's does
}

func newFakeIngest(device, token string, devKey ed25519.PublicKey) *fakeIngest {
	seed := sha256.Sum256([]byte("fake ingest receipt key"))
	key := ed25519.NewKeyFromSeed(seed[:])
	pub := key.Public().(ed25519.PublicKey)
	sum := sha256.Sum256(pub)
	return &fakeIngest{
		device: device, token: token, devKey: devKey, key: key, keyID: hex.EncodeToString(sum[:8]),
		now: time.Now, faults: map[int]string{}, rows: map[uint64]fakeRow{}, buckets: map[int64]event.MinuteBucket{},
		witness: map[event.Witness]int{},
	}
}

var (
	uuid7Form   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	decimalForm = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)
	hashForm    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	scoringForm = regexp.MustCompile(`^[0-9]{1,9}\.[0-9]{1,9}\.[0-9]{1,9}$`)
	// releaseForm is D-version-grammar's version, for client_build and
	// harness versions.
	releaseForm = regexp.MustCompile(`^v?[0-9]+(\.[0-9]+){0,3}([-+][0-9A-Za-z][0-9A-Za-z.-]{0,40})?$`)
	paramsForm  = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

func (f *fakeIngest) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Pairtime-Time", strconv.FormatInt(f.now().UnixMilli(), 10))
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/time":
		f.json(w, 200, map[string]any{"server_time": f.now().UnixMilli()})
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/ingest/state":
		f.state(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/ingest/batches":
		f.batches(w, r)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeIngest) json(w http.ResponseWriter, status int, v any) []byte {
	b, _ := json.Marshal(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(b)
	return b
}

func (f *fakeIngest) refuse(w http.ResponseWriter, status int, code string) {
	f.json(w, status, map[string]string{"error": code, "detail": "refused by the fake ingest"})
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

func (f *fakeIngest) auth(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		f.refuse(w, 401, "token_invalid")
		return false
	}
	if r.Header.Get("X-Pairtime-Device") != f.device {
		f.refuse(w, 403, "device_mismatch")
		return false
	}
	return true
}

func (f *fakeIngest) state(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++
	if !f.auth(w, r) {
		return
	}
	timeS, sigS := r.Header.Get("X-Pairtime-Time"), r.Header.Get("X-Pairtime-Signature")
	if !decimalForm.MatchString(timeS) {
		f.refuse(w, 400, "time_malformed")
		return
	}
	if len(sigS) != len("ed25519=")+88 || sigS[:8] != "ed25519=" {
		f.refuse(w, 400, "signature_malformed")
		return
	}
	if t, _ := strconv.ParseInt(timeS, 10, 64); t-f.now().UnixMilli() > 300_000 || f.now().UnixMilli()-t > 300_000 {
		f.refuse(w, 401, "clock_skew")
		return
	}
	sig, err := base64.StdEncoding.DecodeString(sigS[8:])
	if err != nil || !ed25519.Verify(f.devKey, []byte("pairtime-ingest-state-v1\n"+f.device+"\n"+timeS), sig) {
		f.refuse(w, 401, "signature_invalid")
		return
	}
	var head, start any
	if f.head != nil {
		head = map[string]any{"b": f.head.b, "H_b": f.head.head, "received_at": f.head.receivedAt}
		start = f.start
	}
	gaps := []map[string]uint64{}
	for _, g := range f.gaps {
		gaps = append(gaps, map[string]uint64{"first": g[0], "last": g[1]})
	}
	f.json(w, 200, map[string]any{
		"device": f.device, "req": f.counter, "head": head, "chain_start": start,
		"relinked": f.relinked && f.head != nil, "gaps": gaps,
		"server_key": map[string]string{"key_id": f.keyID, "public_key": hex.EncodeToString(f.key.Public().(ed25519.PublicKey))},
	})
}

func (f *fakeIngest) batches(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posts++
	fault := f.faults[f.posts]
	raw, err := io.ReadAll(io.LimitReader(r.Body, 262_145))
	if err != nil {
		drop(w)
		return
	}
	if fault == "down" {
		drop(w)
		return
	}
	if !f.auth(w, r) {
		return
	}
	h := r.Header
	key, reqS, timeS, hash, sigS := h.Get("Idempotency-Key"), h.Get("X-Pairtime-Req"), h.Get("X-Pairtime-Time"), h.Get("X-Pairtime-Body-SHA256"), h.Get("X-Pairtime-Signature")
	switch {
	case !uuid7Form.MatchString(key):
		f.refuse(w, 400, "idempotency_key_malformed")
		return
	case !decimalForm.MatchString(reqS):
		f.refuse(w, 400, "counter_malformed")
		return
	case !decimalForm.MatchString(timeS):
		f.refuse(w, 400, "time_malformed")
		return
	case !hashForm.MatchString(hash):
		f.refuse(w, 400, "body_hash_malformed")
		return
	case len(sigS) != len("ed25519=")+88 || sigS[:8] != "ed25519=":
		f.refuse(w, 400, "signature_malformed")
		return
	}
	sig, err := base64.StdEncoding.DecodeString(sigS[8:])
	if err != nil || len(sig) != 64 {
		f.refuse(w, 400, "signature_malformed")
		return
	}
	if h.Get("Content-Type") != "application/json" {
		f.refuse(w, 415, "content_type_unsupported")
		return
	}
	if h.Get("Content-Encoding") != "gzip" {
		f.refuse(w, 415, "encoding_unsupported")
		return
	}
	t, _ := strconv.ParseInt(timeS, 10, 64)
	if d := t - f.now().UnixMilli(); d > 300_000 || d < -300_000 {
		f.refuse(w, 401, "clock_skew")
		return
	}
	if len(raw) > 262_144 {
		f.refuse(w, 413, "body_too_large")
		return
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != hash {
		f.refuse(w, 400, "body_hash_mismatch")
		return
	}
	m := "pairtime-ingest-v1\n" + f.device + "\n" + reqS + "\n" + timeS + "\n" + hash
	if !ed25519.Verify(f.devKey, []byte(m), sig) {
		f.refuse(w, 401, "signature_invalid")
		return
	}
	br := bytes.NewReader(raw)
	zr, err := gzip.NewReader(br)
	if err != nil {
		f.refuse(w, 400, "gzip_invalid")
		return
	}
	zr.Multistream(false)
	js, err := io.ReadAll(io.LimitReader(zr, 4_194_305))
	if err != nil || br.Len() != 0 {
		// Not one whole member, or bytes after it.
		f.refuse(w, 400, "gzip_invalid")
		return
	}
	if len(js) > 4_194_304 {
		f.refuse(w, 413, "decompressed_too_large")
		return
	}
	f.bodies = append(f.bodies, js)
	switch fault {
	case "429":
		w.Header().Set("Retry-After", "1")
		f.refuse(w, 429, "too_many_requests")
		return
	case "503":
		f.refuse(w, 503, "unavailable")
		return
	case "mismatch":
		// The device id is another account's.
		f.refuse(w, 403, "device_mismatch")
		return
	case "stale-fork":
		// Another computer with the same identity got there first: it took
		// higher counters and stored its own batch after the head.
		var root, head ledger.Hash
		root[0], head[0] = 0xf0, 0xf1
		b := uint64(1)
		prev := ledger.Hash{}.String()
		if f.head != nil {
			b, prev = f.head.b+1, f.head.head
		}
		row := fakeRow{b: b, n: 1, root: root.String(), head: head.String(), prev: prev, receivedAt: uint64(f.now().UnixMilli()), sig: "00", keyID: f.keyID}
		f.rows[b] = row
		f.head = &row
		f.counter += 1000
		f.refuse(w, 409, "counter_stale")
		return
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(js, &top); err != nil {
		f.refuse(w, 400, "json_invalid")
		return
	}
	var roots, buckets, commits []json.RawMessage
	var labels json.RawMessage
	for k, v := range top {
		var err error
		switch k {
		case "batches":
			err = json.Unmarshal(v, &roots)
		case "buckets":
			err = json.Unmarshal(v, &buckets)
		case "commits":
			err = json.Unmarshal(v, &commits)
		case "labels":
			labels = v
			if len(v) == 0 || v[0] != '{' {
				err = errors.New("labels isn't an object")
			}
		default:
			err = fmt.Errorf("member %q", k)
		}
		if err != nil {
			f.refuse(w, 422, "body_shape")
			return
		}
	}
	if len(roots) > 2000 || len(buckets) > 2000 || len(commits) > 500 {
		f.refuse(w, 413, "too_many_batches")
		return
	}
	if key == f.lastKey {
		if hash != f.lastHash {
			f.refuse(w, 422, "idempotency_key_reused")
			return
		}
		f.replays++
		w.Header().Set("X-Pairtime-Replay", "true")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write(f.lastResp)
		return
	}
	req, _ := strconv.ParseUint(reqS, 10, 64)
	if req <= f.counter {
		f.refuse(w, 409, "counter_stale")
		return
	}
	receivedAt := uint64(f.now().UnixMilli())
	resp := map[string]any{"received_at": receivedAt, "req": req, "commits": []any{}, "labels": nil}
	bres := []map[string]any{}
	for _, raw := range roots {
		bres = append(bres, f.root(raw, receivedAt))
	}
	kres := []map[string]any{}
	for _, raw := range buckets {
		kres = append(kres, f.bucket(raw))
	}
	resp["batches"], resp["buckets"] = bres, kres
	if labels != nil {
		resp["labels"] = f.label(labels)
	}
	if f.head != nil {
		resp["head"] = map[string]any{"b": f.head.b, "H_b": f.head.head}
	} else {
		resp["head"] = nil
	}
	f.counter, f.lastKey, f.lastHash = req, key, hash
	if fault == "lost" {
		// The answer is stored, and the server stops before sending it.
		f.lastResp, _ = json.Marshal(resp)
		drop(w)
		return
	}
	f.lastResp = f.json(w, 200, resp)
}

func (f *fakeIngest) root(raw json.RawMessage, receivedAt uint64) map[string]any {
	var r struct {
		B    *uint64 `json:"b"`
		N    *uint64 `json:"n"`
		Root string  `json:"R_b"`
		Head string  `json:"H_b"`
		Prev string  `json:"prev"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return map[string]any{"status": 422, "code": "unknown_field", "field": ""}
	}
	if r.B == nil || r.N == nil || !hashForm.MatchString(r.Root) || !hashForm.MatchString(r.Head) || !hashForm.MatchString(r.Prev) {
		return map[string]any{"status": 422, "code": "missing_field"}
	}
	b, n := *r.B, *r.N
	if n == 0 {
		return map[string]any{"status": 422, "code": "batch_empty", "b": b}
	}
	var prev, root ledger.Hash
	pb, _ := hex.DecodeString(r.Prev)
	rb, _ := hex.DecodeString(r.Root)
	copy(prev[:], pb)
	copy(root[:], rb)
	if ledger.BatchHead(prev, root, n, b).String() != r.Head {
		return map[string]any{"status": 422, "code": "head_mismatch", "b": b}
	}
	if old, ok := f.rows[b]; ok {
		if old.n == n && old.root == r.Root && old.head == r.Head && old.prev == r.Prev {
			return map[string]any{"status": 200, "code": "duplicate", "b": b,
				"receipt": map[string]any{"received_at": old.receivedAt, "key_id": old.keyID, "sig": old.sig}}
		}
		return map[string]any{"status": 409, "code": "batch_conflict", "b": b}
	}
	switch {
	case f.head == nil && b == 1 && r.Prev != ledger.Hash{}.String():
		return map[string]any{"status": 409, "code": "prev_mismatch", "b": b}
	case f.head == nil:
		f.start = b
	case b > f.head.b+1 && f.relinked:
		// After a re-link the first batch may skip a local-only gap, with its
		// prev taken as given (D-local-gap).
		f.relinked = false
		f.gaps = append(f.gaps, [2]uint64{f.head.b + 1, b - 1})
	case b > f.head.b+1:
		return map[string]any{"status": 409, "code": "chain_gap", "b": b}
	case b <= f.head.b:
		return map[string]any{"status": 409, "code": "before_chain", "b": b}
	case r.Prev != f.head.head:
		return map[string]any{"status": 409, "code": "prev_mismatch", "b": b}
	}
	var head ledger.Hash
	hb, _ := hex.DecodeString(r.Head)
	copy(head[:], hb)
	rc, err := ledger.SignReceipt(f.key, f.device, b, head, receivedAt)
	if err != nil {
		panic(err)
	}
	row := fakeRow{b: b, n: n, root: r.Root, head: r.Head, prev: r.Prev, receivedAt: receivedAt, sig: hex.EncodeToString(rc.Signature[:]), keyID: f.keyID}
	f.rows[b] = row
	f.head = &row
	f.relinked = false // a batch stored under the new sign-in ends it (ingest-v1 3)
	return map[string]any{"status": 200, "code": "accepted", "b": b,
		"receipt": map[string]any{"received_at": receivedAt, "key_id": f.keyID, "sig": row.sig}}
}

func (f *fakeIngest) bucket(raw json.RawMessage) map[string]any {
	// Only DESIGN 4.3's members, as pkg/event lists them: anchors, which
	// event.MinuteBucket would decode, is refused here as the real
	// allowlist refuses it.
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		return map[string]any{"status": 422, "code": "not_object"}
	}
	for k := range members {
		if !slices.Contains(event.Tier1BucketFields(), k) {
			return map[string]any{"status": 422, "code": "unknown_field", "field": ""}
		}
	}
	var slots []map[string]json.RawMessage
	if err := json.Unmarshal(members["slots"], &slots); err != nil {
		return map[string]any{"status": 422, "code": "bad_type", "field": "slots"}
	}
	for i, sl := range slots {
		for k := range sl {
			if !slices.Contains(event.Tier1SlotFields(), k) {
				return map[string]any{"status": 422, "code": "unknown_field", "field": fmt.Sprintf("slots[%d]", i)}
			}
		}
	}
	var mb event.MinuteBucket
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&mb); err != nil {
		return map[string]any{"status": 422, "code": "unknown_field", "field": ""}
	}
	if !scoringForm.MatchString(mb.V) || !paramsForm.MatchString(mb.Params) {
		return map[string]any{"status": 422, "code": "bad_string"}
	}
	var probe struct {
		Minute *string `json:"minute"`
	}
	if json.Unmarshal(raw, &probe) != nil || probe.Minute == nil || len(*probe.Minute) != 20 {
		return map[string]any{"status": 422, "code": "bad_string", "field": "minute"}
	}
	if err := mb.Validate(); err != nil {
		return map[string]any{"status": 422, "code": "bad_rule"}
	}
	if mb.Device != f.device {
		return map[string]any{"status": 422, "code": "wrong_device"}
	}
	now := f.now()
	if mb.Minute.After(now.Add(300 * time.Second)) {
		f.futures++
		return map[string]any{"status": 422, "code": "future_minute"}
	}
	if !mb.Minute.After(now.Add(-180 * 24 * time.Hour)) {
		return map[string]any{"status": 422, "code": "expired_minute"}
	}
	ms := mb.Minute.UnixMilli()
	cur, ok := f.buckets[ms]
	switch {
	case !ok || cur.Rev < mb.Rev:
		f.buckets[ms] = mb
		f.revs++
		f.witness[mb.Witness]++
		return map[string]any{"status": 200, "code": "accepted"}
	case cur.Rev > mb.Rev:
		return map[string]any{"status": 200, "code": "superseded", "rev": cur.Rev}
	}
	a, _ := json.Marshal(cur)
	b, _ := json.Marshal(mb)
	if bytes.Equal(a, b) {
		return map[string]any{"status": 200, "code": "duplicate"}
	}
	return map[string]any{"status": 409, "code": "rev_conflict", "rev": cur.Rev}
}

func (f *fakeIngest) label(raw json.RawMessage) map[string]any {
	var l struct {
		ClientBuild    string `json:"client_build"`
		ScoringVersion string `json:"scoring_version"`
		Params         string `json:"params"`
		Harnesses      *[]struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"harnesses"`
		TypingModule *bool `json:"typing_module"`
		VM           *bool `json:"vm"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&l); err != nil {
		return map[string]any{"status": 422, "code": "unknown_field", "field": ""}
	}
	if !releaseForm.MatchString(l.ClientBuild) || len(l.ClientBuild) > 64 || !scoringForm.MatchString(l.ScoringVersion) || !paramsForm.MatchString(l.Params) ||
		l.Harnesses == nil || len(*l.Harnesses) > 16 || l.TypingModule == nil || l.VM == nil {
		return map[string]any{"status": 422, "code": "bad_string"}
	}
	for _, h := range *l.Harnesses {
		if h.Version != "" && (!releaseForm.MatchString(h.Version) || len(h.Version) > 64) {
			return map[string]any{"status": 422, "code": "bad_string", "field": "harnesses"}
		}
	}
	f.labels++
	return map[string]any{"status": 200, "code": "accepted"}
}

// rotate gives the server a new receipt key, as DESIGN 10.2's yearly
// rotation does. The rows it holds keep the receipts the old key signed.
func (f *fakeIngest) rotate(name string) {
	seed := sha256.Sum256([]byte(name))
	f.key = ed25519.NewKeyFromSeed(seed[:])
	sum := sha256.Sum256(f.key.Public().(ed25519.PublicKey))
	f.keyID = hex.EncodeToString(sum[:8])
}

// totals sums the server's current revisions.
func (f *fakeIngest) totals() (minutes int, credited, agent float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, b := range f.buckets {
		minutes++
		credited += b.CreditedS
		agent += b.AgentRunningS
	}
	return minutes, credited, agent
}

func (f *fakeIngest) snapshot() (posts int, bodies [][]byte, head uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.head != nil {
		head = f.head.b
	}
	return f.posts, append([][]byte(nil), f.bodies...), head
}
