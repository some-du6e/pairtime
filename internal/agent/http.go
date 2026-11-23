// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/localhttp"
	"github.com/some-du6e/pairtime/internal/version"
	"github.com/some-du6e/pairtime/internal/webui"
)

// handler is the agent's HTTP server: the UI at /, which needs no token
// because it is the same static files for everyone, and the API under
// /v1/. The routes the dashboard reads, and the one change it makes,
// turning a project's name on or off on the owner's dashboard
// (projectnames.go), take the bearer token from port.json or the
// dashboard's session cookie, which for a change also needs the
// dashboard's Origin (localhttp.RequireAuth); POST /v1/agent/stop takes
// the token alone, so the cookie can't stop the agent. `pairtime
// dashboard` gets a one-time sign-in link over the socket (dashboard.go),
// and GET /signin trades it for the cookie (DESIGN 3.9). Guard checks the
// Host and Origin of everything (CP 6.3).
func (a *agent) handler(token string) http.Handler {
	reads := http.NewServeMux()
	reads.HandleFunc("GET /v1/status", a.serveStatus)
	reads.HandleFunc("GET /v1/days/{date}", a.serveDay)
	reads.HandleFunc("GET /v1/sessions/last", a.serveLastSession)
	reads.HandleFunc("GET /v1/projects", a.serveProjects)
	reads.HandleFunc("PUT /v1/projects/{key}/name", a.serveProjectName)

	stop := http.NewServeMux()
	stop.HandleFunc("POST /v1/agent/stop", a.serveStop)

	mux := http.NewServeMux()
	mux.Handle("/v1/agent/stop", localhttp.RequireToken(token, stop))
	mux.Handle("/v1/", localhttp.RequireAuth(token, a.sessions, reads))
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
	Wakeups uint64     `json:"wakeups"`
	Jobs    []jobReply `json:"jobs"`
	IPC     ipcReply   `json:"ipc"`
	// Signals says where idle time, focus, power and the lock state come
	// from, for doctor's idle and focus lines (DESIGN 5.9, D-design-5.9).
	Signals  signalsReply  `json:"signals"`
	Adapters adaptersReply `json:"adapters"`
	// Ledger is what the ledger writer says of its chain, absent while the
	// ledger isn't open.
	Ledger *ledgerReply `json:"ledger,omitempty"`
	// Footprint is what the agent costs the computer, as the OS reports it,
	// for doctor --report (DESIGN 5.9, 9.7).
	Footprint footprintReply    `json:"footprint"`
	Problems  map[string]string `json:"problems,omitempty"`
}

// ledgerReply is what the ledger writer says of its chain.
type ledgerReply struct {
	// LateLocal counts the records the writer dropped over the chain's
	// life: made while config.toml said local-only, they reached it only
	// after it had sealed a batch online since, so they never upload
	// (ledger.Status.LateLocal; D-late-local). It counts every writer's
	// drops: the adapters', presence's and the git watcher's.
	LateLocal uint64 `json:"late_local"`
}

type jobReply struct {
	Name   string  `json:"name"`
	EveryS float64 `json:"every_s,omitempty"`
	Runs   uint64  `json:"runs"`
}

func (a *agent) serveStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.status())
}

// status is GET /v1/status, and the agent's answer to the CLI's status
// question (ipc.AskStatus), which `pairtime doctor` asks.
func (a *agent) status() statusReply {
	jobs := []jobReply{}
	for _, j := range a.sched.Jobs() {
		jobs = append(jobs, jobReply{Name: j.Name, EveryS: j.Every.Seconds(), Runs: j.Runs})
	}
	return statusReply{
		Version: version.Get(),
		PID:     os.Getpid(),
		Started: a.started.UTC(),
		UptimeS: int64(time.Since(a.started) / time.Second),
		Mode:    a.uploadMode(),
		// The socket answers the status question before Run has the port,
		// so it reads the one the HTTP server published (agent.linkPort).
		Port:      int(a.linkPort.Load()),
		Wakeups:   a.sched.Wakeups(),
		Jobs:      jobs,
		IPC:       a.ipcStatus(),
		Signals:   a.signalsNow(),
		Adapters:  a.adaptersHost().status(),
		Ledger:    a.ledgerStatus(),
		Footprint: footprintNow(),
		Problems:  a.currentProblems(),
	}
}

// ledgerStatus is what the ledger writer says of its chain for the
// status, nil while the ledger isn't open.
func (a *agent) ledgerStatus() *ledgerReply {
	w := a.writer.Load()
	if w == nil {
		return nil
	}
	return &ledgerReply{LateLocal: w.Status().LateLocal}
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
