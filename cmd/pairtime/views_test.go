// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// Tests for `pairtime today`, `recap` and `statusline` against a fake
// agent: an HTTP server behind the real agent's Guard and RequireToken,
// found through port.json in a PAIRTIME_HOME of its own. It serves days
// and sessions built from the SM 6.2 and 6.4 timeline fixtures, whose
// totals are the oracle, or made up to check one rule.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"maps"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/localhttp"
	"github.com/some-du6e/pairtime/internal/coach/text"
	"github.com/some-du6e/pairtime/internal/jsonschema"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/params"
)

// fakeAgent answers GET /v1/days/{date} and GET /v1/sessions/last as the
// agent would.
type fakeAgent struct {
	// days answers /v1/days/{date} by date, "today" included, and a date
	// it doesn't hold is an empty day. With days nil the route is missing,
	// as on an agent older than it.
	days map[string]dayReply
	// session answers /v1/sessions/last. With it nil the route is missing.
	session *sessionReply
	// hang holds every answer until the client gives up.
	hang bool
}

// start serves f on 127.0.0.1 and writes its port.json into a new
// PAIRTIME_HOME. It returns the paths the agent was asked for.
func (f fakeAgent) start(t *testing.T) func() []string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(home.EnvHome, dir)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := localhttp.Port(ln)
	token := localhttp.NewToken()
	stop := make(chan struct{})
	var mu sync.Mutex
	var asked []string
	answer := func(w http.ResponseWriter, r *http.Request, v any) {
		mu.Lock()
		asked = append(asked, r.URL.Path)
		mu.Unlock()
		if f.hang {
			select {
			case <-r.Context().Done():
			case <-stop:
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
	mux := http.NewServeMux()
	if f.days != nil {
		mux.HandleFunc("GET /v1/days/{date}", func(w http.ResponseWriter, r *http.Request) {
			d, ok := f.days[r.PathValue("date")]
			if !ok {
				d = dayReply{Date: r.PathValue("date"), Projects: []dayProject{}}
			}
			answer(w, r, d)
		})
	}
	if f.session != nil {
		mux.HandleFunc("GET /v1/sessions/last", func(w http.ResponseWriter, r *http.Request) { answer(w, r, f.session) })
	}
	srv := httptest.NewUnstartedServer(localhttp.Guard(port, localhttp.RequireToken(token, mux)))
	srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(stop) })
	writePortFile(t, dir, localhttp.PortFile{Port: port, Token: token, PID: os.Getpid()})
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(asked)
	}
}

func writePortFile(t *testing.T, dir string, pf localhttp.PortFile) {
	t.Helper()
	if err := localhttp.WritePortFile(home.Dirs{Data: dir}.PortFile(), pf); err != nil {
		t.Fatal(err)
	}
}

