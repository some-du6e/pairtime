// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"slices"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/params"
)

// slotWant is a slot's expected presence; until is seconds after base, 0
// for the slot's end. slept is whether relayed credit reads the slot as
// slept through (Slot.slept).
type slotWant struct {
	at      int64 // the slot's start, seconds after base
	present event.Presence
	until   int64
	then    event.Presence
	asleep  bool
	locked  bool
	slept   bool
}

func checkSlots(t *testing.T, got Derived, want []slotWant) {
	t.Helper()
	for _, w := range want {
		s, ok := slotAt(got, at(w.at))
		if !ok {
			t.Errorf("no slot at %d s", w.at)
			continue
		}
		until := s.End()
		if w.until != 0 {
			until = at(w.until)
		}
		then := w.then
		if then == "" {
			then = w.present
		}
		if s.Present != w.present || s.PresentUntil != until || s.Then != then || s.Asleep != w.asleep || s.Locked != w.locked ||
			s.slept != w.slept {
			t.Errorf("slot at %d s: %q until %d then %q, asleep %v, locked %v, slept %v; want %q until %d then %q, asleep %v, locked %v, slept %v",
				w.at, s.Present, s.PresentUntil, s.Then, s.Asleep, s.Locked, s.slept, w.present, until, then, w.asleep, w.locked,
				w.slept)
		}
	}
}

func TestPresence(t *testing.T) {
	cases := []struct {
		name  string
		build func(b *tl)
		want  []slotWant
	}{
		{
			name: "a silent run of PRESENCE_GAP_S with input on both sides is bridged",
			build: func(b *tl) {
				b.mouse(at(5), at(140))
				b.samples(at(0), at(180))
			},
			want: []slotWant{{at: 0, present: event.PresentYes}, {at: 15, present: event.PresentBridged},
				{at: 120, present: event.PresentBridged}, {at: 135, present: event.PresentYes}},
		},
		{
			name: "a longer silent run keeps PRESENCE_TAIL_S after the last input, to the millisecond, and is no after it",
			build: func(b *tl) {
				b.mouse(at(5), at(155))
				b.samples(at(0), at(180))
			},
			want: []slotWant{{at: 0, present: event.PresentYes}, {at: 15, present: event.PresentTail},
				{at: 30, present: event.PresentTail, until: 35, then: event.PresentNo},
				{at: 45, present: event.PresentNo}, {at: 135, present: event.PresentNo}, {at: 150, present: event.PresentYes}},
		},
		{
			name: "a slot that overlaps a screen lock is no, input or not",
			build: func(b *tl) {
				b.mouse(every(at(1), at(179), 5000)...)
				b.samples(at(0), at(60))
				b.power(at(62), event.PowerLock)
				b.samples(at(61), at(89))
				b.power(at(90), event.PowerUnlock)
				b.samples(at(90), at(180))
			},
			want: []slotWant{{at: 45, present: event.PresentYes}, {at: 60, present: event.PresentNo, locked: true},
				{at: 75, present: event.PresentNo, locked: true}, {at: 90, present: event.PresentYes}},
		},
		{
			name: "a suspend is a hard break from suspend to resume",
			build: func(b *tl) {
				b.mouse(every(at(1), at(59), 5000)...)
				b.mouse(every(at(601), at(700), 5000)...)
				b.samples(at(0), at(60))
				b.power(at(60), event.PowerSuspend)
				b.sample(at(60))
				b.power(at(600), event.PowerResume)
				b.samples(at(600), at(720))
			},
			want: []slotWant{{at: 45, present: event.PresentYes}, {at: 60, present: event.PresentNo, asleep: true, slept: true},
				{at: 585, present: event.PresentNo, asleep: true, slept: true}, {at: 600, present: event.PresentYes}},
		},
		{
			name: "a gap between samples longer than SLEEP_GAP_S is a hard break, and the silence around it isn't bridged",
			build: func(b *tl) {
				b.mouse(at(5), at(100))
				for _, s := range []int64{0, 15, 30, 75, 90, 105, 120} {
					b.sample(at(s))
				}
			},
			want: []slotWant{{at: 15, present: event.PresentTail}, {at: 30, present: event.PresentNo, asleep: true},
				{at: 45, present: event.PresentNo, asleep: true}, {at: 60, present: event.PresentNo, asleep: true},
				{at: 75, present: event.PresentNo}, {at: 90, present: event.PresentYes}},
		},
		{
			name: "a gap of exactly SLEEP_GAP_S is no break",
			build: func(b *tl) {
				b.mouse(at(5), at(50))
				for _, s := range []int64{0, 15, 45, 60, 75} {
					b.sample(at(s))
				}
			},
			want: []slotWant{{at: 15, present: event.PresentBridged}, {at: 30, present: event.PresentBridged},
				{at: 45, present: event.PresentYes}},
		},
		{
			name: "after CONTEXT_IDLE_S without input, samples a minute apart are slow sampling, not sleep (DESIGN 6.2 D14)",
			build: func(b *tl) {
				b.mouse(at(5), at(785))
				b.samples(at(0), at(660))
				for _, s := range []int64{720, 780, 795, 810} {
					b.sample(at(s))
				}
			},
			want: []slotWant{{at: 660, present: event.PresentNo}, {at: 705, present: event.PresentNo},
				{at: 780, present: event.PresentYes}},
		},
		{
			// The sampler reads idle time on every slot boundary but records
			// only the minute's reading and one that shows new input, here
			// at 765 s (D-d14-cadence, whose D14 text P07 lands). pkg/score
			// already reads that gap as slow cadence, one of its readings.
			name: "after CONTEXT_IDLE_S without input, a sample 45 s after a minute sample that shows new input is no sleep, and the slot holding the input is yes (D-d14-cadence)",
			build: func(b *tl) {
				b.mouse(at(5), at(760))
				b.samples(at(0), at(660))
				for _, s := range []int64{720, 765, 780} {
					b.sample(at(s))
				}
			},
			want: []slotWant{{at: 720, present: event.PresentNo}, {at: 735, present: event.PresentNo},
				{at: 750, present: event.PresentYes}},
		},
		{
			// The sample at 15 s shows input at 10 s, idle 5 s, and so does
			// the one at 75 s, at 70 s: under CONTEXT_IDLE_S, so neither gap
			// after them is slow sampling (DESIGN 6.2 D14).
			name: "a slot in a 60 s gap after a sample with idle 5 s is asleep, a hard break, but not slept through for relayed credit; one in a 121 s gap is both",
			build: func(b *tl) {
				b.mouse(at(10), at(70))
				for _, s := range []int64{0, 15, 75, 196, 210, 225, 240} {
					b.sample(at(s))
				}
			},
			want: []slotWant{{at: 0, present: event.PresentYes},
				{at: 15, present: event.PresentNo, asleep: true}, {at: 60, present: event.PresentNo, asleep: true},
				{at: 75, present: event.PresentNo, asleep: true, slept: true},
				{at: 195, present: event.PresentNo, asleep: true, slept: true}, {at: 210, present: event.PresentNo}},
		},
		{
			name: "slots before the first sample are unknown",
			build: func(b *tl) {
				b.harness(at(0), event.ViaLog, "s_1", "p_1", &event.AgentOutput{TurnKey: "t_1", Words: 10})
				b.mouse(at(61))
				b.samples(at(60), at(90))
			},
			want: []slotWant{{at: 0, present: event.PresentUnknown}, {at: 45, present: event.PresentUnknown},
				{at: 60, present: event.PresentYes}},
		},
		{
			name: "a tail ends at a hard break",
			build: func(b *tl) {
				b.mouse(at(10))
				b.samples(at(0), at(15))
				b.power(at(20), event.PowerLock)
				b.samples(at(20), at(60))
			},
			want: []slotWant{{at: 0, present: event.PresentYes}, {at: 15, present: event.PresentNo, locked: true}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(false)
			c.build(b)
			checkSlots(t, b.run(t, Device{}), c.want)
		})
	}
}

