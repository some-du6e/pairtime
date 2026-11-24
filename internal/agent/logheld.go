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
// heldLogs, counting from the newest batch an earlier run sealed
// (ledger.Writer.Opened), the run before's last seal (heldLogsFrom), and
// every batch after it, such as the one Open sealed from what a kill left
// (DESIGN 3.6). A crash's re-read of the lines taken since state.json's
// last save, or a restart's resume, reads again what the run before took,
// whenever this run starts, so the window counts back from that seal and
// not from now. A record
// written longer before it that a Rewind or a relocate makes again goes to
// the ledger a second time, where pkg/score drops it for the one it holds
// if that one is still in its state, and a minute it already made final
// stays final. A log reader's line dated before the chain began that a
// replay brings back from beyond the window is judged by when the writer
// takes it in (ledger.Writer.lookback), as its first copy was.
const heldLogsWindow = 48 * time.Hour

// heldLogsMaxBatches bounds the batches up to the run before's last seal
// that heldLogs reads: about three and a half times the 2880 that 48 hours
// hold at a seal a minute (BATCH_S), so a busy window fits, while a wall
// clock that stepped between the seals can't make the agent read the whole
// ledger (heldLogsFrom).
const heldLogsMaxBatches = 10_000

// heldLogsPrune is how often heldLogs forgets the records this run wrote
// more than heldLogsWindow ago.
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
// never meet a log record's. Dropped ids stay for the run's life, as they
// are few, but in memory only, so a re-read after a restart reaches the
// writer, which drops the record and counts it again. It belongs to the
// adapters' worker, which alone writes log records.
type heldLogs struct {
	at      map[[16]byte]int64 // id → when this run wrote it, in Unix ms, or heldForRun
	dropped map[[16]byte]struct{}
	pruned  time.Time
}

// loadHeldLogs reads the event_ids of the records of the batches the ledger
// in dir sealed from heldLogsWindow before anchor, the newest batch an
// earlier run sealed (ledger.Writer.Opened), on (heldLogsFrom), with now as
// when it last pruned. A ledger it can't read gives an empty one, and the
// error: a replay may then write a record twice, as before D-late-local
// point 5.
func loadHeldLogs(dir string, anchor uint64, now time.Time) (*heldLogs, error) {
	held := &heldLogs{at: make(map[[16]byte]int64), dropped: make(map[[16]byte]struct{}), pruned: now}
	_, rows, err := ledger.ReadRows(dir, 0)
	if err != nil {
		return held, err
	}
	from := heldLogsFrom(rows, anchor)
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
// oldest of those sealed within heldLogsWindow before the anchor, the
// batch numbered anchor, the newest an earlier run sealed
// (ledger.Writer.Opened), and then every batch after it, as Open's of what
// a kill left. The anchor goes by its number, not by this run's clock, so
// a start clock that reads behind changes nothing. Counting back from it to
// heldLogsMaxBatches, the walk passes over every batch sealed more than
// heldLogsWindow before the anchor or after it, as the wall clock that
// stamps the seals needn't rise: a batch a forward step of the clock stamped
// late, put right later, can sit between batches of the window, and when
// the run that sealed the anchor had its clock reset to an old year, every
// batch before its own is after it. So a clock that stepped between the
// seals can't make the walk read the whole ledger, or stop it short of the
// window. With no batch an earlier run sealed kept, every batch is this
// run's, and it reads them all.
func heldLogsFrom(rows []ledger.Row, anchor uint64) uint64 {
	a := -1
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].B <= anchor {
			a = i
			break
		}
	}
	if a < 0 {
		if len(rows) == 0 {
			return 0
		}
		return rows[0].B
	}
	at := rows[a].SealedAt
	since, until := at.Add(-heldLogsWindow), at.Add(heldLogsWindow)
	from := rows[a].B
	for i := a - 1; i >= 0 && a-i < heldLogsMaxBatches; i-- {
		if s := rows[i].SealedAt; s.Before(since) || s.After(until) {
			continue
		}
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
	h.dropped[idKey(id)] = struct{}{}
	h.prune(now)
}

// prune forgets, at most every heldLogsPrune, the records this run wrote
// more than heldLogsWindow before now, so a long run holds no more of them
// than the window's. Those the ledger dropped stay, so a replay of one in
// the same run counts once, however late it comes.
func (h *heldLogs) prune(now time.Time) {
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
