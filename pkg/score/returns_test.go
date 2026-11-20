// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
)

// silence builds SM 2.1's return example: a person works in a terminal,
// the agent ends its turn with a 160-word answer just after their last
// touch at 100 s, and after a silence of resume-100 seconds they act again.
// app is the window in focus throughout; act adds what they do on
// returning.
func silence(app event.AppClass, resume int64, act func(b *tl)) *tl {
	b := newTL(false)
	b.focus(at(0), app, "p_1", "s_1")
	b.start(at(0), "s_1", "p_1", event.EntrypointCLI, event.ParentTerminalApp, true)
	b.mouse(every(at(1), at(96), 5000)...)
	b.mouse(at(100))
	b.prompt(at(30), event.ViaHook, "s_1", "p_1", "t_1", 30)
	b.harness(at(102), event.ViaLog, "s_1", "p_1", &event.AgentOutput{TurnKey: "t_1", Words: 160, IsFinal: true})
	b.harness(at(102), event.ViaHook, "s_1", "p_1", &event.AgentStop{TurnKey: "t_1", StopReason: event.StopEndTurn})
	b.press(every(atMS(resume*1000+500), atMS(resume*1000+18_500), 1000)...)
	act(b)
	b.samples(at(0), at(resume+60))
	return b
}

// TestActsEndingASilence checks SM 2.3's presence tests in the slot whose
// input ends a silence longer than PRESENCE_GAP_S, where a sample taken
// later in the slot dates the input (presence.markStarts). The tests read
// SM 2.1's presence for the slot, which has input, so an act early in it
// still opens its anchor, and an interrupt that comes first still keeps
// the return rule from crediting the silence.
func TestActsEndingASilence(t *testing.T) {
	// The person watches the agent after its answer at 102 s and touches
	// nothing from 100 s until 330 s.
	watch := func(b *tl) {
		b.focus(at(0), event.AppTerminal, "p_1", "s_1")
		b.start(at(0), "s_1", "p_1", event.EntrypointCLI, event.ParentTerminalApp, true)
		b.mouse(every(at(1), at(96), 5000)...)
		b.mouse(at(100))
		b.prompt(at(30), event.ViaHook, "s_1", "p_1", "t_1", 30)
		b.harness(at(102), event.ViaLog, "s_1", "p_1", &event.AgentOutput{TurnKey: "t_1", Words: 160})
	}
	cases := []struct {
		name string
		act  func(b *tl) event.Event
		kind event.AnchorKind
	}{
		{
			name: "an interrupt at 333 s, before the correction whose hook sample at 340 s dates the slot's input, is INT, and nothing is returned",
			act: func(b *tl) event.Event {
				e := b.harness(at(333), event.ViaLog, "s_1", "p_1", &event.AgentInterrupt{TurnKey: "t_1"})
				b.press(atMS(339_500))
				b.prompt(at(340), event.ViaHook, "s_1", "p_1", "t_2", 20)
				b.sample(at(340))
				return e
			},
			kind: event.AnchorInterrupt,
		},
		{
			name: "a permission answered at 333 s, before the tool's hook sample at 338 s dates the slot's input, is PA",
			act: func(b *tl) event.Event {
				b.mouse(atMS(337_500))
				e := b.harness(at(338), event.ViaHook, "s_1", "p_1", &event.AgentPermission{RequestedAt: at(320),
					AnsweredAt: at(333), Decision: event.DecisionAccept, DecidedBy: event.DecidedByUser})
				b.sample(at(338))
				return e
			},
			kind: event.AnchorPermissionAnswer,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(false)
			watch(b)
			e := c.act(b)
			b.samples(at(0), at(400))
			d := b.run(t, Device{})
			if a, ok := anchorByID(d, e.EventID); !ok || a.Kind != c.kind {
				t.Errorf("anchor %+v (found %v), want %s", a, ok, c.kind)
			}
			if len(d.Returns) != 0 {
				t.Errorf("returns %+v, want none", d.Returns)
			}
			if s, _ := slotAt(d, at(330)); s.PresentUntil == s.End() {
				t.Errorf("slot at 330 s is %+v; the test needs its input dated by the sample inside it", s)
			}
		})
	}
}

