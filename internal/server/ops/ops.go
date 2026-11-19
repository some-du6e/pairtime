// SPDX-License-Identifier: AGPL-3.0-or-later

// Package ops serves pairtimed's operations endpoints (DESIGN 10.4):
//
//   - GET /healthz answers 200 while the process is up. It is the one ops
//     endpoint also on the public listener, because probe.yml and doctor
//     fetch it through the tunnel (WP-OP1).
//   - GET /readyz runs the readiness checks and answers 200 when they all
//     pass, 503 when one fails, with a JSON body saying which. pairtimed's
//     first check writes a row through the writer goroutine, so "ready"
//     means the database takes writes and the writer is committing. WP-OP1
//     and WP-SV15 add Litestream lag and object storage.
//   - GET /metrics writes the Prometheus text format: ingest rate, batch
//     latency, open streams and writer queue depth, among others.
//
// /readyz and /metrics are only on the private ops listener, which the
// tunnel never exposes.
package ops

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// DefaultCheckTimeout bounds each readiness check.
const DefaultCheckTimeout = 2 * time.Second

// Check is one readiness check. Run returns nil when the check passes.
type Check struct {
	Name string
	Run  func(ctx context.Context) error
}

// Healthz answers 200 "ok" while the process can answer at all.
func Healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte("ok\n"))
}

// Readiness is the /readyz body.
type Readiness struct {
	Ready  bool          `json:"ready"`
	Checks []CheckResult `json:"checks"`
}

// CheckResult is one check's outcome.
type CheckResult struct {
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// Ms is how long the check took, in milliseconds.
	Ms int64 `json:"ms"`
}

// Readyz runs every check at once, each bounded by timeout, and answers
// 200 when all pass or 503 when any fails.
func Readyz(checks []Check, timeout time.Duration) http.HandlerFunc {
	if timeout <= 0 {
		timeout = DefaultCheckTimeout
	}
	return func(w http.ResponseWriter, r *http.Request) {
		res := Readiness{Ready: true, Checks: make([]CheckResult, len(checks))}
		var wg sync.WaitGroup
		for i, c := range checks {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(r.Context(), timeout)
				defer cancel()
				start := time.Now()
				err := runCheck(ctx, c)
				cr := CheckResult{Name: c.Name, OK: err == nil, Ms: time.Since(start).Milliseconds()}
				if err != nil {
					cr.Error = err.Error()
				}
				res.Checks[i] = cr
			}()
		}
		wg.Wait()
		for _, c := range res.Checks {
			if !c.OK {
				res.Ready = false
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if !res.Ready {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		json.NewEncoder(w).Encode(res)
	}
}

// runCheck runs c, but returns when ctx ends even if c doesn't.
func runCheck(ctx context.Context, c Check) error {
	errc := make(chan error, 1)
	go func() { errc <- c.Run(ctx) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Metrics serves the registry in the Prometheus text format.
func Metrics(reg *Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		reg.WriteText(w)
	}
}

// Handler is the private ops listener's handler: /healthz, /readyz and
// /metrics, GET and HEAD only.
func Handler(reg *Registry, checks []Check, timeout time.Duration) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", Healthz)
	mux.Handle("GET /readyz", Readyz(checks, timeout))
	mux.Handle("GET /metrics", Metrics(reg))
	return mux
}
