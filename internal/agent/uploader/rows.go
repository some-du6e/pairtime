// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"fmt"
	"sort"

	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/pkg/event"
	pkgledger "github.com/some-du6e/pairtime/pkg/ledger"
)

// rowTime is what the uploader keeps of every sealed batch to read the
// witness labels: when it was sealed and when the server received it.
type rowTime struct {
	sealed int64 // Unix ms
	// sealedMax is the latest seal of this batch and every one before it,
	// so the first batch sealed at or after a time can be found by binary
	// search even where the clock went back between seals.
	sealedMax int64
	// received is the receipt's received_at, in Unix ms, 0 without one.
	received int64
	// unchecked is set when received comes from a receipt the uploader
	// couldn't check, which batches.seg doesn't keep.
	unchecked bool
}

// rowIndex holds the device's chain as the uploader needs it: the times of
// every batch, and the rows of the batches from first on, whose roots may
// still have to go. It isn't safe for concurrent use.
type rowIndex struct {
	device string // the chain's device id
	// created is when the chain began, in Unix ms, 0 when unknown.
	created int64
	times   []rowTime // times[i] is batch i + 1
	first   uint64    // queue[0]'s batch, when queue isn't empty
	queue   []ledger.Row
	// lastHead is the newest batch's H_b, the prev of the next one.
	lastHead pkgledger.Hash
}

// loadRows reads the chain in the ledger directory dir and keeps the rows
// of the batches from keep on.
func loadRows(dir string, keep uint64) (*rowIndex, error) {
	info, rows, err := ledger.ReadRows(dir, 0)
	if err != nil {
		return nil, err
	}
	ix := &rowIndex{device: info.DeviceID, created: info.Created.UnixMilli()}
	for _, r := range rows {
		if err := ix.add(r); err != nil {
			return nil, err
		}
	}
	ix.trim(keep)
	return ix, nil
}

// last returns the number of the newest batch the index holds.
func (ix *rowIndex) last() uint64 { return uint64(len(ix.times)) }

// add takes the row of the batch after the newest one the index holds.
func (ix *rowIndex) add(r ledger.Row) error {
	if r.B != ix.last()+1 {
		return fmt.Errorf("uploader: batch %d doesn't follow batch %d", r.B, ix.last())
	}
	t := rowTime{sealed: r.SealedAt.UnixMilli()}
	t.sealedMax = t.sealed
	if n := len(ix.times); n > 0 {
		t.sealedMax = max(t.sealed, ix.times[n-1].sealedMax)
	}
	if r.Receipt != nil {
		t.received = int64(r.Receipt.ReceivedAt)
	}
	ix.times = append(ix.times, t)
	if len(ix.queue) == 0 {
		ix.first = r.B
	}
	ix.queue = append(ix.queue, r)
	ix.lastHead = r.Head
	return nil
}

// trim forgets the rows of the batches before keep, whose roots went.
func (ix *rowIndex) trim(keep uint64) {
	if len(ix.queue) == 0 || keep <= ix.first {
		return
	}
	drop := min(keep-ix.first, uint64(len(ix.queue)))
	ix.queue = append([]ledger.Row(nil), ix.queue[drop:]...)
	ix.first += drop
}

// holds reports whether the index still has the rows from batch from on,
// so a resend from there doesn't need the ledger read again.
func (ix *rowIndex) holds(from uint64) bool {
	return from > ix.last() || (len(ix.queue) > 0 && from >= ix.first)
}

// rowsFrom returns up to n rows from batch from on.
func (ix *rowIndex) rowsFrom(from uint64, n int) []ledger.Row {
	if len(ix.queue) == 0 || from < ix.first || from > ix.last() {
		return nil
	}
	rows := ix.queue[from-ix.first:]
	return rows[:min(n, len(rows))]
}

// holdsRow reports whether the device has a batch b, whether or not the
// index still holds its row.
func (ix *rowIndex) holdsRow(b uint64) bool { return b >= 1 && b <= ix.last() }

// row returns batch b's row while the index holds it.
func (ix *rowIndex) row(b uint64) (ledger.Row, bool) {
	if len(ix.queue) == 0 || b < ix.first || b > ix.last() {
		return ledger.Row{}, false
	}
	return ix.queue[b-ix.first], true
}

// received records batch b's receipt time, in Unix ms.
func (ix *rowIndex) received(b uint64, at int64) {
	if b >= 1 && b <= ix.last() {
		ix.times[b-1].received, ix.times[b-1].unchecked = at, false
	}
}

// receivedUnchecked records the received_at of a receipt for batch b the
// uploader couldn't check, which reads its minutes until a checked one
// comes.
func (ix *rowIndex) receivedUnchecked(b uint64, at int64) {
	if b >= 1 && b <= ix.last() && ix.times[b-1].received == 0 {
		ix.times[b-1].received, ix.times[b-1].unchecked = at, true
	}
}

// unchecked reports whether batch b's receipt time is from a receipt the
// uploader couldn't check.
func (ix *rowIndex) unchecked(b uint64) bool {
	return b >= 1 && b <= ix.last() && ix.times[b-1].unchecked
}

