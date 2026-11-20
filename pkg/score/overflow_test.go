// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"math"
	"strconv"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
)

// huge returns 2^shift as an int. It is computed at run time, so the file
// builds on 32-bit targets, where TestHugeCounts skips.
func huge(shift uint) int { return int(int64(1) << shift) }

// checkDerived checks that everything d holds is a valid record: the
// anchors, turns and slots convert to valid pkg/event records, every credit
// slot accounts for its 15 seconds with valid entries, and no fill is
// negative.
func checkDerived(t *testing.T, d Derived) {
	t.Helper()
	for _, a := range d.Anchors {
		if err := a.Event().Validate(); err != nil {
			t.Errorf("anchor %s: %v", a.ID, err)
		}
	}
	for _, tu := range d.Turns {
		if err := tu.Event().Validate(); err != nil {
			t.Errorf("turn %s: %v", tu.TurnKey, err)
		}
	}
	for _, s := range d.Slots {
		if err := s.Event().Validate(); err != nil {
			t.Errorf("slot %d: %v", s.Start, err)
		}
	}
	for _, c := range d.Credits {
		var n int64
		for _, e := range c.Credited {
			n += e.Seconds
			if err := e.Event(c.Slot).Validate(); err != nil {
				t.Errorf("slot %d, %s: %v", c.Slot, e.Reason, err)
			}
		}
		for _, s := range c.Uncredited {
			n += s
		}
		if n != event.SlotMS/1000 {
			t.Errorf("slot %d accounts for %d seconds", c.Slot, n)
		}
	}
	for _, f := range d.Fills {
		if f.FillMS < 0 || f.OutputMS < 0 {
			t.Errorf("fill %+v is negative", f)
		}
	}
}

// creditedSeconds sums d's credited seconds under the given reasons.
func creditedSeconds(d Derived, reasons ...event.Reason) int64 {
	var n int64
	for _, c := range d.Credits {
		for _, e := range c.Credited {
			for _, r := range reasons {
				if e.Reason == r {
					n += e.Seconds
				}
			}
		}
	}
	return n
}

