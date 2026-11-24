// SPDX-License-Identifier: AGPL-3.0-or-later

// Package stream is GET /api/v1/stream, the server-sent events that keep
// an open dashboard live (WP-SV8; SP 6.4; DESIGN 2's R5 row and 3.10's
// "Rollups and live dashboard" row).
//
// A page opens one EventSource for every topic it shows, as
// /api/v1/stream?topics=user:me. The stream starts with "retry: 5000",
// carries the topics' events, and sends a ": keepalive" comment every 15
// s, well inside the 125 s Cloudflare waits between bytes (SP 6.4). The
// comment comes with an event named keepalive and no data, because
// EventSource shows a page no comment, and the page needs to hear the
// stream to know when to fall back to polling. Each event is written and
// flushed through http.ResponseController the moment it is published.
//
// # Topics
//
// user:me is the signed-in account's own topic. The server resolves it from
// the session cookie, so a stream only ever carries its own account's
// events; a stream that asks for it signed out gets 401, and one that
// names any other user: topic gets 403. Its credited events carry the
// numbers of the owner's dashboard from dashboard.View, the View the page
// draws, so the two never disagree, with projects named by their
// account-level ids and never by a name (D-owner-names stays the page's).
//
// profile:@<name> topics are anonymous. They carry only what DESIGN 8.3
// gives the public profile: daily totals of final minutes at level A or B,
// with no project and no time of day (PublicTotals; DESIGN 6.2 D1, D11).
// Like the public page they change once a day, at 12:00 UTC, show only
// dates that have ended in every time zone, and take a removal at once
// (D-public-daily, profile.go). Their ids depend on the clock alone, so a
// viewer learns nothing of other viewers from them. Profiles is how a
// name finds its account; until WP-SV9 gives pairtimed one, every
// profile: topic is unknown.
//
// SP 6.4's project: and global: topics, and its commit events, which wait
// for WP-AG5's commit records, aren't here yet.
//
// # Event ids, the ring buffers and resync
//
// An event's id is the stream's place in every topic it carries,
// "<topic>:<epoch>-<n>" for each, joined by commas, as
// "user:me:k3j9x2abcde7q-18". A browser sends the last id it saw as
// Last-Event-ID when it reconnects, so one id has to say where the stream
// stood in all its topics. The numbers are per topic, and never one count
// the whole server shares: from a shared count, an anonymous viewer of a
// profile, or any signed-in owner, could see how many events other
// accounts' topics had between two of their own, and on a small server
// that times when other people work. Per topic, a stream learns nothing
// from an id but its own topics' events.
//
// A user topic keeps its events of the last Window (5 minutes), at most
// ringMax of them and ringBytes of their bytes, and counts them from 1
// under an epoch, a random tag the topic gets when it is made, so a
// restart, or a topic no stream was on for the window and that was made
// again, starts a new count. A reconnect whose id is from the topic's
// epoch and whose gap the ring still holds gets the gap replayed, in
// order. Any other id gets an event named resync, whose data names the
// topics to refetch, and the page refetches its snapshot (GET
// /api/v1/users/me/dashboard). A stream's first block carries its place as
// an id with no data, which sets the browser's last id without dispatching
// an event, so a reconnect before the first event still resumes where the
// stream was.
//
// A profile topic's id is different on purpose: the hub's one
// profileEpoch and the day of the last 12:00 UTC publication
// (dayNumber), the same for every viewer and across the topic being made
// again, so nobody can tell from it whether others watch. Nothing is
// replayed: a reconnect from another day gets resync.
//
// # Publishing
//
// Changed tells the hub that a transaction which changed an account's
// rollups has committed. pairtimed calls it after the writer's op
// returned, never inside it, and not for an op that failed. Changed only
// marks the account; the hub reads the View off the caller's goroutine, at
// most once every Gap (1 s) per account, so a burst of uploads makes one
// event and the first change of a quiet minute goes out at once. Gap stays
// under half of R5's 2 s, which SP 6.3's 2 s would use up alone. Nothing is
// read for an account with no stream open, or none in the last Window. A
// read that fails is tried again, Gap later and then twice as long each
// time, and after maxReadRetries the topic's streams get resync, so each
// page refetches its numbers itself.
//
// Publishing never blocks: an event goes to each stream's buffer without
// waiting, and a stream whose buffer is full has fallen behind, so the hub
// ends it. Its browser reconnects with its last id and gets the gap from
// the ring, or resync.
//
// # Caps
//
// A stream that carries user:me counts toward its account, past
// MaxPerAccount the request gets 429, so one account can't hold thousands
// of streams open. A stream without it is anonymous and counts toward its
// client address, up to MaxPerClient, and toward MaxAnonymous, half of
// MaxStreams, so anonymous streams never take the places signed-in owners
// need, at a school behind one address or anywhere. Every stream counts
// toward MaxStreams. The client is the address Cloudflare names in
// CF-Connecting-IP, as ingest reads it, else the connection's, with IPv6
// counted by its /48, the most a home network or a tunnel broker usually
// hands out. pairtimed is reached only through cloudflared, which sets the
// header itself.
//
// A stream of user:me asks again every Recheck whether its session still
// names its account, and ends when it doesn't, so a sign-out, a revoked
// session or one past its lifetime stops its events within a minute. The
// browser's reconnect then gets 401, which stops EventSource.
//
// The stream holds events in memory for Window and stores nothing (DESIGN
// 4.4 has no row for it, and needs none).
package stream

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/some-du6e/pairtime/internal/server/dashboard"
)

