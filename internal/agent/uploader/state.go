// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"cmp"
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

// gapsMax bounds the runs of local-only batches the uploader remembers;
// past it the two oldest become one, which counts the batches between them
// as local-only too, so nothing that was local-only goes.
const gapsMax = 1024

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
	// LinkedAt is the sign-in's linked_at From was set for, in Unix ms: a
	// newer one is a re-link, after which From moves to the first batch
	// sealed after it (D-local-gap).
	LinkedAt int64 `json:"linked_at,omitempty"`
	// Gaps are the runs of batches sealed in local-only mode, merged
	// where they touch: before the first sign-in, between a logout and a
	// re-link (from the server's head then to the first batch after the
	// re-link), and while config.toml's mode was local (mode). Neither
	// their roots nor their minutes ever go (D-local-gap; DESIGN 4.2). The
	// server takes a gap only at a re-link, so the roots stop before a run
	// sealed after the chain's last link until the next one (localStop).
	Gaps []gap `json:"gaps,omitempty"`
	// LocalFrom is the first batch sealed while config.toml's mode was
	// local, while it still is: it and every batch after it count as
	// sealed in local-only mode, until a batch is sealed online again and
	// the run moves into Gaps (mode). 0 while the mode isn't local.
	LocalFrom uint64 `json:"local_from,omitempty"`
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

// gap is a run of batches, From to To, sealed in local-only mode.
type gap struct {
	From uint64 `json:"from"`
	To   uint64 `json:"to"`
}

// addGap records the batches from a to b as local-only, as one run with
// every run it overlaps or touches.
func (s *saved) addGap(a, b uint64) {
	if a == 0 || b < a {
		return
	}
	out := make([]gap, 0, len(s.Gaps)+1)
	for _, g := range s.Gaps {
		if g.To+1 < a || b+1 < g.From {
			out = append(out, g)
			continue
		}
		a, b = min(a, g.From), max(b, g.To)
	}
	out = append(out, gap{From: a, To: b})
	slices.SortFunc(out, func(x, y gap) int { return cmp.Compare(x.From, y.From) })
	for len(out) > gapsMax {
		out[1].From = out[0].From
		out = out[1:]
	}
	s.Gaps = out
}

// localOnly reports whether batch b was sealed in local-only mode: in a
// recorded gap, at or after LocalFrom while the mode is local, or before
// From where the state read that records the gap hasn't run yet, as for
// `pairtime privacy show` before an agent's first upload.
func (s saved) localOnly(b uint64) bool {
	if s.LocalFrom > 0 && b >= s.LocalFrom {
		return true
	}
	for _, g := range s.Gaps {
		if g.From <= b && b <= g.To {
			return true
		}
	}
	base := max(s.Next, 1)
	return s.From > base && b >= base && b < s.From
}

// mode records that batch b was sealed while config.toml's mode was local
// (local) or online. The first batch sealed in local mode opens a run at
// LocalFrom, and the first one sealed online after it closes the run into
// Gaps, itself included: its records began while the mode was still
// local, as the first one's began before it was. It reports whether
// anything changed.
func (s *saved) mode(b uint64, local bool) bool {
	switch {
	case b == 0:
		return false
	case local && s.LocalFrom == 0:
		s.LocalFrom = b
		return true
	case !local && s.LocalFrom > 0:
		s.addGap(s.LocalFrom, max(b, s.LocalFrom))
		s.LocalFrom = 0
		return true
	}
	return false
}

// cutLocal ends every run of local-only batches that started before from,
// the first batch sealed after a re-link, at the batch before it: a re-link
// sets the mode online (`pairtime login`), and the batches sealed after it
// went online again even when config.toml's change reached the agent a
// moment later (D-local-gap). A run that started at or after from is the
// mode set to local again after the re-link, and stays.
func (s *saved) cutLocal(from uint64) {
	if from == 0 {
		return
	}
	if s.LocalFrom > 0 && s.LocalFrom < from {
		s.addGap(s.LocalFrom, from-1)
		s.LocalFrom = 0
	}
	for i, g := range s.Gaps {
		if g.From < from && g.To >= from {
			s.Gaps[i].To = from - 1
		}
	}
}

// localStop returns the first batch, up to last, that the next batch roots
// can't pass: the first one sealed in local-only mode at or after where
// they go on from. The server takes no gap without a re-link, and none of
// those batches may go, so nothing after it goes until `pairtime logout`
// and `pairtime login` link the device again (D-local-gap). 0 when there
// is none.
func (s saved) localStop(last uint64) uint64 {
	start := max(s.Next, s.From, 1)
	stop := uint64(0)
	consider := func(b uint64) {
		if b <= last && (stop == 0 || b < stop) {
			stop = b
		}
	}
	if s.LocalFrom > 0 {
		consider(max(s.LocalFrom, start))
	}
	for _, g := range s.Gaps {
		if g.To >= start {
			consider(max(g.From, start))
		}
	}
	return stop
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
	s.Gaps = slices.Clone(s.Gaps)
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