func runCmd(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// setZone makes the views give times of day in zone for the rest of the
// test.
func setZone(t *testing.T, zone *time.Location) {
	t.Helper()
	old := viewZone
	viewZone = func() *time.Location { return zone }
	t.Cleanup(func() { viewZone = old })
}

// fixtureExpected is the part of a timeline fixture's expected.json the
// store is built from (testdata/timelines/README.md).
type fixtureExpected struct {
	UTCOffset string            `json:"utc_offset"`
	Evidence  event.Evidence    `json:"evidence"`
	Projects  map[string]string `json:"projects"`
	Rows      []struct {
		Start         time.Time                           `json:"start"`
		Minutes       int                                 `json:"minutes"`
		Bucket        bool                                `json:"bucket"`
		CreditedS     float64                             `json:"credited_s"`
		Credit        map[string]map[event.Reason]float64 `json:"credit"`
		Uncredited    map[event.Reason]float64            `json:"uncredited"`
		AgentRunningS float64                             `json:"agent_running_s"`
	} `json:"rows"`
	Totals struct {
		CreditedS        float64                  `json:"credited_s"`
		CreditedByReason map[event.Reason]float64 `json:"credited_by_reason"`
		Uncredited       map[event.Reason]float64 `json:"uncredited"`
		AgentOnlyS       float64                  `json:"agent_only_s"`
	} `json:"totals"`
}

// fixtureMinute is one minute bucket of a fixture.
type fixtureMinute struct {
	start      time.Time
	credit     map[event.Reason]float64
	credited   float64
	uncredited map[event.Reason]float64
	running    float64
}

// fixtureStore is what an agent holds after a fixture's records: the
// fixture's minute buckets, which it rolls up into days and work sessions
// as WP-AG4's stores do (SM 5.3).
type fixtureStore struct {
	exp     fixtureExpected
	zone    *time.Location
	project string
	name    string
	harness event.Harness
	minutes []fixtureMinute
}

// loadFixture builds the store for testdata/timelines/<name> and checks
// that its minutes add up to the fixture's totals, so what the views print
// can be held to them.
func loadFixture(t *testing.T, name string) *fixtureStore {
	t.Helper()
	dir := filepath.Join("..", "..", "testdata", "timelines", name)
	b, err := os.ReadFile(filepath.Join(dir, "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := &fixtureStore{harness: fixtureHarness(t, filepath.Join(dir, "events.jsonl"))}
	if err := json.Unmarshal(b, &s.exp); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	ref, err := time.Parse("-07:00", s.exp.UTCOffset)
	if err != nil {
		t.Fatalf("%s: utc_offset: %v", name, err)
	}
	_, offset := ref.Zone()
	s.zone = time.FixedZone(s.exp.UTCOffset, offset)
	if len(s.exp.Projects) != 1 {
		t.Fatalf("%s: the store takes a fixture with one project, got %v", name, s.exp.Projects)
	}
	for k, v := range s.exp.Projects {
		s.project, s.name = k, v
	}
	for _, r := range s.exp.Rows {
		if !r.Bucket {
			continue // minutes with nothing credited and no agent running have no bucket (SM 5.1)
		}
		n := max(r.Minutes, 1)
		// A row of several minutes is the same minute repeated, so each
		// gets an equal part.
		part := func(v float64) float64 { return v / float64(n) }
		sum := 0.0
		for i := range n {
			m := fixtureMinute{
				start:      r.Start.Add(time.Duration(i) * time.Minute),
				credit:     map[event.Reason]float64{},
				uncredited: map[event.Reason]float64{},
				running:    part(r.AgentRunningS),
			}
			for p, byReason := range r.Credit {
				if p != s.project {
					t.Fatalf("%s: credit for project %q", name, p)
				}
				for reason, sec := range byReason {
					m.credit[reason] = part(sec)
					m.credited += part(sec)
				}
			}
			for reason, sec := range r.Uncredited {
				m.uncredited[reason] = part(sec)
			}
			sum += m.credited
			s.minutes = append(s.minutes, m)
		}
		if math.Abs(sum-r.CreditedS) > 1e-9 {
			t.Fatalf("%s: the row at %s credits %v s by reason but says %v s", name, r.Start, sum, r.CreditedS)
		}
	}
	slices.SortFunc(s.minutes, func(a, b fixtureMinute) int { return a.start.Compare(b.start) })

	all := s.tallyOf(s.minutes)
	byReason := map[event.Reason]float64{}
	for _, m := range s.minutes {
		for reason, sec := range m.credit {
			byReason[reason] += sec
		}
	}
	tot := s.exp.Totals
	checks := []struct {
		what      string
		got, want any
	}{
		{"credited_s", all.CreditedS, tot.CreditedS},
		{"agent_only_s", all.AgentOnlyS, tot.AgentOnlyS},
		{"credited_by_reason", byReason, tot.CreditedByReason},
		{"uncredited", all.Uncredited, tot.Uncredited},
	}
	for _, c := range checks {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Fatalf("%s: the store's %s is %v, but the fixture's totals say %v", name, c.what, c.got, c.want)
		}
	}
	return s
}

// fixtureHarness returns the one harness behind a fixture's agent events.
func fixtureHarness(t *testing.T, path string) event.Harness {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	seen := map[event.Harness]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var e struct {
			Harness event.Harness `json:"harness"`
		}
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if e.Harness != event.HarnessNone {
			seen[e.Harness] = true
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 {
		t.Fatalf("%s: want the events of one harness, got %v", path, seen)
	}
	for h := range seen {
		return h
	}
	return ""
}

// tallyOf rolls minutes up into what the agent would answer for them.
func (s *fixtureStore) tallyOf(ms []fixtureMinute) tally {
	t := tally{
		CreditedByKind:     map[event.Activity]float64{},
		CreditedByEvidence: map[event.Evidence]float64{},
		AgentOnlyByHarness: map[event.Harness]float64{s.harness: 0},
		Uncredited:         map[event.Reason]float64{},
	}
	for _, m := range ms {
		for reason, sec := range m.credit {
			t.CreditedS += sec
			t.CreditedByKind[reason.Activity()] += sec
			t.CreditedByEvidence[s.exp.Evidence] += sec
		}
		// A minute's agent-only seconds are those its turns ran that
		// nothing credited. In these fixtures a minute's credit falls
		// inside its turn wherever both are, so that is the running
		// seconds less the credited, which loadFixture checks against the
		// fixture's agent_only_s.
		only := max(0, m.running-m.credited)
		t.AgentOnlyS += only
		t.AgentOnlyByHarness[s.harness] += only
		for reason, sec := range m.uncredited {
			t.Uncredited[reason] += sec
		}
	}
	return t
}

// days rolls the minutes up into local days in the fixture's zone.
func (s *fixtureStore) days() map[string]dayReply {
	byDay := map[string][]fixtureMinute{}
	for _, m := range s.minutes {
		d := m.start.In(s.zone).Format(time.DateOnly)
		byDay[d] = append(byDay[d], m)
	}
	out := map[string]dayReply{}
	for d, ms := range byDay {
		out[d] = dayReply{Date: d, Projects: []dayProject{{Project: s.project, Name: s.name, tally: s.tallyOf(ms)}}}
	}
	return out
}

// lastSession is the fixture's last work session: a run of credited
// minutes split where one ends more than SESSION_GAP_S before the next
// starts (SM 5.3).
func (s *fixtureStore) lastSession() workSession {
	tables := params.Tables()
	gap := time.Duration(tables[len(tables)-1].Int(params.SESSION_GAP_S)) * time.Second
	var start, end time.Time
	for _, m := range s.minutes {
		if m.credited <= 0 {
			continue
		}
		if start.IsZero() || m.start.Sub(end) > gap {
			start = m.start
		}
		end = m.start.Add(time.Minute)
	}
	var in []fixtureMinute
	for _, m := range s.minutes {
		if !m.start.Before(start) && m.start.Before(end) {
			in = append(in, m)
		}
	}
	return workSession{Project: s.project, Name: s.name, Start: start, End: end, tally: s.tallyOf(in)}
}

const today62 = `Tuesday, November 24

remind-bot: you 4m 25s · Cursor 20s
  Your time                   4m 25s
    editing by hand               2m
    writing prompts               1m
    reviewing and deciding       50s
    watching Cursor work         20s
    reading Cursor's answers     15s
  Cursor, working on its own     20s
  Away                           15s
`

const today64Friday = `Friday, November 20

pixel-forge: you 6m 30s · Claude 16m 20s
  Your time                    6m 30s
    watching Claude work       3m 40s
    writing prompts            2m 50s
  Claude, working on its own  16m 20s
  Away                        16m 20s
  Here, between steps             10s
`

const today64Saturday = `Saturday, November 21

pixel-forge: you 7m · Claude 6h 10m
  Your time                       7m
    reading Claude's answers  4m 19s
    writing prompts           2m 41s
  Claude, working on its own  6h 10m
  Away                        6h 10m
`

const recap62 = `Your last session: remind-bot
Tuesday, November 24, 07:40 to 07:45

  Your time                   4m 25s
    editing by hand               2m
    writing prompts               1m
    reviewing and deciding       50s
    watching Cursor work         20s
    reading Cursor's answers     15s
  Cursor, working on its own     20s
  Away                           15s

How pairtime saw your time
  100%  level B: pairtime saw you at the computer, not your typing
`

const recap64 = `Your last session: pixel-forge
Saturday, November 21, 07:30 to 07:37

  Your time                       7m
    reading Claude's answers  4m 19s
    writing prompts           2m 41s

How pairtime saw your time
  100%  level A: pairtime saw your typing
`

func TestTodayFromFixtures(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
		date    string
		want    string
	}{
		{"SM 6.2: your 265 s by kind, Cursor's 20 s on its own, and 15 s away", "6.2", "2026-11-24", today62},
		{"SM 6.4 on Friday: the /goal prompt and the watching, then Claude on its own until midnight", "6.4", "2026-11-20", today64Friday},
		{"SM 6.4 on Saturday: Claude's night on its own while you were away, then your reading and the follow-up", "6.4", "2026-11-21", today64Saturday},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := loadFixture(t, tc.fixture)
			asked := fakeAgent{days: s.days()}.start(t)
			code, out, errOut := runCmd("today", "--date", tc.date)
			if code != 0 || errOut != "" {
				t.Fatalf("exit %d, stderr %q", code, errOut)
			}
			if out != tc.want {
				t.Errorf("got\n%s\nwant\n%s", out, tc.want)
			}
			if got, want := asked(), []string{"/v1/days/" + tc.date}; !slices.Equal(got, want) {
				t.Errorf("asked the agent for %q, want %q", got, want)
			}
		})
	}
}

