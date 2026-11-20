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
