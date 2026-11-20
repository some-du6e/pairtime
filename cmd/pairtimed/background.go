// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/server/objstore"
	"github.com/some-du6e/pairtime/internal/server/ops"
	"github.com/some-du6e/pairtime/internal/version"
)

// userAgent is what pairtimed's own HTTP requests to other services send.
// A descriptive one gets past Cloudflare's Bot Fight Mode, which answers
// the default Go and curl agents from data-center addresses with a 403
// challenge (deploy/runbook.md, User-Agent).
func userAgent(product string) string {
	return product + "/" + version.Get().Version + " (+https://github.com/some-du6e/pairtime)"
}

// The three push monitors, the dead man's switches of DESIGN 10.4
// (deploy/secrets.env.example). Each is a URL that a GET marks up.
const (
	// envHeartbeatURL is pinged every heartbeatEvery while /readyz passes.
	envHeartbeatURL = "PAIRTIME_HEARTBEAT_URL"
	// envSnapshotHeartbeatURL is pinged after each snapshot is stored, so
	// its monitor expects a ping a day, with 26 hours of grace.
	envSnapshotHeartbeatURL = "PAIRTIME_SNAPSHOT_HEARTBEAT_URL"
	// envLitestreamHeartbeatURL is Litestream's own, which litestream.yml
	// reads; pairtimed reads it only in doctor.
	envLitestreamHeartbeatURL = "PAIRTIME_LITESTREAM_HEARTBEAT_URL"
)

// pushMonitors are the three, for checks that they differ.
var pushMonitors = []string{envHeartbeatURL, envSnapshotHeartbeatURL, envLitestreamHeartbeatURL}

// heartbeatEvery is how often pairtimed pings its push monitor while
// /readyz passes (DESIGN 10.4).
const heartbeatEvery = 5 * time.Minute

// pushTimeout bounds one ping of a push monitor.
const pushTimeout = 10 * time.Second

// checkPushURL checks a push monitor's URL: any http or https URL that a
// GET marks up, such as a Healthchecks.io check URL or an Uptime Kuma push
// URL. Plain http is fine: the pilot's monitors are Uptime Kuma on the
// LAN, kept off Cloudflare so a Cloudflare outage can't raise a false
// alert, and a ping carries no data. Errors never quote the URL, whose
// path is the monitor's secret.
func checkPushURL(name, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("%s isn't a URL with a host, and no user or fragment", name)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("%s must be an http or https URL", name)
	}
	return nil
}

// distinctPushURLs fails when two of the push monitors that are set share
// a URL: each part needs a monitor of its own, or one could hide another's
// silence.
func distinctPushURLs(get func(string) string) error {
	seen := make(map[string]string)
	for _, name := range pushMonitors {
		raw := get(name)
		if raw == "" {
			continue
		}
		if other, dup := seen[raw]; dup {
			return fmt.Errorf("%s and %s are the same URL; each needs a push monitor of its own", other, name)
		}
		seen[raw] = name
	}
	return nil
}