// forgetReceipts forgets every receipt time: they came from another
// server, which says nothing about when this one has the batches.
func (ix *rowIndex) forgetReceipts() {
	for i := range ix.times {
		ix.times[i].received, ix.times[i].unchecked = 0, false
	}
}

// receivedAt returns when the server received batch b, by its receipt, and
// false while the device holds none.
func (ix *rowIndex) receivedAt(b uint64) (int64, bool) {
	if b < 1 || b > ix.last() || ix.times[b-1].received == 0 {
		return 0, false
	}
	return ix.times[b-1].received, true
}

// sealedAfter returns the first batch sealed at or after t, in Unix ms,
// and false when none is yet.
func (ix *rowIndex) sealedAfter(t int64) (uint64, bool) {
	i := sort.Search(len(ix.times), func(i int) bool { return ix.times[i].sealedMax >= t })
	if i == len(ix.times) {
		return 0, false
	}
	return uint64(i) + 1, true
}

// reading is what a minute's witness reading rests on besides the rows:
// where the server's chain starts and where it forked from the device's,
// the server's clock less the device's, and the stretches the uploader
// couldn't reach the server.
type reading struct {
	lag        int64  // WITNESS_LAG_S, in ms
	chainStart uint64 // the server's first batch, 0 when unknown
	fork       uint64 // the batch the chains part at, 0 for none
	offset     int64  // server clock less device clock, in ms
	offline    []span
}

// witnessOf reads the witness label of the minute that starts at minute,
// in Unix ms on the device's clock, as the device sees it, and false when
// it can't be read yet. The server stores the reading as sent, and its
// own label is WP-SV6's (ingest-v1 2.3.2).
//
// A minute's records are in the batches up to the first one sealed after
// the minute ended, B*, and the server received none of them later than
// B*, because it takes a chain's batches in order. So the reading is B*'s
// (DESIGN 7.4):
//
//   - witnessed when its receipt's received_at, on the server's clock,
//     is at most lag after the minute's end, put on the server's clock
//     by the offset the answers measured;
//   - offline when it was late, but B* was sealed within lag of the end
//     and the uploader's failure notes fall within lag after that seal:
//     it tried to send B* in time and couldn't reach the server;
//   - backfilled otherwise. That takes in every minute logged while
//     nothing was sent (signed out, local-only mode, asleep), which a
//     failure during the catch-up after doesn't change, and every minute
//     whose B* the server can never receive: before its chain starts, or
//     at or after a fork.
//
// Lookback's minutes are backfilled whatever their batch (D11; DESIGN
// 7.4): every minute that ended before the device's chain began is one,
// since nothing recorded it live, and the rest were sealed long after
// they happened. The reading waits while no batch has been sealed after
// the minute, or B* has no receipt yet, so it rests on what the device
// keeps and is the same whenever it is read: `pairtime privacy show`
// gives the bytes the upload sends.
//
// A revision written after B*, by a later batch's records, can rest on
// those records too: a prompt whose batch waited out an outage credits
// the minutes its lead reaches back into. So a revision also reads batch,
// the one its credit rests on (the planner passes the batch whose scoring
// changed the credit last, sentRev), by when the server received it after
// it was sealed, and takes the worse of the two readings.
func witnessOf(ix *rowIndex, minute int64, batch uint64, r reading) (event.Witness, bool) {
	end := minute + 60_000
	if ix.created > 0 && end <= ix.created {
		return event.Backfilled, true
	}
	b, ok := ix.sealedAfter(end)
	if !ok {
		return "", false
	}
	w, ok := r.of(ix, b, end)
	if !ok || batch <= b || batch > ix.last() {
		return w, ok
	}
	wb, ok := r.of(ix, batch, ix.times[batch-1].sealed)
	if !ok {
		return "", false
	}
	return worse(w, wb), true
}

// of reads batch b's witness label for records of the time due, on the
// device's clock: witnessed when the server received it within lag after
// due, offline when it was sealed within lag of due and waited out an
// outage, backfilled otherwise, and backfilled for a batch the server can
// never receive. It waits while b has no receipt.
func (r reading) of(ix *rowIndex, b uint64, due int64) (event.Witness, bool) {
	if (r.chainStart > 0 && b < r.chainStart) || (r.fork > 0 && b >= r.fork) {
		return event.Backfilled, true
	}
	got, ok := ix.receivedAt(b)
	if !ok {
		if r.fork > 0 {
			// No root goes after a fork, so no receipt comes for it.
			return event.Backfilled, true
		}
		return "", false
	}
	sealed := ix.times[b-1].sealed
	switch {
	case got-(due+r.offset) <= r.lag:
		return event.Witnessed, true
	case sealed-due <= r.lag && waitedOut(r.offline, sealed, r.lag):
		return event.Offline, true
	}
	return event.Backfilled, true
}

// worse returns the weaker of two witness labels: backfilled, then
// offline, then witnessed.
func worse(a, b event.Witness) event.Witness {
	rank := func(w event.Witness) int {
		switch w {
		case event.Witnessed:
			return 0
		case event.Offline:
			return 1
		}
		return 2
	}
	if rank(b) > rank(a) {
		return b
	}
	return a
}
