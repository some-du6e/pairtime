// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
)

// TestSecondSpans pins the span rule at a mid-second edge: a second belongs
// to a span the rules define, such as an anchor's window or a tail, when the
// second's start is at or after the span's start and before its end. SM 1.3
// applies window edges to the second, and E-6.4-prompt-lengths decided this
// reading of it for every span
// (docs/errata/2026-11-19-sm-6.4-prompt-lengths.md, decided 2026-11-20),
// which SM 1.3 states once P11 lands.
func TestSecondSpans(t *testing.T) {
	cases := []struct {
		name  string
		build func(b *tl)
		want  []secondsWant
		// reason and seconds, when set, pin one reason's seconds in the
		// slots [at, at + 15 s).
		at      int64
		reason  event.Reason
		seconds int64
	}{
		{
			name: "a second belongs to a lead when its start does, so a go-ahead sent at 60.3 s credits GO in seconds 41 to 60 and not in second 40 (SM 1.3)",
			build: func(b *tl) {
				terminalSession(b)
				b.press(every(at(1), at(60), 1000)...)
				// Two typed characters make a go-ahead, whose LOW_LEAD_S
				// lead is [40.3 s, 60.3 s).
				b.prompt(atMS(60_300), event.ViaHook, "s_1", "p_1", "t_1", 2)
				b.mouse(every(at(61), at(86), 5000)...)
				b.samples(at(0), at(90))
			},
			want: []secondsWant{
				{30, 45, map[event.Reason]int64{event.ReasonGoAhead: 4, xNoAnch: 11}},
				{45, 60, map[event.Reason]int64{event.ReasonGoAhead: 15}},
			},
			at: 60, reason: event.ReasonGoAhead, seconds: 1,
		},
		{
			name: "a second belongs to a tail when its start does, so a tail ending at 130.3 s credits second 130 and leaves second 131 X.absent (E-6.4-prompt-lengths)",
			build: func(b *tl) {
				terminalSession(b)
				b.press(every(atMS(30_500), atMS(59_500), 1000)...)
				b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 30)
				// 900 words are 300 s of reading, far more than the tail
				// drains.
				b.agentSays(at(61), "s_1", "t_1", 900)
				b.agentStops(at(62), "s_1", "t_1")
				b.mouse(every(at(65), at(95), 5000)...)
				// The last input before a silence longer than
				// PRESENCE_GAP_S, so the tail is [100.3 s, 130.3 s).
				b.mouse(atMS(100_300))
				b.mouse(every(at(400), at(415), 5000)...)
				b.samples(at(0), at(420))
			},
			want: []secondsWant{
				{105, 120, map[event.Reason]int64{rREAD: 15}},
				{120, 135, map[event.Reason]int64{rREAD: 11, xAbsent: 4}},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(false)
			c.build(b)
			d := b.run(t, Device{})
			checkDerived(t, d)
			for _, w := range c.want {
				if got := sumIn(d, at(w.from), at(w.to)); !sameCounts(got, w.want) {
					t.Errorf("seconds %d to %d: %v, want %v", w.from, w.to, got, w.want)
				}
			}
			if c.reason != "" {
				if got := sumIn(d, at(c.at), at(c.at+15))[c.reason]; got != c.seconds {
					t.Errorf("%d s of %s in the slot at %d s, want %d", got, c.reason, c.at, c.seconds)
				}
			}
		})
	}
}
