// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"cmp"
	"sort"
	"strings"

	"github.com/some-du6e/pairtime/pkg/event"
)

// One timeline across projects (SM 3). Each slot of a device's timeline
// resolves to at most one project (SM 3.2), and only candidates of that
// project can credit its seconds (SM 3.3 rule 1), so every second goes to
// one project at most and per-project totals add up to the device's total
// (SM 3.1). Within the slot SM 2.7's candidates compete by SM 3.3's order
// (credit.go). A browser or chat-app slot resolves to no project, and only
// a lookup, which takes its project from its brackets, credits it (SM 2.5).

// attribution is what SM 3.2 resolves one slot to.
type attribution struct {
	// proj is the project the slot's seconds may go to, or "" when the
	// focus resolves to none.
	proj string
	// resolver indexes the anchor that gave the slot of a window hosting
	// several sessions to one of them, by SM 3.2 rule 1's look-ahead or by
	// ATTRIB_STICKY_S, or is -1. A second credited in the slot takes no
	// better level than it (DESIGN 6.4).
	resolver int32
	// unrecorded is set when the slot comes before the client's first
	// os.focus record for most of it, so its focus can't be seen
	// (E-6.6-codes: X.ambiguous, not X.other_focus).
	unrecorded bool
}

// beat is an editor heartbeat with input in its slot, which SM 3.2 rule 2
// resolves an editor slot with.
type beat struct {
	t    int64
	proj string
}

// indexAttribution builds what attribute looks up: each session's anchors
// in time order, and the editor heartbeats with input in their slots.
func (c *creditor) indexAttribution() {
	d := c.d
	c.bySession = map[string][]int{}
	for i, a := range d.anchors {
		if a.SessionKey != "" {
			c.bySession[a.SessionKey] = append(c.bySession[a.SessionKey], i)
		}
	}
	for _, e := range d.recs {
		p, ok := e.Data.(*event.EditorActivity)
		// An agent's edit can be reported from an editor window that isn't
		// in front, so it says nothing about which one is.
		if !ok || e.ProjectKey == "" || p.EditSource == event.EditSourceAgent || !d.pres.slotInput(e.T, false) {
			continue
		}
		c.beats = append(c.beats, beat{e.T, e.ProjectKey})
	}
	c.attr = make([]attribution, len(c.slots))
	for i := range c.attr {
		c.attr[i].resolver = -1
	}
}

// attribute resolves slot s to the project its seconds may go to (SM 3.2):
//
//  1. A terminal, or 3. an agent desktop app: the project the focus record
//     names, which the client sets when the window resolves to one
//     project; else that of its hosted sessions when they all have one;
//     else, for several, SM 3.2 rule 1's look-ahead and ATTRIB_STICKY_S
//     (hostedProject).
//  2. An editor: the project the focus record names; else the project of
//     the most recent editor heartbeat with input in the same or previous
//     slot.
//  4. A browser or chat app, and 5. anything else: no project.
//
// The credit pass calls it at the slot's first second, before that second
// drains any bucket, because rule 1 reads a bucket when the slot begins.
func (c *creditor) attribute(s *Slot) attribution {
	at := attribution{resolver: -1}
	switch s.FocusClass {
	case event.AppTerminal, event.AppAgentApp:
		at.proj = s.FocusProject
		if at.proj == "" {
			var r int
			at.proj, r = c.hostedProject(s)
			at.resolver = int32(r)
		}
	case event.AppEditor:
		at.proj = s.FocusProject
		if at.proj == "" {
			at.proj = c.heartbeatProject(s)
		}
	case event.AppNone:
		at.unrecorded = c.d.focus.longest(s.Start, s.End()) == noFocus
	}
	return at
}

// hostedProject resolves a terminal or agent app whose focus record names
// no project through the harness sessions it hosts (SM 3.2 rules 1 and 3).
// When every hosted session with a project has the same one, the window is
// that project's. When they have several, the slot goes to the session of
// the first anchor in the window after the slot begins, if the slot lies
// inside that anchor's lead, or if that anchor comes within READ_HORIZON_S
// and its session's reading bucket isn't empty when the slot begins;
// otherwise to the session with the most recent anchor within
// ATTRIB_STICKY_S; otherwise the slot is ambiguous (E-6.3-two-tabs). It
// returns the project and the anchor that decided it, or -1.
//
// An anchor is in the window when its session is one the window hosts. A
// slot takes its project from the anchor, the project of the act.
func (c *creditor) hostedProject(s *Slot) (string, int) {
	proj, several := "", false
	for _, k := range s.Hosted {
		ss := c.d.sessions[k]
		switch {
		case ss == nil || ss.project == "":
		case proj == "":
			proj = ss.project
		case ss.project != proj:
			several = true
		}
	}
	if !several {
		return proj, -1
	}
	if a := c.lookAhead(s); a >= 0 {
		return c.d.anchors[a].ProjectKey, a
	}
	if a := c.sticky(s); a >= 0 {
		return c.d.anchors[a].ProjectKey, a
	}
	return "", -1
}

