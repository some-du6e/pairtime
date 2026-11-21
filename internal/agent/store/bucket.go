// SPDX-License-Identifier: AGPL-3.0-or-later

// Package store keeps what the scorer derives on the device (DESIGN 3.5):
// minute buckets with their revisions in buckets/YYYY-MM-DD.jsonl, one
// line per revision, and day rollups in days/YYYY-MM-DD.json, written by
// atomic rename. Everything in it can be rebuilt from the ledger, which is
// what the scorer loop reads (internal/agent/scorer).
//
// The bucket and rollup shapes here stand in for the ones WP-SC4 builds in
// pkg/score (SM 5.1, 5.3). Each is marked where it does, so wiring SC4's
// in changes those functions and not their callers.
package store

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
)

// Bucket is one revision of a minute bucket as the device keeps it: SM
// 5.1's record for one device and UTC minute in which anything was
// credited or an agent was running, with two differences. It has no
// witness label, because only the server's receipt for the batch decides
// that (DESIGN 7.4), so the uploader adds it (MinuteBucket). And it has
// Projects, the exact split of the minute's credit and agent time by
// project, reason and level, which the four slot summaries round away: a
// slot names one project and one level even when its seconds went to two.
// Day rollups and the local API sum Projects.
type Bucket struct {
	// V is the scoring version, pkg/score's VERSION, and Params the hash
	// of the parameter table the scorer ran with (SM 5.1, 7.6). A refit
	// adds a table and leaves the version, and a rule change bumps the
	// version and may keep the table (DESIGN 6.6), so neither stands for
	// the other.
	V      string    `json:"v"`
	Params string    `json:"params"`
	Minute time.Time `json:"minute"`
	Device string    `json:"device"`
	// Slots holds the minute's four slots, in order, as SM 5.1 sums them
	// up for tier 1.
	Slots     []event.BucketSlot `json:"slots"`
	CreditedS float64            `json:"credited_s"`
	// Uncredited holds the seconds of the minute that were looked at and
	// not credited, by reason.
	Uncredited    map[event.Reason]float64 `json:"uncredited,omitempty"`
	AgentRunningS float64                  `json:"agent_running_s"`
	// Anchors are the event ids whose windows covered credited seconds.
	// They stay on the device (DESIGN 4.1 E-5).
	Anchors []string          `json:"anchors,omitempty"`
	State   event.BucketState `json:"state"`
	// Rev numbers the minute's revisions from 0 (DESIGN 3.7, 6.2 D1).
	// Buckets.Put sets it.
	Rev uint32 `json:"rev"`
	// Batch is the ledger batch whose Step wrote the revision. A scorer
	// that scores batches again, after a restart or a lost state.json,
	// skips a minute whose latest revision came from a later batch, so
	// it writes no revision twice. It stays on the device.
	Batch uint64 `json:"batch"`
	// Projects splits the minute by project key. It stays on the device.
	Projects map[string]ProjectMinute `json:"projects,omitempty"`
}

// ProjectMinute is one project's part of a minute.
type ProjectMinute struct {
	// Credited holds the project's credit by reason and evidence level,
	// ordered by reason, then level.
	Credited []Credited `json:"credited,omitempty"`
	// AgentS is how many of the minute's seconds a turn of the project was
	// running, and AgentOnlyS how many of those the project wasn't
	// credited: SM 5.3's agent-only time, whatever the seconds' reason code.
	AgentS     int64 `json:"agent_s,omitempty"`
	AgentOnlyS int64 `json:"agent_only_s,omitempty"`
	// Harnesses splits AgentOnlyS by the harness whose turn ran, and names
	// every harness that had a session in the project in the minute, with
	// 0 for one that had no agent-only time, so a view can name the agent
	// whose answers were read.
	Harnesses map[event.Harness]int64 `json:"harnesses,omitempty"`
	// Uncredited holds the minute's uncredited seconds that belong to the
	// project, by reason (SM 5.2). Each slot's go to the project its focus
	// resolves to, else to the one whose agent or credit has most of it.
	Uncredited map[event.Reason]int64 `json:"uncredited,omitempty"`
}

// Credited is a project's credit in one minute under one reason at one
// evidence level.
type Credited struct {
	Why event.Reason   `json:"why"`
	Ev  event.Evidence `json:"ev"`
	// Halves is the credit in half-seconds after weighting, so a sum of
	// them is exact.
	Halves int64 `json:"halves"`
}

// At returns the start of the bucket's minute in Unix milliseconds.
func (b Bucket) At() int64 { return b.Minute.UnixMilli() }

// Halves returns the minute's credited half-seconds, summed over its
// projects.
func (b Bucket) Halves() int64 {
	var n int64
	for _, p := range b.Projects {
		for _, c := range p.Credited {
			n += c.Halves
		}
	}
	return n
}

// Empty reports whether nothing was credited in the minute and no agent
// ran in it: a minute SM 5.1 has no bucket for. A pending minute that
// turns out empty still gets an empty revision, so it supersedes the one
// before.
func (b Bucket) Empty() bool { return b.CreditedS == 0 && b.AgentRunningS == 0 }

// MinuteBucket returns the bucket as SM 5.1's record with the witness
// label w, the form the uploader passes through Shareable (DESIGN 4.3).
// Backfilled credit is level C whatever the scorer computed (DESIGN 6.4,
// 7.4), so in a backfilled minute every credited slot says C.
func (b Bucket) MinuteBucket(w event.Witness) event.MinuteBucket {
	m := event.MinuteBucket{
		V: b.V, Params: b.Params, Minute: b.Minute, Device: b.Device,
		Slots: slices.Clone(b.Slots), CreditedS: b.CreditedS, Uncredited: maps.Clone(b.Uncredited),
		AgentRunningS: b.AgentRunningS, AgentOnly: b.agentOnly(), Anchors: slices.Clone(b.Anchors),
		State: b.State, Witness: w, Rev: b.Rev,
	}
	if w == event.Backfilled {
		for i := range m.Slots {
			if m.Slots[i].Credited > 0 {
				m.Slots[i].Ev = event.EvidenceC
			}
		}
	}
	return m
}

// agentOnly returns the minute's agent-only seconds by project, as the
// tier-1 bucket carries them (D-bucket-agent-only): the AgentOnlyS of each
// project that has any, and nil when none has. AgentOnlyS is the same
// count, the seconds a turn of the project ran that it wasn't credited.
func (b Bucket) agentOnly() map[string]float64 {
	var out map[string]float64
	for key, p := range b.Projects {
		if p.AgentOnlyS > 0 {
			if out == nil {
				out = map[string]float64{}
			}
			out[key] = float64(p.AgentOnlyS)
		}
	}
	return out
}

// sameContent reports whether a and b say the same, apart from their rev
// and the batch that wrote them. It compares their JSON, which writes maps
// in key order, so it is exact and needs no list of fields.
func sameContent(a, b Bucket) bool {
	a.Rev, b.Rev = 0, 0
	a.Batch, b.Batch = 0, 0
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}
