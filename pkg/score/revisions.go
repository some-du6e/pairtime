// SPDX-License-Identifier: AGPL-3.0-or-later

package score

import (
	"bytes"
	"cmp"
	"encoding/json"
	"maps"
	"slices"

	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/params"
)

// Revisions turns what successive Steps derive into minute bucket
// revisions (SM 5.1, DESIGN 3.7, 6.2 D1). The agent steps the scorer every
// 60 s and after each prompt or stop hook, hands each Output to Update, and
// writes and uploads the revisions it returns.
//
// A minute's bucket is pending until PENDING_WINDOW_S after the minute
// ends, then final (or provisional or restored, Minute.State). Its first
// revision has rev 0, and each Update that changes the bucket in any field,
// its state included, writes it again with rev one higher, which
// supersedes the lower one for the same device and minute. An Update that
// changes nothing writes nothing, and a final bucket is written once and
// never again. A minute in which nothing was credited and no agent ran
// gets no bucket, unless an earlier revision gave it one: then the new
// revision says the minute has nothing.
//
// Revisions holds the last revision of every minute not yet final and the
// final slots of the minutes that are only partly final, so its size is
// bounded by PENDING_WINDOW_S. It isn't safe for concurrent use.
type Revisions struct {
	table   *params.Table
	device  string
	witness func(minute int64) event.Witness
	// final holds the slots that became final, by minute, until the whole
	// minute is.
	final map[int64][]MinuteSlot
	// last holds the last revision written for each minute that isn't final
	// yet, or that Seed named.
	last map[int64]event.MinuteBucket
}

// NewRevisions returns a Revisions for the device with opaque key device,
// scored with table t. witness gives each minute's witness label, from when
// the server received the batches holding its records (DESIGN 7.4): the
// agent's own view, which the server checks against its receipts. A nil
// witness labels every minute witnessed.
func NewRevisions(t *params.Table, device string, witness func(minute int64) event.Witness) *Revisions {
	if t == nil {
		panic("score: NewRevisions needs a parameter table")
	}
	if witness == nil {
		witness = func(int64) event.Witness { return event.Witnessed }
	}
	return &Revisions{table: t, device: device, witness: witness, final: map[int64][]MinuteSlot{},
		last: map[int64]event.MinuteBucket{}}
}

// Seed tells r about revisions written before it existed, such as those in
// buckets/YYYY-MM-DD.jsonl after the agent restarts and rebuilds its State
// from the ledger. For each minute it keeps the highest rev, so the next
// revision of a minute that changed continues from there, and a minute
// rebuilt as it was written gets no new one.
func (r *Revisions) Seed(written []event.MinuteBucket) {
	for _, b := range written {
		m := b.Minute.UnixMilli()
		if prev, ok := r.last[m]; !ok || b.Rev >= prev.Rev {
			r.last[m] = b
		}
	}
}

// Revision is one new revision of a minute's bucket, and the minute in full,
// for the rollups.
type Revision struct {
	Bucket event.MinuteBucket `json:"bucket"`
	Minute Minute             `json:"minute"`
}

// Update takes the Output of the next Step and returns the revisions it
// makes, in minute order.
func (r *Revisions) Update(out Output) []Revision {
	for _, c := range out.Final.Credits {
		m := floorMinute(c.Slot)
		r.final[m] = append(r.final[m], MinuteSlot{Credit: c})
	}
	pending := map[int64][]MinuteSlot{}
	for _, c := range out.Pending.Credits {
		m := floorMinute(c.Slot)
		pending[m] = append(pending[m], MinuteSlot{Credit: c})
	}
	starts := slices.Collect(maps.Keys(r.final))
	for m := range pending {
		if _, ok := r.final[m]; !ok {
			starts = append(starts, m)
		}
	}
	slices.Sort(starts)
	var revs []Revision
	for _, start := range starts {
		slots := append(slices.Clone(r.final[start]), pending[start]...)
		slices.SortStableFunc(slots, func(a, b MinuteSlot) int { return cmp.Compare(a.Slot, b.Slot) })
		m := Minute{Start: start, Slots: slots}
		final := m.End() <= out.FinalBefore
		b := m.Bucket(BucketInfo{Table: r.table, Device: r.device, State: m.State(final), Witness: r.witness(start)})
		prev, had := r.last[start]
		if had {
			b.Rev = prev.Rev + 1
		}
		// A revision is written when the bucket changed, or when the minute
		// has something and never had a bucket.
		if changed := had && !sameBucket(prev, b) || !had && !m.empty(); changed {
			revs = append(revs, Revision{Bucket: b, Minute: m})
			r.last[start] = b
		}
		if final {
			delete(r.final, start)
			delete(r.last, start)
		}
	}
	// A seeded minute the scorer no longer derives can't change again once
	// it is past the pending window.
	for start := range r.last {
		if start+MinuteMS <= out.FinalBefore {
			delete(r.last, start)
		}
	}
	return revs
}

// sameBucket reports whether two revisions of a minute's bucket say the
// same, apart from their rev.
func sameBucket(a, b event.MinuteBucket) bool {
	a.Rev, b.Rev = 0, 0
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}
