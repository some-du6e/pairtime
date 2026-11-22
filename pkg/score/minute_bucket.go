// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/params"
)

// MinuteMS is the length of a UTC minute in milliseconds.
const MinuteMS = 60_000

// slotsPerMinute is the number of 15-second slots in a minute.
const slotsPerMinute = int(MinuteMS / event.SlotMS)

// Minute is one UTC minute of what the scorer derived: the credit of each
// of its 15-second slots in full, by project, reason and level. It is what
// every rollup sums (SM 5.3), and its Bucket is the summary SM 5.1 uploads,
// which keeps only each slot's dominant reason.
type Minute struct {
	// Start is the minute's start in Unix milliseconds.
	Start int64 `json:"start"`
	// Slots are the minute's derived slots in time order: four, or fewer at
	// either end of what the scorer has derived.
	Slots []MinuteSlot `json:"slots"`
}

// MinuteSlot is one derived slot of a minute: its credit slot, whose
// ProjectKey is the project the slot's seconds go to (SM 3.2, SM 5.1's p).
// A rollup counts the slot's uncredited seconds toward that project.
type MinuteSlot struct {
	Credit
}

// floorMinute returns the start of the UTC minute holding t.
func floorMinute(t int64) int64 {
	q := t / MinuteMS
	if t%MinuteMS < 0 {
		q--
	}
	return q * MinuteMS
}

// End returns the end of the minute.
func (m Minute) End() int64 { return m.Start + MinuteMS }

// Minutes groups d's credit slots by UTC minute, in time order. A minute
// holds only the slots d derived.
func Minutes(d Derived) []Minute {
	credits := slices.Clone(d.Credits)
	slices.SortStableFunc(credits, func(a, b Credit) int { return cmp.Compare(a.Slot, b.Slot) })
	var out []Minute
	for _, c := range credits {
		start := floorMinute(c.Slot)
		if n := len(out); n == 0 || out[n-1].Start != start {
			out = append(out, Minute{Start: start})
		}
		m := &out[len(out)-1]
		if n := len(m.Slots); n > 0 && m.Slots[n-1].Slot == c.Slot {
			continue // a slot Derived holds twice, as Append of overlapping views would give
		}
		m.Slots = append(m.Slots, MinuteSlot{Credit: c})
	}
	return out
}

// HalfWeighted reports whether some credited second of the minute is at
// half weight, waiting for a progress event (SM 4.3). WP-SC6 adds the
// weights; until then every second is at weight 1.
func (m Minute) HalfWeighted() bool {
	for _, s := range m.Slots {
		for _, e := range s.Credited {
			if e.Halves < 2*e.Seconds {
				return true
			}
		}
	}
	return false
}

// restored reports whether a later progress event put some half-weighted
// second of the minute back to weight 1 (SM 4.3), which its reason says.
func (m Minute) restored() bool {
	for _, s := range m.Slots {
		for _, e := range s.Credited {
			if strings.HasSuffix(string(e.Reason), event.RestoredSuffix) {
				return true
			}
		}
	}
	return false
}

// State returns the minute's bucket state (SM 5.1, DESIGN 6.2 D1): pending
// until the minute is final, which is PENDING_WINDOW_S after it ends, then
// provisional while it holds half-weighted seconds, restored once a
// progress event has restored them all, and final otherwise.
func (m Minute) State(final bool) event.BucketState {
	switch {
	case !final:
		return event.StatePending
	case m.HalfWeighted():
		return event.StateProvisional
	case m.restored():
		return event.StateRestored
	}
	return event.StateFinal
}

// empty reports whether nothing was credited in the minute and no agent ran
// in it: SM 5.1 writes a bucket only for a minute in which one of those
// happened.
func (m Minute) empty() bool {
	for _, s := range m.Slots {
		if s.AgentS > 0 || s.Halves() > 0 {
			return false
		}
	}
	return true
}

// BucketInfo is what a minute bucket says besides the minute's credit.
type BucketInfo struct {
	// Table is the parameter table the minute was scored with; its hash is
	// the bucket's params.
	Table *params.Table
	// Device is the device's opaque key.
	Device string
	// State is the bucket's state, from Minute.State.
	State event.BucketState
	// Witness is the minute's witness label (DESIGN 7.4). Credit in a
	// backfilled minute is level C (DESIGN 6.4).
	Witness event.Witness
	// Rev numbers the bucket's revision (DESIGN 3.7).
	Rev uint32
}

