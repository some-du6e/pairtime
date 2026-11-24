// SPDX-License-Identifier: AGPL-3.0-or-later

package stream

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// meTopic is the stream every test opens for an owner.
const meTopic = Path + "?topics=" + TopicMe

func TestParseID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want map[string]position
	}{
		{name: "one topic's place reads back as its name, epoch and number", id: "user:me:abcdefghijklm-18",
			want: map[string]position{"user:me": {epoch: "abcdefghijklm", seq: 18}}},
		{name: "places of several topics are joined by commas, and each name keeps its own colons", id: "user:me:abcdefghijklm-0,profile:@rowan:zzzzzzzzzzzzz-7",
			want: map[string]position{"user:me": {epoch: "abcdefghijklm", seq: 0}, "profile:@rowan": {epoch: "zzzzzzzzzzzzz", seq: 7}}},
		{name: "an epoch that isn't 13 lowercase base32 letters isn't one", id: "user:me:ABCDEFGHIJKLM-1"},
		{name: "a short epoch isn't one", id: "user:me:abc-1"},
		{name: "a number with a leading zero isn't one", id: "user:me:abcdefghijklm-01"},
		{name: "a number past uint64 isn't one", id: "user:me:abcdefghijklm-18446744073709551616"},
		{name: "a place without a number isn't one", id: "user:me:abcdefghijklm"},
		{name: "a place without a topic isn't one", id: ":abcdefghijklm-1"},
		{name: "an empty place isn't one", id: "user:me:abcdefghijklm-1,"},
		{name: "an id longer than maxIDLen isn't read", id: "user:me:abcdefghijklm-1," + strings.Repeat("x", maxIDLen)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseID(tt.id)
			if ok != (tt.want != nil) || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseID(%q) = %v, %v; want %v", tt.id, got, ok, tt.want)
			}
		})
	}
}

// TestStart checks how a stream starts.
func TestStart(t *testing.T) {
	tests := []struct {
		name  string
		check func(t *testing.T, c *client, text string)
	}{
		{name: "a stream answers 200 as text/event-stream with no-cache, so no proxy holds it", check: func(t *testing.T, c *client, _ string) {
			if c.code != http.StatusOK {
				t.Errorf("status %d, want 200", c.code)
			}
			for k, want := range map[string]string{"Content-Type": "text/event-stream", "Cache-Control": "no-cache", "X-Accel-Buffering": "no", "X-Content-Type-Options": "nosniff"} {
				if got := c.header.Get(k); got != want {
					t.Errorf("%s = %q, want %q", k, got, want)
				}
			}
		}},
		{name: "its first block sets retry: 5000 and its place as an id, with no data, so it dispatches nothing", check: func(t *testing.T, _ *client, text string) {
			blocks := parseSSE(text)
			if len(blocks) != 1 {
				t.Fatalf("got %d blocks, want 1:\n%s", len(blocks), text)
			}
			b := blocks[0]
			if b.retry != "5000" || b.data != "" || b.event != "" {
				t.Errorf("first block = %+v, want retry 5000 and no event or data", b)
			}
			if p, ok := parseID(b.id); !ok || len(p) != 1 || p[TopicMe].seq != 0 {
				t.Errorf("first block's id %q isn't user:me's place before any event", b.id)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := New(Options{Account: testAccount})
				defer h.Close()
				c := open(t, h, meTopic, as("a1"))
				text := c.read()
				tt.check(t, c, text)
			})
		})
	}
}

// seen is a stream that saw three events on a1's topic, then went.
type seen struct {
	first string   // the id of its first block, before any event
	ids   []string // the ids of the three events
}

// seeThree opens a stream on a1's topic, publishes three events to it,
// and closes it, as a browser whose connection dropped.
func seeThree(t *testing.T, h *Hub) seen {
	t.Helper()
	c := open(t, h, meTopic, as("a1"))
	s := seen{first: lastID(c.read())}
	for i := 1; i <= 3; i++ {
		publishTo(h, userKey+"a1", "a1", "credited", `{"n":`+strconv.Itoa(i)+`}`)
	}
	synctest.Wait()
	for _, e := range events(c.read()) {
		s.ids = append(s.ids, e.id)
	}
	if len(s.ids) != 3 {
		t.Fatalf("the stream saw %d events, want 3", len(s.ids))
	}
	c.close()
	return s
}

