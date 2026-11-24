// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"bytes"
	"net/http"
	"sync"

	"github.com/some-du6e/pairtime/internal/server/ingest"
)

// replayWindowMS is how long a copy of a signed request can still pass the
// clock check: its X-Pairtime-Time may be up to ingest.MaxSkewMS ahead of
// the server's clock when the first copy arrives, and as much behind it,
// that millisecond included, when a later one does.
const replayWindowMS = 2*ingest.MaxSkewMS + 1

// maxKeptRefusals bounds the store. A device's rate bounds what it can
// add, since only a request the rate admitted is kept: its burst of 30
// and one more every 10 s, at most 90 in a window. So the store reaches
// the bound only when about a hundred devices are refused at full rate at
// once, and it then drops the oldest refusal first.
const maxKeptRefusals = 10_000

// refusalStore keeps, for replayWindowMS and while pairtimed runs, the
// refusal each signed create or withdrawal its device's rate admitted
// got, by its device and signature. A copy of the request
// gets that refusal again instead of being checked anew, so it can't go
// through once the refusal no longer holds, as after a withdrawal frees
// the time a create overlapped (docs/spec/submissions-v1.md 2.2). A
// request that went through needs no entry: the create's request_id and
// the withdrawal's idempotency answer its copy with the stored submission,
// and it changes nothing. A client's retry signs a new X-Pairtime-Time,
// so it never matches.
type refusalStore struct {
	mu   sync.Mutex
	kept map[string]keptRefusal
	// order holds kept's keys in the order they were stored, so the oldest
	// expire first.
	order []string
}

// keptRefusal is a refusal as its request got it.
type keptRefusal struct {
	status                  int
	contentType, retryAfter string
	body                    []byte
	untilMS                 int64
}

// get returns the refusal kept under key, while it is kept.
func (rs *refusalStore) get(key string, nowMS int64) (keptRefusal, bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.expire(nowMS)
	k, ok := rs.kept[key]
	return k, ok && k.untilMS > nowMS
}

// put keeps k under key.
func (rs *refusalStore) put(key string, k keptRefusal, nowMS int64) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.expire(nowMS)
	if rs.kept == nil {
		rs.kept = make(map[string]keptRefusal)
	}
	if _, ok := rs.kept[key]; !ok {
		if len(rs.order) >= maxKeptRefusals {
			delete(rs.kept, rs.order[0])
			rs.order[0] = ""
			rs.order = rs.order[1:]
		}
		rs.order = append(rs.order, key)
	}
	rs.kept[key] = k
}

// expire drops the refusals whose time is up, oldest first. It reslices
// order rather than copying it, so an expiry costs only what it drops.
func (rs *refusalStore) expire(nowMS int64) {
	n := 0
	for ; n < len(rs.order); n++ {
		if k, ok := rs.kept[rs.order[n]]; ok && k.untilMS > nowMS {
			break
		}
		delete(rs.kept, rs.order[n])
	}
	clear(rs.order[:n])
	rs.order = rs.order[n:]
}

// recorder is the ResponseWriter a create or a withdrawal answers through.
// It keeps the status and the body, so keepRefusals can keep a refusal.
type recorder struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
	// key is the request's device and signature, which admit sets once the
	// signature verifies and the device's rate admits the request. "" keeps
	// nothing, as for a web session or a request past the rate.
	key string
}

func (r *recorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the connection, for
// readBody's read deadline.
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// keepRefusals answers a create or a withdrawal through a recorder, and
// keeps the refusal a signed one got, if it got one, for its copies.
func (s *Service) keepRefusals(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rec := &recorder{ResponseWriter: w}
		h(rec, r)
		if rec.key == "" || rec.status < 300 {
			return
		}
		now := s.o.Now().UnixMilli()
		s.refused.put(rec.key, keptRefusal{
			status:      rec.status,
			contentType: rec.Header().Get("Content-Type"),
			retryAfter:  rec.Header().Get("Retry-After"),
			body:        bytes.Clone(rec.body.Bytes()),
			untilMS:     now + replayWindowMS,
		}, now)
	}
}

// answerAgain gives a copy of a signed request the refusal the first one
// got.
func (s *Service) answerAgain(w http.ResponseWriter, k keptRefusal) {
	s.stamp(w)
	if k.contentType != "" {
		w.Header().Set("Content-Type", k.contentType)
	}
	if k.retryAfter != "" {
		w.Header().Set("Retry-After", k.retryAfter)
	}
	w.WriteHeader(k.status)
	w.Write(k.body)
}
