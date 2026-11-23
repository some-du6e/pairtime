// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/some-du6e/pairtime/internal/agent/home"
)

// Sweep drops the ledger's oldest batches, as DESIGN 4.4 keeps the device's
// ledger records and batch roots, and the presence, focus and MinHash data
// inside them, 180 days (WP-LG6; docs/errata/2026-11-20-batches-retention.md
// and 2026-11-23-device-activity-retention.md): every batch whose first
// record is in the segment of a UTC day before from, with the segments of
// those days. When every batch goes, the base frame names the one to be
// sealed next, so the chain goes on from its head.
//
// floor, when it isn't 0, is the first batch whose row stays whatever its
// age: the uploads' first batch the server hasn't stored, while this
// computer is linked, whose root can only link as the batch after the
// server's head (docs/errata/2026-11-23-unsent-batches-retention.md). The
// segments of those batches go all the same, so their records don't stay
// past their time.
//
// It writes batches.seg again by atomic rename, with its chain frame, a
// base frame naming the first batch it keeps, and the rows and receipts
// from there on, and only then deletes the segments. It holds the seal
// lock, which every append to batches.seg takes, from before it reads the
// old file until the Writer's handle is on the new one, and it closes the
// old handle before the rename, since Windows renames over no open file. A
// crash leaves the old batches.seg or the new one, and maybe segments the
// new one doesn't need, which nothing reads and the next Sweep deletes. It
// never deletes a segment that holds a record no batch holds yet, nor one
// a seal may still flush; the one records go to it closes first, when
// nothing is pending.
//
// The chains of device keys this installation no longer writes with, in
// old/, are swept by the same day, the rows of their batches with the
// segments, and a chain left with none goes. Sweep returns how many
// batches it dropped and how many segments it deleted.
func (w *Writer) Sweep(from string, floor uint64) (batches, segs int, err error) {
	if !isDay(from) {
		return 0, 0, fmt.Errorf("ledger: %q isn't a UTC day", from)
	}
	w.sealMu.Lock()
	defer w.sealMu.Unlock()
	// No Writer holds old/'s files, and its chains go by their own days,
	// whatever the live chain holds.
	oldBatches, oldSegs, oldErr := sweepOld(filepath.Join(w.dir, oldDir), from)
	w.mu.Lock()
	err = w.usable()
	retired := map[string]bool{} // the segments a seal may still flush
	for _, r := range w.retired {
		retired[strings.TrimSuffix(filepath.Base(r.name), segSuffix)] = true
	}
	segDay, pendingSeg := w.segDay, ""
	if len(w.pending) > 0 {
		pendingSeg = w.pending[0].pos.Seg
	}
	w.mu.Unlock()
	if err != nil {
		return oldBatches, oldSegs, errors.Join(err, oldErr)
	}
	name := filepath.Join(w.dir, batchesName)
	bf, err := readBatchesFile(name)
	if err != nil {
		return oldBatches, oldSegs, errors.Join(err, oldErr)
	}
	if bf.torn {
		// Every append to batches.seg is whole or cut back, under the seal
		// lock this holds, so only damage leaves this.
		return oldBatches, oldSegs, errors.Join(damaged("%s ends in a frame that isn't whole while its Writer is open", batchesName), oldErr)
	}
	k := keptFrom(bf, from, floor)
	if k > 0 {
		data, kerr := keptFile(bf, k)
		if kerr != nil {
			return oldBatches, oldSegs, errors.Join(kerr, oldErr)
		}
		w.batches.close()
		werr := home.WriteFile(name, data)
		f, oerr := openAppend(name, false)
		if oerr != nil {
			// Nothing can be sealed without batches.seg.
			w.fail(oerr)
			return oldBatches, oldSegs, errors.Join(oerr, oldErr)
		}
		w.batches = f
		if werr != nil {
			// The old file stays, and the Writer goes on with it.
			return oldBatches, oldSegs, errors.Join(werr, oldErr)
		}
		w.mu.Lock()
		w.base = bf.base.B + uint64(k)
		w.mu.Unlock()
		batches = k
	}
	// Each segment before need goes: those of the days before from, and
	// those before the first record that is kept, as the rest of a dropped
	// batch's records in the next day's segment.
	need := ""
	if k < len(bf.rows) {
		need = max(from, bf.rows[k].Start.Seg)
	}
	if pendingSeg != "" && (need == "" || pendingSeg < need) {
		need = pendingSeg
	}
	days, err := segments(w.dir)
	if err != nil {
		return oldBatches + batches, oldSegs, errors.Join(err, oldErr)
	}
	var errs []error
	for _, day := range days {
		if need != "" && day >= need {
			break
		}
		if retired[day] {
			continue
		}
		if day == segDay && !w.closeSegment(day) {
			continue
		}
		if err := os.Remove(segPath(w.dir, day)); err != nil {
			errs = append(errs, err)
			continue
		}
		segs++
	}
	if segs > 0 {
		syncDir(w.dir)
	}
	return oldBatches + batches, oldSegs + segs, errors.Join(append(errs, oldErr)...)
}

// closeSegment closes the segment records go to, which is day's, so a
// sweep can delete it, and reports whether it did: only while no record
// waits for a batch, since then the seal that flushed the last one has
// closed every segment before it, and the next record opens a segment of
// its own day (toSegment). w.sealMu is held.
func (w *Writer) closeSegment(day string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) > 0 || len(w.retired) > 0 || w.seg == nil || w.segDay != day {
		return false
	}
	w.seg.close()
	w.seg, w.segDay = nil, ""
	return true
}