// lookAhead returns the first anchor of a session s's window hosts after
// the slot begins, if the slot lies inside its lead, or if it comes within
// READ_HORIZON_S and its session's bucket isn't empty when the slot begins,
// and -1 otherwise (SM 3.2 rule 1). After is strict: an anchor at the
// slot's very start is the last one before it, which ATTRIB_STICKY_S reads
// (E-6.3-two-tabs). "Inside the lead" means the slot starts inside it.
// Within READ_HORIZON_S counts as SM 2.4's horizon before an act does: the
// slot that starts READ_HORIZON_S before the anchor is inside.
//
// An anchor counts only for slots it can credit (Anchor.from), so a record
// that comes later than the scorer's reach changes nothing further back. A
// session's next anchor that can't is passed over for the one after it.
func (c *creditor) lookAhead(s *Slot) int {
	first := -1
	for _, k := range s.Hosted {
		idx := c.bySession[k]
		j := sort.Search(len(idx), func(j int) bool { return c.d.anchors[idx[j]].T > s.Start })
		for ; j < len(idx); j++ {
			a := &c.d.anchors[idx[j]]
			// An anchor more than reach after the slot is decided too late
			// for it, as is every later one.
			if a.T-s.Start > c.r.reach {
				break
			}
			if a.from(c.r) <= s.Start {
				if first < 0 || resolverCmp(a, &c.d.anchors[first], false) < 0 {
					first = idx[j]
				}
				break
			}
		}
	}
	if first < 0 {
		return -1
	}
	a := &c.d.anchors[first]
	if a.T-a.LeadMS <= s.Start {
		return first
	}
	if a.T-s.Start <= c.r.readHorizon {
		if b := c.d.buckets[a.SessionKey]; b != nil {
			b.advance(c.r, s.Start)
			if b.total(false) > 0 {
				return first
			}
		}
	}
	return -1
}

// sticky returns the most recent anchor of a session s's window hosts at
// or before the slot's start and less than ATTRIB_STICKY_S before it, or -1
// (SM 3.2 rule 1). Less than: a prompt at a slot's start keeps the window
// for ATTRIB_STICKY_S, eight slots, and not a ninth.
func (c *creditor) sticky(s *Slot) int {
	best := -1
	for _, k := range s.Hosted {
		idx := c.bySession[k]
		j := sort.Search(len(idx), func(j int) bool { return c.d.anchors[idx[j]].T > s.Start }) - 1
		for ; j >= 0; j-- {
			a := &c.d.anchors[idx[j]]
			if s.Start-a.T >= c.r.attribSticky {
				break
			}
			if a.from(c.r) <= s.Start {
				if best < 0 || resolverCmp(a, &c.d.anchors[best], true) < 0 {
					best = idx[j]
				}
				break
			}
		}
	}
	return best
}

// resolverCmp orders two anchors competing to resolve a slot: the earlier
// first for the look-ahead, the later first when latest is set, for
// ATTRIB_STICKY_S; then the lower project key, as SM 3.3 rule 6 settles a
// tie, then the lower session key and event id, so the result is
// deterministic.
func resolverCmp(a, b *Anchor, latest bool) int {
	c := cmp.Compare(a.T, b.T)
	if latest {
		c = -c
	}
	if c != 0 {
		return c
	}
	if c := strings.Compare(a.ProjectKey, b.ProjectKey); c != 0 {
		return c
	}
	if c := strings.Compare(a.SessionKey, b.SessionKey); c != 0 {
		return c
	}
	return strings.Compare(a.ID, b.ID)
}

// heartbeatProject resolves an editor whose focus record names no project
// to the project of the most recent editor heartbeat with input in the same
// or previous slot (SM 3.2 rule 2), or "". Heartbeats need activity, so they
// come from the editor window in front.
func (c *creditor) heartbeatProject(s *Slot) string {
	j := sort.Search(len(c.beats), func(j int) bool { return c.beats[j].t >= s.End() }) - 1
	if j >= 0 && c.beats[j].t >= s.Start-event.SlotMS {
		return c.beats[j].proj
	}
	return ""
}

// steer applies the slot's resolver to a second credited in it: the second
// takes no better level than the anchor that gave the slot to its session
// (DESIGN 6.4, E-6.3-two-tabs), so an uncorroborated track, plugin or bridge
// anchor can't steer reading at level A or B into its session, and it
// carries that anchor's side-door label, which a lookup it brackets then
// carries too.
func (c *creditor) steer(at *attribution, sec *second) {
	if at.resolver < 0 {
		return
	}
	a := &c.d.anchors[at.resolver]
	sec.level = lowerLevel(sec.level, a.Level)
	if sec.label == "" {
		sec.label = sideDoorLabel(a.Via)
	}
}
