// SPDX-License-Identifier: AGPL-3.0-or-later

package stream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/some-du6e/pairtime/internal/server/dashboard"
)

// testAccount is the fake session the tests sign in with: the account a
// request's X-Test-Account header names, and signed out without one.
func testAccount(r *http.Request) (string, error) {
	if id := r.Header.Get("X-Test-Account"); id != "" {
		return id, nil
	}
	return "", dashboard.ErrNotSignedIn
}

// as returns a header that signs a request in to account.
func as(account string) http.Header {
	return http.Header{"X-Test-Account": {account}}
}

// pipeWriter is a ResponseWriter for a stream inside a synctest bubble:
// each flush hands what was written since to the test, on a channel made
// in the bubble, so a handler waiting on it is durably blocked. While hold
// is set, a flush waits until the test closes release.
type pipeWriter struct {
	header  http.Header
	code    int
	buf     []byte
	out     chan []byte
	hold    atomic.Bool
	release chan struct{}
	// deadlines gets the write deadlines the handler sets, as a net/http
	// ResponseWriter would set them on its connection, as many as it
	// holds; a long test that never reads them drops the rest.
	deadlines chan time.Time
}

func newPipeWriter() *pipeWriter {
	return &pipeWriter{header: http.Header{}, out: make(chan []byte, 4096), release: make(chan struct{}), deadlines: make(chan time.Time, 4096)}
}

func (p *pipeWriter) SetWriteDeadline(t time.Time) error {
	select {
	case p.deadlines <- t:
	default:
	}
	return nil
}

func (p *pipeWriter) Header() http.Header { return p.header }

func (p *pipeWriter) WriteHeader(code int) {
	if p.code == 0 {
		p.code = code
	}
}

func (p *pipeWriter) Write(b []byte) (int, error) {
	p.WriteHeader(http.StatusOK)
	p.buf = append(p.buf, b...)
	return len(b), nil
}

func (p *pipeWriter) Flush() {
	if p.hold.Load() {
		<-p.release
	}
	p.out <- p.buf
	p.buf = nil
}

// client is a stream a test opened on a hub inside a synctest bubble.
type client struct {
	w      *pipeWriter
	cancel context.CancelFunc
	done   chan struct{}
	// code and header are the response's, once the first flush came.
	code   int
	header http.Header
}

// open starts a stream for target with header on h, inside the current
// bubble, and waits until it has written all it can.
func open(t *testing.T, h *Hub, target string, header http.Header) *client {
	t.Helper()
	return openWith(t, h, target, header, newPipeWriter())
}

func openWith(t *testing.T, h *Hub, target string, header http.Header, w *pipeWriter) *client {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, target, nil)
	for k, vs := range header {
		r.Header[k] = vs
	}
	ctx, cancel := context.WithCancel(context.Background())
	r = r.WithContext(ctx)
	c := &client{w: w, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(c.done)
		h.ServeHTTP(w, r)
	}()
	synctest.Wait()
	return c
}

// read returns what the stream flushed since the last read.
func (c *client) read() string {
	var b strings.Builder
	for {
		select {
		case p := <-c.w.out:
			if c.header == nil {
				c.code, c.header = c.w.code, c.w.header.Clone()
			}
			b.Write(p)
		default:
			return b.String()
		}
	}
}

// close ends the stream from the client's side and waits for its handler.
func (c *client) close() {
	c.cancel()
	<-c.done
}

// ended reports whether the stream's handler has returned.
func (c *client) ended() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// sse is one block of an event stream. hasData says it has a data field,
// empty or not: EventSource dispatches a block that has one, and none that
// doesn't.
type sse struct {
	id, event, data, retry, comment string
	hasData                         bool
}

// parseSSE splits a stream's text into its blocks, as EventSource reads
// them: a field and a value after one optional space, a line starting with
// a colon a comment. Only blocks a blank line ends count, since
// EventSource dispatches nothing before it, so a half block after the last
// is left out.
func parseSSE(s string) []sse {
	var out []sse
	blocks := strings.Split(s, "\n\n")
	for _, block := range blocks[:len(blocks)-1] {
		if block == "" {
			continue
		}
		var e sse
		for _, line := range strings.Split(block, "\n") {
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "":
				e.comment = value
			case "id":
				e.id = value
			case "event":
				e.event = value
			case "data":
				e.data, e.hasData = value, true
			case "retry":
				e.retry = value
			}
		}
		out = append(out, e)
	}
	return out
}

