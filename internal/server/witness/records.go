// SPDX-License-Identifier: AGPL-3.0-or-later

package witness

import (
	"cmp"
	"math"
	"slices"

	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/ledger"
	"github.com/some-du6e/pairtime/pkg/params"
)

// FutureMS is how far after its batch reached the server a record may be
// dated before the server refuses it: SP 8.4's 5 minutes, the limit
// ingest-v1 holds X-Pairtime-Time and a bucket's minute to as well.
const FutureMS = 300_000

// Batch is a witness row the server holds for a device: batch B, and when
// the server received it, in Unix ms on its own clock.
type Batch struct {
	B          uint64
	ReceivedAt int64
}

// Record is one of a device's ledger records, as a submission reveals it.
type Record struct {
	// S is the record's sequence number, and B the batch that holds it.
	S, B uint64
	// Type is the record's rtype, from its header.
	Type ledger.RType
	// Timed is set when the record's body was revealed, and T with it.
	Timed bool
	// T is the record's wall time, in Unix ms on the device's clock: an
	// event's t, or a checkpoint's or an upload-failure note's.
	T int64
	// Mono and Boot are an event's mono and boot_id. Mono is 0 for a
	// record without one, as checkpoints and notes are.
	Mono int64
	Boot string
	// Lookback is set for a record the lookback reader read from a
	// harness's logs. The client knows it and the body doesn't say it. A
	// record so marked reads backfilled whatever its lag, so the mark can
	// only take credit away (DESIGN 7.4).
	Lookback bool
}

// Reading is what Records says of one record.
type Reading struct {
	// Label is the record's witness label, or "" when it has none: its
	// body wasn't revealed, the server holds no row for its batch, or it
	// is refused.
	Label event.Witness
	// Refused is set for a record dated more than FutureMS after its batch
	// reached the server (SP 8.4).
	Refused bool
	// Corrected is the record's corrected time, and Lag its batch's
	// received_at less that, both in ms. They are set for every timed
	// record in a batch the server holds, a refused one included.
	Corrected, Lag int64
	// Stretch is the index in Result.Stretches of an offline record's
	// stretch, and -1 for every other record.
	Stretch int
}

// Stretch is one catch-up's offline records: a run of the device's batches
// that waited out an outage and reached the server together once the
// device got through again (DESIGN 7.4). D6's OFFLINE_PUSH_S counts from
// Reconnect, and OFFLINE_CAP_S caps the offline time of all the stretches
// one submission holds (DESIGN 6.2 D6; WP-SB4 applies both).
type Stretch struct {
	// Reconnect is when the server received the catch-up's first batch, in
	// Unix ms on its clock: when the device got through again.
	Reconnect int64
	// First and Last are the first and the last batch that hold one of the
	// stretch's offline records.
	First, Last uint64
	// From and To are the earliest and the latest corrected time of its
	// offline records, in Unix ms.
	From, To int64
}

// Result is what Records returns.
type Result struct {
	// Readings answers each record, in the order given.
	Readings []Reading
	// Stretches are the offline stretches, in the order of their batches.
	Stretches []Stretch
}

// Records labels one device's records (DESIGN 7.4; the package
// documentation says how). batches are the server's witness rows of the
// device: the row of every batch whose records recs holds, and every row
// from the batch before each late record's catch-up on. Records reads a
// row it lacks as a batch the server never held. Its records get no label
// and bound no other record's time, so leaving out a row the server holds
// can make the other records of their boots read fresher than they are.
//
// recs needs the timed records of the batch before each catch-up as well,
// its os.presence, os.focus and os.power records at least (DESIGN 6.5's
// rule (g)): a late record reads offline only when that batch has one that
// reads witnessed or offline. Each record's Mono must be a reading of its
// boot's monotonic clock taken at the record's own time, or 0, because a
// reading that runs ahead of its record's time pulls every earlier record
// of its boot back. tab is the parameter table the labels go by, which a
// submission freezes (DESIGN 7.5 step 2).
func Records(tab *params.Table, batches []Batch, recs []Record) Result {
	res, _ := records(tab, batches, recs)
	return res
}

