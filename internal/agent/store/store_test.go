// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
)

const testParams = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

func minute(t *testing.T, s string) time.Time {
	t.Helper()
	m, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return m.UTC()
}

// bucket returns a bucket for the minute at s whose projects earned the
// given credit, every slot summed into the first, and agent seconds by
// project.
func bucket(t *testing.T, s string, state event.BucketState, credit map[string][]Credited, agent map[string]int64) Bucket {
	t.Helper()
	b := Bucket{V: "0.1.0", Params: testParams, Minute: minute(t, s), Device: "d_test", Slots: make([]event.BucketSlot, 4),
		State: state, Projects: map[string]ProjectMinute{}}
	var halves int64
	for _, key := range slices.Sorted(maps.Keys(credit)) {
		pm := b.Projects[key]
		pm.Credited = credit[key]
		for _, c := range credit[key] {
			halves += c.Halves
		}
		b.Projects[key] = pm
	}
	for _, key := range slices.Sorted(maps.Keys(agent)) {
		pm := b.Projects[key]
		pm.AgentS += agent[key]
		b.Projects[key] = pm
		b.AgentRunningS += float64(agent[key])
	}
	b.CreditedS = float64(halves) / 2
	if halves > 0 {
		keys := slices.Sorted(maps.Keys(credit))
		c := credit[keys[0]][0]
		b.Slots[0] = event.BucketSlot{Project: keys[0], Credited: min(b.CreditedS, 15), Why: c.Why, Kind: c.Why.Activity(), Ev: c.Ev}
	}
	return b
}

