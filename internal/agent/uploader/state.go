// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"maps"
	"slices"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/store"
	"github.com/some-du6e/pairtime/pkg/event"
)

// StateKey is the uploader's key in state.json: DESIGN 3.5's upload
// cursor, the request counter and what the labels and failure notes need.
const StateKey = "uploader"

// counterBlock is how many request counters the uploader reserves with
// one write of state.json. The counter has to keep rising across
// restarts (DESIGN 4.5), so a value is only used once a reservation that
// covers it is on disk, and the next start goes on above the reservation:
// one write per counterBlock requests rather than one per request, which
// suits a spinning disk. The server only asks for a higher counter each
// time, so the values a restart skips cost nothing.
const counterBlock = 64

// offlineSpans bounds the offline spans the uploader remembers. Each is
// one stretch of failed uploads; an older one only ever matters to minutes
// that were already labelled.
const offlineSpans = 256

// uncheckedMax bounds the receipts kept unchecked; past it the oldest go.
const uncheckedMax = 4096

// harnessesMax is how many harnesses the labels carry (ingest-v1 2.3.4).
const harnessesMax = 16

// saved is what the uploader keeps in state.json under StateKey.
// Everything in it can be rebuilt: from GET /api/v1/ingest/state, which
// gives the counter and the server's head, and from the ledger and the
// bucket store, which hold every item. A lost state.json sends the
// buckets again, which the server answers as duplicates.
type saved struct {
	// Chain is the device id of the ledger chain the cursor belongs to. A
	// new chain, which a new K_dev starts, starts the cursor afresh.
	Chain string `json:"chain"`
	// Server and Device are where the uploads go and the device id the
	// server knows: the counter and the head are theirs, so another server
	// or another sign-in starts them afresh.
	Server string `json:"server,omitempty"`
	Device string `json:"device,omitempty"`
	// Req is the counter reservation: the requests so far used values at
	// or below it, and the next one uses more.
	Req uint64 `json:"req"`
	// Next is the batch whose root goes next, from 1. Rows before it hold
	// receipts, or are before ChainStart.
	Next uint64 `json:"next,omitempty"`
	// From is the first batch sealed after the sign-in. The batches before
	// it were sealed in local-only mode: none of them goes, nor the buckets
	// of the minutes they hold (ingest-v1 5.3, 7.6). 0 until the first
	// load with this sign-in sets it.
	From uint64 `json:"from,omitempty"`
	// ChainStart is the first batch of the device's witnessed chain on the
	// server, 0 while the server holds none or it isn't known yet. Rows
	// before it are never sent again: the server answers them
	// before_chain.
	ChainStart uint64 `json:"chain_start,omitempty"`
	// Gone is the highest batch the server answered before_chain for: it
	// holds no row for it any more, so no receipt comes for it or for the
	// batches before it, and their minutes read backfilled.
	Gone uint64 `json:"gone,omitempty"`
	// Fork is the batch the server answered batch_conflict for: the
	// device's ledger and the server's witness rows disagree there, which
	// is a bug or tampering, and the uploader sends no batch root after it
	// (ingest-v1 5 step 3). 0 while there is none.
	Fork uint64 `json:"fork,omitempty"`
	// Marks says how far each day's bucket file was uploaded, by day.
	Marks map[string]store.Mark `json:"marks,omitempty"`
	// Labels is the hash of the labels the server last took, and LabelsAt
	// when, in Unix ms.
	Labels   string `json:"labels,omitempty"`
	LabelsAt int64  `json:"labels_at,omitempty"`
	// Harnesses are the harnesses the ledger's records name, each with the
	// version it last named, for the labels.
	Harnesses map[event.Harness]string `json:"harnesses,omitempty"`
	// Offline are the stretches the uploader couldn't reach the server, as
	// its upload-failure notes say, oldest first.
	Offline []span `json:"offline,omitempty"`
	// Sent maps each minute still pending on the server, by its start in
	// Unix ms, to what its last revision's credit rests on (sentRev).
	Sent map[int64]sentRev `json:"sent,omitempty"`
	// Bump is what a minute's revs go up by on the way out, by its start in
	// Unix ms, for a minute the server holds a higher or another revision
	// of than the bucket store does, as after the store lost its last lines:
	// a minute's rev only rises (ingest-v1 5.5).
	Bump map[int64]uint32 `json:"bump,omitempty"`
	// Offset is the server's clock less the device's, in ms, as the last
	// answer measured: what X-Pairtime-Time is corrected by, and how far
	// ahead of the server's clock a minute may be before it waits.
	Offset int64 `json:"offset_ms,omitempty"`
	// Foreign is the newest batch whose batches.seg receipt may be another
	// server's, after a sign-in to this one: the receipts of the batches
	// from Next to it are forgotten whenever the rows are read, until this
	// server's own come in. 0 when there was no switch.
	Foreign uint64 `json:"foreign,omitempty"`
	// Unchecked are receipts the uploader couldn't check yet, because they
	// name a server key it doesn't have, by batch. They read the batches'
	// minutes, and go into batches.seg once their key checks them
	// (ingest-v1 5.4).
	Unchecked map[uint64]WireReceipt `json:"unchecked,omitempty"`
	// ServerKey is the receipt key GET /api/v1/ingest/state gave.
	ServerKey *ServerKey `json:"server_key,omitempty"`
	// Outbox is the request a retry sends again, when one is waiting.
	Outbox *outbox `json:"outbox,omitempty"`
}

