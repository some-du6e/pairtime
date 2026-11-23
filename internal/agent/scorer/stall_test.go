// SPDX-License-Identifier: AGPL-3.0-or-later

package scorer

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/params"
	"github.com/some-du6e/pairtime/pkg/score"
)

func TestStallNote(t *testing.T) {
	const (
		minute  = int64(60_000)
		pending = 15 * minute
		at      = int64(1_795_046_400_000)
	)
	cases := []struct {
		name        string
		fb, sampled int64
		focus       event.AppClass
		// want are the parts the note must hold, none for no note; bare says
		// the note names no cause.
		want []string
		bare bool
	}{
		{name: "FinalBefore PENDING_WINDOW_S behind the newest record is no lag", fb: at - pending, sampled: at},
		{name: "ten minutes past PENDING_WINDOW_S behind it is no lag yet", fb: at - pending - stallMS, sampled: at},
		{name: "with no presence sample at all, the note says presence is unknown on this computer",
			fb: at - 125*minute,
			want: []string{"newest final minute ends 2 h 5 min before its newest record, where 15 min is usual",
				"presence is unknown on this computer"}},
		{name: "with samples that stopped while records came, the note says how long none has come",
			fb: at - 90*minute, sampled: at - 90*minute,
			want: []string{"ends 1 h 30 min before", "no presence sample has come for 1 h 30 min", "or for 2 h of the agent's records without one"}},
		{name: "with samples coming and a browser in front for two hours, a lookup's hold under SM 2.5, there is no note",
			fb: at - 2*60*minute, sampled: at - minute, focus: event.AppBrowser},
		{name: "with samples coming and a chat app in front, a lookup's hold under SM 2.5, there is no note",
			fb: at - 40*minute, sampled: at - minute, focus: event.AppChatApp},
		{name: "with samples that stopped and a browser in front, the note still says no sample has come",
			fb: at - 90*minute, sampled: at - 90*minute, focus: event.AppBrowser,
			want: []string{"no presence sample has come for 1 h 30 min"}},
		{name: "with samples coming and a terminal in front, the note names no cause",
			fb: at - 2*60*minute, sampled: at - minute, focus: event.AppTerminal,
			want: []string{"ends 2 h before"}, bare: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := stallNote(at, c.fb, pending, 2*60*minute, c.sampled, c.focus)
			if len(c.want) == 0 {
				if got != "" {
					t.Errorf("note %q, want none", got)
				}
				return
			}
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("note %q, want it to say %q", got, w)
				}
			}
			if c.bare && !strings.HasSuffix(got, "stay pending") {
				t.Errorf("note %q names a cause, want none", got)
			}
		})
	}
}

// TestStallWarning checks what doctor hears from the loop: over a hooked
// turn of two hours whose agent samples presence once a minute, as after
// CONTEXT_IDLE_S without input (hookTurn), with some of those samples left
// out; and over SM 6.1's morning up to its first browser run, a lookup,
// with the browser then left in front for 40 minutes while samples come.
func TestStallWarning(t *testing.T) {
	const minute = int64(60_000)
	var tools []int64
	for m := int64(5); m <= 120; m += 5 {
		tools = append(tools, m*minute)
	}
	// turn is the hooked turn with the samples keep says the agent writes,
	// by the minute after the prompt they are taken in.
	turn := func(keep func(m int64) bool) func(t *testing.T) []event.Event {
		return func(t *testing.T) []event.Event {
			at := fixture(t, "6.4").Events[0].T
			var evs []event.Event
			for _, e := range hookTurn(t, at, tools) {
				if _, ok := e.Data.(*event.OSPresence); ok && !keep((e.T-at)/minute) {
					continue
				}
				evs = append(evs, e)
			}
			return evs
		}
	}
	// browser is SM 6.1 up to 08:39:00, when its first browser run starts,
	// right after credited reading, so SM 2.5 holds FinalBefore at the run's
	// start until its end bracket is decided. The browser stays in front
	// for 40 minutes, with a sample every 15 s.
	browser := func(t *testing.T) []event.Event {
		f := fixture(t, "6.1")
		start := int64(0)
		for _, e := range f.Events {
			if d, ok := e.Data.(*event.OSFocus); ok && d.AppClass == event.AppBrowser {
				start = e.T
				break
			}
		}
		var evs []event.Event
		var sample event.Event
		for _, e := range f.Events {
			if e.T > start {
				break
			}
			evs = append(evs, e)
			if _, ok := e.Data.(*event.OSPresence); ok {
				sample = e
			}
		}
		if start == 0 || sample.Data == nil {
			t.Fatal("6.1 has no browser run, or no sample before it")
		}
		for n, ts := int64(1), start+15_000; ts <= start+40*minute; n, ts = n+1, ts+15_000 {
			e := sample
			e.EventID = fmt.Sprintf("01a20b9e-0000-7000-8000-%012x", n)
			e.Mono += ts - e.T
			e.T = ts
			evs = append(evs, e)
		}
		return evs
	}
	cases := []struct {
		name  string
		build func(t *testing.T) []event.Event
		// warned says whether doctor hears that minutes lag at some point,
		// and last holds what it hears at the end, "" for nothing.
		warned bool
		last   string
		// held says the newest final minute ends up more than the
		// warning's lag behind the newest record, so a case's silence
		// isn't that of a short lag.
		held bool
	}{
		{name: "a computer whose samples keep coming tells doctor nothing", build: turn(func(int64) bool { return true })},
		{name: "samples that stop while the turn's records keep coming tell doctor that the minutes since wait for the next sample",
			build: turn(func(m int64) bool { return m <= 30 }), warned: true, last: "no presence sample has come for", held: true},
		{name: "once samples come again and the minutes catch up, doctor hears no more of it",
			build: turn(func(m int64) bool { return m <= 30 || m >= 100 }), warned: true},
		{name: "a browser in front for 40 minutes after credit, whose run SM 2.5 holds as a possible lookup, tells doctor nothing",
			build: browser, held: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			evs := c.build(t)
			r := newRig(t, score.Device{})
			r.write(evs, nil)
			r.mu.Lock()
			problems := slices.Clone(r.problems)
			r.mu.Unlock()
			last := r.lastProblem()
			warned := slices.ContainsFunc(problems, func(p string) bool { return strings.Contains(p, "newest final minute") })
			if warned != c.warned {
				t.Errorf("doctor heard of a lag: %v, want %v (it heard %q)", warned, c.warned, problems)
			}
			switch {
			case c.last == "" && last != "":
				t.Errorf("doctor last heard %q, want nothing", last)
			case c.last != "" && !strings.Contains(last, c.last):
				t.Errorf("doctor last heard %q, want it to say %q", last, c.last)
			}
			lag := evs[len(evs)-1].T - r.rescore().FinalBefore
			if held := lag > r.table.Int(params.PENDING_WINDOW_S)*1000+stallMS; held != c.held {
				t.Errorf("the newest final minute ends %d s before the newest record: held %v, want %v", lag/1000, held, c.held)
			}
		})
	}
}