// Path is where the stream is served.
const Path = "/api/v1/stream"

// Defaults for Options.
const (
	// DefaultWindow is how long a topic keeps its events for a reconnect
	// to replay (SP 6.4).
	DefaultWindow = 5 * time.Minute
	// DefaultKeepalive is how often a stream sends ": keepalive" (SP 6.4).
	DefaultKeepalive = 15 * time.Second
	// DefaultRetry is the reconnect delay the stream gives the browser.
	DefaultRetry = 5 * time.Second
	// DefaultGap is the shortest time between two reads of one account's
	// View for its topic.
	DefaultGap = time.Second
	// DefaultMaxStreams is the most streams open at once, twice the 5,000
	// DESIGN 10.6 plans for at an event peak.
	DefaultMaxStreams = 10000
	// DefaultMaxPerAccount is the most streams one account holds open,
	// one for each tab of a few windows.
	DefaultMaxPerAccount = 10
	// DefaultMaxPerClient is the most anonymous streams one client address
	// holds open. A school network puts a whole class behind one address,
	// so it is far above one person's tabs.
	DefaultMaxPerClient = 100
	// DefaultMaxAnonymous is the most anonymous streams open at once: half
	// of DefaultMaxStreams, so signed-in owners always have the other half.
	DefaultMaxAnonymous = DefaultMaxStreams / 2
	// DefaultRecheck is how often a stream of user:me asks again whether
	// its session is still signed in to its account.
	DefaultRecheck = time.Minute
)

// MaxTopics is the most topics one stream carries.
const MaxTopics = 8

// ringMax and ringBytes are the most events, and the most bytes of them,
// a topic keeps, whatever their age, which bounds a busy topic's memory: at
// 10,000 streams on as many topics, 160 MB at most. A reconnect that needs
// one it dropped gets resync. A user topic publishes at most one event a
// Gap, and its events carry whole totals, so the page needs none of the
// older ones; a profile topic publishes one a day and replays none.
const (
	ringMax   = 16
	ringBytes = 16 << 10
)

// sendBuffer is how many events a stream can be behind before the hub
// ends it.
const sendBuffer = 16

// writeTimeout bounds each write to a stream, so a client that stops
// reading doesn't hold its stream for ever: the server has no write
// timeout of its own, because streams stay open (cmd/pairtimed's
// newHTTPServer).
const writeTimeout = 10 * time.Second

