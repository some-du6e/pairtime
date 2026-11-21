// SPDX-License-Identifier: AGPL-3.0-or-later

// Package retention holds pairtime's retention schedule and pairtimed's
// retention jobs (DESIGN 4.4; WP-SV14).
//
// The schedule is docs/retention.json, embedded by package docs, and Policy
// is that file loaded and checked. The privacy notice at /privacy renders
// its retention table from a Policy (package pages), and Sweep runs the
// jobs the same Policy describes, so the page and the jobs can't disagree.
//
// An entry whose data is in the server's database, a server entry or the
// evidence store's, whose index rows are there too (DESIGN 10.1), may bind
// jobs to tables. A job deletes its table's rows whose time column is at
// least the entry's days old, or, for a job by the window, whose time
// column is a decision whose spot-check window has closed, except rows a
// hold keeps. That one shape covers DESIGN 4.4's four server jobs, and
// the two that delete the holds:
//
//   - IP addresses and JA4 fingerprints, 30 days after they were seen.
//   - Per-minute buckets, 180 days after their minute, unless a
//     submission still inside its spot-check window uses them, since
//     CC6 and the bundle compare against the buckets received live
//     (DESIGN 7.3, 7.8). Ingest keeps the daily totals per project and
//     activity current in rollups_daily (WP-SV7), so deleting the minute
//     rows leaves exactly those totals.
//   - Witness rows, 180 days after they were received, unless a
//     submission still inside its spot-check window uses them.
//   - Device rows, 90 days after the device was removed, but only once
//     ingest holds nothing from the device: the job's kept_by tables
//     keep a removed device's row while any of their rows names it, so
//     its device id stays with its account (D-device-account-binding). A
//     device that hasn't been removed has no removal time, so its row
//     stays.
//   - The holds that say which minute buckets and witness rows a
//     submission uses, once its spot-check window has closed. Load
//     refuses a hold whose table no job of its entry deletes from this
//     way, so no hold outlives the window it stands for.
//
// The tables belong to the work packages that create them. Until a table
// exists, its job, hold or kept_by guard names that package in awaits and
// isn't used.
// TestTables fails when a table the migrations create has no entry, when a
// job's table lacks the columns its entry names, and when an awaited table
// appears, so the package that creates a table also turns its job on.
//
// An entry with a period, days or the spot-check window, can't simply list
// a table: each of its tables is a job's, so something deletes its rows,
// or is in its lifetime_tables, whose rows its kept says stay while the
// account exists. Only an entry without a period, such as the account,
// lists plain tables.
//
// The other entries are enforced elsewhere: on the device by the client's
// retention sweeps (WP-LG6), sign-in sessions by package accounts, whose
// TestSessionRetention checks its periods against the schedule's, on
// account deletion by the eraser (WP-SV13), and in the evidence store by
// the submission work packages.
package retention

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/docs"
)

// The places data is kept, the values of Entry.Where.
const (
	Device   = "device"
	Server   = "server"
	Evidence = "evidence"
	Public   = "public"
)

// Places lists the places in the order the privacy notice shows them.
var Places = []string{Device, Server, Evidence, Public}

// Policy is a loaded and checked retention schedule.
type Policy struct {
	// About says what the file is, for people reading it.
	About string `json:"about"`
	// Version is the file format's version.
	Version int `json:"version"`
	// Source is the design section the schedule follows.
	Source string `json:"source"`
	// SpotCheckWindow is how long a decided submission's evidence stays
	// reachable for spot checks.
	SpotCheckWindow Window `json:"spot_check_window"`
	// NoPersonalData lists server tables that hold nothing about a
	// person, such as schema_migrations, so no entry covers them.
	NoPersonalData []string `json:"no_personal_data"`
	// Entries are DESIGN 4.4's rows, in its order.
	Entries []Entry `json:"entries"`
}

// Window is the spot-check window.
type Window struct {
	// Months is how long the window stays open after the decision.
	Months int `json:"months"`
	// Provisional, when set, says why Months is a stand-in: it is the
	// design's default until Hack Club HQ sets the window (DESIGN 13.2).
	Provisional string `json:"provisional,omitempty"`
}

