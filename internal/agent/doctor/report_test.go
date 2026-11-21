// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"strings"
	"testing"
	"time"
)

func TestReportText(t *testing.T) {
	ms := time.Millisecond
	agent := func() *AgentFootprint {
		return &AgentFootprint{Version: "v0.1.0", UptimeS: 26*3600 + 13*60, RSSBytes: 18_200_000, PeakRSSBytes: 21_400_000,
			CPUS: 38.1, Wakeups: 2988, SpanS: 60, SpanCPUS: 0.012, SpanWakeups: 2}
	}
	base := func() Report {
		return Report{Version: "v0.1.0", Platform: "windows/amd64", GoVersion: "go1.26.3", OS: "Windows 10, build 10.0.19045.5131 (22H2)",
			CPUs: 2, Disk: "hdd", Agent: agent(),
			Hooks:   []HookTimes{{Harness: "claude_code", Times: Times{N: 20, P50: 21 * ms, P95: 34 * ms, Max: 41 * ms}}},
			Startup: Times{N: 20, P50: 14 * ms, P95: 19 * ms, Max: 22 * ms}}
	}
	tests := []struct {
		name string
		edit func(*Report)
		want []string
		not  []string
	}{
		{
			name: "a running agent's memory, CPU and wakeups are shown since it started and over the span at rest, against R8's budgets",
			edit: func(*Report) {},
			want: []string{
				"pairtime:        v0.1.0, windows/amd64, go1.26.3\n",
				"os:              Windows 10, build 10.0.19045.5131 (22H2)\n",
				"cpus:            2\n",
				"disk:            hdd, a disk that spins\n",
				"agent:           v0.1.0, running for 26h 13m\n",
				"agent memory:    18.2 MB now, 21.4 MB at most (pairtime aims for under 25.0 MB)\n",
				"agent cpu:       0.040% since it started; 0.020% over 1m 0s at rest (pairtime aims for under 0.100%)\n",
				"agent wakeups:   1.9 a minute since it started; 2.0 a minute over the same span\n",
				"hook round trip: claude_code: 21 ms median, 34 ms p95, 41 ms most, over 20 warm calls, from start to exit (pairtime aims for under 50 ms)\n",
				"start-up alone:  14 ms median, 19 ms p95, 22 ms most, over 20 warm calls, for pairtime hook -h\n",
			},
		},
		{
			name: "where the OS gives only the peak, as macOS does, the memory line has the peak alone",
			edit: func(r *Report) { r.Agent.RSSBytes = 0 },
			want: []string{"agent memory:    21.4 MB at most (pairtime aims for under 25.0 MB)\n"},
		},
		{
			name: "where the OS gives no memory at all, the line says so",
			edit: func(r *Report) { r.Agent.RSSBytes, r.Agent.PeakRSSBytes = 0, 0 },
			want: []string{"agent memory:    unknown on this OS\n"},
		},
		{
			name: "a span in which the agent took hook calls says it wasn't at rest",
			edit: func(r *Report) { r.Agent.Busy = true },
			want: []string{"over 1m 0s while it took hook calls, so not at rest"},
			not:  []string{"1m 0s at rest"},
		},
		{
			name: "with no span, as when the second question went unanswered, only the numbers since the start show",
			edit: func(r *Report) { r.Agent.SpanS = 0 },
			want: []string{"agent cpu:       0.040% since it started (pairtime aims", "agent wakeups:   1.9 a minute since it started\n"},
			not:  []string{"over the same span"},
		},
		{
			name: "an agent that has run under a minute has no wakeup rate yet",
			edit: func(r *Report) { r.Agent.UptimeS, r.Agent.SpanS = 30, 0 },
			want: []string{"agent wakeups:   unknown, since it has run under a minute\n"},
		},
		{
			name: "with no agent to ask, the report says why and keeps the computer's lines and the hook's",
			edit: func(r *Report) { r.Agent, r.NoAgent = nil, "not running" },
			want: []string{"agent:           not running, so its memory, CPU and wakeups aren't here\n", "os:", "hook round trip: claude_code:"},
			not:  []string{"agent memory", "agent cpu"},
		},
		{
			name: "a hook whose test call failed says so, without the error, which could name a path",
			edit: func(r *Report) { r.Hooks[0] = HookTimes{Harness: "claude_code", Failed: true} },
			want: []string{"hook round trip: claude_code: a test call failed, so none was timed; pairtime doctor says why\n"},
		},
		{
			name: "with no harness's hooks, the report says none was timed",
			edit: func(r *Report) { r.Hooks = nil },
			want: []string{"hook round trip: no harness has pairtime's hooks, so none was timed\n"},
		},
		{
			name: "a disk whose kind the OS doesn't say is unknown",
			edit: func(r *Report) { r.Disk = "unknown" },
			want: []string{"disk:            unknown: the OS doesn't say for the disk pairtime's data is on\n"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := base()
			tt.edit(&r)
			got := r.Text()
			if !strings.HasPrefix(got, "pairtime footprint report\n") {
				t.Errorf("the report doesn't start with its title:\n%s", got)
			}
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("the report doesn't have %q:\n%s", w, got)
				}
			}
			for _, w := range tt.not {
				if strings.Contains(got, w) {
					t.Errorf("the report has %q:\n%s", w, got)
				}
			}
		})
	}
}

func TestTimesOf(t *testing.T) {
	ms := time.Millisecond
	var twenty []time.Duration
	for i := 20; i >= 1; i-- {
		twenty = append(twenty, time.Duration(i)*ms)
	}
	tests := []struct {
		name string
		in   []time.Duration
		want Times
	}{
		{"no calls sum up to nothing", nil, Times{}},
		{"the 95th percentile of five calls is the slowest, by nearest rank", []time.Duration{5 * ms, 1 * ms, 3 * ms, 2 * ms, 4 * ms},
			Times{N: 5, P50: 3 * ms, P95: 5 * ms, Max: 5 * ms}},
		{"the 95th percentile of twenty calls is the nineteenth fastest", twenty,
			Times{N: 20, P50: 10*ms + 500*time.Microsecond, P95: 19 * ms, Max: 20 * ms}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TimesOf(tt.in); got != tt.want {
				t.Errorf("TimesOf = %+v, want %+v", got, tt.want)
			}
		})
	}
}
