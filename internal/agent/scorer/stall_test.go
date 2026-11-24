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
		// gapEnd and gap are the latest gap longer than BUCKET_TTL_S
		// between two of the sampler's records, 0 for none.
		gapEnd, gap int64
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
			want: []string{"ends 1 h 30 min before", "no presence sample has come for 1 h 30 min",
				"or for 2 h of records the agent stamps itself (hooks, its own watchers, an agent app's reader) without one, and then any record 5 min later"}},
		{name: "with samples coming and a browser in front for two hours, a lookup's hold under SM 2.5, there is no note",
			fb: at - 2*60*minute, sampled: at - minute, focus: event.AppBrowser},
		{name: "with samples coming and a chat app in front, a lookup's hold under SM 2.5, there is no note",
			fb: at - 40*minute, sampled: at - minute, focus: event.AppChatApp},
		{name: "with samples that stopped and a browser in front, the note still says no sample has come",
			fb: at - 90*minute, sampled: at - 90*minute, focus: event.AppBrowser,
			want: []string{"no presence sample has come for 1 h 30 min"}},
		{name: "with a sample that ended a gap longer than BUCKET_TTL_S under 5 min before the newest record, the note says the minutes wait for a record dated in that gap",
			fb: at - 14*60*minute, sampled: at, gapEnd: at - 2*minute, gap: 13*60*minute + 50*minute, focus: event.AppTerminal,
			want: []string{"a presence sample came after 13 h 50 min without one", "wait 5 min for a record dated in that gap"}},
		{name: "with that gap ended more than 5 min before the newest record, the note doesn't name it",
			fb: at - 2*60*minute, sampled: at, gapEnd: at - 6*minute, gap: 13 * 60 * minute, focus: event.AppTerminal,
			want: []string{"ends 2 h before"}, bare: true},
		{name: "with samples coming and a terminal in front, the note names no cause",
			fb: at - 2*60*minute, sampled: at - minute, focus: event.AppTerminal,
			want: []string{"ends 2 h before"}, bare: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := stallNote(at, c.fb, pending, 2*60*minute, 5*minute, sampling{newest: c.sampled, gapEnd: c.gapEnd, gap: c.gap}, c.focus)
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

// TestHeldGap checks which gap between the sampler's records watch names as
// pkg/score's hold (reach.held): the latest longer than BUCKET_TTL_S, unless
// a record the agent stamps itself, dated more than BUCKET_TTL_S into it,
// ended the reach before it and has taken effect.
func TestHeldGap(t *testing.T) {
	const minute, hour = int64(60_000), int64(3600_000)
	const ttl, late = 2 * hour, 5 * minute
	night := []int64{0, 15_000, 30_000, 14 * hour, 14*hour + 15_000}
	cases := []struct {
		name           string
		times, stamped []int64
		high           int64
		end, length    int64
	}{
		{"a gap longer than BUCKET_TTL_S with nothing stamped in it is held",
			night, nil, 14*hour + 15_000, 14 * hour, 14*hour - 30_000},
		{"a record stamped more than BUCKET_TTL_S into the gap, whose cut has taken effect, leaves nothing held",
			night, []int64{3 * hour}, 14*hour + 15_000, 0, 0},
		{"a record stamped in the gap whose cut still waits, under 5 min before the newest record, keeps the gap held",
			night, []int64{14*hour - minute}, 14*hour + 15_000, 14 * hour, 14*hour - 30_000},
		{"a record stamped within BUCKET_TTL_S of the gap's start ends no reach",
			night, []int64{hour}, 14*hour + 15_000, 14 * hour, 14*hour - 30_000},
		{"no gap longer than BUCKET_TTL_S holds nothing",
			[]int64{0, hour, 2 * hour}, nil, 2 * hour, 0, 0},
		{"of two gaps longer than BUCKET_TTL_S, the latest is held",
			[]int64{0, 3 * hour, 3*hour + 15_000, 6 * hour}, nil, 6 * hour, 6 * hour, 3*hour - 15_000},
		{"times out of order are sorted first",
			[]int64{14 * hour, 0, 30_000}, nil, 14 * hour, 14 * hour, 14*hour - 30_000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			end, length := heldGap(c.times, c.stamped, ttl, late, c.high)
			if end != c.end || length != c.length {
				t.Errorf("gap ending at %d ms, %d ms long; want %d ms, %d ms", end, length, c.end, c.length)
			}
		})
	}
}

// TestStallWarning checks what doctor hears from the loop: over a hooked
// turn of two hours whose agent samples presence once a minute, as after
// CONTEXT_IDLE_S without input (hookTurn), with some of those samples left
// out; over SM 6.1's morning up to its first browser run, a lookup, with
// the browser then left in front for 40 minutes while samples come; and
// over a night's suspend, after which pkg/score holds the minutes for
// PENDING_WINDOW_S - READ_HORIZON_S past the morning's resume.
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
	// overnight is an evening's hooked turn, then an os.power suspend and
	// the sample taken with it, and fourteen hours later the resume and a
	// sample every 15 s for half an hour, as a laptop closed for the night
	// and opened in the morning writes. The resume ends a gap longer than
	// BUCKET_TTL_S, so pkg/score keeps the night's minutes at the sample
	// before it until the newest record is PENDING_WINDOW_S -
	// READ_HORIZON_S past the resume.
	overnight := func(t *testing.T) []event.Event {
		day := hookTurn(t, fixture(t, "6.4").Events[0].T, tools[:12])
		last := day[len(day)-1].T
		n := 0
		rec := func(ts int64, d event.Payload) event.Event {
			n++
			return sampleAt(day, ts, fmt.Sprintf("01a1fb1e-5318-78a1-a7f4-%012x", n), d)
		}
		idle := func(s float64) *event.OSPresence { return &event.OSPresence{IdleAnyS: s, IdleKeysS: &s} }
		evs := append(day, rec(last+minute, &event.OSPower{Op: event.PowerSuspend}), rec(last+minute, idle(660)))
		morning := last + minute + 14*60*minute
		evs = append(evs, rec(morning, &event.OSPower{Op: event.PowerResume}))
		for k := int64(0); k <= 120; k++ {
			evs = append(evs, rec(morning+k*15_000, idle(float64(1+k%4))))
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
		// each, when set, is what every lag doctor hears must say: its
		// cause.
		each string
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
		{name: "a night's suspend, which holds the minutes for 5 min after the morning's resume, tells doctor why while it holds, and nothing once they catch up",
			build: overnight, warned: true, each: "a presence sample came after 14 h without one"},
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
			for _, p := range problems {
				if c.each != "" && strings.Contains(p, "newest final minute") && !strings.Contains(p, c.each) {
					t.Errorf("doctor heard %q, want every lag it hears to say %q", p, c.each)
				}
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