// Entry is one kind of data and how long it is kept.
type Entry struct {
	// ID is where.name, such as "server.devices".
	ID string `json:"id"`
	// Where is Device, Server, Evidence or Public.
	Where string `json:"where"`
	// Data says what the data is, in plain words.
	Data string `json:"data"`
	// Kept says how long the data is kept, in plain words. "{days}"
	// stands for Days and "{window}" for the spot-check window.
	Kept string `json:"kept"`
	// Days is the entry's period, when it has one.
	Days int `json:"days,omitempty"`
	// Tables lists server tables that hold this data and that no job
	// deletes from. Only an entry without a period has them: its data is
	// kept while the account exists, or deleted by other code, as its kept
	// says.
	Tables []string `json:"tables,omitempty"`
	// LifetimeTables lists tables of an entry with a period whose rows
	// stay while the account exists, as its kept says: the daily totals
	// that outlive the minute buckets.
	LifetimeTables []string `json:"lifetime_tables,omitempty"`
	// Jobs are the server jobs that enforce the entry's period, at most one
	// per table.
	Jobs []JobSpec `json:"jobs,omitempty"`
}

// HasPeriod reports whether the entry's data is kept for a period, days or
// the spot-check window, rather than while the account exists or for a
// time kept says in words.
func (e Entry) HasPeriod() bool {
	return e.Days > 0 || strings.Contains(e.Kept, "{window}")
}

// The values of JobSpec.Period.
const (
	// PeriodDays deletes a row once its time is the entry's days old.
	PeriodDays = "days"
	// PeriodWindow deletes a row once its time, a decision, is past the
	// spot-check window (Window.Closes).
	PeriodWindow = "window"
)

// JobSpec binds an entry to a table one of its jobs deletes from.
type JobSpec struct {
	// Table is the table the job deletes rows of.
	Table string `json:"table"`
	// Key is the table's primary key. The job deletes by it.
	Key []string `json:"key"`
	// TimeColumn holds when each row's period started, in epoch
	// milliseconds. A row whose TimeColumn is NULL never expires.
	TimeColumn string `json:"time_column"`
	// Period is PeriodDays, the default when empty, or PeriodWindow.
	Period string `json:"period,omitempty"`
	// Hold, when set, keeps the rows a submission uses.
	Hold *HoldSpec `json:"hold,omitempty"`
	// KeptBy lists tables that keep a row of Table while any of their
	// rows names it by the job's key columns, which each of them has.
	// Ingest's tables keep a removed device's row this way, so its device
	// id stays with its account while ingest holds anything from it
	// (D-device-account-binding).
	KeptBy []KeepSpec `json:"kept_by,omitempty"`
	// Awaits names the work package that creates Table. The job is off
	// while it is set.
	Awaits string `json:"awaits,omitempty"`
}

// KeepSpec is one table of a job's kept_by.
type KeepSpec struct {
	// Table is the table, which has the job's key columns.
	Table string `json:"table"`
	// Awaits names the work package that creates Table. The job runs
	// without this table's guard while it is set, which is right only
	// while the table doesn't exist.
	Awaits string `json:"awaits,omitempty"`
}

// HoldSpec names a table of holds. A hold row has the job's key columns,
// naming the row it holds, and DecidedColumn: NULL while its submission is
// open, then the time the submission was decided, in epoch milliseconds.
// A held row stays while its submission is open, and until the spot-check
// window after the decision has passed. The submission code deletes the
// holds of a submission that is withdrawn. The hold table is also the
// table of one of the entry's jobs by the window, with DecidedColumn as
// its time column, which deletes each hold once its window has closed.
type HoldSpec struct {
	// Table is the hold table.
	Table string `json:"table"`
	// DecidedColumn is the hold table's decision time.
	DecidedColumn string `json:"decided_column"`
	// Awaits names the work package that creates Table. The job runs
	// without the hold while it is set, which is right only while no
	// submission can exist.
	Awaits string `json:"awaits,omitempty"`
}

// Default returns the schedule in docs/retention.json.
func Default() (*Policy, error) {
	return Load(docs.RetentionJSON)
}

// Load parses and checks a schedule. An unknown field is an error, so a
// misspelt key can't silently drop a rule.
func Load(data []byte) (*Policy, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var p Policy
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("retention: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("retention: there is more after the schedule's closing brace")
	}
	if err := p.check(); err != nil {
		return nil, fmt.Errorf("retention: %w", err)
	}
	return &p, nil
}

var (
	// entryID is where.name.
	entryID = regexp.MustCompile(`^[a-z]+\.[a-z0-9_]+$`)
	// identifier is a table or column name. Jobs put them into SQL, so
	// nothing else gets through.
	identifier = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	// placeholder is a {name} in Kept.
	placeholder = regexp.MustCompile(`\{[^{}]*\}`)
)