// withNumber returns id with its one place's number set to n.
func withNumber(id string, n int) string {
	i := strings.LastIndexByte(id, '-')
	return id[:i+1] + strconv.Itoa(n)
}

// withEpoch returns id with its one place's epoch set to epoch.
func withEpoch(id, epoch string) string {
	i := strings.LastIndexByte(id, ':')
	j := strings.LastIndexByte(id, '-')
	return id[:i+1] + epoch + id[j:]
}

func TestResume(t *testing.T) {
	type reconnect struct {
		hub    *Hub
		lastID string
	}
	tests := []struct {
		name string
		// before runs after the first stream saw its three events and
		// went, and returns the reconnect.
		before func(t *testing.T, h *Hub, s seen) reconnect
		// replay is the data of the events the reconnect gets, and
		// resync whether it gets resync.
		replay []string
		resync bool
	}{
		{name: "a reconnect with the first event's id replays the gap, the second and third, in order",
			before: func(_ *testing.T, h *Hub, s seen) reconnect { return reconnect{h, s.ids[0]} },
			replay: []string{`{"n":2}`, `{"n":3}`}},
		{name: "a reconnect with the id of the stream's first block, sent before any event, replays every event",
			before: func(_ *testing.T, h *Hub, s seen) reconnect { return reconnect{h, s.first} },
			replay: []string{`{"n":1}`, `{"n":2}`, `{"n":3}`}},
		{name: "a reconnect with the last id replays nothing and resyncs nothing",
			before: func(_ *testing.T, h *Hub, s seen) reconnect { return reconnect{h, s.ids[2]} }},
		{name: "a reconnect four minutes later still replays the gap",
			before: func(_ *testing.T, h *Hub, s seen) reconnect {
				time.Sleep(4 * time.Minute)
				return reconnect{h, s.ids[1]}
			},
			replay: []string{`{"n":3}`}},
		{name: "a reconnect whose gap is older than the window gets resync",
			before: func(t *testing.T, h *Hub, s seen) reconnect {
				// A second stream keeps the topic, so only the ring's
				// window decides.
				open(t, h, meTopic, as("a1"))
				time.Sleep(DefaultWindow)
				return reconnect{h, s.ids[0]}
			},
			resync: true},
		{name: "a reconnect with an id older than the window gets nothing when its topic published nothing since",
			before: func(t *testing.T, h *Hub, s seen) reconnect {
				open(t, h, meTopic, as("a1"))
				time.Sleep(2 * DefaultWindow)
				return reconnect{h, s.ids[2]}
			}},
		{name: "an id from another epoch gets resync",
			before: func(_ *testing.T, h *Hub, s seen) reconnect {
				return reconnect{h, withEpoch(s.ids[0], "aaaaaaaaaaaaa")}
			},
			resync: true},
		{name: "an id from before a restart gets resync from the new process's hub, never a replay",
			before: func(_ *testing.T, h *Hub, s seen) reconnect {
				restarted := New(Options{Account: testAccount})
				publishTo(restarted, userKey+"a1", "a1", "credited", `{"n":1}`)
				publishTo(restarted, userKey+"a1", "a1", "credited", `{"n":2}`)
				return reconnect{restarted, s.ids[0]}
			},
			resync: true},
		{name: "an id past the topic's last event gets resync",
			before: func(_ *testing.T, h *Hub, s seen) reconnect { return reconnect{h, withNumber(s.ids[2], 99)} },
			resync: true},
		{name: "an id that isn't one gets resync",
			before: func(_ *testing.T, h *Hub, s seen) reconnect { return reconnect{h, "not an id"} },
			resync: true},
		{name: "an id of another topic gets resync",
			before: func(_ *testing.T, h *Hub, s seen) reconnect {
				return reconnect{h, "profile:@rowan" + s.ids[0][len(TopicMe):]}
			},
			resync: true},
		{name: "a gap longer than the ring holds gets resync",
			before: func(t *testing.T, h *Hub, s seen) reconnect {
				open(t, h, meTopic, as("a1"))
				for range ringMax {
					publishTo(h, userKey+"a1", "a1", "credited", `{"n":0}`)
				}
				return reconnect{h, s.ids[1]}
			},
			resync: true},
		{name: "a stream with no id starts at the topic's last event and replays nothing",
			before: func(_ *testing.T, h *Hub, _ seen) reconnect { return reconnect{h, ""} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := New(Options{Account: testAccount})
				defer h.Close()
				s := seeThree(t, h)
				rc := tt.before(t, h, s)
				if rc.hub != h {
					defer rc.hub.Close()
				}
				header := as("a1")
				if rc.lastID != "" {
					header.Set("Last-Event-ID", rc.lastID)
				}
				c := open(t, rc.hub, meTopic, header)
				text := c.read()
				var replay []string
				resync := false
				for _, e := range events(text) {
					switch e.event {
					case "resync":
						resync = true
						var r Resync
						if err := json.Unmarshal([]byte(e.data), &r); err != nil || !reflect.DeepEqual(r.Topics, []string{TopicMe}) {
							t.Errorf("resync data %s, want the topics user:me", e.data)
						}
					case "credited":
						replay = append(replay, e.data)
					}
				}
				if !reflect.DeepEqual(replay, tt.replay) || resync != tt.resync {
					t.Errorf("the reconnect got replay %q and resync %v, want %q and %v:\n%s", replay, resync, tt.replay, tt.resync, text)
				}
				// The replayed events carry the ids the first stream saw.
				if tt.replay != nil {
					for i, e := range events(text) {
						if want := s.ids[len(s.ids)-len(tt.replay)+i]; e.id != want {
							t.Errorf("replayed event %d has id %q, want %q", i, e.id, want)
						}
					}
				}
				// Whatever it got, the reconnect's place is the topic's
				// last event, so the next event follows it.
				publishTo(rc.hub, userKey+"a1", "a1", "credited", `{"n":"next"}`)
				synctest.Wait()
				next := events(c.read())
				if len(next) != 1 || next[0].data != `{"n":"next"}` {
					t.Fatalf("after the reconnect, a new event reached it as %+v", next)
				}
				p, _ := parseID(next[0].id)
				rc.hub.mu.Lock()
				topic := rc.hub.topics[userKey+"a1"]
				want := position{epoch: topic.epoch, seq: topic.seq}
				rc.hub.mu.Unlock()
				if p[TopicMe] != want {
					t.Errorf("the next event's id %q, want user:me at %+v", next[0].id, want)
				}
			})
		})
	}
}

