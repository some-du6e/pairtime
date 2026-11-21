// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/some-du6e/pairtime/internal/server/auth"
	"github.com/some-du6e/pairtime/internal/server/backup"
	"github.com/some-du6e/pairtime/internal/server/config"
	"github.com/some-du6e/pairtime/internal/server/dashboard"
	"github.com/some-du6e/pairtime/internal/server/device"
	"github.com/some-du6e/pairtime/internal/server/migrations"
	"github.com/some-du6e/pairtime/internal/server/ops"
	"github.com/some-du6e/pairtime/internal/server/pages"
	"github.com/some-du6e/pairtime/internal/server/retention"
	"github.com/some-du6e/pairtime/internal/server/rollup"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/writer"
	"github.com/some-du6e/pairtime/internal/version"
)

const serveSummary = "Run the server: check SQLite, apply migrations, then serve until SIGINT or SIGTERM"

func init() {
	register(&command{name: "serve", summary: serveSummary, run: runServe})
}

// shutdownTimeout bounds how long serve waits for requests in flight once
// it is told to stop.
const shutdownTimeout = 20 * time.Second

// retentionEvery is how often the retention jobs sweep. Periods are whole
// days, so data outlives its period by at most this much (DESIGN 4.4).
const retentionEvery = time.Hour

func runServe(ctx context.Context, e *env, args []string) int {
	fs := newFlagSet("serve", serveSummary, e)
	load := configFlags(fs, e)
	replica := fs.String("litestream-replica", "", "the path in the main bucket that Litestream replicates the database to, as litestream.yml's replica path; /readyz then checks the replica's lag (DESIGN 10.4)")
	if code, done := parse(fs, args); done {
		return code
	}
	logger := slog.New(slog.NewTextHandler(e.stderr, nil))
	cfg, err := load()
	if err != nil {
		logger.Error("pairtimed can't start", "err", err)
		return 1
	}
	for _, w := range cfg.Warnings {
		logger.Warn(w)
	}
	if err := serve(ctx, cfg, logger, e, serveOptions{replica: *replica}); err != nil {
		logger.Error("pairtimed stopped", "err", err)
		return 1
	}
	return 0
}

// serveOptions are serve's choices besides the configuration.
type serveOptions struct {
	// replica is the replica's path in the main bucket when Litestream
	// runs serve, or "".
	replica string
}

