// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/ledger"
	"github.com/some-du6e/pairtime/pkg/score"
)

// batchesFile is batches.seg as read back.
type batchesFile struct {
	chain chainPayload
	// base is the first batch the file keeps: batch 1, whose first record
	// is s = 1 and whose prev is zero(32), unless a base frame names a
	// later one (Writer.Sweep). A sweep that drops every row names the
	// batch to be sealed next, which then has no row yet.
	base     basePayload
	based    bool         // the file has a base frame
	rows     []rowPayload // rows[i] is batch base.B + i
	receipts map[uint64]ledger.Receipt
	// good is where the file stops holding whole frames: its size, or the
	// offset of a frame a crash or power cut tore.
	good int64
	torn bool
}

// basePayload is a base frame's payload: the first batch batches.seg keeps,
// the number of its first record, and H_(b-1), which the chain from it
// takes as given, as ledger-v1 9 step 1 takes a run that starts later
// (docs/errata/2026-11-20-batches-retention.md).
type basePayload struct {
	B        uint64      `json:"b"`
	FirstSeq uint64      `json:"first_seq"`
	Prev     ledger.Hash `json:"prev"`
}

// row returns the row of batch b, when the file keeps it.
func (bf *batchesFile) row(b uint64) (rowPayload, bool) {
	if b < bf.base.B || b-bf.base.B >= uint64(len(bf.rows)) {
		return rowPayload{}, false
	}
	return bf.rows[b-bf.base.B], true
}

// last returns the number of the last batch the file has a row for, and
// base.B - 1 when it has none.
func (bf *batchesFile) last() uint64 { return bf.base.B + uint64(len(bf.rows)) - 1 }

// damaged returns an error wrapping ErrDamaged.
func damaged(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrDamaged, fmt.Sprintf(format, args...))
}