func (p *Policy) check() error {
	var errs []error
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}
	if p.Version != 1 {
		fail("version is %d; this pairtime reads version 1", p.Version)
	}
	if p.SpotCheckWindow.Months < 1 {
		fail("spot_check_window.months is %d; it must be at least 1", p.SpotCheckWindow.Months)
	}
	if len(p.Entries) == 0 {
		fail("there are no entries")
	}

	// owner says which entry, or no_personal_data, each table belongs to.
	// One entry may name a table more than once, as a job's table and as
	// a hold.
	owner := make(map[string]string)
	// keepers are the kept_by tables in use, each with where it's named,
	// checked against owner once every table is claimed.
	type keeper struct{ at, table string }
	var keepers []keeper
	claim := func(table, by string) {
		if !identifier.MatchString(table) {
			fail("%s: table %q isn't a lowercase SQL name", by, table)
			return
		}
		if other, dup := owner[table]; dup && other != by {
			fail("table %s belongs to both %s and %s", table, other, by)
			return
		}
		owner[table] = by
	}
	for _, t := range p.NoPersonalData {
		claim(t, "no_personal_data")
	}

	ids := make(map[string]bool)
	for i, e := range p.Entries {
		at := fmt.Sprintf("entry %d", i+1)
		if e.ID != "" {
			at = e.ID
		}
		switch {
		case !slices.Contains(Places, e.Where):
			fail("%s: where is %q; use one of %s", at, e.Where, strings.Join(Places, ", "))
		case !entryID.MatchString(e.ID) || !strings.HasPrefix(e.ID, e.Where+"."):
			fail("%s: id must be where.name, such as %q", at, e.Where+".name")
		}
		if ids[e.ID] {
			fail("%s: the id is used twice", at)
		}
		ids[e.ID] = true
		if strings.TrimSpace(e.Data) == "" {
			fail("%s: data is empty", at)
		}
		if strings.TrimSpace(e.Kept) == "" {
			fail("%s: kept is empty", at)
		}
		for _, m := range placeholder.FindAllString(e.Kept, -1) {
			if m != "{days}" && m != "{window}" {
				fail("%s: kept has %s; the placeholders are {days} and {window}", at, m)
			}
		}
		saysDays := strings.Contains(e.Kept, "{days}")
		switch {
		case e.Days < 0:
			fail("%s: days is %d", at, e.Days)
		case e.Days > 0 && !saysDays:
			fail("%s: days is %d, but kept doesn't say {days}", at, e.Days)
		case e.Days == 0 && saysDays:
			fail("%s: kept says {days}, but days isn't set", at)
		}
		if e.Days > 0 && strings.ContainsAny(placeholder.ReplaceAllString(e.Kept, ""), "0123456789") {
			fail("%s: kept has a number besides {days}, which could drift from days", at)
		}
		hasTables := len(e.Tables) > 0 || len(e.LifetimeTables) > 0 || len(e.Jobs) > 0
		if hasTables && e.Where != Server && e.Where != Evidence {
			fail("%s: only server and evidence entries have tables and jobs, since only their data is in the server's database", at)
		}
		for _, t := range e.Tables {
			if e.HasPeriod() {
				fail("%s: the entry has a period, so nothing would delete table %s's rows on time; give it a job, or list it in lifetime_tables if its rows stay while the account exists", at, t)
			}
			claim(t, e.ID)
		}
		if len(e.LifetimeTables) > 0 && !e.HasPeriod() {
			fail("%s: lifetime_tables is for an entry with a period; list its tables in tables", at)
		}
		for _, t := range e.LifetimeTables {
			claim(t, e.ID)
		}
		jobTables := make(map[string]bool)
		for k, j := range e.Jobs {
			jat := fmt.Sprintf("%s job %d", at, k+1)
			switch j.Period {
			case "", PeriodDays:
				if e.Days == 0 {
					fail("%s: a job by days needs the entry's days", jat)
				}
			case PeriodWindow:
				if !strings.Contains(e.Kept, "{window}") {
					fail("%s: a job by the spot-check window needs kept to say {window}", jat)
				}
			default:
				fail("%s: period is %q; use %q or %q", jat, j.Period, PeriodDays, PeriodWindow)
			}
			if jobTables[j.Table] {
				fail("%s: another job of the entry deletes from %s", jat, j.Table)
			}
			jobTables[j.Table] = true
			if slices.Contains(e.LifetimeTables, j.Table) {
				fail("%s: %s is in lifetime_tables, but the job deletes its rows", jat, j.Table)
			}
			claim(j.Table, e.ID)
			if len(j.Key) == 0 {
				fail("%s: key is empty", jat)
			}
			keys := make(map[string]bool)
			for _, c := range j.Key {
				if !identifier.MatchString(c) {
					fail("%s: key column %q isn't a lowercase SQL name", jat, c)
				}
				if keys[c] {
					fail("%s: key lists %s twice", jat, c)
				}
				keys[c] = true
			}
			if !identifier.MatchString(j.TimeColumn) {
				fail("%s: time_column %q isn't a lowercase SQL name", jat, j.TimeColumn)
			}
			if h := j.Hold; h != nil {
				claim(h.Table, e.ID)
				if !identifier.MatchString(h.DecidedColumn) {
					fail("%s: hold.decided_column %q isn't a lowercase SQL name", jat, h.DecidedColumn)
				}
				if keys[h.DecidedColumn] {
					fail("%s: hold.decided_column %s is also a key column", jat, h.DecidedColumn)
				}
				// Without a job of its own, a hold would stay after its
				// window closed and its held row was deleted.
				deleted := slices.ContainsFunc(e.Jobs, func(o JobSpec) bool {
					return o.Table == h.Table && o.Period == PeriodWindow && o.TimeColumn == h.DecidedColumn
				})
				if !deleted {
					fail("%s: nothing deletes the holds in %s; give the entry a job on %s with \"period\": \"window\" and time_column %s, so each hold goes once its window has closed", jat, h.Table, h.Table, h.DecidedColumn)
				}
			}
			for n, k := range j.KeptBy {
				switch {
				case !identifier.MatchString(k.Table):
					fail("%s: kept_by table %q isn't a lowercase SQL name", jat, k.Table)
				case k.Table == j.Table:
					fail("%s: kept_by names the job's own table", jat)
				case slices.ContainsFunc(j.KeptBy[:n], func(o KeepSpec) bool { return o.Table == k.Table }):
					fail("%s: kept_by names %s twice", jat, k.Table)
				case k.Awaits == "":
					keepers = append(keepers, keeper{jat, k.Table})
				}
			}
		}
	}
	// A guard in use reads a table the schedule places, so a misspelt
	// name can't keep rows, or fail the job, unnoticed.
	for _, k := range keepers {
		if _, ok := owner[k.table]; !ok {
			fail("%s: kept_by table %s isn't in the schedule; give it an entry, or set awaits to the work package that creates it", k.at, k.table)
		}
	}
	return errors.Join(errs...)
}