// Bucket returns the minute as SM 5.1's minute bucket. Each of its four
// slots holds, where the scorer derived the slot and credited some of it:
//
//   - p, the project with the most credit in the slot, the lower key on a
//     tie (SM 3.3 rule 6). SM 3.3 rule 1 keeps a slot's credit in one
//     project, so this names the only one;
//   - cr, every credited second of the slot, in half-seconds where the
//     no-progress rule weighs them (SM 4.3);
//   - why, the credit reason with the most credit in the slot, the earlier
//     in SM 5.2's table on a tie, and kind, the activity it counts as;
//   - ev, the weakest evidence level among the slot's credited seconds, so
//     a slot claims no more than its weakest second (DESIGN 6.4), and C in
//     a backfilled minute;
//   - lbl, the labels of pkg/event's slot list (event.SlotLabels) that the
//     anchors behind its credit carry, in that list's order: the side-door
//     labels (DESIGN 4.1 E-6), then focus_unknown (SM 2.5).
//
// A slot nothing credited, or the scorer didn't derive, is empty. The
// minute's uncredited seconds, agent_running_s, agent_only and anchors sum
// its slots. agent_only gives, for each project a turn of which ran in the
// minute, the seconds it ran that weren't credited to that project, the
// slots' Credit.AgentOnly summed (SM 2.6, D-bucket-agent-only); a project
// with none is left out. Anchors are local (DESIGN 6.2 D9): Shareable
// clears them.
func (m Minute) Bucket(info BucketInfo) event.MinuteBucket {
	b := event.MinuteBucket{
		V:       Version(),
		Params:  info.Table.Hash(),
		Minute:  time.UnixMilli(m.Start).UTC(),
		Device:  info.Device,
		Slots:   make([]event.BucketSlot, slotsPerMinute),
		State:   info.State,
		Witness: info.Witness,
		Rev:     info.Rev,
	}
	var halves int64
	var anchors []string
	for _, s := range m.Slots {
		i := int((s.Slot - m.Start) / event.SlotMS)
		if i < 0 || i >= slotsPerMinute {
			continue
		}
		b.Slots[i] = s.bucketSlot(info.Witness == event.Backfilled)
		halves += s.Halves()
		for r, n := range s.Uncredited {
			if b.Uncredited == nil {
				b.Uncredited = map[event.Reason]float64{}
			}
			b.Uncredited[r] += float64(n)
		}
		b.AgentRunningS += float64(s.AgentS)
		for p, n := range s.AgentOnly {
			if n <= 0 {
				continue
			}
			if b.AgentOnly == nil {
				b.AgentOnly = map[string]float64{}
			}
			b.AgentOnly[p] += float64(n)
		}
		for _, e := range s.Credited {
			anchors = append(anchors, e.AnchorIDs...)
		}
	}
	b.CreditedS = float64(halves) / 2
	if len(anchors) > 0 {
		slices.Sort(anchors)
		b.Anchors = slices.Compact(anchors)
	}
	return b
}

// bucketSlot sums one slot's credit into a minute bucket's slot.
func (s MinuteSlot) bucketSlot(backfilled bool) event.BucketSlot {
	byProject := map[string]int64{}
	byReason := map[event.Reason]int64{}
	var halves int64
	var level event.Evidence
	labels := map[event.Label]bool{}
	for _, e := range s.Credited {
		if e.Halves <= 0 {
			continue
		}
		halves += e.Halves
		byProject[e.ProjectKey] += e.Halves
		byReason[e.Reason] += e.Halves
		level = lowerLevel(level, e.Level)
		for _, l := range e.Labels {
			labels[l] = true
		}
	}
	if halves == 0 {
		return event.BucketSlot{}
	}
	if backfilled {
		level = event.EvidenceC
	}
	why := mostCredit(byReason, reasonLess)
	out := event.BucketSlot{
		Project:  mostCredit(byProject, func(a, b string) bool { return a < b }),
		Credited: float64(halves) / 2,
		Why:      why,
		Kind:     why.Activity(),
		Ev:       level,
	}
	for _, l := range event.SlotLabels() {
		if labels[l] {
			out.Lbl = append(out.Lbl, l)
		}
	}
	return out
}

// mostCredit returns the key with the most credit, the first by less on a
// tie.
func mostCredit[K comparable](by map[K]int64, less func(a, b K) bool) K {
	var best K
	var most int64 = -1
	for k, n := range by {
		if n > most || (n == most && less(k, best)) {
			best, most = k, n
		}
	}
	return best
}

// creditOrder is SM 5.2's table of credit reasons, in its order.
var creditOrder = []event.Reason{
	event.ReasonPrompt, event.ReasonAnswer, event.ReasonGoAhead, event.ReasonQueued, event.ReasonPermission,
	event.ReasonInterrupt, event.ReasonEdit, event.ReasonNavigate, event.ReasonCommand, event.ReasonTest,
	event.ReasonCommit, event.ReasonSupervise, event.ReasonRead, event.ReasonLookup,
}

// reasonLess orders credit reasons by SM 5.2's table, a reason without a
// suffix before the same reason with one.
func reasonLess(a, b event.Reason) bool {
	ia, ib := slices.Index(creditOrder, a.Base()), slices.Index(creditOrder, b.Base())
	if ia != ib {
		return ia < ib
	}
	return a < b
}

// creditedHalves sums the minute's credited half-seconds by project.
func (m Minute) creditedHalves() map[string]int64 {
	out := map[string]int64{}
	for _, s := range m.Slots {
		for _, e := range s.Credited {
			out[e.ProjectKey] += e.Halves
		}
	}
	return out
}