// serve runs the server until ctx ends or a listener fails. The order
// matters: the SQLite version check comes before anything touches the
// database file, and migrations finish before the first request.
func serve(ctx context.Context, cfg *config.Config, logger *slog.Logger, e *env, so serveOptions) error {
	logger.Info("starting", "version", version.Get().String(), "config", cfg.Path, "engine", cfg.Database.Engine)

	// The pages render before the database opens, so a bad template or
	// controller contact stops the server at once.
	policy, err := retention.Default()
	if err != nil {
		return err
	}
	site, err := pages.New(pages.Options{
		BaseURL: cfg.BaseURL,
		Controller: pages.Controller{
			Name:    cfg.Controller.Name,
			Contact: cfg.Controller.Contact,
			DPO:     cfg.Controller.DPO,
		},
		ProfileDefault: cfg.ProfileDefault,
		PrivacyHub:     cfg.PrivacyHub.Aud != "",
		SignupsOpen:    cfg.SignupsOpen,
		Operators:      cfg.Operators,
		ScoringVersion: cfg.ScoringVersion,
		Version:        version.Get().Version,
		Commit:         sourceCommit(version.Get()),
		Retention:      policy,
	})
	if err != nil {
		return err
	}
	if !cfg.SignupsOpen {
		logger.Info("sign-ups are closed; only operators can create accounts", "operators", len(cfg.Operators))
	}

	db, err := store.Open(ctx, storeOptions(cfg, e))
	if err != nil {
		return err
	}
	defer db.Close()
	logger.Info("database open", "engine", db.Engine, "engine_version", db.Version)

	ms, err := migrations.Embedded()
	if err != nil {
		return err
	}
	res, err := migrations.Apply(ctx, db, ms, time.Now)
	if err != nil {
		return err
	}
	logger.Info("migrations up to date", "applied_now", res.Applied, "in_release", len(ms))
	if len(res.Unknown) > 0 {
		// A rollback to the previous image (DESIGN 10.5 step 6). v1
		// migrations only add, so this release runs on.
		logger.Warn("the database has migrations a newer release applied; running on, since they only add", "unknown", res.Unknown)
	}

	reg := ops.NewRegistry()
	var w *writer.Writer
	// The replica's lag, under Litestream, hears of every commit (set
	// below, once the bucket is open).
	var repl atomic.Pointer[backup.Replication]
	series := ops.NewSeries(reg, func() int { return w.QueueDepth() })
	w = writer.Start(db.Write, writer.Options{OnCommit: func(c writer.Commit) {
		series.ObserveCommit(c.Duration, c.Rows, c.Failed, c.Err)
		if c.Err != nil {
			logger.Error("writer transaction failed", "err", c.Err, "ops", c.Ops, "rows", c.Rows)
		} else if r := repl.Load(); r != nil && c.Rows > c.Failed {
			r.Wrote(c.Start.Add(c.Duration))
		}
	}})
	// Deferred after db.Close, so it runs first: queued writes commit
	// before the database closes.
	defer w.Close()

	// The retention jobs whose tables exist sweep through the writer, so
	// their deletes queue with every other write (DESIGN 4.4).
	jobs, off := policy.Jobs()
	if len(off) > 0 {
		logger.Info("retention jobs that wait for their tables", "off", off)
	}
	report := retentionReport(logger,
		reg.Counter("pairtime_retention_deleted_rows_total", "Rows the retention jobs deleted (DESIGN 4.4)."),
		reg.Counter("pairtime_retention_errors_total", "Retention job runs that failed."))
	rctx, stopRetention := context.WithCancel(ctx)
	retentionDone := make(chan struct{})
	go func() {
		defer close(retentionDone)
		retention.Run(rctx, w, jobs, retentionEvery, time.Now, func(r retention.Result) {
			report(r, rctx.Err() != nil)
		})
	}()
	// Deferred after w.Close, so it runs first: the sweep stops before the
	// writer does.
	defer func() {
		stopRetention()
		<-retentionDone
	}()

	// The settle job runs beside the sweep, through the writer: a minute
	// its device left pending counts as final by the server's clock once
	// PENDING_WINDOW_S has passed since it ended (D-server-settles-pending).
	// It too stops before the writer does.
	settled := reg.Counter("pairtime_rollup_settled_minutes_total", "Minutes their devices left pending that the server settled as final (D-server-settles-pending).")
	settleCtx, stopSettle := context.WithCancel(ctx)
	settleDone := make(chan struct{})
	go func() {
		defer close(settleDone)
		rollup.RunSettle(settleCtx, w, db.Read, rollup.SettleEvery, time.Now, func(n int, err error) {
			settled.Add(uint64(n))
			if err != nil && settleCtx.Err() == nil {
				logger.Error("settling pending minutes failed", "settled", n, "err", err)
			}
		})
	}()
	defer func() {
		stopSettle()
		<-settleDone
	}()

	checks := []ops.Check{{
		Name: "database",
		Run: func(ctx context.Context) error {
			return w.Do(ctx, 1, func(ctx context.Context, tx writer.Tx) error {
				return store.WriteProbe(ctx, tx, time.Now().UnixMilli())
			})
		},
	}}
	objs, err := openBuckets(cfg)
	if err != nil {
		return err
	}
	if objs == nil {
		logger.Warn("object storage is off: no evidence store and no snapshots; a deployment sets [object_storage] in pairtime.toml (DESIGN 10.5)")
	} else {
		checks = append(checks, objs.checks()...)
		logger.Info("object storage", "endpoint", cfg.ObjectStorage.Endpoint, "bucket", objs.main.Name(), "snapshot_bucket", objs.snapshots.Name())
	}
	switch {
	case so.replica != "" && objs == nil:
		return fmt.Errorf("-litestream-replica says Litestream replicates to the main bucket, but [object_storage] in %s is off", cfg.Path)
	case so.replica != "" && cfg.Database.Engine != config.EngineSQLite:
		return fmt.Errorf("-litestream-replica is for SQLite, and database.engine is %q", cfg.Database.Engine)
	case so.replica != "":
		r := backup.NewReplication(cfg.Database.Path, objs.main, so.replica)
		repl.Store(r)
		checks = append(checks, ops.Check{Name: "replication", Run: r.Check})
		reg.GaugeFunc("pairtime_replication_lag_seconds", "How long the oldest change Litestream's replica lacks has waited, -1 before the first measure (DESIGN 10.4).", r.LastLag)
		logger.Info("under Litestream: /readyz checks the replica's lag", "replica", objs.main.Name()+"/"+so.replica, "max_lag", backup.MaxLag.String())
	case objs != nil:
		logger.Warn("serve runs without -litestream-replica, so /readyz doesn't check replication; litestream.yml's exec passes it (deploy/litestream.yml)")
	}

	// The jobs below stop before the database closes: each is deferred
	// after db.Close and w.Close, so it runs first.
	bctx, stopBackground := context.WithCancel(ctx)
	var background sync.WaitGroup
	defer func() {
		stopBackground()
		background.Wait()
	}()
	if objs != nil && cfg.Database.Engine == config.EngineSQLite {
		if err := backup.RemoveStaleCopies(backup.SnapshotDir(cfg.DataDir)); err != nil {
			logger.Warn("couldn't remove the copies an unfinished snapshot left", "err", err)
		}
		job, err := newSnapshotJob(cfg, objs)
		if err != nil {
			logger.Warn("no nightly snapshots", "reason", err.Error())
		} else {
			last := reg.Gauge("pairtime_snapshot_last_success_timestamp_seconds", "When the last nightly snapshot was stored, in Unix seconds (DESIGN 10.3).")
			failed := reg.Counter("pairtime_snapshot_failures_total", "Nightly snapshots that failed.")
			background.Go(func() { runSnapshots(bctx, job, logger, last, failed) })
		}
	}
	if err := distinctPushURLs(cfg.Secrets.Get); err != nil {
		return err
	}
	if raw := cfg.Secrets.Get(envHeartbeatURL); raw != "" {
		if err := checkPushURL(envHeartbeatURL, raw); err != nil {
			return err
		}
		last := reg.Gauge("pairtime_heartbeat_last_ping_timestamp_seconds", "When pairtimed last pinged its push monitor, in Unix seconds (DESIGN 10.4).")
		failed := reg.Counter("pairtime_heartbeat_failures_total", "Push-monitor pings that failed.")
		background.Go(func() { runHeartbeat(bctx, raw, checks, logger, last, failed) })
		logger.Info("the heartbeat pings its push monitor every 5 minutes while /readyz passes", "every", heartbeatEvery.String())
	} else if objs != nil {
		logger.Warn(envHeartbeatURL + " isn't set, so no push monitor hears from pairtimed (DESIGN 10.4)")
	}

	// The device grant, /device, /account/devices and the device API
	// (WP-SV3). With sign-in off, the grant refuses new codes and the
	// pages say linking isn't open yet. Sign-in removes the devices of an
	// account Hack Club Auth turns ineligible, in the transaction that
	// revokes its sessions (D-device-account-binding).
	devices, err := device.New(device.Options{DB: db, Writer: w})
	if err != nil {
		return err
	}
	signIn, err := newSignIn(cfg, db, w, logger, site.Signups.MayCreate, devices.RemoveAccount)
	if err != nil {
		return err
	}
	linking, err := device.NewService(device.Config{
		Store:       devices,
		BaseURL:     cfg.BaseURL,
		SignIn:      signIn,
		Site:        site,
		Handle:      handle,
		SignupsOpen: site.Signups.Open,
		Logger:      logger,
	})
	if err != nil {
		return err
	}

	public := http.NewServeMux()
	public.HandleFunc("GET /healthz", ops.Healthz)
	site.Register(public)
	signIn.Register(public)
	linking.Register(public)
	newIngest(cfg, db, w, devices, series, logger).Register(public)
	visit := pages.SignIn{
		Enabled:    signIn.Enabled(),
		LoginPath:  auth.LoginPath,
		LogoutPath: auth.LogoutPath,
		Visitor:    visitor(signIn, logger),
	}
	// The home page at /, where a sign-in without return_to lands.
	site.RegisterHome(public, visit)
	// /demo, the dashboard page drawn from a fixed, labelled sample.
	site.RegisterDemo(public, visit, time.Now)
	// The signed-in owner's dashboard, as a page and as JSON, from the
	// rollups (WP-SV7). Ingest's bucket revisions reach them through
	// rollup.ApplyEvent, whose comment has the wiring for ingest's
	// RollupFunc.
	rollups := dashboard.Rollups{DB: db.Read}
	owner := accountOf(signIn)
	site.RegisterDashboard(public, visit, pages.Dashboard{
		Source:  rollups,
		Account: owner,
		SetZone: func(ctx context.Context, account, zone string) error {
			return w.Do(ctx, 1, func(ctx context.Context, tx writer.Tx) error {
				return rollup.SetZone(ctx, tx, account, zone)
			})
		},
		SameOrigin: signIn.SameOrigin,
		Now:        time.Now,
		Logger:     logger,
	})
	public.HandleFunc("GET "+dashboard.APIPath, dashboard.API(rollups, owner, time.Now, logger))
	maintainCtx, stopMaintain := context.WithCancel(ctx)
	defer stopMaintain()
	go signIn.Maintain(maintainCtx)
	go linking.Maintain(maintainCtx)

	pubLn, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	opsLn, err := net.Listen("tcp", cfg.OpsListen)
	if err != nil {
		pubLn.Close()
		return fmt.Errorf("ops_listen: %w", err)
	}
	errLog := slog.NewLogLogger(logger.Handler(), slog.LevelWarn)
	servers := []*http.Server{
		newHTTPServer(withHSTS(cfg.BaseURL, public), errLog),
		newHTTPServer(ops.Handler(reg, checks, ops.DefaultCheckTimeout), errLog),
	}
	listeners := []net.Listener{pubLn, opsLn}
	errc := make(chan error, len(servers))
	for i, s := range servers {
		go func() { errc <- s.Serve(listeners[i]) }()
	}
	logger.Info("serving", "listen", pubLn.Addr().String(), "ops_listen", opsLn.Addr().String(), "base_url", cfg.BaseURL)
	if e.listening != nil {
		e.listening(pubLn.Addr().String(), opsLn.Addr().String())
	}

	var serveErr error
	select {
	case <-ctx.Done():
		logger.Info("stopping")
	case err := <-errc:
		serveErr = fmt.Errorf("http: %w", err)
	}
	sctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	for _, s := range servers {
		if err := s.Shutdown(sctx); err != nil {
			logger.Warn("shutdown", "err", err)
		}
	}
	return serveErr
}

