// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/server/pages"
	"github.com/some-du6e/pairtime/internal/server/shadow"
	"github.com/some-du6e/pairtime/internal/server/store"
)

const calibratingSummary = "Turn a calibration volunteer's flag on or off, or show it: while it is on, the server hides the account's credited time from it, and its dashboard shows only the agent's time (D-server-shadow)"

// builtinScoring is the scoring version this build runs, which ends the
// flag's hiding at 1.0.0 (shadow.Hides). Tests replace it to see both
// sides of the freeze.
var builtinScoring = pages.BuiltinScoringVersion

func init() {
	register(&command{name: "calibrating", summary: calibratingSummary, run: runCalibrating})
}

func runCalibrating(ctx context.Context, e *env, args []string) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fmt.Fprint(e.stderr, `Usage:
  pairtimed calibrating on   -slack ID | -hca SUBJECT
  pairtimed calibrating off  -slack ID | -hca SUBJECT
  pairtimed calibrating show -slack ID | -hca SUBJECT

on turns the calibration flag on for the account of a volunteer of the
scoring's shadow week. While it is on, and until the server scores at
1.0.0, every reply that shows the owner their own credited time (the
dashboard, its API, the build-days API and the live stream) hides what
shadow mode hides on the volunteer's own computer (D-server-shadow): an
open dashboard switches at its next update, a page drawn after it at
once. off turns the flag off, and show says whether it is on, since when,
and whether it still hides anything. From 1.0.0 on, on refuses, since a
flag would hide nothing, and pairtimed serve clears every flag as it
starts. Name the account by its Slack ID or by its Hack Club Auth
subject.
`)
		if len(args) > 0 && (args[0] == "-h" || args[0] == "-help" || args[0] == "--help") {
			return 0
		}
		return 2
	}
	switch args[0] {
	case "on", "off", "show":
		return runCalibratingAs(ctx, e, args[0], args[1:])
	default:
		fmt.Fprintf(e.stderr, "pairtimed calibrating: unknown subcommand %q; use on, off or show\n", args[0])
		return 2
	}
}

// runCalibratingAs runs pairtimed calibrating's action, on, off or show.
// It prints and logs only the name it was given for the account, never
// the account's id or its other names.
func runCalibratingAs(ctx context.Context, e *env, action string, args []string) int {
	name := "calibrating " + action
	fs := newFlagSet(name, calibratingSummary, e)
	load := configFlags(fs, e)
	slack := fs.String("slack", "", "the account's Slack ID")
	hca := fs.String("hca", "", "the account's Hack Club Auth subject")
	if code, done := parse(fs, args); done {
		return code
	}
	if (*slack == "") == (*hca == "") {
		fmt.Fprintf(e.stderr, "pairtimed %s: name the account with one of -slack and -hca\n", name)
		return 2
	}
	// From the freeze on a flag hides nothing (shadow.Hides), and serve
	// clears every one as it starts, so none is set.
	if action == "on" && shadow.Frozen(builtinScoring) {
		fmt.Fprintf(e.stderr, "pairtimed %s: calibration ended at 1.0.0, and this server scores at %s, so a flag would hide nothing; nothing changed\n", name, builtinScoring)
		return 1
	}
	// The column is one of two fixed names, never the operator's input.
	column, value, said := "slack_id", *slack, "Slack ID "+*slack
	if *hca != "" {
		column, value, said = "hca_sub", *hca, "Hack Club Auth subject "+*hca
	}
	fail := func(err error) int {
		fmt.Fprintf(e.stderr, "pairtimed %s: %v\n", name, err)
		return 1
	}
	cfg, err := load()
	if err != nil {
		return fail(err)
	}
	o := storeOptions(cfg, e)
	o.NoCreate = true
	db, err := store.Open(ctx, o)
	if err != nil {
		return fail(err)
	}
	defer db.Close()

	now := time.Now()
	since, changed, err := setCalibration(ctx, db.Write, action, column, value, now)
	if errors.Is(err, errNoAccount) {
		return fail(fmt.Errorf("no account has %s", said))
	}
	if err != nil {
		return fail(err)
	}
	if changed {
		logger := slog.New(slog.NewTextHandler(e.stderr, nil))
		logger.Info("pairtimed calibrating", "action", action, "by", column, "account", value)
	}
	when := func(ms int64) string { return time.UnixMilli(ms).UTC().Format(time.RFC3339) }
	// A flag left on past the freeze hides nothing (shadow.Hides), and
	// serve clears it as it next starts (shadow.Clear).
	frozen := shadow.Frozen(builtinScoring)
	ended := fmt.Sprintf("It hides nothing, though: this server scores at %s, and calibration ended at 1.0.0. pairtimed serve clears it as it next starts.", builtinScoring)
	const next = "An open dashboard switches at its next update."
	switch {
	case action == "on" && changed:
		fmt.Fprintf(e.stdout, "Calibrating is now on for %s, since %s.\n", said, when(since.Int64))
		fmt.Fprintln(e.stdout, next)
	case action == "on":
		fmt.Fprintf(e.stdout, "Calibrating was already on for %s, since %s. Nothing changed.\n", said, when(since.Int64))
	case action == "off" && changed:
		fmt.Fprintf(e.stdout, "Calibrating is now off for %s.\n", said)
		if !frozen {
			fmt.Fprintln(e.stdout, next)
		}
	case action == "off":
		fmt.Fprintf(e.stdout, "Calibrating was already off for %s. Nothing changed.\n", said)
	case since.Valid:
		fmt.Fprintf(e.stdout, "Calibrating is on for %s, since %s.\n", said, when(since.Int64))
		if frozen {
			fmt.Fprintln(e.stdout, ended)
		}
	default:
		fmt.Fprintf(e.stdout, "Calibrating is off for %s.\n", said)
	}
	return 0
}