// Options configure a Hub. Zero values take the defaults.
type Options struct {
	// Source is where a user topic's numbers come from: dashboard.Rollups
	// in pairtimed, the Source the dashboard page and its API read. Nil
	// publishes no credited events.
	Source dashboard.Source
	// Account returns the users id of the account a request is signed in
	// to: dashboard.ErrNotSignedIn when it isn't, and another error when
	// its session couldn't be read. Nil refuses user:me as signed out.
	Account func(r *http.Request) (string, error)
	// Profiles finds the accounts of public profiles and their totals.
	// Nil makes every profile: topic unknown.
	Profiles Profiles

	Window, Keepalive, Retry, Gap, Recheck time.Duration
	// MaxStreams caps every stream, MaxPerAccount those of one account,
	// and MaxAnonymous and MaxPerClient the streams without user:me, in
	// all and from one client address.
	MaxStreams, MaxPerAccount, MaxAnonymous, MaxPerClient int

	// Logger hears of reads that fail. Nil means slog.Default().
	Logger *slog.Logger
}

// Hub is the stream: its topics, their ring buffers and the streams open
// on them.
type Hub struct {
	o Options

	// mu guards everything below, the topics' fields and the streams'
	// subscriptions.
	mu     sync.Mutex
	topics map[string]*topic
	closed bool
	// total, perAccount, anonymous and perClient count the open streams:
	// all of them, those of each account, and those without an account,
	// in all and from each client address.
	total      int
	perAccount map[string]int
	anonymous  int
	perClient  map[string]int
	// ord numbers every event the hub publishes, in order, so a replay
	// of several topics' gaps keeps that order. It never leaves the hub.
	ord uint64
	// profileEpoch is every profile topic's epoch, one for the process,
	// so a profile's id says nothing of whether others watch it.
	profileEpoch string

	// done is closed by Close, which cancels ctx, the context of the
	// hub's reads; wg waits for the hub's own goroutines.
	done   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	// reads bounds the View reads running at once.
	reads chan struct{}
}

// maxReads is how many View or profile reads the hub runs at once.
const maxReads = 4

// New returns a Hub and starts its goroutines: the sweep that drops old
// events and idle topics, and the daily profile pass. Close stops them.
func New(o Options) *Hub {
	if o.Window <= 0 {
		o.Window = DefaultWindow
	}
	if o.Keepalive <= 0 {
		o.Keepalive = DefaultKeepalive
	}
	if o.Retry <= 0 {
		o.Retry = DefaultRetry
	}
	if o.Gap <= 0 {
		o.Gap = DefaultGap
	}
	if o.MaxStreams <= 0 {
		o.MaxStreams = DefaultMaxStreams
	}
	if o.MaxPerAccount <= 0 {
		o.MaxPerAccount = DefaultMaxPerAccount
	}
	if o.MaxPerClient <= 0 {
		o.MaxPerClient = DefaultMaxPerClient
	}
	if o.MaxAnonymous <= 0 {
		o.MaxAnonymous = min(DefaultMaxAnonymous, max(o.MaxStreams/2, 1))
	}
	if o.Recheck <= 0 {
		o.Recheck = DefaultRecheck
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	h := &Hub{
		o:          o,
		topics:     make(map[string]*topic),
		perAccount: make(map[string]int),
		perClient:  make(map[string]int),
		done:       make(chan struct{}),
		reads:      make(chan struct{}, maxReads),
	}
	h.ctx, h.cancel = context.WithCancel(context.Background())
	h.profileEpoch = newEpoch()
	h.wg.Add(1)
	go h.sweep()
	if o.Profiles != nil {
		h.wg.Add(1)
		go h.daily()
	}
	return h
}

// Close ends every stream, stops the hub's goroutines and waits for them.
// Streams opened after it get 503. Calling it again does nothing more.
func (h *Hub) Close() {
	h.mu.Lock()
	already := h.closed
	h.closed = true
	h.mu.Unlock()
	if !already {
		close(h.done)
		h.cancel()
	}
	h.wg.Wait()
}

// Streams is how many streams are open, for /metrics (DESIGN 10.4).
func (h *Hub) Streams() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.total
}

// The two kinds of topic, as the hub keys them: by account, whatever name
// the stream gave.
const (
	userKey    = "user:"
	profileKey = "profile:"
)

