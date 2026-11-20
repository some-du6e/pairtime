// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"slices"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
)

func trackOf(d Derived, id string) (TrackCall, bool) {
	i := slices.IndexFunc(d.Tracks, func(c TrackCall) bool { return c.ID == id })
	if i < 0 {
		return TrackCall{}, false
	}
	return d.Tracks[i], true
}

func flagged(d Derived, name, id string) bool {
	return slices.ContainsFunc(d.Flags, func(f Flag) bool { return f.Name == name && f.EventID == id })
}

// TestTrackBesideHooks covers a `pairtime track` call from a session that
// pairtime also sees through its hooks (DESIGN 5.4), as WP-FX4's cases (f)
// and (g) do.
func TestTrackBesideHooks(t *testing.T) {
	cases := []struct {
		name    string
		after   int64 // seconds after the hook prompt
		outcome TrackOutcome
	}{
		{"a call 4 s after the session's hook prompt is track_matched and opens nothing", 4, TrackMatched},
		{"a call 20 s after it is still track_matched, within TRACK_DEDUPE_S, and isn't flagged", 20, TrackMatched},
		{"a call with no prompt of its session nearby opens nothing and raises track_without_prompt", 120, TrackWithoutPrompt},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newTL(false)
			terminalSession(b)
			b.press(every(atMS(30_500), atMS(59_500), 1000)...)
			p := b.prompt(at(60), event.ViaHook, "s_1", "p_1", "t_1", 30)
			b.mouse(every(atMS(61_000), atMS(190_000), 5000)...)
			tr := b.track(at(60+c.after), "s_1", 30, event.LogUnmatched, "")
			b.samples(at(0), at(200))
			got := b.run(t, Device{})
			call, ok := trackOf(got, tr.EventID)
			if !ok || call.Outcome != c.outcome {
				t.Errorf("call %+v, want outcome %s", call, c.outcome)
			}
			if len(got.Anchors) != 1 || got.Anchors[0].ID != p.EventID {
				t.Errorf("anchors %+v, want only the hook prompt's", got.Anchors)
			}
			if want := c.outcome == TrackWithoutPrompt; flagged(got, FlagTrackWithoutPrompt, tr.EventID) != want {
				t.Errorf("flags %+v; track_without_prompt raised should be %v", got.Flags, want)
			}
		})
	}
}

// trackScene is a terminal in focus hosting session s_t, which no hook or
// log reader sees, with pointer input every 5 s until 59 s, then calls.
func trackScene(keys bool) *tl {
	b := newTL(keys)
	b.focus(at(0), event.AppTerminal, "p_1", "s_t")
	b.mouse(every(atMS(1_200), atMS(59_200), 5000)...)
	return b
}

