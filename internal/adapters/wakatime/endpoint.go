// SPDX-License-Identifier: AGPL-3.0-or-later

package wakatime

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/peer"
	"github.com/some-du6e/pairtime/pkg/wakacompat"
)

// Prefix is where the endpoint's routes sit on the agent's server. The
// installer writes the api_url http://127.0.0.1:<port>/api/v1, and
// wakatime-cli appends WakaTime's paths to it (DESIGN 5.6).
const Prefix = "/api/v1"

// MaxBulk is the most heartbeats one bulk request may hold, as Hackatime
// takes (MAX_BULK_HEARTBEATS, hackatime_controller.rb line 14).
// wakatime-cli sends at most 10.
const MaxBulk = 100

// maxBody bounds a request's body. wakatime-cli's 10 heartbeats come to a
// few kilobytes, and 100 with long paths and dependency lists stay far
// below it.
const maxBody = 1 << 20

// Horizon is how far back the endpoint remembers the heartbeats it took:
// 30 days, the window lookback reads (SM 2.8). A heartbeat from further
// back is answered as taken and passed on nowhere, because the endpoint
// can't tell it from one it took and forgot.
const Horizon = 30 * 24 * time.Hour

// maxSeen bounds the keys the endpoint holds, about 5 MB of them: a person
// coding all day for 30 days sends tens of thousands of heartbeats.
const maxSeen = 100_000

// Options are what an Endpoint needs from the agent.
type Options struct {
	// Key is the API key of pairtime's line in ~/.wakatime.cfg, a UUIDv4
	// the installer made (DESIGN 5.6). The endpoint takes no other.
	Key string
	// Peer returns the heartbeat rule of the peer-process check for the
	// connection r came on: Checker.Lookup's Result for the connection,
	// kept for its life, with Result.Heartbeat applied (internal/agent/
	// peer). Nil fails every connection, as an OS with no lookup does.
	Peer func(r *http.Request) peer.Result
	// Accept takes the heartbeats a request brought that the endpoint
	// hadn't taken before. While it returns an error, the request is
	// answered 503, its heartbeats aren't remembered, and wakatime-cli
	// sends them again later.
	Accept func(Batch) error
	// Today returns the status bar's seconds and text for today. Nil
	// answers 0 seconds and "0m".
	Today func(now time.Time) Today
	// Now is the clock. Nil is time.Now.
	Now func() time.Time
}

// Batch is what one request brought that the endpoint hadn't taken
// before.
type Batch struct {
	// Heartbeats are the new ones, in the order sent. A heartbeat with no
	// user agent of its own has the request's User-Agent, as Hackatime
	// gives it (heartbeat_ingest.rb line 112).
	Heartbeats []wakacompat.Heartbeat
	// Peer is the heartbeat rule's result for the request's connection.
	// Only one that passed makes the bridge's records editor or bridge;
	// any other, a lookup that found no client included, makes them
	// editor_unverified or bridge_unverified (DESIGN 5.7;
	// docs/errata/2026-11-19-bridge-unverified.md).
	Peer peer.Result
	// Received is when the endpoint took the request in.
	Received time.Time
}

// Today is what the status bar shows: wakatime-cli's --today prints Text
// (testdata/fixtures/wakatime/2.28.4/linux/local-only).
type Today struct {
	Seconds int64
	Text    string
}

// Counts are the heartbeats the endpoint answered since it started.
type Counts struct {
	// Taken were passed on, Duplicate answered as taken before, Stale
	// answered as taken but too old to pass on, and Refused refused.
	Taken, Duplicate, Stale, Refused int
}

// Endpoint is the local WakaTime endpoint: POST
// {Prefix}/users/{user}/heartbeats and heartbeats.bulk, and GET
// {Prefix}/users/{user}/statusbar/today (DESIGN 5.6). Like Hackatime it
// reads the user from the key, never from {user}, which wakatime-cli
// sends as current. It is safe for concurrent use.
type Endpoint struct {
	key    []byte
	peer   func(*http.Request) peer.Result
	accept func(Batch) error
	today  func(time.Time) Today
	now    func() time.Time
	mux    *http.ServeMux

	// mu orders the requests that bring heartbeats, so that two copies of
	// one heartbeat in flight at once are passed on once.
	mu     sync.Mutex
	seen   *wakacompat.Seen
	counts Counts
}

