// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/migrations"
	"github.com/some-du6e/pairtime/internal/server/store"
)

// calibratingAccounts are the accounts TestCalibrating's database holds:
// id, Hack Club Auth subject and Slack ID. Two share a Slack ID, which
// nothing stops.
var calibratingAccounts = [][3]string{
	{"acct-one", "sub-one", "U0FAKE1"},
	{"acct-two", "sub-two", "U0FAKE2"},
	{"acct-three", "sub-three", "U0SHARED"},
	{"acct-four", "sub-four", "U0SHARED"},
}

// calibratingDB makes the database writeConfig's config names, migrated,
// with calibratingAccounts, and returns the config and a handle on it.
func calibratingDB(t *testing.T) (string, *store.DB) {
	t.Helper()
	ctx := context.Background()
	configPath, dbPath := writeConfig(t)
	db, err := store.Open(ctx, store.Options{Engine: store.SQLite, Path: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ms, err := migrations.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(ctx, db, ms, time.Now); err != nil {
		t.Fatal(err)
	}
	for _, a := range calibratingAccounts {
		if _, err := db.Write.ExecContext(ctx, `INSERT INTO users (id, hca_sub, slack_id, verification_status, created_at_ms, updated_at_ms, signed_in_at_ms)
VALUES ($1, $2, $3, 'verified', 0, 0, 0)`, a[0], a[1], a[2]); err != nil {
			t.Fatal(err)
		}
	}
	return configPath, db
}

// flags returns each account's calibrating_since_ms, -1 for NULL.
func flags(t *testing.T, db *store.DB) map[string]int64 {
	t.Helper()
	rows, err := db.Read.QueryContext(context.Background(), `SELECT id, calibrating_since_ms FROM users`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := make(map[string]int64)
	for rows.Next() {
		var id string
		var since sql.NullInt64
		if err := rows.Scan(&id, &since); err != nil {
			t.Fatal(err)
		}
		out[id] = -1
		if since.Valid {
			out[id] = since.Int64
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCalibrating(t *testing.T) {
	tests := []struct {
		name string
		// runs are pairtimed calibrating's arguments, each a run, after
		// which the last run's answer is checked.
		runs       [][]string
		wantCode   int
		wantStdout string // substring of the last run's
		wantStderr string // substring of the last run's
		// on lists the accounts whose flag is on afterwards.
		on []string
		// logged says the last run logs its change.
		logged bool
		// scoring is the scoring version the build runs, the one there
		// is when it's "", and notStdout what the last run mustn't say.
		scoring   string
		notStdout string
		// race, when not nil, runs in the last run between its finding
		// the account and its changing the flag (calibrationRace). Its
		// change is made in the run's own transaction, which the run rolls
		// back when it changes nothing, so on says the flag as it was
		// before the run.
		race func(ctx context.Context, tx *sql.Tx, id string) error
	}{
		{name: "on turns the flag on for the account a Slack ID names, and says since when",
			runs: [][]string{{"on", "-slack", "U0FAKE1"}}, wantStdout: "Calibrating is now on for Slack ID U0FAKE1, since 20", on: []string{"acct-one"}, logged: true},
		{name: "on names an account by its Hack Club Auth subject too",
			runs: [][]string{{"on", "-hca", "sub-two"}}, wantStdout: "Calibrating is now on for Hack Club Auth subject sub-two, since 20", on: []string{"acct-two"}, logged: true},
		{name: "a second on changes nothing and says since when the flag has been on",
			runs: [][]string{{"on", "-slack", "U0FAKE1"}, {"on", "-slack", "U0FAKE1"}}, wantStdout: "Calibrating was already on for Slack ID U0FAKE1, since 20", on: []string{"acct-one"}},
		{name: "off turns the flag off",
			runs: [][]string{{"on", "-slack", "U0FAKE1"}, {"off", "-slack", "U0FAKE1"}}, wantStdout: "Calibrating is now off for Slack ID U0FAKE1.", logged: true},
		{name: "off on an account without the flag changes nothing",
			runs: [][]string{{"off", "-hca", "sub-one"}}, wantStdout: "Calibrating was already off for Hack Club Auth subject sub-one. Nothing changed."},
		{name: "show says the flag is on, and since when",
			runs: [][]string{{"on", "-slack", "U0FAKE2"}, {"show", "-slack", "U0FAKE2"}}, wantStdout: "Calibrating is on for Slack ID U0FAKE2, since 20", on: []string{"acct-two"}},
		{name: "show says the flag is off",
			runs: [][]string{{"show", "-slack", "U0FAKE2"}}, wantStdout: "Calibrating is off for Slack ID U0FAKE2."},
		{name: "an on that changes the flag says an open dashboard switches at its next update",
			runs: [][]string{{"on", "-slack", "U0FAKE1"}}, wantStdout: "An open dashboard switches at its next update.", on: []string{"acct-one"}, logged: true, scoring: "0.6.0"},
		{name: "an off that changes the flag says so too",
			runs: [][]string{{"on", "-slack", "U0FAKE1"}, {"off", "-slack", "U0FAKE1"}}, wantStdout: "An open dashboard switches at its next update.", logged: true, scoring: "0.6.0"},
		{name: "while the server scores below 1.0.0, show says nothing of the freeze",
			runs: [][]string{{"on", "-slack", "U0FAKE2"}, {"show", "-slack", "U0FAKE2"}}, wantStdout: "Calibrating is on for Slack ID U0FAKE2, since 20", on: []string{"acct-two"}, scoring: "0.6.0", notStdout: "hides nothing"},
		{name: "once the server scores at 1.0.0, show says a flag left on hides nothing",
			runs: [][]string{{"on", "-slack", "U0FAKE2"}, {"show", "-slack", "U0FAKE2"}}, wantStdout: "It hides nothing, though: this server scores at 1.0.0, and calibration ended at 1.0.0.", on: []string{"acct-two"}, scoring: "1.0.0"},
		{name: "once the server scores at 1.0.0, on says so as it turns the flag on",
			runs: [][]string{{"on", "-slack", "U0FAKE2"}}, wantStdout: "It hides nothing, though: this server scores at 1.0.0", on: []string{"acct-two"}, logged: true, scoring: "1.0.0", notStdout: "switches at its next update"},
		{name: "an on that finds the flag off, and then finds another run turned it on first, keeps that run's time and changes nothing",
			runs: [][]string{{"on", "-slack", "U0FAKE1"}}, race: raceTo(raceSince), wantStdout: "Calibrating was already on for Slack ID U0FAKE1, since 2026-12-01T09:00:00Z. Nothing changed."},
		{name: "an off that finds the flag on, and then finds another run turned it off first, changes nothing",
			runs: [][]string{{"on", "-slack", "U0FAKE1"}, {"off", "-slack", "U0FAKE1"}}, race: raceTo(nil), wantStdout: "Calibrating was already off for Slack ID U0FAKE1. Nothing changed.", on: []string{"acct-one"}},
		{name: "an account erased between finding it and changing its flag is no account",
			runs: [][]string{{"on", "-slack", "U0FAKE1"}}, race: raceErase, wantCode: 1, wantStderr: "no account has Slack ID U0FAKE1"},
		{name: "a name no account has fails and changes nothing",
			runs: [][]string{{"on", "-slack", "U0NOBODY"}}, wantCode: 1, wantStderr: "no account has Slack ID U0NOBODY"},
		{name: "a Slack ID two accounts share is refused, and changes nothing",
			runs: [][]string{{"on", "-slack", "U0SHARED"}}, wantCode: 1, wantStderr: "2 accounts have that name"},
		{name: "a run that names no account is a usage error",
			runs: [][]string{{"on"}}, wantCode: 2, wantStderr: "name the account with one of -slack and -hca"},
		{name: "a run that names an account twice is a usage error",
			runs: [][]string{{"on", "-slack", "U0FAKE1", "-hca", "sub-one"}}, wantCode: 2, wantStderr: "name the account with one of -slack and -hca"},
		{name: "an action it doesn't know is a usage error",
			runs: [][]string{{"toggle", "-slack", "U0FAKE1"}}, wantCode: 2, wantStderr: `unknown subcommand "toggle"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.scoring != "" {
				was := builtinScoring
				builtinScoring = tt.scoring
				t.Cleanup(func() { builtinScoring = was })
			}
			configPath, db := calibratingDB(t)
			var code int
			var stdout, stderr string
			var first map[string]int64
			for i, args := range tt.runs {
				if tt.race != nil && i == len(tt.runs)-1 {
					calibrationRace = tt.race
					t.Cleanup(func() { calibrationRace = nil })
				}
				e, out, errs := testEnv()
				code = run(context.Background(), append([]string{"calibrating", args[0], "-config", configPath}, args[1:]...), e)
				stdout, stderr = out.String(), errs.String()
				if i == 0 {
					first = flags(t, db)
				}
			}
			if code != tt.wantCode {
				t.Fatalf("exit code %d, want %d; stdout:\n%s\nstderr:\n%s", code, tt.wantCode, stdout, stderr)
			}
			if !strings.Contains(stdout, tt.wantStdout) {
				t.Errorf("stdout is %q, want it to hold %q", stdout, tt.wantStdout)
			}
			if tt.notStdout != "" && strings.Contains(stdout, tt.notStdout) {
				t.Errorf("stdout is %q, which holds %q", stdout, tt.notStdout)
			}
			if !strings.Contains(stderr, tt.wantStderr) {
				t.Errorf("stderr is %q, want it to hold %q", stderr, tt.wantStderr)
			}
			got := flags(t, db)
			for _, a := range calibratingAccounts {
				if on := got[a[0]] >= 0; on != containsString(tt.on, a[0]) {
					t.Errorf("%s's flag is on: %v, want %v", a[0], on, !on)
				}
			}
			// A run that changes nothing keeps the time the flag went on.
			for id, since := range first {
				if got[id] >= 0 && since >= 0 && got[id] != since {
					t.Errorf("%s's flag went on at %d, and is now dated %d", id, since, got[id])
				}
			}
			// The output names the account only as the run did.
			for _, a := range calibratingAccounts {
				for _, other := range a {
					if !argsHold(tt.runs, other) && (strings.Contains(stdout, other) || strings.Contains(stderr, other)) {
						t.Errorf("the output holds %q, which the runs never gave:\nstdout:\n%s\nstderr:\n%s", other, stdout, stderr)
					}
				}
			}
			if logs := strings.Contains(stderr, `msg="pairtimed calibrating"`); logs != tt.logged {
				t.Errorf("the run logged its change: %v, want %v:\n%s", logs, tt.logged, stderr)
			} else if logs && !strings.Contains(stderr, "time=") {
				t.Errorf("the log line says no time:\n%s", stderr)
			}
		})
	}
}

// raceSince is when raceTo's other run turns a flag on in
// TestCalibrating.
var raceSince = time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC).UnixMilli()

// raceTo returns a calibrationRace that sets the flag to since, a time in
// Unix milliseconds or nil for off, as another run would.
func raceTo(since any) func(context.Context, *sql.Tx, string) error {
	return func(ctx context.Context, tx *sql.Tx, id string) error {
		_, err := tx.ExecContext(ctx, `UPDATE users SET calibrating_since_ms = $1 WHERE id = $2`, since, id)
		return err
	}
}

// raceErase is a calibrationRace that deletes the account, as the
// privacy hub's eraser would.
func raceErase(ctx context.Context, tx *sql.Tx, id string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, id)
	return err
}

// containsString reports whether list holds s.
func containsString(list []string, s string) bool {
	for _, l := range list {
		if l == s {
			return true
		}
	}
	return false
}

// argsHold reports whether any of runs' arguments is s.
func argsHold(runs [][]string, s string) bool {
	for _, args := range runs {
		if containsString(args, s) {
			return true
		}
	}
	return false
}
