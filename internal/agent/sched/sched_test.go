// SPDX-License-Identifier: AGPL-3.0-or-later

package sched

import (
	"context"
	"maps"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// recorder counts each job's runs and keeps the time of its first run. Jobs
// run on the scheduler's goroutine, and the test reads the recorder only
// after Run has returned.
type recorder struct {
	start time.Time
	runs  map[string]int
	first map[string]time.Duration
}

func (r *recorder) job(name string) func(time.Time) {
	return func(now time.Time) {
		if r.runs[name] == 0 {
			r.first[name] = now.Sub(r.start)
		}
		r.runs[name]++
	}
}

// TestScheduler runs each case in a synctest bubble, where time is fake and
// moves on only while every goroutine waits, so minutes of scheduling take
// no real time and the counts are exact. The grid's origin is put at the
// bubble's start, plus the case's phase.
func TestScheduler(t *testing.T) {
	tests := []struct {
		name        string
		phase       time.Duration
		script      func(s *Scheduler, r *recorder)
		wantRuns    map[string]int
		wantFirst   map[string]time.Duration // checked only for the jobs listed
		wantWakeups uint64
		wantJobs    int // jobs still scheduled at the end
	}{
		{
			name: "an Every job runs once per interval, on the grid",
			script: func(s *Scheduler, r *recorder) {
				s.Every("presence", 15*time.Second, r.job("presence"))
				time.Sleep(time.Minute + time.Second)
			},
			wantRuns:    map[string]int{"presence": 4},
			wantFirst:   map[string]time.Duration{"presence": 15 * time.Second},
			wantWakeups: 4,
			wantJobs:    1,
		},
		{
			name: "jobs whose intervals divide each other share wakeups",
			script: func(s *Scheduler, r *recorder) {
				s.Every("presence", 15*time.Second, r.job("presence"))
				s.Every("procs", time.Minute, r.job("procs"))
				time.Sleep(2*time.Minute + time.Second)
			},
			wantRuns:    map[string]int{"presence": 8, "procs": 2},
			wantWakeups: 8,
			wantJobs:    2,
		},
		{
			name: "jobs added at different times still share the grid's wakeups",
			script: func(s *Scheduler, r *recorder) {
				s.Every("presence", 15*time.Second, r.job("presence"))
				time.Sleep(7 * time.Second)
				s.Every("procs", time.Minute, r.job("procs"))
				time.Sleep(2*time.Minute - 6*time.Second)
			},
			wantRuns:    map[string]int{"presence": 8, "procs": 2},
			wantFirst:   map[string]time.Duration{"procs": time.Minute},
			wantWakeups: 8,
			wantJobs:    2,
		},
		{
			name:  "the grid starts at the scheduler's phase",
			phase: 7 * time.Second,
			script: func(s *Scheduler, r *recorder) {
				s.Every("presence", 15*time.Second, r.job("presence"))
				time.Sleep(time.Minute)
			},
			wantRuns:    map[string]int{"presence": 4},
			wantFirst:   map[string]time.Duration{"presence": 7 * time.Second},
			wantWakeups: 4,
			wantJobs:    1,
		},
		{
			name: "an After job runs once and then leaves the list",
			script: func(s *Scheduler, r *recorder) {
				s.After("upload", 10*time.Second, r.job("upload"))
				time.Sleep(time.Minute)
			},
			wantRuns:    map[string]int{"upload": 1},
			wantFirst:   map[string]time.Duration{"upload": 10 * time.Second},
			wantWakeups: 1,
			wantJobs:    0,
		},
		{
			name: "Reset postpones an After job, which makes a debounce",
			script: func(s *Scheduler, r *recorder) {
				j := s.After("reload", 500*time.Millisecond, r.job("reload"))
				time.Sleep(300 * time.Millisecond)
				j.Reset(500 * time.Millisecond)
				time.Sleep(300 * time.Millisecond)
				j.Reset(500 * time.Millisecond)
				time.Sleep(time.Second)
			},
			wantRuns:    map[string]int{"reload": 1},
			wantFirst:   map[string]time.Duration{"reload": 1100 * time.Millisecond},
			wantWakeups: 1,
			wantJobs:    0,
		},
		{
			name: "Reset runs an After job again after it has run",
			script: func(s *Scheduler, r *recorder) {
				j := s.After("upload", time.Second, r.job("upload"))
				time.Sleep(2 * time.Second)
				j.Reset(time.Second)
				time.Sleep(2 * time.Second)
			},
			wantRuns:    map[string]int{"upload": 2},
			wantWakeups: 2,
			wantJobs:    0,
		},
		{
			name: "Stop keeps a job from running again",
			script: func(s *Scheduler, r *recorder) {
				j := s.Every("presence", 15*time.Second, r.job("presence"))
				time.Sleep(20 * time.Second)
				j.Stop()
				time.Sleep(time.Minute)
			},
			wantRuns:    map[string]int{"presence": 1},
			wantWakeups: 1,
			wantJobs:    0,
		},
		{
			name: "Reset gives an Every job a new interval on the grid",
			script: func(s *Scheduler, r *recorder) {
				j := s.Every("presence", 15*time.Second, r.job("presence"))
				time.Sleep(16 * time.Second)
				j.Reset(time.Minute) // runs at 60 s, the next minute on the grid
				time.Sleep(50 * time.Second)
			},
			wantRuns:    map[string]int{"presence": 2},
			wantWakeups: 2,
			wantJobs:    1,
		},
		{
			name: "a job can stop itself from its own run",
			script: func(s *Scheduler, r *recorder) {
				var self atomic.Pointer[Job]
				count := r.job("self")
				self.Store(s.Every("self", 15*time.Second, func(now time.Time) {
					count(now)
					if r.runs["self"] == 2 {
						self.Load().Stop()
					}
				}))
				time.Sleep(2 * time.Minute)
			},
			wantRuns:    map[string]int{"self": 2},
			wantWakeups: 2,
			wantJobs:    0,
		},
		{
			name: "adding and changing jobs doesn't count as a wakeup",
			script: func(s *Scheduler, r *recorder) {
				j := s.After("later", time.Hour, r.job("later"))
				for range 5 {
					time.Sleep(time.Second)
					j.Reset(time.Hour)
				}
				j.Stop()
				time.Sleep(time.Minute)
			},
			wantRuns:    map[string]int{},
			wantWakeups: 0,
			wantJobs:    0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				origin := time.Duration(start.UnixNano() % int64(day))
				s := newScheduler((origin + tt.phase) % day)
				r := &recorder{start: start, runs: map[string]int{}, first: map[string]time.Duration{}}

				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan struct{})
				go func() {
					s.Run(ctx)
					close(done)
				}()
				tt.script(s, r)
				cancel()
				<-done

				if !maps.Equal(r.runs, tt.wantRuns) {
					t.Errorf("runs = %v, want %v", r.runs, tt.wantRuns)
				}
				for name, want := range tt.wantFirst {
					if got := r.first[name]; got != want {
						t.Errorf("%s first ran %v after the start, want %v", name, got, want)
					}
				}
				if got := s.Wakeups(); got != tt.wantWakeups {
					t.Errorf("Wakeups() = %d, want %d", got, tt.wantWakeups)
				}
				if got := len(s.Jobs()); got != tt.wantJobs {
					t.Errorf("%d jobs still scheduled, want %d: %+v", got, tt.wantJobs, s.Jobs())
				}
			})
		})
	}
}

