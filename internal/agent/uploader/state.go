// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"maps"
	"slices"

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
	// ChainStart is the first batch of the device's witnessed chain on the
	// server, 0 while the server holds none or it isn't known yet. Rows
	// before it are never sent again: the server answers them
	// before_chain.
	ChainStart uint64 `json:"chain_start,omitempty"`
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
	// ServerKey is the receipt key GET /api/v1/ingest/state gave.
	ServerKey *ServerKey `json:"server_key,omitempty"`
	// Outbox is the request a retry sends again, when one is waiting.
	Outbox *outbox `json:"outbox,omitempty"`
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
	// Marks are the bucket marks once its buckets are answered, and
	// Labels the labels' hash when it carries them.
	Marks  map[string]store.Mark `json:"marks,omitempty"`
	Labels string                `json:"labels,omitempty"`
}

// clone returns a copy of s that shares nothing with it.
func (s saved) clone() saved {
	s.Marks = maps.Clone(s.Marks)
	s.Harnesses = maps.Clone(s.Harnesses)
	s.Offline = slices.Clone(s.Offline)
	if s.ServerKey != nil {
		k := *s.ServerKey
		s.ServerKey = &k
	}
	if s.Outbox != nil {
		o := *s.Outbox
		o.Marks = maps.Clone(o.Marks)
		s.Outbox = &o
	}
	return s
}

// noteFailure adds a failure at t, in Unix ms, to the offline spans: it
// stretches the last span when the failure follows it with no success in
// between (open), and starts a new one otherwise.
func (s *saved) noteFailure(t int64, open bool) {
	if n := len(s.Offline); open && n > 0 && t >= s.Offline[n-1].To {
		s.Offline[n-1].To = t
		return
	}
	s.Offline = append(s.Offline, span{From: t, To: t})
	if len(s.Offline) > offlineSpans {
		s.Offline = slices.Clone(s.Offline[len(s.Offline)-offlineSpans:])
	}
}

// offlineDuring reports whether a failure note falls in [from, to], in
// Unix ms.
func offlineDuring(spans []span, from, to int64) bool {
	for _, s := range spans {
		if s.From <= to && s.To >= from {
			return true
		}
	}
	return false
}
