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
// A slot SM 3.2 rule 1 leaves ambiguous takes the project of the anchor
// window that credits its first second (D-ambiguous-anchors).

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
	// sideDoor is the via of the heartbeat that resolved an editor slot
	// under SM 3.2 rule 2 when no verified heartbeat could, or "". Every
	// second credited in the slot is then level C and carries that via's
	// label, if it has one (D-rule2-sidedoor).
	sideDoor event.Via
	// ambiguous is set for a slot of a window hosting sessions of several
	// projects that neither the look-ahead nor ATTRIB_STICKY_S resolves, and
	// for a terminal that resolves to no project at all, as one hosting no
	// harness does: SM 3.2 rule 1 calls both ambiguous. proj is "" and the
	// bucket doesn't drain, but anchors still count: every anchor window
	// covering a present second competes, whatever its project and whether
	// or not its act has a session, until one credits a second
	// (D-ambiguous-anchors, which the orchestrator extended to the plain
	// terminal on 2026-11-20).
	ambiguous bool
	// won is the project of the first second the credit pass credits in the
	// slot, or "". For a slot that resolves to a project it is that project,
	// unless relayed credit took the slot where nothing else credited a
	// second (relayedRest). For an ambiguous slot it is the project of the
	// window that credits that second. Either way it is the slot's p (SM
	// 5.1), and only that project's candidates credit the rest of the slot.
	won string
	// unrecorded is set when the slot comes before the client's first
	// os.focus record for most of it, so its focus can't be seen
	// (E-6.6-codes: X.ambiguous, not X.other_focus).
	unrecorded bool
	// unknown is set for a slot whose focus the client couldn't tell, an
	// os.focus with app_class unknown (SM 2.5). proj is "", every
	// project's windows are realized where the person is present and every
	// project's bucket with a qualifying anchor drains, until one of them
	// credits a second, and from then on only that project's do
	// (D-split-slots). Every second credited there is level C with
	// focus_unknown (steer).
	unknown bool
}

// admits reports whether an anchor window of project p can credit a present
// second of the slot (SM 2.7, 3.3 rule 1): p is the slot's project, or the
// slot is ambiguous or its focus unknown and nothing of another project has
// credited a second of it yet (D-ambiguous-anchors, D-split-slots).
func (at *attribution) admits(p string) bool {
	switch {
	case at.proj != "":
		return p == at.proj
	case at.ambiguous, at.unknown:
		return at.won == "" || p == at.won
	}
	return false
}

// relayNow reports whether the relayed candidates of project p are weighed
// at each second of the slot, right after this device's own (relayedSecond),
// rather than once the slot's other seconds are decided (relayedRest).
// Relayed candidates skip SM 3.3 rule 1's focus test, but a slot holds one
// project (SM 5.1), and only the candidates of the project a slot resolves
// to share its buckets with this device's reading, a second at a time. In a
// slot that resolves to none, and isn't ambiguous, nothing of this
// device's credits a second before the lookups, so relayed candidates of
// every project are weighed at once. Those of any other project, and every
// one in an ambiguous slot or a slot whose focus is unknown, where this
// device's anchors of any project may credit a later second, wait for the
// slot's end, and credit it only if nothing else did, or only in the
// project of what did (SM 2.7).
func (at *attribution) relayNow(p string) bool {
	switch {
	case at.proj != "":
		return p == at.proj
	case at.ambiguous, at.unknown:
		return false
	}
	return true
}

// relayAdmits reports whether a relayed candidate of project p may credit
// a second of the slot in the pass now names (relayNow): the slot holds one
// project, so once a second of it is credited only that project's may.
func (at *attribution) relayAdmits(p string, now bool) bool {
	return at.relayNow(p) == now && (at.won == "" || at.won == p)
}

// holds reports whether a returned run's think part can credit a second of
// the slot to project p and the slot still hold one project (SM 5.1): the
// slot resolves to p, or its focus is unknown and nothing of another
// project has credited a second of it (SM 2.5, D-split-slots). A run is
// returned only in a window SM 3.2 resolves to its project, or one whose
// focus is unknown throughout (markReturns), so a slot of it that resolves
// to none, or to another project by a later heartbeat, takes no returned
// second, and no lookup shares a returned slot with another project.
func (at *attribution) holds(p string) bool {
	if at.unknown {
		return at.won == "" || at.won == p
	}
	return at.proj != "" && at.proj == p
}

