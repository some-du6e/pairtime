// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/some-du6e/pairtime/internal/server/backup"
	"github.com/some-du6e/pairtime/internal/server/config"
	"github.com/some-du6e/pairtime/internal/server/objstore"
)

const snapshotSummary = "Take the nightly snapshot now (take), or download the newest one, still encrypted (fetch)"

func init() {
	register(&command{name: "snapshot", summary: snapshotSummary, run: runSnapshot})
}

func runSnapshot(ctx context.Context, e *env, args []string) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fmt.Fprint(e.stderr, `Usage:
  pairtimed snapshot take
  pairtimed snapshot fetch -o FILE [-key KEY]

take makes the snapshot serve makes every night (DESIGN 10.3): VACUUM INTO
a private copy of the database, encrypted with age to $PAIRTIME_AGE_RECIPIENT
as it streams to the snapshot bucket. fetch downloads the newest snapshot,
or KEY, as it is stored; Karim decrypts it offline:

  age -d -i <identity file> FILE > pairtime.db
`)
		if len(args) > 0 && (args[0] == "-h" || args[0] == "-help" || args[0] == "--help") {
			return 0
		}
		return 2
	}
	switch args[0] {
	case "take":
		return runSnapshotTake(ctx, e, args[1:])
	case "fetch":
		return runSnapshotFetch(ctx, e, args[1:])
	default:
		fmt.Fprintf(e.stderr, "pairtimed snapshot: unknown subcommand %q; use take or fetch\n", args[0])
		return 2
	}
}

// snapshotJob is what a snapshot needs, from the configuration.
type snapshotJob struct {
	opts backup.SnapshotOptions
	// heartbeat is the snapshot's push monitor, or "".
	heartbeat string
}

// newSnapshotJob checks that cfg can take snapshots and says how.
func newSnapshotJob(cfg *config.Config, objs *buckets) (*snapshotJob, error) {
	if cfg.Database.Engine != config.EngineSQLite {
		return nil, fmt.Errorf("snapshots are VACUUM INTO copies of SQLite, and database.engine is %q", cfg.Database.Engine)
	}
	if objs == nil {
		return nil, fmt.Errorf("snapshots go to the snapshot bucket, and [object_storage] in %s is off", cfg.Path)
	}
	raw := cfg.Secrets.Get(envAgeRecipient)
	if raw == "" {
		return nil, fmt.Errorf("%s isn't set, so there is no key to encrypt snapshots to (DESIGN 10.3)", envAgeRecipient)
	}
	r, err := backup.ParseRecipient(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", envAgeRecipient, err)
	}
	heartbeat := cfg.Secrets.Get(envSnapshotHeartbeatURL)
	if heartbeat != "" {
		if err := checkPushURL(envSnapshotHeartbeatURL, heartbeat); err != nil {
			return nil, err
		}
	}
	return &snapshotJob{opts: backup.SnapshotOptions{
		DBPath:    cfg.Database.Path,
		Dir:       backup.SnapshotDir(cfg.DataDir),
		Recipient: r,
		Snapshots: objstore.NewSnapshots(objs.snapshots),
	}, heartbeat: heartbeat}, nil
}

func (j *snapshotJob) run(ctx context.Context) (objstore.ObjectInfo, error) {
	return backup.Snapshot(ctx, j.opts)
}

// notify pings the snapshot's push monitor, after a snapshot was stored.
func (j *snapshotJob) notify(ctx context.Context) error {
	if j.heartbeat == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, pushTimeout)
	defer cancel()
	return ping(ctx, pushClient(), j.heartbeat, userAgent("pairtimed"))
}

func runSnapshotTake(ctx context.Context, e *env, args []string) int {
	fs := newFlagSet("snapshot take", "Take the nightly snapshot now", e)
	load := configFlags(fs, e)
	if code, done := parse(fs, args); done {
		return code
	}
	fail := func(err error) int {
		fmt.Fprintf(e.stderr, "pairtimed snapshot take: %v\n", err)
		return 1
	}
	cfg, err := load()
	if err != nil {
		return fail(err)
	}
	objs, err := openBuckets(cfg)
	if err != nil {
		return fail(err)
	}
	job, err := newSnapshotJob(cfg, objs)
	if err != nil {
		return fail(err)
	}
	info, err := job.run(ctx)
	if err != nil {
		return fail(err)
	}
	fmt.Fprintf(e.stdout, "stored %s in %s: %d bytes\n", info.Key, objs.snapshots.Name(), info.Size)
	if err := job.notify(ctx); err != nil {
		return fail(fmt.Errorf("the snapshot is stored, but its push monitor didn't take the ping: %w", err))
	}
	return 0
}

func runSnapshotFetch(ctx context.Context, e *env, args []string) int {
	fs := newFlagSet("snapshot fetch", "Download the newest snapshot, still encrypted", e)
	load := configFlags(fs, e)
	out := fs.String("o", "", "the file to write, never over an existing one (required)")
	key := fs.String("key", "", "the snapshot to download, instead of the newest")
	if code, done := parse(fs, args); done {
		return code
	}
	fail := func(err error) int {
		fmt.Fprintf(e.stderr, "pairtimed snapshot fetch: %v\n", err)
		return 1
	}
	if *out == "" {
		return fail(fmt.Errorf("-o is required"))
	}
	cfg, err := load()
	if err != nil {
		return fail(err)
	}
	objs, err := openBuckets(cfg)
	if err != nil {
		return fail(err)
	}
	if objs == nil {
		return fail(fmt.Errorf("[object_storage] in %s is off", cfg.Path))
	}
	snaps := objstore.NewSnapshots(objs.snapshots)
	k := *key
	if k == "" {
		latest, err := snaps.Latest(ctx)
		if err != nil {
			return fail(err)
		}
		k = latest.Key
	}
	body, info, err := snaps.Get(ctx, k)
	if err != nil {
		return fail(err)
	}
	defer body.Close()
	f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fail(err)
	}
	n, err := io.Copy(f, body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && info.Size >= 0 && n != info.Size {
		err = fmt.Errorf("got %d of %d bytes", n, info.Size)
	}
	if err != nil {
		os.Remove(*out)
		return fail(err)
	}
	fmt.Fprintf(e.stdout, "fetched %s, last modified %s: %d bytes\n", k, info.LastModified.UTC().Format("2006-01-02T15:04:05Z"), n)
	return 0
}