// sentRev is what a minute's last revision sent rests on: a hash of its
// credit, and the batch whose reading it took. A later revision that
// changes no credit, as the flip from pending to final, keeps that batch's
// reading rather than take its own, whose batch may have come late for
// reasons that have nothing to do with the minute (DESIGN 7.4).
type sentRev struct {
	H string `json:"h"`
	B uint64 `json:"b"`
}

// span is a stretch of failed uploads: the first and the last failure's
// upload-failure note, in Unix ms.
type span struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

// outbox is a request that failed and is waiting for its retry: a retry
// sends the same body with the same Idempotency-Key (ingest-v1 2.10), so
// the body waits in outbox.json beside state.json, and this says what its
// answer moves.
type outbox struct {
	Key string `json:"key"`
	// Hash is the SHA-256 of outbox.json's JSON, in hex, so a damaged file
	// is noticed and a new request built instead.
	Hash string `json:"hash"`
	// Marks are the bucket marks once its buckets are answered, Sent what
	// their credit rests on, and Labels the labels' hash when it carries
	// them.
	Marks  map[string]store.Mark `json:"marks,omitempty"`
	Sent   map[int64]sentRev     `json:"sent,omitempty"`
	Labels string                `json:"labels,omitempty"`
}

// clone returns a copy of s that shares nothing with it.
func (s saved) clone() saved {
	s.Marks = maps.Clone(s.Marks)
	s.Harnesses = maps.Clone(s.Harnesses)
	s.Offline = slices.Clone(s.Offline)
	s.Sent = maps.Clone(s.Sent)
	s.Bump = maps.Clone(s.Bump)
	s.Unchecked = maps.Clone(s.Unchecked)
	if s.ServerKey != nil {
		k := *s.ServerKey
		s.ServerKey = &k
	}
	if s.Outbox != nil {
		o := *s.Outbox
		o.Marks = maps.Clone(o.Marks)
		o.Sent = maps.Clone(o.Sent)
		s.Outbox = &o
	}
	return s
}

// spanGap is the longest wait between two failures of one outage: the
// longest Retry-After the uploader keeps, and a wake. A longer gap, as
// while the machine slept or the agent was stopped, ends the span: the
// uploader wasn't trying then.
const spanGap = int64((retryAfterMax + Every) / time.Millisecond)

// noteFailure adds a failure at t, in Unix ms, after which the uploader
// waits until until to try again, to the offline spans: the server was out
// of reach for all of it, as when it sent a Retry-After. It stretches the
// last span when the failure follows it with no success in between (open)
// and within spanGap, and starts a new one otherwise.
func (s *saved) noteFailure(t, until int64, open bool) {
	until = max(until, t)
	if n := len(s.Offline); open && n > 0 && t >= s.Offline[n-1].From && t-s.Offline[n-1].To <= spanGap {
		s.Offline[n-1].To = max(s.Offline[n-1].To, until)
		return
	}
	s.Offline = append(s.Offline, span{From: t, To: until})
	if len(s.Offline) > offlineSpans {
		s.Offline = slices.Clone(s.Offline[len(s.Offline)-offlineSpans:])
	}
}

// waitedOut reports whether a batch sealed at sealed, on the device's
// clock in Unix ms, was due to go while the uploader couldn't reach the
// server: a span of failures, or the wait after one, falls in the batch's
// witness window, the lag after its seal. Then its late receipt reads
// offline whatever the flush delay, as the teen who syncs at school the
// next morning gets (DESIGN 6.2 D6, 7.4).
func waitedOut(spans []span, sealed, lag int64) bool {
	for _, s := range spans {
		if s.From <= sealed+lag && s.To >= sealed {
			return true
		}
	}
	return false
}

// prepare puts what st says of the device's receipts into rows: it forgets
// the receipts that may be another server's (Foreign) and reads the
// unchecked ones. The uploader and `pairtime privacy show` both call it
// after reading the rows, so they read the same minutes.
func (st saved) prepare(rows *rowIndex) {
	if st.Foreign > 0 {
		for b := max(st.Next, 1); b <= min(st.Foreign, rows.last()); b++ {
			rows.times[b-1].received, rows.times[b-1].unchecked = 0, false
		}
	}
	for b, wr := range st.Unchecked {
		rows.receivedUnchecked(b, int64(wr.ReceivedAt))
	}
}
