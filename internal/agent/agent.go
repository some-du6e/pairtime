// SPDX-License-Identifier: AGPL-3.0-or-later

// Package agent is `pairtime agent`, the per-user background process that
// owns every store on the device (DESIGN 3.2, 3.3).
//
// This is the skeleton from WP-AG1: the single-instance lock, the data and
// config directories, config.toml, state.json, the scheduler that every
// timer goes through, and the HTTP server on 127.0.0.1. WP-AG2 added the
// socket that hooks hand their messages to and the inbox they spool to
// when the agent is down (packages ipc and inbox), and WP-OS8 the notifier
// that shows the coach's notification. The other components of
// DESIGN 3.3 arrive in later work packages, each as a package under
// internal/agent that Run starts.
package agent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/internal/agent/ipc"
	"github.com/some-du6e/pairtime/internal/agent/localhttp"
	"github.com/some-du6e/pairtime/internal/agent/notifier"
	"github.com/some-du6e/pairtime/internal/agent/sched"
	"github.com/some-du6e/pairtime/internal/agent/state"
	"github.com/some-du6e/pairtime/internal/version"
)

// ErrAlreadyRunning means another agent holds the lock on the same data
// directory. `pairtime agent` exits with status 3 when Run returns it.
var ErrAlreadyRunning = errors.New("another pairtime agent is already running for this data directory")

// Options configure Run.
type Options struct {
	Dirs home.Dirs
	// Log gets the agent's log. Nil discards it.
	Log *slog.Logger
	// Ports, when set, replaces the ports the HTTP server tries, which
	// otherwise come from localhttp.Candidates. Tests use []int{0}.
	Ports []int
	// Ready, when set, is called once the socket and the HTTP server are
	// listening and port.json is written.
	Ready func()
	// ForcePipe makes the agent take hook messages on the named pipe
	// instead of the socket. It works on Windows only, where tests use it
	// to run the fallback (WP-AG2).
	ForcePipe bool
	// DrainEvery, when set, replaces how often the agent drains the inbox,
	// once a minute otherwise. Tests set it so they needn't wait a minute.
	DrainEvery time.Duration
}

// linked keeps heavyModules' entry points reachable (see heavy_linux.go).
// Run fills it once per process, so tests can run several agents.
var (
	linkOnce sync.Once
	linked   []any
)

// shutdownWait is how long in-flight HTTP requests get to finish at stop.
const shutdownWait = 2 * time.Second

type agent struct {
	dirs    home.Dirs
	log     *slog.Logger
	started time.Time
	sched   *sched.Scheduler
	state   *state.Store
	cfg     atomic.Pointer[config.Config]
	port    int
	journal *inbox.Journal // nil when it couldn't be opened
	ipcSrv  *ipc.Server    // nil when hooks can't reach the agent
	// notifier shows the coach's notification when a work session ends
	// (WP-OS8); the coach calls it (WP-CO1).
	notifier *notifier.Notifier

	stop     chan struct{} // closed when a client asks the agent to stop
	stopOnce sync.Once

	mu       sync.Mutex
	problems map[string]string // what doctor should hear about, by source
}

// Run runs the agent until ctx is done or a client asks it to stop over
// HTTP, then stops cleanly and returns nil. It returns ErrAlreadyRunning at
// once when another agent holds the data directory.
func Run(ctx context.Context, opts Options) error {
	log := opts.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if err := opts.Dirs.Create(); err != nil {
		return err
	}
	lock, err := home.Acquire(opts.Dirs.Data)
	if errors.Is(err, home.ErrLocked) {
		return ErrAlreadyRunning
	}
	if err != nil {
		return fmt.Errorf("locking %s: %w", opts.Dirs.Data, err)
	}
	defer lock.Release()

	a := &agent{
		dirs:     opts.Dirs,
		log:      log,
		started:  time.Now(),
		sched:    sched.New(),
		stop:     make(chan struct{}),
		problems: make(map[string]string),
	}
	linkOnce.Do(func() { linked = heavyModules() })

	// Each part that starts adds the step that stops it, and they stop in
	// reverse order: the HTTP server, port.json, the socket and the inbox,
	// the config watch, the scheduler, the notifier, state.json.
	var closers []func()
	defer func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}()

	a.loadConfig(true)
	markStopped, err := a.markStarted()
	if err != nil {
		return err
	}
	closers = append(closers, markStopped)

	// Idle, the notifier holds no goroutine, timer or connection. It stops
	// before state.json is saved, so what it remembers is in the file.
	a.notifier = notifier.New(notifier.Options{
		Settings: func() config.Notifications { return a.config().Notifications },
		Memory:   a.state,
		Log:      log.With("component", "notifier"),
		Problem:  func(msg string) { a.setProblem("notifications", msg) },
	})
	closers = append(closers, a.notifier.Close)

	schedCtx, stopSched := context.WithCancel(context.Background())
	schedDone := make(chan struct{})
	go func() {
		a.sched.Run(schedCtx)
		close(schedDone)
	}()
	closers = append(closers, func() {
		stopSched()
		<-schedDone
	})

	closers = append(closers, a.watchConfig())
	closers = append(closers, a.startIPC(opts))

	ln, pf, err := a.listen(opts.Ports)
	if err != nil {
		return err
	}
	a.port = pf.Port
	closers = append(closers, func() {
		pf.PID = 0
		if err := localhttp.WritePortFile(a.dirs.PortFile(), pf); err != nil {
			a.log.Warn("couldn't mark port.json stopped", "err", err)
		}
	})

	srv := &http.Server{
		Handler:           a.handler(pf.Token),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	closers = append(closers, func() {
		sctx, cancel := context.WithTimeout(context.Background(), shutdownWait)
		defer cancel()
		if err := srv.Shutdown(sctx); err != nil {
			srv.Close()
		}
	})

	if opts.Ready != nil {
		opts.Ready()
	}
	log.Info("pairtime agent started", "version", version.Get().String(),
		"data", a.dirs.Data, "port", a.port, "mode", a.config().Mode)

	select {
	case <-ctx.Done():
		log.Info("pairtime agent stopping", "reason", context.Cause(ctx))
	case <-a.stop:
		log.Info("pairtime agent stopping", "reason", "a client asked it to")
	case err := <-served:
		return fmt.Errorf("the local HTTP server stopped: %w", err)
	}
	return nil
}