// TestTrackGate covers the fallback anchor of DESIGN 5.4 and 6.2 D3.
func TestTrackGate(t *testing.T) {
	type want struct {
		outcome TrackOutcome
		failed  []int
		level   event.Evidence
		kind    event.AnchorKind
		lead    int64 // seconds
	}
	cases := []struct {
		name  string
		keys  bool
		build func(b *tl) []event.Event
		want  []want
	}{
		{name: "a call that passes the gate opens a level C anchor labeled via_track, classed by its length",
			build: func(b *tl) []event.Event { return []event.Event{b.track(at(60), "s_t", 30, event.LogNoReader, "")} },
			want:  []want{{outcome: TrackAnchor, level: event.EvidenceC, kind: event.AnchorSubstantivePrompt, lead: 140}}},
		{name: "key presses in the host window in the KEYS_PRE_S before the call make it level A",
			keys: true,
			build: func(b *tl) []event.Event {
				b.press(every(atMS(50_500), atMS(59_500), 1000)...)
				return []event.Event{b.track(at(60), "s_t", 30, event.LogNoReader, "")}
			},
			want: []want{{outcome: TrackAnchor, level: event.EvidenceA, kind: event.AnchorSubstantivePrompt, lead: 140}}},
		{name: "a short call is a go-ahead",
			build: func(b *tl) []event.Event { return []event.Event{b.track(at(60), "s_t", 2, event.LogNoReader, "ok")} },
			want:  []want{{outcome: TrackAnchor, level: event.EvidenceC, kind: event.AnchorGoAhead, lead: 20}}},
		{name: "condition 1: a call from a session the window in focus doesn't host is rejected",
			build: func(b *tl) []event.Event {
				b.focus(at(30), event.AppBrowser, "")
				return []event.Event{b.track(at(60), "s_t", 30, event.LogNoReader, "")}
			},
			want: []want{{outcome: TrackRejected, failed: []int{1, 2}}}},
		{name: "condition 1: a call from no harness session is rejected",
			build: func(b *tl) []event.Event {
				return []event.Event{b.track(at(60), event.NoSession, 30, event.LogNoReader, "")}
			},
			want: []want{{outcome: TrackRejected, failed: []int{1}}}},
		{name: "condition 2: no input in the TRACK_INPUT_PRE_S before the call rejects it",
			build: func(b *tl) []event.Event { return []event.Event{b.track(at(100), "s_t", 30, event.LogNoReader, "")} },
			want:  []want{{outcome: TrackRejected, failed: []int{2}}}},
		{name: "condition 3: a second call within TRACK_MIN_GAP_S of the session's last track anchor is rejected",
			build: func(b *tl) []event.Event {
				b.mouse(every(atMS(61_200), atMS(89_200), 5000)...)
				return []event.Event{b.track(at(60), "s_t", 30, event.LogNoReader, ""),
					b.track(at(75), "s_t", 30, event.LogNoReader, "")}
			},
			want: []want{{outcome: TrackAnchor, level: event.EvidenceC, kind: event.AnchorSubstantivePrompt, lead: 140},
				{outcome: TrackRejected, failed: []int{3}}}},
		{name: "condition 3: a call TRACK_MIN_GAP_S after the last track anchor opens another",
			build: func(b *tl) []event.Event {
				b.mouse(every(atMS(61_200), atMS(89_200), 5000)...)
				return []event.Event{b.track(at(60), "s_t", 30, event.LogNoReader, ""),
					b.track(at(80), "s_t", 30, event.LogNoReader, "")}
			},
			want: []want{{outcome: TrackAnchor, level: event.EvidenceC, kind: event.AnchorSubstantivePrompt, lead: 140},
				{outcome: TrackAnchor, level: event.EvidenceC, kind: event.AnchorSubstantivePrompt, lead: 140}}},
		{name: "condition 4: a call from a session a log reader covers goes by the log, and with no prompt there is flagged",
			build: func(b *tl) []event.Event { return []event.Event{b.track(at(60), "s_t", 30, event.LogUnmatched, "")} },
			want:  []want{{outcome: TrackWithoutPrompt}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := trackScene(c.keys)
			calls := c.build(b)
			b.samples(at(0), at(120))
			got := b.run(t, Device{})
			for i, call := range calls {
				w := c.want[i]
				tc, ok := trackOf(got, call.EventID)
				if !ok || tc.Outcome != w.outcome || !slices.Equal(tc.Failed, w.failed) {
					t.Errorf("call %d: %+v, want outcome %s failing %v", i, tc, w.outcome, w.failed)
				}
				a, ok := anchorByID(got, call.EventID)
				if ok != (w.outcome == TrackAnchor) {
					t.Errorf("call %d: anchor %v, want one only for outcome anchor", i, ok)
					continue
				}
				if !ok {
					continue
				}
				if a.Level != w.level || a.Kind != w.kind || a.LeadMS != w.lead*1000 || !a.TypingNeedsKeys ||
					!slices.Equal(a.Labels, []event.Label{event.LabelViaTrack}) || a.Origin != event.OriginHumanTyped {
					t.Errorf("call %d: anchor %+v, want level %s kind %s lead %d s, typing needing keys, via_track", i, a, w.level, w.kind, w.lead)
				}
			}
		})
	}
}

// TestTrackJiggler is WP-FX4's case (a) as a unit test: `pairtime track`
// every 20 s from a session no hook sees, with a jiggler holding input_any
// and focus on the host terminal, on a machine without a key counter.
// Every call opens an anchor, and every anchor is level C (DESIGN 5.2).
func TestTrackJiggler(t *testing.T) {
	b := newTL(false)
	b.focus(at(0), event.AppTerminal, "p_1", "s_t")
	b.mouse(every(atMS(3_700), atMS(299_700), 12_000)...)
	var calls []event.Event
	for s := int64(40); s <= 280; s += 20 {
		calls = append(calls, b.track(at(s), "s_t", 120, event.LogNoReader, ""))
	}
	b.samples(at(0), at(300))
	got := b.run(t, Device{})
	t.Run("every call opens an anchor, all of them level C", func(t *testing.T) {
		for _, c := range calls {
			a, ok := anchorByID(got, c.EventID)
			if !ok || a.Level != event.EvidenceC {
				t.Errorf("call at %d: anchor %v level %s, want a level C anchor", c.T, ok, a.Level)
			}
		}
	})
	t.Run("no anchor of any other level exists", func(t *testing.T) {
		for _, a := range got.Anchors {
			if a.Level != event.EvidenceC {
				t.Errorf("anchor %s is level %s", a.ID, a.Level)
			}
		}
	})
}
