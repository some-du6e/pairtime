// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"cmp"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// DayMS is a day's length in milliseconds. Day d holds the Unix-ms times
// from d * DayMS to (d + 1) * DayMS - 1, UTC (spec 8).
const DayMS = 86_400_000

// dayTag is the first byte of a day chain link's SHA-256 input (spec 2,
// 8).
const dayTag = 0x04

// dayContext starts the day message the server signs (spec 2, 8).
const dayContext = "pairtime-day-v1"

// DayOf returns the day number of t, a time in Unix ms: floor(t /
// 86,400,000), the days since 1970-01-01 UTC (spec 8).
func DayOf(t uint64) uint64 { return t / DayMS }

// DayRow is one batch a server stored, as its day tree sees it: the
// device, the batch number, the head and when the server stored the row
// (spec 8).
type DayRow struct {
	DeviceID   string
	B          uint64
	Head       Hash
	ReceivedAt uint64
}

// Leaf returns r's leaf data (spec 8), which the day tree hashes with the
// leaf prefix:
//
//	u8(len(device_id)) || device_id || u64(b) || H_b || u64(received_at)
func (r DayRow) Leaf() ([]byte, error) {
	if r.B == 0 {
		return nil, fmt.Errorf("%w: batch numbers start at 1", ErrDayRows)
	}
	leaf, err := appendDevice(make([]byte, 0, 1+len(r.DeviceID)+8+HashSize+8), r.DeviceID)
	if err != nil {
		return nil, err
	}
	leaf = binary.BigEndian.AppendUint64(leaf, r.B)
	leaf = append(leaf, r.Head[:]...)
	return binary.BigEndian.AppendUint64(leaf, r.ReceivedAt), nil
}

// CompareDayRows orders rows as a day tree does: by received_at, then by
// device_id compared byte by byte, then by b (spec 8). Rows that tie on
// all three are the same batch, which a server stores once.
func CompareDayRows(a, b DayRow) int {
	if c := cmp.Compare(a.ReceivedAt, b.ReceivedAt); c != 0 {
		return c
	}
	if c := strings.Compare(a.DeviceID, b.DeviceID); c != 0 {
		return c
	}
	return cmp.Compare(a.B, b.B)
}

// DayHead returns G_d = SHA-256(0x04 || prev || root || u64(count) ||
// u64(d)), where prev is G_(d-1), zero(32) before the first day a server
// publishes (spec 8).
func DayHead(prev, root Hash, count, d uint64) Hash {
	var in [1 + 2*HashSize + 8 + 8]byte
	in[0] = dayTag
	copy(in[1:], prev[:])
	copy(in[1+HashSize:], root[:])
	binary.BigEndian.PutUint64(in[1+2*HashSize:], count)
	binary.BigEndian.PutUint64(in[1+2*HashSize+8:], d)
	return sha256.Sum256(in[:])
}

// DayMessage returns D_d = "pairtime-day-v1" || u64(d) || G_d, the bytes
// the server's day signature covers (spec 8).
func DayMessage(d uint64, head Hash) []byte {
	m := make([]byte, 0, len(dayContext)+8+HashSize)
	m = append(m, dayContext...)
	m = binary.BigEndian.AppendUint64(m, d)
	return append(m, head[:]...)
}

// Day is one day's tree and head, as the server builds it (spec 8).
type Day struct {
	// D is the day number.
	D uint64
	// Prev is G_(d-1).
	Prev Hash
	// Rows are the day's rows, in day order.
	Rows []DayRow
	// Root is T_d, the MTH of the rows' leaves.
	Root Hash
	// Head is G_d.
	Head   Hash
	leaves []Hash
}

// batchID names one stored batch.
type batchID struct {
	device string
	b      uint64
}

// BuildDay builds day d's tree over rows, chained to prev: the head of day
// d - 1, or zero(32) for the first day a server publishes. It sorts a copy
// of rows into day order, and refuses a row whose leaf can't be encoded,
// one received on another day, and a batch listed twice. A day with no
// rows gets a head too, over the SHA-256 of no bytes, so the day chain has
// no gaps.
func BuildDay(d uint64, prev Hash, rows []DayRow) (*Day, error) {
	sorted := slices.Clone(rows)
	slices.SortFunc(sorted, CompareDayRows)
	leaves := make([]Hash, len(sorted))
	seen := make(map[batchID]bool, len(sorted))
	for i, r := range sorted {
		leaf, err := r.Leaf()
		if err != nil {
			return nil, err
		}
		if got := DayOf(r.ReceivedAt); got != d {
			return nil, fmt.Errorf("%w: batch %d of %s was received on day %d, not day %d", ErrDayRows, r.B, r.DeviceID, got, d)
		}
		id := batchID{r.DeviceID, r.B}
		if seen[id] {
			return nil, fmt.Errorf("%w: batch %d of %s is listed twice", ErrDayRows, r.B, r.DeviceID)
		}
		seen[id] = true
		leaves[i] = LeafHash(leaf)
	}
	root := Root(leaves)
	return &Day{D: d, Prev: prev, Rows: sorted, Root: root, Head: DayHead(prev, root, uint64(len(sorted)), d), leaves: leaves}, nil
}

// Count returns count_d, the number of the day's rows.
func (d *Day) Count() uint64 { return uint64(len(d.Rows)) }

// Message returns D_d, the bytes the day's signature covers.
func (d *Day) Message() []byte { return DayMessage(d.D, d.Head) }

