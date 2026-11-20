// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/ledger"
)

// batchesFile is batches.seg as read back.
type batchesFile struct {
	chain    chainPayload
	rows     []rowPayload // rows[i] is batch i + 1
	receipts map[uint64]ledger.Receipt
	// good is where the file stops holding whole frames: its size, or the
	// offset of a frame a crash or power cut tore.
	good int64
	torn bool
}

// damaged returns an error wrapping ErrDamaged.
func damaged(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrDamaged, fmt.Sprintf(format, args...))
}

// readBatchesFile reads batches.seg. It stops at the first frame that
// isn't whole, which only a crash or a power cut leaves at the end of a
// file written without a flush, and says where that is. Its chain frame
// was written whole before any other, so a file that doesn't start with
// one is damaged.
func readBatchesFile(name string) (*batchesFile, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	bf := &batchesFile{receipts: map[uint64]ledger.Receipt{}}
	fr := newFrameReader(f, 0)
	for i := 0; ; i++ {
		fm, err := fr.next()
		if errors.Is(err, io.EOF) {
			bf.good = fr.off
			break
		}
		var torn *tornError
		if errors.As(err, &torn) {
			if i == 0 {
				return nil, damaged("%s doesn't start with the chain it belongs to: %v", batchesName, err)
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
			if bf.chain.V != chainVersion || !isKeyID(bf.chain.KeyID) || !isOpaqueKey(bf.chain.DeviceID) {
				return nil, damaged("%s's chain frame isn't version %d's", batchesName, chainVersion)
			}
		case kindBatch:
			var rp rowPayload
			if err := json.Unmarshal(fm.payload, &rp); err != nil {
				return nil, damaged("%s, offset %d: %v", batchesName, fm.off, err)
			}
			if want := uint64(len(bf.rows)) + 1; rp.B != want || rp.N == 0 || rp.LastSeq != rp.FirstSeq+rp.N-1 ||
				(len(bf.rows) > 0 && (rp.FirstSeq != bf.rows[len(bf.rows)-1].LastSeq+1 || rp.Prev != bf.rows[len(bf.rows)-1].Head)) ||
				(len(bf.rows) == 0 && rp.FirstSeq != 1) {
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
			if err != nil || len(sig) != ledger.SignatureSize || r.B == 0 || r.B > uint64(len(bf.rows)) {
				return nil, damaged("%s, offset %d: a receipt that doesn't fit a batch", batchesName, fm.off)
			}
			rc := ledger.Receipt{ReceivedAt: r.ReceivedAt}
			copy(rc.Signature[:], sig)
			bf.receipts[r.B] = rc
		default:
			return nil, damaged("%s has a %q frame at offset %d", batchesName, fm.kind, fm.off)
		}
	}
	return bf, nil
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
	w.device = bf.chain.DeviceID
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
	restored, err := w.scan(bf.rows)
	if err != nil {
		return err
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

// newChain starts the chain of this writer's key: a new device id, and
// batches.seg with nothing but its chain frame, written whole before any
// record. It refuses a key that kept an old chain here (ErrKeyReuse).
func (w *Writer) newChain() error {
	if err := w.keyUnused(); err != nil {
		return err
	}
	w.device = newDeviceID()
	payload, err := json.Marshal(chainPayload{V: chainVersion, DeviceID: w.device, KeyID: w.keyID, Created: w.now().UnixMilli()})
	if err != nil {
		return err
	}
	name := filepath.Join(w.dir, batchesName)
	if err := home.WriteFile(name, appendFrame(nil, kindChain, payload)); err != nil {
		return err
	}
	w.batches, err = openAppend(name, false)
	return err
}

// keyUnused returns ErrKeyReuse when the writer's key kept an old chain
// here, so a chain under it would start again from s = 1.
func (w *Writer) keyUnused() error {
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

// questionRows is how many batches back Open reads the records it rebuilds
// the prompt classes' questions from (score.Questions): about an hour of
// work, at a batch a minute. A prompt that answers a question asked before
// the agent restarted usually comes within minutes.
const questionRows = 64

// before reports whether p comes before q in the segments.
func (p position) before(q position) bool {
	return p.Seg < q.Seg || p.Seg == q.Seg && p.Off < q.Off
}

// scan reads the segments from where the last row of batches.seg says its
// batch ends, and leaves the writer ready to append after the last whole
// frame: w.next, w.b, w.head, w.pending and w.seg. It returns the rows of
// batches whose seal frames it found and whose rows batches.seg lacks.
//
// A frame that isn't whole is a torn tail, which a crash or a power cut
// leaves, only at the end of the newest segment and after the last row's
// end, which was flushed before the row was written. scan cuts it off.
// Anywhere else, the files are damaged. Reading starts questionRows
// batches before the last row's end; the records before that end are
// read only to rebuild what the prompt classes need.
func (w *Writer) scan(rows []rowPayload) ([]rowPayload, error) {
	days, err := segments(w.dir)
	if err != nil {
		return nil, err
	}
	var start, from position
	next, b, head := uint64(1), uint64(0), ledger.Hash{}
	if n := len(rows); n > 0 {
		last := rows[n-1]
		start, next, b, head = last.End, last.LastSeq+1, last.B, last.Head
		if !slices.Contains(days, start.Seg) {
			return nil, damaged("batch %d ends in the segment of %s, which isn't there", last.B, start.Seg)
		}
		from = rows[max(0, n-questionRows)].Start
		if !slices.Contains(days, from.Seg) {
			from = position{Seg: start.Seg}
		}
	} else if len(days) > 0 {
		start = position{Seg: days[0]}
		from = start
	} else {
		return nil, nil
	}
	var restored []rowPayload
	keyChecked := false
	for i := slices.Index(days, from.Seg); i < len(days); i++ {
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
				if s := rec.Header.Seq(); s != next {
					f.close()
					return nil, damaged("the segment of %s has record %d where record %d belongs", day, s, next)
				}
				w.pending = append(w.pending, pending{rec: rec, pos: position{Seg: day, Off: fm.off}})
				next++
			case kindSeal:
				if sealed {
					continue
				}
				rp, err := w.sealFrame(fm.payload, b, head, end)
				if err != nil {
					f.close()
					return nil, fmt.Errorf("the segment of %s, offset %d: %w", day, fm.off, err)
				}
				restored = append(restored, rp)
				b, head = rp.B, rp.Head
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
	w.next, w.b, w.head = next, b, head
	return restored, nil
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
