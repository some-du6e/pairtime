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
// it. Its slots and agentOnly name projects by project id, except in the
// row minuteOf makes from a bucket, which names them by the device's
// project keys until toProjects turns them into ids.
type device struct {
	id      string
	rev     int64
	pending bool
	slots   [slotsPerMinute]slot
	// agentOnly is the agent's time on its own, in milliseconds by
	// project, as the bucket's agent_only says; nil when it says none.
	agentOnly map[string]int64
	// day is the local day the minute counts toward.
	day string
}

// slot is a slot's credit, in rollup_minutes' JSON. p is the project id.
type slot struct {
	Project string `json:"p,omitempty"`
	MS      int64  `json:"ms,omitempty"`
	Kind    string `json:"k,omitempty"`
	Level   string `json:"ev,omitempty"`
}

// minuteOf checks b and turns it into the device row Apply stores. Seconds
// become whole milliseconds: credit and agent_only move in steps of 0.5 s,
// so nothing rounds away.
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
	d := device{id: b.Device, rev: int64(b.Rev), pending: b.State == "pending"}
	for p, s := range b.AgentOnly {
		switch {
		case p == "":
			return bad("agent_only gives seconds to no project")
		case !finite(s) || s < 0 || s > 60 || s*2 != math.Trunc(s*2):
			return bad("agent_only gives %s %v seconds, not 0 to 60 in steps of 0.5", p, s)
		case s > 0:
			if d.agentOnly == nil {
				d.agentOnly = make(map[string]int64, len(b.AgentOnly))
			}
			d.agentOnly[p] = int64(math.Round(s * 1000))
		}
	}
	for i, s := range b.Slots {
		cr := s.CreditedS
		if !finite(cr) || cr < 0 || cr > 15 || cr*2 != math.Trunc(cr*2) {
			return bad("slot %d credits %v seconds, not 0 to 15 in steps of 0.5", i, cr)
		}
		if cr == 0 {
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
	// agent is the agent's time on its own that counts, by project, in
	// project id order.
	agent []share
}

// share is the agent's milliseconds on its own in one project, and
// whether the bucket they came from is pending.
type share struct {
	project string
	ms      int64
	pending bool
}

// merge merges devs' revisions of one minute. In each slot the device
// with the most credited seconds wins, then the one whose credit is
// keyboard-backed (level A, input_keys), then the lower device_id, and the
// others are dropped (SM 3.4). Two devices crediting a slot is SM 3.4's
// concurrent_devices, which nothing stores (D-no-ingest-flags):
// ConcurrentMinutes counts it for a submission's windows (concurrentIn).
//
// The agent's time on its own is wall-clock time too. In each project the
// device that reports the most of it counts, the lower device_id on a tie,
// and the others don't add to it. It never takes more than the part of the
// minute that the merged credit in that project leaves: a second another
// device credited to the project is covered by a person, so it isn't the
// agent's alone (SM 2.6). Projects are project ids, so this crosses
// devices only for keys that project registration (WP-SV10) has linked to
// one id (D-account-project-id).
func merge(devs []device) merged {
	m, _ := mergeWon(devs)
	return m
}

// mergeWon is merge, and also says which of devs won each slot: its index
// in devs, or -1 where none credits the slot. The public read keeps only
// the slots that revisions the server labeled witnessed or offline won
// (PublicCreditAsOf).
func mergeWon(devs []device) (merged, [slotsPerMinute]int) {
	var m merged
	var won [slotsPerMinute]int
	for i := range slotsPerMinute {
		won[i] = -1
		var win *device
		for j := range devs {
			d := &devs[j]
			if d.slots[i].MS == 0 {
				continue
			}
			if win == nil || beats(d, win, i) {
				win = d
				won[i] = j
			}
		}
		if win != nil {
			m.slots[i].slot = win.slots[i]
			m.slots[i].pending = win.pending
		}
	}
	credited := make(map[string]int64)
	for _, s := range m.slots {
		credited[s.Project] += s.MS
	}
	best := make(map[string]*device)
	var projects []string
	for j := range devs {
		d := &devs[j]
		for p, ms := range d.agentOnly {
			w, ok := best[p]
			if !ok {
				projects = append(projects, p)
			}
			if !ok || ms > w.agentOnly[p] || (ms == w.agentOnly[p] && d.id < w.id) {
				best[p] = d
			}
		}
	}
	slices.Sort(projects)
	for _, p := range projects {
		w := best[p]
		if ms := min(w.agentOnly[p], 60_000-credited[p]); ms > 0 {
			m.agent = append(m.agent, share{project: p, ms: ms, pending: w.pending})
		}
	}
	return m, won
}

// concurrentIn reports whether two of devs, one minute's revisions, credit
// one slot and at least one of them credits it to a project in projects:
// a minute ConcurrentMinutes counts (D-sb1-submissions, Q8).
func concurrentIn(devs []device, projects map[string]bool) bool {
	for i := range slotsPerMinute {
		crediting, ours := 0, false
		for j := range devs {
			s := devs[j].slots[i]
			if s.MS == 0 {
				continue
			}
			crediting++
			ours = ours || projects[s.Project]
		}
		if crediting > 1 && ours {
			return true
		}
	}
	return false
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

// dayKey is a rollups_daily row's key, within an account.
type dayKey struct{ project, day, kind string }

// columns are a rollups_daily row's values, in its column order:
// final_a_ms, final_b_ms, final_c_ms, pending_a_ms, pending_b_ms,
// pending_c_ms, agent_only_final_ms, agent_only_pending_ms, day_cap_ms.
type columns [9]int64

// The index of the agent's final and pending columns, and of the credit
// the day cap withheld (SM 5.3's X.day_cap).
const (
	agentFinal   = 6
	agentPending = 7
	dayCap       = 8
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
		if a.pending {
			c[agentPending] += a.ms
		} else {
			c[agentFinal] += a.ms
		}
		out[k] = c
	}
	return out
}

// add adds o to d.
func (d delta) add(o delta) {
	for k, oc := range o {
		c := d[k]
		for i := range c {
			c[i] += oc[i]
		}
		d[k] = c
	}
}

// credit is m's credited milliseconds, every slot's at every level.
func (m merged) credit() int64 {
	var n int64
	for _, s := range m.slots {
		n += s.MS
	}
	return n
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
