// SPDX-License-Identifier: AGPL-3.0-or-later

package rollup

import (
	"cmp"
	"fmt"
	"math"
	"regexp"
	"slices"
)

// slotsPerMinute is how many 15-second slots a minute has.
const slotsPerMinute = 4

// Levels are the evidence levels, in the order rollups_daily's columns
// hold them (DESIGN 6.4).
var Levels = [3]string{"A", "B", "C"}

// states are the bucket states Apply takes (pkg/event.BucketState).
var states = []string{"pending", "final", "provisional", "restored"}

// kindShape is what an activity kind looks like: SM 5.1's names, and any
// a newer scoring version adds in the same form.
var kindShape = regexp.MustCompile(`^[a-z][a-z_]{0,31}$`)

// device is one device's revision of one minute, as rollup_minutes holds
// it.
type device struct {
	id      string
	rev     int64
	pending bool
	slots   [slotsPerMinute]slot
	agentMS int64
	// day is the local day the minute counts toward.
	day string
}

// slot is a slot's credit, in rollup_minutes' JSON.
type slot struct {
	Project string `json:"p,omitempty"`
	MS      int64  `json:"ms,omitempty"`
	Kind    string `json:"k,omitempty"`
	Level   string `json:"ev,omitempty"`
}

// minuteOf checks b and turns it into the device row Apply stores. Seconds
// become whole milliseconds: credit moves in steps of 0.5 s and agent time
// in whole seconds, so nothing rounds away.
func minuteOf(b Bucket) (device, error) {
	bad := func(format string, args ...any) (device, error) {
		return device{}, fmt.Errorf("rollup: %s's minute %d on %s: %s", b.Account, b.Minute, b.Device, fmt.Sprintf(format, args...))
	}
	switch {
	case b.Account == "":
		return bad("no account")
	case b.Device == "":
		return bad("no device")
	case b.Minute <= 0 || b.Minute%60 != 0:
		return bad("the minute doesn't start a UTC minute")
	case b.Rev < 0:
		return bad("rev %d is negative", b.Rev)
	case !slices.Contains(states, b.State):
		return bad("state %q isn't one of %q", b.State, states)
	case len(b.Slots) > slotsPerMinute:
		return bad("%d slots, more than %d", len(b.Slots), slotsPerMinute)
	case !finite(b.AgentRunningS) || b.AgentRunningS < 0 || b.AgentRunningS > 60:
		return bad("agent_running_s is %v, not 0 to 60", b.AgentRunningS)
	}
	d := device{id: b.Device, rev: int64(b.Rev), pending: b.State == "pending", agentMS: int64(math.Round(b.AgentRunningS * 1000))}
	for i, s := range b.Slots {
		cr := s.CreditedS
		if !finite(cr) || cr < 0 || cr > 15 || cr*2 != math.Trunc(cr*2) {
			return bad("slot %d credits %v seconds, not 0 to 15 in steps of 0.5", i, cr)
		}
		if cr == 0 {
			// An uncredited slot keeps only its project, which places the
			// agent's time.
			d.slots[i] = slot{Project: s.Project}
			continue
		}
		switch {
		case s.Project == "":
			return bad("slot %d credits time but names no project", i)
		case !slices.Contains(Levels[:], s.Level):
			return bad("slot %d has evidence level %q, not one of %q", i, s.Level, Levels)
		case !kindShape.MatchString(s.Kind):
			return bad("slot %d has activity kind %q", i, s.Kind)
		}
		d.slots[i] = slot{Project: s.Project, MS: int64(math.Round(cr * 1000)), Kind: s.Kind, Level: s.Level}
	}
	return d, nil
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// merged is an account's minute once its devices are merged: one human
// minute per wall-clock minute (SM 3.4).
type merged struct {
	// slots are the credit that counts in each slot, each with whether
	// the bucket it came from is pending.
	slots [slotsPerMinute]struct {
		slot
		pending bool
	}
	// agent is the agent time that counts, from the device that ran the
	// most of it, split over the projects that device's slots name.
	agent []share
	// agentPending says whether that device's bucket is pending.
	agentPending bool
	// concurrent says two devices credit the same slot.
	concurrent bool
}

// share is the agent's milliseconds in one project.
type share struct {
	project string
	ms      int64
}

// merge merges devs' revisions of one minute. In each slot the device
// with the most credited seconds wins, then the one whose credit is
// keyboard-backed (level A, input_keys), then the lower device_id, and the
// others are dropped; two devices crediting a slot raise
// concurrent_devices (SM 3.4). The agent's time is a wall-clock measure
// too, so the device that ran an agent longest counts and the others
// don't add to it.
func merge(devs []device) merged {
	var m merged
	for i := range slotsPerMinute {
		var win *device
		crediting := 0
		for j := range devs {
			d := &devs[j]
			if d.slots[i].MS == 0 {
				continue
			}
			crediting++
			if win == nil || beats(d, win, i) {
				win = d
			}
		}
		if crediting > 1 {
			m.concurrent = true
		}
		if win != nil {
			m.slots[i].slot = win.slots[i]
			m.slots[i].pending = win.pending
		}
	}
	var agent *device
	for j := range devs {
		d := &devs[j]
		if d.agentMS > 0 && (agent == nil || d.agentMS > agent.agentMS || (d.agentMS == agent.agentMS && d.id < agent.id)) {
			agent = d
		}
	}
	if agent != nil {
		m.agent = split(agent.agentMS, agent.slots)
		m.agentPending = agent.pending
	}
	return m
}

// beats reports whether d's slot i wins over w's (SM 3.4).
func beats(d, w *device, i int) bool {
	a, b := d.slots[i], w.slots[i]
	if c := cmp.Compare(a.MS, b.MS); c != 0 {
		return c > 0
	}
	if (a.Level == "A") != (b.Level == "A") {
		return a.Level == "A"
	}
	return d.id < w.id
}

// split shares ms out over the projects slots name, by how many slots name
// each, in the order they first appear; the milliseconds that don't divide
// evenly go to the first. With no project named, it all goes to "", which
// the dashboard shows as no project.
func split(ms int64, slots [slotsPerMinute]slot) []share {
	var out []share
	named := int64(0)
	for _, s := range slots {
		if s.Project == "" {
			continue
		}
		named++
		i := slices.IndexFunc(out, func(x share) bool { return x.project == s.Project })
		if i < 0 {
			out = append(out, share{project: s.Project})
			i = len(out) - 1
		}
		out[i].ms++ // counts slots until the shares are worked out
	}
	if named == 0 {
		return []share{{project: "", ms: ms}}
	}
	given := int64(0)
	for i := range out {
		n := out[i].ms
		out[i].ms = ms * n / named
		given += out[i].ms
	}
	out[0].ms += ms - given
	return out
}

// dayKey is a rollups_daily row's key, within an account.
type dayKey struct{ project, day, kind string }

// columns are a rollups_daily row's values, in its column order:
// final_a_ms, final_b_ms, final_c_ms, pending_a_ms, pending_b_ms,
// pending_c_ms, agent_final_ms, agent_pending_ms.
type columns [8]int64

// The index of the agent's final and pending columns.
const (
	agentFinal   = 6
	agentPending = 7
)

// delta is a change to an account's rollups_daily rows.
type delta map[dayKey]columns

// contribution is what m adds to the rollups when it counts toward day.
func (m merged) contribution(day string) delta {
	out := make(delta)
	for _, s := range m.slots {
		if s.MS == 0 {
			continue
		}
		col := slices.Index(Levels[:], s.Level)
		if s.pending {
			col += 3
		}
		k := dayKey{project: s.Project, day: day, kind: s.Kind}
		c := out[k]
		c[col] += s.MS
		out[k] = c
	}
	for _, a := range m.agent {
		k := dayKey{project: a.project, day: day}
		c := out[k]
		if m.agentPending {
			c[agentPending] += a.ms
		} else {
			c[agentFinal] += a.ms
		}
		out[k] = c
	}
	return out
}

// subtract takes o away from d.
func (d delta) subtract(o delta) {
	for k, oc := range o {
		c := d[k]
		for i := range c {
			c[i] -= oc[i]
		}
		d[k] = c
	}
}
