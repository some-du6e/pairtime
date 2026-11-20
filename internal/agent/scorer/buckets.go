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
// how.
type bucketMeta struct {
	v, params, device string
}

// minuteBuckets sums one Step's credit slots into minute buckets (SM 5.1),
// one for each UTC minute in which anything was credited or an agent ran,
// keyed by the minute's start in Unix ms. The buckets have no state; the
// loop sets it.
//
// This is the seam for WP-SC4, which builds SM 5.1's buckets in pkg/score.
// Until then a slot's summary names its largest credit entry's project,
// reason and level, the first of them on a tie, with the side-door labels
// of every entry, and Bucket.Projects keeps the exact split that the
// totals the agent shows are summed from.
func minuteBuckets(meta bucketMeta, credits []score.Credit) map[int64]store.Bucket {
	type project struct {
		credited         map[store.Credited]int64 // half-seconds by reason and level, keyed with Halves 0
		agent, agentOnly int64
	}
	type minute struct {
		b       store.Bucket
		halves  int64
		agent   int64
		anchors map[string]bool
		proj    map[string]*project
	}
	mins := map[int64]*minute{}
	proj := func(m *minute, key string) *project {
		p := m.proj[key]
		if p == nil {
			p = &project{credited: map[store.Credited]int64{}}
			m.proj[key] = p
		}
		return p
	}
	for _, c := range credits {
		start := c.Slot - floorMod(c.Slot, minuteMS)
		m := mins[start]
		if m == nil {
			m = &minute{
				b: store.Bucket{V: meta.v, Params: meta.params, Minute: time.UnixMilli(start).UTC(),
					Device: meta.device, Slots: make([]event.BucketSlot, minuteMS/event.SlotMS)},
				anchors: map[string]bool{},
				proj:    map[string]*project{},
			}
			mins[start] = m
		}
		if i := (c.Slot - start) / event.SlotMS; i >= 0 && i < int64(len(m.b.Slots)) {
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
			p := proj(m, e.ProjectKey)
			p.credited[store.Credited{Why: e.Reason, Ev: e.Level}] += e.Halves
			m.halves += e.Halves
			if e.InTurn {
				p.agent += e.Seconds
			}
			for _, id := range e.AnchorIDs {
				m.anchors[id] = true
			}
		}
		for key, s := range c.AgentOnly {
			p := proj(m, key)
			p.agent += s
			p.agentOnly += s
		}
	}
	out := make(map[int64]store.Bucket, len(mins))
	for start, m := range mins {
		if m.halves == 0 && m.agent == 0 {
			continue
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
			if len(pm.Credited) == 0 && pm.AgentS == 0 {
				continue
			}
			if b.Projects == nil {
				b.Projects = map[string]store.ProjectMinute{}
			}
			b.Projects[key] = pm
		}
		out[start] = b
	}
	return out
}

// sideDoorLabels are the labels a bucket slot carries, in the order
// pkg/event checks (DESIGN 4.1 E-6).
var sideDoorLabels = []event.Label{event.LabelViaTrack, event.LabelViaPlugin, event.LabelViaBridge}

// slotSummary sums one slot's credit up the way SM 5.1's slots[4] does:
// credited seconds, and the project, reason, activity and level of the
// largest credit entry. Part of the WP-SC4 seam.
func slotSummary(c score.Credit) event.BucketSlot {
	var s event.BucketSlot
	h := c.Halves()
	if h == 0 || len(c.Credited) == 0 {
		return s
	}
	best := 0
	for i, e := range c.Credited {
		if e.Halves > c.Credited[best].Halves {
			best = i
		}
	}
	d := c.Credited[best]
	s.Project, s.Credited, s.Why, s.Kind, s.Ev = d.ProjectKey, float64(h)/2, d.Reason, d.Reason.Activity(), d.Level
	for _, l := range sideDoorLabels {
		for _, e := range c.Credited {
			if e.Halves > 0 && slices.Contains(e.Labels, l) {
				s.Lbl = append(s.Lbl, l)
				break
			}
		}
	}
	return s
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