func open(t *testing.T, dir string) *Buckets {
	t.Helper()
	s, err := OpenBuckets(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPut(t *testing.T) {
	const m = "2026-11-20T16:42:00Z"
	a := map[string][]Credited{"p_a": {{Why: event.ReasonPrompt, Ev: event.EvidenceA, Halves: 30}}}
	more := map[string][]Credited{"p_a": {{Why: event.ReasonPrompt, Ev: event.EvidenceA, Halves: 40}}}
	type put struct {
		b       func(t *testing.T) Bucket
		rev     uint32
		wrote   bool
		isFinal bool // want ErrFinal
	}
	pending := func(c map[string][]Credited) func(t *testing.T) Bucket {
		return func(t *testing.T) Bucket { return bucket(t, m, event.StatePending, c, nil) }
	}
	final := func(c map[string][]Credited) func(t *testing.T) Bucket {
		return func(t *testing.T) Bucket { return bucket(t, m, event.StateFinal, c, nil) }
	}
	tests := []struct {
		name string
		puts []put
	}{
		{"a minute's first revision has rev 0", []put{{b: pending(a), rev: 0, wrote: true}}},
		{"a revision that says something new gets the latest rev plus one", []put{
			{b: pending(a), rev: 0, wrote: true},
			{b: pending(more), rev: 1, wrote: true},
		}},
		{"a revision that says what the latest says writes nothing", []put{
			{b: pending(a), rev: 0, wrote: true},
			{b: pending(a), rev: 0, wrote: false},
		}},
		{"a revision that differs only in the batch that wrote it writes nothing", []put{
			{b: pending(a), rev: 0, wrote: true},
			{b: func(t *testing.T) Bucket { b := pending(a)(t); b.Batch = 9; return b }, rev: 0, wrote: false},
		}},
		{"the change from pending to final is a revision of its own", []put{
			{b: pending(a), rev: 0, wrote: true},
			{b: final(a), rev: 1, wrote: true},
		}},
		{"a final minute takes no revision that says something else", []put{
			{b: pending(a), rev: 0, wrote: true},
			{b: final(a), rev: 1, wrote: true},
			{b: pending(more), rev: 1, isFinal: true},
			{b: final(more), rev: 1, isFinal: true},
		}},
		{"a final minute takes the same revision again without an error", []put{
			{b: final(a), rev: 0, wrote: true},
			{b: final(a), rev: 0, wrote: false},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := open(t, t.TempDir())
			for i, p := range tt.puts {
				got, wrote, err := s.Put(p.b(t))
				if p.isFinal {
					if !errors.Is(err, ErrFinal) {
						t.Fatalf("put %d: err = %v, want ErrFinal", i, err)
					}
				} else if err != nil {
					t.Fatalf("put %d: %v", i, err)
				}
				if got.Rev != p.rev || wrote != p.wrote {
					t.Errorf("put %d: rev %d, wrote %v; want rev %d, wrote %v", i, got.Rev, wrote, p.rev, p.wrote)
				}
			}
		})
	}
}

func TestPutRefusesAMinuteThatIsNotOne(t *testing.T) {
	tests := []struct {
		name string
		at   time.Time
	}{
		{"a time inside a minute isn't a bucket's minute", time.Date(2026, 11, 20, 16, 42, 15, 0, time.UTC)},
		{"the zero time isn't a bucket's minute", time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := open(t, t.TempDir())
			b := Bucket{Minute: tt.at, Slots: make([]event.BucketSlot, 4), State: event.StatePending}
			if _, _, err := s.Put(b); err == nil {
				t.Fatal("Put took it")
			}
		})
	}
}

func TestReopen(t *testing.T) {
	a := map[string][]Credited{"p_a": {{Why: event.ReasonRead, Ev: event.EvidenceB, Halves: 20}}}
	b := map[string][]Credited{"p_a": {{Why: event.ReasonRead, Ev: event.EvidenceB, Halves: 24}}}
	tests := []struct {
		name string
		// tear appends bytes after the two revisions, as a crash in the
		// middle of an append leaves them.
		tear string
	}{
		{"a store opened again finds each minute's latest revision", ""},
		{"a torn last line is cut off before the next append", `{"v":"0.1.0","params":"sha`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			s := open(t, dir)
			for _, c := range []map[string][]Credited{a, b} {
				if _, _, err := s.Put(bucket(t, "2026-11-20T10:00:00Z", event.StatePending, c, nil)); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(dir, "2026-11-20.jsonl")
			if tt.tear != "" {
				f, err := os.OpenFile(name, os.O_WRONLY|os.O_APPEND, 0)
				if err != nil {
					t.Fatal(err)
				}
				f.WriteString(tt.tear)
				f.Close()
			}
			s2 := open(t, dir)
			got, ok, err := s2.Latest(minute(t, "2026-11-20T10:00:00Z").UnixMilli())
			if err != nil || !ok {
				t.Fatalf("Latest: %v, %v", ok, err)
			}
			if got.Rev != 1 || got.CreditedS != 12 {
				t.Errorf("latest is rev %d with %v s, want rev 1 with 12 s", got.Rev, got.CreditedS)
			}
			// One more revision, on another minute, then every line must be
			// whole JSON.
			if _, _, err := s2.Put(bucket(t, "2026-11-20T10:01:00Z", event.StatePending, a, nil)); err != nil {
				t.Fatal(err)
			}
			if err := s2.Sync(); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
			if len(lines) != 3 || !strings.HasSuffix(string(data), "\n") {
				t.Fatalf("the file holds %d lines, want 3 whole ones:\n%s", len(lines), data)
			}
			for i, l := range lines {
				if !strings.HasPrefix(l, `{"v":`) || !strings.HasSuffix(l, "}") {
					t.Errorf("line %d isn't a whole revision: %q", i, l)
				}
			}
		})
	}
}

func TestRangeAndPending(t *testing.T) {
	a := map[string][]Credited{"p_a": {{Why: event.ReasonPrompt, Ev: event.EvidenceA, Halves: 2}}}
	dir := t.TempDir()
	s := open(t, dir)
	for _, x := range []struct {
		at    string
		state event.BucketState
	}{
		{"2026-11-19T23:58:00Z", event.StateFinal},
		{"2026-11-19T23:59:00Z", event.StatePending},
		{"2026-11-20T00:00:00Z", event.StatePending},
		{"2026-11-20T00:01:00Z", event.StateFinal},
	} {
		if _, _, err := s.Put(bucket(t, x.at, x.state, a, nil)); err != nil {
			t.Fatal(err)
		}
	}
	at := func(s string) int64 {
		m, _ := time.Parse(time.RFC3339, s)
		return m.UnixMilli()
	}
	tests := []struct {
		name string
		get  func(t *testing.T, s *Buckets) ([]Bucket, error)
		want []string
	}{
		{"Range returns the minutes from from up to to across UTC days, in order",
			func(_ *testing.T, s *Buckets) ([]Bucket, error) {
				return s.Range(at("2026-11-19T23:59:00Z"), at("2026-11-20T00:01:00Z"))
			},
			[]string{"2026-11-19T23:59:00Z", "2026-11-20T00:00:00Z"}},
		{"Pending returns the pending minutes from from on, whatever day they are in",
			func(_ *testing.T, s *Buckets) ([]Bucket, error) { return s.Pending(at("2026-11-19T00:00:00Z")) },
			[]string{"2026-11-19T23:59:00Z", "2026-11-20T00:00:00Z"}},
		{"Pending leaves out the minutes before from",
			func(_ *testing.T, s *Buckets) ([]Bucket, error) { return s.Pending(at("2026-11-20T00:00:00Z")) },
			[]string{"2026-11-20T00:00:00Z"}},
		{"Pending reads day files the store hasn't read",
			func(t *testing.T, s *Buckets) ([]Bucket, error) {
				s.Close()
				return open(t, dir).Pending(at("2026-11-01T00:00:00Z"))
			},
			[]string{"2026-11-19T23:59:00Z", "2026-11-20T00:00:00Z"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bs, err := tt.get(t, s)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, b := range bs {
				got = append(got, b.Minute.Format(time.RFC3339))
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// with returns b with fn applied to project key's part of it.
func with(b Bucket, key string, fn func(*ProjectMinute)) Bucket {
	pm := b.Projects[key]
	fn(&pm)
	b.Projects[key] = pm
	return b
}

func TestRollup(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no time zone data:", err)
	}
	const date = "2026-11-20" // 05:00Z to 05:00Z the next day in New York
	cr := func(why event.Reason, ev event.Evidence, halves int64) Credited {
		return Credited{Why: why, Ev: ev, Halves: halves}
	}
	tests := []struct {
		name    string
		buckets func(t *testing.T) []Bucket
		cap     int64
		check   func(t *testing.T, d Day)
	}{
		{
			name: "credit sums by project, by kind and by level, and only levels and kinds with credit are named",
			buckets: func(t *testing.T) []Bucket {
				return []Bucket{
					bucket(t, "2026-11-20T15:00:00Z", event.StateFinal, map[string][]Credited{
						"p_a": {cr(event.ReasonPrompt, event.EvidenceA, 30), cr(event.ReasonRead, event.EvidenceB, 20)},
						"p_b": {cr(event.ReasonEdit, event.EvidenceC, 10)},
					}, nil),
					bucket(t, "2026-11-20T15:01:00Z", event.StateFinal, map[string][]Credited{
						"p_a": {cr(event.ReasonRead, event.EvidenceA, 120)},
					}, nil),
				}
			},
			cap: 43200,
			check: func(t *testing.T, d Day) {
				if len(d.Projects) != 2 || d.Projects[0].Project != "p_a" || d.Projects[1].Project != "p_b" {
					t.Fatalf("projects %+v, want p_a and p_b", d.Projects)
				}
				a, b := d.Projects[0], d.Projects[1]
				if a.CreditedS != 85 || b.CreditedS != 5 {
					t.Errorf("p_a %v s, p_b %v s; want 85 and 5", a.CreditedS, b.CreditedS)
				}
				if want := map[event.Evidence]float64{event.EvidenceA: 75, event.EvidenceB: 10}; !maps.Equal(a.CreditedByEvidence, want) {
					t.Errorf("p_a by level %v, want %v", a.CreditedByEvidence, want)
				}
				if want := map[event.Activity]float64{event.ActivityPrompting: 15, event.ActivityReadingOutput: 70}; !maps.Equal(a.CreditedByKind, want) {
					t.Errorf("p_a by kind %v, want %v", a.CreditedByKind, want)
				}
				if want := map[event.Evidence]float64{event.EvidenceC: 5}; !maps.Equal(b.CreditedByEvidence, want) {
					t.Errorf("p_b by level %v, want %v", b.CreditedByEvidence, want)
				}
			},
		},
		{
			name: "credit in pending minutes is counted apart as well",
			buckets: func(t *testing.T) []Bucket {
				return []Bucket{
					bucket(t, "2026-11-20T15:00:00Z", event.StateFinal, map[string][]Credited{"p_a": {cr(event.ReasonPrompt, event.EvidenceA, 30)}}, nil),
					bucket(t, "2026-11-20T15:01:00Z", event.StatePending, map[string][]Credited{"p_a": {cr(event.ReasonPrompt, event.EvidenceA, 10)}}, nil),
				}
			},
			cap: 43200,
			check: func(t *testing.T, d Day) {
				if p := d.Projects[0]; p.CreditedS != 20 || p.PendingS != 5 {
					t.Errorf("credited %v s with %v s pending, want 20 and 5", p.CreditedS, p.PendingS)
				}
			},
		},
		{
			name: "past DAY_CAP_S the credit of later minutes is X.day_cap of their project",
			buckets: func(t *testing.T) []Bucket {
				return []Bucket{
					bucket(t, "2026-11-20T15:00:00Z", event.StateFinal, map[string][]Credited{"p_a": {cr(event.ReasonPrompt, event.EvidenceA, 100)}}, nil),
					bucket(t, "2026-11-20T15:01:00Z", event.StateFinal, map[string][]Credited{"p_b": {cr(event.ReasonPrompt, event.EvidenceA, 100)}}, nil),
				}
			},
			cap: 60,
			check: func(t *testing.T, d Day) {
				a, b := d.Projects[0], d.Projects[1]
				if a.CreditedS != 50 || b.CreditedS != 10 || b.Uncredited[event.ReasonDayCap] != 40 || len(a.Uncredited) != 0 {
					t.Errorf("p_a %v s %v, p_b %v s %v; want 50 s, and 10 s with 40 s of X.day_cap", a.CreditedS, a.Uncredited, b.CreditedS, b.Uncredited)
				}
			},
		},
		{
			name: "minutes outside the local day are left out",
			buckets: func(t *testing.T) []Bucket {
				return []Bucket{
					bucket(t, "2026-11-20T04:59:00Z", event.StateFinal, map[string][]Credited{"p_a": {cr(event.ReasonPrompt, event.EvidenceA, 30)}}, nil),
					bucket(t, "2026-11-20T05:00:00Z", event.StateFinal, map[string][]Credited{"p_a": {cr(event.ReasonPrompt, event.EvidenceA, 10)}}, nil),
					bucket(t, "2026-11-21T05:00:00Z", event.StateFinal, map[string][]Credited{"p_a": {cr(event.ReasonPrompt, event.EvidenceA, 30)}}, nil),
				}
			},
			cap: 43200,
			check: func(t *testing.T, d Day) {
				if len(d.Projects) != 1 || d.Projects[0].CreditedS != 5 {
					t.Errorf("projects %+v, want p_a with 5 s", d.Projects)
				}
				if !d.Start.Equal(minute(t, "2026-11-20T05:00:00Z")) || !d.End.Equal(minute(t, "2026-11-21T05:00:00Z")) {
					t.Errorf("the day runs %v to %v", d.Start, d.End)
				}
			},
		},
		{
			name: "agent-only time sums by project and by harness, and a harness with none of it is listed with 0",
			buckets: func(t *testing.T) []Bucket {
				b1 := with(bucket(t, "2026-11-20T15:00:00Z", event.StateFinal, nil, map[string]int64{"p_a": 60}), "p_a", func(pm *ProjectMinute) {
					pm.AgentOnlyS = 60
					pm.Harnesses = map[event.Harness]int64{event.HarnessClaudeCode: 60, event.HarnessCodex: 0}
				})
				b2 := with(bucket(t, "2026-11-20T15:01:00Z", event.StateFinal, nil, map[string]int64{"p_a": 30}), "p_a", func(pm *ProjectMinute) {
					pm.AgentOnlyS = 30
					pm.Harnesses = map[event.Harness]int64{event.HarnessClaudeCode: 30}
				})
				return []Bucket{b1, b2}
			},
			cap: 43200,
			check: func(t *testing.T, d Day) {
				if len(d.Projects) != 1 {
					t.Fatalf("projects %+v, want p_a", d.Projects)
				}
				p := d.Projects[0]
				want := map[event.Harness]float64{event.HarnessClaudeCode: 90, event.HarnessCodex: 0}
				if p.AgentOnlyS != 90 || !maps.Equal(p.AgentOnlyByHarness, want) || p.CreditedS != 0 {
					t.Errorf("p_a %+v, want 90 s of agent-only time, %v, and no credit", p, want)
				}
			},
		},
		{
			name: "uncredited seconds sum by project and reason",
			buckets: func(t *testing.T) []Bucket {
				b := bucket(t, "2026-11-20T15:00:00Z", event.StateFinal, map[string][]Credited{
					"p_a": {cr(event.ReasonPrompt, event.EvidenceA, 30)},
					"p_b": {cr(event.ReasonEdit, event.EvidenceA, 10)},
				}, nil)
				b = with(b, "p_a", func(pm *ProjectMinute) {
					pm.Uncredited = map[event.Reason]int64{event.ReasonAbsent: 15, event.ReasonNoAnchor: 5}
				})
				b = with(b, "p_b", func(pm *ProjectMinute) { pm.Uncredited = map[event.Reason]int64{event.ReasonOtherFocus: 15} })
				return []Bucket{b}
			},
			cap: 43200,
			check: func(t *testing.T, d Day) {
				a, b := d.Projects[0], d.Projects[1]
				if a.Uncredited[event.ReasonAbsent] != 15 || a.Uncredited[event.ReasonNoAnchor] != 5 || b.Uncredited[event.ReasonOtherFocus] != 15 {
					t.Errorf("p_a %v, p_b %v", a.Uncredited, b.Uncredited)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := Rollup(date, ny, tt.buckets(t), tt.cap)
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, d)
		})
	}
}

func TestLastSession(t *testing.T) {
	const gap = 20 * 60 * 1000 // SESSION_GAP_S
	a := map[string][]Credited{"p_a": {{Why: event.ReasonPrompt, Ev: event.EvidenceA, Halves: 20}}}
	b := map[string][]Credited{"p_b": {{Why: event.ReasonEdit, Ev: event.EvidenceB, Halves: 30}}}
	agentOnly := func(t *testing.T, at string) Bucket {
		return with(bucket(t, at, event.StateFinal, nil, map[string]int64{"p_a": 60}), "p_a", func(pm *ProjectMinute) { pm.AgentOnlyS = 60 })
	}
	tests := []struct {
		name               string
		minutes            func(t *testing.T) []Bucket
		to                 string
		project, from, end string // "" project: no session
		credited, agent    float64
	}{
		{
			name: "a session runs back over credited minutes no more than SESSION_GAP_S apart",
			minutes: func(t *testing.T) []Bucket {
				return []Bucket{
					bucket(t, "2026-11-20T10:00:00Z", event.StateFinal, a, nil),
					bucket(t, "2026-11-20T10:10:00Z", event.StateFinal, a, nil),
					bucket(t, "2026-11-20T10:31:00Z", event.StateFinal, a, nil),
				}
			},
			to:      "2026-11-21T00:00:00Z",
			project: "p_a", from: "2026-11-20T10:00:00Z", end: "2026-11-20T10:32:00Z", credited: 30,
		},
		{
			name: "a gap longer than SESSION_GAP_S between credited minutes starts a new session",
			minutes: func(t *testing.T) []Bucket {
				return []Bucket{
					bucket(t, "2026-11-20T10:00:00Z", event.StateFinal, a, nil),
					bucket(t, "2026-11-20T10:22:00Z", event.StateFinal, a, nil),
				}
			},
			to:      "2026-11-21T00:00:00Z",
			project: "p_a", from: "2026-11-20T10:22:00Z", end: "2026-11-20T10:23:00Z", credited: 10,
		},
		{
			name: "another project's minutes don't split a session, and the session sums its own project's part of every minute in it",
			minutes: func(t *testing.T) []Bucket {
				return []Bucket{
					bucket(t, "2026-11-20T10:00:00Z", event.StateFinal, a, nil),
					agentOnly(t, "2026-11-20T10:05:00Z"),
					bucket(t, "2026-11-20T10:06:00Z", event.StateFinal, b, nil),
					bucket(t, "2026-11-20T10:15:00Z", event.StateFinal, a, nil),
				}
			},
			to:      "2026-11-21T00:00:00Z",
			project: "p_a", from: "2026-11-20T10:00:00Z", end: "2026-11-20T10:16:00Z", credited: 20, agent: 60,
		},
		{
			name: "the latest credited minute decides the session's project",
			minutes: func(t *testing.T) []Bucket {
				return []Bucket{
					bucket(t, "2026-11-20T10:00:00Z", event.StateFinal, a, nil),
					bucket(t, "2026-11-20T10:05:00Z", event.StateFinal, b, nil),
				}
			},
			to:      "2026-11-21T00:00:00Z",
			project: "p_b", from: "2026-11-20T10:05:00Z", end: "2026-11-20T10:06:00Z", credited: 15,
		},
		{
			name: "minutes at or after to are left out",
			minutes: func(t *testing.T) []Bucket {
				return []Bucket{
					bucket(t, "2026-11-19T23:59:00Z", event.StateFinal, a, nil),
					bucket(t, "2026-11-20T10:05:00Z", event.StateFinal, b, nil),
				}
			},
			to:      "2026-11-20T10:05:00Z",
			project: "p_a", from: "2026-11-19T23:59:00Z", end: "2026-11-20T00:00:00Z", credited: 10,
		},
		{
			name:    "with no credited minute there is no session",
			minutes: func(t *testing.T) []Bucket { return []Bucket{agentOnly(t, "2026-11-20T10:05:00Z")} },
			to:      "2026-11-21T00:00:00Z",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := open(t, t.TempDir())
			for _, bk := range tt.minutes(t) {
				if _, _, err := s.Put(bk); err != nil {
					t.Fatal(err)
				}
			}
			got, err := LastSession(s, minute(t, tt.to).UnixMilli(), gap)
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case tt.project == "":
				if got != nil {
					t.Errorf("session %+v, want none", got)
				}
			case got == nil:
				t.Fatal("no session")
			case got.Project != tt.project || !got.Start.Equal(minute(t, tt.from)) || !got.End.Equal(minute(t, tt.end)):
				t.Errorf("session %s from %s to %s, want %s from %s to %s", got.Project, got.Start, got.End, tt.project, tt.from, tt.end)
			case got.CreditedS != tt.credited || got.AgentOnlyS != tt.agent:
				t.Errorf("session credits %v s with %v s agent-only, want %v and %v", got.CreditedS, got.AgentOnlyS, tt.credited, tt.agent)
			}
		})
	}
}

func TestWriteDay(t *testing.T) {
	tests := []struct {
		name string
		day  Day
		ok   bool
	}{
		{"a day's rollup reads back as written", Day{Date: "2026-11-20", Zone: "UTC", Projects: []DayProject{{Project: "p_a",
			Tally: Tally{CreditedS: 12.5, AgentOnlyS: 3, AgentOnlyByHarness: map[event.Harness]float64{event.HarnessCursor: 3}}}}}, true},
		{"a rollup whose date isn't a date isn't written", Day{Date: "../x"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "days")
			err := WriteDay(dir, tt.day)
			if !tt.ok {
				if err == nil {
					t.Fatal("WriteDay wrote it")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := ReadDay(dir, tt.day.Date)
			if err != nil {
				t.Fatal(err)
			}
			if got.Date != tt.day.Date || len(got.Projects) != 1 || got.Projects[0].CreditedS != 12.5 || got.Projects[0].AgentOnlyByHarness[event.HarnessCursor] != 3 {
				t.Errorf("read back %+v", got)
			}
		})
	}
}

func TestMinuteBucket(t *testing.T) {
	b := bucket(t, "2026-11-20T16:42:00Z", event.StateFinal, map[string][]Credited{
		"p_9f2c": {{Why: event.ReasonPrompt, Ev: event.EvidenceA, Halves: 30}},
	}, nil)
	b.Anchors = []string{"01a20d91-f900-7adf-af18-9024d7dbaf8d"}
	b.Uncredited = map[event.Reason]float64{event.ReasonOtherFocus: 10}
	tests := []struct {
		name    string
		witness event.Witness
		ev      event.Evidence
	}{
		{"a witnessed minute keeps the level the scorer computed and validates", event.Witnessed, event.EvidenceA},
		{"a backfilled minute's credit is level C and validates", event.Backfilled, event.EvidenceC},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := b.MinuteBucket(tt.witness)
			if err := m.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if m.Slots[0].Ev != tt.ev || m.Witness != tt.witness {
				t.Errorf("slot level %q, witness %q; want %q, %q", m.Slots[0].Ev, m.Witness, tt.ev, tt.witness)
			}
			if b.Slots[0].Ev != event.EvidenceA {
				t.Error("MinuteBucket changed the bucket it was called on")
			}
			if s := m.Shareable(); len(s.Anchors) != 0 {
				t.Errorf("the shareable form keeps the anchors %q", s.Anchors)
			}
		})
	}
}
