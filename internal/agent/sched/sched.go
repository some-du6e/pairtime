// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sched runs every timer in the agent, so the agent's own wakeups
// are one number (DESIGN 9.7; CP 1.3). An idle agent should wake about ten
// times a minute (CP 7), and most of those wakeups are periodic jobs: a
// presence sample every 15 s, and a process scan and an inbox drain every
// 60 s (DESIGN 3.3). Periodic jobs run on one grid, so jobs whose
// intervals divide each other share wakeups: a 15 s job and a 60 s job
// together wake the agent four times a minute, not five.
//
// The grid is on SM 1.3's slot boundaries, :00, :15, :30 and :45 of every
// minute of the wall clock. SM 2.1 sets each slot's input from idle time,
// which reports only the latest input, so only a sample taken as a slot
// ends tells for certain whether the slot had any. Each agent's grid is a
// random whole number of slots from the Unix epoch, so its 60 s jobs run
// on one of the four boundaries, and devices don't all run their daily
// jobs at the same time of day.
//
// A job that calls the server, such as the upload check every 60 s, uses
// EverySpread instead. Its grid has an offset that each agent draws to the
// nanosecond, so devices spread their calls over the interval instead of
// all making them on the same slot boundary. That costs the job a wakeup
// of its own.
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

// slot is SM 1.3's 15-second slot, which starts at :00, :15, :30 or :45
// of a minute. The grid's phase is a whole number of slots.
const slot = 15 * time.Second

// day is the range the grid's phase and the spread offset are drawn from.
// Intervals that divide a day keep their alignment with each other
// whatever the phase is.
const day = 24 * time.Hour

// early is how far ahead of its due time a job may run. Timers count
// monotonic time while the grid is in wall-clock time, so when the two
// drift apart a timer can fire a moment before the wall-clock due time.
// Running the job then saves a second wakeup a moment later.
const early = 50 * time.Millisecond

// Scheduler runs jobs at their due times. Make one with New, add jobs with
// Every, EverySpread and After, and call Run once.
type Scheduler struct {
	// phase is the grid's offset from the Unix epoch: a whole number of
	// slots, which each agent draws.
	phase time.Duration
	// spread is the offset of EverySpread's grid, which each agent draws
	// to the nanosecond, so devices don't all call the server in the same
	// second.
	spread time.Duration
	kick   chan struct{} // wakes Run when a job is added or changed

	wakeups atomic.Uint64

	mu   sync.Mutex
	jobs map[*Job]struct{} // the scheduled jobs
}

// Job is one timer, from Every, EverySpread or After.
type Job struct {
	s     *Scheduler
	name  string
	fn    func(now time.Time)
	phase time.Duration // the offset of a periodic job's grid: the scheduler's phase or its spread

	// Guarded by s.mu.
	every time.Duration // the period of a periodic job; 0 for an After job
	next  time.Time     // when the job is due; zero when it isn't scheduled
	gen   uint64        // bumped by Stop and Reset, to cancel a run already picked
	runs  uint64
}

// New returns a scheduler whose grid is a random whole number of slots from
// the Unix epoch, with a random offset for EverySpread.
func New() *Scheduler {
	return newScheduler(
		time.Duration(rand.Int64N(int64(day/slot)))*slot,
		time.Duration(rand.Int64N(int64(day))),
	)
}

func newScheduler(phase, spread time.Duration) *Scheduler {
	return &Scheduler{
		phase:  phase,
		spread: spread,
		kick:   make(chan struct{}, 1),
		jobs:   make(map[*Job]struct{}),
	}
}

// Every adds a job that runs fn every interval, at the scheduler's grid
// times: the phase plus a whole number of intervals since the Unix epoch,
// in wall-clock time. For an interval that is a whole number of slots,
// such as 15 s or 60 s, those are slot boundaries. The first run is at the
// next grid time, so it can be up to one interval away.
//
// fn gets the wall-clock time of the wakeup without a monotonic reading,
// so the difference between two runs is a wall-clock gap, which counts the
// time a laptop spent asleep on every OS (CP 2.2).
func (s *Scheduler) Every(name string, interval time.Duration, fn func(now time.Time)) *Job {
	return s.every(name, interval, s.phase, fn)
}

// EverySpread is Every for a job that calls the server, such as the upload
// check (DESIGN 3.3). Its grid has an offset of its own, the scheduler's
// spread, which each agent draws at random, so devices make their calls at
// different moments of the interval instead of all on the same slot
// boundary. It costs a wakeup that Every jobs don't share.
func (s *Scheduler) EverySpread(name string, interval time.Duration, fn func(now time.Time)) *Job {
	return s.every(name, interval, s.spread, fn)
}

func (s *Scheduler) every(name string, interval, phase time.Duration, fn func(now time.Time)) *Job {
	if interval <= 0 {
		panic("sched: Every needs a positive interval")
	}
	j := &Job{s: s, name: name, fn: fn, phase: phase, every: interval}
	s.mu.Lock()
	j.next = gridAfter(time.Now(), interval, phase)
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
// Every or EverySpread job runs every d from now on, starting at the next
// time on its grid.
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
		j.next = gridAfter(now, d, j.phase)
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

// earliest returns the due time of the job due first. It also moves a
// periodic job that is due more than one interval from now, which happens
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
			j.next = gridAfter(now, j.every, j.phase)
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
			j.next = gridAfter(from, j.every, j.phase)
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

// gridAfter returns the first time after t that is phase plus a whole
// number of intervals every from the Unix epoch.
func gridAfter(t time.Time, every, phase time.Duration) time.Time {
	n := t.UnixNano() - int64(phase)
	e := int64(every)
	k := n / e
	if n < 0 && n%e != 0 {
		k-- // round down, for times before the grid's origin
	}
	return time.Unix(0, (k+1)*e+int64(phase))
}
