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
	// Created is when the chain began, at the writer's first start under
	// the key. No record of a time before it was written live: those are
	// lookback's, read from logs (DESIGN 6.2 D11).
	Created time.Time
}

// ReadChain reads which chain the ledger in dir holds, from batches.seg.
func ReadChain(dir string) (ChainInfo, error) {
	bf, err := readBatchesFile(filepath.Join(dir, batchesName))
	if err != nil {
		return ChainInfo{}, err
	}
	return ChainInfo{DeviceID: bf.chain.DeviceID, KeyID: bf.chain.KeyID, Created: time.UnixMilli(bf.chain.Created)}, nil
}

// ReadBatches calls fn with each sealed batch of the ledger in dir whose
// number is from or more, in order, until fn returns an error, which it
// returns. A from of 0 reads from batch 1. Each batch has its row, with the receipt when batches.seg holds
// one, when it was sealed, and its records as stored. Records no batch
// holds yet are skipped.
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
	from = max(from, 1)
	var start position
	next, prev := uint64(1), ledger.Hash{}
	switch n := uint64(len(bf.rows)); {
	case from <= n:
		rp := bf.rows[from-1]
		start, next, prev = rp.Start, from, rp.Prev
	case n > 0:
		rp := bf.rows[n-1]
		start, next, prev = rp.End, rp.B+1, rp.Head
	case len(days) > 0:
		start = position{Seg: days[0]}
	default:
		return nil
	}
	i := slices.Index(days, start.Seg)
	if i < 0 {
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
				recs = append(recs, rec)
			case kindSeal:
				var sp sealPayload
				if err := json.Unmarshal(fm.payload, &sp); err != nil {
					return damaged("the segment of %s, offset %d: %v", day, fm.off, err)
				}
				if sp.B != next || sp.Prev != prev || sp.N != uint64(len(recs)) || len(recs) == 0 ||
					sp.FirstSeq != recs[0].Header.Seq() {
					return damaged("the seal of batch %d, in the segment of %s, doesn't close the records before it", sp.B, day)
				}
				row := ledger.BatchRow{B: sp.B, FirstSeq: sp.FirstSeq, N: sp.N, Root: sp.Root, Head: sp.Head}
				if sp.B <= uint64(len(bf.rows)) && bf.rows[sp.B-1].Head != sp.Head {
					return damaged("batch %d's seal and its row in %s disagree", sp.B, batchesName)
				}
				if r, ok := bf.receipts[sp.B]; ok {
					row.Receipt = &r
				}
				batch := Batch{Row: row, SealedAt: time.UnixMilli(sp.SealedAt), Online: sp.online(), Back: sp.Back, Records: recs}
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
// batch 1.
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
	for b := max(from, 1); b <= uint64(len(bf.rows)); b++ {
		if held[bf.rows[b-1].Start.Seg] {
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
