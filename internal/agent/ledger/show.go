// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"encoding/json"
	"errors"
	"io/fs"
	"strings"

	"github.com/some-du6e/pairtime/pkg/ledger"
)

// Stored is a record as the ledger holds it, for `pairtime ledger show`
// (DESIGN 8.8).
type Stored struct {
	Record ledger.Record
	// Segment is the UTC day of the segment that holds the record, the day
	// it was written on.
	Segment string
	// Batch is the batch whose seal closes the record. It is 0 while no
	// batch seals it yet, when a crash can still cut it and its number can
	// go to another record (doc.go, Numbers), and for a record whose seal
	// was lost to damage.
	Batch uint64
}

// ReadRecords calls fn with every record the ledger in dir holds in the
// segments of day from on, all of them for a from of "", sealed or not,
// in the order they were written, until fn returns an error, which it
// returns.
//
// It reads the files as they are, like ReadBatches, so on a ledger a
// Writer has open the newest records may be missing, and it verifies no
// root or head. A segment can start inside a batch whose first records are
// in a segment from leaves out or retention deleted: its records take the
// batch of the seal that closes them. A record of a kind added after this
// version (ledger-v1 4.2) is read like any other. A segment before the
// newest that ends in a frame that isn't whole is damaged, and what came
// after that frame is lost: ReadRecords goes on with the next segment, and
// once it has read the rest it returns an error wrapping ErrDamaged that
// names the segments. A seal that doesn't close the records before it
// stops it with ErrDamaged.
func ReadRecords(dir, from string, fn func(Stored) error) error {
	days, err := segments(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	// A batch's records can span segments, and its number is known only
	// at its seal, so the records wait for it. Until a seal has been read
	// since the start or since damage, the records waiting may be only
	// the last of their batch.
	var waiting []Stored
	fresh := true
	emit := func(batch uint64) error {
		for _, s := range waiting {
			s.Batch = batch
			if err := fn(s); err != nil {
				return err
			}
		}
		waiting = waiting[:0]
		return nil
	}
	var torn []string
	for i, day := range days {
		if day < from {
			continue
		}
		more, err := readSegment(segPath(dir, day), 0, func(fm frame) error {
			switch fm.kind {
			case kindRecord:
				rec, err := storedRecord(fm.payload)
				if err != nil {
					return damaged("the segment of %s, offset %d: %v", day, fm.off, err)
				}
				waiting = append(waiting, Stored{Record: rec, Segment: day})
			case kindSeal:
				var sp sealPayload
				if err := json.Unmarshal(fm.payload, &sp); err != nil {
					return damaged("the segment of %s, offset %d: %v", day, fm.off, err)
				}
				if !closes(sp, waiting, fresh) {
					return damaged("the seal of batch %d, in the segment of %s, doesn't close the records before it", sp.B, day)
				}
				fresh = false
				return emit(sp.B)
			default:
				return damaged("the segment of %s has a %q frame at offset %d", day, fm.kind, fm.off)
			}
			return nil
		})
		if err != nil {
			return err
		}
		if !more && i < len(days)-1 {
			// The seal of the records waiting may be in what was lost.
			torn = append(torn, day)
			if err := emit(0); err != nil {
				return err
			}
			fresh = true
		}
	}
	if err := emit(0); err != nil {
		return err
	}
	if len(torn) > 0 {
		return damaged("the segments of %s end in a frame that isn't whole, so what came after it in each is lost", strings.Join(torn, ", "))
	}
	return nil
}

// closes reports whether sp seals waiting, the records read since the
// last seal. When fresh, the batch's first records may be in a segment
// that wasn't read: waiting must then be its last ones.
func closes(sp sealPayload, waiting []Stored, fresh bool) bool {
	n := uint64(len(waiting))
	if !fresh || n == sp.N {
		return n == sp.N && n > 0 && sp.FirstSeq == waiting[0].Record.Header.Seq()
	}
	return n < sp.N && (n == 0 || waiting[0].Record.Header.Seq() == sp.FirstSeq+sp.N-n)
}

// storedRecord reads a record frame's payload as recordOf does, but takes
// a record of a kind added after this version, which a reader shows as
// unknown (ledger-v1 4.2).
func storedRecord(payload []byte) (ledger.Record, error) {
	if len(payload) < ledger.HeaderSize {
		return ledger.Record{}, errors.New("a record frame shorter than a header")
	}
	var rec ledger.Record
	copy(rec.Header[:], payload)
	rec.Body = payload[ledger.HeaderSize:]
	if err := rec.Header.Check(); err != nil && !errors.Is(err, ledger.ErrUnknownRType) {
		return ledger.Record{}, err
	}
	return rec, nil
}
