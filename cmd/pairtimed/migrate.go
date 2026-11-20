// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/some-du6e/pairtime/internal/server/migrations"
	"github.com/some-du6e/pairtime/internal/server/store"
)

const migrateSummary = "Apply the migrations an existing database lacks; serve does this at start too"

func init() {
	register(&command{name: "migrate", summary: migrateSummary, run: runMigrate})
}

func runMigrate(ctx context.Context, e *env, args []string) int {
	fs := newFlagSet("migrate", migrateSummary, e)
	load := configFlags(fs, e)
	status := fs.Bool("status", false, "list every migration and whether it is applied, and apply none")
	if code, done := parse(fs, args); done {
		return code
	}
	fail := func(err error) int {
		fmt.Fprintf(e.stderr, "pairtimed migrate: %v\n", err)
		return 1
	}
	cfg, err := load()
	if err != nil {
		return fail(err)
	}
	// Neither form creates a database. Litestream restores the database
	// from the replica only when the file is missing, and then starts
	// serve, which creates a new one where there is no replica (DESIGN
	// 10.1). A file migrate made on a new host would stop that restore.
	o := storeOptions(cfg, e)
	o.NoCreate = true
	db, err := store.Open(ctx, o)
	if errors.Is(err, store.ErrNoDatabase) {
		return fail(fmt.Errorf("%w. migrate opens only a database that exists, so that Litestream can still restore one here; pairtimed serve creates it", err))
	}
	if err != nil {
		return fail(err)
	}
	defer db.Close()
	ms, err := migrations.Embedded()
	if err != nil {
		return fail(err)
	}

	if *status {
		states, err := migrations.Status(ctx, db, ms)
		if err != nil {
			return fail(err)
		}
		for _, s := range states {
			switch {
			case !s.Known:
				fmt.Fprintf(e.stdout, "%s  applied %s, by a newer release\n", s.ID, s.AppliedAt.Format(time.RFC3339))
			case s.AppliedAt.IsZero():
				fmt.Fprintf(e.stdout, "%s  pending\n", s.ID)
			default:
				fmt.Fprintf(e.stdout, "%s  applied %s\n", s.ID, s.AppliedAt.Format(time.RFC3339))
			}
		}
		return 0
	}

	res, err := migrations.Apply(ctx, db, ms, time.Now)
	if err != nil {
		return fail(err)
	}
	for _, id := range res.Applied {
		fmt.Fprintf(e.stdout, "applied %s\n", id)
	}
	if len(res.Applied) == 0 {
		fmt.Fprintln(e.stdout, "up to date")
	}
	for _, id := range res.Unknown {
		fmt.Fprintf(e.stdout, "left %s alone: a newer release applied it\n", id)
	}
	return 0
}
