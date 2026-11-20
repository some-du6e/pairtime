// SPDX-License-Identifier: AGPL-3.0-or-later

package presence

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/sched"
	"github.com/some-du6e/pairtime/pkg/event"
)

// person is a fake OS input queue: the moments someone touched the
// keyboard or mouse, as offsets from the test's start. Before the first
// one, the last input was an hour before the start.
type person struct {
	start  time.Time
	inputs []time.Duration
	keys   bool                         // the OS tells keys apart, as macOS does
	fail   func(at time.Duration) error // when set, a read at this offset fails if it returns an error
}

func (p *person) idle() (Idle, error) {
	now := time.Now().Sub(p.start)
	if p.fail != nil {
		if err := p.fail(now); err != nil {
			return Idle{}, err
		}
	}
	last := -time.Hour
	for _, in := range p.inputs {
		if in <= now {
			last = in
		}
	}
	return Idle{Any: now - last, Keys: now - last, HasKeys: p.keys}, nil
}

// env is what a test's acts reach: the sampler, and the functions the
// sampler gave the fake power watches.
type env struct {
	t                           *testing.T
	s                           *Sampler
	suspend, lock               func(event.PowerOp)
	suspendStopped, lockStopped bool
}

type act struct {
	at time.Duration
	do func(e *env)
}

func at(d time.Duration, do func(e *env)) act { return act{at: d, do: do} }

func focusOn(c event.AppClass) func(*env) { return func(e *env) { e.s.Focus(c) } }

func hookOf(session string, k event.Kind) func(*env) {
	return func(e *env) { e.s.Hook(session, k) }
}

func power(op event.PowerOp) func(*env) { return func(e *env) { e.s.Power(op) } }

// every returns the times from from to to, step apart.
func every(from, to, step time.Duration) []time.Duration {
	var ts []time.Duration
	for t := from; t <= to; t += step {
		ts = append(ts, t)
	}
	return ts
}

// describe writes a record as a test expects it: its time after the start,
// then its kind and values.
func describe(start time.Time, r Record) string {
	at := r.At.Sub(start)
	switch d := r.Data.(type) {
	case *event.OSPresence:
		s := fmt.Sprintf("%v presence idle=%g", at, d.IdleAnyS)
		if d.IdleKeysS != nil {
			s += fmt.Sprintf(" keys=%g", *d.IdleKeysS)
		}
		if d.Locked {
			s += " locked"
		}
		return s
	case *event.OSPower:
		return fmt.Sprintf("%v power %s", at, d.Op)
	}
	return fmt.Sprintf("%v %T", at, r.Data)
}

