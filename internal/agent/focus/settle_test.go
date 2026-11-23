// SPDX-License-Identifier: AGPL-3.0-or-later

package focus

import (
	"slices"
	"sync"
	"testing"
	"time"
)

// fakeAfter is a settler's clock that the test moves: each wait it starts
// runs only when fire says so.
type fakeAfter struct {
	mu       sync.Mutex
	fns      []func()
	canceled []bool
}

func (a *fakeAfter) after(_ time.Duration, f func()) func() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	i := len(a.fns)
	a.fns = append(a.fns, f)
	a.canceled = append(a.canceled, false)
	return func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		was := !a.canceled[i]
		a.canceled[i] = true
		return was
	}
}

// fire runs the i-th wait's function as if it had passed, canceled or
// not: a timer can fire just as a later change cancels it.
func (a *fakeAfter) fire(i int) {
	a.mu.Lock()
	f := a.fns[i]
	a.mu.Unlock()
	f()
}

func TestSettler(t *testing.T) {
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	noWindow := func(ms int) Change { return Change{At: at(ms), Unknown: "Windows names no foreground window"} }
	window := func(ms, pid int) Change { return Change{At: at(ms), PID: pid} }
	type step struct {
		change  *Change // passed on, with passing set for a moment that may be passing
		passing bool
		fire    int // otherwise, the wait to run, from 0
		stop    bool
	}
	pass := func(c Change, passing bool) step { return step{change: &c, passing: passing} }
	fire := func(i int) step { return step{fire: i} }
	tests := []struct {
		name  string
		steps []step
		want  []Change
	}{
		{"a window report goes on at once",
			[]step{pass(window(0, 6100), false)}, []Change{window(0, 6100)}},
		{"a moment with no foreground window waits, and the window that comes within the wait replaces it, so a switch is one change",
			[]step{pass(noWindow(0), true), pass(window(20, 9600), false), fire(0)}, []Change{window(20, 9600)}},
		{"a moment with no foreground window that outlasts the wait is focus unknown, dated when the moment began",
			[]step{pass(noWindow(0), true), fire(0), pass(window(900, 9600), false)}, []Change{noWindow(0), window(900, 9600)}},
		{"of two moments in a row only the later waits on",
			[]step{pass(noWindow(0), true), pass(noWindow(10), true), fire(0), fire(1)}, []Change{noWindow(10)}},
		{"a change that isn't a moment, such as another desktop taking the input, goes on at once",
			[]step{pass(Change{At: at(0), Unknown: "no window of this desktop is in front"}, false)},
			[]Change{{At: at(0), Unknown: "no window of this desktop is in front"}}},
		{"stop drops a moment still waiting, and nothing passes on after it",
			[]step{pass(noWindow(0), true), {stop: true}, fire(0), pass(window(20, 6100), false)}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []Change
			clock := &fakeAfter{}
			s := &settler{fn: func(c Change) { got = append(got, c) }, wait: 50 * time.Millisecond, after: clock.after}
			for _, st := range tt.steps {
				switch {
				case st.stop:
					s.stop()
				case st.change != nil:
					s.pass(*st.change, st.passing)
				default:
					clock.fire(st.fire)
				}
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("changes %+v, want %+v", got, tt.want)
			}
		})
	}
	t.Run("with the real clock a moment that outlasts the wait goes on by itself", func(t *testing.T) {
		done := make(chan Change, 1)
		s := &settler{fn: func(c Change) { done <- c }, wait: time.Millisecond}
		defer s.stop()
		s.pass(noWindow(0), true)
		select {
		case c := <-done:
			if c != noWindow(0) {
				t.Errorf("change %+v, want %+v", c, noWindow(0))
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the held moment never went on")
		}
	})
}
