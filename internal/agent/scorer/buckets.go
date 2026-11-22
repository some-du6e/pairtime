// SPDX-License-Identifier: AGPL-3.0-or-later

package scorer

import (
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/store"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/score"
)

// minuteMS is a minute in milliseconds.
const minuteMS = 60_000

// bucketMeta is what every bucket of one Step says about who scored it and
// how: v is the scoring version, pkg/score's VERSION, and params the hash
// of the parameter table the Step ran with (SM 5.1, 7.6; DESIGN 4.3).
type bucketMeta struct {
	v, params, device string
}

// minuteBuckets sums what one Step derived, d, into minute buckets (SM
// 5.1), one for each UTC minute in which anything was credited or an agent
// ran, keyed by the minute's start in Unix ms. recs are the records the
// Step scored, which say which harness runs each session. The buckets have
// no state; the loop sets it.
//
// This is the seam for WP-SC4, which builds SM 5.1's buckets in pkg/score.
// Until then a slot's summary names its largest credit entry's project,
// reason and level, the first of them on a tie, with the side-door labels
// of every entry, and Bucket.Projects keeps the exact split that every
// total the agent shows is summed from: credit by reason and level,
// agent-only seconds by harness, and uncredited seconds by reason.
func minuteBuckets(meta bucketMeta, d score.Derived, recs []event.Event) map[int64]store.Bucket {
	// Which harness runs each session, from its records and its turns.
	harnessOf := map[string]event.Harness{}
	for _, e := range recs {
		if isSession(e.SessionKey) && isHarness(e.Harness) {
			harnessOf[e.SessionKey] = e.Harness
		}
	}
	for _, t := range d.Turns {
		if isSession(t.SessionKey) && isHarness(t.Harness) {
			harnessOf[t.SessionKey] = t.Harness
		}
	}
	anchorSession := map[string]string{}
	for _, a := range d.Anchors {
		anchorSession[a.ID] = a.SessionKey
	}
	focus := map[int64]string{}
	for _, s := range d.Slots {
		if s.FocusProject != "" {
			focus[s.Start] = s.FocusProject
		}
	}
	runs := turnRuns(d.Turns)

	mins := map[int64]*minute{}
	for _, c := range d.Credits {
		start := c.Slot - floorMod(c.Slot, minuteMS)
		m := mins[start]
		if m == nil {
			m = newMinute(meta, start)
			mins[start] = m
		}
		m.add(c, anchorSession, harnessOf)
	}
	// The harnesses that had a session in each project in the minute: a
	// record of the session, a turn of it running, or an anchor of it
	// behind the minute's credit (minute.add).
	for _, e := range recs {
		if m := mins[e.T-floorMod(e.T, minuteMS)]; m != nil && e.ProjectKey != "" && isSession(e.SessionKey) {
			m.saw(e.ProjectKey, harnessOf[e.SessionKey])
		}
	}
	if len(mins) > 0 {
		keys := slices.Sorted(maps.Keys(mins))
		first, last := keys[0], keys[len(keys)-1]
		for p, rs := range runs {
			for _, r := range rs {
				from := max(r.from-floorMod(r.from, minuteMS), first)
				for start := from; start < r.to && start <= last; start += minuteMS {
					if m := mins[start]; m != nil {
						m.saw(p, r.harness)
					}
				}
			}
		}
	}
	out := make(map[int64]store.Bucket, len(mins))
	for start, m := range mins {
		if b, ok := m.bucket(focus, runs); ok {
			out[start] = b
		}
	}
	return out
}

func isSession(key string) bool      { return key != "" && key != event.NoSession }
func isHarness(h event.Harness) bool { return h != "" && h != event.HarnessNone }

// run is a span in which a turn of one harness ran, [from, to) in Unix ms.
type run struct {
	from, to int64
	harness  event.Harness
}

// turnRuns returns, by project, when each turn whose harness is known ran:
// from its submit to its RunUntil (SM 2.6).
func turnRuns(turns []score.Turn) map[string][]run {
	out := map[string][]run{}
	for _, t := range turns {
		if t.ProjectKey == "" || !isHarness(t.Harness) || t.RunUntil <= t.TSubmit {
			continue
		}
		out[t.ProjectKey] = append(out[t.ProjectKey], run{from: t.TSubmit, to: t.RunUntil, harness: t.Harness})
	}
	return out
}

