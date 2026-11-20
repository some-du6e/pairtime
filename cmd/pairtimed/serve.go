// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/some-du6e/pairtime/internal/server/config"
	"github.com/some-du6e/pairtime/internal/server/migrations"
	"github.com/some-du6e/pairtime/internal/server/ops"
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

func runServe(ctx context.Context, e *env, args []string) int {
	fs := newFlagSet("serve", serveSummary, e)
	load := configFlags(fs, e)
	if code, done := parse(fs, args); done {
		return code
	}
	logger := slog.New(slog.NewTextHandler(e.stderr, nil))
	cfg, err := load()
	if err != nil {
		logger.Error("pairtimed can't start", "err", err)
		return 1
	}
	if err := serve(ctx, cfg, logger, e); err != nil {
		logger.Error("pairtimed stopped", "err", err)
		return 1
	}
	return 0
}

// serve runs the server until ctx ends or a listener fails. The order
// matters: the SQLite version check comes before anything touches the
// database file, and migrations finish before the first request.
func serve(ctx context.Context, cfg *config.Config, logger *slog.Logger, e *env) error {
	logger.Info("starting", "version", version.Get().String(), "config", cfg.Path, "engine", cfg.Database.Engine)
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
	series := ops.NewSeries(reg, func() int { return w.QueueDepth() })
	w = writer.Start(db.Write, writer.Options{OnCommit: func(c writer.Commit) {
		series.ObserveCommit(c.Duration, c.Rows, c.Failed, c.Err)
		if c.Err != nil {
			logger.Error("writer transaction failed", "err", c.Err, "ops", c.Ops, "rows", c.Rows)
		}
	}})
	// Deferred after db.Close, so it runs first: queued writes commit
	// before the database closes.
	defer w.Close()

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

	signIn, err := newSignIn(cfg, db, w, logger)
	if err != nil {
		return err
	}

	public := http.NewServeMux()
	public.HandleFunc("GET /healthz", ops.Healthz)
	signIn.Register(public)
	maintainCtx, stopMaintain := context.WithCancel(ctx)
	defer stopMaintain()
	go signIn.Maintain(maintainCtx)

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
		newHTTPServer(public, errLog),
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
