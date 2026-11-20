// SPDX-License-Identifier: AGPL-3.0-or-later

package presence

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/sched"
	"github.com/some-du6e/pairtime/internal/platform/darwin"
	"github.com/some-du6e/pairtime/pkg/event"
)

// macRig is a sampler on MacOS's sources, with a fake darwin.Presence and
// the MacPower a darwin.Loop's handler would call. Hooks and power events
// sample at once, so no case needs the scheduler to run.
type macRig struct {
	t     *testing.T
	power *MacPower
	s     *Sampler

	mu     sync.Mutex
	sample event.OSPresence
	err    error
	log    []string // records and seals, in order
}

func newMacRig(t *testing.T, withLoop bool) *macRig {
	r := &macRig{t: t, sample: event.OSPresence{IdleAnyS: 2.5}}
	if withLoop {
		r.power = &MacPower{}
	}
	read := func() (event.OSPresence, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.sample, r.err
	}
	r.s = New(Options{
		Sched: sched.New(),
		OS:    MacOS(read, r.power),
		Record: func(rec Record) {
			if err := rec.Data.Validate(); err != nil {
				t.Errorf("%s doesn't validate: %v", what(rec), err)
			}
			r.note(what(rec))
		},
		Seal: func(op event.PowerOp) { r.note("seal " + string(op)) },
	})
	return r
}

func (r *macRig) note(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.log = append(r.log, s)
}

func (r *macRig) lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.log)
}

// what is describe without the time.
func what(r Record) string {
	return strings.TrimPrefix(describe(r.At, r), "0s ")
}

// live makes a project context live, as a prompt hook does.
func live(r *macRig) { r.s.Hook("s_mac", event.KindPromptSubmit) }

// loop calls the MacPower as the Loop's handler goroutine would.
func loop(op event.PowerOp) func(r *macRig) {
	return func(r *macRig) {
		if r.power == nil {
			r.t.Fatal("no MacPower to deliver to")
		}
		r.power.Power(time.Now().Round(0), op)
		// Everything the sampler does for op happens inside Power, so by
		// now a suspend or a lock has been sealed (DESIGN 3.3).
		if op == event.PowerSuspend || op == event.PowerLock {
			if got := r.lines(); len(got) == 0 || got[len(got)-1] != "seal "+string(op) {
				r.t.Errorf("Power(%s) returned before the seal; the log so far is %q", op, got)
			}
		}
	}
}

