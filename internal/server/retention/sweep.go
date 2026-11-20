// SPDX-License-Identifier: AGPL-3.0-or-later

package retention

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/server/writer"
)

// Job is a server retention job, ready to run.
type Job struct {
	// ID is its entry's id, such as "server.ip_ja4".
	ID string
	// Table, Key and TimeColumn are the entry's binding.
	Table      string
	Key        []string
	TimeColumn string
	// MaxAge is the entry's period: rows whose time is at least this old
	// are deleted.
	MaxAge time.Duration
	// Hold, when set, keeps the rows a submission uses.
	Hold *Hold
}

// Hold is the hold table a job consults.
type Hold struct {
	Table         string
	DecidedColumn string
	// WindowMonths is the spot-check window.
	WindowMonths int
}

// Jobs returns the jobs whose tables exist: those without awaits, each
// with its hold unless the hold awaits its table. off lists the rest, as
// "server.devices awaits WP-SV3", for the log.
func (p *Policy) Jobs() (jobs []Job, off []string) {
	for _, e := range p.Entries {
		j := e.Job
		if j == nil {
			continue
		}
		if j.Awaits != "" {
			off = append(off, e.ID+" awaits "+j.Awaits)
			continue
		}
		job := p.job(e)
		if h := j.Hold; h != nil && h.Awaits != "" {
			off = append(off, e.ID+" hold awaits "+h.Awaits)
			job.Hold = nil
		}
		jobs = append(jobs, job)
	}
	return jobs, off
}

// AllJobs returns every job with its hold, whether or not its tables exist
// yet. Tests run them against tables they create.
func (p *Policy) AllJobs() []Job {
	var jobs []Job
	for _, e := range p.Entries {
		if e.Job != nil {
			jobs = append(jobs, p.job(e))
		}
	}
	return jobs
}

func (p *Policy) job(e Entry) Job {
	j := Job{
		ID:         e.ID,
		Table:      e.Job.Table,
		Key:        e.Job.Key,
		TimeColumn: e.Job.TimeColumn,
		MaxAge:     time.Duration(e.Days) * 24 * time.Hour,
	}
	if h := e.Job.Hold; h != nil {
		j.Hold = &Hold{Table: h.Table, DecidedColumn: h.DecidedColumn, WindowMonths: p.SpotCheckWindow.Months}
	}
	return j
}

// DefaultBatch is how many rows one delete removes at most. It is the
// writer's MaxRows, so a retention delete never makes a writer transaction
// bigger than a full ingest batch does.
const DefaultBatch = writer.DefaultMaxRows

// maxBatches bounds one job's deletes in one sweep, so a job that keeps
// finding rows can't keep the writer busy for long. The next sweep goes
// on where this one stopped.
const maxBatches = 2000

// Doer runs a write through the writer goroutine. *writer.Writer is one.
type Doer interface {
	Do(ctx context.Context, rows int, op writer.Op) error
}

// Result says what one job did in one sweep.
type Result struct {
	ID      string
	Deleted int64
	Err     error
}

// Sweep runs each job once as of now. A job deletes at most batch rows per
// writer op, and stops when a delete removes fewer. A job that fails has
// the error in its Result, and the jobs after it still run.
func Sweep(ctx context.Context, d Doer, jobs []Job, now time.Time, batch int) []Result {
	if batch < 1 {
		batch = DefaultBatch
	}
	results := make([]Result, 0, len(jobs))
	for _, j := range jobs {
		r := Result{ID: j.ID}
		query, args := j.deleteSQL(now, batch)
		for range maxBatches {
			var n int64
			err := d.Do(ctx, batch, func(ctx context.Context, tx writer.Tx) error {
				res, err := tx.ExecContext(ctx, query, args...)
				if err != nil {
					return err
				}
				n, err = res.RowsAffected()
				return err
			})
			if err != nil {
				r.Err = fmt.Errorf("retention: %s: %w", j.ID, err)
				break
			}
			r.Deleted += n
			if n < int64(batch) {
				break
			}
		}
		results = append(results, r)
	}
	return results
}

// deleteSQL is one batch's DELETE and its arguments, in the portable SQL of
// package store. A row whose time is at or before now minus MaxAge is past
// its period. A hold keeps its row while the hold's decision time is NULL,
// because the submission is open, or later than now minus the window,
// because the window closes when the decision is that old.
func (j Job) deleteSQL(now time.Time, batch int) (string, []any) {
	col := func(alias string, cols []string) []string {
		out := make([]string, len(cols))
		for i, c := range cols {
			out[i] = alias + "." + c
		}
		return out
	}
	var b strings.Builder
	b.WriteString("DELETE FROM " + j.Table + " WHERE ")
	if len(j.Key) == 1 {
		b.WriteString(j.Key[0])
	} else {
		b.WriteString("(" + strings.Join(j.Key, ", ") + ")")
	}
	b.WriteString(" IN (SELECT " + strings.Join(col("t", j.Key), ", ") + " FROM " + j.Table + " t WHERE t." + j.TimeColumn + " <= $1")
	args := []any{now.Add(-j.MaxAge).UnixMilli(), batch}
	if h := j.Hold; h != nil {
		match := make([]string, len(j.Key))
		for i, k := range j.Key {
			match[i] = "h." + k + " = t." + k
		}
		decided := "h." + h.DecidedColumn
		b.WriteString(" AND NOT EXISTS (SELECT 1 FROM " + h.Table + " h WHERE " + strings.Join(match, " AND ") +
			" AND (" + decided + " IS NULL OR " + decided + " > $3))")
		args = append(args, now.AddDate(0, -h.WindowMonths, 0).UnixMilli())
	}
	b.WriteString(" LIMIT $2)")
	return b.String(), args
}

// Run sweeps at once, then every interval, until ctx ends. report, when
// set, gets every job's result from every sweep.
func Run(ctx context.Context, d Doer, jobs []Job, every time.Duration, now func() time.Time, report func(Result)) {
	if len(jobs) == 0 {
		return
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		for _, r := range Sweep(ctx, d, jobs, now(), DefaultBatch) {
			if report != nil {
				report(r)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