// TestResumeTopics checks that one id resumes every topic of a stream.
func TestResumeTopics(t *testing.T) {
	tests := []struct {
		name string
		// publish is the events published while the stream was away, as
		// "u" for a1's own topic and "p" for its profile's.
		publish string
		replay  []string
	}{
		{name: "the gaps of both topics replay in the order they were published", publish: "pup", replay: []string{"p1", "u2", "p2"}},
		{name: "a topic with no gap replays nothing while the other replays its own", publish: "uu", replay: []string{"u2", "u3"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := New(Options{Account: testAccount, Profiles: &fakeProfiles{}})
				defer h.Close()
				const target = Path + "?topics=user:me,profile:@rowan"
				c := open(t, h, target, as("a1"))
				c.read()
				publishTo(h, userKey+"a1", "a1", "credited", `"u1"`)
				synctest.Wait()
				last := lastID(c.read())
				c.close()
				n := map[byte]int{'u': 1, 'p': 0}
				for i := range len(tt.publish) {
					kind := tt.publish[i]
					n[kind]++
					key, name := userKey+"a1", "credited"
					if kind == 'p' {
						key, name = profileKey+"a1", "profile"
					}
					publishTo(h, key, "a1", name, `"`+string(kind)+strconv.Itoa(n[kind])+`"`)
				}
				header := as("a1")
				header.Set("Last-Event-ID", last)
				c = open(t, h, target, header)
				var replay []string
				for _, e := range events(c.read()) {
					if e.event == "resync" {
						t.Errorf("resync of %s, want none", e.data)
					}
					replay = append(replay, strings.Trim(e.data, `"`))
				}
				if !reflect.DeepEqual(replay, tt.replay) {
					t.Errorf("replayed %q, want %q", replay, tt.replay)
				}
			})
		})
	}
}

