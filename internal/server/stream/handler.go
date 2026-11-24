// SPDX-License-Identifier: AGPL-3.0-or-later

package stream

import (
	"cmp"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/server/dashboard"
)

// The codes of the stream's refusals, which answer {"error": code}.
const (
	// CodeBadTopics: the topics parameter is missing, given twice, empty,
	// malformed or names more than MaxTopics topics.
	CodeBadTopics = "bad_topics"
	// CodeForbiddenTopic: a user: topic other than user:me.
	CodeForbiddenTopic = "forbidden_topic"
	// CodeNotSignedIn: user:me from a request that isn't signed in.
	CodeNotSignedIn = "not_signed_in"
	// CodeUnknownTopic: a topic this server doesn't serve, such as a
	// profile no public profile has, or SP 6.4's project: and global:.
	CodeUnknownTopic = "unknown_topic"
	// CodeTooManyStreams: a cap is reached (429).
	CodeTooManyStreams = "too_many_streams"
	// CodeUnavailable: the session or a profile couldn't be read, or the
	// server is stopping.
	CodeUnavailable = "stream_unavailable"
	// CodeMethod: anything but GET.
	CodeMethod = "method_not_allowed"
)

// TopicMe is the signed-in account's own topic.
const TopicMe = "user:me"

// profilePrefix starts a profile topic, as profile:@rowan.
const profilePrefix = "profile:@"

// profileName is the form of a profile's name in its topic. WP-SV9 decides
// which names exist; this only keeps the id's separators out of them.
var profileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// maxTopicsLen bounds the topics parameter.
const maxTopicsLen = 1024

// sessionChallenge is the 401's WWW-Authenticate challenge, the one the
// dashboard's API sends: the stream authenticates with the session cookie
// (RFC 9110 15.5.2).
const sessionChallenge = `Session realm="pairtime"`

// retryAfter is the seconds a 429 asks the client to wait.
const retryAfter = "30"

// refusal is a request the stream doesn't open.
type refusal struct {
	status int
	code   string
}

// want is a topic a stream asked for: the name it gave, and the hub's key
// and account for it.
type want struct {
	name, key, account string
}

// stream is one open stream.
type stream struct {
	sub *sub
	// names, epochs and pos are the stream's topics in the order it asked
	// for them: the names it gave, their epochs, and the number of the last
	// event it has of each.
	names  []string
	epochs []string
	pos    []uint64
	// resync names the topics whose gap the hub couldn't replay, and
	// replay is the gap of the others, oldest first.
	resync []string
	replay []delivery
	// account is the account of user:me, "" for an anonymous stream, and
	// client the address an anonymous stream counts toward.
	account, client string
}

// ServeHTTP serves GET /api/v1/stream?topics=<name>,<name>... A stream
// stays open until the client goes, the hub ends it or Close.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		refuse(w, refusal{http.StatusMethodNotAllowed, CodeMethod})
		return
	}
	names, ref := parseTopics(r.URL.Query()["topics"])
	if ref != nil {
		refuse(w, *ref)
		return
	}
	wants := make([]want, 0, len(names))
	account := ""
	for _, name := range names {
		var wt want
		if name == TopicMe {
			if account == "" {
				if account, ref = h.signedIn(r); ref != nil {
					refuse(w, *ref)
					return
				}
			}
			wt = want{name: name, key: userKey + account, account: account}
		} else {
			acct, ref := h.profile(r, strings.TrimPrefix(name, profilePrefix))
			if ref != nil {
				refuse(w, *ref)
				return
			}
			wt = want{name: name, key: profileKey + acct, account: acct}
		}
		if slices.ContainsFunc(wants, func(o want) bool { return o.key == wt.key }) {
			// Two names of one profile: one place each in an id can't
			// say where the stream stood.
			refuse(w, refusal{http.StatusBadRequest, CodeBadTopics})
			return
		}
		wants = append(wants, wt)
	}
	// A browser sends the last id it saw when it reconnects, and nothing
	// on a first connection. An id that isn't one of the hub's resyncs
	// every topic.
	var resume map[string]position
	id := r.Header.Get("Last-Event-ID")
	if id != "" {
		resume, _ = parseID(id)
	}
	st, ref := h.open(wants, account, clientOf(r), resume, id != "")
	if ref != nil {
		refuse(w, *ref)
		return
	}
	defer h.end(st)
	h.serve(w, r, st)
}

