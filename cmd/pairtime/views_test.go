// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// Tests for `pairtime today`, `recap` and `statusline`. today and
// statusline read a PAIRTIME_HOME of their own: day rollups in days/ in the
// shape WP-AG4's scorer writes them, config.toml and port.json. recap asks
// a fake agent on that home's socket, which answers ipc.AskLastSession as
// the agent does. The days and sessions come from the SM 6.2 and 6.4
// timeline fixtures, whose totals are the oracle, or are made up to check
// one rule.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"math"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/doctor"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/internal/agent/ipc"
	"github.com/some-du6e/pairtime/internal/agent/localhttp"
	"github.com/some-du6e/pairtime/internal/coach/text"
	"github.com/some-du6e/pairtime/internal/jsonschema"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/params"
)

// fakeHome is a PAIRTIME_HOME for one test, on a path short enough for the
// agent's socket (shortHome).
type fakeHome struct {
	t   *testing.T
	dir string
}

func newHome(t *testing.T) fakeHome {
	t.Helper()
	dir := shortHome(t)
	t.Setenv(home.EnvHome, dir)
	return fakeHome{t: t, dir: dir}
}

func (h fakeHome) dirs() home.Dirs { return home.Dirs{Data: h.dir, Config: h.dir, Runtime: h.dir} }

// running writes the port.json of an agent that runs: this process.
func (h fakeHome) running() fakeHome {
	h.t.Helper()
	writePortFile(h.t, h.dir, localhttp.PortFile{Port: 7247, Token: localhttp.NewToken(), PID: os.Getpid()})
	return h
}

// shadow writes a config.toml that turns display.shadow on. Once
// pairtime's scoring reaches 1.0.0, Parse reads that line as off
// (config.Calibrating), so a test that needs it skips: nobody sees the
// views it checks any more, and WP-CA3 can delete those cases.
func (h fakeHome) shadow() fakeHome {
	h.t.Helper()
	if !config.Calibrating() {
		h.t.Skip("pairtime's scoring has reached 1.0.0, so display.shadow is off whatever config.toml says")
	}
	return h.writeFile("config.toml", []byte("[display]\nshadow = true\n"))
}

// writeDays writes each day's rollup into days/, as the agent's scorer
// does.
func (h fakeHome) writeDays(days ...dayFile) fakeHome {
	h.t.Helper()
	for _, d := range days {
		b, err := json.Marshal(d)
		if err != nil {
			h.t.Fatal(err)
		}
		h.writeFile(filepath.Join("days", d.Date+".json"), append(b, '\n'))
	}
	return h
}

// writeFile writes data to name, a path inside the home.
func (h fakeHome) writeFile(name string, data []byte) fakeHome {
	h.t.Helper()
	path := filepath.Join(h.dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		h.t.Fatal(err)
	}
	return h
}

// agent starts an agent on the home's socket that answers questions with
// answer, as the agent does through ipc.Options.Answer, and takes no
// messages. With answer nil it answers none, as an agent from before
// WP-AG4 does. It returns the questions it was asked.
func (h fakeHome) agent(answer func(q string) (json.RawMessage, error)) func() []string {
	h.t.Helper()
	ep, err := ipc.EndpointFor(h.dirs())
	if err != nil {
		h.t.Fatal(err)
	}
	var mu sync.Mutex
	var asked []string
	opts := ipc.Options{}
	if answer != nil {
		opts.Answer = func(q string) (json.RawMessage, error) {
			mu.Lock()
			asked = append(asked, q)
			mu.Unlock()
			return answer(q)
		}
	}
	srv, err := ipc.Listen(ep, opts)
	if err != nil {
		h.t.Fatalf("listening on the agent's socket: %v", err)
	}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(noMessages{}) }()
	h.t.Cleanup(func() {
		srv.Close()
		<-served
	})
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(asked)
	}
}

// noMessages is a Sink that takes no message: the views send none.
type noMessages struct{}

func (noMessages) Deliver(inbox.Delivery) error {
	return errors.New("the views send the agent no messages")
}

// sessionAgent starts an agent whose answer to AskLastSession is r.
func (h fakeHome) sessionAgent(r sessionReply) func() []string {
	h.t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		h.t.Fatal(err)
	}
	return h.agent(func(q string) (json.RawMessage, error) {
		if q != ipc.AskLastSession {
			return nil, ipc.ErrUnknownQuestion
		}
		return b, nil
	})
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

// setZone makes the views give times of day and days in zone for the rest
// of the test.
func setZone(t *testing.T, zone *time.Location) {
	t.Helper()
	old := viewZone
	viewZone = func() *time.Location { return zone }
	t.Cleanup(func() { viewZone = old })
}

// onDay makes today date, at noon in zone, for the rest of the test.
func onDay(t *testing.T, date string, zone *time.Location) {
	t.Helper()
	d, err := time.ParseInLocation(time.DateOnly, date, zone)
	if err != nil {
		t.Fatal(err)
	}
	setZone(t, zone)
	old := viewNow
	viewNow = func() time.Time { return d.Add(12 * time.Hour) }
	t.Cleanup(func() { viewNow = old })
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

// tallyOf rolls minutes up into what the agent would store for them.
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
		// A minute's agent-only seconds are those its project's turns ran
		// that the project didn't credit. These fixtures have one project,
		// and a minute's credit falls inside its turn wherever both are, so
		// that is the running seconds less the credited, which loadFixture
		// checks against the fixture's agent_only_s.
		only := max(0, m.running-m.credited)
		t.AgentOnlyS += only
		t.AgentOnlyByHarness[s.harness] += only
		for reason, sec := range m.uncredited {
			t.Uncredited[reason] += sec
		}
	}
	return t
}

