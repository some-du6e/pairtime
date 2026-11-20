// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/localhttp"
	"github.com/some-du6e/pairtime/internal/version"
	"github.com/some-du6e/pairtime/internal/webui"
)

// handler is the agent's HTTP server: the UI at /, which needs no token
// because it is the same static files for everyone, and the API under
// /v1/, which takes the bearer token from port.json or the dashboard's
// session cookie. `pairtime dashboard` gets a one-time sign-in link from
// POST /v1/dashboard/link with the token alone, and GET /signin trades it
// for the cookie (DESIGN 3.9). Guard checks the Host and Origin of
// everything (CP 6.3).
func (a *agent) handler(token string) http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/status", a.serveStatus)
	api.HandleFunc("POST /v1/agent/stop", a.serveStop)
	api.HandleFunc("GET /v1/days", a.days.ServeDays)

	mux := http.NewServeMux()
	mux.Handle("POST /v1/dashboard/link", localhttp.RequireToken(token, http.HandlerFunc(a.serveLink)))
	mux.Handle("/v1/", localhttp.RequireAuth(token, a.sessions, api))
	mux.Handle("GET /signin", localhttp.SignIn(a.sessions))
	mux.Handle("/", readOnly(http.FileServerFS(webui.FS())))
	return localhttp.Guard(a.port, mux)
}

// readOnly passes GET and HEAD and answers anything else with 405.
func readOnly(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// statusReply is GET /v1/status: what `pairtime doctor` and footprint.yml
// read from a running agent.
type statusReply struct {
	Version version.Info `json:"version"`
	PID     int          `json:"pid"`
	Started time.Time    `json:"started"`
	UptimeS int64        `json:"uptime_s"`
	Mode    config.Mode  `json:"mode"`
	Port    int          `json:"port"`
	// Wakeups counts the scheduler's timer firings since the start.
	Wakeups  uint64            `json:"wakeups"`
	Jobs     []jobReply        `json:"jobs"`
	IPC      ipcReply          `json:"ipc"`
	Problems map[string]string `json:"problems,omitempty"`
}

type jobReply struct {
	Name   string  `json:"name"`
	EveryS float64 `json:"every_s,omitempty"`
	Runs   uint64  `json:"runs"`
}

func (a *agent) serveStatus(w http.ResponseWriter, r *http.Request) {
	jobs := []jobReply{}
	for _, j := range a.sched.Jobs() {
		jobs = append(jobs, jobReply{Name: j.Name, EveryS: j.Every.Seconds(), Runs: j.Runs})
	}
	writeJSON(w, http.StatusOK, statusReply{
		Version:  version.Get(),
		PID:      os.Getpid(),
		Started:  a.started.UTC(),
		UptimeS:  int64(time.Since(a.started) / time.Second),
		Mode:     a.config().Mode,
		Port:     a.port,
		Wakeups:  a.sched.Wakeups(),
		Jobs:     jobs,
		IPC:      a.ipcStatus(),
		Problems: a.currentProblems(),
	})
}

// linkReply is POST /v1/dashboard/link.
type linkReply struct {
	URL        string `json:"url"`
	ExpiresInS int    `json:"expires_in_s"`
}

// serveLink answers POST /v1/dashboard/link with a one-time URL that signs
// a browser in to the dashboard, for `pairtime dashboard` to open.
func (a *agent) serveLink(w http.ResponseWriter, r *http.Request) {
	u := url.URL{
		Scheme:   "http",
		Host:     net.JoinHostPort("127.0.0.1", strconv.Itoa(a.port)),
		Path:     "/signin",
		RawQuery: url.Values{"code": {a.sessions.NewCode()}}.Encode(),
	}
	writeJSON(w, http.StatusCreated, linkReply{URL: u.String(), ExpiresInS: int(localhttp.CodeTTL / time.Second)})
}

// serveStop answers POST /v1/agent/stop and then stops the agent, the same
// way on every OS. Windows has no signal to send a process that has no
// console, and footprint.yml uses this to check a clean stop.
func (a *agent) serveStop(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "stopping"})
	a.stopOnce.Do(func() { close(a.stop) })
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