// sourceCommit is the commit the pages link the code and docs/ files at:
// the one this build came from, unless the build had local changes, which
// no link could show.
func sourceCommit(v version.Info) string {
	if v.Modified {
		return ""
	}
	return v.Commit
}

// retentionReport returns what serve does with each retention job's
// result. It counts and logs the rows the job deleted, and counts and logs
// a failure, unless the sweep failed only because it is being stopped. A
// job that fails partway has still deleted the rows of every batch that
// committed before the failure, so they count and appear in the log either
// way.
func retentionReport(logger *slog.Logger, deleted, failed *ops.Counter) func(r retention.Result, stopping bool) {
	return func(r retention.Result, stopping bool) {
		if r.Deleted > 0 {
			deleted.Add(uint64(r.Deleted))
		}
		switch {
		case r.Err != nil && !stopping:
			failed.Inc()
			logger.Error("retention job failed", "job", r.ID, "rows", r.Deleted, "err", r.Err)
		case r.Deleted > 0:
			logger.Info("retention job deleted rows", "job", r.ID, "rows", r.Deleted)
		}
	}
}

// hstsValue is the Strict-Transport-Security header the public listener
// sends: a browser that reads it over https uses only https for this host
// for the next year (RFC 6797).
const hstsValue = "max-age=31536000"

// withHSTS sets Strict-Transport-Security on every response h writes, when
// base_url is https. Over plain http the header must not be sent (RFC 6797
// 7.2), and config allows http only on a loopback host, for local runs and
// tests. config has already put base_url in canonical form, so the scheme
// is lowercase.
func withHSTS(baseURL string, h http.Handler) http.Handler {
	if !strings.HasPrefix(baseURL, "https://") {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", hstsValue)
		h.ServeHTTP(w, r)
	})
}

// newHTTPServer returns a server with timeouts for slow clients. There is
// no write timeout, because live-dashboard streams stay open (WP-SV8).
func newHTTPServer(h http.Handler, errLog *log.Logger) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          errLog,
	}
}