// TestEarliest calls runDue and earliest with chosen wall-clock times,
// which both take as a parameter, so it needs no fake clock. Times are
// relative to G, the grid time the job was due at: a real timer can fire a
// moment before it, because timers count monotonic time and the grid is in
// wall-clock time.
func TestEarliest(t *testing.T) {
	const every = 15 * time.Second
	tests := []struct {
		name string
		ran  time.Duration // when runDue ran the job
		now  time.Duration // when earliest looked
		want time.Duration // when the job is due next
	}{
		{name: "a job run a moment early stays due at the next grid time, so it doesn't run twice for one", ran: -5 * time.Millisecond, now: -4 * time.Millisecond, want: every},
		{name: "a job run as early as runDue allows stays due at the next grid time", ran: -early, now: -early, want: every},
		{name: "a job run on time stays due at the next grid time", ran: 0, now: time.Millisecond, want: every},
		{name: "a wall clock set back by more than an interval moves the job to the first grid time after the new time", ran: 0, now: -time.Minute, want: -45 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newScheduler(0)
			runs := 0
			s.Every("presence", every, func(time.Time) { runs++ })
			g := s.Jobs()[0].Next
			s.runDue(g.Add(tt.ran))
			if runs != 1 {
				t.Fatalf("runDue %v from G ran the job %d times, want 1", tt.ran, runs)
			}
			got, ok := s.earliest(g.Add(tt.now))
			if !ok || !got.Equal(g.Add(tt.want)) {
				t.Errorf("earliest(%v from G) = %v from G, %v; want %v from G", tt.now, got.Sub(g), ok, tt.want)
			}
			if next := s.Jobs()[0].Next; !next.Equal(g.Add(tt.want)) {
				t.Errorf("the job is due %v from G, want %v from G", next.Sub(g), tt.want)
			}
		})
	}
}

func TestGridAfter(t *testing.T) {
	epoch := time.Unix(0, 0)
	tests := []struct {
		name  string
		phase time.Duration
		t     time.Time
		every time.Duration
		want  time.Time
	}{
		{name: "a time on the grid moves to the next grid time", t: epoch.Add(30 * time.Second), every: 15 * time.Second, want: epoch.Add(45 * time.Second)},
		{name: "a time between grid times moves to the next one", t: epoch.Add(31 * time.Second), every: 15 * time.Second, want: epoch.Add(45 * time.Second)},
		{name: "the phase shifts the grid", phase: 7 * time.Second, t: epoch.Add(31 * time.Second), every: 15 * time.Second, want: epoch.Add(37 * time.Second)},
		{name: "a time before the grid's origin rounds up to it", phase: 7 * time.Second, t: epoch, every: 15 * time.Second, want: epoch.Add(7 * time.Second)},
		{name: "a time one interval before the origin rounds up to the origin", phase: 15 * time.Second, t: epoch, every: 15 * time.Second, want: epoch.Add(15 * time.Second)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := newScheduler(tt.phase).gridAfter(tt.t, tt.every)
			if !got.Equal(tt.want) {
				t.Errorf("gridAfter(%v, %v) = %v, want %v", tt.t.Sub(epoch), tt.every, got.Sub(epoch), tt.want.Sub(epoch))
			}
		})
	}
}