// pushClient sends push-monitor pings. It follows no redirect: a monitor
// that moved is a configuration to fix, not a request to repeat elsewhere.
func pushClient() *http.Client {
	return &http.Client{
		Timeout:       pushTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// ping GETs a push monitor's URL, which marks it up, and fails unless the
// answer is 2xx. Errors name the monitor's host, never its URL.
func ping(ctx context.Context, client *http.Client, raw, agent string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("the push monitor's URL doesn't parse")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return fmt.Errorf("pinging %s: %v", u.Host, err)
	}
	req.Header.Set("User-Agent", agent)
	resp, err := client.Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return fmt.Errorf("pinging %s: %v", u.Host, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode/100 != 2 {
		hint := ""
		if resp.StatusCode == http.StatusForbidden && resp.Header.Get("Cf-Mitigated") == "challenge" {
			hint = "; Cloudflare challenged the request, so exempt the monitor's path from Bot Fight Mode"
		}
		return fmt.Errorf("%s answered the ping with %s%s", u.Host, resp.Status, hint)
	}
	return nil
}

// heartbeat runs the readiness checks and, when they all pass, pings the
// push monitor. When one fails it sends nothing, and the monitor alerts
// once the pings stop (DESIGN 10.4).
func heartbeat(ctx context.Context, client *http.Client, raw string, checks []ops.Check) (ops.Readiness, error) {
	res := ops.Run(ctx, checks, ops.DefaultCheckTimeout)
	if !res.Ready {
		return res, nil
	}
	return res, ping(ctx, client, raw, userAgent("pairtimed"))
}

// runHeartbeat calls heartbeat every heartbeatEvery until ctx ends.
func runHeartbeat(ctx context.Context, raw string, checks []ops.Check, logger *slog.Logger, last *ops.Gauge, failed *ops.Counter) {
	client := pushClient()
	t := time.NewTicker(heartbeatEvery)
	defer t.Stop()
	for {
		res, err := heartbeat(ctx, client, raw, checks)
		switch {
		case ctx.Err() != nil:
			return
		case !res.Ready:
			var names []string
			for _, c := range res.Checks {
				if !c.OK {
					names = append(names, c.Name)
				}
			}
			logger.Warn("not ready, so the heartbeat sent no ping", "failing", strings.Join(names, ","))
		case err != nil:
			failed.Inc()
			logger.Error("the heartbeat ping failed", "err", err)
		default:
			last.Set(time.Now().Unix())
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// snapshotAt is when the nightly snapshot runs: 03:10 UTC, after the
// publisher's 00:10 UTC day root and in Europe's and America's night.
const snapshotAt = 3*time.Hour + 10*time.Minute

// snapshotStale is how old the newest snapshot may be when serve starts
// before it takes one early, after snapshotStartDelay: a server that was
// down at 03:10 still gets its night's snapshot.
const snapshotStale = 25 * time.Hour

// snapshotStartDelay is the wait for that early snapshot, so a server
// that restarts over and over doesn't copy its database each time.
const snapshotStartDelay = 10 * time.Minute

// nextSnapshot is the first snapshotAt past a UTC midnight after now.
func nextSnapshot(now time.Time) time.Time {
	day := now.UTC().Truncate(24 * time.Hour)
	next := day.Add(snapshotAt)
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}

// firstSnapshotWait is how long serve waits before its first snapshot:
// snapshotStartDelay when the newest one is older than snapshotStale or
// there is none, and until the next snapshotAt otherwise.
func firstSnapshotWait(now, newest time.Time) time.Duration {
	if newest.IsZero() || now.Sub(newest) > snapshotStale {
		return snapshotStartDelay
	}
	return nextSnapshot(now).Sub(now)
}

// runSnapshots takes a snapshot every night until ctx ends.
func runSnapshots(ctx context.Context, job *snapshotJob, logger *slog.Logger, last *ops.Gauge, failed *ops.Counter) {
	var newest time.Time
	if info, err := job.opts.Snapshots.Latest(ctx); err == nil {
		newest = info.LastModified
		if newest.IsZero() {
			newest = time.Now()
		}
	} else if !errors.Is(err, objstore.ErrNotFound) {
		logger.Warn("couldn't find the newest snapshot, so one runs soon", "err", err)
	}
	wait := firstSnapshotWait(time.Now(), newest)
	logger.Info("nightly snapshots are on", "first_in", wait.Round(time.Second).String(), "bucket", job.opts.Snapshots.Bucket().Name())
	for {
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		start := time.Now()
		info, err := job.run(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			failed.Inc()
			logger.Error("the nightly snapshot failed", "err", err)
		default:
			last.Set(time.Now().Unix())
			logger.Info("took the nightly snapshot", "key", info.Key, "bytes", info.Size, "took", time.Since(start).Round(time.Second).String())
			if err := job.notify(ctx); err != nil {
				logger.Error("the snapshot's push monitor didn't take the ping", "err", err)
			}
		}
		wait = nextSnapshot(time.Now()).Sub(time.Now())
	}
}
