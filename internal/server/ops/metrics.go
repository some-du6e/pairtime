// SPDX-License-Identifier: AGPL-3.0-or-later

package ops

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Registry holds metrics and writes them in the Prometheus text exposition
// format, version 0.0.4. It is a few dozen lines of the standard library
// instead of the Prometheus client module, which pairtimed would otherwise
// need for nothing else.
type Registry struct {
	mu      sync.Mutex
	metrics []metric
	names   map[string]bool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{names: make(map[string]bool)}
}

type metric struct {
	name, help, kind string
	labels           string // rendered constant labels, such as `{version="v0.1.0"}`, or ""
	write            func(w io.Writer, name, labels string)
}

// metricName is what Prometheus accepts as a metric name.
var metricName = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)

// labelName is what Prometheus accepts as a label name.
var labelName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func (r *Registry) add(m metric) {
	if !metricName.MatchString(m.name) {
		panic(fmt.Sprintf("ops: bad metric name %q", m.name))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.names[m.name] {
		panic(fmt.Sprintf("ops: metric %s registered twice", m.name))
	}
	r.names[m.name] = true
	r.metrics = append(r.metrics, m)
	slices.SortFunc(r.metrics, func(a, b metric) int { return strings.Compare(a.name, b.name) })
}

// Counter only goes up.
type Counter struct{ v atomic.Uint64 }

// Inc adds one.
func (c *Counter) Inc() { c.v.Add(1) }

// Add adds n.
func (c *Counter) Add(n uint64) { c.v.Add(n) }

// Value returns the count.
func (c *Counter) Value() uint64 { return c.v.Load() }

// Gauge goes up and down.
type Gauge struct{ v atomic.Int64 }

// Set sets the value.
func (g *Gauge) Set(v int64) { g.v.Store(v) }

// Add adds d, which may be negative.
func (g *Gauge) Add(d int64) { g.v.Add(d) }

// Value returns the value.
func (g *Gauge) Value() int64 { return g.v.Load() }

// Histogram counts observations into buckets.
type Histogram struct {
	bounds []float64       // upper bounds, ascending, without +Inf
	counts []atomic.Uint64 // per bucket, not cumulative; the last is +Inf
	sum    atomic.Uint64   // float64 bits
	count  atomic.Uint64
}

// Observe records one value.
func (h *Histogram) Observe(v float64) {
	i, _ := slices.BinarySearch(h.bounds, v) // the first bound >= v
	h.counts[i].Add(1)
	for {
		old := h.sum.Load()
		if h.sum.CompareAndSwap(old, math.Float64bits(math.Float64frombits(old)+v)) {
			break
		}
	}
	h.count.Add(1)
}

// Count returns how many values were observed.
func (h *Histogram) Count() uint64 { return h.count.Load() }

// DurationBuckets are Prometheus's default buckets, in seconds.
var DurationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// Counter registers a counter. Its name should end in _total.
func (r *Registry) Counter(name, help string) *Counter {
	c := &Counter{}
	r.add(metric{name: name, help: help, kind: "counter", write: func(w io.Writer, name, labels string) {
		fmt.Fprintf(w, "%s%s %d\n", name, labels, c.Value())
	}})
	return c
}

// Gauge registers a gauge.
func (r *Registry) Gauge(name, help string) *Gauge {
	g := &Gauge{}
	r.add(metric{name: name, help: help, kind: "gauge", write: func(w io.Writer, name, labels string) {
		fmt.Fprintf(w, "%s%s %d\n", name, labels, g.Value())
	}})
	return g
}

// GaugeFunc registers a gauge whose value fn reads at each scrape.
func (r *Registry) GaugeFunc(name, help string, fn func() float64) {
	r.add(metric{name: name, help: help, kind: "gauge", write: func(w io.Writer, name, labels string) {
		fmt.Fprintf(w, "%s%s %s\n", name, labels, formatFloat(fn()))
	}})
}

// Info registers a gauge that is always 1 and carries labels, such as the
// build's version.
func (r *Registry) Info(name, help string, labels map[string]string) {
	r.add(metric{name: name, help: help, kind: "gauge", labels: renderLabels(labels), write: func(w io.Writer, name, labels string) {
		fmt.Fprintf(w, "%s%s 1\n", name, labels)
	}})
}

// Histogram registers a histogram with the given upper bounds, which must
// ascend.
func (r *Registry) Histogram(name, help string, bounds []float64) *Histogram {
	if !slices.IsSorted(bounds) || len(bounds) == 0 || slices.Contains(bounds, math.Inf(1)) {
		panic(fmt.Sprintf("ops: histogram %s needs ascending finite bounds", name))
	}
	h := &Histogram{bounds: slices.Clone(bounds), counts: make([]atomic.Uint64, len(bounds)+1)}
	r.add(metric{name: name, help: help, kind: "histogram", write: func(w io.Writer, name, _ string) {
		var cum uint64
		for i, b := range h.bounds {
			cum += h.counts[i].Load()
			fmt.Fprintf(w, "%s_bucket{le=%q} %d\n", name, formatFloat(b), cum)
		}
		cum += h.counts[len(h.bounds)].Load()
		fmt.Fprintf(w, "%s_bucket{le=\"+Inf\"} %d\n", name, cum)
		fmt.Fprintf(w, "%s_sum %s\n", name, formatFloat(math.Float64frombits(h.sum.Load())))
		fmt.Fprintf(w, "%s_count %d\n", name, cum)
	}})
	return h
}

// WriteText writes every metric, sorted by name, in the text format.
func (r *Registry) WriteText(w io.Writer) error {
	r.mu.Lock()
	ms := slices.Clone(r.metrics)
	r.mu.Unlock()
	bw := bufio.NewWriter(w)
	for _, m := range ms {
		fmt.Fprintf(bw, "# HELP %s %s\n", m.name, escapeHelp(m.help))
		fmt.Fprintf(bw, "# TYPE %s %s\n", m.name, m.kind)
		m.write(bw, m.name, m.labels)
	}
	return bw.Flush()
}

// formatFloat writes a float the way Prometheus parses one.
func formatFloat(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case math.IsNaN(v):
		return "NaN"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// escapeHelp escapes a HELP line: backslash and newline.
func escapeHelp(s string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`).Replace(s)
}

// renderLabels writes {a="1",b="2"} with names sorted and values escaped:
// backslash, double quote and newline.
func renderLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	names := make([]string, 0, len(labels))
	for n := range labels {
		if !labelName.MatchString(n) || strings.HasPrefix(n, "__") {
			panic(fmt.Sprintf("ops: bad label name %q", n))
		}
		names = append(names, n)
	}
	slices.Sort(names)
	esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	var b strings.Builder
	b.WriteByte('{')
	for i, n := range names {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `%s="%s"`, n, esc.Replace(labels[n]))
	}
	b.WriteByte('}')
	return b.String()
}
