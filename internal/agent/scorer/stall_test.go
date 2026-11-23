// SPDX-License-Identifier: AGPL-3.0-or-later

package scorer

import (
	"slices"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
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
		focusAt     int64
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
			want: []string{"ends 1 h 30 min before", "no presence sample has come for 1 h 30 min"}},
		{name: "with samples coming and a browser in front, the note says its run may be a lookup",
			fb: at - 40*minute, sampled: at - minute, focus: event.AppBrowser, focusAt: at - 45*minute,
			want: []string{"ends 40 min before", "a browser or chat app has been in front for 45 min", "lookup (SM 2.5)"}},
		{name: "with samples coming and a terminal in front, the note names no cause",
			fb: at - 2*60*minute, sampled: at - minute, focus: event.AppTerminal, focusAt: at - 3*60*minute,
			want: []string{"ends 2 h before"}, bare: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := stallNote(at, c.fb, pending, c.sampled, c.focus, c.focusAt)
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

// TestStallWarning checks what doctor hears from the loop over a hooked
// turn of two hours whose agent samples presence once a minute, as after
// CONTEXT_IDLE_S without input (hookTurn), with some of those samples left
// out.
func TestStallWarning(t *testing.T) {
	const minute = int64(60_000)
	var tools []int64
	for m := int64(5); m <= 120; m += 5 {
		tools = append(tools, m*minute)
	}
	cases := []struct {
		name string
		// keep says which samples the agent writes, by the minute after the
		// prompt they are taken in.
		keep func(m int64) bool
		// warned says whether doctor hears that minutes lag at some point,
		// and last holds what it hears at the end, "" for nothing.
		warned bool
		last   string
	}{
		{"a computer whose samples keep coming tells doctor nothing", func(int64) bool { return true }, false, ""},
		{"samples that stop while the turn's records keep coming tell doctor that the minutes since wait for the next sample",
			func(m int64) bool { return m <= 30 }, true, "no presence sample has come for"},
		{"once samples come again and the minutes catch up, doctor hears no more of it",
			func(m int64) bool { return m <= 30 || m >= 100 }, true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			at := fixture(t, "6.4").Events[0].T
			var evs []event.Event
			for _, e := range hookTurn(t, at, tools) {
				if _, ok := e.Data.(*event.OSPresence); ok && !c.keep((e.T-at)/minute) {
					continue
				}
				evs = append(evs, e)
			}
			r := newRig(t, score.Device{})
			r.write(evs, nil)
			r.mu.Lock()
			problems := slices.Clone(r.problems)
			r.mu.Unlock()
			warned := slices.ContainsFunc(problems, func(p string) bool { return strings.Contains(p, "newest final minute") })
			if warned != c.warned {
				t.Errorf("doctor heard of a lag: %v, want %v (it heard %q)", warned, c.warned, problems)
			}
			switch last := r.lastProblem(); {
			case c.last == "" && last != "":
				t.Errorf("doctor last heard %q, want nothing", last)
			case c.last != "" && !strings.Contains(last, c.last):
				t.Errorf("doctor last heard %q, want it to say %q", last, c.last)
			}
		})
	}
}