func TestRecapFromFixtures(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
		want    string
	}{
		{"SM 6.2: one session, its time by kind, Cursor's time on its own and level B", "6.2", recap62},
		{"SM 6.4: the morning session after the night, all of it level A, with no agent time in it", "6.4", recap64},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := loadFixture(t, tc.fixture)
			setZone(t, s.zone)
			last := s.lastSession()
			asked := fakeAgent{session: &sessionReply{Session: &last}}.start(t)
			code, out, errOut := runCmd("recap")
			if code != 0 || errOut != "" {
				t.Fatalf("exit %d, stderr %q", code, errOut)
			}
			if out != tc.want {
				t.Errorf("got\n%s\nwant\n%s", out, tc.want)
			}
			if got, want := asked(), []string{"/v1/sessions/last"}; !slices.Equal(got, want) {
				t.Errorf("asked the agent for %q, want %q", got, want)
			}
		})
	}
}

func TestStatuslineFromFixtures(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
		date    string
		want    string
	}{
		{"SM 6.2's day is your time and Cursor's on one line", "6.2", "2026-11-24", "you 4m 25s · Cursor 20s\n"},
		{"SM 6.4's Saturday is your morning and Claude's night", "6.4", "2026-11-21", "you 7m · Claude 6h 10m\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := loadFixture(t, tc.fixture)
			days := s.days()
			days["today"] = days[tc.date]
			asked := fakeAgent{days: days}.start(t)
			code, out, errOut := runCmd("statusline", "--timeout", "5s")
			if code != 0 || errOut != "" {
				t.Fatalf("exit %d, stderr %q", code, errOut)
			}
			if out != tc.want {
				t.Errorf("got %q, want %q", out, tc.want)
			}
			if got, want := asked(), []string{"/v1/days/today"}; !slices.Equal(got, want) {
				t.Errorf("asked the agent for %q, want %q", got, want)
			}
		})
	}
}

func loadTodaySchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "schema", "cli-today.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := jsonschema.Parse(b)
	if err != nil {
		t.Fatalf("cli-today.schema.json: %v", err)
	}
	return s
}

func checkSchema(t *testing.T, s *jsonschema.Schema, out string) {
	t.Helper()
	errs, err := s.ValidateJSON([]byte(out))
	if err != nil {
		t.Fatalf("today --json printed what isn't JSON: %v\n%s", err, out)
	}
	for _, e := range errs {
		t.Errorf("today --json breaks cli-today.schema.json: %s", e)
	}
}

