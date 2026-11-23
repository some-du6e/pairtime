// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/ledger"
	pkgledger "github.com/some-du6e/pairtime/pkg/ledger"
)

// heldLogsWindow is how far back the agent reads what the ledger holds for
// heldLogs, counting from the newest batch sealed before this run started,
// the run before's last seal (heldLogsFrom), and every batch after it,
// such as those Open sealed from what a kill left (DESIGN 3.6). A crash's
// re-read of the lines taken since state.json's last save, or a restart's
// resume, reads again what the run before took, whenever this run starts,
// so the window counts back from that seal and not from now. A record
// written longer before it that a Rewind or a relocate makes again goes to
// the ledger a second time, where pkg/score drops it for the one it holds
// if that one is still in its state, and a minute it already made final
// stays final. A log reader's line dated before the chain began that a
// replay brings back from beyond the window is judged by when the writer
// takes it in (ledger.Writer.lookback), as its first copy was.
const heldLogsWindow = 48 * time.Hour

// heldLogsPrune is how often heldLogs forgets the records this run wrote,
// or dropped, more than heldLogsWindow ago.
const heldLogsPrune = time.Hour

// heldForRun is the time heldLogs keeps for an event_id loadHeldLogs read
// from the ledger, which stays for the run's life: the window counts back
// from the run before's last seal, so the prune, which counts back from
// now, leaves it alone.
const heldForRun = math.MaxInt64

// heldLogs is the event_ids of the log records the ledger holds, from the
// batches loadHeldLogs read and the records the adapters wrote in this run,
// and of those the ledger dropped in this run as made in a local-only
// stretch (D-late-local point 5). A log reader's record gets its event_id
// from its line's file and place (adapterHost.logEventID), so a replay that
// reads the line again makes a record of the same event_id, which the
// adapters leave out rather than write twice. It is built from the ledger,
// not from state.json, since a crash re-reads exactly the lines written
// after state.json's last save. It holds every event record's id, as a log
// reader's session.start a process tie rewrites goes to the ledger with
// via = os (tiedStart), and the ids of other records, which are random,
// never meet a log record's. Dropped ids stay in memory only, so a re-read
// after a restart reaches the writer, which drops the record and counts it
// again. It belongs to the adapters' worker, which alone writes log
// records.
type heldLogs struct {
	at      map[[16]byte]int64 // id → when this run wrote it, in Unix ms, or heldForRun
	dropped map[[16]byte]int64 // id → when the ledger dropped it in this run, in Unix ms
	pruned  time.Time
}

// loadHeldLogs reads the event_ids of the records of the batches the ledger
// in dir sealed from heldLogsWindow before the newest batch sealed before
// started on (heldLogsFrom), with now as when it last pruned. A ledger it
// can't read gives an empty one, and the error: a replay may then write a
// record twice, as before D-late-local point 5.
func loadHeldLogs(dir string, started, now time.Time) (*heldLogs, error) {
	held := &heldLogs{at: make(map[[16]byte]int64), dropped: make(map[[16]byte]int64), pruned: now}
	_, rows, err := ledger.ReadRows(dir, 0)
	if err != nil {
		return held, err
	}
	from := heldLogsFrom(rows, started)
	if from == 0 {
		return held, nil
	}
	err = ledger.ReadBatches(dir, from, func(b ledger.Batch) error {
		for _, rec := range b.Records {
			if id, ok := recordID(rec); ok {
				held.at[idKey(id)] = heldForRun
			}
		}
		return nil
	})
	return held, err
}

// heldLogsFrom is the first batch loadHeldLogs reads, 0 for none: the
// oldest sealed within heldLogsWindow before the anchor, the newest batch
// sealed before started, and every batch after it. With none sealed before
// started, as when this run's clock reads earlier than every seal, the
// anchor is the batch sealed last by the wall clock, so a clock far behind
// doesn't read the whole ledger.
func heldLogsFrom(rows []ledger.Row, started time.Time) uint64 {
	a := -1
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].SealedAt.Before(started) {
			a = i
			break
		}
	}
	if a < 0 {
		for i := range rows {
			if a < 0 || rows[i].SealedAt.After(rows[a].SealedAt) {
				a = i
			}
		}
	}
	if a < 0 {
		return 0
	}
	since := rows[a].SealedAt.Add(-heldLogsWindow)
	from := rows[a].B
	for i := a - 1; i >= 0 && !rows[i].SealedAt.Before(since); i-- {
		from = rows[i].B
	}
	return from
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

// wasDropped reports whether the ledger dropped a record of event_id id in
// this run, as made in a local-only stretch (ledger.ErrLateLocal).
func (h *heldLogs) wasDropped(id string) bool {
	_, ok := h.dropped[idKey(id)]
	return ok
}

// add notes that the adapters wrote a record of event_id id at now.
func (h *heldLogs) add(id string, now time.Time) {
	h.at[idKey(id)] = now.UnixMilli()
	h.prune(now)
}

// drop notes that the ledger dropped a record of event_id id at now.
func (h *heldLogs) drop(id string, now time.Time) {
	h.dropped[idKey(id)] = now.UnixMilli()
	h.prune(now)
}

// prune forgets, at most every heldLogsPrune, the records this run wrote or
// the ledger dropped more than heldLogsWindow before now, so a long run
// holds no more of them than the window's.
func (h *heldLogs) prune(now time.Time) {
	if now.Sub(h.pruned) < heldLogsPrune {
		return
	}
	h.pruned = now
	cut := now.Add(-heldLogsWindow).UnixMilli()
	for _, m := range []map[[16]byte]int64{h.at, h.dropped} {
		for k, at := range m {
			if at < cut {
				delete(m, k)
			}
		}
	}
}