// config returns the settings in force.
func (a *agent) config() config.Config {
	return *a.cfg.Load()
}

// setProblem records a problem from source, or clears it when msg is "".
func (a *agent) setProblem(source, msg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if msg == "" {
		delete(a.problems, source)
	} else {
		a.problems[source] = msg
	}
}

func (a *agent) currentProblems() map[string]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.problems) == 0 {
		return nil
	}
	out := make(map[string]string, len(a.problems))
	for k, v := range a.problems {
		out[k] = v
	}
	return out
}

// loadConfig reads config.toml and puts it in force. The first start
// writes the default file when there is none; a reload after an edit only
// reads, so a file the teen deleted stays deleted and the defaults apply.
func (a *agent) loadConfig(first bool) {
	name := a.dirs.ConfigFile()
	cfg, problems, err := config.Load(name)
	switch {
	case errors.Is(err, fs.ErrNotExist) && first:
		if err := config.WriteDefault(name); err != nil {
			problems = append(problems, fmt.Sprintf("couldn't write the default config.toml: %v", err))
		}
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		problems = append(problems, fmt.Sprintf("couldn't read config.toml, so every setting has its default and notifications are off: %v", err))
	}
	a.cfg.Store(&cfg)
	a.setProblem("config", strings.Join(problems, "; "))
	for _, p := range problems {
		a.log.Warn(p)
	}
}

// agentKey is the agent's own key in state.json.
const agentKey = "agent"

// agentState is what state.json keeps about the agent's own runs.
type agentState struct {
	Version string    `json:"version"` // of the binary that started last
	Starts  uint64    `json:"starts"`
	Started time.Time `json:"started"`
	// Stopped is when the last clean stop was. It is before Started while
	// an agent runs, and after one crashed or was killed.
	Stopped time.Time `json:"stopped,omitzero"`
}

// markStarted opens state.json and records this start. It returns the step
// that records the stop.
func (a *agent) markStarted() (func(), error) {
	st, problem, err := state.Open(a.dirs.StateFile())
	if err != nil {
		return nil, fmt.Errorf("reading state.json: %w", err)
	}
	a.state = st
	a.setProblem("state", problem)
	var s agentState
	if _, err := st.Get(agentKey, &s); err != nil {
		s = agentState{} // rebuilt, like any damaged state
	}
	s.Version = version.Get().Version
	s.Starts++
	s.Started = a.started.UTC()
	if err := a.saveAgentState(s); err != nil {
		return nil, fmt.Errorf("writing state.json: %w", err)
	}
	return func() {
		s.Stopped = time.Now().UTC()
		if err := a.saveAgentState(s); err != nil {
			a.log.Warn("couldn't record the stop in state.json", "err", err)
		}
	}, nil
}

func (a *agent) saveAgentState(s agentState) error {
	if err := a.state.Put(agentKey, s); err != nil {
		return err
	}
	return a.state.Save()
}

// listen binds the HTTP port and writes port.json. The token in port.json
// is kept across restarts, and a new one is made only when there is none.
func (a *agent) listen(ports []int) (net.Listener, localhttp.PortFile, error) {
	name := a.dirs.PortFile()
	old, err := localhttp.ReadPortFile(name)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		a.log.Warn("port.json is damaged, so the agent makes a new token", "err", err)
	}
	token := old.Token
	if !localhttp.ValidToken(token) {
		token = localhttp.NewToken()
	}
	if ports == nil {
		ports = localhttp.Candidates(a.config().HTTP.Port, old.Port)
	}
	ln, err := localhttp.Listen(ports)
	if err != nil {
		return nil, localhttp.PortFile{}, fmt.Errorf("the local HTTP server can't listen: %w", err)
	}
	port := localhttp.Port(ln)
	if len(ports) > 0 && ports[0] != 0 && port != ports[0] {
		a.setProblem("port", fmt.Sprintf("port %d was taken, so the agent listens on %d", ports[0], port))
	}
	pf := localhttp.PortFile{Port: port, Token: token, PID: os.Getpid()}
	if err := localhttp.WritePortFile(name, pf); err != nil {
		ln.Close()
		return nil, localhttp.PortFile{}, fmt.Errorf("writing port.json: %w", err)
	}
	return ln, pf, nil
}