func TestTodayJSONMatchesBucketTotals(t *testing.T) {
	schema := loadTodaySchema(t)
	for _, fixture := range []string{"6.2", "6.4"} {
		t.Run("SM "+fixture+": today --json fits its schema, and over the fixture's days it adds up to the bucket totals", func(t *testing.T) {
			s := loadFixture(t, fixture)
			days := s.days()
			fakeAgent{days: days}.start(t)
			var you, agents float64
			kinds := map[event.Activity]float64{}
			other := map[event.Reason]float64{}
			for _, d := range slices.Sorted(maps.Keys(days)) {
				code, out, errOut := runCmd("today", "--json", "--date", d)
				if code != 0 || errOut != "" {
					t.Fatalf("%s: exit %d, stderr %q", d, code, errOut)
				}
				checkSchema(t, schema, out)
				var got todayJSON
				if err := json.Unmarshal([]byte(out), &got); err != nil {
					t.Fatal(err)
				}
				if got.Date != d || got.Calibrating || len(got.Projects) != 1 {
					t.Fatalf("%s: got date %q, calibrating %v and %d projects", d, got.Date, got.Calibrating, len(got.Projects))
				}
				p := got.Projects[0]
				if p.Project != s.project || p.Name != s.name {
					t.Errorf("%s: project %q named %q, want %q named %q", d, p.Project, p.Name, s.project, s.name)
				}
				you += p.You.S
				levelSum, shares := 0.0, 0
				for _, l := range p.You.Levels {
					levelSum += l.S
					shares += l.Share
					if l.Level != s.exp.Evidence {
						t.Errorf("%s: level %s, but every credited second of the fixture is level %s", d, l.Level, s.exp.Evidence)
					}
				}
				if levelSum != p.You.S || (p.You.S > 0 && shares != 100) {
					t.Errorf("%s: levels add up to %v s and %d%%, want %v s and 100%%", d, levelSum, shares, p.You.S)
				}
				for _, k := range p.You.Kinds {
					kinds[k.Kind] += k.S
				}
				for _, a := range p.Agents {
					if a.Harness != s.harness || a.Name != text.Agent(s.harness) {
						t.Errorf("%s: agent %s named %q, want %s", d, a.Harness, a.Name, s.harness)
					}
					agents += a.S
				}
				for _, o := range p.Other {
					other[o.Reason] += o.S
				}
			}
			tot := s.exp.Totals
			wantKinds := map[event.Activity]float64{}
			for reason, sec := range tot.CreditedByReason {
				wantKinds[reason.Activity()] += sec
			}
			wantOther := map[event.Reason]float64{}
			for reason, sec := range tot.Uncredited {
				if !text.IsAgentTime(reason) {
					wantOther[reason] += sec
				}
			}
			checks := []struct {
				what      string
				got, want any
			}{
				{"your time against credited_s", you, tot.CreditedS},
				{"the agents' time against agent_only_s", agents, tot.AgentOnlyS},
				{"your time by kind against credited_by_reason", kinds, wantKinds},
				{"other time against uncredited without the agent's time", other, wantOther},
			}
			for _, c := range checks {
				if !reflect.DeepEqual(c.got, c.want) {
					t.Errorf("%s: got %v, want %v", c.what, c.got, c.want)
				}
			}
		})
	}
}

func TestRecapJSONMatchesTheSession(t *testing.T) {
	for _, fixture := range []string{"6.2", "6.4"} {
		t.Run("SM "+fixture+": recap --json gives the last session's span and numbers", func(t *testing.T) {
			s := loadFixture(t, fixture)
			last := s.lastSession()
			fakeAgent{session: &sessionReply{Session: &last}}.start(t)
			code, out, errOut := runCmd("recap", "--json")
			if code != 0 || errOut != "" {
				t.Fatalf("exit %d, stderr %q", code, errOut)
			}
			var got recapJSON
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatal(err)
			}
			if got.V != 1 || got.Calibrating || got.Session == nil || got.Session.You == nil {
				t.Fatalf("got %s", out)
			}
			agents := 0.0
			for _, a := range got.Session.Agents {
				agents += a.S
			}
			if !got.Session.Start.Equal(last.Start) || !got.Session.End.Equal(last.End) ||
				got.Session.You.S != last.CreditedS || agents != last.AgentOnlyS {
				t.Errorf("got %v to %v, %v s yours and %v s the agent's; want %v to %v, %v s and %v s",
					got.Session.Start, got.Session.End, got.Session.You.S, agents, last.Start, last.End, last.CreditedS, last.AgentOnlyS)
			}
		})
	}
}

func day(projects ...dayProject) dayReply {
	return dayReply{Date: "2026-11-24", Projects: projects}
}

