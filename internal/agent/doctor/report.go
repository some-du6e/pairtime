// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

// Report is the footprint report `pairtime doctor --report` prints: what
// pairtime costs this computer, the R8 evidence that DESIGN 5.9 and 9.7
// and TE WP7.9 ask for, which the teen can choose to share. It holds
// numbers, versions and fixed words only, never a path, a name, an id or a
// problem's text, so nothing in it can name the teen or the computer;
// cmd/pairtime checks the text against what names them anyway.
type Report struct {
	// Version, Platform and GoVersion are this pairtime's.
	Version, Platform, GoVersion string
	// OS is the OS build (OSBuild), CPUs how many logical CPUs there are,
	// and Disk whether the disk with pairtime's data spins (DiskKind).
	OS   string
	CPUs int
	Disk string
	// Agent is the running agent's numbers, nil when it didn't answer,
	// and NoAgent says why then.
	Agent   *AgentFootprint
	NoAgent string
	// Hooks are the warm round trips of each harness's hook, from start to
	// exit, and Startup pairtime's start alone, `pairtime hook -h`, which
	// footprint.yml times beside them too.
	Hooks   []HookTimes
	Startup Times
}

// AgentFootprint is what the agent costs, since it started and over a
// span at rest that doctor measured.
type AgentFootprint struct {
	Version string
	UptimeS int64
	// RSSBytes and PeakRSSBytes are its resident set now and at most, 0
	// where the OS doesn't say.
	RSSBytes, PeakRSSBytes uint64
	// CPUS and Wakeups are its CPU time and the scheduler's timer firings
	// since it started.
	CPUS    float64
	Wakeups uint64
	// SpanS is how long the span lasted, and SpanCPUS and SpanWakeups what
	// the agent did in it. Busy is set when it took calls in the span, so
	// it wasn't at rest.
	SpanS       float64
	SpanCPUS    float64
	SpanWakeups uint64
	Busy        bool
}

// HookTimes is the round trips of one harness's hook.
type HookTimes struct {
	Harness string
	Times   Times
	// Failed is set when a call failed, which the report says without the
	// error, since an error can name a path.
	Failed bool
}

// Times sums up some timings.
type Times struct {
	N             int
	P50, P95, Max time.Duration
}

// TimesOf sums up ds: its median, its 95th percentile by nearest rank, and
// its most.
func TimesOf(ds []time.Duration) Times {
	if len(ds) == 0 {
		return Times{}
	}
	s := slices.Clone(ds)
	slices.Sort(s)
	rank := int(math.Ceil(0.95*float64(len(s)))) - 1
	return Times{N: len(s), P50: Median(s), P95: s[max(rank, 0)], Max: s[len(s)-1]}
}

// R8's budgets, which footprint.yml gates on Windows (DESIGN 9.7).
const (
	r8RSSBytes = 25_000_000
	r8CPUShare = 0.001
)

// Text is the report as doctor prints it.
func (r Report) Text() string {
	var b strings.Builder
	b.WriteString("pairtime footprint report\n")
	b.WriteString("It holds numbers and versions, never your name, this computer's name or a path, so you can share it.\n\n")
	line := func(label, format string, a ...any) {
		fmt.Fprintf(&b, "%-16s %s\n", label+":", fmt.Sprintf(format, a...))
	}
	line("pairtime", "%s, %s, %s", r.Version, r.Platform, r.GoVersion)
	line("os", "%s", r.OS)
	line("cpus", "%d", r.CPUs)
	line("disk", "%s", diskWords(r.Disk))
	if a := r.Agent; a == nil {
		line("agent", "%s, so its memory, CPU and wakeups aren't here", r.NoAgent)
	} else {
		up := time.Duration(a.UptimeS) * time.Second
		line("agent", "%s, running for %s", a.Version, roundDuration(up))
		switch {
		case a.RSSBytes > 0 && a.PeakRSSBytes > 0:
			line("agent memory", "%s now, %s at most (pairtime aims for under %s)", mb(a.RSSBytes), mb(a.PeakRSSBytes), mb(r8RSSBytes))
		case a.PeakRSSBytes > 0:
			line("agent memory", "%s at most (pairtime aims for under %s)", mb(a.PeakRSSBytes), mb(r8RSSBytes))
		default:
			line("agent memory", "unknown on this OS")
		}
		cpu := "unknown, since it has run under a second"
		if a.UptimeS > 0 {
			cpu = fmt.Sprintf("%s since it started", percent(a.CPUS/float64(a.UptimeS)))
		}
		wake := "unknown, since it has run under a minute"
		if a.UptimeS >= 60 {
			wake = fmt.Sprintf("%.1f a minute since it started", float64(a.Wakeups)/(float64(a.UptimeS)/60))
		}
		if a.SpanS > 0 {
			rest := "at rest"
			if a.Busy {
				rest = "while it took hook calls, so not at rest"
			}
			cpu += fmt.Sprintf("; %s over %s %s", percent(a.SpanCPUS/a.SpanS), roundDuration(time.Duration(a.SpanS*float64(time.Second))), rest)
			wake += fmt.Sprintf("; %.1f a minute over the same span", float64(a.SpanWakeups)/(a.SpanS/60))
		}
		line("agent cpu", "%s (pairtime aims for under %s)", cpu, percent(r8CPUShare))
		line("agent wakeups", "%s", wake)
	}
	if len(r.Hooks) == 0 {
		line("hook round trip", "no harness has pairtime's hooks, so none was timed")
	}
	for _, h := range r.Hooks {
		if h.Failed {
			line("hook round trip", "%s: a test call failed, so none was timed; pairtime doctor says why", h.Harness)
			continue
		}
		line("hook round trip", "%s: %s, from start to exit (pairtime aims for under %d ms)", h.Harness, timesWords(h.Times), R8HookMS)
	}
	if r.Startup.N > 0 {
		line("start-up alone", "%s, for pairtime hook -h", timesWords(r.Startup))
	}
	return b.String()
}

// timesWords says t as the report does: "21 ms median, 34 ms p95, 41 ms
// most, over 20 warm calls".
func timesWords(t Times) string {
	return fmt.Sprintf("%s median, %s p95, %s most, over %d warm calls", Millis(t.P50), Millis(t.P95), Millis(t.Max), t.N)
}

func diskWords(kind string) string {
	switch kind {
	case "hdd":
		return "hdd, a disk that spins"
	case "ssd":
		return "ssd"
	}
	return "unknown: the OS doesn't say for the disk pairtime's data is on"
}

// mb says bytes in megabytes, as footprint.yml's 25 MB budget counts them.
func mb(n uint64) string { return fmt.Sprintf("%.1f MB", float64(n)/1e6) }

// percent says a share as a percentage with three decimals.
func percent(share float64) string { return fmt.Sprintf("%.3f%%", share*100) }

// roundDuration says d in whole units: "26h 13m", "4m 5s", "45s".
func roundDuration(d time.Duration) string {
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%dh %dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
	case d >= time.Minute:
		return fmt.Sprintf("%dm %ds", int(d/time.Minute), int(d%time.Minute/time.Second))
	}
	return fmt.Sprintf("%ds", int(d.Round(time.Second)/time.Second))
}
