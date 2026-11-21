// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"encoding/json"
	"errors"
	"io/fs"

	"github.com/some-du6e/pairtime/pkg/ledger"
)

// Stored is a record as the ledger holds it, for `pairtime ledger show`
// (DESIGN 8.8).
type Stored struct {
	Record ledger.Record
	// Segment is the UTC day of the segment that holds the record, the day
	// it was written on.
	Segment string
	// Batch is the batch whose seal closes the record, and 0 while no
	// batch holds it yet.
	Batch uint64
}

// ReadRecords calls fn with every record of the ledger in dir, sealed or
// not, in the order they were written, until fn returns an error, which
// it returns. A ledger with no segments has no records.
//
// Like ReadBatches, it reads the files as they are and stops quietly at
// the first frame that isn't whole, so on a ledger a Writer has open the
// newest records may be missing. It checks that each seal closes the
// records before it, by count, and returns an error wrapping ErrDamaged
// when one doesn't, or when a frame isn't a record or a seal; it doesn't
// verify roots or heads.
func ReadRecords(dir string, fn func(Stored) error) error {
	days, err := segments(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	// A batch's records can span segments, and its number is known only
	// at its seal, so the records wait for it.
	var waiting []Stored
	for _, day := range days {
		more, err := readSegment(segPath(dir, day), 0, func(fm frame) error {
			switch fm.kind {
			case kindRecord:
				rec, err := recordOf(fm.payload)
				if err != nil {
					return damaged("the segment of %s, offset %d: %v", day, fm.off, err)
				}
				waiting = append(waiting, Stored{Record: rec, Segment: day})
			case kindSeal:
				var sp sealPayload
				if err := json.Unmarshal(fm.payload, &sp); err != nil {
					return damaged("the segment of %s, offset %d: %v", day, fm.off, err)
				}
				if sp.N != uint64(len(waiting)) || len(waiting) == 0 || sp.FirstSeq != waiting[0].Record.Header.Seq() {
					return damaged("the seal of batch %d, in the segment of %s, doesn't close the records before it", sp.B, day)
				}
				for _, s := range waiting {
					s.Batch = sp.B
					if err := fn(s); err != nil {
						return err
					}
				}
				waiting = waiting[:0]
			default:
				return damaged("the segment of %s has a %q frame at offset %d", day, fm.kind, fm.off)
			}
			return nil
		})
		if err != nil {
			return err
		}
		if !more {
			break
		}
	}
	for _, s := range waiting {
		if err := fn(s); err != nil {
			return err
		}
	}
	return nil
}