// TestHugeCounts feeds the scorer counts that event.Validate accepts but no
// harness writes: 2^55 words, 2^56 code lines, 2^61 unread words and
// lengths of 2^62. Unclamped, each one overflows the bucket's units or a
// lead's typing time, and two send roundDiv into a recursion that ends the
// process. Clamped, each is far past the cap it meets.
func TestHugeCounts(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("counts of 2^55 and more need a 64-bit int")
	}
	r := newRules(testTable(t))
	// plugin adds a record of session s_9, which no hook or log reader sees.
	plugin := func(b *tl, tm int64, p event.Payload) event.Event {
		return b.emit(tm, event.Envelope{Source: event.SourceHook, Via: event.ViaPlugin, Harness: event.HarnessOther,
			SessionKey: "s_9", ProjectKey: "p_1"}, p)
	}
	// reading is a hooked turn at 60 s whose agent shows out at 61 s, with
	// the person present in the terminal until 900 s.
	reading := func(out func(b *tl)) func(b *tl) {
		return func(b *tl) {
			terminalSession(b)
			b.mouse(every(at(1), at(896), 5000)...)
			b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 30)
			out(b)
			b.agentStops(at(62), "s_1", "t_1")
			b.samples(at(0), at(900))
		}
	}
	// typingPart checks that the anchor of the record id has a typing part
	// of at most COMPOSE_MAX_S.
	typingPart := func(id *string) func(t *testing.T, d Derived) {
		return func(t *testing.T, d Derived) {
			a, ok := anchorByID(d, *id)
			if !ok {
				t.Fatal("no anchor")
			}
			if a.TypingMS <= 0 || a.TypingMS > r.composeMax || a.LeadMS > r.thinkLead+r.composeMax {
				t.Errorf("typing part %d ms of a %d ms lead, want at most COMPOSE_MAX_S of typing", a.TypingMS, a.LeadMS)
			}
		}
	}
	// bucketCapped checks that the reading the bucket credits stays within
	// BUCKET_MAX_S and the base drained before the output came.
	bucketCapped := func(t *testing.T, d Derived) {
		if n := creditedSeconds(d, rSUP, rREAD); n < 600 || n > 601 {
			t.Errorf("%d s of SUP and READ, want BUCKET_MAX_S and the second of base before the output", n)
		}
	}
	var id string
	cases := []struct {
		name  string
		build func(b *tl)
		check func(t *testing.T, d Derived)
	}{
		{
			name: "2^55 + 1 words and 2,000 code lines from a plugin fill a turn no hook backs only to R_TURN_BASE_S",
			build: func(b *tl) {
				b.focus(at(0), event.AppTerminal, "p_1", "s_9")
				b.mouse(every(at(1), at(116), 5000)...)
				plugin(b, at(60), &event.PromptSubmit{TurnKey: "t_9", LenChars: 30, TypedLenEst: 30, OriginDeclared: event.DeclaredNone})
				plugin(b, at(70), &event.AgentOutput{TurnKey: "t_9", Words: huge(55) + 1, CodeLines: 2000})
				plugin(b, at(70), &event.AgentStop{TurnKey: "t_9", StopReason: event.StopEndTurn})
				b.samples(at(0), at(120))
			},
			check: func(t *testing.T, d Derived) {
				if len(d.Fills) != 1 || d.Fills[0].FillMS != r.turnBase {
					t.Errorf("fills %+v, want one of R_TURN_BASE_S", d.Fills)
				}
			},
		},
		{
			name:  "2^55 words from a log reader fill the bucket to BUCKET_MAX_S",
			build: reading(func(b *tl) { b.agentSays(at(61), "s_1", "t_1", huge(55)) }),
			check: bucketCapped,
		},
		{
			name: "2^56 code lines fill the bucket to BUCKET_MAX_S",
			build: reading(func(b *tl) {
				b.harness(at(61), event.ViaLog, "s_1", "p_1", &event.AgentOutput{TurnKey: "t_1", CodeLines: huge(56)})
			}),
			check: bucketCapped,
		},
		{
			name: "an edit of 2^62 lines added and 2^62 removed fills the bucket to BUCKET_MAX_S",
			build: reading(func(b *tl) {
				b.harness(at(61), event.ViaHook, "s_1", "p_1", &event.AgentTool{ToolClass: event.ToolEdit,
					LinesAdded: huge(62), LinesRemoved: huge(62), TStart: atMS(60_500), TEnd: at(61)})
			}),
			check: func(t *testing.T, d Derived) {
				bucketCapped(t, d)
				if tu, _ := turnByKey(d, "t_1"); tu.DiffLines != countMax {
					t.Errorf("diff_lines %d, want countMax", tu.DiffLines)
				}
			},
		},
		{
			name: "a prompt of 2^62 typed characters is cut to what its input could type, with a typing part of at most COMPOSE_MAX_S",
			build: func(b *tl) {
				terminalSession(b)
				b.mouse(every(at(1), at(116), 5000)...)
				e := b.harness(at(60), event.ViaHook, "s_1", "p_1", &event.PromptSubmit{TurnKey: "t_1", LenChars: huge(62),
					TypedLenEst: huge(62), OriginDeclared: event.DeclaredNone})
				id = e.EventID
				b.samples(at(0), at(120))
			},
			check: typingPart(&id),
		},
		{
			name: "a queued message of 2^62 characters has a typing part of at most COMPOSE_MAX_S",
			build: func(b *tl) {
				terminalSession(b)
				b.mouse(every(at(1), at(116), 5000)...)
				id = b.harness(at(58), event.ViaHook, "s_1", "p_1", &event.AgentQueue{TurnKey: "t_q", LenChars: huge(62)}).EventID
				b.samples(at(0), at(120))
			},
			check: typingPart(&id),
		},
		{
			name: "a track call of 2^62 characters has a typing part of at most COMPOSE_MAX_S",
			build: func(b *tl) {
				*b = *trackScene(false)
				id = b.track(at(60), "s_t", huge(62), event.LogNoReader, "").EventID
				b.samples(at(0), at(120))
			},
			check: typingPart(&id),
		},
		{
			name: "2^61 and 2^62 unread words make the reading part of a returned run reach the run's end",
			build: func(b *tl) {
				*b = *silence(event.AppTerminal, 451, func(b *tl) {
					b.prompt(at(470), event.ViaHook, "s_1", "p_1", "t_2", 30)
				})
				b.agentSays(at(103), "s_1", "t_1", huge(61))
				b.agentSays(at(104), "s_1", "t_1", huge(62))
			},
			check: func(t *testing.T, d Derived) {
				if len(d.Returns) != 1 {
					t.Fatalf("returns %+v, want one", d.Returns)
				}
				rt := d.Returns[0]
				if rt.ReadUntil != rt.RunEnd || rt.UnreadWords != countMax || rt.CreditMS != r.readingMS(countMax)+r.thinkLead {
					t.Errorf("return %+v, want countMax unread words read until the run's end", rt)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(false)
			c.build(b)
			d := b.run(t, Device{})
			checkDerived(t, d)
			c.check(t, d)
		})
	}
}