// In returns the entries kept at where, in the schedule's order.
func (p *Policy) In(where string) []Entry {
	var out []Entry
	for _, e := range p.Entries {
		if e.Where == where {
			out = append(out, e)
		}
	}
	return out
}

// Find returns the entry with the given id.
func (p *Policy) Find(id string) (Entry, bool) {
	for _, e := range p.Entries {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

// Kept returns e.Kept with its placeholders filled in. It is the period
// the privacy notice shows for e.
func (p *Policy) Kept(e Entry) string {
	return strings.NewReplacer("{days}", Days(e.Days), "{window}", p.SpotCheckWindow.String()).Replace(e.Kept)
}

// Days says n days in words, such as "30 days".
func Days(n int) string {
	if n == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", n)
}

// Closes returns when the spot-check window of a decision made at decided
// closes: Months calendar months later, in UTC. When that month lacks the
// day, as one month after 31 March does, the window stays open to the end
// of the month and closes as the next month starts. The evidence store
// uses the same rule.
func (w Window) Closes(decided time.Time) time.Time {
	d := decided.UTC()
	c := d.AddDate(0, w.Months, 0)
	if c.Day() != d.Day() {
		// AddDate rolled an impossible date, such as 31 April, into the
		// next month.
		c = time.Date(c.Year(), c.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	return c
}

// lastClosed returns the latest decision time, to the millisecond, whose
// window of months has closed by now, as Window.Closes counts it. A hold
// keeps its row while its decision is later. Subtracting months from now
// isn't the inverse of Closes, because AddDate rolls an impossible date
// into the next month: 29 February 2028 less 12 months is 1 March 2027,
// which would release decisions of 1 March 2027 a day early.
func lastClosed(now time.Time, months int) time.Time {
	n := now.UTC()
	c := n.AddDate(0, -months, 0)
	if c.Day() != n.Day() {
		// No date months back has n's day, so every decision of the
		// shorter month has closed, and none of the next.
		c = time.Date(c.Year(), c.Month(), 1, 0, 0, 0, 0, time.UTC).Add(-time.Millisecond)
	}
	return c
}

// String says the window in plain words, such as "12 months after the
// decision, a default until Hack Club sets the window".
func (w Window) String() string {
	s := fmt.Sprintf("%d months after the decision", w.Months)
	if w.Months == 1 {
		s = "1 month after the decision"
	}
	if w.Provisional != "" {
		s += ", a default until Hack Club sets the window"
	}
	return s
}
