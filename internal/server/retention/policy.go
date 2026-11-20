// SPDX-License-Identifier: AGPL-3.0-or-later

// Package retention holds pairtime's retention schedule and pairtimed's
// retention jobs (DESIGN 4.4; WP-SV14).
//
// The schedule is docs/retention.json, embedded by package docs, and Policy
// is that file loaded and checked. The privacy notice at /privacy renders
// its retention table from a Policy (package pages), and Sweep runs the
// jobs the same Policy describes, so the page and the jobs can't disagree.
//
// A server entry may bind a job to a table. The job deletes the table's
// rows whose time column is at least the entry's days old, except rows a
// hold keeps. That one shape covers DESIGN 4.4's four server jobs:
//
//   - IP addresses and JA4 fingerprints, 30 days after they were seen.
//   - Per-minute buckets, 180 days after their minute. Ingest keeps the
//     daily totals per project current in rollups_daily (WP-SV7), so
//     deleting the minute rows leaves exactly those totals.
//   - Witness rows, 180 days after they were received, unless a
//     submission still inside its spot-check window uses them.
//   - Device rows, 90 days after the device was removed. A device that
//     hasn't been removed has no removal time, so its row stays.
//
// The tables belong to the work packages that create them. Until a table
// exists, its job or hold names that package in awaits and doesn't run.
// TestTables fails when a table the migrations create has no entry, when a
// job's table lacks the columns its entry names, and when an awaited table
// appears, so the package that creates a table also turns its job on.
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
	// Tables lists server tables that hold this data, besides Job's.
	Tables []string `json:"tables,omitempty"`
	// Job, when set, is the server job that enforces Days.
	Job *JobSpec `json:"job,omitempty"`
}

// JobSpec binds an entry to the table its job deletes from.
type JobSpec struct {
	// Table is the table the job deletes rows of.
	Table string `json:"table"`
	// Key is the table's primary key. The job deletes by it.
	Key []string `json:"key"`
	// TimeColumn holds when each row's period started, in epoch
	// milliseconds. A row whose TimeColumn is NULL never expires.
	TimeColumn string `json:"time_column"`
	// Hold, when set, keeps the rows a submission uses.
	Hold *HoldSpec `json:"hold,omitempty"`
	// Awaits names the work package that creates Table. The job is off
	// while it is set.
	Awaits string `json:"awaits,omitempty"`
}

// HoldSpec names a table of holds. A hold row has the job's key columns,
// naming the row it holds, and DecidedColumn: NULL while its submission is
// open, then the time the submission was decided, in epoch milliseconds.
// A held row stays while its submission is open, and until the spot-check
// window after the decision has passed. The submission code deletes the
// holds of a submission that is withdrawn.
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

	owner := make(map[string]string)
	claim := func(table, by string) {
		if !identifier.MatchString(table) {
			fail("%s: table %q isn't a lowercase SQL name", by, table)
			return
		}
		if other, dup := owner[table]; dup {
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
		for _, t := range e.Tables {
			if e.Where != Server {
				fail("%s: only server entries have tables", at)
			}
			claim(t, e.ID)
		}
		j := e.Job
		if j == nil {
			continue
		}
		if e.Where != Server {
			fail("%s: only server entries have jobs", at)
		}
		if e.Days == 0 {
			fail("%s: a job needs days", at)
		}
		claim(j.Table, e.ID)
		if len(j.Key) == 0 {
			fail("%s: job.key is empty", at)
		}
		keys := make(map[string]bool)
		for _, c := range j.Key {
			if !identifier.MatchString(c) {
				fail("%s: job.key column %q isn't a lowercase SQL name", at, c)
			}
			if keys[c] {
				fail("%s: job.key lists %s twice", at, c)
			}
			keys[c] = true
		}
		if !identifier.MatchString(j.TimeColumn) {
			fail("%s: job.time_column %q isn't a lowercase SQL name", at, j.TimeColumn)
		}
		if h := j.Hold; h != nil {
			claim(h.Table, e.ID+" (hold)")
			if !identifier.MatchString(h.DecidedColumn) {
				fail("%s: hold.decided_column %q isn't a lowercase SQL name", at, h.DecidedColumn)
			}
			if keys[h.DecidedColumn] {
				fail("%s: hold.decided_column %s is also a key column", at, h.DecidedColumn)
			}
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
