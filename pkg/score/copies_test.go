// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"slices"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
)

// TestQueuedMessageCopies checks E-cc-queued-turn's rule that pkg/score
// takes a log reader's agent.queue and another path's of one message, of
// one length and within TRACK_LOG_MATCH_S, for one act, paired one to one,
// oldest first, while two messages one code path reported stay two. The
// act goes by its most trusted copy, and is corroborated when one of its
// copies came through a hook (DESIGN 6.4).
func TestQueuedMessageCopies(t *testing.T) {
	queued := func(b *tl, ms int64, v event.Via, n ...int) {
		chars := 40
		if len(n) > 0 {
			chars = n[0]
		}
		b.harness(atMS(ms), v, "s_1", "p_1", &event.AgentQueue{TurnKey: "t_q", LenChars: chars})
	}
	// q is a Q anchor: its time, in ms after the timeline's start, the via
	// of the copy it goes by, and whether it is corroborated.
	type q struct {
		ms           int64
		via          event.Via
		corroborated bool
	}
	cases := []struct {
		name string
		add  func(b *tl)
		want []q
	}{
		{"a hook's and a transcript's copy of one message, 25 ms apart, are one Q, the hook's, corroborated",
			func(b *tl) { queued(b, 58_000, event.ViaHook); queued(b, 58_025, event.ViaLog) }, []q{{58_000, event.ViaHook, true}}},
		{"the transcript's copy coming first, 25 ms before the hook's, changes nothing",
			func(b *tl) { queued(b, 58_000, event.ViaLog); queued(b, 58_025, event.ViaHook) }, []q{{58_025, event.ViaHook, true}}},
		{"two messages a hook reported 3 s apart are two",
			func(b *tl) { queued(b, 55_000, event.ViaHook); queued(b, 58_000, event.ViaHook) },
			[]q{{55_000, event.ViaHook, false}, {58_000, event.ViaHook, false}}},
		{"two messages a hook reported in the same millisecond are two",
			func(b *tl) { queued(b, 58_000, event.ViaHook); queued(b, 58_000, event.ViaHook) },
			[]q{{58_000, event.ViaHook, false}, {58_000, event.ViaHook, false}}},
		{"copies further apart than TRACK_LOG_MATCH_S are two messages",
			func(b *tl) { queued(b, 40_000, event.ViaHook); queued(b, 58_000, event.ViaLog) },
			[]q{{40_000, event.ViaHook, false}, {58_000, event.ViaLog, false}}},
		{"a hook's and a transcript's of other lengths are two messages",
			func(b *tl) { queued(b, 58_000, event.ViaHook); queued(b, 58_025, event.ViaLog, 41) },
			[]q{{58_000, event.ViaHook, false}, {58_025, event.ViaLog, false}}},
		{"a hook's and a plugin's, with no log reader's, are two messages",
			func(b *tl) { queued(b, 58_000, event.ViaHook); queued(b, 58_025, event.ViaPlugin) },
			[]q{{58_000, event.ViaHook, false}, {58_025, event.ViaPlugin, false}}},
		{"a plugin's and a transcript's copy of one message are one Q, the transcript's, not corroborated",
			func(b *tl) { queued(b, 58_000, event.ViaPlugin); queued(b, 58_025, event.ViaLog) }, []q{{58_025, event.ViaLog, false}}},
		{"a pair takes no third copy: two messages 3 s apart, the first spooled, pair oldest first",
			func(b *tl) {
				queued(b, 55_000, event.ViaPlugin)
				queued(b, 55_025, event.ViaLog)
				queued(b, 58_000, event.ViaHook)
				queued(b, 58_025, event.ViaLog)
			},
			[]q{{55_025, event.ViaLog, false}, {58_000, event.ViaHook, true}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newTL(false)
			terminalSession(b)
			b.press(every(atMS(30_500), atMS(59_500), 1000)...)
			tc.add(b)
			b.samples(at(0), at(90))
			got := b.run(t, Device{})
			var qs []q
			for _, a := range got.Anchors {
				if a.Kind == event.AnchorQueuedMessage {
					qs = append(qs, q{a.T - base, a.Via, a.Corroborated})
				}
			}
			if !slices.Equal(qs, tc.want) {
				t.Errorf("Q anchors %+v, want %+v", qs, tc.want)
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
