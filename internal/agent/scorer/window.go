// SPDX-License-Identifier: AGPL-3.0-or-later

package scorer

import (
	"cmp"
	"maps"
	"slices"
	"strings"

	"github.com/some-du6e/pairtime/pkg/event"
)

// held is a record in the window, with the batch it came in.
type held struct {
	ev event.Event
	b  uint64
}

// window is what the loop gives pkg/score at each Step: every record whose
// t is from or later, and the carry, which stands for the records before
// from (see the package doc).
type window struct {
	from  int64
	recs  []held // in the order they came in
	carry carry
}

// add puts ev, which came in batch b, in the window, or in the carry when
// it is dated before the window.
func (w *window) add(ev event.Event, b uint64) {
	if ev.T < w.from {
		w.carry.fold(ev)
		w.carry.prune(w.from)
		return
	}
	w.recs = append(w.recs, held{ev: ev, b: b})
}

// slide moves the window's start to from, folding the records before it
// into the carry. A start before the current one changes nothing.
func (w *window) slide(from int64) {
	if from <= w.from {
		return
	}
	kept := make([]held, 0, len(w.recs))
	for _, h := range w.recs {
		if h.ev.T < from {
			w.carry.fold(h.ev)
		} else {
			kept = append(kept, h)
		}
	}
	w.recs = kept
	w.from = from
	w.carry.prune(from)
}

// records returns what a Step scores: the carry's records, then the
// window's.
func (w *window) records() []event.Event {
	out := w.carry.records(make([]event.Event, 0, len(w.recs)+16))
	for _, h := range w.recs {
		out = append(out, h.ev)
	}
	return out
}

// firstBatch returns the lowest batch number of the window's records, or
// 0 when it holds none.
func (w *window) firstBatch() uint64 {
	var first uint64
	for _, h := range w.recs {
		if first == 0 || h.b < first {
			first = h.b
		}
	}
	return first
}

// carry keeps, of the records before the window, the ones a rule can
// still read after any time span: what holds until a later record of the
// same kind says otherwise. Those are the last focus, power and presence
// records, which say what was in front, whether the screen was locked and
// when the last input came (SM 2.1, 3.2), and for each harness session its
// start, which says how the session runs (SM 2.2), and its last prompt,
// agent output and stop, which say which turn runs and whether the agent
// asked a question (SM 2.2, 2.6). Each slot keeps the latest record in
// record order (t, then seq, then event id), so folding records in any
// order gives the same carry.
type carry struct {
	Focus    *event.Event             `json:"focus,omitempty"`
	Power    *event.Event             `json:"power,omitempty"`
	Presence *event.Event             `json:"presence,omitempty"`
	Sessions map[string]*sessionCarry `json:"sessions,omitempty"`
}

// sessionCarry is what the carry keeps of one harness session.
type sessionCarry struct {
	Start  *event.Event `json:"start,omitempty"`
	Prompt *event.Event `json:"prompt,omitempty"`
	Output *event.Event `json:"output,omitempty"`
	Stop   *event.Event `json:"stop,omitempty"`
	End    *event.Event `json:"end,omitempty"`
	// Last is the t of the session's latest record folded in.
	Last int64 `json:"last"`
}

// carrySessions bounds how many sessions the carry keeps, and
// carrySessionMS how long it keeps one that has had no record. A session
// left open longer than a week without a record starts again from what
// the window holds.
const (
	carrySessions  = 64
	carrySessionMS = 7 * 24 * 3600 * 1000
)

// recordCmp orders records as pkg/score does: by t, then seq, then event
// id.
func recordCmp(a, b *event.Event) int {
	if c := cmp.Compare(a.T, b.T); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Seq, b.Seq); c != 0 {
		return c
	}
	return strings.Compare(a.EventID, b.EventID)
}

// latest returns whichever of cur and ev comes later in record order.
func latest(cur *event.Event, ev event.Event) *event.Event {
	if cur != nil && recordCmp(cur, &ev) >= 0 {
		return cur
	}
	return &ev
}

// fold takes ev, a record before the window, into the carry.
func (c *carry) fold(ev event.Event) {
	switch ev.Data.(type) {
	case *event.OSFocus:
		c.Focus = latest(c.Focus, ev)
	case *event.OSPower:
		c.Power = latest(c.Power, ev)
	case *event.OSPresence:
		c.Presence = latest(c.Presence, ev)
	}
	if ev.SessionKey == "" || ev.SessionKey == event.NoSession {
		return
	}
	if c.Sessions == nil {
		c.Sessions = make(map[string]*sessionCarry)
	}
	s := c.Sessions[ev.SessionKey]
	if s == nil {
		s = &sessionCarry{}
		c.Sessions[ev.SessionKey] = s
	}
	s.Last = max(s.Last, ev.T)
	switch ev.Data.(type) {
	case *event.SessionStart:
		s.Start = latest(s.Start, ev)
	case *event.PromptSubmit:
		s.Prompt = latest(s.Prompt, ev)
	case *event.AgentOutput:
		s.Output = latest(s.Output, ev)
	case *event.AgentStop:
		s.Stop = latest(s.Stop, ev)
	case *event.SessionEnd:
		s.End = latest(s.End, ev)
	}
}

// prune drops the sessions no later record can need: one whose end is its
// latest record, one with no record for carrySessionMS before from, and
// past carrySessions, the ones whose latest record is oldest.
func (c *carry) prune(from int64) {
	for k, s := range c.Sessions {
		if (s.End != nil && s.End.T >= s.Last) || s.Last < from-carrySessionMS {
			delete(c.Sessions, k)
		}
	}
	if len(c.Sessions) <= carrySessions {
		return
	}
	keys := slices.Collect(maps.Keys(c.Sessions))
	slices.SortFunc(keys, func(a, b string) int {
		if x := cmp.Compare(c.Sessions[a].Last, c.Sessions[b].Last); x != 0 {
			return x
		}
		return strings.Compare(a, b)
	})
	for _, k := range keys[:len(keys)-carrySessions] {
		delete(c.Sessions, k)
	}
}

// records appends the carry's records to out, in a fixed order.
func (c *carry) records(out []event.Event) []event.Event {
	for _, e := range []*event.Event{c.Focus, c.Power, c.Presence} {
		if e != nil {
			out = append(out, *e)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(c.Sessions)) {
		s := c.Sessions[k]
		for _, e := range []*event.Event{s.Start, s.Prompt, s.Output, s.Stop, s.End} {
			if e != nil {
				out = append(out, *e)
			}
		}
	}
	return out
}
