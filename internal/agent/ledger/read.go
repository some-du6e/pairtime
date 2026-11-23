// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/some-du6e/pairtime/pkg/ledger"
)

// ChainInfo says which device a ledger directory's chain belongs to.
type ChainInfo struct {
	DeviceID string
	// KeyID is the id of the device key that writes it.
	KeyID string
	// Created is when the chain began, at the writer's first Open under the
	// key. A record dated before it is lookback's, read from logs (DESIGN
	// 6.2 D11), or one that waited for that Open, as presence's queue and
	// held hook calls do while a locked keyring keeps the ledger shut
	// (Writer.Created).
	Created time.Time
	// Base is the number of the first batch the ledger keeps: 1 until a
	// retention sweep has dropped the oldest batches (Writer.Sweep). The
	// chain the device keeps starts there, with Prev, H_(Base-1), zero(32)
	// for batch 1, as given: the prev of the batch to be sealed next when
	// the sweep dropped every row.
	Base uint64
	Prev ledger.Hash
}

// ReadChain reads which chain the ledger in dir holds, from batches.seg.
func ReadChain(dir string) (ChainInfo, error) {
	bf, err := readBatchesFile(filepath.Join(dir, batchesName))
	if err != nil {
		return ChainInfo{}, err
	}
	return bf.info(), nil
}

// info returns the chain the file names.
func (bf *batchesFile) info() ChainInfo {
	return ChainInfo{DeviceID: bf.chain.DeviceID, KeyID: bf.chain.KeyID, Created: time.UnixMilli(bf.chain.Created), Base: bf.base.B, Prev: bf.base.Prev}
}

// ReadBatches calls fn with each sealed batch of the ledger in dir whose
// number is from or more, in order, until fn returns an error, which it
// returns. A from of 0 reads from batch 1, and one before the first batch
// the ledger keeps (ChainInfo.Base) from that batch. Each batch has its
// row, with the receipt when batches.seg holds one, when it was sealed,
// and its records as stored. Records no batch holds yet are skipped, and
// so are the batches whose records went with their segments while their
// rows stay, as a retention sweep keeps the rows of batches whose roots
// still have to be sent (Writer.Sweep): reading starts at the first batch
// from from on whose first record is held (FirstHeld).
//
// It reads the files as they are, without Open's repairs, and stops
// quietly at the first frame that isn't whole, so on a ledger a Writer has
// open, or one a crash left, the newest batches may be missing. It doesn't
// verify roots or heads, which pkg/ledger's Chain.Verify does; it returns
// an error wrapping ErrDamaged when a seal frame doesn't close the records
// before it, or disagrees with its batch's row.
func ReadBatches(dir string, from uint64, fn func(Batch) error) error {
	bf, err := readBatchesFile(filepath.Join(dir, batchesName))
	if err != nil {
		return err
	}
	days, err := segments(dir)
	if err != nil {
		return err
	}
	from = max(from, bf.base.B)
	for from <= bf.last() {
		if rp, _ := bf.row(from); slices.Contains(days, rp.Start.Seg) {
			break
		}
		from++
	}
	var start position
	next, prev := bf.base.B, bf.base.Prev
	// skip is set while what comes first in the segments may be a batch a
	// sweep dropped with every row, whose base frame names the next.
	skip := false
	rp, kept := bf.row(from)
	switch n := len(bf.rows); {
	case kept:
		start, next, prev = rp.Start, from, rp.Prev
	case n > 0:
		rp := bf.rows[n-1]
		start, next, prev = rp.End, rp.B+1, rp.Head
	case len(days) > 0:
		start, skip = position{Seg: days[0]}, bf.based
	default:
		return nil
	}
	i, found := slices.BinarySearch(days, start.Seg)
	switch {
	case i == len(days):
		// The newest row's segment went with every one before it, and
		// nothing came after.
		return nil
	case !found && i > 0:
		// A sweep deletes the oldest segments only.
		return damaged("the segment of %s, where batch %d is, isn't there", start.Seg, next)
	}
	var recs []ledger.Record
	for ; i < len(days); i++ {
		day := days[i]
		off := int64(0)
		if day == start.Seg {
			off = start.Off
		}
		more, err := readSegment(segPath(dir, day), off, func(fm frame) error {
			switch fm.kind {
			case kindRecord:
				rec, err := recordOf(fm.payload)
				if err != nil {
					return damaged("the segment of %s, offset %d: %v", day, fm.off, err)
				}
				if skip && rec.Header.Seq() < bf.base.FirstSeq {
					return nil // a record of a batch the sweep dropped
				}
				skip = false
				recs = append(recs, rec)
			case kindSeal:
				var sp sealPayload
				if err := json.Unmarshal(fm.payload, &sp); err != nil {
					return damaged("the segment of %s, offset %d: %v", day, fm.off, err)
				}
				if skip && sp.B < bf.base.B {
					return nil // the seal of a batch the sweep dropped
				}
				if sp.B != next || sp.Prev != prev || sp.N != uint64(len(recs)) || len(recs) == 0 ||
					sp.FirstSeq != recs[0].Header.Seq() {
					return damaged("the seal of batch %d, in the segment of %s, doesn't close the records before it", sp.B, day)
				}
				row := ledger.BatchRow{B: sp.B, FirstSeq: sp.FirstSeq, N: sp.N, Root: sp.Root, Head: sp.Head}
				if rp, ok := bf.row(sp.B); ok && rp.Head != sp.Head {
					return damaged("batch %d's seal and its row in %s disagree", sp.B, batchesName)
				}
				if r, ok := bf.receipts[sp.B]; ok {
					row.Receipt = &r
				}
				batch := Batch{Row: row, SealedAt: time.UnixMilli(sp.SealedAt), Online: sp.online(), Back: sp.Back,
					LocalUntil: msTime(sp.LocalUntil), Records: recs}
				recs, next, prev = nil, sp.B+1, sp.Head
				if sp.B >= from {
					return fn(batch)
				}
			default:
				return damaged("the segment of %s has a %q frame at offset %d", day, fm.kind, fm.off)
			}
			return nil
		})
		if err != nil || !more {
			return err
		}
	}
	return nil
}