// Proof returns the inclusion proof of the day's row i, which a bundle
// carries with i (DESIGN 7.8). It verifies against Root with Count leaves.
func (d *Day) Proof(i int) ([]Hash, error) { return InclusionProof(i, d.leaves) }

// Publish signs the day's head with the server's key and returns what the
// server commits for the day (spec 8).
func (d *Day) Publish(key ed25519.PrivateKey) (PublishedDay, error) {
	sig, err := SignDay(key, d.D, d.Head)
	if err != nil {
		return PublishedDay{}, err
	}
	return PublishedDay{D: d.D, Prev: d.Prev, Root: d.Root, Count: d.Count(), Head: d.Head, Signature: sig}, nil
}

// SignDay returns the server's signature over D_d for day d with head
// (spec 8).
func SignDay(key ed25519.PrivateKey, d uint64, head Hash) ([SignatureSize]byte, error) {
	return sign(key, DayMessage(d, head))
}

// VerifyDay checks sig, a signature over D_d for day d with head, under
// pub. It returns ErrDaySignature when sig doesn't verify.
func VerifyDay(pub ed25519.PublicKey, d uint64, head Hash, sig [SignatureSize]byte) error {
	if !verify(pub, DayMessage(d, head), sig) {
		return ErrDaySignature
	}
	return nil
}

// PublishedDay is what the server commits for a day: d, G_(d-1), T_d,
// count_d, G_d and the signature over D_d (spec 8).
type PublishedDay struct {
	D         uint64
	Prev      Hash
	Root      Hash
	Count     uint64
	Head      Hash
	Signature [SignatureSize]byte
}

// VerifyRows checks that rows, in the order given, are all of day p's rows
// (spec 9 item 9): each received on day p.D, in day order, no batch listed
// twice, count_d of them, and T_d the root of their tree. It returns nil
// or every problem joined: DayErrors wrapping ErrDayRows, and a
// RootMismatchError for a root that doesn't match.
func (p PublishedDay) VerifyRows(rows []DayRow) error {
	var v problems
	leaves := make([]Hash, 0, len(rows))
	seen := make(map[batchID]bool, len(rows))
	for i, r := range rows {
		leaf, err := r.Leaf()
		if err != nil {
			v.add(&DayError{D: p.D, Err: fmt.Errorf("row %d: %w", i, err)})
			continue
		}
		leaves = append(leaves, LeafHash(leaf))
		if got := DayOf(r.ReceivedAt); got != p.D {
			v.add(&DayError{D: p.D, Err: fmt.Errorf("%w: row %d was received on day %d", ErrDayRows, i, got)})
		}
		if i > 0 && CompareDayRows(rows[i-1], r) > 0 {
			v.add(&DayError{D: p.D, Err: fmt.Errorf("%w: row %d is out of order; rows sort by received_at, then device_id, then b", ErrDayRows, i)})
		}
		id := batchID{r.DeviceID, r.B}
		if seen[id] {
			v.add(&DayError{D: p.D, Err: fmt.Errorf("%w: row %d repeats batch %d of %s", ErrDayRows, i, r.B, r.DeviceID)})
		}
		seen[id] = true
	}
	if uint64(len(rows)) != p.Count {
		v.add(&DayError{D: p.D, Err: fmt.Errorf("%w: count_d is %d, and the day has %d rows", ErrDayRows, p.Count, len(rows))})
	}
	if root := Root(leaves); root != p.Root {
		v.add(&RootMismatchError{Day: true, Number: p.D, Stored: p.Root, Computed: root})
	}
	return v.err()
}

// VerifyDays checks a run of published days (spec 9 item 9): each day
// after the first has the next day number and a prev equal to the head
// before it, each head follows from its day's prev, root, count and d, and
// each signature verifies under pub. The first day's prev is taken as
// given. It returns nil or every problem joined: a GapError for a missing
// day, HeadMismatchErrors, and DayErrors wrapping ErrDaySignature.
func VerifyDays(pub ed25519.PublicKey, days []PublishedDay) error {
	var v problems
	for i, p := range days {
		if v.full() {
			break
		}
		if i > 0 {
			last := days[i-1]
			if p.D != last.D+1 {
				v.add(&GapError{Field: "d", Want: last.D + 1, Got: p.D})
			}
			if p.Prev != last.Head {
				v.add(&HeadMismatchError{Day: true, Number: p.D, Prev: true, Stored: p.Prev, Computed: last.Head})
			}
		}
		if head := DayHead(p.Prev, p.Root, p.Count, p.D); head != p.Head {
			v.add(&HeadMismatchError{Day: true, Number: p.D, Stored: p.Head, Computed: head})
		}
		if err := VerifyDay(pub, p.D, p.Head, p.Signature); err != nil {
			v.add(&DayError{D: p.D, Err: err})
		}
	}
	return v.err()
}

// errTooMany ends a list of problems that was cut short.
var errTooMany = fmt.Errorf("ledger: stopped after %d problems", maxProblems)

// maxProblems caps the problems one verification reports.
const maxProblems = 100

// problems collects what a verification finds, up to maxProblems.
type problems struct{ errs []error }

func (p *problems) add(err error) {
	switch {
	case len(p.errs) < maxProblems:
		p.errs = append(p.errs, err)
	case len(p.errs) == maxProblems:
		p.errs = append(p.errs, errTooMany)
	}
}

// full reports whether the cap was reached, so the caller can stop.
func (p *problems) full() bool { return len(p.errs) > maxProblems }

func (p *problems) err() error {
	switch len(p.errs) {
	case 0:
		return nil
	case 1:
		return p.errs[0]
	}
	return errors.Join(p.errs...)
}