// records is Records, and also says how many steps its notes' window took
// (offline), so a test can hold the cost to the input's size.
func records(tab *params.Table, batches []Batch, recs []Record) (Result, int) {
	lag := tab.Int(params.WITNESS_LAG_S) * 1000
	got := make(map[uint64]int64, len(batches))
	for _, b := range batches {
		got[b.B] = b.ReceivedAt
	}
	out := Result{Readings: make([]Reading, len(recs))}
	for i := range out.Readings {
		out.Readings[i].Stretch = -1
	}
	// held are the records a label can go to: timed, in a batch the server
	// holds a row for.
	var held []int
	for i, r := range recs {
		if _, ok := got[r.B]; r.Timed && ok {
			held = append(held, i)
		}
	}
	corrected := correct(recs, held, got)
	for _, i := range held {
		r, rd := recs[i], &out.Readings[i]
		at := got[r.B]
		rd.Corrected, rd.Lag = corrected[i], at-corrected[i]
		switch {
		case r.T > at && r.T-at > FutureMS:
			rd.Refused = true
		case r.Lookback || rd.Lag > lag:
			// Late, or lookback's: offline is decided below.
			rd.Label = event.Backfilled
		default:
			rd.Label = event.Witnessed
		}
	}
	steps := offline(&out, lag, got, recs, held, corrected)
	return out, steps
}

// correct returns the corrected time of each record in held, by its index
// in recs: the earliest of its wall time, its batch's receipt, and, for a
// record with a monotonic reading, the receipt of any record of its boot
// whose monotonic clock read later, less the monotonic time between them.
// Every record existed when its batch reached the server, and a monotonic
// clock never runs ahead of real time, though it may stop while the
// machine sleeps, so each of those is a time the record was made by. Two
// records whose readings are equal bound nothing: a clock that stopped
// while the machine slept can read the same on both sides of the sleep.
func correct(recs []Record, held []int, got map[uint64]int64) []int64 {
	out := make([]int64, len(recs))
	boots := make(map[string][]int)
	for _, i := range held {
		r := recs[i]
		out[i] = min(r.T, got[r.B])
		if r.Mono > 0 && r.Boot != "" {
			boots[r.Boot] = append(boots[r.Boot], i)
		}
	}
	for _, idx := range boots {
		// Latest first by the monotonic clock, so least is the smallest
		// receipt less mono of every record that read later than the ones
		// at hand.
		slices.SortFunc(idx, func(a, b int) int { return cmp.Compare(recs[b].Mono, recs[a].Mono) })
		least := int64(math.MaxInt64)
		for j := 0; j < len(idx); {
			k := j
			for ; k < len(idx) && recs[idx[k]].Mono == recs[idx[j]].Mono; k++ {
				if least != math.MaxInt64 {
					out[idx[k]] = min(out[idx[k]], recs[idx[k]].Mono+least)
				}
			}
			for ; j < k; j++ {
				least = min(least, got[recs[idx[j]].B]-recs[idx[j]].Mono)
			}
		}
	}
	for _, i := range held {
		// No record was made before 1970, and a negative time could
		// overflow a lag.
		out[i] = max(out[i], 0)
	}
	return out
}

// run is a batch's place in its run of receipts: the first batch of the
// run, and whether the run began after a silence that followed a batch the
// server holds.
type run struct {
	start   uint64
	resumed bool
}

// runs returns each batch's run: the batches back from it to the first one
// received more than lag after the batch before it. A run that reaches a
// batch the server holds no row before, at the chain's start, after a
// local-only gap or before the first row given, didn't resume.
func runs(got map[uint64]int64, lag int64) map[uint64]run {
	bs := make([]uint64, 0, len(got))
	for b := range got {
		bs = append(bs, b)
	}
	slices.Sort(bs)
	out := make(map[uint64]run, len(bs))
	for _, b := range bs {
		prev, ok := got[b-1]
		switch {
		case b <= 1 || !ok:
			out[b] = run{start: b}
		case got[b]-prev > lag:
			out[b] = run{start: b, resumed: true}
		default:
			out[b] = out[b-1]
		}
	}
	return out
}