// serve writes st until its client goes, the hub ends it or Close.
func (h *Hub) serve(w http.ResponseWriter, r *http.Request, st *stream) {
	hd := w.Header()
	hd.Set("Content-Type", "text/event-stream")
	// SP 6.4 asks for no-cache, which proxies read as a stream not to
	// hold. no-store keeps any cache, a school's inspecting proxy among
	// them, from keeping an owner's events, as the dashboard's API says
	// for its view. no-transform keeps an edge from compressing the
	// stream, which would hold events back until a compressed block fills.
	hd.Set("Cache-Control", "no-cache, no-store, no-transform")
	// nginx, for a server someone runs behind one, passes the stream on
	// as it comes rather than buffer it.
	hd.Set("X-Accel-Buffering", "no")
	hd.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	send := func(b []byte) error {
		// A deadline the writer doesn't support, such as a test's, just
		// isn't set. It is cleared again once the bytes are out: Go's
		// HTTP/2 server resets a stream when its deadline passes, write or
		// no write, which would end a quiet stream between keepalives.
		rc.SetWriteDeadline(time.Now().Add(writeTimeout))
		if _, err := w.Write(b); err != nil {
			return err
		}
		if err := rc.Flush(); err != nil {
			return err
		}
		rc.SetWriteDeadline(time.Time{})
		return nil
	}

	if send(h.first(st)) != nil {
		return
	}
	tick := time.NewTicker(h.o.Keepalive)
	defer tick.Stop()
	// Only a stream of user:me has a session to ask about again.
	var recheck <-chan time.Time
	if st.account != "" {
		t := time.NewTicker(h.o.Recheck)
		defer t.Stop()
		recheck = t.C
	}
	var buf []byte
	for {
		select {
		case d := <-st.sub.ch:
			buf = st.appendEvent(buf[:0], d)
			// Whatever else is waiting goes out with it, in one flush.
			for more := true; more; {
				select {
				case d := <-st.sub.ch:
					buf = st.appendEvent(buf, d)
				default:
					more = false
				}
			}
			if len(buf) > 0 && send(buf) != nil {
				return
			}
			if cap(buf) > maxKeptBuf {
				// A burst of big events shouldn't stay with the stream.
				buf = nil
			}
		case <-recheck:
			if id, ref := h.signedIn(r); ref != nil || id != st.account {
				return
			}
		case <-tick.C:
			if send(keepalive) != nil {
				return
			}
		case <-st.sub.kicked:
			return
		case <-r.Context().Done():
			return
		case <-h.done:
			return
		}
	}
}

// first returns what a stream starts with: the reconnect delay, its place
// as an id, resync when a topic needs it, then the gap replayed.
func (h *Hub) first(st *stream) []byte {
	b := append([]byte("retry: "), strconv.FormatInt(h.o.Retry.Milliseconds(), 10)...)
	b = append(b, "\nid: "...)
	b = st.appendID(b)
	b = append(b, '\n')
	if len(st.resync) > 0 {
		data, _ := json.Marshal(Resync{Topics: st.resync})
		b = append(b, "event: resync\ndata: "...)
		b = append(b, data...)
		b = append(b, '\n')
	}
	b = append(b, '\n')
	for _, d := range st.replay {
		b = st.appendEvent(b, d)
	}
	return b
}

// maxKeptBuf is the most of its write buffer a stream keeps between
// events.
const maxKeptBuf = 64 << 10

// keepalive is what a stream sends every Keepalive: SP 6.4's comment,
// which a proxy sees as bytes, and an event named keepalive with no data.
// EventSource hands a page no comment, so the event is how the page knows
// its stream is alive, and falls back to polling after 45 s of nothing.
var keepalive = []byte(": keepalive\nevent: keepalive\ndata:\n\n")

