// SPDX-License-Identifier: AGPL-3.0-or-later

package ops

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestWriteText checks the output against the Prometheus text exposition
// format, version 0.0.4: HELP and TYPE lines, cumulative le buckets ending
// in +Inf, _sum and _count, sorted metric names, and escaping of HELP text
// (backslash, newline) and label values (also the double quote).
func TestWriteText(t *testing.T) {
	r := NewRegistry()
	r.Info("e_info", "Build \"info\" with a \\ backslash\nand a second line.", map[string]string{"version": `v"1\`, "commit": "abc"})
	h := r.Histogram("d_seconds", "Durations.", []float64{0.005, 0.25, 10})
	r.GaugeFunc("c_func", "A gauge read at each scrape.", func() float64 { return 1.5 })
	g := r.Gauge("b_gauge", "A gauge.")
	c := r.Counter("a_total", "Things counted.")
	c.Add(2)
	c.Inc()
	g.Set(-2)
	// Binary fractions, so the sum is exact.
	for _, v := range []float64{0.00390625, 0.125, 7, 20} {
		h.Observe(v)
	}

	var b strings.Builder
	if err := r.WriteText(&b); err != nil {
		t.Fatal(err)
	}
	want := `# HELP a_total Things counted.
# TYPE a_total counter
a_total 3
# HELP b_gauge A gauge.
# TYPE b_gauge gauge
b_gauge -2
# HELP c_func A gauge read at each scrape.
# TYPE c_func gauge
c_func 1.5
# HELP d_seconds Durations.
# TYPE d_seconds histogram
d_seconds_bucket{le="0.005"} 1
d_seconds_bucket{le="0.25"} 2
d_seconds_bucket{le="10"} 3
d_seconds_bucket{le="+Inf"} 4
d_seconds_sum 27.12890625
d_seconds_count 4
# HELP e_info Build "info" with a \\ backslash\nand a second line.
# TYPE e_info gauge
e_info{commit="abc",version="v\"1\\"} 1
`
	if got := b.String(); got != want {
		t.Errorf("WriteText =\n%s\nwant\n%s", got, want)
	}
}

func TestRegistryRefuses(t *testing.T) {
	tests := []struct {
		name string
		add  func(r *Registry)
	}{
		{name: "a name registered twice", add: func(r *Registry) { r.Counter("x_total", ""); r.Gauge("x_total", "") }},
		{name: "a name Prometheus can't parse", add: func(r *Registry) { r.Counter("x-total", "") }},
		{name: "histogram bounds that don't ascend", add: func(r *Registry) { r.Histogram("x", "", []float64{1, 0.5}) }},
		{name: "a histogram without bounds", add: func(r *Registry) { r.Histogram("x", "", nil) }},
		{name: "a label name Prometheus reserves", add: func(r *Registry) { r.Info("x", "", map[string]string{"__name": "y"}) }},
	}
	for _, tt := range tests {
		t.Run(tt.name+" panics", func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("no panic")
				}
			}()
			tt.add(NewRegistry())
		})
	}
}

func TestHandler(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	pass := Check{Name: "database", Run: func(context.Context) error { return nil }}
	fail := Check{Name: "database", Run: func(context.Context) error { return errors.New("disk full") }}
	hang := Check{Name: "storage", Run: func(context.Context) error { <-block; return nil }}

	tests := []struct {
		name       string
		checks     []Check
		method     string
		path       string
		wantCode   int
		wantType   string // Content-Type prefix
		wantBody   string // regular expression
		wantNoBody string // regular expression the body must not match
	}{
		{name: "/healthz answers 200 ok", path: "/healthz", wantCode: 200, wantType: "text/plain", wantBody: `^ok\n$`},
		{name: "/healthz answers HEAD", method: http.MethodHead, path: "/healthz", wantCode: 200},
		{name: "/readyz answers 200 when every check passes", checks: []Check{pass}, path: "/readyz", wantCode: 200, wantType: "application/json", wantBody: `^\{"ready":true,"checks":\[\{"name":"database","ok":true,"ms":\d+\}\]\}`},
		{name: "/readyz answers 503 and names the failing check", checks: []Check{pass, fail}, path: "/readyz", wantCode: 503, wantBody: `"ready":false.*"name":"database","ok":false,"error":"disk full"`},
		{name: "/readyz gives up on a check that outlasts its timeout", checks: []Check{pass, hang}, path: "/readyz", wantCode: 503, wantBody: `"name":"storage","ok":false,"error":"context deadline exceeded"`},
		{name: "/readyz refuses POST", checks: []Check{pass}, method: http.MethodPost, path: "/readyz", wantCode: 405},
		{name: "/metrics serves the text format", path: "/metrics", wantCode: 200, wantType: "text/plain; version=0.0.4", wantBody: `(?m)^pairtime_writer_queue_depth 7$`},
		{name: "/metrics labels name no paths, prompts or URLs", path: "/metrics", wantCode: 200, wantNoBody: `(?m)^[a-z_]+\{[^}]*(path|prompt|url)=`},
		{name: "other paths are 404", path: "/", wantCode: 404},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := NewRegistry()
			NewSeries(reg, func() int { return 7 })
			h := Handler(reg, tt.checks, 50*time.Millisecond)
			method := tt.method
			if method == "" {
				method = http.MethodGet
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(method, tt.path, nil))
			resp := rec.Result()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != tt.wantCode {
				t.Errorf("%s %s = %d, want %d; body: %s", method, tt.path, resp.StatusCode, tt.wantCode, body)
			}
			if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, tt.wantType) {
				t.Errorf("Content-Type = %q, want it to start with %q", ct, tt.wantType)
			}
			if tt.wantBody != "" && !regexp.MustCompile(tt.wantBody).Match(body) {
				t.Errorf("body doesn't match %s:\n%s", tt.wantBody, body)
			}
			if tt.wantNoBody != "" && regexp.MustCompile(tt.wantNoBody).Match(body) {
				t.Errorf("body matches %s:\n%s", tt.wantNoBody, body)
			}
		})
	}
}

func TestSeries(t *testing.T) {
	reg := NewRegistry()
	s := NewSeries(reg, func() int { return 0 })
	s.ObserveCommit(30*time.Millisecond, 120, 2, nil)
	s.ObserveCommit(2*time.Second, 1, 0, errors.New("disk I/O error"))
	var b strings.Builder
	if err := reg.WriteText(&b); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		line string
	}{
		{name: "every transaction is counted", line: "pairtime_writer_commits_total 2"},
		{name: "a failed transaction is counted", line: "pairtime_writer_commit_errors_total 1"},
		{name: "ops rolled back alone are counted", line: "pairtime_writer_op_errors_total 2"},
		{name: "commit durations land in their buckets", line: `pairtime_writer_commit_duration_seconds_bucket{le="0.05"} 1`},
		{name: "rows per transaction land in their buckets", line: `pairtime_writer_batch_rows_bucket{le="250"} 2`},
		{name: "the build is labeled", line: "pairtime_build_info{"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(b.String(), tt.line) {
				t.Errorf("metrics lack %q:\n%s", tt.line, b.String())
			}
		})
	}
}
