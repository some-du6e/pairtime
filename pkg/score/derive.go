// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"cmp"
	"slices"
	"strings"

	"github.com/some-du6e/pairtime/pkg/event"
)

// deriver derives everything WP-SC1 builds from one sorted list of
// records, in passes: focus and presence, sessions and turns, anchors,
// `pairtime track` calls, then the return rule, which needs the anchors.
type deriver struct {
	r        rules
	dev      Device
	recs     []event.Event
	focus    *focusLine
	pres     *presence
	sessions map[string]*session
	turns    []*turnBuild
	anchors  []Anchor
	tracks   []TrackCall
	flags    []Flag
	returns  []Return
}

// derive runs every pass over recs, which must be in record order.
func derive(r rules, dev Device, recs []event.Event) Derived {
	if len(recs) == 0 {
		return Derived{}
	}
	d := &deriver{r: r, dev: dev, recs: recs}
	d.focus = buildFocus(recs)
	d.pres = buildPresence(r, recs, recs[len(recs)-1].T, d.focus)
	d.buildSessions()
	d.buildTurns()
	d.promptAnchors()
	d.eventAnchors()
	d.trackCalls()
	slices.SortFunc(d.anchors, anchorCmp)
	d.markReturns()

	out := Derived{Slots: slices.Clone(d.pres.slots), Anchors: d.anchors, Returns: d.returns, Tracks: d.tracks}
	for _, tb := range d.turns {
		out.Turns = append(out.Turns, tb.turn)
	}
	slices.SortFunc(out.Tracks, trackCmp)
	out.Flags = d.flags
	slices.SortFunc(out.Flags, flagCmp)
	return out
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
	// near the call: something outside the agent's turn ran the CLI
	// (DESIGN 5.4).
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