// Resync is a resync event's data: the topics whose events the stream
// couldn't replay, by the names the stream gave them. The page refetches
// its snapshot.
type Resync struct {
	Topics []string `json:"topics"`
}

// appendID appends st's id: its place in each of its topics.
func (st *stream) appendID(b []byte) []byte {
	for i, name := range st.names {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, name...)
		b = append(b, ':')
		b = append(b, st.epochs[i]...)
		b = append(b, '-')
		b = strconv.AppendUint(b, st.pos[i], 10)
	}
	return b
}

// appendEvent appends d with st's id after it, unless st has it already.
func (st *stream) appendEvent(b []byte, d delivery) []byte {
	if d.ev.seq <= st.pos[d.i] {
		return b
	}
	st.pos[d.i] = d.ev.seq
	b = append(b, "id: "...)
	b = st.appendID(b)
	b = append(b, '\n')
	return append(b, d.ev.frame...)
}

// open checks the caps and puts a new stream on its topics, with what it
// needs replayed or resynced when it resumes. Under the one lock, so no
// event falls between the replay and the stream's first live one.
func (h *Hub) open(wants []want, account, client string, resume map[string]position, resumed bool) (*stream, *refusal) {
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case h.closed:
		return nil, &refusal{http.StatusServiceUnavailable, CodeUnavailable}
	case h.total >= h.o.MaxStreams,
		account != "" && h.perAccount[account] >= h.o.MaxPerAccount,
		account == "" && (h.anonymous >= h.o.MaxAnonymous || h.perClient[client] >= h.o.MaxPerClient):
		return nil, &refusal{http.StatusTooManyRequests, CodeTooManyStreams}
	}
	st := &stream{
		sub:     &sub{ch: make(chan delivery, sendBuffer), kicked: make(chan struct{})},
		names:   make([]string, len(wants)),
		epochs:  make([]string, len(wants)),
		pos:     make([]uint64, len(wants)),
		account: account,
		client:  client,
	}
	day := dayNumber(lastPublication(now))
	for i, wt := range wants {
		h.liveTopicLocked(wt.key, now)
		t := h.topicLocked(wt.key, wt.account)
		trimLocked(t, now, h.o.Window)
		st.names[i] = wt.name
		if isProfile(t) {
			// A profile's place is the day of the last publication, the
			// same for every viewer whatever the topic saw, so an id tells
			// nobody whether others watch. A reconnect from another day
			// refetches the page, and nothing is replayed (D-public-daily).
			st.epochs[i], st.pos[i] = h.profileEpoch, day
			if p, ok := resume[wt.name]; resumed && (!ok || p.epoch != h.profileEpoch || p.seq != day) {
				st.resync = append(st.resync, wt.name)
			}
		} else {
			st.epochs[i], st.pos[i] = t.epoch, t.seq
			if resumed {
				p, ok := resume[wt.name]
				if !ok || p.epoch != t.epoch || p.seq > t.seq || p.seq < t.lost {
					// Another epoch, a number the topic never reached, or
					// a gap the ring no longer holds all of.
					st.resync = append(st.resync, wt.name)
				} else {
					st.pos[i] = p.seq
					for _, ev := range t.ring {
						if ev.seq > p.seq {
							st.replay = append(st.replay, delivery{i: i, ev: ev})
						}
					}
				}
			}
		}
		t.subs[st.sub] = i
		t.idle = time.Time{}
		st.sub.topics = append(st.sub.topics, t)
	}
	// The topics' gaps interleave as they were published.
	slices.SortFunc(st.replay, func(a, b delivery) int { return cmp.Compare(a.ev.ord, b.ev.ord) })
	h.total++
	if account != "" {
		h.perAccount[account]++
	} else {
		h.anonymous++
		h.perClient[client]++
	}
	return st, nil
}

// end takes a stream off its topics and the counts.
func (h *Hub) end(st *stream) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.leaveLocked(st.sub, time.Now())
	h.total--
	if st.account != "" {
		if h.perAccount[st.account]--; h.perAccount[st.account] <= 0 {
			delete(h.perAccount, st.account)
		}
		return
	}
	h.anonymous--
	if h.perClient[st.client]--; h.perClient[st.client] <= 0 {
		delete(h.perClient, st.client)
	}
}

