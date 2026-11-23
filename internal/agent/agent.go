// SPDX-License-Identifier: AGPL-3.0-or-later

// Package agent is `pairtime agent`, the per-user background process that
// owns every store on the device (DESIGN 3.2, 3.3).
//
// This is the skeleton from WP-AG1: the single-instance lock, the data and
// config directories, config.toml, state.json, the scheduler that every
// timer goes through, and the HTTP server on 127.0.0.1. WP-AG2 added the
// socket that hooks hand their messages to and the inbox they spool to
// when the agent is down (packages ipc and inbox), WP-AG3 what the agent
// makes of each hook call (hooks.go, package hookpath), WP-OS8 the
// notifier that shows the coach's notification, WP-AG4 the scorer loop
// and the stores it writes (packages scorer and store), and WP-AG5 the
// uploader (package uploader). The other
// components of DESIGN 3.3 arrive in later work packages, each as a
// package under internal/agent that Run starts.
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
	"github.com/some-du6e/pairtime/internal/agent/focus"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/internal/agent/ipc"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/localhttp"
	"github.com/some-du6e/pairtime/internal/agent/notifier"
	"github.com/some-du6e/pairtime/internal/agent/presence"
	"github.com/some-du6e/pairtime/internal/agent/procs"
	"github.com/some-du6e/pairtime/internal/agent/sched"
	"github.com/some-du6e/pairtime/internal/agent/scorer"
	"github.com/some-du6e/pairtime/internal/agent/state"
	"github.com/some-du6e/pairtime/internal/agent/store"
	"github.com/some-du6e/pairtime/internal/agent/tailers"
	"github.com/some-du6e/pairtime/internal/agent/uploader"
	"github.com/some-du6e/pairtime/internal/agent/vault"
	"github.com/some-du6e/pairtime/internal/install"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/version"
	"github.com/some-du6e/pairtime/pkg/params"
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
	// KeyStores, when not nil, replaces the OS's secret store that keeps
	// the device key, as keys.Options.Stores does. Tests pass an empty
	// slice, which keeps K_dev in a key file in the data directory and
	// off the OS's store.
	KeyStores []keys.Store
	// LedgerRetry, when set, replaces how long the agent first waits
	// before it tries again to open a ledger that didn't open for a reason
	// that can pass, such as a locked keyring; 15 s otherwise. Tests set
	// it so they needn't wait.
	LedgerRetry time.Duration
	// Zone, when set, replaces time.Local as the time zone the agent
	// counts days in. Tests set it.
	Zone *time.Location
	// Mac is the macOS notification loop, which cmd/pairtime runs on the
	// main thread while Run runs on another goroutine. It gives presence
	// its suspends, resumes, locks and unlocks, and focus its app
	// activations (WP-OS9). Nil means none runs: presence still samples
	// idle time, and GET /v1/status says that power, locks and focus go
	// unseen. Other OSes ignore it.
	Mac *MacLoop
	// Walk, when set, replaces how the agent reads the parent chain of a
	// hook connection's peer, which is hookpath.ReadChain otherwise
	// (ipc.Options.Walk). Tests use it to hand captured calls in from
	// under the processes the capture recorded.
	Walk func(pid int) []platform.Proc
	// Env, when set, replaces the machine whose harness logs the tailers
	// read (adapters.LogReader.LogRoots), which is this one otherwise: the
	// home folder and environment the agent runs with. Tests point it at
	// a folder of their own.
	Env *install.Env
	// Procs, when set, replaces the process table the harness process scan
	// reads (procs.Options.Read), which is this machine's otherwise. Tests
	// give one of their own.
	Procs func() ([]platform.Proc, error)
	// Lookback starts lookback's reader, a child process that reads the
	// harnesses' logs from before pairtime at low priority
	// (tailers.ServeLookback), as `pairtime agent` has it run `pairtime
	// lookback`. Nil reads none of them.
	Lookback func() (tailers.Child, error)
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
	// linkPort is port while the HTTP server listens, and 0 before and
	// after, for the sign-in links the socket hands out (dashboard.go),
	// which can be asked for before the port is known.
	linkPort atomic.Int32
	procs    *procs.Scanner // the harness process scan (DESIGN 3.3)
	journal  *inbox.Journal // nil when it couldn't be opened
	// ipcSrv is the socket's or the pipe's server, nil while hooks can't
	// reach the agent.
	ipcSrv atomic.Pointer[ipc.Server]
	// spoolKick asks the drain goroutine for a drain of the inbox while it
	// runs (startIPC, drainSoon), and is nil otherwise. spoolNoDrain is set
	// when no drain runs in this run, as when the journal couldn't open.
	spoolKick    atomic.Pointer[chan struct{}]
	spoolNoDrain atomic.Bool
	// spoolBusySince is when the asks in a row that found a spool file
	// busy began, nil while none did (spoolHolds).
	spoolBusySince atomic.Pointer[time.Time]
	// linkedSeen is the link modes accounted for (modes.linked) once the
	// writer has what modes saw of the stretch it ended, and modesLocal is
	// modes.local, both for the writer's Online, which takes no lock
	// (writerOnline, seenLinkLocked), and seeing is set while seeSoon's
	// goroutine runs.
	linkedSeen atomic.Int64
	modesLocal atomic.Bool
	seeing     atomic.Bool
	// notifier shows the coach's notification when a work session ends
	// (WP-OS8). The coach calls its Notify, and its UserPresent on unlock
	// and resume (WP-CO1).
	notifier *notifier.Notifier

	presence *presence.Sampler // presence and power (DESIGN 3.3)
	focus    *focus.Watcher    // the window in front (DESIGN 3.3)
	// kApp is K_app, which keys os.focus's app_hash, once keyFocus has
	// derived it from the device key.
	kApp atomic.Pointer[[]byte]
	// hooked are the processes hooks came from, with their sessions, for
	// focus (hooks.go), and kSession is K_session, which keys those
	// sessions, once keySessions has derived it from the device key.
	hooked   *hookTies
	kSession atomic.Pointer[[]byte]
	// adapt runs the harness adapters and the git adapter, which turn the
	// hook calls the journal stores, and the commits in the sessions'
	// repos, into ledger records (adapters.go, git.go; WP-AG7).
	// adaptersHost makes it on first use.
	adapt     *adapterHost
	adaptOnce sync.Once
	// ledgerIn takes presence's and focus's records, and presence's
	// seals, to the ledger writer once it opens (WP-LG5).
	ledgerIn *ledger.Presence
	// signals says where each OS signal comes from, for GET /v1/status
	// (openOS).
	signals     func() signalsReply
	keyStores   []keys.Store  // Options.KeyStores
	ledgerRetry time.Duration // the first wait before the ledger opens again
	// table is the parameter table the agent scores with, which the
	// ledger writer and presence get too.
	table *params.Table
	// scorer steps pkg/score over every sealed batch and writes buckets,
	// which the local API reads (WP-AG4). Both are nil when the bucket
	// store didn't open.
	scorer  *scorer.Loop
	buckets *store.Buckets
	// upload sends the device's tier-1 data once the ledger is open and
	// the device is signed in (WP-AG5). It is nil before the ledger opens.
	upload atomic.Pointer[uploader.Uploader]
	// writer is the ledger writer once it is published, and modes what the
	// agent sees of config.toml's mode, which it tells the writer
	// (noteMode, publishWriter).
	writer atomic.Pointer[ledger.Writer]
	modes  modes
	// vault keeps the content the ledger commits to, from when the writer
	// that keeps content in it opens until closeVault (WP-LG6). vaultMu
	// guards it, and vaultWork counts the runs of dropText applyPrivacy
	// starts, which dropMu orders.
	vaultMu   sync.Mutex
	vault     *vault.Vault
	vaultWork sync.WaitGroup
	dropMu    sync.Mutex
	// early are the batches the ledger writer sealed before upload was
	// set, for handEarly; earlyMu orders them with it.
	earlyMu sync.Mutex
	early   []ledger.Batch
	// signIn keeps the sign-in between uploads (credentials).
	signIn signInCache
	// loc is Options.Zone (zone).
	loc *time.Location

	// sessions are the dashboard's sign-ins (DESIGN 3.9). The dashboard
	// reads the same day and session routes as the CLI views, which
	// scorer.go serves from the scorer's stores (WP-AG4; D-shadow-shape).
	sessions *localhttp.Sessions

	stop     chan struct{} // closed when a client asks the agent to stop
	stopOnce sync.Once
	// drained is closed once the inbox's first drain has handed its calls
	// to the adapter host, after which the tailers may start (logs.go).
	drained   chan struct{}
	drainOnce sync.Once

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
		dirs:        opts.Dirs,
		log:         log,
		started:     time.Now(),
		sched:       sched.New(),
		stop:        make(chan struct{}),
		drained:     make(chan struct{}),
		problems:    make(map[string]string),
		keyStores:   opts.KeyStores,
		ledgerRetry: defaultLedgerRetry,
		table:       newestTable(),
		loc:         opts.Zone,
		sessions:    localhttp.NewSessions(nil),
		hooked:      newHookTies(),
	}
	if opts.LedgerRetry > 0 {
		a.ledgerRetry = opts.LedgerRetry
	}
	linkOnce.Do(func() { linked = heavyModules() })
	if err := optIntoEcoQoS(); err != nil {
		log.Info("the agent runs without EcoQoS", "err", err)
	}

	// Each part that starts adds the step that stops it, and they stop in
	// reverse order: the HTTP server, port.json, the socket and the inbox,
	// focus, the process scan, the config watch, presence, the OS's
	// sources, the ledger, the scorer, which scores the ledger's last seal,
	// the scheduler, the notifier, state.json.
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
	closers = append(closers, a.startScorer())
	closers = append(closers, a.startLedger())
	src := a.openOS(opts)
	a.signals = src.signals
	if src.close != nil {
		closers = append(closers, src.close)
	}
	closers = append(closers, a.startPresence(src.presence))

	closers = append(closers, a.watchConfig())
	a.newFocus(src.focus)
	closers = append(closers, a.startProcs(opts))
	closers = append(closers, a.startFocus())
	closers = append(closers, a.startAdapters())
	closers = append(closers, a.startIPC(opts))
	// After the socket, and stopped before it: the tailers' lines go to
	// the adapters after the hook calls the inbox's first drain brings.
	closers = append(closers, a.startTailers(opts))

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
	// The listener is bound, so a browser sent to a link from now on waits
	// in its queue until Serve takes it.
	a.linkPort.Store(int32(a.port))
	closers = append(closers, func() {
		a.linkPort.Store(0)
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
		problems = append(problems, fmt.Sprintf("couldn't read config.toml, so every setting has its default and notifications are off, and pairtime keeps no new prompt text: %v", err))
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

// markDrained records that the inbox's first drain has handed its calls to
// the adapter host, or that there is no drain, and has the host look. An
// agent built without the drained channel, as some tests build one, has
// none to close.
func (a *agent) markDrained() {
	a.drainOnce.Do(func() {
		if a.drained != nil {
			close(a.drained)
		}
	})
	a.adaptersHost().poke()
}