// keptFrom returns the index in bf.rows of the first row a sweep by from
// and floor keeps: the first whose batch's first record is in the segment
// of from or a later day, or whose batch is floor or later, and
// len(bf.rows) when it keeps none.
func keptFrom(bf *batchesFile, from string, floor uint64) int {
	k := 0
	for k < len(bf.rows) && bf.rows[k].Start.Seg < from && (floor == 0 || bf.rows[k].B < floor) {
		k++
	}
	return k
}

// keptFile returns batches.seg as Sweep writes it, keeping the rows from
// bf.rows[k] on: the chain frame, of the version with a base frame, a base
// frame naming that batch, or the batch after the last when k is past
// them, the rows, then the receipts of the rows kept, in the order of
// their batches. The rows go before every receipt, so each receipt follows
// its row.
func keptFile(bf *batchesFile, k int) ([]byte, error) {
	cp := bf.chain
	cp.V = chainVersionBase
	chain, err := json.Marshal(cp)
	if err != nil {
		return nil, err
	}
	bp := basePayload{B: bf.base.B, FirstSeq: bf.base.FirstSeq, Prev: bf.base.Prev}
	switch {
	case k < len(bf.rows):
		first := bf.rows[k]
		bp = basePayload{B: first.B, FirstSeq: first.FirstSeq, Prev: first.Prev}
	case len(bf.rows) > 0:
		last := bf.rows[len(bf.rows)-1]
		bp = basePayload{B: last.B + 1, FirstSeq: last.LastSeq + 1, Prev: last.Head}
	}
	base, err := json.Marshal(bp)
	if err != nil {
		return nil, err
	}
	data := appendFrame(appendFrame(nil, kindChain, chain), kindBase, base)
	kept := bf.rows[min(k, len(bf.rows)):]
	for _, rp := range kept {
		p, err := json.Marshal(rp)
		if err != nil {
			return nil, err
		}
		data = appendFrame(data, kindBatch, p)
	}
	for _, rp := range kept {
		r, ok := bf.receipts[rp.B]
		if !ok {
			continue
		}
		p, err := json.Marshal(receiptPayload{B: rp.B, ReceivedAt: r.ReceivedAt, Signature: hex.EncodeToString(r.Signature[:])})
		if err != nil {
			return nil, err
		}
		data = appendFrame(data, kindReceipt, p)
	}
	return data, nil
}

// sweepOld sweeps each chain old/ keeps by from, as Sweep sweeps the live
// one with no floor, since nothing uploads an old chain: its batches.seg
// keeps the rows from the first batch whose first record is in the
// segment of from or a later day, and the segments before that batch's
// first record go. A chain whose rows all go goes whole, since no writer
// appends to it. One with no row, or whose batches.seg doesn't read, loses
// the segments of the days before from, and goes once it has none, and a
// batches.seg that doesn't read stays as it is meanwhile. sweepOld also
// deletes the temporary file of a rewrite a crash cut short. It returns
// how many batches it dropped and how many segments it deleted.
func sweepOld(dir, from string) (batches, segs int, err error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	var errs []error
	for _, e := range entries {
		if !e.IsDir() || !isKeyID(e.Name()) {
			continue // such as an archive still being moved in (finishArchives)
		}
		b, n, err := sweepChain(filepath.Join(dir, e.Name()), from)
		batches, segs = batches+b, segs+n
		if err != nil {
			errs = append(errs, fmt.Errorf("the chain of key %s in %s: %w", e.Name(), oldDir, err))
		}
	}
	return batches, segs, errors.Join(errs...)
}

// sweepChain is sweepOld for the chain in dir.
func sweepChain(dir, from string) (batches, segs int, err error) {
	if err := removeTemps(dir); err != nil {
		return 0, 0, err
	}
	days, err := segments(dir)
	if err != nil {
		return 0, 0, err
	}
	name := filepath.Join(dir, batchesName)
	need := from
	bf, rerr := readBatchesFile(name)
	if rerr == nil && len(bf.rows) > 0 {
		k := keptFrom(bf, from, 0)
		if k == len(bf.rows) {
			if err := os.RemoveAll(dir); err != nil {
				return 0, 0, err
			}
			return k, len(days), nil
		}
		need = bf.rows[k].Start.Seg
		if k > 0 {
			data, err := keptFile(bf, k)
			if err != nil {
				return 0, 0, err
			}
			if err := home.WriteFile(name, data); err != nil {
				return 0, 0, err
			}
			batches = k
		}
	}
	var errs []error
	if rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
		errs = append(errs, rerr)
	}
	left := 0
	for _, day := range days {
		if day >= need {
			left++
			continue
		}
		if err := os.Remove(segPath(dir, day)); err != nil {
			errs = append(errs, err)
			left++
			continue
		}
		segs++
	}
	if segs > 0 {
		syncDir(dir)
	}
	if left == 0 && (rerr != nil || len(bf.rows) == 0) {
		if err := os.RemoveAll(dir); err != nil {
			errs = append(errs, err)
		}
	}
	return batches, segs, errors.Join(errs...)
}

// removeTemps deletes the temporary file of a rewrite of batches.seg that
// a crash cut short (home.WriteFile), which holds rows and receipts nothing
// reads, from the ledger directory dir.
func removeTemps(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "."+batchesName+".tmp-") && e.Type().IsRegular() {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