func TestKeepalive(t *testing.T) {
	tests := []struct {
		name string
		// publishAt is when an event goes out, or 0 for none.
		publishAt time.Duration
	}{
		{name: "an idle stream gets : keepalive every 15 s from its start, with an event a page hears, and nothing between"},
		{name: "an event between keepalives doesn't move them", publishAt: 7 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := New(Options{Account: testAccount})
				defer h.Close()
				start := time.Now()
				c := open(t, h, meTopic, as("a1"))
				c.read()
				var at []time.Duration
				for range 61 {
					time.Sleep(time.Second)
					if time.Since(start) == tt.publishAt {
						publishTo(h, userKey+"a1", "a1", "credited", `{}`)
					}
					synctest.Wait()
					for _, b := range parseSSE(c.read()) {
						if b.comment == "keepalive" {
							at = append(at, time.Since(start))
							if b.event != "keepalive" || b.data != "" || b.id != "" {
								t.Errorf("a keepalive block %+v, want the comment with an event named keepalive, no data and no id", b)
							}
						}
					}
				}
				want := []time.Duration{15 * time.Second, 30 * time.Second, 45 * time.Second, 60 * time.Second}
				if !reflect.DeepEqual(at, want) {
					t.Errorf("keepalives came at %v, want %v", at, want)
				}
			})
		})
	}
}

func TestRefusals(t *testing.T) {
	broken := func(*http.Request) (string, error) { return "", errors.New("the session table is locked") }
	private := &fakeProfiles{}
	private.private.Store(true)
	tests := []struct {
		name string
		opts Options
		// noSessions leaves Options.Account nil, where every other case
		// reads the test session unless opts has its own.
		noSessions bool
		method     string
		target     string
		header     http.Header
		closed     bool
		status     int
		code       string
		// headers are more headers the answer must have.
		headers map[string]string
	}{
		{name: "user:me signed out gets 401 with the session challenge", target: meTopic,
			status: 401, code: CodeNotSignedIn, headers: map[string]string{"WWW-Authenticate": `Session realm="pairtime"`}},
		{name: "user:me on a hub that reads no sessions gets 401", noSessions: true, target: meTopic, header: as("a1"),
			status: 401, code: CodeNotSignedIn},
		{name: "another account's topic gets 403, even signed in", target: Path + "?topics=user:a2", header: as("a1"),
			status: 403, code: CodeForbiddenTopic},
		{name: "an account's own id as a topic gets 403 too, since only user:me names it", target: Path + "?topics=user:a1", header: as("a1"),
			status: 403, code: CodeForbiddenTopic},
		{name: "another account's topic beside user:me gets 403", target: Path + "?topics=user:me,user:a2", header: as("a1"),
			status: 403, code: CodeForbiddenTopic},
		{name: "a profile topic gets 404 while the server serves no profiles", target: Path + "?topics=profile:@rowan",
			status: 404, code: CodeUnknownTopic},
		{name: "a profile no public profile has gets 404", opts: Options{Profiles: &fakeProfiles{}}, target: Path + "?topics=profile:@nobody",
			status: 404, code: CodeUnknownTopic},
		{name: "a private profile gets 404, as one that doesn't exist", opts: Options{Profiles: private}, target: Path + "?topics=profile:@rowan",
			status: 404, code: CodeUnknownTopic},
		{name: "SP 6.4's global: and project: topics get 404 until they're built", target: Path + "?topics=global:pairing",
			status: 404, code: CodeUnknownTopic},
		{name: "two names of one profile get 400", opts: Options{Profiles: &fakeProfiles{}}, target: Path + "?topics=profile:@rowan,profile:@ro",
			status: 400, code: CodeBadTopics},
		{name: "no topics parameter gets 400", target: Path, status: 400, code: CodeBadTopics},
		{name: "a topics parameter given twice gets 400", target: meTopic + "&topics=user:me", header: as("a1"), status: 400, code: CodeBadTopics},
		{name: "an empty topic name gets 400", target: meTopic + ",", header: as("a1"), status: 400, code: CodeBadTopics},
		{name: "a profile topic without its @ gets 400", target: Path + "?topics=profile:rowan", status: 400, code: CodeBadTopics},
		{name: "a profile name with a colon in it gets 400", target: Path + "?topics=profile:@ro:1", status: 400, code: CodeBadTopics},
		{name: "more than MaxTopics topics get 400", target: Path + "?topics=profile:@a,profile:@b,profile:@c,profile:@d,profile:@e,profile:@f,profile:@g,profile:@h,profile:@i",
			status: 400, code: CodeBadTopics},
		{name: "a session that can't be read gets 500, never a sign-in", opts: Options{Account: broken}, target: meTopic,
			status: 500, code: CodeUnavailable},
		{name: "a POST gets 405 that says GET", method: http.MethodPost, target: meTopic, header: as("a1"),
			status: 405, code: CodeMethod, headers: map[string]string{"Allow": "GET"}},
		{name: "a stopped hub gets 503", target: meTopic, header: as("a1"), closed: true,
			status: 503, code: CodeUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := tt.opts
			if o.Account == nil && !tt.noSessions {
				o.Account = testAccount
			}
			h := New(o)
			defer h.Close()
			if tt.closed {
				h.Close()
			}
			method := tt.method
			if method == "" {
				method = http.MethodGet
			}
			r := httptest.NewRequest(method, tt.target, nil)
			for k, vs := range tt.header {
				r.Header[k] = vs
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			var body struct{ Error string }
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("body %q isn't JSON: %v", rec.Body, err)
			}
			if rec.Code != tt.status || body.Error != tt.code {
				t.Errorf("got %d %q, want %d %q", rec.Code, body.Error, tt.status, tt.code)
			}
			want := map[string]string{"Content-Type": "application/json; charset=utf-8", "Cache-Control": "no-store"}
			for k, v := range tt.headers {
				want[k] = v
			}
			for k, v := range want {
				if got := rec.Header().Get(k); got != v {
					t.Errorf("%s = %q, want %q", k, got, v)
				}
			}
			if h.Streams() != 0 {
				t.Errorf("a refused request left %d streams counted", h.Streams())
			}
		})
	}
}