// topic is one topic's events and subscribers.
type topic struct {
	// key is userKey or profileKey and the account's id.
	key, account string
	// epoch tags a user topic's event numbers (newEpoch). A profile
	// topic has none: its ids take the hub's profileEpoch.
	epoch string
	// seq is the number of the last event published, 0 before the first.
	seq uint64
	// ring holds the events of the last Window, oldest first, at most
	// ringMax of them and ringBytes of their frames, which bytes counts.
	ring  []*event
	bytes int
	// lost is the number of the newest event the ring dropped, 0 for none.
	lost uint64
	// subs are the streams on the topic, each with this topic's index in
	// its stream's list.
	subs map[*sub]int
	// idle is when the last stream left, zero while one is open.
	idle time.Time
	// owner is a user topic's publishing state, nil on a profile topic.
	owner *ownerState
}

// event is one published event: its number in its topic, its place
// among all the hub's events, and when it was published.
type event struct {
	seq, ord uint64
	at       time.Time
	// frame is the event's lines after its id, "event: <name>\ndata:
	// <json>\n\n", the same for every stream.
	frame []byte
}

// sub is one stream's subscription to its topics.
type sub struct {
	ch chan delivery
	// kicked is closed when the hub ends the stream for falling behind.
	kicked chan struct{}
	kick   sync.Once
	topics []*topic
}

// delivery is an event for a stream, with the index of its topic in the
// stream's list.
type delivery struct {
	i  int
	ev *event
}

// epochEncoding writes epochs: lowercase base32 without padding, which
// has no "-" or ":" to confuse an id.
var epochEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// newEpoch returns a new topic epoch: 8 random bytes, 13 characters.
func newEpoch() string {
	var b [8]byte
	rand.Read(b[:])
	return epochEncoding.EncodeToString(b[:])
}

// topicLocked returns the topic of key, making it when it doesn't exist.
func (h *Hub) topicLocked(key, account string) *topic {
	t := h.topics[key]
	if t != nil {
		return t
	}
	// A topic is made for a stream that subscribes at once, which clears
	// idle; until then it counts as just left.
	t = &topic{key: key, account: account, subs: make(map[*sub]int), idle: time.Now()}
	if strings.HasPrefix(key, userKey) {
		t.epoch = newEpoch()
		t.owner = &ownerState{}
	}
	h.topics[key] = t
	return t
}

// frame returns an event's lines after its id.
func frame(name string, data []byte) []byte {
	b := make([]byte, 0, len("event: \ndata: \n\n")+len(name)+len(data))
	b = append(b, "event: "...)
	b = append(b, name...)
	b = append(b, "\ndata: "...)
	b = append(b, data...)
	return append(b, "\n\n"...)
}

// isProfile reports whether t is a profile topic.
func isProfile(t *topic) bool { return strings.HasPrefix(t.key, profileKey) }

// publishLocked adds t's next event to its ring and hands it to t's
// streams. A stream whose buffer is full is ended.
func (h *Hub) publishLocked(t *topic, name string, data []byte, now time.Time) {
	h.publishAtLocked(t, t.seq+1, name, data, now)
}

// publishAtLocked publishes an event numbered seq, above t's last: a
// profile topic numbers its events by their publication's day.
func (h *Hub) publishAtLocked(t *topic, seq uint64, name string, data []byte, now time.Time) {
	t.seq = seq
	h.ord++
	ev := &event{seq: t.seq, ord: h.ord, at: now, frame: frame(name, data)}
	t.ring = append(t.ring, ev)
	t.bytes += len(ev.frame)
	trimLocked(t, now, h.o.Window)
	for s, i := range t.subs {
		select {
		case s.ch <- delivery{i: i, ev: ev}:
		default:
			h.kickLocked(s)
		}
	}
}

