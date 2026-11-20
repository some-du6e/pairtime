// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
)

// Batch is one sealed batch as a verifier holds it: its row, the entries
// of its records in sequence order, and whichever bodies are revealed. A
// revealed batch shows every record's header, and each body's hash stands
// in for the body until the body itself is revealed (spec 4.6; DESIGN
// 4.2).
type Batch struct {
	Row     BatchRow
	Entries []Entry
	// Bodies is nil when no body is revealed. Otherwise it has one element
	// per entry: the record's stored body, or nil when that body isn't
	// revealed.
	Bodies [][]byte
}

// BatchOf returns the batch of row that holds recs, with every body
// revealed: how a device holds its own batches.
func BatchOf(row BatchRow, recs []Record) Batch {
	b := Batch{Row: row, Entries: make([]Entry, len(recs)), Bodies: make([][]byte, len(recs))}
	for i, r := range recs {
		b.Entries[i] = r.Entry()
		b.Bodies[i] = r.Body
	}
	return b
}

// Chain is a run of one device's consecutive batches, as a verifier holds
// them (spec 9).
type Chain struct {
	// DeviceID is the device's id, which each receipt covers.
	DeviceID string
	// Prev is H_(b-1) for the run's first batch b: zero(32), the zero
	// value, when b is 1, and otherwise the head the server stored for
	// batch b - 1, when it stored one (spec 9 item 1).
	Prev Hash
	// Batches are the run's batches, in order.
	Batches []Batch
}

// Options are the checks Chain.Verify makes only when it has a key.
type Options struct {
	// ServerKey, when set, makes Verify check the receipt of every row
	// that holds one, under the key it returns for the receipt's
	// received_at: the server's public key valid at that time (DESIGN
	// 10.2), or nil if none was.
	ServerKey func(t uint64) ed25519.PublicKey
	// Keys, when set, makes Verify check the nonce in every revealed body.
	// Only the device holds them.
	Keys *Keys
}

// Verify checks c as spec section 9 says in items 1 to 7:
//
//   - batch numbers rise by 1, and a run from batch 1 starts after
//     zero(32);
//   - sequence numbers rise by 1 within and across batches, from 1 in
//     batch 1, and each row's first_seq is its first record's;
//   - every header follows spec 4.3;
//   - every revealed body hashes to its entry, is valid and agrees with its
//     header and with c.DeviceID (CheckBody), and with Options.Keys carries
//     its record's nonce;
//   - n_b counts the batch's records, R_b is the MTH of their entries, and
//     H_b follows from the head stored for the batch before, R_b, n_b and
//     b, which is the link the server checks;
//   - with Options.ServerKey, every receipt verifies.
//
// It returns nil, or what it found, joined, stopping after 100 problems.
// A gap in b or seq is a *GapError, a root mismatch a *RootMismatchError
// and a head mismatch a *HeadMismatchError; the other problems are
// *RecordError and *BatchError. errors.As picks each type out. A record
// whose rtype is a kind added after this package (RType.Later) is a
// RecordError wrapping ErrUnknownRType, and its batch is still checked,
// since roots and heads never read rtype: an auditor reports the kind as
// unknown, and a server refuses or holds it (spec 4.2). A code version 1
// never assigns is a RecordError wrapping ErrHeader.
func (c Chain) Verify(opt Options) error {
	v := &chainCheck{opt: opt, deviceID: c.DeviceID}
	prev := c.Prev
	var lastB, nextSeq uint64
	for i := range c.Batches {
		if v.full() {
			break
		}
		bt := &c.Batches[i]
		b := bt.Row.B
		switch {
		case i > 0 && b != lastB+1:
			v.add(&GapError{Field: "b", Want: lastB + 1, Got: b})
		case i == 0 && b == 0:
			v.add(&BatchError{B: 0, Err: errors.New("ledger: batch numbers start at 1")})
		case i == 0 && b == 1 && prev != zeroHash:
			v.add(&HeadMismatchError{Number: 1, Prev: true, Stored: prev, Computed: zeroHash})
		}
		nextSeq = v.batch(bt, prev, i == 0, nextSeq)
		prev, lastB = bt.Row.Head, b
	}
	return v.err()
}

type chainCheck struct {
	problems
	opt      Options
	deviceID string
}

// batch checks one batch, whose predecessor's stored head is prev and
// whose first record should be record want. A run's first batch takes its
// first s as given, unless it is batch 1. It returns the s the next batch
// should start at.
func (v *chainCheck) batch(bt *Batch, prev Hash, first bool, want uint64) uint64 {
	row := bt.Row
	if len(bt.Entries) == 0 {
		v.add(&BatchError{B: row.B, Err: errors.New("ledger: a batch holds at least one record")})
		return want
	}
	bodies := bt.Bodies
	if bodies != nil && len(bodies) != len(bt.Entries) {
		v.add(&BatchError{B: row.B, Err: fmt.Errorf("ledger: %d bodies for %d entries", len(bodies), len(bt.Entries))})
		bodies = nil
	}
	firstSeq := bt.Entries[0].Header().Seq()
	if first {
		want = firstSeq
		if row.B == 1 {
			want = 1
		}
	}
	if row.FirstSeq != firstSeq {
		v.add(&BatchError{B: row.B, Err: fmt.Errorf("ledger: first_seq is %d, and the batch's first record is %d", row.FirstSeq, firstSeq)})
	}
	for k := range bt.Entries {
		h := bt.Entries[k].Header()
		s := h.Seq()
		if s != want {
			v.add(&GapError{Field: "seq", B: row.B, Want: want, Got: s})
		}
		want = s + 1
		headerErr := h.Check()
		if headerErr != nil {
			v.add(&RecordError{B: row.B, Seq: s, Err: headerErr})
		}
		if bodies == nil || bodies[k] == nil {
			continue
		}
		body := bodies[k]
		if Hash(sha256.Sum256(body)) != bt.Entries[k].BodyHash() {
			v.add(&RecordError{B: row.B, Seq: s, Err: ErrBodyHash})
		}
		if headerErr == nil {
			if err := CheckBody(v.deviceID, h, body); err != nil {
				v.add(&RecordError{B: row.B, Seq: s, Err: err})
			}
		}
		if v.opt.Keys != nil {
			if err := v.opt.Keys.CheckNonce(s, body); err != nil {
				v.add(&RecordError{B: row.B, Seq: s, Err: err})
			}
		}
	}
	if row.N != uint64(len(bt.Entries)) {
		v.add(&BatchError{B: row.B, Err: fmt.Errorf("%w: n_b is %d, and the batch holds %d", ErrCount, row.N, len(bt.Entries))})
	}
	if root := BatchRoot(bt.Entries); root != row.Root {
		v.add(&RootMismatchError{Number: row.B, Stored: row.Root, Computed: root})
	}
	if head := BatchHead(prev, row.Root, row.N, row.B); head != row.Head {
		v.add(&HeadMismatchError{Number: row.B, Stored: row.Head, Computed: head})
	}
	if v.opt.ServerKey != nil && row.Receipt != nil {
		pub := v.opt.ServerKey(row.Receipt.ReceivedAt)
		if err := VerifyReceipt(pub, v.deviceID, row.B, row.Head, *row.Receipt); err != nil {
			v.add(&BatchError{B: row.B, Err: err})
		}
	}
	return want
}