func TestCaps(t *testing.T) {
	// from opens a stream from the address addr, as Cloudflare names it.
	from := func(account, addr string) http.Header {
		hd := http.Header{"Cf-Connecting-Ip": {addr}}
		if account != "" {
			hd.Set("X-Test-Account", account)
		}
		return hd
	}
	type try struct {
		target string
		header http.Header
		// status is what it gets: 200 for a stream that opens, which
		// stays open, or 429.
		status int
		// closeAfter ends the stream once it opened.
		closeAfter bool
	}
	const profile = Path + "?topics=profile:@rowan"
	tests := []struct {
		name  string
		opts  Options
		tries []try
	}{
		{name: "an account past MaxPerAccount streams gets 429 with Retry-After, while another account still opens one",
			opts: Options{MaxPerAccount: 2},
			tries: []try{
				{meTopic, from("a1", "192.0.2.1"), 200, false},
				{meTopic, from("a1", "192.0.2.2"), 200, false},
				{meTopic, from("a1", "192.0.2.3"), 429, false},
				{meTopic, from("a2", "192.0.2.1"), 200, false},
			}},
		{name: "a client address past MaxPerClient gets 429, counting its signed-in and anonymous streams, while another address opens one",
			opts: Options{MaxPerClient: 2},
			tries: []try{
				{meTopic, from("a1", "192.0.2.1"), 200, false},
				{profile, from("", "192.0.2.1"), 200, false},
				{profile, from("", "192.0.2.1"), 429, false},
				{meTopic, from("a2", "192.0.2.1"), 429, false},
				{profile, from("", "198.51.100.7"), 200, false},
			}},
		{name: "IPv6 addresses count by their /64",
			opts: Options{MaxPerClient: 1},
			tries: []try{
				{profile, from("", "2001:db8::1"), 200, false},
				{profile, from("", "2001:db8::2"), 429, false},
				{profile, from("", "2001:db8:0:1::1"), 200, false},
			}},
		{name: "past MaxStreams every new stream gets 429, whoever asks",
			opts: Options{MaxStreams: 2},
			tries: []try{
				{meTopic, from("a1", "192.0.2.1"), 200, false},
				{profile, from("", "198.51.100.7"), 200, false},
				{meTopic, from("a2", "203.0.113.9"), 429, false},
			}},
		{name: "a stream that ends frees its place",
			opts: Options{MaxPerAccount: 1, MaxPerClient: 1, MaxStreams: 1},
			tries: []try{
				{meTopic, from("a1", "192.0.2.1"), 200, true},
				{meTopic, from("a1", "192.0.2.1"), 200, false},
				{meTopic, from("a1", "192.0.2.1"), 429, false},
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := tt.opts
				o.Account, o.Profiles = testAccount, &fakeProfiles{}
				h := New(o)
				defer h.Close()
				for i, tr := range tt.tries {
					if tr.status == http.StatusTooManyRequests {
						r := httptest.NewRequest(http.MethodGet, tr.target, nil)
						for k, vs := range tr.header {
							r.Header[k] = vs
						}
						rec := httptest.NewRecorder()
						h.ServeHTTP(rec, r)
						if rec.Code != 429 || rec.Header().Get("Retry-After") != "30" || !strings.Contains(rec.Body.String(), CodeTooManyStreams) {
							t.Errorf("try %d got %d, Retry-After %q, %s; want 429 too_many_streams after 30 s", i, rec.Code, rec.Header().Get("Retry-After"), rec.Body)
						}
						continue
					}
					c := open(t, h, tr.target, tr.header)
					c.read()
					if c.code != 200 {
						t.Errorf("try %d got %d, want a stream", i, c.code)
					}
					if tr.closeAfter {
						c.close()
					}
				}
			})
		})
	}
}

