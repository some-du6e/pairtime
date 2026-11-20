// SPDX-License-Identifier: AGPL-3.0-or-later

package sched

import (
	"context"
	"maps"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// recorder keeps the times each job ran at. Jobs run on the scheduler's
// goroutine, and the test reads the recorder only after Run has returned.
type recorder struct {
	at map[string][]time.Time
}

func (r *recorder) job(name string) func(time.Time) {
	return func(now time.Time) {
		r.at[name] = append(r.at[name], now)
	}
}

func (r *recorder) runs(name string) int { return len(r.at[name]) }

// TestScheduler runs each case in a synctest bubble, where time is fake and
// moves on only while every goroutine waits, so minutes of scheduling take
// no real time and the times are exact. The bubble's clock starts at
// midnight UTC, on a slot boundary, and the grid's origin is put at the
// bubble's start, plus the case's phase or spread.
func TestScheduler(t *testing.T) {
	tests := []struct {
		name   string
		phase  time.Duration // the grid's phase
		spread time.Duration // EverySpread's offset
		// fromNew takes the scheduler from New, with its random phase and
		// spread, instead.
		fromNew     bool
		script      func(s *Scheduler, r *recorder)
		wantRuns    map[string]int
		wantAt      map[string][]time.Duration // each run's time from the start; checked only for the jobs listed
		wantOnSlot  []string                   // jobs that may run only on slot boundaries
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
			wantAt:      map[string][]time.Duration{"presence": {15 * time.Second, 30 * time.Second, 45 * time.Second, time.Minute}},
			wantOnSlot:  []string{"presence"},
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
			wantAt:      map[string][]time.Duration{"procs": {time.Minute, 2 * time.Minute}},
			wantWakeups: 8,
			wantJobs:    2,
		},
		{
			name:  "a phase of whole slots moves 60 s jobs within the minute and keeps them on the 15 s job's wakeups",
			phase: 45 * time.Second,
			script: func(s *Scheduler, r *recorder) {
				s.Every("presence", 15*time.Second, r.job("presence"))
				s.Every("procs", time.Minute, r.job("procs"))
				time.Sleep(2*time.Minute + time.Second)
			},
			wantRuns:    map[string]int{"presence": 8, "procs": 2},
			wantAt:      map[string][]time.Duration{"procs": {45 * time.Second, 105 * time.Second}},
			wantOnSlot:  []string{"presence", "procs"},
			wantWakeups: 8,
			wantJobs:    2,
		},
		{
			name:    "Every jobs from New run on slot boundaries and share the 15 s job's wakeups, whatever phase New drew",
			fromNew: true,
			script: func(s *Scheduler, r *recorder) {
				s.Every("presence", 15*time.Second, r.job("presence"))
				s.Every("procs", time.Minute, r.job("procs"))
				time.Sleep(2*time.Minute + time.Second)
			},
			wantRuns:    map[string]int{"presence": 8, "procs": 2},
			wantOnSlot:  []string{"presence", "procs"},
			wantWakeups: 8,
			wantJobs:    2,
		},
		{
			name:   "an EverySpread job runs at the agent's spread offset, off the slot boundaries",
			spread: 7 * time.Second,
			script: func(s *Scheduler, r *recorder) {
				s.EverySpread("upload", time.Minute, r.job("upload"))
				time.Sleep(2*time.Minute + time.Second)
			},
			wantRuns:    map[string]int{"upload": 2},
			wantAt:      map[string][]time.Duration{"upload": {7 * time.Second, 67 * time.Second}},
			wantWakeups: 2,
			wantJobs:    1,
		},
		{
			name:   "an EverySpread job takes wakeups of its own beside Every jobs",
			spread: 7 * time.Second,
			script: func(s *Scheduler, r *recorder) {
				s.Every("presence", 15*time.Second, r.job("presence"))
				s.EverySpread("upload", time.Minute, r.job("upload"))
				time.Sleep(time.Minute + time.Second)
			},
			wantRuns:    map[string]int{"presence": 4, "upload": 1},
			wantAt:      map[string][]time.Duration{"upload": {7 * time.Second}},
			wantOnSlot:  []string{"presence"},
			wantWakeups: 5,
			wantJobs:    2,
		},
		{
			name:   "Reset keeps an EverySpread job's offset",
			spread: 7 * time.Second,
			script: func(s *Scheduler, r *recorder) {
				j := s.EverySpread("upload", time.Minute, r.job("upload"))
				time.Sleep(10 * time.Second)
				j.Reset(30 * time.Second) // next at 37 s, the next half minute on the spread grid
				time.Sleep(time.Minute)
			},
			wantRuns:    map[string]int{"upload": 3},
			wantAt:      map[string][]time.Duration{"upload": {7 * time.Second, 37 * time.Second, 67 * time.Second}},
			wantWakeups: 3,
			wantJobs:    1,
		},
		{
			name: "an After job runs once and then leaves the list",
			script: func(s *Scheduler, r *recorder) {
				s.After("upload", 10*time.Second, r.job("upload"))
				time.Sleep(time.Minute)
			},
			wantRuns:    map[string]int{"upload": 1},
			wantAt:      map[string][]time.Duration{"upload": {10 * time.Second}},
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
			wantAt:      map[string][]time.Duration{"reload": {1100 * time.Millisecond}},
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
			wantAt:      map[string][]time.Duration{"upload": {time.Second, 3 * time.Second}},
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
			wantAt:      map[string][]time.Duration{"presence": {15 * time.Second, time.Minute}},
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
					if r.runs("self") == 2 {
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
				s := newScheduler((origin+tt.phase)%day, (origin+tt.spread)%day)
				if tt.fromNew {
					s = New()
				}
				r := &recorder{at: map[string][]time.Time{}}

				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan struct{})
				go func() {
					s.Run(ctx)
					close(done)
				}()
				tt.script(s, r)
				cancel()
				<-done

				runs := map[string]int{}
				for name, at := range r.at {
					runs[name] = len(at)
				}
				if !maps.Equal(runs, tt.wantRuns) {
					t.Errorf("runs = %v, want %v", runs, tt.wantRuns)
				}
				for name, want := range tt.wantAt {
					var got []time.Duration
					for _, at := range r.at[name] {
						got = append(got, at.Sub(start))
					}
					if !slices.Equal(got, want) {
						t.Errorf("%s ran at %v from the start, want %v", name, got, want)
					}
				}
				for _, name := range tt.wantOnSlot {
					for _, at := range r.at[name] {
						if off := time.Duration(at.UnixNano() % int64(slot)); off != 0 {
							t.Errorf("%s ran at %s, %v past a slot boundary", name, at.UTC().Format("15:04:05.000"), off)
						}
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

// TestNew checks what New draws over many schedulers, so that one lucky
// draw can't pass a case.
func TestNew(t *testing.T) {
	const draws = 1000
	ss := make([]*Scheduler, draws)
	for i := range ss {
		ss[i] = New()
	}
	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{
			name: "the grid's phase is a whole number of slots within a day, so Every jobs run on slot boundaries",
			check: func(t *testing.T) {
				for _, s := range ss {
					if s.phase < 0 || s.phase >= day || s.phase%slot != 0 {
						t.Fatalf("phase %v isn't a whole number of %v slots in [0, %v)", s.phase, slot, day)
					}
				}
			},
		},
		{
			name: "agents draw different phases, so their daily jobs don't all run at one time of day",
			check: func(t *testing.T) {
				seen := map[time.Duration]bool{}
				for _, s := range ss {
					seen[s.phase] = true
				}
				if len(seen) < draws/2 {
					t.Errorf("%d draws gave only %d different phases", draws, len(seen))
				}
			},
		},
		{
			name: "the spread offset is within a day and isn't held to slot boundaries",
			check: func(t *testing.T) {
				off := 0
				for _, s := range ss {
					if s.spread < 0 || s.spread >= day {
						t.Fatalf("spread %v isn't in [0, %v)", s.spread, day)
					}
					if s.spread%slot != 0 {
						off++
					}
				}
				if off < draws/2 {
					t.Errorf("only %d of %d spread offsets are off the slot boundaries", off, draws)
				}
			},
		},
		{
			name: "spread offsets fall in every quarter of a minute, so a 60 s EverySpread job's calls spread over the minute",
			check: func(t *testing.T) {
				var quarters [4]int
				for _, s := range ss {
					quarters[(s.spread%time.Minute)/slot]++
				}
				for q, n := range quarters {
					if n == 0 {
						t.Errorf("no spread offset fell %v to %v into the minute: %v", time.Duration(q)*slot, time.Duration(q+1)*slot, quarters)
					}
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.check)
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
			s := newScheduler(0, 0)
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
			got := gridAfter(tt.t, tt.every, tt.phase)
			if !got.Equal(tt.want) {
				t.Errorf("gridAfter(%v, %v, %v) = %v, want %v", tt.t.Sub(epoch), tt.every, tt.phase, got.Sub(epoch), tt.want.Sub(epoch))
			}
		})
	}
}