// readBatchesFile reads batches.seg. It stops at the first frame that
// isn't whole, which only a crash or a power cut leaves at the end of a
// file written without a flush, and says where that is. A receipt is
// flushed with everything before it (StoreReceipt), so such a frame with a
// whole receipt after it is damage instead. Its chain frame
// was written whole, by atomic rename, before any other, so a file that
// doesn't start with one is damaged, and so is an empty one: without its
// chain frame nothing says which device or key the segments belong to,
// and a new chain under this key would number them again.
func readBatchesFile(name string) (*batchesFile, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	bf := &batchesFile{base: basePayload{B: 1, FirstSeq: 1}, receipts: map[uint64]ledger.Receipt{}}
	fr := newFrameReader(f, 0)
	for i := 0; ; i++ {
		fm, err := fr.next()
		if errors.Is(err, io.EOF) {
			if i == 0 {
				return nil, damaged("%s is empty", batchesName)
			}
			bf.good = fr.off
			break
		}
		var torn *tornError
		if errors.As(err, &torn) {
			if i == 0 {
				return nil, damaged("%s doesn't start with the chain it belongs to: %v", batchesName, err)
			}
			// StoreReceipt flushes the file before each receipt, so only
			// what follows the last receipt can be a torn tail.
			later, err := receiptAfter(f, torn.off+1)
			if err != nil {
				return nil, err
			}
			if later {
				return nil, damaged("%s: %v, before a receipt that was flushed after it", batchesName, torn)
			}
			bf.good, bf.torn = torn.off, true
			break
		}
		if err != nil {
			return nil, err
		}
		if (i == 0) != (fm.kind == kindChain) {
			return nil, damaged("%s has a %q frame at offset %d", batchesName, fm.kind, fm.off)
		}
		switch fm.kind {
		case kindChain:
			if err := json.Unmarshal(fm.payload, &bf.chain); err != nil {
				return nil, damaged("%s's chain frame: %v", batchesName, err)
			}
			if v := bf.chain.V; v != chainVersion && v != chainVersionBase || !isKeyID(bf.chain.KeyID) || !isOpaqueKey(bf.chain.DeviceID) {
				return nil, damaged("%s's chain frame is of version %d, which this pairtime doesn't read", batchesName, bf.chain.V)
			}
		case kindBase:
			// Right after a chain frame of the version that has one,
			// written with it and the rows after it in one atomic rename
			// (Writer.Sweep).
			if i != 1 || bf.chain.V != chainVersionBase {
				return nil, damaged("%s has a base frame at offset %d, which isn't right after a chain frame of version %d", batchesName, fm.off, chainVersionBase)
			}
			var base basePayload
			if err := json.Unmarshal(fm.payload, &base); err != nil {
				return nil, damaged("%s's base frame: %v", batchesName, err)
			}
			if base.B == 0 || base.FirstSeq == 0 || (base.B == 1) != (base.FirstSeq == 1) || (base.B == 1) != (base.Prev == ledger.Hash{}) {
				return nil, damaged("%s's base frame names batch %d from record %d, which no chain has", batchesName, base.B, base.FirstSeq)
			}
			bf.base, bf.based = base, true
		case kindBatch:
			var rp rowPayload
			if err := json.Unmarshal(fm.payload, &rp); err != nil {
				return nil, damaged("%s, offset %d: %v", batchesName, fm.off, err)
			}
			if want := bf.last() + 1; rp.B != want || rp.N == 0 || rp.LastSeq != rp.FirstSeq+rp.N-1 ||
				(len(bf.rows) > 0 && (rp.FirstSeq != bf.rows[len(bf.rows)-1].LastSeq+1 || rp.Prev != bf.rows[len(bf.rows)-1].Head)) ||
				(len(bf.rows) == 0 && (rp.FirstSeq != bf.base.FirstSeq || rp.Prev != bf.base.Prev)) {
				return nil, damaged("%s, offset %d: the row of batch %d doesn't follow the one before", batchesName, fm.off, rp.B)
			}
			if ledger.BatchHead(rp.Prev, rp.Root, rp.N, rp.B) != rp.Head {
				return nil, damaged("%s: batch %d's head doesn't follow from its row", batchesName, rp.B)
			}
			bf.rows = append(bf.rows, rp)
		case kindReceipt:
			var r receiptPayload
			if err := json.Unmarshal(fm.payload, &r); err != nil {
				return nil, damaged("%s, offset %d: %v", batchesName, fm.off, err)
			}
			sig, err := hex.DecodeString(r.Signature)
			if _, ok := bf.row(r.B); err != nil || len(sig) != ledger.SignatureSize || !ok {
				return nil, damaged("%s, offset %d: a receipt that doesn't fit a batch", batchesName, fm.off)
			}
			rc := ledger.Receipt{ReceivedAt: r.ReceivedAt}
			copy(rc.Signature[:], sig)
			bf.receipts[r.B] = rc
		default:
			return nil, damaged("%s has a %q frame at offset %d", batchesName, fm.kind, fm.off)
		}
	}
	if bf.chain.V == chainVersionBase && !bf.based {
		// The sweep writes the base frame in the same atomic rename as the
		// chain frame.
		return nil, damaged("%s's chain frame is of version %d and no base frame follows it", batchesName, chainVersionBase)
	}
	return bf, nil
}