// offline gives the offline label to each late record whose batch waited
// out an outage (DESIGN 7.4), and gathers them into stretches: the
// record's batch closed within lag after it; a failure note falls within
// lag of the batch's close, before or after it, in a batch of the same
// catch-up or a later one; the batch reached the server within lag after
// its run's first batch; that run began after a silence that followed a
// batch the server holds; and that batch, the one before the catch-up,
// has a timed record that reads witnessed or offline. It returns how many
// steps the notes' window took, which is at most twice the number of
// notes.
//
// A batch closes, as far as the server can see, at the latest corrected
// time of its timed records, and no earlier than the batch before it,
// since a chain seals its batches in order. A batch before a catch-up
// that reads witnessed or offline closed in time, so every batch of the
// catch-up closed no earlier than lag before that batch reached the
// server. That keeps a batch of backdated records sent after the device
// got batches of the present through from reading offline, whatever
// records a caller leaves out. The batches are decided in order, so a
// batch before a catch-up has its labels when the catch-up needs them. A
// note may come before the close because the uploader waits between
// tries: a session that ends asleep while the uploader waits leaves its
// last batches with only the failures before them. A note in a batch from
// before the catch-up was sealed before the device last got through, so it
// belongs to a failure that had ended.
func offline(out *Result, lag int64, got map[uint64]int64, recs []Record, held []int, corrected []int64) int {
	closes := make(map[uint64]int64)
	byBatch := make(map[uint64][]int)
	type note struct {
		at int64
		b  uint64
	}
	var notes []note
	for _, i := range held {
		r := recs[i]
		if c, ok := closes[r.B]; !ok || corrected[i] > c {
			closes[r.B] = corrected[i]
		}
		byBatch[r.B] = append(byBatch[r.B], i)
		if r.Type == ledger.RTypeUploadFailure && !out.Readings[i].Refused {
			notes = append(notes, note{corrected[i], r.B})
		}
	}
	bs := make([]uint64, 0, len(closes))
	for b := range closes {
		bs = append(bs, b)
	}
	slices.Sort(bs)
	latest := int64(math.MinInt64)
	for _, b := range bs {
		latest = max(latest, closes[b])
		closes[b] = latest
	}
	slices.SortFunc(notes, func(a, b note) int { return cmp.Compare(a.at, b.at) })
	// The notes' window, closes[b] less lag to closes[b] plus lag, only
	// moves on in batch order. window holds the indices of its notes whose
	// batch is later than every note after it in the window, from head on,
	// so notes[window[head]] has the window's latest batch.
	var window []int
	head, lo, hi, steps := 0, 0, 0, 0
	rs := runs(got, lag)
	anchors := make(map[uint64]bool)  // a batch with a record that reads witnessed or offline
	stretch := make(map[uint64][]int) // run start: offline records
	for _, b := range bs {
		closed, ru := closes[b], rs[b]
		for ; hi < len(notes) && notes[hi].at <= closed+lag; hi++ {
			for len(window) > head && notes[window[len(window)-1]].b <= notes[hi].b {
				window = window[:len(window)-1]
			}
			window = append(window, hi)
			steps++
		}
		for ; lo < hi && notes[lo].at < closed-lag; lo++ {
			if len(window) > head && window[head] == lo {
				head++
			}
			steps++
		}
		waited := ru.resumed && got[b]-got[ru.start] <= lag && anchors[ru.start-1] &&
			len(window) > head && notes[window[head]].b >= ru.start
		for _, i := range byBatch[b] {
			rd := &out.Readings[i]
			if !waited || rd.Label != event.Backfilled || recs[i].Lookback || rd.Lag <= lag || closed-corrected[i] > lag {
				continue
			}
			rd.Label = event.Offline
			stretch[ru.start] = append(stretch[ru.start], i)
		}
		for _, i := range byBatch[b] {
			if l := out.Readings[i].Label; l == event.Witnessed || l == event.Offline {
				anchors[b] = true
				break
			}
		}
	}
	starts := make([]uint64, 0, len(stretch))
	for s := range stretch {
		starts = append(starts, s)
	}
	slices.Sort(starts)
	for k, s := range starts {
		st := Stretch{Reconnect: got[s], First: math.MaxUint64, From: math.MaxInt64, To: math.MinInt64}
		for _, i := range stretch[s] {
			st.First, st.Last = min(st.First, recs[i].B), max(st.Last, recs[i].B)
			st.From, st.To = min(st.From, corrected[i]), max(st.To, corrected[i])
			out.Readings[i].Stretch = k
		}
		out.Stretches = append(out.Stretches, st)
	}
	return steps
}
