// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/some-du6e/pairtime/internal/server/ingest"
)

// maxKeptRefusals bounds the store. A device's rate bounds what it can
// add, since only a request the rate admitted is kept: its burst of 30
// and one more every 10 s, at most 96 in the 660 s an entry stays, the
// 600 s a copy can pass the clock check and keepGraceMS. So the store
// reaches the bound only when about a hundred devices are refused at full
// rate at once, and it then drops the oldest refusal first.
const maxKeptRefusals = 10_000

// keepGraceMS is how long an entry stays after the last millisecond a
// copy can pass the clock check. A copy takes its clock check's time
// before its body arrives, which may take ingest.BodyTimeout, and looks
// the store up only once its signature verifies, by then. Another
// request's store call meanwhile mustn't drop the entry the copy will ask
// for, so expiry waits that long and a second more, while get still
// answers only up to the clock check's last millisecond.
const keepGraceMS = int64(ingest.BodyTimeout/time.Millisecond) + 1000

// refusalStore keeps, while pairtimed runs, the status and error code of
// the refusal each signed create or withdrawal got once its device's rate
// admitted it, by its device and signature, until the last millisecond a
// copy of the request can pass the clock check. A copy gets that status
// and code again, before the rate check, instead of being checked anew,
// so it can't go through once the refusal no longer holds, as after a
// withdrawal frees the time a create overlapped (docs/spec/
// submissions-v1.md 2.2). A copy is a replay, not the client's retry,
// which signs a new X-Pairtime-Time, so it needs no detail. A request that
// went through needs no entry: the create's request_id and the
// withdrawal's idempotency answer its copy with the stored submission.
// The store's limits: a request the rate refused isn't kept, so its copy
// is checked anew once the rate admits it; a restart forgets every entry;
// and past maxKeptRefusals the oldest goes early.
type refusalStore struct {
	mu   sync.Mutex
	kept map[string]keptRefusal
	// order holds kept's keys in the order they were stored, so the store
	// drops the oldest first when it is full.
	order []string
}

// keptRefusal is a refusal as a copy of its request gets it again.
type keptRefusal struct {
	status int
	code   string
	// untilMS is the first millisecond a copy no longer passes the clock
	// check: the request's X-Pairtime-Time plus ingest.MaxSkewMS, plus one.
	untilMS int64
}

// get returns the refusal kept under key, while a copy can pass the
// clock check at nowMS, the time that check took.
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

// expire drops the refusals at the front of order whose time and
// keepGraceMS are up. An entry's time follows its request's own
// X-Pairtime-Time, so one behind an entry still kept waits for it, and get
// checks each entry's time anyway. It reslices order rather than copying
// it, so an expiry costs only what it drops.
func (rs *refusalStore) expire(nowMS int64) {
	n := 0
	for ; n < len(rs.order); n++ {
		if k, ok := rs.kept[rs.order[n]]; ok && k.untilMS+keepGraceMS > nowMS {
			break
		}
		delete(rs.kept, rs.order[n])
	}
	clear(rs.order[:n])
	rs.order = rs.order[n:]
}

// recorder is the ResponseWriter a create or a withdrawal answers through,
// so that refuse can keep the refusal a signed one gets.
type recorder struct {
	http.ResponseWriter
	// key is the request's device and signature, and untilMS the first
	// millisecond a copy of it no longer passes the clock check, both of
	// which admit sets once the signature verifies and the device's rate
	// admits the request. A "" key keeps nothing, as for a web session or
	// a request past the rate.
	key     string
	untilMS int64
}

// Unwrap lets http.ResponseController reach the connection, for
// readBody's read deadline.
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// keepRefusals answers a create or a withdrawal through a recorder, so
// that refuse keeps the status and code of the refusal a signed one gets,
// for its copies. refuse keeps it as it answers, while the request still
// holds its place in the device's rate, so no copy the rate admits after
// the answer can be checked anew.
func (s *Service) keepRefusals(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h(&recorder{ResponseWriter: w}, r)
	}
}

// keep keeps the refusal ref that w's request gets, when w is the recorder
// of a signed write the device's rate admitted.
func (s *Service) keep(w http.ResponseWriter, ref *refusal) {
	if rec, ok := w.(*recorder); ok && rec.key != "" {
		s.refused.put(rec.key, keptRefusal{status: ref.status, code: ref.code, untilMS: rec.untilMS}, s.o.Now().UnixMilli())
	}
}

// answerAgain gives a copy of a signed request the status and error code
// the first one got, with Retry-After on a 503 as every 503 carries.
func (s *Service) answerAgain(w http.ResponseWriter, k keptRefusal) {
	if k.status == http.StatusServiceUnavailable {
		w.Header().Set("Retry-After", "5")
	}
	s.stamp(w)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(k.status)
	json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{k.code})
}