// TestSlowStream checks what the hub does with a stream that can't keep
// up.
func TestSlowStream(t *testing.T) {
	tests := []struct {
		name string
		// events is how many events go out while the stream's writes are
		// stuck, after the one it is writing.
		events int
		ended  bool
	}{
		{name: "a stream a buffer behind keeps its place, and gets every event once its writes move", events: sendBuffer},
		{name: "a stream more than a buffer behind is ended, so publishing never waits for it", events: sendBuffer + 1, ended: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := New(Options{Account: testAccount})
				defer h.Close()
				c := open(t, h, meTopic, as("a1"))
				c.read()
				c.w.hold.Store(true)
				publishTo(h, userKey+"a1", "a1", "credited", `{"n":0}`)
				synctest.Wait()
				for range tt.events {
					publishTo(h, userKey+"a1", "a1", "credited", `{"n":1}`)
				}
				h.mu.Lock()
				on := len(h.topics[userKey+"a1"].subs) == 1
				h.mu.Unlock()
				if on == tt.ended {
					t.Errorf("after %d events behind, the stream is on its topic: %v, want %v", tt.events, on, !tt.ended)
				}
				close(c.w.release)
				synctest.Wait()
				got := len(events(c.read()))
				if tt.ended {
					if !c.ended() {
						t.Error("the stream that fell behind is still open")
					}
					if h.Streams() != 0 {
						t.Errorf("%d streams counted after the slow one ended", h.Streams())
					}
					return
				}
				if c.ended() || got != tt.events+1 {
					t.Errorf("the stream got %d events and ended: %v; want all %d, still open", got, c.ended(), tt.events+1)
				}
			})
		})
	}
}

func TestClose(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "Close ends every stream, and the hub's goroutines with them"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := New(Options{Account: testAccount, Profiles: &fakeProfiles{}, Source: &viewSource{}})
				a := open(t, h, meTopic, as("a1"))
				b := open(t, h, Path+"?topics=profile:@rowan", nil)
				h.Changed("a1")
				h.Close()
				synctest.Wait()
				if !a.ended() || !b.ended() {
					t.Errorf("after Close, streams ended: %v and %v, want both", a.ended(), b.ended())
				}
				if h.Streams() != 0 {
					t.Errorf("%d streams counted after Close", h.Streams())
				}
				h.Changed("a1")
			})
		})
	}
}

func TestSweep(t *testing.T) {
	tests := []struct {
		name string
		// away is how long no stream is on the topic before one
		// reconnects with its last id.
		away   time.Duration
		resync bool
	}{
		{name: "a topic a stream left less than the window ago keeps its epoch, so a reconnect replays its gap", away: DefaultWindow - time.Second},
		{name: "a topic no stream was on for the window, with no event left, is dropped, and the one made again has a new epoch", away: DefaultWindow + time.Minute, resync: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := New(Options{Account: testAccount})
				defer h.Close()
				c := open(t, h, meTopic, as("a1"))
				last := lastID(c.read())
				c.close()
				// One event while it's away, which a reconnect in time
				// replays.
				publishTo(h, userKey+"a1", "a1", "credited", `{"n":1}`)
				time.Sleep(tt.away)
				synctest.Wait()
				h.mu.Lock()
				_, kept := h.topics[userKey+"a1"]
				h.mu.Unlock()
				if kept == tt.resync {
					t.Errorf("after %v away the topic is kept: %v, want %v", tt.away, kept, !tt.resync)
				}
				header := as("a1")
				header.Set("Last-Event-ID", last)
				c = open(t, h, meTopic, header)
				var got []string
				for _, e := range events(c.read()) {
					got = append(got, e.event)
				}
				want := []string{"credited"}
				if tt.resync {
					want = []string{"resync"}
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("the reconnect got %q, want %q", got, want)
				}
			})
		})
	}
}