// trimLocked drops the events older than window from t's ring, and the
// oldest while it holds more than ringMax events or ringBytes of them. The
// newest event stays whatever its size, so a stream on the topic has it.
func trimLocked(t *topic, now time.Time, window time.Duration) {
	n := 0
	for n < len(t.ring) && (now.Sub(t.ring[n].at) >= window || len(t.ring)-n > ringMax || t.bytes > ringBytes && n < len(t.ring)-1) {
		t.bytes -= len(t.ring[n].frame)
		n++
	}
	if n == 0 {
		return
	}
	t.lost = t.ring[n-1].seq
	// slices.Delete clears the tail, so no dropped event stays reachable.
	t.ring = slices.Delete(t.ring, 0, n)
}

// kickLocked ends s: it leaves its topics, and its stream returns.
func (h *Hub) kickLocked(s *sub) {
	s.kick.Do(func() { close(s.kicked) })
	h.leaveLocked(s, time.Now())
}

// leaveLocked takes s off its topics.
func (h *Hub) leaveLocked(s *sub, now time.Time) {
	for _, t := range s.topics {
		if _, ok := t.subs[s]; !ok {
			continue
		}
		delete(t.subs, s)
		if len(t.subs) == 0 {
			t.idle = now
		}
	}
}

// sweepEvery is how often the sweep runs: a fifth of the window, and at
// least a second.
func (h *Hub) sweepEvery() time.Duration { return max(h.o.Window/5, time.Second) }

// sweep drops old events and idle topics until Close.
func (h *Hub) sweep() {
	defer h.wg.Done()
	tick := time.NewTicker(h.sweepEvery())
	defer tick.Stop()
	for {
		select {
		case <-h.done:
			return
		case now := <-tick.C:
			h.sweepOnce(now)
		}
	}
}

// sweepOnce drops the events older than the window from every topic, and
// every topic no stream has been on for the window, whatever its ring
// still holds: a reconnect to it gets resync from the topic made in its
// place. A topic whose account keeps changing publishes into its ring
// while nobody is there, so the ring alone would keep it, and its reads,
// for as long as the changes come.
func (h *Hub) sweepOnce(now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for key := range h.topics {
		if t := h.liveTopicLocked(key, now); t != nil {
			trimLocked(t, now, h.o.Window)
		}
	}
}

// abandonedLocked reports whether no stream has been on t for the window,
// after which nothing a reconnect could replay is worth reading for it.
func (h *Hub) abandonedLocked(t *topic, now time.Time) bool {
	return len(t.subs) == 0 && now.Sub(t.idle) >= h.o.Window
}

// liveTopicLocked returns the topic of key, or nil when there is none or it
// was abandoned, which it then drops at once rather than wait for the
// sweep: a change it ignored mustn't leave a reconnect told its old place
// is current, and the topic made in its place has a new epoch. A read in
// flight for it publishes nothing, since its topic is no longer the one in
// the hub.
func (h *Hub) liveTopicLocked(key string, now time.Time) *topic {
	t := h.topics[key]
	if t != nil && h.abandonedLocked(t, now) {
		delete(h.topics, key)
		return nil
	}
	return t
}

// position is a stream's place in one topic: the epoch and number of the
// last event it has.
type position struct {
	epoch string
	seq   uint64
}

// maxIDLen bounds the Last-Event-ID the hub reads: MaxTopics places, each
// well under 100 bytes.
const maxIDLen = 1024

// parseID reads a stream's id: "<topic>:<epoch>-<n>" for each topic,
// joined by commas. A topic name has colons of its own, so each place
// splits at its last one. It reports false for an id that isn't one.
func parseID(s string) (map[string]position, bool) {
	if s == "" || len(s) > maxIDLen {
		return nil, false
	}
	out := make(map[string]position)
	for _, part := range strings.Split(s, ",") {
		i := strings.LastIndexByte(part, ':')
		if i <= 0 {
			return nil, false
		}
		epoch, n, ok := strings.Cut(part[i+1:], "-")
		if !ok || len(epoch) != 13 || strings.Trim(epoch, "abcdefghijklmnopqrstuvwxyz234567") != "" {
			return nil, false
		}
		seq, err := strconv.ParseUint(n, 10, 64)
		if err != nil || strconv.FormatUint(seq, 10) != n {
			return nil, false
		}
		out[part[:i]] = position{epoch: epoch, seq: seq}
	}
	return out, true
}