func TestReturnRule(t *testing.T) {
	typedPrompt := func(resume int64) func(b *tl) {
		return func(b *tl) { b.prompt(at(resume+19), event.ViaHook, "s_1", "p_1", "t_2", 30) }
	}
	t.Run("SM 2.1: a 6-minute silence after a 160-word answer, then a typed prompt, earns at most 53 + 120 = 173 s", func(t *testing.T) {
		got := silence(event.AppTerminal, 451, typedPrompt(451)).run(t, Device{})
		if len(got.Returns) != 1 {
			t.Fatalf("%d returned runs, want 1", len(got.Returns))
		}
		r := got.Returns[0]
		want := Return{RunStart: at(105), RunEnd: at(450), LastInput: at(100), TailEnd: at(130), ReadUntil: at(183),
			UnreadWords: 160, ProjectKey: "p_1", CreditMS: 173_000, Anchor: r.Anchor}
		if r != want {
			t.Errorf("return %+v, want %+v", r, want)
		}
		checkSlots(t, got, []slotWant{
			{at: 105, present: event.PresentTail},
			{at: 120, present: event.PresentTail, until: 130, then: event.PresentReturned},
			{at: 135, present: event.PresentReturned},
			{at: 435, present: event.PresentReturned},
			{at: 450, present: event.PresentYes},
		})
	})
	t.Run("SM 2.1: the same silence followed by editor navigation earns only the tail", func(t *testing.T) {
		got := silence(event.AppEditor, 451, func(b *tl) {
			b.editor(at(455), event.ViaEditor, "p_1", event.ActNavigate, event.EditSourceUnknown)
			typedPrompt(451)(b)
		}).run(t, Device{})
		if len(got.Returns) != 0 {
			t.Errorf("returned runs %+v, want none", got.Returns)
		}
		checkSlots(t, got, []slotWant{{at: 120, present: event.PresentTail, until: 130, then: event.PresentNo},
			{at: 135, present: event.PresentNo}})
	})
	t.Run("a silence longer than RETURN_GAP_MAX_S is no, whatever follows", func(t *testing.T) {
		got := silence(event.AppTerminal, 721, typedPrompt(721)).run(t, Device{})
		if len(got.Returns) != 0 {
			t.Errorf("returned runs %+v, want none", got.Returns)
		}
	})
	t.Run("a focus change during the silence rules the return out", func(t *testing.T) {
		got := silence(event.AppTerminal, 451, func(b *tl) {
			b.focus(at(300), event.AppBrowser, "")
			b.focus(at(440), event.AppTerminal, "p_1", "s_1")
			typedPrompt(451)(b)
		}).run(t, Device{})
		if len(got.Returns) != 0 {
			t.Errorf("returned runs %+v, want none", got.Returns)
		}
	})
	t.Run("output the person had seen before the silence isn't unread", func(t *testing.T) {
		b := newTL(false)
		b.focus(at(0), event.AppTerminal, "p_1", "s_1")
		b.start(at(0), "s_1", "p_1", event.EntrypointCLI, event.ParentTerminalApp, true)
		b.mouse(every(at(1), at(96), 5000)...)
		b.mouse(at(100))
		b.prompt(at(30), event.ViaHook, "s_1", "p_1", "t_1", 30)
		b.harness(at(80), event.ViaLog, "s_1", "p_1", &event.AgentOutput{TurnKey: "t_1", Words: 160, IsFinal: true})
		b.harness(at(80), event.ViaHook, "s_1", "p_1", &event.AgentStop{TurnKey: "t_1", StopReason: event.StopEndTurn})
		b.press(every(atMS(451_500), atMS(469_500), 1000)...)
		typedPrompt(451)(b)
		b.samples(at(0), at(511))
		got := b.run(t, Device{})
		if len(got.Returns) != 1 || got.Returns[0].UnreadWords != 0 || got.Returns[0].CreditMS != 120_000 {
			t.Errorf("returned runs %+v, want one with no unread words and THINK_LEAD_S of credit", got.Returns)
		}
	})
}
