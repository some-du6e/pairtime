// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"cmp"
	"slices"
	"strings"

	"github.com/some-du6e/pairtime/pkg/event"
)

// deriver derives everything from one sorted list of records, in passes:
// focus and presence, editor heartbeats, sessions, the pairings of agent
// apps' messages with prompts, turns, anchors, `pairtime track` calls, the
// return rule, which needs the anchors, then the reading buckets and the
// per-second credit, which need all of them.
type deriver struct {
	r    rules
	dev  Device
	recs []event.Event
	// now is the present as Step takes it (State.now). Nothing is derived
	// for a slot after it, but on a device that never samples, for the rest
	// of its minute (buildPresence).
	now      int64
	byID     map[string]int
	focus    *focusLine
	pres     *presence
	sessions map[string]*session
	// relays are the relay.message records in record order, relayOf the one
	// each prompt.submit record paired with, by the prompt's index in recs,
	// and threads each thread's stamps of declared none, sorted (relay.go).
	relays  []*relayRec
	relayOf map[int]*relayRec
	threads map[string][]int64
	turns   []*turnBuild
	anchors []Anchor
	tracks  []TrackCall
	flags   []Flag
	returns []Return
	buckets map[string]*bucket
	fills   []Fill
	credits []Credit
	// beats are the editor heartbeats SM 3.2 rule 2 reads, which resolve
	// editor slots and SC1's typed-edit focus test (attrib.go).
	beats beatLine
	// lookups are the browser and chat-app runs, whose credit waits for
	// their end (Step).
	lookups []span
}

// derive runs every pass over recs, which must be in record order, with now
// as the present and reaches the sampler's records (sampledReaches) as Step
// gives them. Besides what it derives, it returns the spans of the browser
// and chat-app runs, the times before which FinalBefore waits for an agent
// app's message to pair (relayHolds), and the start of the first slot that
// nothing decides yet, never for none (presence.undecided).
func derive(r rules, dev Device, recs []event.Event, now int64, reaches []reach) (Derived, []span, []int64, int64) {
	if len(recs) == 0 {
		return Derived{}, nil, nil, never
	}
	d := &deriver{r: r, dev: dev, recs: recs, now: now}
	d.focus = buildFocus(recs)
	d.pres = buildPresence(r, recs, now, reaches, d.focus)
	d.buildBeats()
	d.buildSessions()
	d.pairRelays()
	d.buildTurns()
	d.promptAnchors()
	d.eventAnchors()
	d.trackCalls()
	slices.SortFunc(d.anchors, anchorCmp)
	d.markReturns()
	d.buildBuckets()
	d.credit()

	out := Derived{Slots: slices.Clone(d.pres.slots), Anchors: d.anchors, Returns: d.returns, Tracks: d.tracks,
		Relays: d.relayRecords(), Credits: d.credits, Fills: d.fills}
	for _, tb := range d.turns {
		out.Turns = append(out.Turns, tb.turn)
	}
	slices.SortFunc(out.Tracks, trackCmp)
	out.Flags = d.flags
	slices.SortFunc(out.Flags, flagCmp)
	return out, d.lookups, d.relayHolds(), d.pres.undecided
}

// Flag is a review flag a record raised (SM 8.7, DESIGN 5.4). Flags change
// review priority, never credit.
type Flag struct {
	Name       string `json:"name"`
	T          int64  `json:"t"`
	EventID    string `json:"event_id"`
	SessionKey string `json:"session_key,omitempty"`
}

// The flags WP-SC1 raises.
const (
	// FlagTrackWithoutPrompt is a `pairtime track` call from a session that
	// pairtime sees through hooks or a log reader, with no prompt from it
	// within TRACK_DEDUPE_S of the call (E-track-windows): something outside
	// the agent's turn ran the CLI (DESIGN 5.4).
	FlagTrackWithoutPrompt = "track_without_prompt"
	// FlagPermissionTooFast is a permission answer faster than
	// PA_MIN_LATENCY_MS, which a script gives, not a person. It opens no
	// anchor (SM 2.3).
	FlagPermissionTooFast = "permission_too_fast"
)

func flagCmp(a, b Flag) int {
	if c := cmp.Compare(a.T, b.T); c != 0 {
		return c
	}
	if c := strings.Compare(a.Name, b.Name); c != 0 {
		return c
	}
	return strings.Compare(a.EventID, b.EventID)
}

func (d *deriver) flag(name string, e event.Event) {
	d.flags = append(d.flags, Flag{Name: name, T: e.T, EventID: e.EventID, SessionKey: e.SessionKey})
}
