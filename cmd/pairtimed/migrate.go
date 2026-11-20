// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/some-du6e/pairtime/internal/server/migrations"
	"github.com/some-du6e/pairtime/internal/server/store"
)

const migrateSummary = "Apply the migrations the database lacks; serve does this at start too"

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
	db, err := store.Open(ctx, storeOptions(cfg, e))
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
