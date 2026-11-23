// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/ledger"
	pkgledger "github.com/some-du6e/pairtime/pkg/ledger"
)

// heldLogsWindow is how far back the agent reads what the ledger holds for
// heldLogs, by when each batch was sealed. A replay that reads a log line
// again soon after its record was written, as a crash's re-read of the
// lines since the last save of state.json or a restart's resume does,
// finds the record's event_id there. A record written longer ago that a
// Rewind or a relocate makes again goes to the ledger a second time, where
// pkg/score drops it for the one it holds if that one is still in its
// state, and a minute it already made final stays final.
const heldLogsWindow = 48 * time.Hour

// heldLogsPrune is how often heldLogs forgets the records written before
// its window.
const heldLogsPrune = time.Hour

// heldLogs is the event_ids of the records the ledger holds, as of the
// batches sealed in the last heldLogsWindow and every record the adapters
// wrote since, with when each was written (D-late-local point 5). A log
// reader's record gets its event_id from its line's file and place
// (adapterHost.logEventID), so a replay that reads the line again makes a
// record of the same event_id, which the adapters drop rather than write
// twice. It is built from the ledger, not from state.json, since a crash
// re-reads exactly the lines written after state.json's last save. It
// holds every event record's id, as a log reader's session.start a process
// tie rewrites goes to the ledger with via = os (tiedStart), and the ids
// of other records, which are random, never meet a log record's. It
// belongs to the adapters' worker, which alone writes log records.
type heldLogs struct {
	at     map[[16]byte]int64 // id → when it was written, in Unix ms
	pruned time.Time
}

// loadHeldLogs reads the event_ids of the records of the batches the
// ledger in dir sealed from heldLogsWindow before now on. A ledger it
// can't read gives an empty one, and the error: a replay may then write
// a record twice, as before part 5.
func loadHeldLogs(dir string, now time.Time) (*heldLogs, error) {
	held := &heldLogs{at: make(map[[16]byte]int64), pruned: now}
	_, rows, err := ledger.ReadRows(dir, 0)
	if err != nil {
		return held, err
	}
	since := now.Add(-heldLogsWindow)
	from := uint64(0)
	for i := len(rows) - 1; i >= 0 && !rows[i].SealedAt.Before(since); i-- {
		from = rows[i].B
	}
	if from == 0 {
		return held, nil
	}
	err = ledger.ReadBatches(dir, from, func(b ledger.Batch) error {
		at := b.SealedAt.UnixMilli()
		for _, rec := range b.Records {
			if id, ok := recordID(rec); ok {
				held.at[idKey(id)] = at
			}
		}
		return nil
	})
	return held, err
}

// recordID is the event_id of the event a ledger record holds, false for
// a record that holds none, as a checkpoint.
func recordID(rec pkgledger.Record) (string, bool) {
	if _, ok := rec.Header.RType().EventKind(); !ok {
		return "", false
	}
	var body struct {
		E struct {
			EventID string `json:"event_id"`
		} `json:"e"`
	}
	if json.Unmarshal(rec.Body, &body) != nil || body.E.EventID == "" {
		return "", false
	}
	return body.E.EventID, true
}

// idKey is an event_id as heldLogs keeps it: the UUID's 16 bytes, or, for
// an id that isn't one, the first 16 bytes of its SHA-256.
func idKey(id string) [16]byte {
	var k [16]byte
	if b, err := hex.DecodeString(strings.ReplaceAll(id, "-", "")); err == nil && len(b) == len(k) {
		copy(k[:], b)
		return k
	}
	sum := sha256.Sum256([]byte(id))
	copy(k[:], sum[:])
	return k
}

// has reports whether the ledger holds a record of event_id id.
func (h *heldLogs) has(id string) bool {
	_, ok := h.at[idKey(id)]
	return ok
}

// add notes that the adapters wrote a record of event_id id at now, and
// forgets, at most every heldLogsPrune, the records written before the
// window, so a long run holds no more than the window's.
func (h *heldLogs) add(id string, now time.Time) {
	h.at[idKey(id)] = now.UnixMilli()
	if now.Sub(h.pruned) < heldLogsPrune {
		return
	}
	h.pruned = now
	cut := now.Add(-heldLogsWindow).UnixMilli()
	for k, at := range h.at {
		if at < cut {
			delete(h.at, k)
		}
	}
}