// beat is an editor heartbeat with input in its slot, which SM 3.2 rule 2
// resolves an editor slot with.
type beat struct {
	t    int64
	pos  int // index in the records
	proj string
	via  event.Via
}

// beatLine holds the editor heartbeats with input in their slots, each list
// in record order: the verified ones apart from the others, since a
// verified heartbeat decides rule 2 ahead of any other (D-rule2-sidedoor).
type beatLine struct {
	verified, other []beat
}

// verifiedBeat reports whether a heartbeat that arrived via v counts as a
// verified one under D-rule2-sidedoor: v is one of the paths DESIGN 6.4
// lets reach level A or B (levelVia). For an editor.activity record that is
// `editor`, a heartbeat that passed the peer-process check, or `hook`, from
// the Cursor hooks SM 1.2 lists, which the decision names neither way. Any
// other via, such as `plugin`, `bridge`, `bridge_unverified`, `track` or
// `editor_unverified`, is a side door here.
func verifiedBeat(v event.Via) bool { return levelVia(v) }

// buildBeats indexes the editor heartbeats SM 3.2 rule 2 reads. An agent's
// edit can be reported from an editor window that isn't in front, so it
// says nothing about which one is, and rule 2's heartbeats are the others.
func (d *deriver) buildBeats() {
	for i, e := range d.recs {
		p, ok := e.Data.(*event.EditorActivity)
		if !ok || e.ProjectKey == "" || p.EditSource == event.EditSourceAgent || !d.pres.slotInput(e.T, false) {
			continue
		}
		b := beat{t: e.T, pos: i, proj: e.ProjectKey, via: e.Via}
		if verifiedBeat(e.Via) {
			d.beats.verified = append(d.beats.verified, b)
		} else {
			d.beats.other = append(d.beats.other, b)
		}
	}
}

// pick returns the heartbeat SM 3.2 rule 2 resolves an editor by, of those
// in(b) holds for that came at from or later: the most recent verified one,
// else the most recent other one (D-rule2-sidedoor). in must hold for a
// prefix of each list, as a bound on time or record order does.
func (bl *beatLine) pick(from int64, in func(beat) bool) (beat, bool) {
	for _, bs := range [][]beat{bl.verified, bl.other} {
		j := sort.Search(len(bs), func(j int) bool { return !in(bs[j]) }) - 1
		if j >= 0 && bs[j].t >= from {
			return bs[j], true
		}
	}
	return beat{}, false
}

// slotBeat returns the heartbeat that resolves editor slot s: of those with
// input in the same or the previous slot (SM 3.2 rule 2), the one pick
// prefers.
func (d *deriver) slotBeat(s *Slot) (beat, bool) {
	return d.beats.pick(s.Start-event.SlotMS, func(b beat) bool { return b.t < s.End() })
}

// editorProject returns the project the editor in focus is on at the
// record at pos, for SC1's typed-edit focus test, by SM 3.2 rule 2 in
// D-editor-heartbeat-first's order: the project of the heartbeat pick
// prefers among those with input in the same or the previous slot, up to
// the record; else the project the focus record f names, which may be "".
// A typed edit is such a heartbeat itself, so another project's heartbeat
// decides only when it is verified and the edit isn't.
func (d *deriver) editorProject(f *focusRec, pos int) string {
	from := floorSlot(d.recs[pos].T) - event.SlotMS
	if b, ok := d.beats.pick(from, func(b beat) bool { return b.pos <= pos }); ok {
		return b.proj
	}
	return f.project
}