// TestSampler runs the sampler on the agent's own scheduler in a synctest
// bubble, where the clock is fake and moves only while every goroutine
// waits, so each case's minutes take no real time and every timestamp is
// exact. The bubble's clock starts at midnight, a slot boundary.
func TestSampler(t *testing.T) {
	tests := []struct {
		name         string
		inputs       []time.Duration
		keys         bool
		failIdle     func(at time.Duration) error
		noIdle       bool           // the OS has no idle source
		locked       bool           // the session is locked at the start
		lockErr      error          // reading the lock state fails
		lockWatchErr error          // the lock watch fails to start
		before       []func(e *env) // calls made before Start
		acts         []act
		end          time.Duration
		// want lists every record. When it is nil, wantSamples lists the
		// times of the samples, and there must be no other records.
		want         []string
		wantSamples  []time.Duration
		wantSeals    []event.PowerOp
		wantProblems []string
		noTimer      bool // no sample is scheduled at the end
	}{
		{
			name: "outside a project context nothing is sampled or recorded, but suspends and locks still seal",
			acts: []act{
				at(s(30), power(event.PowerLock)),
				at(s(40), power(event.PowerUnlock)),
				at(s(50), focusOn(event.AppBrowser)),
				at(s(70), power(event.PowerSuspend)),
				at(s(75), power(event.PowerResume)),
			},
			end:       2 * time.Minute,
			want:      []string{},
			wantSeals: []event.PowerOp{event.PowerLock, event.PowerSuspend},
			noTimer:   true,
		},
		{
			name:   "a context takes a sample at once, then one on every 15 s slot boundary",
			inputs: []time.Duration{s(5), s(20)},
			acts:   []act{at(s(7), focusOn(event.AppTerminal))},
			end:    s(61),
			want: []string{
				"7s presence idle=2",
				"15s presence idle=10",
				"30s presence idle=10",
				"45s presence idle=25",
				"1m0s presence idle=40",
			},
		},
		{
			name:   "focus reported before Start makes a context, sampled once Start runs",
			inputs: []time.Duration{-s(2)},
			before: []func(e *env){focusOn(event.AppTerminal)},
			end:    s(16),
			want:   []string{"0s presence idle=2", "15s presence idle=17"},
		},
		{
			name:   "a hook takes a sample at once, between the boundaries",
			inputs: []time.Duration{s(0.5)},
			acts: []act{
				at(s(1), focusOn(event.AppTerminal)),
				at(s(22), hookOf("s_a", event.KindPromptSubmit)),
			},
			end: s(31),
			want: []string{
				"1s presence idle=0.5",
				"15s presence idle=14.5",
				"22s presence idle=21.5",
				"30s presence idle=29.5",
			},
		},
		{
			name:   "a hook less than a second after the last sample takes none",
			inputs: []time.Duration{s(0.5)},
			acts: []act{
				at(s(1), focusOn(event.AppTerminal)),
				at(s(22), hookOf("s_a", event.KindPromptSubmit)),
				at(s(22.5), hookOf("s_a", event.KindAgentTool)),
				at(s(23.2), hookOf("s_a", event.KindAgentTool)),
				at(s(30.4), hookOf("s_a", event.KindAgentTool)),
			},
			end: s(31),
			want: []string{
				"1s presence idle=0.5",
				"15s presence idle=14.5",
				"22s presence idle=21.5",
				"23.2s presence idle=22.7",
				"30s presence idle=29.5",
			},
		},
		{
			name:   "a focus change in a live context takes a sample at once",
			inputs: []time.Duration{s(0.5)},
			acts: []act{
				at(s(1), focusOn(event.AppTerminal)),
				at(s(8), focusOn(event.AppBrowser)),
			},
			end:  s(16),
			want: []string{"1s presence idle=0.5", "8s presence idle=7.5", "15s presence idle=14.5"},
		},
		{
			name:   "a lock and an unlock are recorded, each with a sample after it, and the lock seals",
			inputs: []time.Duration{s(0.5), s(52)},
			acts: []act{
				at(s(1), focusOn(event.AppTerminal)),
				at(s(20), power(event.PowerLock)),
				at(s(50), power(event.PowerUnlock)),
			},
			end: s(61),
			want: []string{
				"1s presence idle=0.5",
				"15s presence idle=14.5",
				"20s power lock",
				"20s presence idle=19.5 locked",
				"30s presence idle=29.5 locked",
				"45s presence idle=44.5 locked",
				"50s power unlock",
				"50s presence idle=49.5",
				"1m0s presence idle=8",
			},
			wantSeals: []event.PowerOp{event.PowerLock},
		},
		{
			name:   "a suspend is recorded and seals, and the resume is recorded",
			inputs: []time.Duration{s(0.5)},
			acts: []act{
				at(s(1), focusOn(event.AppTerminal)),
				at(s(20), power(event.PowerSuspend)),
				at(s(25), power(event.PowerResume)),
			},
			end: s(31),
			want: []string{
				"1s presence idle=0.5",
				"15s presence idle=14.5",
				"20s power suspend",
				"20s presence idle=19.5",
				"25s power resume",
				"25s presence idle=24.5",
				"30s presence idle=29.5",
			},
			wantSeals: []event.PowerOp{event.PowerSuspend},
		},
		{
			name:   "power events from the OS's watches reach the sampler",
			inputs: []time.Duration{s(0.5)},
			acts: []act{
				at(s(1), focusOn(event.AppTerminal)),
				at(s(5), func(e *env) { e.lock(event.PowerLock) }),
				at(s(6), func(e *env) { e.suspend(event.PowerSuspend) }),
			},
			end: s(7),
			want: []string{
				"1s presence idle=0.5",
				"5s power lock",
				"5s presence idle=4.5 locked",
				"6s power suspend",
				"6s presence idle=5.5 locked",
			},
			wantSeals: []event.PowerOp{event.PowerLock, event.PowerSuspend},
		},
		{
			name:   "600 s with no input slow sampling to every minute, and input brings back every 15 s",
			inputs: []time.Duration{s(0.5), s(700)},
			acts:   []act{at(s(1), focusOn(event.AppTerminal))},
			end:    s(781),
			wantSamples: slices.Concat(
				[]time.Duration{s(1)},
				every(s(15), s(615), s(15)),
				[]time.Duration{s(660), s(720)},
				every(s(735), s(780), s(15)),
			),
		},
		{
			name: "with nobody at the keyboard a context samples every minute and stops 600 s after the terminal lost focus",
			acts: []act{
				at(s(1), focusOn(event.AppTerminal)),
				at(s(2), focusOn(event.AppBrowser)),
			},
			end:         15 * time.Minute,
			wantSamples: slices.Concat([]time.Duration{s(1), s(2)}, every(s(60), s(600), s(60))),
			noTimer:     true,
		},
		{
			name:   "a running harness process makes a context, sampled at once, until the scanner counts none",
			inputs: []time.Duration{s(2)},
			acts: []act{
				at(s(3), func(e *env) { e.s.HarnessProcesses(1) }),
				at(s(31), func(e *env) { e.s.HarnessProcesses(0) }),
			},
			end:     s(61),
			want:    []string{"3s presence idle=1", "15s presence idle=13", "30s presence idle=28"},
			noTimer: true,
		},
		{
			name:   "a process count in a live context takes no extra sample",
			inputs: []time.Duration{s(0.5)},
			acts: []act{
				at(s(1), focusOn(event.AppTerminal)),
				at(s(5), func(e *env) { e.s.HarnessProcesses(1) }),
			},
			end:  s(16),
			want: []string{"1s presence idle=0.5", "15s presence idle=14.5"},
		},
		{
			name:   "Stop ends sampling, and later events record nothing",
			inputs: []time.Duration{s(0.5)},
			acts: []act{
				at(s(1), focusOn(event.AppTerminal)),
				at(s(20), func(e *env) { e.s.Stop() }),
				at(s(30), focusOn(event.AppEditor)),
				at(s(31), power(event.PowerLock)),
			},
			end:     s(61),
			want:    []string{"1s presence idle=0.5", "15s presence idle=14.5"},
			noTimer: true,
		},
		{
			name:   "a failed idle read records nothing and is reported until a read works",
			inputs: []time.Duration{s(0.5)},
			failIdle: func(at time.Duration) error {
				if at < s(20) {
					return errors.New("access denied")
				}
				return nil
			},
			acts: []act{at(s(1), focusOn(event.AppTerminal))},
			end:  s(31),
			want: []string{"30s presence idle=29.5"},
			wantProblems: []string{
				"couldn't read the input idle time, so presence has gaps: access denied",
				"",
			},
		},
		{
			name:   "a session locked at the start is sampled as locked",
			locked: true,
			acts:   []act{at(s(1), focusOn(event.AppTerminal))},
			end:    s(2),
			want:   []string{"1s presence idle=3601 locked"},
		},
		{
			name:    "a lock state that can't be read is reported and taken as unlocked",
			lockErr: errors.New("no such session"),
			acts:    []act{at(s(1), focusOn(event.AppTerminal))},
			end:     s(2),
			want:    []string{"1s presence idle=3601"},
			wantProblems: []string{
				"couldn't read whether the session is locked, so presence takes it as unlocked until the next lock: no such session",
			},
		},
		{
			name:         "a power watch that fails to start is reported, and sampling goes on without it",
			lockWatchErr: errors.New("WTSRegisterSessionNotification: access denied"),
			acts:         []act{at(s(1), focusOn(event.AppTerminal))},
			end:          s(2),
			want:         []string{"1s presence idle=3601"},
			wantProblems: []string{"lock and unlock go unrecorded: WTSRegisterSessionNotification: access denied"},
		},
		{
			name:   "an OS that tells keys from other input records idle_keys_s",
			inputs: []time.Duration{s(0.5)},
			keys:   true,
			acts:   []act{at(s(1), focusOn(event.AppTerminal))},
			end:    s(2),
			want:   []string{"1s presence idle=0.5 keys=0.5"},
		},
		{
			name:   "with no idle source nothing is ever recorded or sealed",
			noIdle: true,
			acts: []act{
				at(s(1), focusOn(event.AppTerminal)),
				at(s(2), power(event.PowerLock)),
			},
			end:     s(61),
			want:    []string{},
			noTimer: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				if start.UnixNano()%int64(time.Minute) != 0 {
					t.Fatalf("the bubble's clock starts at %v, not on a minute boundary", start)
				}
				sch := sched.New()
				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan struct{})
				go func() {
					sch.Run(ctx)
					close(done)
				}()

				var mu sync.Mutex
				got := []string{}
				var samples []time.Duration
				var seals []event.PowerOp
				var problems []string
				p := &person{start: start, inputs: tt.inputs, keys: tt.keys, fail: tt.failIdle}
				e := &env{t: t}
				osys := OS{
					Idle:   p.idle,
					Locked: func() (bool, error) { return tt.locked, tt.lockErr },
					WatchSuspend: func(fn func(event.PowerOp)) (func(), error) {
						e.suspend = fn
						return func() { e.suspendStopped = true }, nil
					},
					WatchLock: func(fn func(event.PowerOp)) (func(), error) {
						if tt.lockWatchErr != nil {
							return nil, tt.lockWatchErr
						}
						e.lock = fn
						return func() { e.lockStopped = true }, nil
					},
				}
				if tt.noIdle {
					osys.Idle = nil
				}
				e.s = New(Options{
					Sched: sch,
					OS:    osys,
					Record: func(r Record) {
						if err := r.Data.Validate(); err != nil {
							t.Errorf("%s doesn't validate: %v", describe(start, r), err)
						}
						mu.Lock()
						defer mu.Unlock()
						got = append(got, describe(start, r))
						if _, ok := r.Data.(*event.OSPresence); ok {
							samples = append(samples, r.At.Sub(start))
						}
					},
					Seal: func(op event.PowerOp) {
						mu.Lock()
						defer mu.Unlock()
						seals = append(seals, op)
					},
					Problem: func(msg string) {
						mu.Lock()
						defer mu.Unlock()
						problems = append(problems, msg)
					},
				})
				for _, do := range tt.before {
					do(e)
				}
				e.s.Start()
				for _, a := range tt.acts {
					time.Sleep(time.Until(start.Add(a.at)))
					a.do(e)
				}
				time.Sleep(time.Until(start.Add(tt.end)))
				synctest.Wait()
				timer := slices.ContainsFunc(sch.Jobs(), func(j sched.JobInfo) bool { return j.Name == timerName })
				e.s.Stop()
				cancel()
				<-done

				mu.Lock()
				defer mu.Unlock()
				if tt.want != nil {
					if !slices.Equal(got, tt.want) {
						t.Errorf("records:\n  %s\nwant:\n  %s", join(got), join(tt.want))
					}
				} else {
					if len(got) != len(samples) {
						t.Errorf("records other than samples:\n  %s", join(got))
					}
					if !slices.Equal(samples, tt.wantSamples) {
						t.Errorf("samples at %v\nwant %v", samples, tt.wantSamples)
					}
				}
				if !slices.Equal(seals, tt.wantSeals) {
					t.Errorf("seals %v, want %v", seals, tt.wantSeals)
				}
				if !slices.Equal(problems, tt.wantProblems) {
					t.Errorf("problems %q, want %q", problems, tt.wantProblems)
				}
				if tt.noTimer && timer {
					t.Errorf("a sample is still scheduled at the end: %+v", sch.Jobs())
				}
				if e.suspend != nil && !e.suspendStopped {
					t.Error("Stop left the suspend watch running")
				}
				if e.lock != nil && !e.lockStopped {
					t.Error("Stop left the lock watch running")
				}
			})
		})
	}
}