// signedIn returns the account r is signed in to.
func (h *Hub) signedIn(r *http.Request) (string, *refusal) {
	if h.o.Account == nil {
		return "", &refusal{http.StatusUnauthorized, CodeNotSignedIn}
	}
	id, err := h.o.Account(r)
	switch {
	case errors.Is(err, dashboard.ErrNotSignedIn):
		return "", &refusal{http.StatusUnauthorized, CodeNotSignedIn}
	case err != nil:
		h.o.Logger.Error("reading a session for the stream", "err", err)
		return "", &refusal{http.StatusInternalServerError, CodeUnavailable}
	case id == "":
		return "", &refusal{http.StatusUnauthorized, CodeNotSignedIn}
	}
	return id, nil
}

// profile returns the account of the public profile name names.
func (h *Hub) profile(r *http.Request, name string) (string, *refusal) {
	if h.o.Profiles == nil {
		return "", &refusal{http.StatusNotFound, CodeUnknownTopic}
	}
	account, ok, err := h.o.Profiles.Resolve(r.Context(), name)
	switch {
	case err != nil:
		h.o.Logger.Error("finding a profile for the stream", "err", err)
		return "", &refusal{http.StatusServiceUnavailable, CodeUnavailable}
	case !ok || account == "":
		return "", &refusal{http.StatusNotFound, CodeUnknownTopic}
	}
	return account, nil
}

// parseTopics reads the topics parameter: names joined by commas, each
// named once.
func parseTopics(values []string) ([]string, *refusal) {
	bad := &refusal{http.StatusBadRequest, CodeBadTopics}
	if len(values) != 1 || values[0] == "" || len(values[0]) > maxTopicsLen {
		return nil, bad
	}
	var names []string
	for _, name := range strings.Split(values[0], ",") {
		switch {
		case name == TopicMe:
		case strings.HasPrefix(name, userKey):
			// An account reads its own events only.
			return nil, &refusal{http.StatusForbidden, CodeForbiddenTopic}
		case strings.HasPrefix(name, profilePrefix) && profileName.MatchString(name[len(profilePrefix):]):
		case name == "" || strings.HasPrefix(name, profileKey):
			return nil, bad
		default:
			return nil, &refusal{http.StatusNotFound, CodeUnknownTopic}
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	if len(names) > MaxTopics {
		return nil, bad
	}
	return names, nil
}

// clientOf returns the address an anonymous stream counts toward:
// CF-Connecting-IP, which cloudflared sets and ingest reads too, when there
// is one valid value, else the connection's. An IPv6 address counts by its
// /48: a home network often gets a /56 and a tunnel broker hands out a /48,
// so a /64 a stream would let one person count as hundreds of clients.
func clientOf(r *http.Request) string {
	var a netip.Addr
	if vs := r.Header.Values("CF-Connecting-IP"); len(vs) == 1 {
		a, _ = netip.ParseAddr(strings.TrimSpace(vs[0]))
	}
	if !a.IsValid() {
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			a, _ = netip.ParseAddr(host)
		}
	}
	if !a.IsValid() {
		return "unknown"
	}
	a = a.WithZone("").Unmap()
	if a.Is6() {
		if p, err := a.Prefix(48); err == nil {
			return p.String()
		}
	}
	return a.String()
}

// refuse answers a request the stream doesn't open.
func refuse(w http.ResponseWriter, ref refusal) {
	hd := w.Header()
	hd.Set("Content-Type", "application/json; charset=utf-8")
	hd.Set("Cache-Control", "no-store")
	hd.Set("X-Content-Type-Options", "nosniff")
	switch ref.status {
	case http.StatusUnauthorized:
		hd.Set("WWW-Authenticate", sessionChallenge)
	case http.StatusTooManyRequests:
		hd.Set("Retry-After", retryAfter)
	}
	w.WriteHeader(ref.status)
	json.NewEncoder(w).Encode(map[string]string{"error": ref.code})
}