func TestMacOS(t *testing.T) {
	keys := 7.25
	tests := []struct {
		name     string
		withLoop bool
		sample   event.OSPresence
		err      error
		before   []func(*macRig) // before Start
		steps    []func(*macRig)
		want     []string
		problems int
		watching []string
	}{
		{
			name:     "a suspend from the Loop is recorded and sealed before the Loop's Power call returns, so the batch is on disk before the Mac sleeps",
			withLoop: true,
			sample:   event.OSPresence{IdleAnyS: 2.5},
			steps:    []func(*macRig){live, loop(event.PowerSuspend), loop(event.PowerResume)},
			want: []string{
				"presence idle=2.5",
				"power suspend", "presence idle=2.5", "seal suspend",
				"power resume", "presence idle=2.5",
			},
			watching: []string{"suspend", "lock"},
		},
		{
			name:     "a lock and an unlock from the Loop become os.power records, and the samples after them show the lock state",
			withLoop: true,
			sample:   event.OSPresence{IdleAnyS: 2.5},
			steps:    []func(*macRig){live, loop(event.PowerLock), loop(event.PowerUnlock)},
			want: []string{
				"presence idle=2.5",
				"power lock", "presence idle=2.5 locked", "seal lock",
				"power unlock", "presence idle=2.5",
			},
			watching: []string{"suspend", "lock"},
		},
		{
			name:     "the key-down idle time darwin.Presence reads goes in idle_keys_s, as macOS gives it",
			withLoop: true,
			sample:   event.OSPresence{IdleAnyS: 2.5, IdleKeysS: &keys},
			steps:    []func(*macRig){live},
			want:     []string{"presence idle=2.5 keys=7.25"},
			watching: []string{"suspend", "lock"},
		},
		{
			name:     "a sample without a key-down idle time, as when CoreGraphics can't answer and IOKit's HIDIdleTime stands in, has a null idle_keys_s",
			withLoop: true,
			sample:   event.OSPresence{IdleAnyS: 41},
			steps:    []func(*macRig){live},
			want:     []string{"presence idle=41"},
			watching: []string{"suspend", "lock"},
		},
		{
			name:     "a screen that is locked when the sampler starts shows in the first sample",
			withLoop: true,
			sample:   event.OSPresence{IdleAnyS: 2.5, Locked: true},
			steps:    []func(*macRig){live},
			want:     []string{"presence idle=2.5 locked"},
			watching: []string{"suspend", "lock"},
		},
		{
			name:     "power events that come before the sampler watches are dropped, and seal nothing",
			withLoop: true,
			sample:   event.OSPresence{IdleAnyS: 2.5},
			before:   []func(*macRig){loop(event.PowerUnlock), loop(event.PowerResume)},
			steps:    []func(*macRig){live},
			want:     []string{"presence idle=2.5"},
			watching: []string{"suspend", "lock"},
		},
		{
			name:     "when macOS gives no idle time the sample is missing, not a guess, and both the idle time and the lock state are reported unread",
			withLoop: true,
			err:      darwin.ErrNoIdle,
			steps:    []func(*macRig){live},
			want:     []string{},
			problems: 2,
			watching: []string{"suspend", "lock"},
		},
		{
			name:   "without a Loop the sampler still samples idle time, and watches no power events",
			sample: event.OSPresence{IdleAnyS: 2.5},
			steps:  []func(*macRig){live},
			want:   []string{"presence idle=2.5"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newMacRig(t, tt.withLoop)
			r.sample, r.err = tt.sample, tt.err
			for _, do := range tt.before {
				do(r)
			}
			r.s.Start()
			defer r.s.Stop()
			for _, do := range tt.steps {
				do(r)
			}
			if got := r.lines(); !slices.Equal(got, tt.want) {
				t.Errorf("records and seals:\n  %s\nwant:\n  %s", join(got), join(tt.want))
			}
			st := r.s.Status()
			if len(st.Problems) != tt.problems {
				t.Errorf("problems %q, want %d", st.Problems, tt.problems)
			}
			if !slices.Equal(st.Watching, tt.watching) {
				t.Errorf("watching %q, want %q", st.Watching, tt.watching)
			}
			r.s.Stop()
			if st := r.s.Status(); len(st.Watching) != 0 {
				t.Errorf("after Stop, watching %q, want none", st.Watching)
			}
		})
	}
}

func TestMacPowerAfterStop(t *testing.T) {
	tests := []struct {
		name string
		op   event.PowerOp
	}{
		{name: "a suspend after the sampler stops is dropped, and seals nothing", op: event.PowerSuspend},
		{name: "a lock after the sampler stops is dropped, and seals nothing", op: event.PowerLock},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newMacRig(t, true)
			r.s.Start()
			live(r)
			r.s.Stop()
			before := len(r.lines())
			r.power.Power(time.Now().Round(0), tt.op)
			if got := r.lines()[before:]; len(got) != 0 {
				t.Errorf("after Stop, Power(%s) gave %q", tt.op, got)
			}
		})
	}
}

func TestFromSeconds(t *testing.T) {
	tests := []struct {
		name string
		s    float64
		want time.Duration
	}{
		{name: "seconds to the millisecond become that duration", s: 2.345, want: 2345 * time.Millisecond},
		{name: "zero is zero", s: 0, want: 0},
		{name: "a negative time, which darwin.Presence never gives, is zero", s: -1, want: 0},
		{name: "a time too long for a duration is the longest one", s: 1e13, want: time.Duration(1<<63 - 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fromSeconds(tt.s); got != tt.want {
				t.Errorf("fromSeconds(%v) = %v, want %v", tt.s, got, tt.want)
			}
		})
	}
}