func join(lines []string) string {
	if len(lines) == 0 {
		return "(none)"
	}
	out := lines[0]
	for _, l := range lines[1:] {
		out += "\n  " + l
	}
	return out
}

// TestStatus checks what the sampler tells doctor, through a context's
// life.
func TestStatus(t *testing.T) {
	tests := []struct {
		name string
		acts []act
		at   time.Duration
		want Status
	}{
		{
			name: "before any context the sampler is idle",
			at:   s(5),
			want: Status{},
		},
		{
			name: "in a context with recent input it samples every 15 s",
			acts: []act{at(s(1), focusOn(event.AppTerminal))},
			at:   s(31),
			want: Status{Live: true, Every: SampleInterval, Samples: 3},
		},
		{
			name: "a locked session shows as locked",
			acts: []act{at(s(1), focusOn(event.AppTerminal)), at(s(2), power(event.PowerLock))},
			at:   s(3),
			want: Status{Live: true, Every: SampleInterval, Locked: true, Samples: 2},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				sch := sched.New()
				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan struct{})
				go func() {
					sch.Run(ctx)
					close(done)
				}()
				p := &person{start: start, inputs: []time.Duration{s(0.5)}}
				e := &env{t: t, s: New(Options{Sched: sch, OS: OS{Idle: p.idle}})}
				e.s.Start()
				for _, a := range tt.acts {
					time.Sleep(time.Until(start.Add(a.at)))
					a.do(e)
				}
				time.Sleep(time.Until(start.Add(tt.at)))
				synctest.Wait()
				got := e.s.Status()
				e.s.Stop()
				cancel()
				<-done
				if got.Live != tt.want.Live || got.Every != tt.want.Every || got.Locked != tt.want.Locked ||
					got.Samples != tt.want.Samples || len(got.Problems) != 0 {
					t.Errorf("Status() = %+v, want %+v", got, tt.want)
				}
			})
		})
	}
}

