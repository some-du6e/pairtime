// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
)

// TestQueuedMessageCopies checks E-cc-queued-turn's rule that pkg/score
// takes a hook's and a log reader's agent.queue of one message, within
// TRACK_LOG_MATCH_S, for one corroborated act, while two messages one code
// path reported stay two.
func TestQueuedMessageCopies(t *testing.T) {
	queued := func(b *tl, ms int64, v event.Via) {
		b.harness(atMS(ms), v, "s_1", "p_1", &event.AgentQueue{TurnKey: "t_q", LenChars: 40})
	}
	cases := []struct {
		name         string
		add          func(b *tl)
		want         int
		corroborated bool
	}{
		{"a hook's and a transcript's copy of one message, 25 ms apart, are one Q, the hook's, corroborated",
			func(b *tl) { queued(b, 58_000, event.ViaHook); queued(b, 58_025, event.ViaLog) }, 1, true},
		{"the transcript's copy coming first changes nothing",
			func(b *tl) { queued(b, 58_025, event.ViaLog); queued(b, 58_000, event.ViaHook) }, 1, true},
		{"two messages a hook reported 3 s apart are two",
			func(b *tl) { queued(b, 55_000, event.ViaHook); queued(b, 58_000, event.ViaHook) }, 2, false},
		{"copies further apart than TRACK_LOG_MATCH_S are two messages",
			func(b *tl) { queued(b, 40_000, event.ViaHook); queued(b, 58_000, event.ViaLog) }, 2, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newTL(false)
			terminalSession(b)
			b.press(every(atMS(30_500), atMS(59_500), 1000)...)
			tc.add(b)
			b.samples(at(0), at(90))
			got := b.run(t, Device{})
			var qs []Anchor
			for _, a := range got.Anchors {
				if a.Kind == event.AnchorQueuedMessage {
					qs = append(qs, a)
				}
			}
			if len(qs) != tc.want {
				t.Fatalf("%d Q anchors, want %d", len(qs), tc.want)
			}
			if tc.corroborated && (!qs[0].Corroborated || qs[0].Via != event.ViaHook) {
				t.Errorf("the anchor via %s corroborated %v, want the hook's, corroborated", qs[0].Via, qs[0].Corroborated)
			}
		})
	}
}

// TestPromptLabelsFromEveryCopy checks E-cc-transcript-labels' rule that
// SM 2.2 rules 1 to 3 read every copy of a prompt a hook or a log reader
// made: a transcript can say a scheduler or a subagent sent a prompt its
// hook says nothing of. A side door's copy is its sender's word, which
// changes nothing where the harness's own paths saw the prompt (DESIGN 5.2).
func TestPromptLabelsFromEveryCopy(t *testing.T) {
	cases := []struct {
		name string
		via  event.Via
		said event.DeclaredOrigin
		want event.Origin
	}{
		{"a transcript's copy declared cron makes the hook's prompt scheduled", event.ViaLog, event.DeclaredCron, event.OriginScheduled},
		{"a transcript's copy declared subagent makes it a subagent's", event.ViaLog, event.DeclaredSubagent, event.OriginSubagent},
		{"a transcript's copy declared goal makes it a stop hook's continuation", event.ViaLog, event.DeclaredGoal, event.OriginSelfContinue},
		{"a transcript's copy that declares nothing leaves it the person's", event.ViaLog, event.DeclaredNone, event.OriginHumanTyped},
		{"a plugin's copy declared cron changes nothing", event.ViaPlugin, event.DeclaredCron, event.OriginHumanTyped},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newTL(false)
			terminalSession(b)
			b.press(every(atMS(30_500), atMS(59_500), 1000)...)
			b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_p", 30)
			b.harness(atMS(60_025), tc.via, "s_1", "p_1", &event.PromptSubmit{TurnKey: "t_p", LenChars: 30, TypedLenEst: 30,
				OriginDeclared: tc.said})
			b.samples(at(0), at(90))
			got := b.run(t, Device{})
			tu, ok := turnByKey(got, "t_p")
			if !ok || tu.Origin != tc.want {
				t.Errorf("turn %+v, want origin %s", tu, tc.want)
			}
		})
	}
}