// receiptAfter reports whether f holds a whole receipt frame anywhere from
// off on, at any offset, since frames after one that isn't whole can't be
// found by reading on. A receipt there was flushed after every byte before
// it (StoreReceipt). Random bytes pass for one only if they have the
// receipt's kind byte, a length that fits, a matching CRC-32C and a
// receipt's JSON, which a torn tail's zeros or leftovers don't.
func receiptAfter(f *os.File, off int64) (bool, error) {
	fi, err := f.Stat()
	if err != nil {
		return false, err
	}
	if fi.Size() <= off {
		return false, nil
	}
	buf := make([]byte, fi.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	for i := 0; i+frameHeadSize+frameTailSize <= len(buf); i++ {
		if buf[i] != kindReceipt {
			continue
		}
		size := binary.BigEndian.Uint32(buf[i+1:])
		if size > maxPayload || int64(len(buf)-i) < frameSize(int(size)) {
			continue
		}
		end := i + frameHeadSize + int(size)
		if crc32.Checksum(buf[i:end], castagnoli) != binary.BigEndian.Uint32(buf[end:]) {
			continue
		}
		var r receiptPayload
		if json.Unmarshal(buf[i+frameHeadSize:end], &r) == nil && r.B > 0 && r.Signature != "" {
			return true, nil
		}
	}
	return false, nil
}

// isOpaqueKey reports whether s is a pkg/event opaque key: 1 to 128
// letters, digits, '_' or '-'.
func isOpaqueKey(s string) bool {
	if len(s) == 0 || len(s) > ledger.MaxKeyLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// recover reads the ledger back and puts it in a state to write to: see
// Open.
func (w *Writer) recover() error {
	if err := w.finishArchives(); err != nil {
		return err
	}
	// A rewrite of batches.seg that a crash cut short leaves its temporary
	// file, with rows and receipts nothing reads (Writer.Sweep).
	if err := removeTemps(w.dir); err != nil {
		return err
	}
	name := filepath.Join(w.dir, batchesName)
	bf, err := readBatchesFile(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		days, err := segments(w.dir)
		if err != nil {
			return err
		}
		if len(days) > 0 {
			// batches.seg is written before the first segment, and never
			// deleted. Without it nothing says which device or key the
			// records belong to, and a new chain under this key would
			// number them again.
			return damaged("the ledger has segments and no %s", batchesName)
		}
		return w.newChain()
	case err != nil:
		return err
	}
	if bf.chain.KeyID != w.keyID {
		// Checked before anything moves, so a key that can't write here
		// leaves the chain where it is.
		if err := w.keyUnused(); err != nil {
			return err
		}
		if err := w.archive(bf.chain.KeyID); err != nil {
			return err
		}
		return w.newChain()
	}
	// A chain the key started before keystore.json kept chains isn't on
	// record yet. It goes on record before anything is written to it, and
	// before scan can find the files damaged, so deleting a damaged ledger
	// can't make the key start again from s = 1.
	if err := w.ownChain(bf.chain.DeviceID); err != nil {
		return err
	}
	w.device, w.created, w.base = bf.chain.DeviceID, bf.chain.Created, bf.base.B
	if w.batches, err = openAppend(name, false); err != nil {
		return err
	}
	if bf.torn {
		// The rows after it were written without a flush and are rebuilt
		// from their seal frames below.
		if err := w.batches.cut(bf.good); err != nil {
			return err
		}
	}
	restored, err := w.scan(bf)
	if err != nil {
		return err
	}
	if len(restored) > 0 {
		// A crash between a seal frame and its flush leaves the frame, and
		// the batch's records, in the page cache only. A row must never
		// reach the disk before its batch, or a power cut would leave a
		// row pointing past its segment's end, which every Open after
		// refuses.
		if err := w.syncSegments(restored[0].Start.Seg); err != nil {
			return err
		}
	}
	for _, rp := range restored {
		if err := w.writeRow(rp); err != nil {
			return err
		}
	}
	if len(restored) > 0 {
		return w.batches.sync()
	}
	return nil
}

// syncSegments flushes the segments of from's day and every later one, the
// newest through w.seg.
func (w *Writer) syncSegments(from string) error {
	days, err := segments(w.dir)
	if err != nil {
		return err
	}
	for _, day := range days {
		switch {
		case day < from:
		case w.seg != nil && day == w.segDay:
			if err := w.seg.sync(); err != nil {
				return err
			}
		default:
			f, err := openAppend(segPath(w.dir, day), false)
			if err != nil {
				return err
			}
			err = f.sync()
			f.close()
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// newChain starts the chain of this writer's key: a new device id, and
// batches.seg with nothing but its chain frame, written whole before any
// record. It refuses a key that has a chain on record or kept an old chain
// here (ErrKeyReuse), and records the new chain before Open writes any
// record to it.
//
// The record comes after batches.seg and not before it. A chain is on
// record before any record is written, which is all that keeps nonces
// from repeating, and a record made first would lock the key out for good
// if batches.seg then failed to write, as on a full disk.
func (w *Writer) newChain() error {
	if err := w.keyUnused(); err != nil {
		return err
	}
	w.device, w.created, w.base = newDeviceID(), w.now().UnixMilli(), 1
	payload, err := json.Marshal(chainPayload{V: chainVersion, DeviceID: w.device, KeyID: w.keyID, Created: w.created})
	if err != nil {
		return err
	}
	fr, err := frameOf(kindChain, payload)
	if err != nil {
		return err
	}
	name := filepath.Join(w.dir, batchesName)
	if err := home.WriteFile(name, fr); err != nil {
		return err
	}
	if w.batches, err = openAppend(name, false); err != nil {
		return err
	}
	return w.ownChain(w.device)
}

// ownChain checks that device's chain is the one Options.Chains records
// for the writer's key, and records it when the key has none on record
// yet. A key whose record names another chain gets ErrKeyReuse.
func (w *Writer) ownChain(device string) error {
	if w.chains == nil {
		return nil
	}
	switch rec := w.chains.Chain(); rec {
	case device:
		return nil
	case "":
		if err := w.chains.RecordChain(device); err != nil {
			return fmt.Errorf("ledger: recording the chain of device %s next to its key: %w", device, err)
		}
		return nil
	default:
		return fmt.Errorf("%w: the key writes the chain of device %s, and this ledger holds its chain of device %s", ErrKeyReuse, rec, device)
	}
}

// keyUnused returns ErrKeyReuse when the writer's key has a chain on
// record, which this ledger doesn't hold, or kept an old chain here, so a
// chain under it would start again from s = 1.
func (w *Writer) keyUnused() error {
	if w.chains != nil {
		if rec := w.chains.Chain(); rec != "" {
			return fmt.Errorf("%w: the key started the chain of device %s, which this ledger doesn't hold", ErrKeyReuse, rec)
		}
	}
	for _, name := range []string{w.keyID, w.keyID + partialSuffix} {
		_, err := os.Stat(filepath.Join(w.dir, oldDir, name))
		if err == nil {
			return ErrKeyReuse
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// archive moves the chain another device key wrote to old/<its key id>,
// where it is kept for reading. Segments go first and batches.seg last, so
// an archive cut off by a crash is finished by the next Open, which still
// finds batches.seg naming the old key.
func (w *Writer) archive(keyID string) error {
	dst := filepath.Join(w.dir, oldDir, keyID)
	if _, err := os.Stat(dst); err == nil {
		// Only a chain under that key archived before, and a second one
		// started under it since, which newChain refuses, leaves both.
		return damaged("the ledger holds two chains of key %s", keyID)
	}
	tmp := dst + partialSuffix
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return err
	}
	days, err := segments(w.dir)
	if err != nil {
		return err
	}
	for _, day := range days {
		if err := os.Rename(segPath(w.dir, day), segPath(tmp, day)); err != nil {
			return err
		}
	}
	if err := os.Rename(filepath.Join(w.dir, batchesName), filepath.Join(tmp, batchesName)); err != nil {
		return err
	}
	syncDir(tmp)
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	syncDir(filepath.Dir(dst))
	syncDir(w.dir)
	return nil
}

// partialSuffix ends the name of an archive being moved into old.
const partialSuffix = ".partial"

// finishArchives gives an archive that a crash cut off after its last
// file, batches.seg, was moved its final name. One cut off earlier still
// has batches.seg in the ledger directory, and archive finishes it.
func (w *Writer) finishArchives() error {
	entries, err := os.ReadDir(filepath.Join(w.dir, oldDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		key, ok := strings.CutSuffix(e.Name(), partialSuffix)
		if !ok || !e.IsDir() || !isKeyID(key) {
			continue
		}
		tmp := filepath.Join(w.dir, oldDir, e.Name())
		if _, err := os.Stat(filepath.Join(tmp, batchesName)); err != nil {
			continue
		}
		if err := os.Rename(tmp, filepath.Join(w.dir, oldDir, key)); err != nil {
			return err
		}
		syncDir(filepath.Join(w.dir, oldDir))
	}
	return nil
}

// questionRows is how often a seal frame holds what the prompt classes'
// questions remember (score.Questions): every questionRows-th batch's does.
// Open restores them from the newest such frame and reads only the records
// after it, so a restart changes no prompt's class: f.pc follows from the
// chain's records in order, wherever the agent restarted. A ledger with no
// such frame yet reads them from its first record. One written before seal
// frames held them reads them from questionRows batches back, as near as
// it gets.
const questionRows = 64

// before reports whether p comes before q in the segments.
func (p position) before(q position) bool {
	return p.Seg < q.Seg || p.Seg == q.Seg && p.Off < q.Off
}

// scan reads the segments from where the last row of batches.seg says its
// batch ends, and leaves the writer ready to append after the last whole
// frame: w.next, w.b, w.head, w.pending, w.seg, and w.lastLocal, from the
// newest batch's mode. It returns the rows of batches whose seal frames it
// found and whose rows batches.seg lacks.
//
// A frame that isn't whole is a torn tail, which a crash or a power cut
// leaves, only at the end of the newest segment and after the last row's
// end, which was flushed before the row was written. scan cuts it off.
// Anywhere else, the files are damaged. Reading starts at the newest seal
// frame that holds the prompt classes' questions (questionRows); the
// records before the last row's end are read only to rebuild those.
//
// A retention sweep deletes the oldest segments, those of rows it keeps
// for the uploads too (Writer.Sweep), so the segment where the last row's
// batch ends may be gone, with every one before it: reading then starts at
// the first segment after it. With no row, as after a sweep that dropped
// every batch, the base frame names the next batch, and the records and
// seals of the dropped batches the kept segments still hold come before
// its records and are passed over.
func (w *Writer) scan(bf *batchesFile) ([]rowPayload, error) {
	days, err := segments(w.dir)
	if err != nil {
		return nil, err
	}
	rows := bf.rows
	var start, from, snap position
	next, b, head := bf.base.FirstSeq, bf.base.B-1, bf.base.Prev
	lastLocal := false
	// skip is set while what comes first in the segments may be a batch a
	// sweep dropped with every row.
	skip := false
	if n := len(rows); n > 0 {
		last := rows[n-1]
		start, next, b, head, snap = last.End, last.LastSeq+1, last.B, last.Head, last.Snap
		lastLocal = !last.online()
		w.until, w.sealed = last.until(), last.SealedAt
		if !slices.Contains(days, start.Seg) && len(days) > 0 && days[0] < start.Seg {
			// A sweep deletes the oldest segments only.
			return nil, damaged("batch %d ends in the segment of %s, which isn't there", last.B, start.Seg)
		}
		from = rows[max(0, n-questionRows)].Start
		if snap != (position{}) {
			// The newest seal frame that holds the questions, unless the
			// last batch is the one it seals: then from that batch's first
			// record, so a record is always read for the key check below,
			// and the seal frame after the records restores the questions
			// they gave.
			from = snap
			if last.Start.before(snap) {
				from = last.Start
			}
		}
		if !slices.Contains(days, from.Seg) {
			from = position{Seg: start.Seg}
		}
	} else if len(days) > 0 {
		start = position{Seg: days[0]}
		from = start
		skip = bf.based
		// The row that said in which mode the batch before the base was
		// sealed is gone, so it counts as local-only: the next batch sealed
		// online then takes the mark, unless something of a local-only
		// stretch is still on its way (Batch.Back).
		lastLocal = bf.based
	}
	// The first segment from from's on: one a sweep deleted holds nothing
	// to read.
	first, _ := slices.BinarySearch(days, from.Seg)
	if first == len(days) {
		w.next, w.b, w.head, w.snap, w.lastLocal = next, b, head, snap, lastLocal || len(rows) == 0 && bf.based
		return nil, nil
	}
	var restored []rowPayload
	keyChecked := false
	for i := first; i < len(days); i++ {
		day, newest := days[i], i == len(days)-1
		f, err := openAppend(segPath(w.dir, day), false)
		if err != nil {
			return nil, err
		}
		if day == start.Seg && f.size < start.Off {
			f.close()
			return nil, damaged("the segment of %s is shorter than its batches say", day)
		}
		off := int64(0)
		if day == from.Seg {
			off = from.Off
		}
		if f.size < off {
			f.close()
			return nil, damaged("the segment of %s is shorter than its batches say", day)
		}
		fr := newFrameReader(io.NewSectionReader(f.f, off, f.size-off), off)
		for {
			fm, err := fr.next()
			if errors.Is(err, io.EOF) {
				break
			}
			var torn *tornError
			if errors.As(err, &torn) {
				if !newest || (position{Seg: day, Off: torn.off}).before(start) {
					f.close()
					return nil, damaged("the segment of %s: %v", day, err)
				}
				if err := f.cut(torn.off); err != nil {
					f.close()
					return nil, err
				}
				break
			}
			if err != nil {
				f.close()
				return nil, err
			}
			end := position{Seg: day, Off: fm.end}
			sealed := !start.before(end)
			if !sealed && (position{Seg: day, Off: fm.off}).before(start) {
				f.close()
				return nil, damaged("the segment of %s has a frame across the end of batch %d", day, b)
			}
			switch fm.kind {
			case kindRecord:
				rec, err := recordOf(fm.payload)
				if err != nil {
					f.close()
					return nil, damaged("the segment of %s, offset %d: %v", day, fm.off, err)
				}
				if !keyChecked {
					// The chain frame names this key; the records must
					// carry its nonces too.
					if err := w.keys.CheckNonce(rec.Header.Seq(), rec.Body); err != nil {
						f.close()
						return nil, damaged("record %d doesn't carry the nonce of this device key", rec.Header.Seq())
					}
					keyChecked = true
				}
				w.observe(rec)
				if sealed {
					continue
				}
				if skip && rec.Header.Seq() < next {
					continue // a record of a batch the sweep dropped
				}
				skip = false
				if s := rec.Header.Seq(); s != next {
					f.close()
					return nil, damaged("the segment of %s has record %d where record %d belongs", day, s, next)
				}
				w.pending = append(w.pending, pending{rec: rec, pos: position{Seg: day, Off: fm.off}})
				next++
			case kindSeal:
				// What the questions remember after the batch's records,
				// which reading those records gives too.
				if err := w.restoreQuestions(fm.payload, position{Seg: day, Off: fm.off}, &snap); err != nil {
					f.close()
					return nil, fmt.Errorf("the segment of %s, offset %d: %w", day, fm.off, err)
				}
				if sealed {
					continue
				}
				if skip {
					var sp struct {
						B uint64 `json:"b"`
					}
					if json.Unmarshal(fm.payload, &sp) == nil && sp.B <= b {
						continue // the seal of a batch the sweep dropped
					}
				}
				rp, err := w.sealFrame(fm.payload, b, head, end)
				if err != nil {
					f.close()
					return nil, fmt.Errorf("the segment of %s, offset %d: %w", day, fm.off, err)
				}
				rp.Snap = snap
				restored = append(restored, rp)
				b, head, lastLocal = rp.B, rp.Head, !rp.online()
				w.until, w.sealed = rp.until(), rp.SealedAt
				w.pending = nil
			default:
				f.close()
				return nil, damaged("the segment of %s has a %q frame at offset %d", day, fm.kind, fm.off)
			}
		}
		if newest {
			w.seg, w.segDay = f, day
		} else {
			f.close()
		}
	}
	w.next, w.b, w.head, w.snap, w.lastLocal = next, b, head, snap, lastLocal
	return restored, nil
}

// restoreQuestions restores the prompt classes' questions from a seal
// frame that holds them, and then says the frame, which starts at at, is
// the newest that does.
func (w *Writer) restoreQuestions(payload []byte, at position, snap *position) error {
	var sp struct {
		B uint64                `json:"b"`
		Q *score.QuestionsState `json:"q"`
	}
	if err := json.Unmarshal(payload, &sp); err != nil {
		return damaged("a seal frame: %v", err)
	}
	if sp.Q == nil {
		return nil
	}
	if err := w.q.Restore(*sp.Q); err != nil {
		return damaged("the seal of batch %d: %v", sp.B, err)
	}
	*snap = at
	return nil
}

// recordOf reads a record frame's payload: a header that follows spec
// 4.3, then the body.
func recordOf(payload []byte) (ledger.Record, error) {
	if len(payload) < ledger.HeaderSize {
		return ledger.Record{}, errors.New("a record frame shorter than a header")
	}
	var rec ledger.Record
	copy(rec.Header[:], payload)
	rec.Body = payload[ledger.HeaderSize:]
	if err := rec.Header.Check(); err != nil {
		return ledger.Record{}, err
	}
	return rec, nil
}

// sealFrame checks a seal frame against the records written since the
// batch before it, b with head prev, and returns the row of the batch it
// seals, which ends at end.
func (w *Writer) sealFrame(payload []byte, b uint64, prev ledger.Hash, end position) (rowPayload, error) {
	var sp sealPayload
	if err := json.Unmarshal(payload, &sp); err != nil {
		return rowPayload{}, damaged("a seal frame: %v", err)
	}
	if len(w.pending) == 0 || sp.B != b+1 || sp.Prev != prev || sp.N != uint64(len(w.pending)) ||
		sp.FirstSeq != w.pending[0].rec.Header.Seq() {
		return rowPayload{}, damaged("the seal of batch %d doesn't close the records before it", sp.B)
	}
	entries := make([]ledger.Entry, len(w.pending))
	for i, p := range w.pending {
		entries[i] = p.rec.Entry()
	}
	row, err := ledger.Seal(prev, sp.B, entries)
	if err != nil {
		return rowPayload{}, damaged("batch %d: %v", sp.B, err)
	}
	if row.Root != sp.Root || row.Head != sp.Head {
		return rowPayload{}, damaged("the seal of batch %d has a root or head its records don't give", sp.B)
	}
	sp.Q = nil // rows leave it out
	return rowPayload{sealPayload: sp, LastSeq: sp.FirstSeq + sp.N - 1, Start: w.pending[0].pos, End: end}, nil
}

// observe feeds the record of a harness event to the questions the prompt
// classes read (score.Questions), as Write does for the records it writes.
func (w *Writer) observe(rec ledger.Record) {
	kind, ok := rec.Header.RType().EventKind()
	if !ok || !harnessKind(kind) {
		return
	}
	var body struct {
		E event.Event `json:"e"`
	}
	if json.Unmarshal(rec.Body, &body) != nil {
		return
	}
	w.q.Observe(body.E)
}

// arrivedLocked gives each record a crash left that is lookback's
// (Writer.lookback), an event a log reader read, dated before the chain
// began, the earliest time it can have reached the writer, which a moment
// then holds it to, as Write held it to when it did: when the newest batch
// was sealed, or the chain began, since it came after both. w.mu is held.
func (w *Writer) arrivedLocked() {
	for i := range w.pending {
		p := &w.pending[i]
		if p.has {
			continue
		}
		if e, ok := envelopeOf(p.rec); ok && w.lookback(e) {
			p.env, p.has = event.Envelope{T: max(w.sealed, w.created)}, true
		}
	}
}

// madeBefore reports whether before says a record in recs is an event made
// before a moment, as Write would have found it (pending.madeBefore).
func madeBefore(before func(event.Envelope) bool, recs []pending) bool {
	for _, p := range recs {
		if p.madeBefore(before) {
			return true
		}
	}
	return false
}

// envelopeOf returns the times of an event record's event, which are all
// a moment holds it to, with its via, which tells lookback's records
// (arrivedLocked), or false for a record that holds no event, as a
// checkpoint or an upload-failure note doesn't.
func envelopeOf(rec ledger.Record) (event.Envelope, bool) {
	if _, ok := rec.Header.RType().EventKind(); !ok {
		return event.Envelope{}, false
	}
	var body struct {
		E struct {
			T      int64     `json:"t"`
			Mono   int64     `json:"mono"`
			BootID string    `json:"boot_id"`
			Via    event.Via `json:"via"`
		} `json:"e"`
	}
	if json.Unmarshal(rec.Body, &body) != nil {
		return event.Envelope{}, false
	}
	return event.Envelope{T: body.E.T, Mono: body.E.Mono, BootID: body.E.BootID, Via: body.E.Via}, true
}

// harnessKind reports whether events of kind k can belong to a harness
// session, which is all score.Questions reads.
func harnessKind(k event.Kind) bool {
	switch k {
	case event.KindSessionStart, event.KindSessionEnd, event.KindPromptSubmit, event.KindAgentOutput,
		event.KindAgentTool, event.KindAgentPermission, event.KindAgentInterrupt, event.KindAgentQueue,
		event.KindAgentStop, event.KindAgentSubagent, event.KindCLITrack:
		return true
	}
	return false
}
