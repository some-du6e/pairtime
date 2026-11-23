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
// record is in the segment of a UTC day before from, except the newest
// batch, which stays so the chain always has a row to go on from.
//
// It writes batches.seg again by atomic rename, with its chain frame, a
// base frame naming the first batch it keeps, and the rows and receipts
// from there on, and only then deletes the segments wholly before that
// batch's first record. It holds the seal lock, which every append to
// batches.seg takes, from before it reads the old file until the Writer's
// handle is on the new one, and it closes the old handle before the
// rename, since Windows renames over no open file. A crash leaves the old
// batches.seg or the new one, and maybe segments the new one doesn't need,
// which nothing reads and the next Sweep deletes. It never deletes a
// segment the Writer has open.
//
// The chains of device keys this installation no longer writes with, in
// old/, lose their segments of the days before from too, and a chain left
// with none goes. Sweep returns how many batches it dropped and how many
// segments it deleted.
func (w *Writer) Sweep(from string) (batches, segs int, err error) {
	if !isDay(from) {
		return 0, 0, fmt.Errorf("ledger: %q isn't a UTC day", from)
	}
	w.sealMu.Lock()
	defer w.sealMu.Unlock()
	w.mu.Lock()
	err = w.usable()
	inUse := map[string]bool{w.segDay: true} // the segments the writer has open
	for _, r := range w.retired {
		inUse[strings.TrimSuffix(filepath.Base(r.name), segSuffix)] = true
	}
	w.mu.Unlock()
	if err != nil {
		return 0, 0, err
	}
	name := filepath.Join(w.dir, batchesName)
	bf, err := readBatchesFile(name)
	if err != nil {
		return 0, 0, err
	}
	if bf.torn {
		// Every append to batches.seg is whole or cut back, under the seal
		// lock this holds, so only damage leaves this.
		return 0, 0, damaged("%s ends in a frame that isn't whole while its Writer is open", batchesName)
	}
	if len(bf.rows) == 0 {
		return 0, 0, nil
	}
	k := 0
	for k < len(bf.rows)-1 && bf.rows[k].Start.Seg < from {
		k++
	}
	first := bf.rows[k]
	if k > 0 {
		data, kerr := keptFile(bf, k)
		if kerr != nil {
			return 0, 0, kerr
		}
		w.batches.close()
		werr := home.WriteFile(name, data)
		f, oerr := openAppend(name, false)
		if oerr != nil {
			// Nothing can be sealed without batches.seg.
			w.fail(oerr)
			return 0, 0, oerr
		}
		w.batches = f
		if werr != nil {
			return 0, 0, werr // the old file stays, and the Writer goes on with it
		}
		w.mu.Lock()
		w.base = first.B
		w.mu.Unlock()
		batches = k
	}
	days, err := segments(w.dir)
	if err != nil {
		return batches, 0, err
	}
	var errs []error
	for _, day := range days {
		if day >= first.Start.Seg {
			break
		}
		if inUse[day] {
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
	n, err := sweepOld(filepath.Join(w.dir, oldDir), from)
	segs += n
	if err != nil {
		errs = append(errs, err)
	}
	return batches, segs, errors.Join(errs...)
}

// keptFile returns batches.seg as Sweep writes it, keeping the rows from
// bf.rows[k] on: the chain frame, a base frame naming that batch, the rows,
// then the receipts of the rows kept, in the order of their batches. The
// rows go before every receipt, so each receipt follows its row.
func keptFile(bf *batchesFile, k int) ([]byte, error) {
	chain, err := json.Marshal(bf.chain)
	if err != nil {
		return nil, err
	}
	first := bf.rows[k]
	base, err := json.Marshal(basePayload{B: first.B, FirstSeq: first.FirstSeq, Prev: first.Prev})
	if err != nil {
		return nil, err
	}
	data := appendFrame(appendFrame(nil, kindChain, chain), kindBase, base)
	for _, rp := range bf.rows[k:] {
		p, err := json.Marshal(rp)
		if err != nil {
			return nil, err
		}
		data = appendFrame(data, kindBatch, p)
	}
	for _, rp := range bf.rows[k:] {
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

// sweepOld deletes the segments of the days before from in each chain old/
// keeps, and a chain directory left with none, and returns how many
// segments it deleted.
func sweepOld(dir, from string) (int, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n := 0
	var errs []error
	for _, e := range entries {
		if !e.IsDir() || !isKeyID(e.Name()) {
			continue // such as an archive still being moved in (finishArchives)
		}
		chain := filepath.Join(dir, e.Name())
		days, err := segments(chain)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		left := 0
		for _, day := range days {
			if day >= from {
				left++
				continue
			}
			if err := os.Remove(segPath(chain, day)); err != nil {
				errs = append(errs, err)
				left++
				continue
			}
			n++
		}
		if left == 0 {
			if err := os.RemoveAll(chain); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return n, errors.Join(errs...)
}