// errNoKey refuses an Endpoint without a key, which would take any
// request that sent an empty one.
var errNoKey = errors.New("wakatime: the endpoint needs the key of pairtime's line in ~/.wakatime.cfg")

// New returns an Endpoint.
func New(opts Options) (*Endpoint, error) {
	if opts.Key == "" {
		return nil, errNoKey
	}
	e := &Endpoint{
		key:    []byte(opts.Key),
		peer:   opts.Peer,
		accept: opts.Accept,
		today:  opts.Today,
		now:    opts.Now,
		seen:   wakacompat.NewSeen(Horizon, maxSeen),
	}
	if e.peer == nil {
		e.peer = noPeer
	}
	if e.now == nil {
		e.now = time.Now
	}
	e.mux = http.NewServeMux()
	e.mux.HandleFunc("POST "+Prefix+"/users/{user}/heartbeats.bulk", e.serveBulk)
	e.mux.HandleFunc("POST "+Prefix+"/users/{user}/heartbeats", e.serveOne)
	e.mux.HandleFunc("GET "+Prefix+"/users/{user}/statusbar/today", e.serveToday)
	return e, nil
}

// errNoCheck is why a connection fails when the agent gave the endpoint
// no peer check.
var errNoCheck = errors.New("wakatime: no peer-process check")

func noPeer(*http.Request) peer.Result {
	return peer.Result{Reason: peer.NoOwner, Editor: -1, Err: errNoCheck}
}

// ServeHTTP answers the endpoint's routes, and 404 or 405 anything else.
func (e *Endpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) { e.mux.ServeHTTP(w, r) }

// Counts returns the heartbeats answered so far.
func (e *Endpoint) Counts() Counts {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.counts
}

// The bodies of the answers, in Hackatime's shapes: error.schema.json,
// heartbeat.schema.json, heartbeat_error.schema.json,
// heartbeats_bulk.schema.json and statusbar_today.schema.json in
// testdata/upstream/hackatime/responses.
type (
	errorBody struct {
		Error string `json:"error"`
	}
	rowBody struct {
		ID     int64   `json:"id"`
		Time   float64 `json:"time"`
		Entity string  `json:"entity"`
		Type   *string `json:"type"`
	}
	refusalBody struct {
		Error string `json:"error"`
		Type  string `json:"type"`
	}
	bulkBody struct {
		Responses [][2]any `json:"responses"`
	}
	todayBody struct {
		Data struct {
			GrandTotal struct {
				Text         string `json:"text"`
				TotalSeconds int64  `json:"total_seconds"`
			} `json:"grand_total"`
		} `json:"data"`
	}
)

// The messages of the answers that refuse a whole request. Those for no
// heartbeat and too many are Hackatime's (hackatime_controller.rb lines
// 62-64, 71), and so is Unauthorized (render_helpers.rb).
const (
	msgUnauthorized = "Unauthorized"
	msgNoData       = "No data provided..."
	msgTooMany      = "Too many heartbeats in a single request (max 100)"
	msgTooLarge     = "Request body too large"
	msgUnavailable  = "pairtime couldn't record these heartbeats; send them again"
)