// mixedDay has two projects, the one with less of your time first, three
// agents, level-C and pending time, and the no-progress half.
var mixedDay = day(
	dayProject{Project: "p_aa01", Name: "remind-bot", tally: tally{
		CreditedS: 600, CreditedByKind: map[event.Activity]float64{event.ActivityTesting: 600},
		CreditedByEvidence: map[event.Evidence]float64{event.EvidenceB: 600},
		AgentOnlyS:         300, AgentOnlyByHarness: map[event.Harness]float64{event.HarnessCursor: 300},
		Uncredited: map[event.Reason]float64{event.ReasonAbsent: 30, event.ReasonAgentOnly: 300},
	}},
	dayProject{Project: "p_bb02", Name: "pixel-forge", tally: tally{
		CreditedS: 4200, PendingS: 300,
		CreditedByKind:     map[event.Activity]float64{event.ActivityPrompting: 2400, event.ActivityHandEdit: 1800},
		CreditedByEvidence: map[event.Evidence]float64{event.EvidenceA: 3000, event.EvidenceC: 1200},
		AgentOnlyS:         2400, AgentOnlyByHarness: map[event.Harness]float64{event.HarnessClaudeCode: 1800, event.HarnessCodex: 600},
		Uncredited: map[event.Reason]float64{event.ReasonOtherFocus: 120, event.ReasonNoProgress: 900, event.ReasonAgentOnly: 2400},
	}},
)

const todayMixed = `Tuesday, November 24

pixel-forge: you 1h 10m · Claude 30m · Codex 10m
  Your time                                 1h 10m
    writing prompts                            40m
    editing by hand                            30m
    20m of it is level C: it counts only on your own dashboard
    5m of it may still change
  Claude, working on its own                   30m
  Codex, working on its own                    10m
  On hold: these come back when you fix it     15m
  In other apps                                 2m

remind-bot: you 10m · Cursor 5m
  Your time                   10m
    testing                   10m
  Cursor, working on its own   5m
  Away                        30s
`

// shadowDay is a day while display.shadow is on.
var shadowDay = dayReply{Date: "2026-11-24", Shadow: true, Projects: []dayProject{{Project: "p_9f2c", Name: "remind-bot", tally: tally{
	CreditedS: 4320, CreditedByKind: map[event.Activity]float64{event.ActivityPrompting: 4320},
	CreditedByEvidence: map[event.Evidence]float64{event.EvidenceB: 4320},
	AgentOnlyS:         9600, AgentOnlyByHarness: map[event.Harness]float64{event.HarnessClaudeCode: 9600},
	Uncredited: map[event.Reason]float64{event.ReasonAbsent: 600, event.ReasonAgentOnly: 9000, event.ReasonAutomated: 600},
}}}}

const todayShadow = `Tuesday, November 24

remind-bot: you calibrating · Claude 2h 40m
  Your time                   calibrating
  Claude, working on its own       2h 40m
  Away                                10m

Your time shows as calibrating while pairtime checks how it counts.
`

func TestTodayRules(t *testing.T) {
	empty := day()
	cases := []struct {
		name string
		days map[string]dayReply
		args []string
		want string
	}{
		{"projects go the most of your time first; level C, pending time and on-hold time are labeled", map[string]dayReply{"today": mixedDay}, nil, todayMixed},
		{"with display.shadow on, your time is calibrating and the agent's time and other time still show", map[string]dayReply{"today": shadowDay}, nil, todayShadow},
		{"an empty today says nothing is tracked yet", map[string]dayReply{"today": empty}, nil, "Tuesday, November 24\n\nNothing tracked yet today.\n"},
		{"an empty day asked for by date says nothing was tracked", map[string]dayReply{"2026-11-24": empty}, []string{"--date", "2026-11-24"}, "Tuesday, November 24\n\nNothing tracked this day.\n"},
	}
	schema := loadTodaySchema(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakeAgent{days: tc.days}.start(t)
			code, out, errOut := runCmd(append([]string{"today"}, tc.args...)...)
			if code != 0 || errOut != "" {
				t.Fatalf("exit %d, stderr %q", code, errOut)
			}
			if out != tc.want {
				t.Errorf("got\n%s\nwant\n%s", out, tc.want)
			}
			code, out, errOut = runCmd(append([]string{"today", "--json"}, tc.args...)...)
			if code != 0 || errOut != "" {
				t.Fatalf("--json: exit %d, stderr %q", code, errOut)
			}
			checkSchema(t, schema, out)
		})
	}
}

func TestTodayJSONWhileCalibrating(t *testing.T) {
	fakeAgent{days: map[string]dayReply{"today": shadowDay}}.start(t)
	code, out, _ := runCmd("today", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	cases := []struct {
		name  string
		check func(raw map[string]any, got todayJSON) bool
	}{
		{"the day says it is calibrating", func(_ map[string]any, got todayJSON) bool { return got.Calibrating }},
		{"no project has your time", func(raw map[string]any, _ todayJSON) bool {
			for _, p := range raw["projects"].([]any) {
				if _, ok := p.(map[string]any)["you"]; ok {
					return false
				}
			}
			return true
		}},
		{"the credited seconds appear nowhere in the output", func(map[string]any, todayJSON) bool { return !strings.Contains(out, "4320") }},
		{"the agent's time still shows", func(_ map[string]any, got todayJSON) bool {
			return len(got.Projects) == 1 && len(got.Projects[0].Agents) == 1 && got.Projects[0].Agents[0].S == 9600
		}},
		{"other time still shows, without X.agent_only and X.automated", func(_ map[string]any, got todayJSON) bool {
			o := got.Projects[0].Other
			return len(o) == 1 && o[0].Reason == event.ReasonAbsent && o[0].Words == "away"
		}},
	}
	var raw map[string]any
	var got todayJSON
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.check(raw, got) {
				t.Errorf("not so in\n%s", out)
			}
		})
	}
}

