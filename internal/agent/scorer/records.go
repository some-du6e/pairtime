// SPDX-License-Identifier: AGPL-3.0-or-later

package scorer

import (
	"errors"
	"fmt"

	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/pkg/event"
	pkgledger "github.com/some-du6e/pairtime/pkg/ledger"
)

// errGap is a batch whose numbers don't follow the last scored batch's.
var errGap = errors.New("the ledger's numbers skip or repeat")

// checkBatch checks that b follows the last batch the loop scored, whose
// number was lastB and whose last record's number was lastS: b is batch
// lastB + 1, its first record is number lastS + 1, and its records go on
// from there with no gap or repeat, as many as its row says. Every record
// the device writes takes the next number, checkpoints and upload-failure
// notes included, and only records in a sealed batch keep theirs, so no
// gap between sealed records is ever legitimate (decision
// E-sm-1.1-seq-gaps). The check runs over every record, not over events
// alone, whose seq values skip the numbers of the other records.
func checkBatch(b ledger.Batch, lastB, lastS uint64) error {
	row := b.Row
	switch {
	case row.B != lastB+1:
		return fmt.Errorf("%w: batch %d follows batch %d", errGap, row.B, lastB)
	case row.FirstSeq != lastS+1:
		return fmt.Errorf("%w: batch %d starts at record %d, after record %d", errGap, row.B, row.FirstSeq, lastS)
	case row.N == 0 || uint64(len(b.Records)) != row.N:
		return fmt.Errorf("%w: batch %d holds %d records, and its row says %d", errGap, row.B, len(b.Records), row.N)
	}
	for i, rec := range b.Records {
		if s := rec.Header.Seq(); s != row.FirstSeq+uint64(i) {
			return fmt.Errorf("%w: record %d of batch %d is number %d, not %d", errGap, i, row.B, s, row.FirstSeq+uint64(i))
		}
	}
	return nil
}

// eventOf returns the event that rec holds, as the scorer reads it, and
// false for a record that holds none: a checkpoint, an upload-failure
// note, or a kind added after this release (ledger-v1 4.2). device is the
// chain's device id, which every event's device_id must be.
//
// The body holds the event's shareable fields only (ledger-v1 4.5), and
// pkg/score reads one local field, a prompt's error_sig, through its
// prompt class. The writer set it to the keyed signature of the prompt's
// text, which the body's f keeps as es (ledger-v1 10.6, 10.7), so eventOf
// puts it back from there. Scored from the ledger, a prompt then gets the
// class it had when it was recorded.
//
// The errors name the record and never quote its content.
func eventOf(rec pkgledger.Record, device string) (event.Event, bool, error) {
	rtype := rec.Header.RType()
	kind, ok := rtype.EventKind()
	if !ok {
		return event.Event{}, false, nil
	}
	s := rec.Header.Seq()
	body, err := pkgledger.ParseBody(rec.Body)
	if err != nil {
		return event.Event{}, false, fmt.Errorf("record %d: %w", s, err)
	}
	ev, err := body.Event()
	if err != nil {
		return event.Event{}, false, fmt.Errorf("record %d's event doesn't decode", s)
	}
	switch {
	case ev.Kind() != kind:
		return event.Event{}, false, fmt.Errorf("record %d is a %s record holding a %s event", s, kind, ev.Kind())
	case ev.Seq != s:
		return event.Event{}, false, fmt.Errorf("record %d holds an event with seq %d", s, ev.Seq)
	case ev.DeviceID != device:
		return event.Event{}, false, fmt.Errorf("record %d holds an event of another device", s)
	}
	if p, ok := ev.Data.(*event.PromptSubmit); ok && p != nil {
		f, present, err := body.Features(rtype)
		if err != nil {
			// A feature map from a newer release: its error signature is
			// unknown, and the scorer mustn't guess it.
			return event.Event{}, false, fmt.Errorf("record %d's features don't read: %w", s, err)
		}
		cp := *p
		cp.ErrorSig = ""
		if present && f.ES != nil {
			cp.ErrorSig = f.ES.String()
		}
		ev.Data = &cp
	}
	if ev.Validate() != nil {
		// Validate's errors can quote a value, which may be content.
		return event.Event{}, false, fmt.Errorf("record %d's event fails event.Validate", s)
	}
	return ev, true, nil
}