func TestRoundDiv(t *testing.T) {
	cases := []struct {
		name    string
		a, b    int64
		want    int64
		comment string
	}{
		{"a half rounds away from zero, upward", 3, 2, 2, "1.5"},
		{"a half rounds away from zero, downward", -3, 2, -2, "-1.5"},
		{"less than a half rounds toward zero", 4, 3, 1, "1.33"},
		{"less than a half rounds toward zero below zero", -4, 3, -1, "-1.33"},
		{"more than a half rounds away from zero", 5, 3, 2, "1.67"},
		{"more than a half rounds away from zero below zero", -5, 3, -2, "-1.67"},
		{"an exact quotient stays", 6, 3, 2, "2"},
		{"zero is zero", 0, 7, 0, "0"},
		{"math.MinInt64 over 1 returns at once", math.MinInt64, 1, math.MinInt64, "no negation"},
		{"math.MinInt64 over 3 rounds away from zero", math.MinInt64, 3, -3074457345618258603, "-3074457345618258602.67"},
		{"math.MinInt64 over math.MaxInt64 is -1", math.MinInt64, math.MaxInt64, -1, "-1.0000000000000000001"},
		{"math.MaxInt64 over 2 rounds up without overflowing", math.MaxInt64, 2, 1 << 62, "4611686018427387903.5"},
		{"math.MaxInt64 over itself is 1", math.MaxInt64, math.MaxInt64, 1, "1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := roundDiv(c.a, c.b); got != c.want {
				t.Errorf("roundDiv(%d, %d) = %d, want %d (%s)", c.a, c.b, got, c.want, c.comment)
			}
		})
	}
}

func TestClampedRules(t *testing.T) {
	r := newRules(testTable(t))
	cases := []struct {
		name      string
		got, want int64
	}{
		{"2^62 typed characters type for COMPOSE_MAX_S", r.typingMS(1 << 62), r.composeMax},
		{"math.MaxInt64 typed characters type for COMPOSE_MAX_S", r.typingMS(math.MaxInt64), r.composeMax},
		{"2^61 words take as long to read as countMax words", r.readingMS(1 << 61), r.readingMS(countMax)},
		{"countMax words take 60 * countMax / READ_WPM seconds", r.readingMS(countMax), roundDiv(60*countMax, r.readWPM) * 1000},
		{"2^55 words fill what countMax words fill", r.proseUnits(1 << 55), r.proseUnits(countMax)},
		{"math.MaxInt64 units convert to the milliseconds of unitsMax", r.unitsMS(math.MaxInt64), r.unitsMS(unitsMax)},
		{"a count above countMax is clamped to it", count(math.MaxInt), countMax},
		{"a negative count is clamped to 0", count(-5), 0},
		{"two counts at countMax add to countMax", addCount(countMax, countMax), countMax},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want || c.got < 0 {
				t.Errorf("got %d, want %d", c.got, c.want)
			}
		})
	}
}