// minute sums one minute's credit slots.
type minute struct {
	b       store.Bucket
	halves  int64
	agent   int64
	anchors map[string]bool
	proj    map[string]*projectSum
	slots   []score.Credit
}

// projectSum is one project's part of a minute, as it is summed.
type projectSum struct {
	credited   map[store.Credited]int64 // half-seconds by reason and level; the key's Halves is 0
	agent      int64
	agentOnly  int64
	harness    map[event.Harness]int64
	uncredited map[event.Reason]int64
}

func newMinute(meta bucketMeta, start int64) *minute {
	return &minute{
		b: store.Bucket{V: meta.v, Params: meta.params, Minute: time.UnixMilli(start).UTC(), Device: meta.device,
			Slots: make([]event.BucketSlot, minuteMS/event.SlotMS)},
		anchors: map[string]bool{},
		proj:    map[string]*projectSum{},
	}
}

func (m *minute) project(key string) *projectSum {
	p := m.proj[key]
	if p == nil {
		p = &projectSum{credited: map[store.Credited]int64{}, harness: map[event.Harness]int64{},
			uncredited: map[event.Reason]int64{}}
		m.proj[key] = p
	}
	return p
}

// saw notes that harness h had a session in project key during the minute,
// if the project has a part in the minute.
func (m *minute) saw(key string, h event.Harness) {
	if p := m.proj[key]; p != nil && isHarness(h) {
		p.harness[h] += 0
	}
}

// add sums one slot's credit into the minute.
func (m *minute) add(c score.Credit, anchorSession map[string]string, harnessOf map[string]event.Harness) {
	m.slots = append(m.slots, c)
	if i := (c.Slot - m.b.At()) / event.SlotMS; i >= 0 && i < int64(len(m.b.Slots)) {
		m.b.Slots[i] = slotSummary(c)
	}
	for r, s := range c.Uncredited {
		if m.b.Uncredited == nil {
			m.b.Uncredited = map[event.Reason]float64{}
		}
		m.b.Uncredited[r] += float64(s)
	}
	m.agent += c.AgentS
	for _, e := range c.Credited {
		p := m.project(e.ProjectKey)
		p.credited[store.Credited{Why: e.Reason, Ev: e.Level}] += e.Halves
		m.halves += e.Halves
		if e.InTurn {
			p.agent += e.Seconds
		}
		for _, id := range e.AnchorIDs {
			m.anchors[id] = true
			if h := harnessOf[anchorSession[id]]; isHarness(h) {
				p.harness[h] += 0
			}
		}
	}
	for key, s := range c.AgentOnly {
		p := m.project(key)
		p.agent += s
		p.agentOnly += s
	}
}

// bucket returns the minute's bucket, and false for a minute in which
// nothing was credited and no agent ran, which has none (SM 5.1). Each
// slot's agent-only seconds go to the harness whose turn of the project ran
// longest in the slot, and each slot's uncredited seconds to one project
// (owner).
func (m *minute) bucket(focus map[int64]string, runs map[string][]run) (store.Bucket, bool) {
	if m.halves == 0 && m.agent == 0 {
		return store.Bucket{}, false
	}
	for _, c := range m.slots {
		for key, s := range c.AgentOnly {
			if h := longest(runs[key], c.Slot, m.proj[key]); isHarness(h) {
				m.proj[key].harness[h] += s
			}
		}
		if key := m.owner(c, focus[c.Slot]); key != "" {
			p := m.project(key)
			for r, s := range c.Uncredited {
				p.uncredited[r] += s
			}
		}
	}
	b := m.b
	b.CreditedS = float64(m.halves) / 2
	b.AgentRunningS = float64(m.agent)
	if len(m.anchors) > 0 {
		b.Anchors = slices.Sorted(maps.Keys(m.anchors))
	}
	for key, p := range m.proj {
		pm := store.ProjectMinute{AgentS: p.agent, AgentOnlyS: p.agentOnly}
		for k, h := range p.credited {
			if h > 0 {
				pm.Credited = append(pm.Credited, store.Credited{Why: k.Why, Ev: k.Ev, Halves: h})
			}
		}
		slices.SortFunc(pm.Credited, func(x, y store.Credited) int {
			if c := strings.Compare(string(x.Why), string(y.Why)); c != 0 {
				return c
			}
			return strings.Compare(string(x.Ev), string(y.Ev))
		})
		if len(p.harness) > 0 {
			pm.Harnesses = maps.Clone(p.harness)
		}
		if len(p.uncredited) > 0 {
			pm.Uncredited = maps.Clone(p.uncredited)
		}
		if len(pm.Credited) == 0 && pm.AgentS == 0 && len(pm.Uncredited) == 0 {
			continue
		}
		if b.Projects == nil {
			b.Projects = map[string]store.ProjectMinute{}
		}
		b.Projects[key] = pm
	}
	return b, true
}