// events returns the blocks of s that carry data: every event the hub
// sends but the keepalive, whose data is empty.
func events(s string) []sse {
	var out []sse
	for _, e := range parseSSE(s) {
		if e.data != "" {
			out = append(out, e)
		}
	}
	return out
}

// lastID returns the id of the last block of s that has one, as a browser
// keeps it.
func lastID(s string) string {
	id := ""
	for _, e := range parseSSE(s) {
		if e.id != "" {
			id = e.id
		}
	}
	return id
}

// publishTo publishes an event on the topic of key, making it if need be,
// as the hub's publishers do.
func publishTo(h *Hub, key, account, name, data string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	t := h.topicLocked(key, account)
	h.publishLocked(t, name, []byte(data), time.Now())
}

// viewSource is a dashboard.Source of Views built by dashboard.Build, as
// dashboard.Rollups builds them: today's date by the clock, in UTC, with
// one project "p1" named "secret-name" by its owner, holding the owner's
// seconds as level A prompting and the agent's own seconds. It counts its
// reads, and fails them while fail is set.
type viewSource struct {
	you, agent atomic.Int64
	calls      atomic.Int32
	fail       atomic.Bool
}

func (s *viewSource) View(_ context.Context, account string, now time.Time) (dashboard.View, error) {
	s.calls.Add(1)
	if s.fail.Load() {
		return dashboard.View{}, context.DeadlineExceeded
	}
	today := now.UTC().Format(dashboard.DateLayout)
	rows := []dashboard.Row{
		{Day: today, Project: "p1", Kind: "prompting", Level: "A", Seconds: float64(s.you.Load())},
		{Day: today, Project: "p1", Agent: true, Seconds: float64(s.agent.Load())},
	}
	return dashboard.Build(dashboard.Meta{
		AsOf: now, TZ: "UTC", Today: today, Agent: dashboard.AgentName,
		Names:  map[string]string{"p1": "Silver Fox"},
		Labels: map[string]string{"p1": "secret-name"},
		Keys:   map[string][]string{"p1": {"k_device_key"}},
	}, rows)
}

// fakeProfiles serves one public profile of account "a1", named "rowan"
// and "ro". It keeps each date's credited seconds as the test sets them,
// with when, and its Totals for a publication gives every date ended in
// every zone by then with the seconds it had at the publication, as WP-SV9's
// page will show them. While private is set, the profile isn't public, and
// while fail is set, every read of its totals fails. It counts the reads.
type fakeProfiles struct {
	mu      sync.Mutex
	history map[string][]credit
	private atomic.Bool
	fail    atomic.Bool
	reads   atomic.Int32
	// resolved, when not nil, runs in Resolve once it found the profile
	// public, before it answers, and resolves counts Resolve's calls.
	resolved func()
	resolves atomic.Int32
}

// credit is a date's credited seconds from a time on.
type credit struct {
	at time.Time
	s  float64
}

// set gives date s credited seconds from now on.
func (f *fakeProfiles) set(date string, s float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.history == nil {
		f.history = make(map[string][]credit)
	}
	f.history[date] = append(f.history[date], credit{at: time.Now(), s: s})
}

func (f *fakeProfiles) Resolve(_ context.Context, name string) (string, bool, error) {
	f.resolves.Add(1)
	if (name != "rowan" && name != "ro") || f.private.Load() {
		return "", false, nil
	}
	if f.resolved != nil {
		f.resolved()
	}
	return "a1", true, nil
}

func (f *fakeProfiles) Totals(_ context.Context, account string, p time.Time) (ProfileTotals, bool, error) {
	f.reads.Add(1)
	if f.fail.Load() {
		return ProfileTotals{}, false, context.DeadlineExceeded
	}
	if account != "a1" || f.private.Load() {
		return ProfileTotals{}, false, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := ProfileTotals{Days: []ProfileDay{}}
	for date, history := range f.history {
		if date > endedEverywhere(p) {
			continue
		}
		s := 0.0
		for _, c := range history {
			if !c.at.After(p) {
				s = c.s
			}
		}
		if s > 0 {
			out.Days = append(out.Days, ProfileDay{Date: date, CreditedS: s})
		}
	}
	slices.SortFunc(out.Days, func(a, b ProfileDay) int { return strings.Compare(a.Date, b.Date) })
	return out, true, nil
}
