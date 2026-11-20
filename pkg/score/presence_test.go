// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"slices"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
)

// slotWant is a slot's expected presence; until is seconds after base, 0
// for the slot's end.
type slotWant struct {
	at      int64 // the slot's start, seconds after base
	present event.Presence
	until   int64
	then    event.Presence
	asleep  bool
	locked  bool
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
		if s.Present != w.present || s.PresentUntil != until || s.Then != then || s.Asleep != w.asleep || s.Locked != w.locked {
			t.Errorf("slot at %d s: %q until %d then %q, asleep %v, locked %v; want %q until %d then %q, asleep %v, locked %v",
				w.at, s.Present, s.PresentUntil, s.Then, s.Asleep, s.Locked, w.present, until, then, w.asleep, w.locked)
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
			want: []slotWant{{at: 45, present: event.PresentYes}, {at: 60, present: event.PresentNo, asleep: true},
				{at: 585, present: event.PresentNo, asleep: true}, {at: 600, present: event.PresentYes}},
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
			name: "a window written again when a session starts under it is one window, and its longest piece gives the hosted sessions",
			focus: func(b *tl) {
				b.focus(at(0), event.AppBrowser, "")
				b.focus(at(6), event.AppTerminal, "p_1")
				b.focus(at(10), event.AppTerminal, "p_1", "s_1")
			},
			class: event.AppTerminal, hosted: []string{"s_1"},
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
