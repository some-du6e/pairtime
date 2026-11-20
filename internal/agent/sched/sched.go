// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sched runs every timer in the agent, so the agent's own wakeups
// are one number (DESIGN 9.7; CP 1.3). An idle agent should wake about ten
// times a minute (CP 7), and most of those wakeups are periodic jobs: a
// presence sample every 15 s, and a process scan, an inbox drain and an
// upload check every 60 s (DESIGN 3.3). Periodic jobs run on one grid, so
// jobs whose intervals divide each other share wakeups: a 15 s job and a
// 60 s job together wake the agent four times a minute, not five.
//
// One goroutine runs every job, one at a time, so a job must return
// quickly. A job with slow work hands it to a goroutine of its own.
package sched

import (
	"cmp"
	"context"
	"math/rand/v2"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// day is the range the grid's phase is drawn from. Intervals that divide
// a day keep their alignment with each other whatever the phase is.
const day = 24 * time.Hour

// early is how far ahead of its due time a job may run. Timers count
// monotonic time while the grid is in wall-clock time, so when the two
// drift apart a timer can fire a moment before the wall-clock due time.
// Running the job then saves a second wakeup a moment later.
const early = 50 * time.Millisecond

// Scheduler runs jobs at their due times. Make one with New, add jobs with
// Every and After, and call Run once.
type Scheduler struct {
	// phase is the grid's offset from the Unix epoch. Each agent draws its
	// own, so devices don't all upload in the same second.
	phase time.Duration
	kick  chan struct{} // wakes Run when a job is added or changed

	wakeups atomic.Uint64

	mu   sync.Mutex
	jobs map[*Job]struct{} // the scheduled jobs
}

// Job is one timer, from Every or After.
type Job struct {
	s    *Scheduler
	name string
	fn   func(now time.Time)

	// Guarded by s.mu.
	every time.Duration // the period of an Every job; 0 for an After job
	next  time.Time     // when the job is due; zero when it isn't scheduled
	gen   uint64        // bumped by Stop and Reset, to cancel a run already picked
	runs  uint64
}

// New returns a scheduler whose grid has a random phase.
func New() *Scheduler {
	return newScheduler(time.Duration(rand.Int64N(int64(day))))
}

func newScheduler(phase time.Duration) *Scheduler {
	return &Scheduler{
		phase: phase,
		kick:  make(chan struct{}, 1),
		jobs:  make(map[*Job]struct{}),
	}
}

// Every adds a job that runs fn every interval, at the scheduler's grid
// times: the phase plus a whole number of intervals since the Unix epoch,
// in wall-clock time. The first run is at the next grid time, so it can be
// up to one interval away.
//
// fn gets the wall-clock time of the wakeup without a monotonic reading,
// so the difference between two runs is a wall-clock gap, which counts the
// time a laptop spent asleep on every OS (CP 2.2).
func (s *Scheduler) Every(name string, interval time.Duration, fn func(now time.Time)) *Job {
	if interval <= 0 {
		panic("sched: Every needs a positive interval")
	}
	j := &Job{s: s, name: name, fn: fn, every: interval}
	s.mu.Lock()
	j.next = s.gridAfter(time.Now(), interval)
	s.jobs[j] = struct{}{}
	s.mu.Unlock()
	s.wake()
	return j
}

// After adds a job that runs fn once, d from now.
func (s *Scheduler) After(name string, d time.Duration, fn func(now time.Time)) *Job {
	j := &Job{s: s, name: name, fn: fn}
	s.mu.Lock()
	j.next = time.Now().Add(d)
	s.jobs[j] = struct{}{}
	s.mu.Unlock()
	s.wake()
	return j
}

// Stop unschedules j. If j was due in the wakeup that is running, it
// doesn't run.
func (j *Job) Stop() {
	s := j.s
	s.mu.Lock()
	j.gen++
	j.next = time.Time{}
	delete(s.jobs, j)
	s.mu.Unlock()
	s.wake()
}

// Reset reschedules j. An After job runs once, d from now, whether or not
// it has run already, so calling Reset on every event makes a debounce. An
// Every job runs every d from now on, starting at the next grid time.
func (j *Job) Reset(d time.Duration) {
	s := j.s
	now := time.Now()
	s.mu.Lock()
	if j.every > 0 && d <= 0 {
		s.mu.Unlock()
		panic("sched: an Every job needs a positive interval")
	}
	j.gen++
	if j.every > 0 {
		j.every = d
		j.next = s.gridAfter(now, d)
	} else {
		j.next = now.Add(d)
	}
	s.jobs[j] = struct{}{}
	s.mu.Unlock()
	s.wake()
}

// Wakeups returns how many times the scheduler's timer has fired. Adding
// or changing a job doesn't count: that happens on a goroutine that was
// awake anyway.
func (s *Scheduler) Wakeups() uint64 {
	return s.wakeups.Load()
}

// JobInfo describes one scheduled job.
type JobInfo struct {
	Name  string
	Every time.Duration // 0 for a job from After
	Next  time.Time
	Runs  uint64
}

// Jobs returns the scheduled jobs, sorted by name. A job from After leaves
// the list once it has run.
func (s *Scheduler) Jobs() []JobInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	infos := make([]JobInfo, 0, len(s.jobs))
	for j := range s.jobs {
		infos = append(infos, JobInfo{Name: j.name, Every: j.every, Next: j.next, Runs: j.runs})
	}
	slices.SortFunc(infos, func(a, b JobInfo) int { return cmp.Compare(a.Name, b.Name) })
	return infos
}