// TestSleptGap checks that relayed credit's bound for a slot slept
// through is SM 2.1's twice SLOW_SAMPLE_S whatever D14 makes of the gap,
// and that a slot slept through is asleep too (D-relayed-sleep). Its table
// has SLEEP_GAP_S 45 s, three times SAMPLE_S, so that D14 excuses a gap of
// up to 180 s after CONTEXT_IDLE_S without input (rules.slowGap). Each
// case samples every 15 s to 660 s, whose sample shows 655 s without
// input, then from gap seconds later on, with input 5 s after that.
func TestSleptGap(t *testing.T) {
	tab := tableWithInts(t, map[params.IntParam]int64{params.SLEEP_GAP_S: 45})
	cases := []struct {
		name string
		gap  int64 // seconds from the sample at 660 s to the next
		want []slotWant
	}{
		{"a 150 s gap after 600 s without input, which D14 excuses under this table, is slept through for relayed credit, and asleep",
			150, []slotWant{{at: 645, present: event.PresentNo},
				{at: 660, present: event.PresentNo, asleep: true, slept: true},
				{at: 795, present: event.PresentNo, asleep: true, slept: true}, {at: 810, present: event.PresentYes}}},
		{"a 120 s gap after 600 s without input is neither, since D14 excuses it and it is no longer than twice SLOW_SAMPLE_S",
			120, []slotWant{{at: 660, present: event.PresentNo}, {at: 765, present: event.PresentNo},
				{at: 780, present: event.PresentYes}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			end := 660 + c.gap
			b := newTL(false)
			b.mouse(at(5), at(end+5))
			b.samples(at(0), at(660))
			b.samples(at(end), at(end+30))
			_, out := Step(NewState(tab, Device{}), b.records())
			var got Derived
			got.Append(out.Final)
			got.Append(out.Pending)
			checkSlots(t, got, c.want)
		})
	}
}