// indexAttribution builds what attribute looks up: each session's anchors
// in time order. A relayed anchor resolves no slot: it is no human anchor
// for anything but its own lead and reading, and 3.3 rule 1's focus test
// doesn't apply to it (SM 2.3, 2.7).
func (c *creditor) indexAttribution() {
	d := c.d
	c.bySession = map[string][]int{}
	for i, a := range d.anchors {
		if a.SessionKey != "" && !a.relayed() {
			c.bySession[a.SessionKey] = append(c.bySession[a.SessionKey], i)
		}
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
//     else, for several, SM 3.2 rule 1's look-ahead and ATTRIB_STICKY_S,
//     and failing both the slot is ambiguous (hostedProject).
//  2. An editor: the project of the most recent editor heartbeat with
//     input in the same or previous slot, a verified one first, and a side
//     door's capping the slot at level C (D-rule2-sidedoor); else, when
//     there is no such heartbeat, the project the focus record names
//     (D-editor-heartbeat-first).
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
			at.proj, r, at.ambiguous = c.hostedProject(s)
			at.resolver = int32(r)
		}
		// SM 3.2 rule 1: a terminal hosting no harness resolves through
		// the shell hook's cwd once v1.5 has one, and until then is
		// ambiguous, as one hosting several projects is, so anchors count
		// in it. An agent app keeps rule 3: its session's project or none.
		if at.proj == "" && s.FocusClass == event.AppTerminal {
			at.ambiguous = true
		}
	case event.AppEditor:
		// D-editor-heartbeat-first: the client names an editor's project
		// from the sessions it hosts (#91), and every window of an editor
		// is one process on Windows and one app on macOS, so the record
		// names the editor, not the window in front. Rule 2's heartbeat is
		// how SM tells the windows apart, so it decides whenever there is
		// one, and the record only before the first, as for SM 6.2's Cursor
		// prompt.
		if b, ok := c.d.slotBeat(s); ok {
			at.proj = b.proj
			if !verifiedBeat(b.via) {
				at.sideDoor = b.via
			}
		} else {
			at.proj = s.FocusProject
		}
	case event.AppNone:
		at.unrecorded = c.d.focus.longest(s.Start, s.End()) == noFocus
	case event.AppUnknown:
		at.unknown = true
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
// returns the project, the anchor that decided it or -1, and whether the
// slot is ambiguous.
//
// An anchor is in the window when its session is one the window hosts. A
// slot takes its project from the anchor, the project of the act.
func (c *creditor) hostedProject(s *Slot) (string, int, bool) {
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
		return proj, -1, false
	}
	if a := c.lookAhead(s); a >= 0 {
		return c.d.anchors[a].ProjectKey, a, false
	}
	if a := c.sticky(s); a >= 0 {
		return c.d.anchors[a].ProjectKey, a, false
	}
	return "", -1, true
}

// lookAhead returns the first anchor of a session s's window hosts after
// the slot begins, if the slot lies inside its lead, or if it comes within
// READ_HORIZON_S and its session's bucket isn't empty when the slot begins,
// and -1 otherwise (SM 3.2 rule 1). After is strict: an anchor at the
// slot's very start is the last one before it, which ATTRIB_STICKY_S reads
// (E-6.3-two-tabs). "Inside the lead" means the slot starts inside it. A
// lead ends at the anchor's T, after the slot begins, except for a prompt
// that rules 6 and 7 made typed at an agent app's stamp, whose lead ends
// at the stamp, up to RELAY_MATCH_S before its T (Anchor.leadEnd): a slot
// that starts between the two is outside the lead, and the bucket test
// decides it. Within READ_HORIZON_S counts as SM 2.4's horizon before an
// act does: the slot that starts READ_HORIZON_S before the anchor is
// inside.
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
	if s.Start >= a.leadEnd()-a.LeadMS && s.Start < a.leadEnd() {
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

// steer applies the slot's resolution to a second credited in it, so a
// side door can't steer reading or acts at level A or B into its project.
// In a window hosting several sessions the second takes no better level
// than the anchor that gave the slot to its session (DESIGN 6.4,
// E-6.3-two-tabs), and in an editor slot that a side-door heartbeat
// resolved it is level C (D-rule2-sidedoor). Either way it carries the
// side door's label, which a lookup it brackets then carries too; the slot's
// Credit adds the label to every entry (slotCredits) when the second
// already carries another.
func (c *creditor) steer(at *attribution, sec *second) {
	if at.resolver >= 0 {
		a := &c.d.anchors[at.resolver]
		sec.level = lowerLevel(sec.level, a.Level)
		if sec.label == "" {
			sec.label = sideDoorLabel(a.Via)
		}
	}
	if at.sideDoor != "" {
		sec.level = event.EvidenceC
		if sec.label == "" {
			sec.label = sideDoorLabel(at.sideDoor)
		}
	}
	// A second credited in a slot whose focus is unknown, or from an anchor
	// that passed its focus test only that way, the one that resolved the
	// slot included, is level C with focus_unknown (SM 2.5).
	if at.unknown || at.resolver >= 0 && c.d.anchors[at.resolver].focusUnknown {
		sec.focusUnknown = true
	}
	if sec.focusUnknown {
		sec.level = event.EvidenceC
	}
}
