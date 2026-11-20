// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"errors"
	"fmt"
)

// The errors that the typed errors below wrap, and that the functions
// checking one value return. Test for them with errors.Is.
var (
	// ErrUnknownRType is a record type that isn't in spec 4.2's table.
	// Roots and heads never read rtype, so the rest of the record's batch
	// can still be checked.
	ErrUnknownRType = errors.New("ledger: rtype isn't in the table")
	// ErrHeader is a header that breaks a rule of spec 4.3.
	ErrHeader = errors.New("ledger: header breaks spec 4.3")
	// ErrBody is a body that isn't a record body (spec 4.5).
	ErrBody = errors.New("ledger: not a record body")
	// ErrBodyHash is a revealed body whose SHA-256 isn't the one in its
	// entry (spec 4.6).
	ErrBodyHash = errors.New("ledger: body's SHA-256 isn't the one in its entry")
	// ErrBodyHeader is a body that disagrees with its header (spec 4.5).
	ErrBodyHeader = errors.New("ledger: body disagrees with its header")
	// ErrNonce is a body that doesn't carry its record's nonce (spec 4.4).
	ErrNonce = errors.New("ledger: body doesn't carry its record's nonce")
	// ErrCount is an n_b other than the number of the batch's records
	// (spec 6).
	ErrCount = errors.New("ledger: n_b isn't the number of the batch's records")
	// ErrReceipt is a receipt that doesn't verify (spec 7).
	ErrReceipt = errors.New("ledger: receipt doesn't verify")
	// ErrProof is an inclusion proof that doesn't verify (spec 5.2).
	ErrProof = errors.New("ledger: inclusion proof doesn't verify")
	// ErrDayRows is a day's rows breaking a rule of spec 8.
	ErrDayRows = errors.New("ledger: day rows break spec 8")
	// ErrDaySignature is a day signature that doesn't verify (spec 8).
	ErrDaySignature = errors.New("ledger: day signature doesn't verify")
)

// GapError is a batch number, sequence number or day number that doesn't
// follow the one before it (spec 9 items 1, 2 and 9): a batch, record or
// day was hidden or dropped.
type GapError struct {
	// Field is "b", "seq" or "d".
	Field string
	// B is the batch where the gap shows, for a gap in "seq".
	B uint64
	// Want is the number that belongs next, and Got the one that is there.
	Want, Got uint64
}

func (e *GapError) Error() string {
	switch e.Field {
	case "seq":
		return fmt.Sprintf("ledger: batch %d has record %d where record %d belongs; sequence numbers have no gaps", e.B, e.Got, e.Want)
	case "d":
		return fmt.Sprintf("ledger: day %d is where day %d belongs; every day has a head", e.Got, e.Want)
	default:
		return fmt.Sprintf("ledger: batch %d is where batch %d belongs; batch numbers have no gaps", e.Got, e.Want)
	}
}

// RootMismatchError is a stored root other than the root of what it
// covers: a batch's R_b and its entries, or a day's T_d and its rows (spec
// 9 items 5 and 9). A record or row was changed or moved.
type RootMismatchError struct {
	// Day is set for a day's T_d, and clear for a batch's R_b.
	Day bool
	// Number is b, or d for a day.
	Number uint64
	// Stored is the root the row or day gives, and Computed the root of
	// what it covers.
	Stored, Computed Hash
}

func (e *RootMismatchError) Error() string {
	if e.Day {
		return fmt.Sprintf("ledger: day %d: T_d is %s, but its rows give %s", e.Number, e.Stored, e.Computed)
	}
	return fmt.Sprintf("ledger: batch %d: R_b is %s, but its entries give %s", e.Number, e.Stored, e.Computed)
}

// HeadMismatchError is a chain link that doesn't hold (spec 9 items 1, 6
// and 9): a head that doesn't follow from the head before it and its own
// fields, or a prev that isn't the head before it. The chain was
// rewritten.
type HeadMismatchError struct {
	// Day is set for the day chain's G_d, and clear for a batch chain's
	// H_b.
	Day bool
	// Number is b, or d for a day.
	Number uint64
	// Prev is set when the wrong value is the link to the head before,
	// rather than the head itself.
	Prev bool
	// Stored is the value given, and Computed the value the chain gives.
	Stored, Computed Hash
}

func (e *HeadMismatchError) Error() string {
	unit, head, from := "batch", "H_b", "H_(b-1), R_b, n_b and b"
	if e.Day {
		unit, head, from = "day", "G_d", "G_(d-1), T_d, count_d and d"
	}
	if e.Prev {
		return fmt.Sprintf("ledger: %s %d: prev is %s, but the chain gives %s", unit, e.Number, e.Stored, e.Computed)
	}
	return fmt.Sprintf("ledger: %s %d: %s is %s, but %s give %s", unit, e.Number, head, e.Stored, from, e.Computed)
}

// RecordError is one record that breaks a rule of spec section 4: a
// header rule, a body whose SHA-256 isn't its entry's, a body that
// disagrees with its header, or a nonce that isn't the record's. Err wraps
// ErrHeader, ErrUnknownRType, ErrBody, ErrBodyHash, ErrBodyHeader or
// ErrNonce.
type RecordError struct {
	// B is the batch that holds the record.
	B uint64
	// Seq is the record's s, as its header gives it.
	Seq uint64
	Err error
}

func (e *RecordError) Error() string {
	return fmt.Sprintf("%v (batch %d, record %d)", e.Err, e.B, e.Seq)
}

func (e *RecordError) Unwrap() error { return e.Err }

// BatchError is a batch that breaks a rule other than a gap or a mismatch
// (spec 6, 7, 9): no records, an n_b that isn't the count (wrapping
// ErrCount), a first_seq that isn't the first record's, bodies that don't
// line up with the entries, or a receipt that doesn't verify (wrapping
// ErrReceipt).
type BatchError struct {
	B   uint64
	Err error
}

func (e *BatchError) Error() string { return fmt.Sprintf("%v (batch %d)", e.Err, e.B) }

func (e *BatchError) Unwrap() error { return e.Err }

// DayError is a published day whose rows or signature break spec 8. Err
// wraps ErrDayRows or ErrDaySignature.
type DayError struct {
	D   uint64
	Err error
}

func (e *DayError) Error() string { return fmt.Sprintf("%v (day %d)", e.Err, e.D) }

func (e *DayError) Unwrap() error { return e.Err }