func TestFocusLongest(t *testing.T) {
	cases := []struct {
		name   string
		focus  func(b *tl)
		class  event.AppClass
		hosted []string
	}{
		{
			name: "SM 2.5: a window whose focus another window splits still holds the slot longest in total: 5 + 4 s against 6 s",
			focus: func(b *tl) {
				b.focus(at(0), event.AppTerminal, "p_1", "s_1")
				b.focus(at(5), event.AppBrowser, "")
				b.focus(at(11), event.AppTerminal, "p_1", "s_1")
			},
			class: event.AppTerminal, hosted: []string{"s_1"},
		},
		{
			name: "a window written again when a session starts under it is one window, and the hosted set it held longest gives the slot's sessions",
			focus: func(b *tl) {
				b.focus(at(0), event.AppBrowser, "")
				b.focus(at(6), event.AppTerminal, "p_1")
				b.focus(at(10), event.AppTerminal, "p_1", "s_1")
			},
			class: event.AppTerminal, hosted: []string{"s_1"},
		},
		{
			name: "E-6.6-codes: of a window's hosted sets the one it held longest in total gives the slot's sessions, 4 + 5 s against a single 6 s",
			focus: func(b *tl) {
				b.focus(at(0), event.AppTerminal, "", "s_1", "s_2")
				b.focus(at(4), event.AppTerminal, "", "s_1", "s_2", "s_3")
				b.focus(at(10), event.AppTerminal, "", "s_2", "s_1")
			},
			class: event.AppTerminal, hosted: []string{"s_1", "s_2"},
		},
		{
			name: "a hosted set is a set: a record that lists a session twice hosts what one listing it once does, 4 + 5 s against 6 s",
			focus: func(b *tl) {
				b.focus(at(0), event.AppTerminal, "", "s_1", "s_2")
				b.focus(at(4), event.AppTerminal, "", "s_1", "s_2", "s_3")
				b.focus(at(10), event.AppTerminal, "", "s_2", "s_1", "s_1")
			},
			class: event.AppTerminal, hosted: []string{"s_1", "s_2"},
		},
		{
			name: "on a tie the window that had focus first holds the slot",
			focus: func(b *tl) {
				b.focus(at(0), event.AppEditor, "p_1")
				b.focus(atMS(7_500), event.AppBrowser, "")
			},
			class: event.AppEditor,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(false)
			c.focus(b)
			b.mouse(at(1))
			b.samples(at(0), at(30))
			s, ok := slotAt(b.run(t, Device{}), at(0))
			if !ok {
				t.Fatal("no slot")
			}
			if s.FocusClass != c.class || !slices.Equal(s.Hosted, c.hosted) {
				t.Errorf("focus %s hosting %q, want %s hosting %q", s.FocusClass, s.Hosted, c.class, c.hosted)
			}
		})
	}
}

func TestInputKeys(t *testing.T) {
	for _, c := range []struct {
		name       string
		keyCounter bool
		press      bool
		want       *bool
	}{
		{"without a key counter input_keys is null", false, true, nil},
		{"with one, a key press sets input_keys", true, true, ptr(true)},
		{"with one, pointer input alone leaves input_keys false", true, false, ptr(false)},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(c.keyCounter)
			if c.press {
				b.press(at(5))
			} else {
				b.mouse(at(5))
			}
			b.samples(at(0), at(30))
			s, ok := slotAt(b.run(t, Device{}), at(0))
			if !ok {
				t.Fatal("no slot")
			}
			if (s.InputKeys == nil) != (c.want == nil) || (s.InputKeys != nil && *s.InputKeys != *c.want) {
				t.Errorf("input_keys %v, want %v", s.InputKeys, c.want)
			}
			if !s.InputAny {
				t.Error("input_any is false")
			}
		})
	}
}

func TestMsOf(t *testing.T) {
	for _, c := range []struct {
		name string
		in   float64
		want int64
	}{
		{"a tenth of a second is 100 ms", 0.1, 100},
		{"a large idle time keeps its tenths", 35248.6, 35_248_600},
		{"sub-millisecond digits round to the nearest millisecond", 2.0006, 2001},
		{"zero is zero", 0, 0},
		{"an idle time too large for int64 milliseconds reads as idleMaxS, a long idle, not as input at the sample", 1e19, idleMaxS * 1000},
		{"one that would overflow the conversion to milliseconds reads as idleMaxS too", 9.3e15, idleMaxS * 1000},
		{"one just under idleMaxS keeps its value", 999_999_999_999, 999_999_999_999_000},
		{"text strconv can't read back, from a value event.Validate would reject, reads as idleMaxS, never as input at the sample",
			-1e19, idleMaxS * 1000},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := msOf(c.in); got != c.want {
				t.Errorf("msOf(%v) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}