// errNoAccount says no account has the name an operator gave.
var errNoAccount = errors.New("no account")

// calibrationRace, when not nil, runs in setCalibration's transaction
// between finding the account and changing its flag. Tests change the
// flag there, as another run's change committed in between would be seen.
var calibrationRace func(ctx context.Context, tx *sql.Tx, id string) error

// setCalibration finds the one account whose column is value and, for
// on and off, turns its calibration flag on at now or off, in one
// transaction, unless it already is. It returns the flag as it is
// afterwards, the time it went on or NULL, and whether it changed it.
func setCalibration(ctx context.Context, db *sql.DB, action, column, value string, now time.Time) (sql.NullInt64, bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return sql.NullInt64{}, false, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id, calibrating_since_ms FROM users WHERE `+column+` = $1`, value)
	if err != nil {
		return sql.NullInt64{}, false, fmt.Errorf("finding the account: %w", err)
	}
	var id string
	var since sql.NullInt64
	n := 0
	for rows.Next() {
		n++
		if err := rows.Scan(&id, &since); err != nil {
			rows.Close()
			return sql.NullInt64{}, false, fmt.Errorf("finding the account: %w", err)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return sql.NullInt64{}, false, fmt.Errorf("finding the account: %w", err)
	}
	switch {
	case n == 0:
		return sql.NullInt64{}, false, errNoAccount
	case n > 1:
		return sql.NullInt64{}, false, fmt.Errorf("%d accounts have that name; name the account by its Hack Club Auth subject", n)
	}
	if calibrationRace != nil {
		if err := calibrationRace(ctx, tx, id); err != nil {
			return sql.NullInt64{}, false, err
		}
	}
	// Each UPDATE changes the flag only while it is as the SELECT found
	// it. On Postgres two runs can both find it off, and the second one's
	// UPDATE then changes no row, so the time the first set stays. SQLite's
	// write pool begins IMMEDIATE, so its runs take turns.
	var res sql.Result
	switch {
	case action == "on" && !since.Valid:
		since = sql.NullInt64{Int64: now.UnixMilli(), Valid: true}
		res, err = tx.ExecContext(ctx, `UPDATE users SET calibrating_since_ms = $1 WHERE id = $2 AND calibrating_since_ms IS NULL`, since.Int64, id)
	case action == "off" && since.Valid:
		since = sql.NullInt64{}
		res, err = tx.ExecContext(ctx, `UPDATE users SET calibrating_since_ms = NULL WHERE id = $1 AND calibrating_since_ms IS NOT NULL`, id)
	default:
		return since, false, nil
	}
	if err != nil {
		return sql.NullInt64{}, false, fmt.Errorf("turning the flag %s: %w", action, err)
	}
	updated, err := res.RowsAffected()
	if err != nil {
		return sql.NullInt64{}, false, fmt.Errorf("turning the flag %s: %w", action, err)
	}
	if updated == 0 {
		// Another run changed the flag first: say what it left, and
		// change nothing.
		err := tx.QueryRowContext(ctx, `SELECT calibrating_since_ms FROM users WHERE id = $1`, id).Scan(&since)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return sql.NullInt64{}, false, errNoAccount
		case err != nil:
			return sql.NullInt64{}, false, fmt.Errorf("reading the flag again: %w", err)
		}
		return since, false, nil
	}
	if err := tx.Commit(); err != nil {
		return sql.NullInt64{}, false, err
	}
	return since, true, nil
}
