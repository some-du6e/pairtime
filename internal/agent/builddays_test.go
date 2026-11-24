// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/account"
	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/agent/uploader"
)

// teenServer is pairtimed's build-days routes as the agent asks them.
type teenServer struct {
	mu sync.Mutex
	// get is GET /api/v1/build-days's status, and body its answer, which
	// summaryJSON of the goal makes when it is "".
	get  int
	body string
	// put is PUT /api/v1/build-days/goal's status, with retryAfter on a
	// 429; a 200 sets goal.
	put        int
	retryAfter string
	goal       int64
	gets, puts int
	// tokens are the Authorization headers the server got.
	tokens []string
	srv    *httptest.Server
}

// teenSeen is what the server has had so far, read under its lock, since the
// requests reach it on goroutines of its own.
type teenSeen struct {
	gets, puts int
	goal       int64
	tokens     []string
}

func (s *teenServer) seen() teenSeen {
	s.mu.Lock()
	defer s.mu.Unlock()
	return teenSeen{gets: s.gets, puts: s.puts, goal: s.goal, tokens: append([]string(nil), s.tokens...)}
}

// set changes the server's answers under its lock.
func (s *teenServer) set(f func(s *teenServer)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s)
}

func newTeenServer(t *testing.T) *teenServer {
	t.Helper()
	s := &teenServer{get: http.StatusOK, put: http.StatusOK, goal: 3}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.tokens = append(s.tokens, r.Header.Get("Authorization"))
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/build-days":
			s.gets++
			w.WriteHeader(s.get)
			body := s.body
			if body == "" {
				body = summaryJSON(s.goal)
			}
			io.WriteString(w, body)
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/build-days/goal":
			s.puts++
			var b struct {
				WeeklyGoal int64 `json:"weekly_goal"`
			}
			json.NewDecoder(r.Body).Decode(&b)
			if s.retryAfter != "" {
				w.Header().Set("Retry-After", s.retryAfter)
			}
			w.WriteHeader(s.put)
			if s.put == http.StatusOK {
				s.goal = b.WeeklyGoal
				fmt.Fprintf(w, `{"weekly_goal":%d}`, b.WeeklyGoal)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

// summaryJSON is a summary as pairtimed sends it, with a member a newer
// server might add.
func summaryJSON(goal int64) string {
	return fmt.Sprintf(`{"time_zone":"America/New_York","today":"2026-11-20","weekly_goal":%d,"build_days":18,"window_days":30,"weeks_in_a_row":4,
"freezes":{"held":1,"max":2,"earned":3,"toward_next":5,"every":7},
"days":[{"date":"2026-11-19","state":"build_day","a_ms":912000,"b_ms":0,"deadline":"2026-11-23T05:00:00Z","decided_at":"2026-11-19T15:01:00Z"},
{"date":"2026-11-20","state":"open","a_ms":0,"b_ms":0,"deadline":"2026-11-24T05:00:00Z"}],
"weeks":[{"start":"2026-11-16","result":"open","goal":%d,"build_days":1}],"streak_flames":7}`, goal, goal)
}

// calibratingJSON is a calibration volunteer's summary as pairtimed sends
// it while their account's flag is on (D-server-shadow).
const calibratingJSON = `{"calibrating":true,"time_zone":"UTC","today":"2026-11-20","weekly_goal":3,"window_days":30,"freezes":{"max":2,"every":7},
"days":[{"date":"2026-11-20","deadline":"2026-11-24T00:00:00Z"}],"weeks":[{"start":"2026-11-16","goal":3}]}`

// bdAgent is an agent with only what the Build days routes read: its
// directories, config.toml as text, and, when linked, account.json and a
// sign-in for server.
func bdAgent(t *testing.T, text string, linked bool, server string) *agent {
	t.Helper()
	dirs := home.Dirs{Data: t.TempDir(), Config: t.TempDir()}
	dirs.Runtime = dirs.Data
	if err := dirs.Create(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dirs.ConfigFile(), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if linked {
		l := `{"v":1,"server":"` + server + `","device_id":"d_bd","public_key":"AAAA","linked_at":"2026-11-01T00:00:00Z"}`
		if err := os.WriteFile(account.File(dirs.Data), []byte(l), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	saved := accountCredentials
	t.Cleanup(func() { accountCredentials = saved })
	accountCredentials = func(d home.Dirs, _ []keys.Store) (uploader.Credentials, bool, error) {
		if _, err := account.Load(d.Data); err != nil {
			return uploader.Credentials{}, false, nil
		}
		return uploader.Credentials{Server: server, Token: "ptd_bd", DeviceID: "d_bd"}, true, nil
	}
	cfg, _ := config.Parse([]byte(text))
	a := &agent{dirs: dirs, log: slog.New(slog.DiscardHandler), problems: map[string]string{}}
	a.cfg.Store(&cfg)
	return a
}

// readBuildDays asks the agent's GET /v1/build-days and returns its answer
// decoded, and as raw JSON, to look for the members left out.
func readBuildDays(t *testing.T, a *agent) (buildDaysReply, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	a.serveBuildDays(rec, httptest.NewRequest(http.MethodGet, "/v1/build-days", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/build-days = %d %s", rec.Code, rec.Body)
	}
	var r buildDaysReply
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatalf("%v in %s", err, rec.Body)
	}
	return r, rec.Body.String()
}

// setGoal asks the agent's PUT /v1/build-days/goal with body.
func setGoal(t *testing.T, a *agent, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	a.serveBuildDaysGoal(rec, httptest.NewRequest(http.MethodPut, "/v1/build-days/goal", strings.NewReader(body)))
	return rec
}

// hiddenMembers are the members a calibrating summary leaves out, as
// "member" or "list[].member".
var hiddenMembers = []string{"build_days", "weeks_in_a_row", "freezes.held", "freezes.earned", "freezes.toward_next",
	"days[].state", "days[].a_ms", "days[].b_ms", "days[].decided_at", "weeks[].result", "weeks[].build_days"}

// keptMembers are the ones it keeps.
var keptMembers = []string{"calibrating", "time_zone", "today", "weekly_goal", "window_days", "freezes.max", "freezes.every",
	"days[].date", "days[].deadline", "weeks[].start", "weeks[].goal"}

// has reports whether the summary in raw, a GET /v1/build-days answer,
// holds member, in any element for a list's member.
func has(t *testing.T, raw, member string) bool {
	t.Helper()
	var reply struct {
		Summary map[string]any `json:"summary"`
	}
	if err := json.Unmarshal([]byte(raw), &reply); err != nil {
		t.Fatal(err)
	}
	parent, name, nested := strings.Cut(member, ".")
	if !nested {
		_, ok := reply.Summary[member]
		return ok
	}
	if list, ok := strings.CutSuffix(parent, "[]"); ok {
		items, _ := reply.Summary[list].([]any)
		for _, it := range items {
			if _, ok := it.(map[string]any)[name]; ok {
				return true
			}
		}
		return false
	}
	obj, _ := reply.Summary[parent].(map[string]any)
	_, ok := obj[name]
	return ok
}

func TestBuildDaysAPI(t *testing.T) {
	online := "mode = \"online\"\n"
	tests := []struct {
		name   string
		config string
		linked bool
		// prepare sets the server up before the steps; down closes it.
		prepare func(s *teenServer)
		down    bool
		steps   func(t *testing.T, a *agent, s *teenServer)
	}{
		{
			name:   "a computer pairtime login never linked asks the server nothing and says so",
			config: online,
			steps: func(t *testing.T, a *agent, s *teenServer) {
				r, _ := readBuildDays(t, a)
				if r.Linked || r.Online || r.Summary != nil || r.Problem != "" || s.seen().gets != 0 {
					t.Errorf("answer %+v after %d requests, want unlinked, offline, no summary, no problem and none", r, s.seen().gets)
				}
			},
		},
		{
			name:   "a local-only computer asks the server nothing and says so",
			config: "mode = \"local\"\n", linked: true,
			steps: func(t *testing.T, a *agent, s *teenServer) {
				r, _ := readBuildDays(t, a)
				if !r.Linked || r.Online || r.Summary != nil || r.Problem != "" || s.seen().gets != 0 {
					t.Errorf("answer %+v after %d requests, want linked, not online, no summary and none", r, s.seen().gets)
				}
			},
		},
		{
			name:   "a linked computer online mirrors the server's summary with its device token, and when it got it",
			config: online, linked: true,
			steps: func(t *testing.T, a *agent, s *teenServer) {
				before := time.Now().Add(-time.Second)
				r, raw := readBuildDays(t, a)
				if !r.Linked || !r.Online || r.Summary == nil || r.Problem != "" || r.AsOf == nil || r.AsOf.Before(before) {
					t.Fatalf("answer %s, want the summary, as of now, with no problem", raw)
				}
				sm := r.Summary
				if sm.Calibrating || sm.WeeklyGoal != 3 || *sm.BuildDays != 18 || *sm.WeeksInARow != 4 || *sm.Freezes.Held != 1 || len(sm.Days) != 2 || sm.Days[0].State != "build_day" || *sm.Days[0].AMS != 912000 || *sm.Weeks[0].BuildDays != 1 {
					t.Errorf("summary %s, want the server's", raw)
				}
				if strings.Contains(raw, "streak_flames") {
					t.Errorf("the answer passes on a member the spec doesn't name: %s", raw)
				}
				if got := s.seen().tokens; len(got) != 1 || got[0] != "Bearer ptd_bd" {
					t.Errorf("the server got Authorization %q, want the device token", got)
				}
			},
		},
		{
			name:   "a second read within 60 seconds answers from memory",
			config: online, linked: true,
			steps: func(t *testing.T, a *agent, s *teenServer) {
				readBuildDays(t, a)
				if r, _ := readBuildDays(t, a); r.Summary == nil || s.seen().gets != 1 {
					t.Errorf("two reads asked the server %d times, want once", s.seen().gets)
				}
			},
		},
		{
			name:   "an answer older than 60 seconds is asked for again",
			config: online, linked: true,
			steps: func(t *testing.T, a *agent, s *teenServer) {
				readBuildDays(t, a)
				a.buildDays.at = time.Now().Add(-buildDaysFresh)
				readBuildDays(t, a)
				if got := s.seen().gets; got != 2 {
					t.Errorf("the server was asked %d times, want twice", got)
				}
			},
		},
		{
			name:   "while display.shadow is on the summary leaves out everything that comes of credited time, as the server's calibrating one does",
			config: online + "[display]\nshadow = true\n", linked: true,
			steps: func(t *testing.T, a *agent, s *teenServer) {
				r, raw := readBuildDays(t, a)
				if !r.Shadow || r.Summary == nil || !r.Summary.Calibrating {
					t.Fatalf("answer %s, want shadow and a calibrating summary", raw)
				}
				for _, m := range hiddenMembers {
					if has(t, raw, m) {
						t.Errorf("the summary holds %s: %s", m, raw)
					}
				}
				for _, m := range keptMembers {
					if !has(t, raw, m) {
						t.Errorf("the summary lacks %s: %s", m, raw)
					}
				}
			},
		},
		{
			name:   "a calibrating summary from the server passes as it came, with display.shadow off",
			config: online, linked: true,
			prepare: func(s *teenServer) { s.body = calibratingJSON },
			steps: func(t *testing.T, a *agent, s *teenServer) {
				r, raw := readBuildDays(t, a)
				if r.Shadow || r.Summary == nil || !r.Summary.Calibrating || r.Problem != "" {
					t.Fatalf("answer %s, want the server's calibrating summary", raw)
				}
				for _, m := range hiddenMembers {
					if has(t, raw, m) {
						t.Errorf("the summary holds %s: %s", m, raw)
					}
				}
			},
		},
		{
			name:   "a server that can't be reached leaves no summary and says unreachable",
			config: online, linked: true, down: true,
			steps: func(t *testing.T, a *agent, s *teenServer) {
				if r, raw := readBuildDays(t, a); r.Summary != nil || r.Problem != problemUnreachable {
					t.Errorf("answer %s, want no summary and unreachable", raw)
				}
			},
		},
		{
			name:   "once the server stops answering, the page has the last answer of this run with its time, and unreachable",
			config: online, linked: true,
			steps: func(t *testing.T, a *agent, s *teenServer) {
				first, _ := readBuildDays(t, a)
				a.buildDays.at = time.Now().Add(-2 * buildDaysFresh)
				s.set(func(s *teenServer) { s.get = http.StatusServiceUnavailable })
				r, raw := readBuildDays(t, a)
				if r.Summary == nil || *r.Summary.BuildDays != 18 || r.Problem != problemUnreachable || r.AsOf == nil || !r.AsOf.Before(*first.AsOf) {
					t.Errorf("answer %s, want the last summary, as of before, and unreachable", raw)
				}
			},
		},
		{
			name:   "a server that refuses the device token says signed_out",
			config: online, linked: true,
			prepare: func(s *teenServer) { s.get, s.body = http.StatusUnauthorized, `{"error":"token_invalid"}` },
			steps: func(t *testing.T, a *agent, s *teenServer) {
				if r, raw := readBuildDays(t, a); r.Summary != nil || r.Problem != problemSignedOut {
					t.Errorf("answer %s, want signed_out", raw)
				}
			},
		},
		{
			name:   "an answer that isn't a summary says unreadable",
			config: online, linked: true,
			prepare: func(s *teenServer) { s.body = `{"time_zone":"UTC"}` },
			steps: func(t *testing.T, a *agent, s *teenServer) {
				if r, raw := readBuildDays(t, a); r.Summary != nil || r.Problem != problemUnreadable {
					t.Errorf("answer %s, want unreadable", raw)
				}
			},
		},
		{
			name:   "a summary kept for another server or device never shows",
			config: online, linked: true,
			steps: func(t *testing.T, a *agent, s *teenServer) {
				old := bdSummary{TimeZone: "UTC", Today: "2026-11-20", WeeklyGoal: 6, Days: []bdDay{}, Weeks: []bdWeek{}}
				a.buildDays.server, a.buildDays.device, a.buildDays.summary, a.buildDays.at = "https://other.example", "d_other", &old, time.Now()
				s.set(func(s *teenServer) { s.get = http.StatusServiceUnavailable })
				if r, raw := readBuildDays(t, a); r.Summary != nil || r.Problem != problemUnreachable {
					t.Errorf("answer %s, want no summary: the one kept is another sign-in's", raw)
				}
			},
		},
		{
			name:   "a goal from 1 to 7 is set on the server at once, and the answer is the summary as the server has it then",
			config: online, linked: true,
			steps: func(t *testing.T, a *agent, s *teenServer) {
				readBuildDays(t, a)
				rec := setGoal(t, a, `{"weekly_goal": 5}`)
				if rec.Code != http.StatusOK {
					t.Fatalf("PUT = %d %s", rec.Code, rec.Body)
				}
				var r buildDaysReply
				if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil || r.Summary == nil || r.Summary.WeeklyGoal != 5 {
					t.Errorf("the answer %s (%v), want the summary with a goal of 5", rec.Body, err)
				}
				if got := s.seen(); got.puts != 1 || got.goal != 5 || got.gets != 2 {
					t.Errorf("the server had %d PUTs and %d GETs, goal %d; want 1, 2 and 5", got.puts, got.gets, got.goal)
				}
			},
		},
		{
			name:   "a goal outside 1 to 7, or a body that isn't one, is refused before the server is asked",
			config: online, linked: true,
			steps: func(t *testing.T, a *agent, s *teenServer) {
				for _, b := range []string{`{"weekly_goal": 0}`, `{"weekly_goal": 8}`, `{"goal": 3}`, `{"weekly_goal": "3"}`, `{"weekly_goal": 2.5}`, `{}`, `{"weekly_goal": 3} {}`} {
					if rec := setGoal(t, a, b); rec.Code != http.StatusBadRequest {
						t.Errorf("PUT %s = %d, want 400", b, rec.Code)
					}
				}
				if got := s.seen().puts; got != 0 {
					t.Errorf("the server was asked %d times", got)
				}
			},
		},
		{
			name:   "a computer pairtime login never linked refuses to set the goal and says how to sign in",
			config: online,
			steps: func(t *testing.T, a *agent, s *teenServer) {
				if rec := setGoal(t, a, `{"weekly_goal": 4}`); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "pairtime login") || s.seen().puts != 0 {
					t.Errorf("PUT = %d %s after %d requests, want 409 naming pairtime login and none", rec.Code, rec.Body, s.seen().puts)
				}
			},
		},
		{
			name:   "a local-only computer refuses to set the goal and says it stays as it was",
			config: "mode = \"local\"\n", linked: true,
			steps: func(t *testing.T, a *agent, s *teenServer) {
				if rec := setGoal(t, a, `{"weekly_goal": 4}`); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "local-only") || s.seen().puts != 0 {
					t.Errorf("PUT = %d %s after %d requests, want 409 saying local-only and none", rec.Code, rec.Body, s.seen().puts)
				}
			},
		},
		{
			name:   "a server that can't be reached leaves the goal as it was and says so",
			config: online, linked: true, down: true,
			steps: func(t *testing.T, a *agent, s *teenServer) {
				if rec := setGoal(t, a, `{"weekly_goal": 4}`); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "stays as it was") {
					t.Errorf("PUT = %d %s, want 503 saying the goal stays", rec.Code, rec.Body)
				}
			},
		},
		{
			name:   "a server that refuses the device token asks for pairtime login again",
			config: online, linked: true,
			prepare: func(s *teenServer) { s.put = http.StatusUnauthorized },
			steps: func(t *testing.T, a *agent, s *teenServer) {
				if rec := setGoal(t, a, `{"weekly_goal": 4}`); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "pairtime login again") {
					t.Errorf("PUT = %d %s, want 409 asking for pairtime login again", rec.Code, rec.Body)
				}
			},
		},
		{
			name:   "a server past the device's rate passes its Retry-After on",
			config: online, linked: true,
			prepare: func(s *teenServer) { s.put, s.retryAfter = http.StatusTooManyRequests, "7" },
			steps: func(t *testing.T, a *agent, s *teenServer) {
				rec := setGoal(t, a, `{"weekly_goal": 4}`)
				if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "7" {
					t.Errorf("PUT = %d, Retry-After %q; want 429 and 7", rec.Code, rec.Header().Get("Retry-After"))
				}
			},
		},
		{
			name:   "a server that fails leaves the goal as it was and says to try again",
			config: online, linked: true,
			prepare: func(s *teenServer) { s.put = http.StatusServiceUnavailable },
			steps: func(t *testing.T, a *agent, s *teenServer) {
				if rec := setGoal(t, a, `{"weekly_goal": 4}`); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "Try again") {
					t.Errorf("PUT = %d %s, want 503 saying to try again", rec.Code, rec.Body)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTeenServer(t)
			if tt.prepare != nil {
				s.set(tt.prepare)
			}
			a := bdAgent(t, tt.config, tt.linked, s.srv.URL)
			if tt.down {
				s.srv.Close()
			}
			tt.steps(t, a, s)
		})
	}
}

// TestSummaryFresh checks how long an answer serves the page: less than
// buildDaysFresh, by both clocks.
func TestSummaryFresh(t *testing.T) {
	at := time.Now()
	tests := []struct {
		name string
		now  time.Time
		want bool
	}{
		{name: "an answer got 10 seconds ago is fresh", now: at.Add(10 * time.Second), want: true},
		{name: "an answer got 60 seconds ago isn't", now: at.Add(buildDaysFresh), want: false},
		{name: "an answer the wall clock dates after now isn't", now: at.Round(0).Add(-time.Second), want: false},
		{name: "an answer the wall clock dates an hour ago isn't", now: at.Round(0).Add(time.Hour), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := summaryFresh(at, tt.now); got != tt.want {
				t.Errorf("summaryFresh = %v, want %v", got, tt.want)
			}
		})
	}
}