// longest returns the harness whose turns ran longest in the slot from
// slot, among rs, the runs of one project, the first in name order on a
// tie. With no run there, it is the first harness p saw in the minute, or
// "" when p saw none.
func longest(rs []run, slot int64, p *projectSum) event.Harness {
	ran := map[event.Harness]int64{}
	for _, r := range rs {
		if from, to := max(r.from, slot), min(r.to, slot+event.SlotMS); to > from {
			ran[r.harness] += to - from
		}
	}
	best, most := event.Harness(""), int64(0)
	for _, h := range slices.Sorted(maps.Keys(ran)) {
		if ran[h] > most {
			best, most = h, ran[h]
		}
	}
	if best == "" && p != nil && len(p.harness) > 0 {
		best = slices.Sorted(maps.Keys(p.harness))[0]
	}
	return best
}

// owner returns the project a slot's uncredited seconds belong to: the one
// its focus resolves to (SM 3.2); else the one with most of the slot's
// agent-only seconds; else the one with most of its credit; else the one
// with most of the minute's credit and agent time. Ties go to the first in
// key order.
func (m *minute) owner(c score.Credit, focusProject string) string {
	if focusProject != "" {
		return focusProject
	}
	if key := most(c.AgentOnly); key != "" {
		return key
	}
	credit := map[string]int64{}
	for _, e := range c.Credited {
		credit[e.ProjectKey] += e.Halves
	}
	if key := most(credit); key != "" {
		return key
	}
	total := map[string]int64{}
	for key, p := range m.proj {
		total[key] = p.agentOnly
		for _, h := range p.credited {
			total[key] += h
		}
	}
	return most(total)
}

// most returns the key with the largest positive value, the first in key
// order on a tie, or "" when no value is positive.
func most(m map[string]int64) string {
	best, top := "", int64(0)
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if m[k] > top && k != "" {
			best, top = k, m[k]
		}
	}
	return best
}

// slotSummary sums one slot's credit up the way SM 5.1's slots[4] does:
// credited seconds; the project, reason and activity of the largest credit
// entry; the weakest evidence level among the credited entries, so a slot
// claims no more than its weakest second (DESIGN 6.4), as pkg/score's
// Minute.Bucket has it; and the labels of pkg/event's slot list
// (event.SlotLabels) that the credited entries carry, in that list's
// order: the side doors, then focus_unknown. Part of the WP-SC4 seam.
func slotSummary(c score.Credit) event.BucketSlot {
	var s event.BucketSlot
	h := c.Halves()
	if h == 0 || len(c.Credited) == 0 {
		return s
	}
	best := 0
	var level event.Evidence
	for i, e := range c.Credited {
		if e.Halves > c.Credited[best].Halves {
			best = i
		}
		if e.Halves > 0 {
			level = weakest(level, e.Level)
		}
	}
	d := c.Credited[best]
	s.Project, s.Credited, s.Why, s.Kind, s.Ev = d.ProjectKey, float64(h)/2, d.Reason, d.Reason.Activity(), level
	for _, l := range event.SlotLabels() {
		for _, e := range c.Credited {
			if e.Halves > 0 && slices.Contains(e.Labels, l) {
				s.Lbl = append(s.Lbl, l)
				break
			}
		}
	}
	return s
}

// weakest returns the weaker of two evidence levels, C below B below A;
// "" is none yet, so any level is weaker.
func weakest(a, b event.Evidence) event.Evidence {
	if a > b { // "C" > "B" > "A" > ""
		return a
	}
	return b
}

// emptyBucket is the revision that says a pending minute turned out to
// have no credit and no agent time.
func emptyBucket(meta bucketMeta, start int64) store.Bucket {
	return store.Bucket{V: meta.v, Params: meta.params, Minute: time.UnixMilli(start).UTC(), Device: meta.device,
		Slots: make([]event.BucketSlot, minuteMS/event.SlotMS)}
}

// floorMod returns a mod m in [0, m), for m > 0.
func floorMod(a, m int64) int64 {
	r := a % m
	if r < 0 {
		r += m
	}
	return r
}
