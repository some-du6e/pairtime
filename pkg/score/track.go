// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"cmp"
	"strings"

	"github.com/some-du6e/pairtime/pkg/event"
)

// TrackOutcome is what became of a `pairtime track` call (DESIGN 5.4).
type TrackOutcome string

const (
	// TrackAnchor is a call that passed the fallback gate and opened an
	// anchor (DESIGN 6.2 D3).
	TrackAnchor TrackOutcome = "anchor"
	// TrackMatched is a call from a session pairtime sees through hooks or a
	// log reader, near one of that session's prompts. The prompt is the
	// anchor; the call opens nothing and counts toward the harness's
	// compliance with the AGENTS.md entry.
	TrackMatched TrackOutcome = "track_matched"
	// TrackWithoutPrompt is a call from such a session with no prompt near
	// it. It opens nothing and raises FlagTrackWithoutPrompt.
	TrackWithoutPrompt TrackOutcome = "track_without_prompt"
	// TrackRejected is a call from a session pairtime can't see otherwise
	// that failed the gate. Failed says which conditions.
	TrackRejected TrackOutcome = "rejected"
)

// TrackCall is one `pairtime track` call and what became of it.
type TrackCall struct {
	ID         string        `json:"event_id"`
	T          int64         `json:"t"`
	SessionKey string        `json:"session_key"`
	Harness    event.Harness `json:"harness"`
	Outcome    TrackOutcome  `json:"outcome"`
	// Failed lists the numbers of DESIGN 5.4's gate conditions a rejected
	// call failed: 1, the calling session isn't hosted by the window in
	// focus; 2, that window saw no input in the TRACK_INPUT_PRE_S before
	// the call; 3, the session's last track anchor is under
	// TRACK_MIN_GAP_S old. Condition 4, that no hook or log reader sees the
	// session, is what sends a call to the gate at all. A rejected call
	// with nothing failed passed the gate but resolves to no project.
	Failed []int `json:"failed,omitempty"`
}

func trackCmp(a, b TrackCall) int {
	if c := cmp.Compare(a.T, b.T); c != 0 {
		return c
	}
	return strings.Compare(a.ID, b.ID)
}

// trackCalls decides every `pairtime track` call (DESIGN 5.4, 6.2 D3).
//
// From a session pairtime sees through hooks or a log reader, the
// session's own prompt is the anchor. A call within TRACK_DEDUPE_S of one
// of its hook or log prompts is track_matched, and one with no such prompt
// within TRACK_DEDUPE_S raises track_without_prompt. DESIGN 5.4 gives the
// flag TRACK_LOG_MATCH_S, so a call 10 to 30 s from a prompt would be both
// matched and flagged; this follows the single window that
// docs/errata/2026-11-19-track-match-windows.md proposes to Karim, since an
// agent following the entry calls track alongside its first tool call,
// which can come that late. TRACK_LOG_MATCH_S keeps its job in DESIGN 5.2,
// where a log record takes over from a side-door prompt.
//
// From any other session the call is the fallback anchor, opened only
// when the gate's four conditions hold. It is classed like a prompt from
// its length and, on the device, its text, but has no origin: the four
// conditions take the place of SM 2.2's origin rules, which are for
// prompt.submit. Its typing part needs key presses, and it is level C with
// via_track, or A with key presses in the host window in the KEYS_PRE_S
// before it.
func (d *deriver) trackCalls() {
	lastAnchor := map[string]int64{}
	for i, e := range d.recs {
		p, ok := e.Data.(*event.CLITrack)
		if !ok {
			continue
		}
		c := TrackCall{ID: e.EventID, T: e.T, SessionKey: e.SessionKey, Harness: e.Harness}
		s := d.sessions[e.SessionKey]
		seen := s != nil && (s.native || p.LogMatch != event.LogNoReader)
		switch {
		case seen && s.nativePromptNear(e.T, d.r.trackDedupe):
			c.Outcome = TrackMatched
		case seen:
			c.Outcome = TrackWithoutPrompt
			d.flag(FlagTrackWithoutPrompt, e)
		default:
			c.Outcome, c.Failed = d.trackGate(i, e, p, s, lastAnchor)
		}
		d.tracks = append(d.tracks, c)
	}
}

// trackGate applies the fallback gate to a call from a session no hook or
// log reader sees, and opens its anchor if the call passes.
func (d *deriver) trackGate(i int, e event.Event, p *event.CLITrack, s *session, lastAnchor map[string]int64) (TrackOutcome, []int) {
	var failed []int
	if s == nil {
		// The caller's process chain reaches no harness session.
		return TrackRejected, []int{1}
	}
	host := d.sessionHost(s, i)
	if !d.hostedBy(d.focus.before(i), s, i) {
		failed = append(failed, 1)
	}
	if !d.pres.inputWhere(e.T-d.r.trackInputPre, e.T, false, host) {
		failed = append(failed, 2)
	}
	if last, ok := lastAnchor[s.key]; ok && e.T-last < d.r.trackMinGap {
		failed = append(failed, 3)
	}
	if len(failed) > 0 {
		return TrackRejected, failed
	}
	typed := count(p.TextLen)
	class := event.ClassSubstantive
	if typed < d.r.substantive || (p.Label != "" && LowEffort(p.Label)) {
		class = event.ClassLowEffort
	}
	think, typing := d.leadParts(class, e.Via, typed)
	project := e.ProjectKey
	if project == "" {
		project = s.project
	}
	a := Anchor{ID: e.EventID, T: e.T, ProjectKey: project, SessionKey: s.key, PromptClass: class, LeadMS: think + typing, TypingMS: typing, TypingNeedsKeys: true, TrailFrom: e.T,
		Via: e.Via}
	a.Kind, a.Reason = promptKind(class)
	d.level(&a, e.T-d.r.keysPre, e.T, host)
	if !d.add(a, e.T) {
		return TrackRejected, nil
	}
	lastAnchor[s.key] = e.T
	return TrackAnchor, nil
}