// TestBoundaryAfter checks the slot boundaries the timer is armed for.
func TestBoundaryAfter(t *testing.T) {
	epoch := time.Unix(0, 0)
	tests := []struct {
		name  string
		t     time.Time
		every time.Duration
		want  time.Time
	}{
		{"a time on a boundary moves to the next one", epoch.Add(s(30)), SampleInterval, epoch.Add(s(45))},
		{"a time between boundaries moves to the next one", epoch.Add(s(31)), SampleInterval, epoch.Add(s(45))},
		{"minute boundaries fall on the minute", epoch.Add(s(61)), SlowInterval, epoch.Add(s(120))},
		{"a time before 1970 still rounds up", epoch.Add(-s(7)), SampleInterval, epoch},
		{"a time one interval before 1970 rounds up to the next boundary", epoch.Add(-s(15)), SampleInterval, epoch},
		{
			"a local time's boundary is the same instant as in UTC",
			time.Date(2026, 11, 19, 10, 0, 7, 0, time.FixedZone("UTC+5:45", 5*3600+45*60)), SampleInterval,
			time.Date(2026, 11, 19, 4, 15, 15, 0, time.UTC),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := boundaryAfter(tt.t, tt.every); !got.Equal(tt.want) {
				t.Errorf("boundaryAfter(%v, %v) = %v, want %v", tt.t, tt.every, got, tt.want)
			}
		})
	}
}

// TestSeconds checks the conversion of idle times to os.presence's seconds.
func TestSeconds(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want float64
	}{
		{"whole seconds stay whole", 15 * time.Second, 15},
		{"milliseconds are kept", 1500 * time.Millisecond, 1.5},
		{"less than a millisecond is dropped", 1500*time.Millisecond + 999*time.Microsecond, 1.5},
		{"a negative idle time is zero", -time.Second, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := seconds(tt.d); got != tt.want {
				t.Errorf("seconds(%v) = %v, want %v", tt.d, got, tt.want)
			}
		})
	}
}
