// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"slices"
	"sort"

	"github.com/some-du6e/pairtime/pkg/event"
)

// Return is a silent run that SM 2.1's return rule credits: longer than
// PRESENCE_GAP_S, at most RETURN_GAP_MAX_S, with input on both sides, and
// ended by a substantive act in the project that held focus through it.
// Its slots past the tail are returned. Of the run, at most CreditMS
// qualifies:
//
//	RETURN_CREDIT = 60 * unread_prose_words / READ_WPM + THINK_LEAD_S
//
// The reading part runs from the end of the tail to ReadUntil, the think
// part is the anchor's own think lead where it reaches back into the run,
// and what lies between is absent. Crediting them is WP-SC2's.
type Return struct {
	// RunStart and RunEnd bound the run's slots.
	RunStart int64 `json:"run_start"`
	RunEnd   int64 `json:"run_end"`
	// LastInput is when the last input before the run came, and TailEnd
	// PRESENCE_TAIL_S after it.
	LastInput int64 `json:"last_input"`
	TailEnd   int64 `json:"tail_end"`
	// ReadUntil ends the reading part: TailEnd plus the reading time of
	// the unread prose, at most RunEnd.
	ReadUntil int64 `json:"read_until"`
	// UnreadWords is the prose the agent showed in the sessions of the
	// focused window since the person last had them in focus with input.
	UnreadWords int64  `json:"unread_words"`
	Anchor      string `json:"anchor_id"`
	ProjectKey  string `json:"project_key"`
	CreditMS    int64  `json:"credit_ms"`
}

// returnsFor reports whether an anchor of kind k and class c is
// substantive enough to end a returned run (SM 2.1): a substantive prompt,
// answer or queued message, a typed edit, a typed command or a human
// commit. Navigation, go-aheads, permission answers and interrupts don't.
func returnsFor(k event.AnchorKind, c event.PromptClass) bool {
	switch k {
	case event.AnchorSubstantivePrompt, event.AnchorAnswer, event.AnchorTypedEdit, event.AnchorTypedCommand,
		event.AnchorHumanCommit:
		return true
	case event.AnchorQueuedMessage:
		return c != event.ClassLowEffort
	}
	return false
}

// markReturns applies the return rule to every silent run (SM 2.1).
func (d *deriver) markReturns() {
	p := d.pres
	for _, run := range p.runs {
		if !run.left || !run.right {
			continue
		}
		length := int64(run.j-run.i) * event.SlotMS
		if length <= d.r.presenceGap || length > d.r.returnGapMax {
			continue
		}
		start, end := p.slots[run.i].Start, p.slots[run.j-1].End()
		focus := d.focus.at(start)
		if focus.project == "" {
			continue
		}
		// The first anchor after the run must be substantive, in the
		// focused project, and within READ_HORIZON_S of the run's end,
		// with no lock, sleep or focus change from the run's start to it.
		k := sort.Search(len(d.anchors), func(k int) bool { return d.anchors[k].T >= end })
		if k == len(d.anchors) {
			continue
		}
		a := d.anchors[k]
		if a.T > end+d.r.readHorizon || !returnsFor(a.Kind, a.PromptClass) || a.ProjectKey != focus.project ||
			d.focus.changed(start, a.T) || d.breakIn(end, a.T) {
			continue
		}
		words := d.unreadWords(focus, run, end)
		read := d.r.readingMS(words)
		d.returns = append(d.returns, Return{RunStart: start, RunEnd: end, LastInput: run.lastInput,
			TailEnd: run.tailEnd, ReadUntil: min(run.tailEnd+read, end), UnreadWords: words, Anchor: a.ID,
			ProjectKey: focus.project, CreditMS: read + d.r.thinkLead})
		for k := run.i; k < run.j; k++ {
			s := &p.slots[k]
			switch {
			case s.Present != event.PresentTail:
				s.Present, s.Then = event.PresentReturned, event.PresentReturned
			case s.PresentUntil < s.End():
				s.Then = event.PresentReturned
			}
		}
		// The silence runs into the slot after it until the input a sample
		// there dates (presence.markStarts).
		if s := &p.slots[run.j]; s.Present == event.PresentNo && s.Then == event.PresentYes {
			s.Present = event.PresentReturned
		}
	}
}

// breakIn reports whether a slot overlapping [from, to] is a hard break.
func (d *deriver) breakIn(from, to int64) bool {
	for t := floorSlot(from); t <= to; t += event.SlotMS {
		if s := d.pres.slotAt(t); s != nil && (s.Asleep || s.Locked) {
			return true
		}
	}
	return false
}

// unreadWords counts the prose the agent showed, up to the run's end, in
// the sessions the focused window hosts, or in the focused project's
// sessions if it hosts none, since the person last had each in focus with
// input (SM 2.1's unread_prose_words).
func (d *deriver) unreadWords(f *focusRec, run silentRun, end int64) int64 {
	var keys []string
	if len(f.hosted) > 0 {
		keys = f.hosted
	} else {
		for k, s := range d.sessions {
			if s.project == f.project {
				keys = append(keys, k)
			}
		}
		slices.Sort(keys)
	}
	var words int64
	for _, k := range keys {
		s := d.sessions[k]
		if s == nil {
			continue
		}
		seen := d.lastSeen(s, run.i)
		for _, o := range s.outputs {
			if o.t > seen && o.t <= end && !(s.native && sideDoor(d.recs[o.pos].Via)) {
				words = addCount(words, count(o.d.Words))
			}
		}
	}
	return words
}

// lastSeen returns the latest input in a slot before slot i in which the
// window in focus held session s, or its editor for an IDE session, or the
// start of the timeline if there is none.
func (d *deriver) lastSeen(s *session, i int) int64 {
	p := d.pres
	for k := i - 1; k >= 0; k-- {
		sl := &p.slots[k]
		if !sl.InputAny {
			continue
		}
		if slices.Contains(sl.Hosted, s.key) || (len(sl.Hosted) == 0 && sl.FocusProject != "" && sl.FocusProject == s.project) {
			return p.lastIn[k]
		}
	}
	return p.first - 1
}