// serveBulk answers heartbeats.bulk: 201 with one entry per heartbeat, in
// the order sent, the row and 201 for one it took, now or before, and
// Hackatime's refusal body and 400 for one it refused.
//
// Hackatime refuses with 422, and pairtime with 400 on purpose
// (docs/errata/2026-11-23-wakatime-cli-2.28.4.md, item 2): wakatime-cli
// drops a heartbeat that got 400, and queues one that got any other status
// outside 2xx to send again, to Hackatime as well, at every sync.
func (e *Endpoint) serveBulk(w http.ResponseWriter, r *http.Request) {
	body, ok := e.begin(w, r)
	if !ok {
		return
	}
	items, ok := bulkItems(body)
	switch {
	case !ok || len(items) == 0:
		writeJSON(w, http.StatusBadRequest, errorBody{msgNoData})
		return
	case len(items) > MaxBulk:
		writeJSON(w, http.StatusBadRequest, errorBody{msgTooMany})
		return
	}
	answers, ok := e.take(r, items)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, errorBody{msgUnavailable})
		return
	}
	out := bulkBody{Responses: make([][2]any, len(answers))}
	for i, a := range answers {
		out.Responses[i] = [2]any{a.body, a.status}
	}
	writeJSON(w, http.StatusCreated, out)
}

// serveOne answers heartbeats: 202 and the row for a heartbeat it took,
// now or before, and 400 and Hackatime's refusal body for one it refused.
// A JSON array's first element is the heartbeat, as Hackatime reads it
// (hackatime_controller.rb lines 264-276).
func (e *Endpoint) serveOne(w http.ResponseWriter, r *http.Request) {
	body, ok := e.begin(w, r)
	if !ok {
		return
	}
	item, ok := oneItem(body)
	if !ok {
		writeJSON(w, http.StatusBadRequest, errorBody{msgNoData})
		return
	}
	answers, ok := e.take(r, []json.RawMessage{item})
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, errorBody{msgUnavailable})
		return
	}
	a := answers[0]
	if a.status == http.StatusCreated {
		a.status = http.StatusAccepted
	}
	writeJSON(w, a.status, a.body)
}

// serveToday answers statusbar/today.
func (e *Endpoint) serveToday(w http.ResponseWriter, r *http.Request) {
	if !e.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, errorBody{msgUnauthorized})
		return
	}
	t := Today{Text: "0m"}
	if e.today != nil {
		t = e.today(e.now())
	}
	if t.Text == "" {
		t.Text = "0m"
	}
	var out todayBody
	out.Data.GrandTotal.Text = t.Text
	out.Data.GrandTotal.TotalSeconds = max(t.Seconds, 0)
	writeJSON(w, http.StatusOK, out)
}

// begin checks a heartbeat request's key and reads its body, answering
// the request itself when either fails.
func (e *Endpoint) begin(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if !e.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, errorBody{msgUnauthorized})
		return nil, false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorBody{msgTooLarge})
		} else {
			writeJSON(w, http.StatusBadRequest, errorBody{msgNoData})
		}
		return nil, false
	}
	return body, true
}

// authorized reports whether r carries the endpoint's key, read as
// Hackatime reads an API key (user_api_authentication.rb lines 24-38):
// Authorization: Bearer <key>; Authorization: Basic with the base64 of the
// key alone, which is what wakatime-cli sends; or else the api_key query
// parameter. A Basic credential is decoded whole, so the base64 of
// <key>:<anything> carries no key.
func (e *Endpoint) authorized(r *http.Request) bool {
	scheme, cred, _ := strings.Cut(r.Header.Get("Authorization"), " ")
	cred = strings.TrimLeft(cred, " \t")
	var token string
	switch {
	case strings.EqualFold(scheme, "Bearer"):
		token = cred
	case strings.EqualFold(scheme, "Basic"):
		b, err := base64.StdEncoding.Strict().DecodeString(cred)
		if err != nil {
			return false
		}
		token = string(b)
	default:
		token = r.URL.Query().Get("api_key")
	}
	return token != "" && subtle.ConstantTimeCompare([]byte(token), e.key) == 1
}

// answer is one heartbeat's entry: a body and its status.
type answer struct {
	body   any
	status int
}