func TestTodaySchemaRejects(t *testing.T) {
	schema := loadTodaySchema(t)
	s := loadFixture(t, "6.2")
	fakeAgent{days: s.days()}.start(t)
	_, good, _ := runCmd("today", "--json", "--date", "2026-11-24")
	cases := []struct {
		name  string
		edit  func(m map[string]any)
		valid bool
	}{
		{"the output as printed fits", func(map[string]any) {}, true},
		{"your time while calibrating doesn't fit", func(m map[string]any) { m["calibrating"] = true }, false},
		{"a project without your time while not calibrating doesn't fit", func(m map[string]any) { delete(project(m), "you") }, false},
		{"an unknown field doesn't fit", func(m map[string]any) { m["total_s"] = 265 }, false},
		{"the agent's time listed as other time doesn't fit", func(m map[string]any) {
			project(m)["other"] = []any{map[string]any{"reason": "X.agent_only", "words": "Cursor", "s": 20}}
		}, false},
		{"an agent with no harness doesn't fit", func(m map[string]any) {
			project(m)["agents"] = []any{map[string]any{"harness": "none", "name": "Cursor", "s": 20}}
		}, false},
		{"a share over 100 doesn't fit", func(m map[string]any) {
			project(m)["you"].(map[string]any)["levels"] = []any{map[string]any{"level": "B", "words": "x", "s": 265, "share": 101}}
		}, false},
		{"negative seconds don't fit", func(m map[string]any) { project(m)["you"].(map[string]any)["s"] = -1 }, false},
		{"a date with a time doesn't fit", func(m map[string]any) { m["date"] = "2026-11-24T00:00:00Z" }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var m map[string]any
			if err := json.Unmarshal([]byte(good), &m); err != nil {
				t.Fatal(err)
			}
			tc.edit(m)
			b, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			errs, err := schema.ValidateJSON(b)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(errs) == 0; got != tc.valid {
				t.Errorf("fits = %v, want %v; errors: %q", got, tc.valid, errs)
			}
		})
	}
}

func project(m map[string]any) map[string]any {
	return m["projects"].([]any)[0].(map[string]any)
}

// TestTodaySchemaMatchesGo fails when cli-today.schema.json and the Go
// types `today --json` prints drift apart: a property on one side only, a
// required list that isn't the fields Go always writes, or an enum that
// isn't the codes internal/coach/text describes.
func TestTodaySchemaMatchesGo(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "schema", "cli-today.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	defs := root["$defs"].(map[string]any)
	objects := []struct {
		def string
		typ reflect.Type
	}{
		{"", reflect.TypeFor[todayJSON]()},
		{"project", reflect.TypeFor[projectJSON]()},
		{"you", reflect.TypeFor[youJSON]()},
		{"kind", reflect.TypeFor[kindJSON]()},
		{"level", reflect.TypeFor[levelJSON]()},
		{"agent", reflect.TypeFor[agentJSON]()},
		{"other", reflect.TypeFor[otherJSON]()},
	}
	for _, o := range objects {
		t.Run("the schema's "+cmpName(o.def)+" has the fields of "+o.typ.Name(), func(t *testing.T) {
			s := root
			if o.def != "" {
				s = defs[o.def].(map[string]any)
			}
			var props, required []string
			for p := range s["properties"].(map[string]any) {
				props = append(props, p)
			}
			for _, r := range s["required"].([]any) {
				required = append(required, r.(string))
			}
			var fields, always []string
			for i := range o.typ.NumField() {
				name, opts, _ := strings.Cut(o.typ.Field(i).Tag.Get("json"), ",")
				fields = append(fields, name)
				if opts != "omitempty" {
					always = append(always, name)
				}
			}
			for _, l := range [][]string{props, required, fields, always} {
				slices.Sort(l)
			}
			if !slices.Equal(props, fields) {
				t.Errorf("properties %q, Go fields %q", props, fields)
			}
			if !slices.Equal(required, always) {
				t.Errorf("required %q, Go always writes %q", required, always)
			}
		})
	}
	c := text.English()
	enums := []struct {
		def, prop string
		want      []string
	}{
		{"kind", "kind", codesOf(c.Kinds)},
		{"level", "level", codesOf(c.Levels)},
		{"agent", "harness", codesOf(c.Agents)},
		{"other", "reason", codesOf(c.Other)},
	}
	for _, e := range enums {
		t.Run("the schema's "+e.def+"."+e.prop+" values are the codes internal/coach/text describes", func(t *testing.T) {
			var got []string
			for _, v := range defs[e.def].(map[string]any)["properties"].(map[string]any)[e.prop].(map[string]any)["enum"].([]any) {
				got = append(got, v.(string))
			}
			slices.Sort(got)
			slices.Sort(e.want)
			if !slices.Equal(got, e.want) {
				t.Errorf("enum %q, want %q", got, e.want)
			}
		})
	}
}

func cmpName(def string) string {
	if def == "" {
		return "root"
	}
	return def
}

func codesOf[K ~string, V any](m map[K]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, string(k))
	}
	return out
}

