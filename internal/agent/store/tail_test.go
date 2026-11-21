// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
)

// tailOf reads every revision of day's file after m, and returns their
// revs, the mark after the last and whether the read restarted.
func tailOf(t *testing.T, dir, day string, m Mark) ([]uint32, Mark, bool) {
	t.Helper()
	var revs []uint32
	last := m
	restart, err := TailDay(dir, day, m, func(r Revision) bool {
		revs = append(revs, r.Bucket.Rev)
		last = r.Mark
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	return revs, last, restart
}

func TestTail(t *testing.T) {
	const day = "2026-11-19"
	tests := []struct {
		name string
		run  func(t *testing.T, dir string, s *Buckets)
	}{
		{"a read from the zero mark gets every revision line in order", func(t *testing.T, dir string, s *Buckets) {
			revs, m, restart := tailOf(t, dir, day, Mark{})
			if len(revs) != 3 || revs[0] != 0 || revs[1] != 1 || revs[2] != 0 || restart || m.Off == 0 {
				t.Errorf("revs %v, mark %+v, restart %v", revs, m, restart)
			}
		}},
		{"a read from a mark gets only the lines after it", func(t *testing.T, dir string, s *Buckets) {
			_, m, _ := tailOf(t, dir, day, Mark{})
			if _, err := put(s, bucket(t, "2026-11-19T10:02:00Z", event.StatePending, nil, map[string]int64{"p_a": 30})); err != nil {
				t.Fatal(err)
			}
			revs, _, restart := tailOf(t, dir, day, m)
			if len(revs) != 1 || restart {
				t.Errorf("revs %v, restart %v", revs, restart)
			}
		}},
		{"a line that isn't whole yet waits for a later read", func(t *testing.T, dir string, s *Buckets) {
			_, m, _ := tailOf(t, dir, day, Mark{})
			f, err := os.OpenFile(filepath.Join(dir, day+".jsonl"), os.O_WRONLY|os.O_APPEND, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			f.WriteString(`{"v":"0.1.0","minute":"2026-11-19T10:05`)
			f.Close()
			revs, m2, _ := tailOf(t, dir, day, m)
			if len(revs) != 0 || m2 != m {
				t.Errorf("a torn line was read: %v", revs)
			}
		}},
		{"a file cut and written again past its mark is read from its start", func(t *testing.T, dir string, s *Buckets) {
			_, m, _ := tailOf(t, dir, day, Mark{})
			name := filepath.Join(dir, day+".jsonl")
			data, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			// A power cut takes the last line, and a different one is
			// written where it was, longer, so the file passes the mark.
			cut := data[:len(data)-1]
			cut = cut[:len(cut)-len(lastLine(cut))]
			b := bucket(t, "2026-11-19T10:09:00Z", event.StateFinal, nil, map[string]int64{"p_a": 50, "p_b": 5})
			line, _ := jsonLine(b)
			if err := os.WriteFile(name, append(append(cut, line...), line...), 0o600); err != nil {
				t.Fatal(err)
			}
			revs, _, restart := tailOf(t, dir, day, m)
			if !restart || len(revs) != 4 {
				t.Errorf("revs %v, restart %v", revs, restart)
			}
		}},
		{"a day without a file has no lines", func(t *testing.T, dir string, s *Buckets) {
			revs, _, restart := tailOf(t, dir, "2026-11-01", Mark{})
			if len(revs) != 0 || restart {
				t.Errorf("revs %v, restart %v", revs, restart)
			}
		}},
		{"BucketDays lists the day files with their sizes", func(t *testing.T, dir string, s *Buckets) {
			days, err := BucketDays(dir)
			if err != nil {
				t.Fatal(err)
			}
			fi, err := os.Stat(filepath.Join(dir, day+".jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			if len(days) != 1 || days[0].Day != day || days[0].Size != fi.Size() {
				t.Errorf("days %+v", days)
			}
		}},
		{"a store that doesn't exist yet has no days", func(t *testing.T, dir string, s *Buckets) {
			days, err := BucketDays(filepath.Join(dir, "missing"))
			if err != nil || len(days) != 0 {
				t.Errorf("days %v, %v", days, err)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			s := open(t, dir)
			for _, b := range []Bucket{
				bucket(t, "2026-11-19T10:00:00Z", event.StatePending, nil, map[string]int64{"p_a": 20}),
				bucket(t, "2026-11-19T10:00:00Z", event.StateFinal, nil, map[string]int64{"p_a": 40}),
				bucket(t, "2026-11-19T10:01:00Z", event.StateFinal, nil, map[string]int64{"p_a": 60}),
			} {
				if _, err := put(s, b); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Sync(); err != nil {
				t.Fatal(err)
			}
			tc.run(t, dir, s)
		})
	}
}

// put stores b and flushes it, so the file holds it.
func put(s *Buckets, b Bucket) (Bucket, error) {
	stored, _, err := s.Put(b)
	if err == nil {
		err = s.Sync()
	}
	return stored, err
}

// jsonLine is b as a line of a day's file.
func jsonLine(b Bucket) ([]byte, error) {
	js, err := json.Marshal(b)
	return append(js, '\n'), err
}

// lastLine returns the last line of data, which ends without its newline.
func lastLine(data []byte) []byte {
	for i := len(data) - 1; i >= 0; i-- {
		if data[i] == '\n' {
			return data[i+1:]
		}
	}
	return data
}

func TestMinuteBucketAgentOnly(t *testing.T) {
	tests := []struct {
		name  string
		agent map[string]ProjectMinute
		want  map[string]float64
	}{
		{"each project's agent-only seconds go as agent_only", map[string]ProjectMinute{"p_a": {AgentS: 30, AgentOnlyS: 20}, "p_b": {AgentS: 10, AgentOnlyS: 10}},
			map[string]float64{"p_a": 20, "p_b": 10}},
		{"a project whose agent time was all credited isn't named", map[string]ProjectMinute{"p_a": {AgentS: 30}, "p_b": {AgentOnlyS: 5}},
			map[string]float64{"p_b": 5}},
		{"a minute with no agent-only time has none", map[string]ProjectMinute{"p_a": {AgentS: 30}}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := Bucket{Projects: tc.agent}
			if got := b.MinuteBucket(event.Witnessed).AgentOnly; !maps.Equal(got, tc.want) || (got == nil) != (tc.want == nil) {
				t.Errorf("agent_only %v, want %v", got, tc.want)
			}
		})
	}
}