// Run runs jobs as they fall due, until ctx is done. Call it once.
func (s *Scheduler) Run(ctx context.Context) {
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	for {
		var fire <-chan time.Time
		if next, ok := s.earliest(time.Now()); ok {
			timer.Reset(time.Until(next))
			fire = timer.C
		}
		select {
		case <-ctx.Done():
			return
		case <-s.kick:
			timer.Stop()
		case <-fire:
			s.wakeups.Add(1)
			s.runDue(time.Now())
		}
	}
}

// wake tells Run that the jobs changed.
func (s *Scheduler) wake() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// earliest returns the due time of the job due first. It also moves an
// Every job that is due more than one interval from now, which happens
// only when the wall clock was set back, to the next grid time, so a clock
// change can't stall it. A job that runDue ran up to early before its due
// time is due up to early more than one interval later, so that much
// counts as on time: moving that job back would run it twice for one grid
// time.
func (s *Scheduler) earliest(now time.Time) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var first time.Time
	for j := range s.jobs {
		if j.every > 0 && j.next.Sub(now) > j.every+early {
			j.next = s.gridAfter(now, j.every)
		}
		if first.IsZero() || j.next.Before(first) {
			first = j.next
		}
	}
	return first, !first.IsZero()
}

// runDue runs every job due by now, in order of due time. All of them are
// rescheduled before any runs, so a job can Reset or Stop itself or
// another job.
func (s *Scheduler) runDue(now time.Time) {
	type pick struct {
		j   *Job
		gen uint64
		at  time.Time
	}
	var due []pick
	limit := now.Add(early)
	s.mu.Lock()
	for j := range s.jobs {
		if j.next.After(limit) {
			continue
		}
		due = append(due, pick{j: j, gen: j.gen, at: j.next})
		if j.every > 0 {
			// From the later of now and the due time, so a job run a
			// little early doesn't come due again at the same grid time.
			from := now
			if j.next.After(from) {
				from = j.next
			}
			j.next = s.gridAfter(from, j.every)
		} else {
			j.next = time.Time{}
			delete(s.jobs, j)
		}
	}
	s.mu.Unlock()
	slices.SortFunc(due, func(a, b pick) int {
		if c := a.at.Compare(b.at); c != 0 {
			return c
		}
		return cmp.Compare(a.j.name, b.j.name)
	})
	wall := now.Round(0)
	for _, p := range due {
		s.mu.Lock()
		run := p.j.gen == p.gen
		if run {
			p.j.runs++
		}
		s.mu.Unlock()
		if run {
			p.j.fn(wall)
		}
	}
}

// gridAfter returns the first grid time for interval every that is after t.
func (s *Scheduler) gridAfter(t time.Time, every time.Duration) time.Time {
	n := t.UnixNano() - int64(s.phase)
	e := int64(every)
	k := n / e
	if n < 0 && n%e != 0 {
		k-- // round down, for times before the grid's origin
	}
	return time.Unix(0, (k+1)*e+int64(s.phase))
}