// FirstHeld returns the number of the first sealed batch from from on whose
// records the ledger in dir still holds, by whether the segment its first
// record is in is there, or 0 when there is none. Retention deletes whole
// segments, the oldest first (DESIGN 4.4), and damage can take one out, so
// a reader that starts with no place, or meets a batch that doesn't read,
// goes on from here (decision E-sm-1.1-seq-gaps). A from of 0 counts from
// batch 1, and one before the first batch the ledger keeps from that
// batch (ChainInfo.Base).
func FirstHeld(dir string, from uint64) (uint64, error) {
	bf, err := readBatchesFile(filepath.Join(dir, batchesName))
	if err != nil {
		return 0, err
	}
	days, err := segments(dir)
	if err != nil {
		return 0, err
	}
	held := make(map[string]bool, len(days))
	for _, d := range days {
		held[d] = true
	}
	for b := max(from, bf.base.B); b <= bf.last(); b++ {
		if rp, _ := bf.row(b); held[rp.Start.Seg] {
			return b, nil
		}
	}
	return 0, nil
}

// readSegment calls fn with each frame of the segment name from offset off
// on. more is false when it met a frame that isn't whole, after which
// nothing in the ledger is.
func readSegment(name string, off int64, fn func(frame) error) (more bool, err error) {
	f, err := os.Open(name)
	if err != nil {
		return false, err
	}
	defer f.Close()
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return false, err
	}
	fr := newFrameReader(f, off)
	for {
		fm, err := fr.next()
		if errors.Is(err, io.EOF) {
			return true, nil
		}
		if errors.Is(err, errTorn) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if err := fn(fm); err != nil {
			return false, err
		}
	}
}