func TestRecapRules(t *testing.T) {
	newYork := time.FixedZone("-05:00", -5*3600)
	berlin := time.FixedZone("+01:00", 3600)
	s := loadFixture(t, "6.2")
	last62 := s.lastSession()
	cases := []struct {
		name  string
		zone  *time.Location
		reply sessionReply
		want  string
	}{
		{
			name: "before the first session, recap says so kindly",
			zone: newYork, reply: sessionReply{},
			want: "No work sessions yet. The first one starts when pairtime counts some of your time.\n",
		},
		{
			name: "a session that goes on is your session so far, since its start",
			zone: newYork,
			reply: sessionReply{Session: &workSession{
				Project: "p_bb02", Name: "pixel-forge", Open: true,
				Start: time.Date(2026, 11, 24, 12, 40, 0, 0, time.UTC), End: time.Date(2026, 11, 24, 13, 52, 0, 0, time.UTC),
				tally: tally{
					CreditedS: 4320, CreditedByKind: map[event.Activity]float64{event.ActivityPrompting: 4320},
					CreditedByEvidence: map[event.Evidence]float64{event.EvidenceB: 4320},
					AgentOnlyS:         600, AgentOnlyByHarness: map[event.Harness]float64{event.HarnessClaudeCode: 600},
					Uncredited: map[event.Reason]float64{event.ReasonAgentOnly: 600},
				},
			}},
			want: `Your session so far: pixel-forge
Tuesday, November 24, since 07:40

  Your time                   1h 12m
    writing prompts           1h 12m
  Claude, working on its own     10m

How pairtime saw your time
  100%  level B: pairtime saw you at the computer, not your typing
`,
		},
		{
			name: "with display.shadow on, your time and its levels are calibrating and the agent's time still shows",
			zone: newYork, reply: sessionReply{Shadow: true, Session: &last62},
			want: `Your last session: remind-bot
Tuesday, November 24, 07:40 to 07:45

  Your time                   calibrating
  Cursor, working on its own          20s
  Away                                15s

Your time shows as calibrating while pairtime checks how it counts.
`,
		},
		{
			name: "a session past midnight names both days, and a level under 1% shows as <1%",
			zone: berlin,
			reply: sessionReply{Session: &workSession{
				Project: "p_bb02", Name: "pixel-forge",
				Start: time.Date(2026, 11, 20, 22, 30, 0, 0, time.UTC), End: time.Date(2026, 11, 20, 23, 10, 0, 0, time.UTC),
				tally: tally{
					CreditedS: 2390, CreditedByKind: map[event.Activity]float64{event.ActivityPrompting: 2390},
					CreditedByEvidence: map[event.Evidence]float64{event.EvidenceA: 2385, event.EvidenceC: 5},
				},
			}},
			want: `Your last session: pixel-forge
Friday, November 20, 23:30 to Saturday, November 21, 00:10

  Your time          39m 50s
    writing prompts  39m 50s
    5s of it is level C: it counts only on your own dashboard

How pairtime saw your time
  100%  level A: pairtime saw your typing
   <1%  level C: this counts only on your own dashboard
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setZone(t, tc.zone)
			fakeAgent{session: &tc.reply}.start(t)
			code, out, errOut := runCmd("recap")
			if code != 0 || errOut != "" {
				t.Fatalf("exit %d, stderr %q", code, errOut)
			}
			if out != tc.want {
				t.Errorf("got\n%s\nwant\n%s", out, tc.want)
			}
		})
	}
}

func TestStatuslineRules(t *testing.T) {
	cases := []struct {
		name string
		day  dayReply
		want string
	}{
		{"the line sums projects and names the two busiest agents", mixedDay, "you 1h 20m · Claude 30m · Codex 10m\n"},
		{"with display.shadow on, your time is calibrating", shadowDay, "you calibrating · Claude 2h 40m\n"},
		{"a day with nothing yet is 0m", day(), "you 0m\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakeAgent{days: map[string]dayReply{"today": tc.day}}.start(t)
			code, out, errOut := runCmd("statusline", "--timeout", "5s")
			if code != 0 || errOut != "" {
				t.Fatalf("exit %d, stderr %q", code, errOut)
			}
			if out != tc.want {
				t.Errorf("got %q, want %q", out, tc.want)
			}
			if line := strings.TrimSuffix(out, "\n"); strings.Contains(line, "\n") || utf8.RuneCountInString(line) > 60 {
				t.Errorf("%q isn't one short line", line)
			}
			code, out, _ = runCmd("statusline", "--json", "--timeout", "5s")
			var got statusJSON
			if err := json.Unmarshal([]byte(out), &got); code != 0 || err != nil {
				t.Fatalf("--json: exit %d, %v: %q", code, err, out)
			}
			if got.Line+"\n" != tc.want || got.Calibrating != tc.day.Shadow || (got.YouS == nil) != tc.day.Shadow {
				t.Errorf("--json gave %s", out)
			}
		})
	}
}

// agentSituation is a way the agent can be missing or fail.
type agentSituation struct {
	name string
	set  func(t *testing.T)
	// want is what today and recap print on stderr.
	want string
}

func agentSituations() []agentSituation {
	return []agentSituation{
		{"no port.json, because the agent never ran", func(t *testing.T) { t.Setenv(home.EnvHome, t.TempDir()) }, text.Phrase(text.KeyAgentDown)},
		{"port.json says the agent stopped", func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv(home.EnvHome, dir)
			writePortFile(t, dir, localhttp.PortFile{Port: 7247, Token: localhttp.NewToken(), PID: 0})
		}, text.Phrase(text.KeyAgentDown)},
		{"nothing listens on the port in port.json", func(t *testing.T) {
			ln, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := localhttp.Port(ln)
			ln.Close()
			dir := t.TempDir()
			t.Setenv(home.EnvHome, dir)
			writePortFile(t, dir, localhttp.PortFile{Port: port, Token: localhttp.NewToken(), PID: os.Getpid()})
		}, text.Phrase(text.KeyAgentDown)},
		{"the agent is older than the route", func(t *testing.T) { fakeAgent{}.start(t) }, text.Phrase(text.KeyAgentOld)},
		{"the agent doesn't answer in time", func(t *testing.T) {
			fakeAgent{days: map[string]dayReply{}, session: &sessionReply{}, hang: true}.start(t)
		}, text.Phrase(text.KeyAgentSlow)},
		{"port.json's token isn't the agent's", func(t *testing.T) {
			fakeAgent{days: map[string]dayReply{}, session: &sessionReply{}}.start(t)
			dirs, err := home.Default()
			if err != nil {
				t.Fatal(err)
			}
			pf, err := localhttp.ReadPortFile(dirs.PortFile())
			if err != nil {
				t.Fatal(err)
			}
			pf.Token = localhttp.NewToken()
			writePortFile(t, dirs.Data, pf)
		}, text.Phrase(text.KeyAgentError, "error", "the agent answered 401 Unauthorized")},
	}
}

func TestViewsWhenTheAgentFails(t *testing.T) {
	old := viewTimeout
	viewTimeout = 200 * time.Millisecond
	t.Cleanup(func() { viewTimeout = old })
	for _, sit := range agentSituations() {
		for _, cmd := range []string{"today", "recap"} {
			t.Run(cmd+": "+sit.name+" exits 1 and says so kindly on stderr", func(t *testing.T) {
				sit.set(t)
				code, out, errOut := runCmd(cmd)
				if code != 1 || out != "" || errOut != sit.want+"\n" {
					t.Errorf("exit %d, stdout %q, stderr %q; want exit 1, no stdout and %q", code, out, errOut, sit.want)
				}
			})
		}
		t.Run("statusline: "+sit.name+" prints nothing, exits 0 and returns at once", func(t *testing.T) {
			sit.set(t)
			begin := time.Now()
			code, out, errOut := runCmd("statusline", "--timeout", "50ms")
			if code != 0 || out != "" || errOut != "" {
				t.Errorf("exit %d, stdout %q, stderr %q; want exit 0 and nothing", code, out, errOut)
			}
			if took := time.Since(begin); took > 2*time.Second {
				t.Errorf("took %v with a 50ms timeout", took)
			}
		})
	}
}

func TestStatuslineDefaultTimeout(t *testing.T) {
	cases := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"the default leaves room for the line in PLAN WP-CO0's 50 ms", func(t *testing.T) {
			if statuslineTimeout >= 50*time.Millisecond {
				t.Errorf("the default timeout is %v", statuslineTimeout)
			}
		}},
		{"with no --timeout, a hung agent gets nothing printed within a second", func(t *testing.T) {
			fakeAgent{days: map[string]dayReply{}, hang: true}.start(t)
			begin := time.Now()
			code, out, errOut := runCmd("statusline")
			if took := time.Since(begin); code != 0 || out != "" || errOut != "" || took > time.Second {
				t.Errorf("exit %d, stdout %q, stderr %q after %v", code, out, errOut, took)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.check)
	}
}

func TestViewUsage(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		wantCode   int
		wantStderr string
	}{
		{"today -h documents --date and the exit status", []string{"today", "-h"}, 0, "--date"},
		{"recap -h documents the exit status", []string{"recap", "-h"}, 0, "1  the agent isn't running"},
		{"statusline -h shows Claude Code's setting", []string{"statusline", "-h"}, 0, `"statusLine": {"type": "command", "command": "pairtime statusline"}`},
		{"today with a date that isn't one is a usage error", []string{"today", "--date", "11/24/2026"}, 2, "--date wants a day such as 2026-11-24"},
		{"today with an argument is a usage error", []string{"today", "yesterday"}, 2, "takes no arguments"},
		{"recap with an argument is a usage error", []string{"recap", "last"}, 2, "takes no arguments"},
		{"statusline with a timeout of 0 is a usage error", []string{"statusline", "--timeout", "0s"}, 2, "--timeout must be more than 0"},
		{"statusline with an unknown flag is a usage error", []string{"statusline", "--color"}, 2, "flag provided but not defined"},
		{"help lists today", []string{"help"}, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(home.EnvHome, t.TempDir())
			code, out, errOut := runCmd(tc.args...)
			if code != tc.wantCode || !strings.Contains(errOut, tc.wantStderr) {
				t.Errorf("exit %d, stderr %q; want exit %d and %q", code, errOut, tc.wantCode, tc.wantStderr)
			}
			if tc.args[0] == "help" {
				for _, name := range []string{"today", "recap", "statusline"} {
					if !strings.Contains(out, "  "+name+" ") {
						t.Errorf("help doesn't list %s:\n%s", name, out)
					}
				}
			}
		})
	}
}