// days rolls the minutes up into local days in the fixture's zone, each a
// rollup as WP-AG4's scorer writes it.
func (s *fixtureStore) days() map[string]dayFile {
	byDay := map[string][]fixtureMinute{}
	for _, m := range s.minutes {
		d := m.start.In(s.zone).Format(time.DateOnly)
		byDay[d] = append(byDay[d], m)
	}
	out := map[string]dayFile{}
	for d, ms := range byDay {
		start, err := time.ParseInLocation(time.DateOnly, d, s.zone)
		if err != nil {
			panic(err)
		}
		out[d] = dayFile{Date: d, Zone: s.zone.String(), Start: start.UTC(), End: start.AddDate(0, 0, 1).UTC(),
			Projects: []dayProject{{Project: s.project, Name: s.name, tally: s.tallyOf(ms)}}}
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

func dayFiles(m map[string]dayFile) []dayFile { return slices.Collect(maps.Values(m)) }

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
			newHome(t).running().writeDays(dayFiles(s.days())...)
			code, out, errOut := runCmd("today", "--date", tc.date)
			if code != 0 || errOut != "" {
				t.Fatalf("exit %d, stderr %q", code, errOut)
			}
			if out != tc.want {
				t.Errorf("got\n%s\nwant\n%s", out, tc.want)
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
			asked := newHome(t).sessionAgent(sessionReply{Session: &last})
			code, out, errOut := runCmd("recap")
			if code != 0 || errOut != "" {
				t.Fatalf("exit %d, stderr %q", code, errOut)
			}
			if out != tc.want {
				t.Errorf("got\n%s\nwant\n%s", out, tc.want)
			}
			if got, want := asked(), []string{ipc.AskLastSession}; !slices.Equal(got, want) {
				t.Errorf("asked the agent %q, want %q", got, want)
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
			onDay(t, tc.date, s.zone)
			newHome(t).running().writeDays(dayFiles(s.days())...)
			code, out, errOut := runCmd("statusline", "--timeout", "5s")
			if code != 0 || errOut != "" {
				t.Fatalf("exit %d, stderr %q", code, errOut)
			}
			if out != tc.want {
				t.Errorf("got %q, want %q", out, tc.want)
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
			newHome(t).running().writeDays(dayFiles(days)...)
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
		t.Run("SM "+fixture+": recap --json gives the last session's day, span and numbers", func(t *testing.T) {
			s := loadFixture(t, fixture)
			setZone(t, s.zone)
			last := s.lastSession()
			newHome(t).sessionAgent(sessionReply{Session: &last})
			code, out, errOut := runCmd("recap", "--json")
			if code != 0 || errOut != "" {
				t.Fatalf("exit %d, stderr %q", code, errOut)
			}
			var got recapJSON
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatal(err)
			}
			g := got.Session
			if got.V != 1 || got.Calibrating || g == nil || g.You == nil || g.Start == nil || g.End == nil {
				t.Fatalf("got %s", out)
			}
			agents := 0.0
			for _, a := range g.Agents {
				agents += a.S
			}
			wantDate := last.Start.In(s.zone).Format(time.DateOnly)
			if g.Date != wantDate || !g.Start.Equal(last.Start) || !g.End.Equal(last.End) || g.You.S != last.CreditedS || agents != last.AgentOnlyS {
				t.Errorf("got %s, %v to %v, %v s yours and %v s the agent's; want %s, %v to %v, %v s and %v s",
					g.Date, g.Start, g.End, g.You.S, agents, wantDate, last.Start, last.End, last.CreditedS, last.AgentOnlyS)
			}
		})
	}
}

func day(projects ...dayProject) dayFile {
	return dayFile{Date: "2026-11-24", Projects: projects}
}

// mixedDay has two projects whose order by your time disagrees with every
// other order: the one with less of your time has more agent time, the
// earlier name and the lower key, and comes first in the file. Its agents'
// and its other time's order by seconds isn't their codes' order either.
// It has level-C time, pending time, and X.noprog partly on hold.
var mixedDay = day(
	dayProject{Project: "p_aa01", Name: "pixel-forge", tally: tally{
		CreditedS: 600, CreditedByKind: map[event.Activity]float64{event.ActivityTesting: 600},
		CreditedByEvidence: map[event.Evidence]float64{event.EvidenceB: 600},
		AgentOnlyS:         2400, AgentOnlyByHarness: map[event.Harness]float64{event.HarnessCodex: 1800, event.HarnessClaudeCode: 600},
		Uncredited: map[event.Reason]float64{event.ReasonOtherFocus: 900, event.ReasonNoProgress: 300, event.ReasonAgentOnly: 2400},
		OnHoldS:    120,
	}},
	dayProject{Project: "p_bb02", Name: "remind-bot", tally: tally{
		CreditedS: 4200, PendingS: 300,
		CreditedByKind:     map[event.Activity]float64{event.ActivityPrompting: 2400, event.ActivityHandEdit: 1800},
		CreditedByEvidence: map[event.Evidence]float64{event.EvidenceA: 3000, event.EvidenceC: 1200},
		AgentOnlyS:         300, AgentOnlyByHarness: map[event.Harness]float64{event.HarnessCursor: 300},
		Uncredited: map[event.Reason]float64{event.ReasonAbsent: 30, event.ReasonAgentOnly: 300},
	}},
)

const todayMixed = `Tuesday, November 24

remind-bot: you 1h 10m, 20m level C · Cursor 5m
  Your time                   1h 10m
    writing prompts              40m
    editing by hand              30m
    20m of it is level C: it shows on your own dashboard, not on your profile, build days or most programs
    5m of it may still change
  Cursor, working on its own      5m
  Away                           30s

pixel-forge: you 10m · Codex 30m · Claude 10m
  Your time                                         10m
    testing                                         10m
  Codex, working on its own                         30m
  Claude, working on its own                        10m
  In other apps                                     15m
  Past two hours without progress, counted at half   3m
  On hold: these come back when you fix it           2m
`

// shadowDay is a day while display.shadow is on, with time of every sort:
// level C and pending, the agent's on its own and while nobody started it,
// away, a loop, the no-progress half and credit past the day cap. Its
// numbers appear nowhere else.
var shadowDay = day(dayProject{Project: "p_9f2c", Name: "remind-bot", tally: tally{
	CreditedS: 4320, PendingS: 77, CreditedByKind: map[event.Activity]float64{event.ActivityPrompting: 4320},
	CreditedByEvidence: map[event.Evidence]float64{event.EvidenceB: 4277, event.EvidenceC: 43},
	AgentOnlyS:         9600, AgentOnlyByHarness: map[event.Harness]float64{event.HarnessClaudeCode: 9600},
	Uncredited: map[event.Reason]float64{event.ReasonAbsent: 615, event.ReasonNoProgress: 135, event.ReasonRewriteLoop: 240,
		event.ReasonDayCap: 345, event.ReasonAgentOnly: 9000, event.ReasonAutomated: 600},
	OnHoldS: 135,
}})

const todayShadow = `Tuesday, November 24

remind-bot: you calibrating · Claude 2h 40m
  Your time                        calibrating
  Claude, working on its own            2h 40m
  Claude rewriting the same lines           4m

Your time shows as calibrating while pairtime checks how it counts.
`

// shadowTwoDay is a calibrating day of two projects. remind-bot has more of
// your time, less agent time, the later name and the lower key, and comes
// first in the file, so only the hidden credit would put it first.
var shadowTwoDay = day(
	dayProject{Project: "p_aa01", Name: "remind-bot", tally: tally{
		CreditedS: 4320, CreditedByKind: map[event.Activity]float64{event.ActivityPrompting: 4320},
		CreditedByEvidence: map[event.Evidence]float64{event.EvidenceB: 4320},
		AgentOnlyS:         300, AgentOnlyByHarness: map[event.Harness]float64{event.HarnessCursor: 300},
		Uncredited: map[event.Reason]float64{event.ReasonAgentOnly: 300, event.ReasonAbsent: 60},
	}},
	dayProject{Project: "p_bb02", Name: "pixel-forge", tally: tally{
		CreditedS: 60, CreditedByKind: map[event.Activity]float64{event.ActivityPrompting: 60},
		CreditedByEvidence: map[event.Evidence]float64{event.EvidenceB: 60},
		AgentOnlyS:         1800, AgentOnlyByHarness: map[event.Harness]float64{event.HarnessClaudeCode: 1800},
		Uncredited: map[event.Reason]float64{event.ReasonAgentOnly: 1800},
	}},
)

const todayShadowTwo = `Tuesday, November 24

pixel-forge: you calibrating · Claude 30m
  Your time                   calibrating
  Claude, working on its own          30m

remind-bot: you calibrating · Cursor 5m
  Your time                   calibrating
  Cursor, working on its own           5m

Your time shows as calibrating while pairtime checks how it counts.
`

func TestTodayRules(t *testing.T) {
	cases := []struct {
		name    string
		shadow  bool // config.toml turns display.shadow on
		stopped bool // the agent is stopped: there is no port.json
		days    []dayFile
		args    []string
		want    string
	}{
		{name: "projects go the most of your time first, which no other order would give; level C, pending and on-hold time are labeled",
			days: []dayFile{mixedDay}, want: todayMixed},
		{name: "with display.shadow on, your time is calibrating, the agent's time and its loops still show, and the other time doesn't",
			shadow: true, days: []dayFile{shadowDay}, want: todayShadow},
		{name: "with display.shadow on, your time doesn't decide the order, so the project with more of it, less agent time and the later name comes second",
			shadow: true, days: []dayFile{shadowTwoDay}, want: todayShadowTwo},
		{name: "an empty today says nothing is tracked yet", want: "Tuesday, November 24\n\nNothing tracked yet today.\n"},
		{name: "an empty day asked for by date says nothing was tracked", args: []string{"--date", "2026-11-24"},
			want: "Tuesday, November 24\n\nNothing tracked this day.\n"},
		{name: "an empty day asked for by date says so while the agent is stopped too", stopped: true, args: []string{"--date", "2026-11-24"},
			want: "Tuesday, November 24\n\nNothing tracked this day.\n"},
		{name: "today's stored time shows while the agent is stopped", stopped: true, days: []dayFile{mixedDay}, want: todayMixed},
	}
	schema := loadTodaySchema(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			onDay(t, "2026-11-24", time.UTC)
			h := newHome(t).writeDays(tc.days...)
			if !tc.stopped {
				h.running()
			}
			if tc.shadow {
				h.shadow()
			}
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

func TestTodayJSONOrder(t *testing.T) {
	cases := []struct {
		name   string
		shadow bool
		day    dayFile
		want   []string // project keys, in order
	}{
		{"the project with the most of your time comes first", false, mixedDay, []string{"p_bb02", "p_aa01"}},
		{"while calibrating, the project with the most agent time comes first, whatever its credit", true, shadowTwoDay, []string{"p_bb02", "p_aa01"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			onDay(t, "2026-11-24", time.UTC)
			h := newHome(t).running().writeDays(tc.day)
			if tc.shadow {
				h.shadow()
			}
			_, out, _ := runCmd("today", "--json")
			var got todayJSON
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			var keys []string
			for _, p := range got.Projects {
				keys = append(keys, p.Project)
			}
			if !slices.Equal(keys, tc.want) {
				t.Errorf("projects %q, want %q", keys, tc.want)
			}
		})
	}
}

func TestTodayJSONWhileCalibrating(t *testing.T) {
	onDay(t, "2026-11-24", time.UTC)
	newHome(t).running().shadow().writeDays(shadowDay)
	code, out, _ := runCmd("today", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var raw map[string]any
	var got todayJSON
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		check func() bool
	}{
		{"the day says it is calibrating", func() bool { return got.Calibrating }},
		{"no project has your time", func() bool {
			for _, p := range raw["projects"].([]any) {
				if _, ok := p.(map[string]any)["you"]; ok {
					return false
				}
			}
			return true
		}},
		{"your time, its pending part and its levels appear nowhere", func() bool {
			return !strings.Contains(out, "4320") && !strings.Contains(out, "4277") && !strings.Contains(out, `"pending_s"`) && !strings.Contains(out, `"levels"`)
		}},
		{"the agent's time still shows", func() bool {
			return len(got.Projects) == 1 && len(got.Projects[0].Agents) == 1 && got.Projects[0].Agents[0].S == 9600
		}},
		{"other time holds SM 4.2's loops alone", func() bool {
			o := got.Projects[0].Other
			return len(o) == 1 && o[0].Reason == event.ReasonRewriteLoop && o[0].Words == "Claude rewriting the same lines" && o[0].S == 240
		}},
		{"away, the no-progress half and the day cap appear nowhere, so the credit can't be worked out from them (D-shadow-scope)", func() bool {
			for _, s := range []string{"615", "135", "345", "X.absent", "X.noprog", "X.day_cap"} {
				if strings.Contains(out, s) {
					return false
				}
			}
			return true
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.check() {
				t.Errorf("not so in\n%s", out)
			}
		})
	}
}

func TestTodaySchemaRejects(t *testing.T) {
	schema := loadTodaySchema(t)
	s := loadFixture(t, "6.2")
	newHome(t).running().writeDays(dayFiles(s.days())...)
	_, good, _ := runCmd("today", "--json", "--date", "2026-11-24")
	calibrating := func(m map[string]any) {
		m["calibrating"] = true
		delete(project(m), "you")
	}
	cases := []struct {
		name  string
		edit  func(m map[string]any)
		valid bool
	}{
		{"the output as printed fits", func(map[string]any) {}, true},
		{"your time while calibrating doesn't fit", func(m map[string]any) { m["calibrating"] = true }, false},
		{"a project without your time while not calibrating doesn't fit", func(m map[string]any) { delete(project(m), "you") }, false},
		{"while calibrating, a project with only the agent's time fits", func(m map[string]any) {
			calibrating(m)
			project(m)["other"] = []any{}
		}, true},
		{"while calibrating, a loop fits", func(m map[string]any) {
			calibrating(m)
			project(m)["other"] = []any{map[string]any{"reason": "X.reroll", "words": "re-rolling the same prompt", "s": 30}}
		}, true},
		{"while calibrating, away time doesn't fit, since it gives the hidden credit away", func(m map[string]any) {
			calibrating(m)
			project(m)["other"] = []any{map[string]any{"reason": "X.absent", "words": "away", "s": 15}}
		}, false},
		{"while calibrating, credit past the day cap doesn't fit (D-shadow-scope)", func(m map[string]any) {
			calibrating(m)
			project(m)["other"] = []any{map[string]any{"reason": "X.day_cap", "words": "past 12 hours in one day", "s": 15}}
		}, false},
		{"while calibrating, the no-progress half doesn't fit", func(m map[string]any) {
			calibrating(m)
			project(m)["other"] = []any{map[string]any{"reason": "X.noprog", "words": "on hold: these come back when you fix it", "s": 15, "on_hold": true}}
		}, false},
		{"the on-hold part of X.noprog fits", func(m map[string]any) {
			project(m)["other"] = []any{map[string]any{"reason": "X.noprog", "words": "on hold: these come back when you fix it", "s": 15, "on_hold": true}}
		}, true},
		{"on hold on a reason other than X.noprog doesn't fit", func(m map[string]any) {
			project(m)["other"] = []any{map[string]any{"reason": "X.absent", "words": "away", "s": 15, "on_hold": true}}
		}, false},
		{"on_hold false doesn't fit, since it is left out instead", func(m map[string]any) {
			project(m)["other"] = []any{map[string]any{"reason": "X.noprog", "words": "x", "s": 15, "on_hold": false}}
		}, false},
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
	t.Run("the schema's loops while calibrating are pkg/event's dark-flow reasons", func(t *testing.T) {
		then := root["then"].(map[string]any)["properties"].(map[string]any)["projects"].(map[string]any)["items"].(map[string]any)
		var got []string
		for _, v := range then["properties"].(map[string]any)["other"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["reason"].(map[string]any)["enum"].([]any) {
			got = append(got, v.(string))
		}
		var want []string
		for _, r := range event.UncreditedReasons() {
			if r.DarkFlow() {
				want = append(want, string(r))
			}
		}
		if !slices.Equal(got, want) {
			t.Errorf("enum %q, want %q", got, want)
		}
	})
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
	open := workSession{
		Project: "p_bb02", Name: "pixel-forge", Open: true,
		Start: time.Date(2026, 11, 24, 12, 40, 0, 0, time.UTC), End: time.Date(2026, 11, 24, 13, 52, 0, 0, time.UTC),
		tally: tally{
			CreditedS: 4320, CreditedByKind: map[event.Activity]float64{event.ActivityPrompting: 4320},
			CreditedByEvidence: map[event.Evidence]float64{event.EvidenceB: 4320},
			AgentOnlyS:         600, AgentOnlyByHarness: map[event.Harness]float64{event.HarnessClaudeCode: 600},
			Uncredited: map[event.Reason]float64{event.ReasonAgentOnly: 600, event.ReasonOtherFocus: 300},
		},
	}
	const shadow62 = `Your last session: remind-bot
Tuesday, November 24

  Your time                   calibrating
  Cursor, working on its own          20s

Your time shows as calibrating while pairtime checks how it counts.
`
	cases := []struct {
		name   string
		zone   *time.Location
		shadow bool // config.toml turns display.shadow on
		reply  sessionReply
		want   string
	}{
		{
			name: "before the first session, recap says so kindly",
			zone: newYork, reply: sessionReply{},
			want: "No work sessions yet. The first one starts when pairtime counts some of your time.\n",
		},
		{
			name: "a session that goes on is your session so far, since its start",
			zone: newYork, reply: sessionReply{Session: &open},
			want: `Your session so far: pixel-forge
Tuesday, November 24, since 07:40

  Your time                   1h 12m
    writing prompts           1h 12m
  Claude, working on its own     10m
  In other apps                   5m

How pairtime saw your time
  100%  level B: pairtime saw you at the computer, not your typing
`,
		},
		{
			name: "with display.shadow on, your time is calibrating, the session is its day alone, and only the agent's time shows",
			zone: newYork, reply: sessionReply{Shadow: true, Session: &last62},
			want: shadow62,
		},
		{
			name: "with display.shadow just turned on in config.toml, before the agent says so, the recap is calibrating too",
			zone: newYork, shadow: true, reply: sessionReply{Session: &last62},
			want: shadow62,
		},
		{
			name: "a session that goes on while calibrating is its day and project alone, without saying it goes on (D-shadow-scope)",
			zone: newYork, reply: sessionReply{Shadow: true, Session: &open},
			want: `Your last session: pixel-forge
Tuesday, November 24

  Your time                   calibrating
  Claude, working on its own          10m

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
    5s of it is level C: it shows on your own dashboard, not on your profile, build days or most programs

How pairtime saw your time
  100%  level A: pairtime saw your typing
   <1%  level C: this shows on your own dashboard, not on your profile, build days or most programs
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setZone(t, tc.zone)
			h := newHome(t)
			if tc.shadow {
				h.shadow()
			}
			h.sessionAgent(tc.reply)
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

func TestRecapJSONWhileCalibrating(t *testing.T) {
	s := loadFixture(t, "6.2")
	setZone(t, s.zone)
	last := s.lastSession()
	newHome(t).sessionAgent(sessionReply{Shadow: true, Session: &last})
	code, out, errOut := runCmd("recap", "--json")
	if code != 0 || errOut != "" {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatal(err)
	}
	session, _ := raw["session"].(map[string]any)
	cases := []struct {
		name string
		ok   bool
	}{
		{"the recap says it is calibrating", raw["calibrating"] == true},
		{"the session has its day", session["date"] == "2026-11-24"},
		{"the session has no start or end, which would bound your time", session["start"] == nil && session["end"] == nil},
		{"whether the session goes on, which would say whether its last minute counted, is left out", session["open"] == nil},
		{"no time of day appears anywhere", !strings.Contains(out, "T12:")},
		{"your time is left out", session["you"] == nil},
		{"the agent's time still shows", len(session["agents"].([]any)) == 1},
		{"other time holds no away time", len(session["other"].([]any)) == 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Errorf("not so in\n%s", out)
			}
		})
	}
}

// shadowAnswer is the agent's answer to AskLastSession while display.shadow
// is on (D-shadow-scope): no credited or pending numbers, no credited-by
// maps, no uncredited codes but its own and the loops, no start, end or
// open, and the session's local date.
const shadowAnswer = `{"shadow": true, "session": {"project": "p_aa01", "name": "remind-bot", "date": "2026-11-24",
	"agent_only_s": 20, "agent_only_by_harness": {"cursor": 20}, "uncredited": {"X.agent_only": 20, "X.reroll": 30}}}`

const recapShadowAnswer = `Your last session: remind-bot
Tuesday, November 24

  Your time                   calibrating
  Cursor, working on its own          20s
  Re-rolling the same prompt          30s

Your time shows as calibrating while pairtime checks how it counts.
`

// TestRecapReadsWhatTheAgentLeavesOut holds recap to D-shadow-scope's
// shape: a number the agent leaves out reads as 0 and a map as empty.
func TestRecapReadsWhatTheAgentLeavesOut(t *testing.T) {
	cases := []struct {
		name   string
		answer string
		want   string
	}{
		{"the shadow answer is calibrating, with the agent's time, the loop and the session's date", shadowAnswer, recapShadowAnswer},
		{"a shadow answer that names no day prints none", strings.Replace(shadowAnswer, `"date": "2026-11-24",`, "", 1),
			strings.Replace(recapShadowAnswer, "Tuesday, November 24\n", "", 1)},
		{"an answer without its maps reads them as empty: your time, and no kinds, levels, agents or other time", `{"shadow": false, "session": {"project": "p_aa01", "name": "remind-bot",
			"date": "2026-11-23", "start": "2026-11-24T04:30:00Z", "end": "2026-11-24T04:40:00Z", "credited_s": 600}}`,
			"Your last session: remind-bot\nMonday, November 23, 23:30 to 23:40\n\n  Your time  10m\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setZone(t, time.FixedZone("-05:00", -5*3600))
			newHome(t).agent(func(string) (json.RawMessage, error) { return json.RawMessage(tc.answer), nil })
			code, out, errOut := runCmd("recap")
			if code != 0 || errOut != "" {
				t.Fatalf("exit %d, stderr %q", code, errOut)
			}
			if out != tc.want {
				t.Errorf("got\n%s\nwant\n%s", out, tc.want)
			}
		})
	}
	t.Run("--json of the shadow answer has its date, the agent's time and the loop, and nothing else", func(t *testing.T) {
		newHome(t).agent(func(string) (json.RawMessage, error) { return json.RawMessage(shadowAnswer), nil })
		_, out, _ := runCmd("recap", "--json")
		var raw map[string]any
		if err := json.Unmarshal([]byte(out), &raw); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		s := raw["session"].(map[string]any)
		var keys []string
		for k := range s {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		if want := []string{"agents", "date", "name", "other", "project"}; !slices.Equal(keys, want) || raw["calibrating"] != true {
			t.Errorf("session fields %q, want %q, in\n%s", keys, want, out)
		}
		if o := s["other"].([]any); len(o) != 1 || o[0].(map[string]any)["reason"] != "X.reroll" {
			t.Errorf("other is %v, want the loop alone", o)
		}
	})
}

func TestStatuslineRules(t *testing.T) {
	cases := []struct {
		name   string
		shadow bool
		day    *dayFile // nil: no rollup yet today
		want   string
		levels map[event.Evidence]float64 // --json's you_by_level
	}{
		{"the line sums projects, names the two busiest agents, and labels level C (D11)", false, &mixedDay,
			"you 1h 20m, 20m level C · Codex 30m · Claude 10m\n",
			map[event.Evidence]float64{event.EvidenceA: 3000, event.EvidenceB: 600, event.EvidenceC: 1200}},
		{"with display.shadow on, your time is calibrating, with no level", true, &shadowDay, "you calibrating · Claude 2h 40m\n", nil},
		{"before today's first counted minute the line is 0m", false, nil, "you 0m\n", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			onDay(t, "2026-11-24", time.UTC)
			h := newHome(t).running()
			if tc.shadow {
				h.shadow()
			}
			if tc.day != nil {
				h.writeDays(*tc.day)
			}
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
			if got.Line+"\n" != tc.want || got.Calibrating != tc.shadow || (got.YouS == nil) != tc.shadow || !maps.Equal(got.YouByLevel, tc.levels) {
				t.Errorf("--json gave %s", out)
			}
		})
	}
}

func TestTodayIsTheDayTheAgentWrites(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skip("no time zone data:", err)
	}
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no time zone data:", err)
	}
	// The agent started in Los Angeles, and counts its days there: its
	// 2026-11-24 runs from 08:00Z to 08:00Z the next day.
	start := time.Date(2026, 11, 24, 0, 0, 0, 0, la)
	written := dayFile{Date: "2026-11-24", Zone: "Local", Start: start.UTC(), End: start.AddDate(0, 0, 1).UTC(), Projects: mixedDay.Projects}
	cases := []struct {
		name string
		now  time.Time
		date string // the day today --json shows
		line string // what statusline prints
	}{
		{
			name: "after the computer's zone moves to New York, today and statusline read the day the agent goes on writing, whose bounds hold now",
			now:  time.Date(2026, 11, 25, 1, 30, 0, 0, ny), // 22:30 on the 24th in Los Angeles
			date: "2026-11-24", line: "you 1h 20m, 20m level C · Codex 30m · Claude 10m\n",
		},
		{
			name: "once the agent's day has ended, today and statusline take the day by the computer's zone until the agent writes the next",
			now:  time.Date(2026, 11, 25, 3, 30, 0, 0, ny), // 00:30 on the 25th in Los Angeles
			date: "2026-11-25", line: "you 0m\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setZone(t, ny)
			old := viewNow
			viewNow = func() time.Time { return tc.now }
			t.Cleanup(func() { viewNow = old })
			newHome(t).running().writeDays(written)
			code, out, errOut := runCmd("today", "--json")
			var got struct {
				Date string `json:"date"`
			}
			if err := json.Unmarshal([]byte(out), &got); code != 0 || errOut != "" || err != nil {
				t.Fatalf("today --json: exit %d, stderr %q, %v", code, errOut, err)
			}
			if got.Date != tc.date {
				t.Errorf("today shows %s, want %s", got.Date, tc.date)
			}
			code, out, errOut = runCmd("statusline", "--timeout", "5s")
			if code != 0 || errOut != "" || out != tc.line {
				t.Errorf("statusline: exit %d, stderr %q, got %q, want %q", code, errOut, out, tc.line)
			}
		})
	}
}

// TestTodayFocusUnknown checks DESIGN 5.9's teen-facing focus-unknown
// sentence in pairtime today: it ends today's view while the running
// agent's status says focus is unknown, and only then, in the words the
// dashboard's Today page uses.
func TestTodayFocusUnknown(t *testing.T) {
	// A slow runner still answers within this; one case shortens it.
	old := focusAskWait
	focusAskWait = 10 * time.Second
	t.Cleanup(func() { focusAskWait = old })
	stops := text.Phrase(text.KeyFocusUnknown)
	counts := text.Phrase(text.KeyFocusUnknownLevelC)
	login := text.Phrase(text.KeyGnomeNeedsLogin)
	unticked := text.Phrase(text.KeyGnomeUnticked)
	// status answers AskStatus with signals whose focus is focus, plus the
	// extra members of GET /v1/status's signals in extra.
	status := func(focus doctor.Signal, extra map[string]any) func(q string) (json.RawMessage, error) {
		signals := map[string]any{"idle": doctor.Signal{Via: "org.gnome.Mutter.IdleMonitor"}, "focus": focus, "session": "wayland"}
		maps.Copy(signals, extra)
		b, err := json.Marshal(map[string]any{"signals": signals})
		if err != nil {
			t.Fatal(err)
		}
		return func(q string) (json.RawMessage, error) {
			if q != ipc.AskStatus {
				return nil, ipc.ErrUnknownQuestion
			}
			return b, nil
		}
	}
	unknown := doctor.Signal{Unknown: "Wayland gives other programs no focus signal"}
	tracked := dayFile{Date: "2026-11-24", Zone: "UTC", Start: time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC),
		End: time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC), Projects: mixedDay.Projects}
	yesterday := tracked
	yesterday.Date, yesterday.Start, yesterday.End = "2026-11-23", tracked.Start.AddDate(0, 0, -1), tracked.Start
	cases := []struct {
		name   string
		answer func(q string) (json.RawMessage, error)
		days   []dayFile
		args   []string
		want   string   // what ends the view after a blank line, "" for nothing about focus
		asked  []string // the questions today asked
	}{
		{"while the agent says focus is unknown, today's view ends with the sentence, which stops at \"in front\" until WP-SC8 (DESIGN 5.9)",
			status(unknown, nil), []dayFile{tracked}, []string{"today"}, stops, []string{ipc.AskStatus}},
		{"a day with nothing tracked yet still says why, when focus is unknown",
			status(unknown, nil), nil, []string{"today"}, stops, []string{ipc.AskStatus}},
		{"once the agent says that time counts at level C, the sentence goes on to where it counts, as the Today page's does",
			status(unknown, map[string]any{"focus_level_c": true}), []dayFile{tracked}, []string{"today"}, counts, []string{ipc.AskStatus}},
		{"on GNOME before the next login, the line that turns the extension on follows",
			status(unknown, map[string]any{"gnome_extension": "needs_login"}), []dayFile{tracked}, []string{"today"}, stops + "\n" + login, []string{ipc.AskStatus}},
		{"on GNOME with the extension unticked at install, the line that brings it back follows",
			status(unknown, map[string]any{"gnome_extension": "unticked"}), []dayFile{tracked}, []string{"today"}, stops + "\n" + unticked, []string{ipc.AskStatus}},
		{"while the agent sees the window in front, today says nothing about focus",
			status(doctor.Signal{Via: "_NET_ACTIVE_WINDOW"}, nil), []dayFile{tracked}, []string{"today"}, "", []string{ipc.AskStatus}},
		{"an agent that can't answer status, as one older than it, leaves the sentence out and today still works",
			func(string) (json.RawMessage, error) { return nil, ipc.ErrUnknownQuestion }, []dayFile{tracked}, []string{"today"}, "", []string{ipc.AskStatus}},
		{"another day, asked for with --date, never asks the agent, since the sentence is about now",
			status(unknown, nil), []dayFile{tracked, yesterday}, []string{"today", "--date", "2026-11-23"}, "", nil},
		{"a day asked for by --date, even today's own, is shown by its date as its empty-day words are, and asks nothing",
			status(unknown, nil), []dayFile{tracked}, []string{"today", "--date", "2026-11-24"}, "", nil},
		{"--json keeps to its schema and asks nothing",
			status(unknown, nil), []dayFile{tracked}, []string{"today", "--json"}, "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			onDay(t, "2026-11-24", time.UTC)
			h := newHome(t).running().writeDays(tc.days...)
			asked := h.agent(tc.answer)
			code, out, errOut := runCmd(tc.args...)
			if code != 0 || errOut != "" {
				t.Fatalf("exit %d, stderr %q", code, errOut)
			}
			if tc.want != "" && !strings.HasSuffix(out, "\n\n"+tc.want+"\n") {
				t.Errorf("the view doesn't end with %q:\n%s", tc.want, out)
			}
			if tc.want == "" && strings.Contains(out, "doesn't tell pairtime which window") {
				t.Errorf("the view speaks of focus:\n%s", out)
			}
			if got := asked(); !slices.Equal(got, tc.asked) {
				t.Errorf("asked the agent %q, want %q", got, tc.asked)
			}
			if slices.Contains(tc.args, "--json") {
				checkSchema(t, loadTodaySchema(t), out)
			}
		})
	}
	t.Run("an agent that doesn't answer within focusAskWait leaves the sentence out, and today prints the day all the same", func(t *testing.T) {
		focusAskWait = 200 * time.Millisecond
		t.Cleanup(func() { focusAskWait = 10 * time.Second })
		onDay(t, "2026-11-24", time.UTC)
		h := newHome(t).running().writeDays(tracked)
		block := make(chan struct{})
		h.agent(func(string) (json.RawMessage, error) {
			<-block
			return nil, ipc.ErrUnknownQuestion
		})
		t.Cleanup(func() { close(block) }) // before the agent's own cleanup, which waits for the answer
		code, out, errOut := runCmd("today")
		if code != 0 || errOut != "" || !strings.HasPrefix(out, dayTitle("2026-11-24")+"\n") || strings.Contains(out, "doesn't tell pairtime which window") {
			t.Errorf("exit %d, stderr %q, stdout\n%s\nwant the day without the sentence", code, errOut, out)
		}
	})
	t.Run("the words are DESIGN 5.9's teen-facing sentence, the first clause alone until WP-SC8 makes the rest true", func(t *testing.T) {
		const whole = "This desktop doesn't tell pairtime which window is in front, so your time here counts only on your own dashboard, not on your profile, build days or most programs."
		if stops != "This desktop doesn't tell pairtime which window is in front." || counts != whole {
			t.Errorf("sentence %q, and with level C %q", stops, counts)
		}
	})
}

func TestTodayAndStatuslineWhenNothingCanBeShown(t *testing.T) {
	agentDown := text.Phrase(text.KeyAgentDown)
	storeFailed := text.Phrase(text.KeyStoreError, "error", "")
	cases := []struct {
		name string
		set  func(h fakeHome)
		// want is today's stderr, or how it starts when prefix is set.
		want   string
		prefix bool
	}{
		{"no port.json, because the agent never ran, and nothing stored today", func(fakeHome) {}, agentDown, false},
		{"port.json says the agent stopped, and nothing stored today", func(h fakeHome) {
			writePortFile(h.t, h.dir, localhttp.PortFile{Port: 7247, Token: localhttp.NewToken(), PID: 0})
		}, agentDown, false},
		{"today's rollup isn't JSON", func(h fakeHome) {
			h.running().writeFile(filepath.Join("days", "2026-11-24.json"), []byte("{"))
		}, storeFailed, true},
		{"today's rollup holds another day", func(h fakeHome) {
			b, _ := json.Marshal(dayFile{Date: "2026-11-23", Projects: []dayProject{}})
			h.running().writeFile(filepath.Join("days", "2026-11-24.json"), b)
		}, storeFailed, true},
		{"config.toml can't be read, so whether to hide your time isn't known", func(h fakeHome) {
			h.running()
			if err := os.Mkdir(h.dirs().ConfigFile(), 0o700); err != nil {
				h.t.Fatal(err)
			}
		}, storeFailed, true},
	}
	for _, tc := range cases {
		t.Run("today: "+tc.name+" exits 1 and says so on stderr", func(t *testing.T) {
			onDay(t, "2026-11-24", time.UTC)
			tc.set(newHome(t))
			code, out, errOut := runCmd("today")
			ok := errOut == tc.want+"\n"
			if tc.prefix {
				ok = strings.HasPrefix(errOut, tc.want) && len(errOut) > len(tc.want)+1
			}
			if code != 1 || out != "" || !ok {
				t.Errorf("exit %d, stdout %q, stderr %q; want exit 1, no stdout and %q", code, out, errOut, tc.want)
			}
		})
		t.Run("statusline: "+tc.name+" prints nothing and exits 0", func(t *testing.T) {
			onDay(t, "2026-11-24", time.UTC)
			tc.set(newHome(t))
			code, out, errOut := runCmd("statusline", "--timeout", "5s")
			if code != 0 || out != "" || errOut != "" {
				t.Errorf("exit %d, stdout %q, stderr %q; want exit 0 and nothing", code, out, errOut)
			}
		})
	}
}

func TestRecapWhenTheAgentFails(t *testing.T) {
	old := viewTimeout
	viewTimeout = 300 * time.Millisecond
	t.Cleanup(func() { viewTimeout = old })
	agentFailed := text.Phrase(text.KeyAgentError, "error", "")
	cases := []struct {
		name string
		set  func(t *testing.T, h fakeHome)
		// want is recap's stderr, or how it starts when prefix is set.
		want   string
		prefix bool
	}{
		{"nothing listens on the socket", func(*testing.T, fakeHome) {}, text.Phrase(text.KeyAgentDown), false},
		{"nothing listens on the socket, though port.json names a port", func(_ *testing.T, h fakeHome) { h.running() }, text.Phrase(text.KeyAgentDown), false},
		{"the agent answers no question, as one from before WP-AG4", func(_ *testing.T, h fakeHome) { h.agent(nil) }, text.Phrase(text.KeyAgentOld), false},
		{"the agent doesn't answer in time", func(t *testing.T, h fakeHome) {
			block := make(chan struct{})
			h.agent(func(string) (json.RawMessage, error) {
				<-block
				return nil, ipc.ErrUnknownQuestion
			})
			t.Cleanup(func() { close(block) }) // before the agent's own cleanup, which waits for the answer
		}, text.Phrase(text.KeyAgentSlow), false},
		{"the agent couldn't answer", func(_ *testing.T, h fakeHome) {
			h.agent(func(string) (json.RawMessage, error) { return nil, errors.New("the bucket store is gone") })
		}, agentFailed, true},
		{"the agent's answer isn't a session", func(_ *testing.T, h fakeHome) {
			h.agent(func(string) (json.RawMessage, error) { return json.RawMessage(`[1, 2]`), nil })
		}, agentFailed, true},
	}
	for _, tc := range cases {
		t.Run(tc.name+": recap exits 1 and says so kindly on stderr", func(t *testing.T) {
			h := newHome(t)
			tc.set(t, h)
			code, out, errOut := runCmd("recap")
			ok := errOut == tc.want+"\n"
			if tc.prefix {
				ok = strings.HasPrefix(errOut, tc.want) && len(errOut) > len(tc.want)+1
			}
			if code != 1 || out != "" || !ok {
				t.Errorf("exit %d, stdout %q, stderr %q; want exit 1, no stdout and %q", code, out, errOut, tc.want)
			}
		})
	}
}

// TestViewsSendNothingOverTCP holds the views to #120: with an agent's
// port.json naming a port where something listens, and everything each
// view needs at hand, none of them connects to that port, so none hands
// the bearer token to whatever listens there.
func TestViewsSendNothingOverTCP(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var connected atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			connected.Add(1)
			c.Close()
		}
	}()
	t.Cleanup(func() { ln.Close() })
	s := loadFixture(t, "6.2")
	onDay(t, "2026-11-24", s.zone)
	h := newHome(t).writeDays(dayFiles(s.days())...)
	writePortFile(t, h.dir, localhttp.PortFile{Port: localhttp.Port(ln), Token: localhttp.NewToken(), PID: os.Getpid()})
	last := s.lastSession()
	h.sessionAgent(sessionReply{Session: &last})
	for _, args := range [][]string{
		{"today"}, {"today", "--json"}, {"today", "--date", "2026-11-24"},
		{"statusline", "--timeout", "5s"}, {"statusline", "--json", "--timeout", "5s"},
		{"recap"}, {"recap", "--json"},
	} {
		code, out, errOut := runCmd(args...)
		if code != 0 || out == "" || errOut != "" {
			t.Errorf("%q: exit %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
	if n := connected.Load(); n != 0 {
		t.Errorf("the views connected to port.json's port %d times", n)
	}
}

func TestDayStore(t *testing.T) {
	t.Run("a day as WP-AG4's scorer writes it reads field for field", func(t *testing.T) {
		// testdata/days/2026-11-24.json is SM 6.2's day as WP-AG4's
		// store.WriteDay writes it at 6c15be9: json.Marshal of a store.Day,
		// whose projects embed store.Tally, with no name yet.
		d, found, err := dayStore{filepath.Join("testdata", "days")}.day("2026-11-24")
		if err != nil || !found {
			t.Fatalf("found %v, %v", found, err)
		}
		start, end := time.Date(2026, 11, 24, 5, 0, 0, 0, time.UTC), time.Date(2026, 11, 25, 5, 0, 0, 0, time.UTC)
		if d.Date != "2026-11-24" || d.Zone != "Local" || !d.Start.Equal(start) || !d.End.Equal(end) || len(d.Projects) != 1 {
			t.Fatalf("got %+v", d)
		}
		want := dayProject{Project: "p_be4901c04a84", tally: tally{
			CreditedS: 265,
			CreditedByKind: map[event.Activity]float64{event.ActivityHandEdit: 120, event.ActivityPrompting: 60,
				event.ActivityReadingOutput: 15, event.ActivityReviewing: 50, event.ActivitySupervising: 20},
			CreditedByEvidence: map[event.Evidence]float64{event.EvidenceB: 265},
			AgentOnlyS:         20, AgentOnlyByHarness: map[event.Harness]float64{event.HarnessCursor: 20},
			Uncredited: map[event.Reason]float64{event.ReasonAbsent: 15, event.ReasonAgentOnly: 20},
		}}
		if !reflect.DeepEqual(d.Projects[0], want) {
			t.Errorf("got %+v\nwant %+v", d.Projects[0], want)
		}
	})
	t.Run("today shows that day with the project's key, which stands in for a name until the project map has one", func(t *testing.T) {
		b, err := os.ReadFile(filepath.Join("testdata", "days", "2026-11-24.json"))
		if err != nil {
			t.Fatal(err)
		}
		newHome(t).running().writeFile(filepath.Join("days", "2026-11-24.json"), b)
		code, out, errOut := runCmd("today", "--date", "2026-11-24")
		want := strings.Replace(today62, "remind-bot:", "p_be4901c04a84:", 1)
		if code != 0 || errOut != "" || out != want {
			t.Errorf("exit %d, stderr %q, got\n%s\nwant\n%s", code, errOut, out, want)
		}
	})
	dir := t.TempDir()
	write := func(name, data string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("2026-11-20.json", `{"date": "2026-11-20", "projects": null, "minutes": 1440, "future": {"x": 1}}`)
	write("2026-11-21.json", `{"date": "2026-11-21", "projects": [`)
	write("2026-11-22.json", `{"date": "2026-11-23", "projects": []}`)
	write("2026-11-23.json", `{"date": "2026-11-23", "projects": [], "pad": "`+strings.Repeat("x", maxDayFile)+`"}`)
	cases := []struct {
		name      string
		date      string
		wantFound bool
		wantErr   bool
	}{
		{"fields a newer agent adds are ignored, and no projects are none", "2026-11-20", true, false},
		{"a day with no rollup is empty, not an error", "2026-11-24", false, false},
		{"a rollup that isn't JSON is an error", "2026-11-21", false, true},
		{"a rollup that holds another day is an error", "2026-11-22", false, true},
		{"a rollup over 1 MiB is an error", "2026-11-23", false, true},
		{"a date that doesn't exist is refused", "2026-02-30", false, true},
		{"a path is refused as a date", "../days/2026-11-20", false, true},
		{"a file name is refused as a date", "2026-11-20.json", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, found, err := dayStore{dir}.day(tc.date)
			if found != tc.wantFound || (err != nil) != tc.wantErr {
				t.Fatalf("found %v, err %v; want found %v and an error: %v", found, err, tc.wantFound, tc.wantErr)
			}
			if err == nil && (d.Date != tc.date || d.Projects == nil || len(d.Projects) != 0) {
				t.Errorf("got %+v, want the day %s with no projects", d, tc.date)
			}
		})
	}
}

// evil is a project name made of what a folder's name may hold: an OSC 52
// write to the clipboard, a bell, a screen clear and U+202E, which turns
// the rest of a line around.
const evil = "remind\x1b]52;c;cm0gLXJmIH4=\x07bot\x1b[2J\xe2\x80\xae"

// terminalUnsafe returns the bytes in s, text a view printed, that a
// terminal would act on: control characters but the line ends, and U+202E.
func terminalUnsafe(s string) []string {
	var out []string
	for _, r := range s {
		if (r < 0x20 && r != '\n') || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == 0x202e {
			out = append(out, string(r))
		}
	}
	return out
}

func TestViewsPrintNoControlBytes(t *testing.T) {
	tl := tally{
		CreditedS: 60, CreditedByKind: map[event.Activity]float64{event.ActivityPrompting: 60, "jug\x1b[31mgling": 30},
		CreditedByEvidence: map[event.Evidence]float64{event.EvidenceB: 60},
		AgentOnlyS:         30, AgentOnlyByHarness: map[event.Harness]float64{"claude\x1b[2J": 30},
		Uncredited: map[event.Reason]float64{event.ReasonAbsent: 30, "X.\x07ring": 30},
	}
	cases := []struct {
		name string
		args []string
	}{
		{"today", []string{"today"}},
		{"statusline", []string{"statusline", "--timeout", "5s"}},
		{"recap", []string{"recap"}},
	}
	for _, tc := range cases {
		t.Run(tc.name+" prints a project's name and the agent's codes with nothing a terminal acts on", func(t *testing.T) {
			onDay(t, "2026-11-24", time.UTC)
			h := newHome(t).running().writeDays(day(dayProject{Project: "p_aa01", Name: evil, tally: tl}))
			h.sessionAgent(sessionReply{Session: &workSession{Project: "p_aa01", Name: evil,
				Start: time.Date(2026, 11, 24, 7, 0, 0, 0, time.UTC), End: time.Date(2026, 11, 24, 7, 5, 0, 0, time.UTC), tally: tl}})
			code, out, errOut := runCmd(tc.args...)
			if code != 0 || errOut != "" {
				t.Fatalf("exit %d, stderr %q", code, errOut)
			}
			if bad := terminalUnsafe(out); len(bad) > 0 {
				t.Errorf("printed %q in\n%q", bad, out)
			}
		})
	}
	t.Run("--json escapes them, as encoding/json does", func(t *testing.T) {
		onDay(t, "2026-11-24", time.UTC)
		newHome(t).running().writeDays(day(dayProject{Project: "p_aa01", Name: evil, tally: tl}))
		_, out, _ := runCmd("today", "--json")
		if strings.ContainsAny(out, "\x1b\x07") {
			t.Errorf("--json printed a raw control character: %q", out)
		}
	})
}

const todayUnknown = `Tuesday, November 24

remind-bot: you 10m · your agent 2m
  Your time                       10m
    writing prompts                5m
  Your agent, working on its own   2m
  Away                             1m
`

// TestCodesANewerAgentAdds holds the views to what they print when the
// agent's stores name a harness, a kind or a reason this build has no
// words for: an agent newer than the CLI wrote them.
func TestCodesANewerAgentAdds(t *testing.T) {
	newer := day(dayProject{Project: "p_aa01", Name: "remind-bot", tally: tally{
		CreditedS: 600, CreditedByKind: map[event.Activity]float64{event.ActivityPrompting: 300, "juggling": 300},
		CreditedByEvidence: map[event.Evidence]float64{event.EvidenceB: 600},
		AgentOnlyS:         120, AgentOnlyByHarness: map[event.Harness]float64{"windsurf": 120},
		Uncredited: map[event.Reason]float64{event.ReasonAbsent: 60, "X.new_thing": 90, event.ReasonAgentOnly: 120},
	}})
	onDay(t, "2026-11-24", time.UTC)
	newHome(t).running().writeDays(newer)
	t.Run("the text calls the harness your agent and leaves out the kind and the reason, but not their seconds", func(t *testing.T) {
		code, out, errOut := runCmd("today")
		if code != 0 || errOut != "" || out != todayUnknown {
			t.Errorf("exit %d, stderr %q, got\n%s\nwant\n%s", code, errOut, out, todayUnknown)
		}
	})
	t.Run("--json still fits cli-today.schema.json", func(t *testing.T) {
		_, out, _ := runCmd("today", "--json")
		checkSchema(t, loadTodaySchema(t), out)
		var got todayJSON
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatal(err)
		}
		p := got.Projects[0]
		if len(p.Agents) != 1 || p.Agents[0].Harness != event.HarnessOther || p.You.S != 600 || len(p.You.Kinds) != 1 || len(p.Other) != 1 {
			t.Errorf("got %s", out)
		}
	})
}

func TestStatuslineTimeout(t *testing.T) {
	cases := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"the default leaves room for the line in PLAN WP-CO0's 50 ms", func(t *testing.T) {
			if statuslineTimeout >= 50*time.Millisecond {
				t.Errorf("the default timeout is %v", statuslineTimeout)
			}
		}},
		{"with no --timeout, a day that takes long to read prints nothing within a second", func(t *testing.T) {
			block := make(chan struct{})
			old := statusRead
			statusRead = func() (*statusJSON, error) {
				<-block
				return nil, errors.New("too late")
			}
			t.Cleanup(func() {
				close(block)
				statusRead = old
			})
			begin := time.Now()
			code, out, errOut := runCmd("statusline")
			if took := time.Since(begin); code != 0 || out != "" || errOut != "" || took > time.Second {
				t.Errorf("exit %d, stdout %q, stderr %q after %v", code, out, errOut, took)
			}
		}},
		{"a bug while reading the day prints nothing and exits 0", func(t *testing.T) {
			old := statusRead
			statusRead = func() (*statusJSON, error) { panic("a bug") }
			t.Cleanup(func() { statusRead = old })
			code, out, errOut := runCmd("statusline", "--timeout", "5s")
			if code != 0 || out != "" || errOut != "" {
				t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
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
		{"today with a date that doesn't exist is a usage error", []string{"today", "--date", "2026-02-30"}, 2, "--date wants a day such as 2026-11-24"},
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