// take parses a request's heartbeats, passes the new ones on through
// Accept, and returns one answer per item. It returns false, and
// remembers nothing, when Accept fails.
func (e *Endpoint) take(r *http.Request, items []json.RawMessage) ([]answer, bool) {
	verdict := e.peer(r)
	answers := make([]answer, len(items))
	type fresh struct {
		key wakacompat.Key
		t   float64
	}
	var (
		batch Batch
		keys  []fresh
		in    = make(map[wakacompat.Key]bool)
		c     Counts
	)
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	for i, raw := range items {
		h, err := wakacompat.Parse(raw)
		if err != nil {
			var ref *wakacompat.Refusal
			if !errors.As(err, &ref) {
				ref = &wakacompat.Refusal{Type: wakacompat.InvalidHeartbeat, Message: "unreadable heartbeat"}
			}
			answers[i] = answer{refusalBody{Error: ref.Message, Type: ref.Type}, http.StatusBadRequest}
			c.Refused++
			continue
		}
		if h.UserAgent == "" {
			h.UserAgent = r.UserAgent()
		}
		k := h.Key()
		answers[i] = answer{row(k, h), http.StatusCreated}
		switch e.seen.Check(k, h.Time, now) {
		case wakacompat.Stale:
			c.Stale++
		case wakacompat.Taken:
			c.Duplicate++
		default:
			if in[k] {
				c.Duplicate++
				continue
			}
			in[k] = true
			batch.Heartbeats = append(batch.Heartbeats, h)
			keys = append(keys, fresh{k, h.Time})
			c.Taken++
		}
	}
	if len(batch.Heartbeats) > 0 && e.accept != nil {
		batch.Peer, batch.Received = verdict, now
		if err := e.accept(batch); err != nil {
			return nil, false
		}
	}
	for _, f := range keys {
		e.seen.Add(f.key, f.t, now)
	}
	e.counts.Taken += c.Taken
	e.counts.Duplicate += c.Duplicate
	e.counts.Stale += c.Stale
	e.counts.Refused += c.Refused
	return answers, true
}

// row is the answer for a heartbeat the endpoint took. Its id comes from
// the heartbeat's Key, so a replay gets its first send's id, as Hackatime
// gives a replay its first send's row. The id fits in 52 bits, which a
// JSON number holds exactly in every client.
func row(k wakacompat.Key, h wakacompat.Heartbeat) rowBody {
	b := rowBody{ID: int64(binary.BigEndian.Uint64(k[:8])>>12) + 1, Time: h.Time, Entity: h.Entity}
	if h.Type != "" {
		b.Type = &h.Type
	}
	return b
}

// bulkItems reads a bulk body in the forms Hackatime takes
// (hackatime_controller.rb lines 252-262): a JSON array, an object whose
// hackatime holds an object with the array under heartbeats, or, as Rails
// also wraps it, an object with the array under heartbeats. The
// Content-Type doesn't matter, so text/plain JSON reads too.
func bulkItems(body []byte) ([]json.RawMessage, bool) {
	var items []json.RawMessage
	if err := json.Unmarshal(body, &items); err == nil {
		return items, items != nil
	}
	var obj struct {
		Heartbeats []json.RawMessage `json:"heartbeats"`
		Hackatime  *struct {
			Heartbeats []json.RawMessage `json:"heartbeats"`
		} `json:"hackatime"`
	}
	if json.Unmarshal(body, &obj) != nil {
		return nil, false
	}
	if obj.Hackatime != nil && obj.Hackatime.Heartbeats != nil {
		return obj.Hackatime.Heartbeats, true
	}
	return obj.Heartbeats, obj.Heartbeats != nil
}

// oneItem reads a single heartbeat's body in the forms Hackatime takes
// (hackatime_controller.rb lines 264-276): an object, the first element of
// an array, or the object under hackatime.
func oneItem(body []byte) (json.RawMessage, bool) {
	var items []json.RawMessage
	if json.Unmarshal(body, &items) == nil {
		if len(items) == 0 {
			return nil, false
		}
		return items[0], true
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(body, &obj) != nil || len(obj) == 0 {
		return nil, false
	}
	if inner, ok := obj["hackatime"]; ok && len(inner) > 0 && inner[0] == '{' {
		return inner, true
	}
	return json.RawMessage(body), true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}
