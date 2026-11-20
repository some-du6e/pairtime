// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
)

const smallDevice = "d_small"

// smallChain is a short chain of real records: three batches holding
// every kind of header rule, all with receipts.
type smallChain struct {
	keys    *Keys
	rows    []BatchRow
	records [][]Record
}

func newSmallChain(t *testing.T) smallChain {
	t.Helper()
	rng := rand.New(rand.NewPCG(1, 2))
	k := testKeys(t)
	plan := [][]RType{
		{0x0e, 0x03, RTypeCheckpoint},
		{0x04},
		{RTypeUploadFailure, 0x0f, 0x0d, 0x12, 0x05},
	}
	c := smallChain{keys: k}
	seq, prev := uint64(1), zeroHash
	for i, rtypes := range plan {
		var recs []Record
		for _, r := range rtypes {
			rec, _, _, err := testRecord(rng, k, recordSpec{rtype: r, deviceID: smallDevice, seq: seq, t: testT + int64(seq)*1000,
				projectKey: "p_small", sessionKey: "s_small"})
			if err != nil {
				t.Fatal(err)
			}
			recs = append(recs, rec)
			seq++
		}
		row, err := Seal(prev, uint64(i+1), entriesOf(recs))
		if err != nil {
			t.Fatal(err)
		}
		rc, err := SignReceipt(testServerKey(), smallDevice, row.B, row.Head, uint64(testT)+uint64(60_000*(i+1)))
		if err != nil {
			t.Fatal(err)
		}
		row.Receipt = &rc
		c.rows = append(c.rows, row)
		c.records = append(c.records, recs)
		prev = row.Head
	}
	return c
}

// chain returns the device's own view of c, every body revealed.
func (c smallChain) chain() Chain {
	ch := Chain{DeviceID: smallDevice}
	for i, row := range c.rows {
		ch.Batches = append(ch.Batches, BatchOf(row, c.records[i]))
	}
	return ch
}

func (c smallChain) options() Options {
	return Options{ServerKey: serverKeyAlways(testServerPub()), Keys: c.keys}
}

// clone copies c deeply enough that a mutation of the copy's rows,
// headers or bodies leaves c alone.
func (c smallChain) clone() smallChain {
	out := smallChain{keys: c.keys}
	for i, row := range c.rows {
		if row.Receipt != nil {
			rc := *row.Receipt
			row.Receipt = &rc
		}
		out.rows = append(out.rows, row)
		recs := slices.Clone(c.records[i])
		for j := range recs {
			recs[j].Body = bytes.Clone(recs[j].Body)
		}
		out.records = append(out.records, recs)
	}
	return out
}

// rowBytes lays a batch row's stored fields out as bytes, so a test can
// flip any one of them: b, first_seq, n, R_b, H_b, received_at and the
// receipt's signature.
func rowBytes(r BatchRow) []byte {
	out := binary.BigEndian.AppendUint64(nil, r.B)
	out = binary.BigEndian.AppendUint64(out, r.FirstSeq)
	out = binary.BigEndian.AppendUint64(out, r.N)
	out = append(out, r.Root[:]...)
	out = append(out, r.Head[:]...)
	out = binary.BigEndian.AppendUint64(out, r.Receipt.ReceivedAt)
	return append(out, r.Receipt.Signature[:]...)
}

func rowFromBytes(b []byte) BatchRow {
	r := BatchRow{
		B:        binary.BigEndian.Uint64(b[0:8]),
		FirstSeq: binary.BigEndian.Uint64(b[8:16]),
		N:        binary.BigEndian.Uint64(b[16:24]),
		Receipt:  &Receipt{ReceivedAt: binary.BigEndian.Uint64(b[88:96])},
	}
	copy(r.Root[:], b[24:56])
	copy(r.Head[:], b[56:88])
	copy(r.Receipt.Signature[:], b[96:160])
	return r
}

func TestVerifyAcceptsIntactChains(t *testing.T) {
	c := newSmallChain(t)
	hidden := c.chain()
	for i := range hidden.Batches {
		hidden.Batches[i].Bodies = nil
	}
	someRevealed := c.chain()
	someRevealed.Batches[2].Bodies[1] = nil
	later := c.chain()
	later.Prev = later.Batches[0].Row.Head
	later.Batches = later.Batches[1:]
	noReceipts := c.clone()
	for i := range noReceipts.rows {
		noReceipts.rows[i].Receipt = nil
	}
	cases := []struct {
		name  string
		chain Chain
		opt   Options
	}{
		{"the device's chain, every body revealed, verifies with its nonces and receipts", c.chain(), c.options()},
		{"the chain verifies with every body hidden, as a server holds a revealed batch", hidden, Options{ServerKey: serverKeyAlways(testServerPub())}},
		{"the chain verifies with some bodies revealed and some hidden", someRevealed, c.options()},
		{"a run that starts at batch 2 verifies from the head stored for batch 1", later, c.options()},
		{"a chain whose batches have no receipts yet verifies", noReceipts.chain(), c.options()},
		{"receipts aren't checked without a server key", c.chain(), Options{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.chain.Verify(tc.opt); err != nil {
				t.Errorf("Verify: %v", err)
			}
		})
	}
}

func TestVerifyFailsOnAnyChangedByte(t *testing.T) {
	c := newSmallChain(t)
	opt := c.options()
	if err := c.chain().Verify(opt); err != nil {
		t.Fatalf("the intact chain doesn't verify: %v", err)
	}
	type mutant struct {
		what  string
		chain Chain
	}
	masks := []byte{0x01, 0x80, 0xff}
	cases := []struct {
		name    string
		mutants func() []mutant
	}{
		{"changing any byte of any stored header fails verification", func() []mutant {
			var ms []mutant
			for i, recs := range c.records {
				for j := range recs {
					for at := 0; at < HeaderSize; at++ {
						for _, mask := range masks {
							m := c.clone()
							m.records[i][j].Header[at] ^= mask
							ms = append(ms, mutant{fmt.Sprintf("batch %d record %d header byte %d ^ %#02x", i+1, j, at, mask), m.chain()})
						}
					}
				}
			}
			return ms
		}},
		{"changing any byte of any stored body fails verification", func() []mutant {
			var ms []mutant
			for i, recs := range c.records {
				for j, rec := range recs {
					for at := range rec.Body {
						for _, mask := range masks {
							m := c.clone()
							m.records[i][j].Body[at] ^= mask
							ms = append(ms, mutant{fmt.Sprintf("batch %d record %d body byte %d ^ %#02x", i+1, j, at, mask), m.chain()})
						}
					}
				}
			}
			return ms
		}},
		{"changing any byte of any stored batch row fails verification", func() []mutant {
			var ms []mutant
			for i, row := range c.rows {
				stored := rowBytes(row)
				for at := range stored {
					for _, mask := range masks {
						changed := bytes.Clone(stored)
						changed[at] ^= mask
						m := c.clone()
						m.rows[i] = rowFromBytes(changed)
						ms = append(ms, mutant{fmt.Sprintf("batch %d row byte %d ^ %#02x", i+1, at, mask), m.chain()})
					}
				}
			}
			return ms
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ms := tc.mutants()
			if len(ms) == 0 {
				t.Fatal("no mutants")
			}
			for _, m := range ms {
				if err := m.chain.Verify(opt); err == nil {
					t.Errorf("%s: the chain still verifies", m.what)
				}
			}
			t.Logf("%d mutants, each refused", len(ms))
		})
	}
}

// wants reports whether err holds an error of type E that ok accepts.
func wants[E error](err error, ok func(E) bool) bool {
	for _, e := range flatten(err) {
		var target E
		if errors.As(e, &target) && ok(target) {
			return true
		}
	}
	return false
}

// flatten lists the errors a joined error holds.
func flatten(err error) []error {
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		return j.Unwrap()
	}
	if err == nil {
		return nil
	}
	return []error{err}
}

func TestVerifyErrorTypes(t *testing.T) {
	c := newSmallChain(t)
	k := c.keys
	cases := []struct {
		name   string
		mutate func(m *smallChain) Chain
		opt    Options
		want   func(err error) bool
		alone  bool // the only problem reported
	}{
		{
			name: "a dropped batch is a gap in b",
			mutate: func(m *smallChain) Chain {
				ch := m.chain()
				ch.Batches = slices.Delete(ch.Batches, 1, 2)
				return ch
			},
			want: func(err error) bool {
				return wants(err, func(e *GapError) bool { return e.Field == "b" && e.Want == 2 && e.Got == 3 })
			},
		},
		{
			name: "a dropped record is a gap in seq",
			mutate: func(m *smallChain) Chain {
				m.records[2] = slices.Delete(m.records[2], 1, 2)
				return m.chain()
			},
			want: func(err error) bool {
				return wants(err, func(e *GapError) bool { return e.Field == "seq" && e.B == 3 && e.Want == 6 && e.Got == 7 })
			},
		},
		{
			name: "two records swapped are a gap in seq",
			mutate: func(m *smallChain) Chain {
				r := m.records[2]
				r[1], r[2] = r[2], r[1]
				return m.chain()
			},
			want: func(err error) bool {
				return wants(err, func(e *GapError) bool { return e.Field == "seq" && e.B == 3 })
			},
		},
		{
			name: "a record rewritten after sealing, still well formed, is a root mismatch and nothing else",
			mutate: func(m *smallChain) Chain {
				rec, err := UploadFailureRecord(k, 5, testT, "connect")
				if err != nil {
					t.Fatal(err)
				}
				m.records[2][0] = rec
				return m.chain()
			},
			want: func(err error) bool {
				return wants(err, func(e *RootMismatchError) bool { return !e.Day && e.Number == 3 })
			},
			alone: true,
		},
		{
			name: "an earlier batch rewritten and resealed is a head mismatch in the batch after it",
			mutate: func(m *smallChain) Chain {
				rec, err := CheckpointRecord(k, 3, "p_small", 1795089600000, []byte(`{"rewritten":true}`))
				if err != nil {
					t.Fatal(err)
				}
				m.records[0][2] = rec
				row, err := Seal(zeroHash, 1, entriesOf(m.records[0]))
				if err != nil {
					t.Fatal(err)
				}
				rc, err := SignReceipt(testServerKey(), smallDevice, row.B, row.Head, m.rows[0].Receipt.ReceivedAt)
				if err != nil {
					t.Fatal(err)
				}
				row.Receipt = &rc
				m.rows[0] = row
				return m.chain()
			},
			want: func(err error) bool {
				return wants(err, func(e *HeadMismatchError) bool { return !e.Day && !e.Prev && e.Number == 2 })
			},
			alone: true,
		},
		{
			name: "batch 1 after a prev other than zero(32) is a head mismatch in its link",
			mutate: func(m *smallChain) Chain {
				ch := m.chain()
				ch.Prev[0] = 1
				return ch
			},
			want: func(err error) bool {
				return wants(err, func(e *HeadMismatchError) bool { return e.Prev && e.Number == 1 })
			},
		},
		{
			name: "an n_b that isn't the count is a batch error wrapping ErrCount",
			mutate: func(m *smallChain) Chain {
				m.rows[1].N = 2
				return m.chain()
			},
			want: func(err error) bool {
				return wants(err, func(e *BatchError) bool { return e.B == 2 && errors.Is(e, ErrCount) })
			},
		},
		{
			name: "a receipt signed by another key is a batch error wrapping ErrReceipt",
			mutate: func(m *smallChain) Chain {
				other := otherServerKey("another server")
				rc, err := SignReceipt(other, smallDevice, m.rows[1].B, m.rows[1].Head, m.rows[1].Receipt.ReceivedAt)
				if err != nil {
					t.Fatal(err)
				}
				m.rows[1].Receipt = &rc
				return m.chain()
			},
			want: func(err error) bool {
				return wants(err, func(e *BatchError) bool { return e.B == 2 && errors.Is(e, ErrReceipt) })
			},
			alone: true,
		},
		{
			name: "a receipt with no server key valid at its time is a batch error wrapping ErrReceipt",
			mutate: func(m *smallChain) Chain {
				return m.chain()
			},
			opt: Options{ServerKey: func(uint64) ed25519.PublicKey { return nil }},
			want: func(err error) bool {
				return wants(err, func(e *BatchError) bool { return errors.Is(e, ErrReceipt) })
			},
		},
		{
			name: "an os.* header with a project is a record error wrapping ErrHeader",
			mutate: func(m *smallChain) Chain {
				h := &m.records[0][0].Header
				h[offProject] = 1
				return m.chain()
			},
			want: func(err error) bool {
				return wants(err, func(e *RecordError) bool { return e.B == 1 && e.Seq == 1 && errors.Is(e, ErrHeader) })
			},
		},
		{
			name: "a revealed body whose hash isn't in its entry is a record error wrapping ErrBodyHash",
			mutate: func(m *smallChain) Chain {
				ch := m.chain()
				ch.Batches[1].Bodies[0] = m.records[0][0].Body
				return ch
			},
			want: func(err error) bool {
				return wants(err, func(e *RecordError) bool { return e.B == 2 && errors.Is(e, ErrBodyHash) })
			},
		},
		{
			name: "a body with another record's nonce is a record error wrapping ErrNonce",
			mutate: func(m *smallChain) Chain {
				rec, err := UploadFailureRecord(k, 99, testT, "dns")
				if err != nil {
					t.Fatal(err)
				}
				// Record 5's header with record 99's body, resealed, so only
				// the nonce is wrong.
				m.records[2][0].Body = rec.Body
				for i := 2; i < len(m.rows); i++ {
					row, err := Seal(m.rows[i-1].Head, m.rows[i].B, entriesOf(m.records[i]))
					if err != nil {
						t.Fatal(err)
					}
					row.Receipt = nil
					m.rows[i] = row
				}
				return m.chain()
			},
			want: func(err error) bool {
				return wants(err, func(e *RecordError) bool { return e.B == 3 && e.Seq == 5 && errors.Is(e, ErrNonce) })
			},
			alone: true,
		},
		{
			name: "a record of rtype 0x13, a kind added later, is reported as unknown, and its batch's root and head still verify",
			mutate: func(m *smallChain) Chain {
				h := &m.records[1][0].Header
				h[offRType] = 0x13
				row, err := sealUnchecked(m.rows[0].Head, 2, entriesOf(m.records[1]))
				if err != nil {
					t.Fatal(err)
				}
				m.rows[1] = row
				row3, err := Seal(row.Head, 3, entriesOf(m.records[2]))
				if err != nil {
					t.Fatal(err)
				}
				m.rows[2] = row3
				ch := m.chain()
				ch.Batches[1].Bodies = nil
				return ch
			},
			want: func(err error) bool {
				return wants(err, func(e *RecordError) bool { return e.B == 2 && errors.Is(e, ErrUnknownRType) })
			},
			alone: true,
		},
		{
			name: "a record of rtype 0x42, which version 1 never assigns, is a header error, and its batch's root and head still verify",
			mutate: func(m *smallChain) Chain {
				h := &m.records[1][0].Header
				h[offRType] = 0x42
				row, err := sealUnchecked(m.rows[0].Head, 2, entriesOf(m.records[1]))
				if err != nil {
					t.Fatal(err)
				}
				m.rows[1] = row
				row3, err := Seal(row.Head, 3, entriesOf(m.records[2]))
				if err != nil {
					t.Fatal(err)
				}
				m.rows[2] = row3
				ch := m.chain()
				ch.Batches[1].Bodies = nil
				return ch
			},
			want: func(err error) bool {
				return wants(err, func(e *RecordError) bool {
					return e.B == 2 && errors.Is(e, ErrHeader) && !errors.Is(e, ErrUnknownRType)
				})
			},
			alone: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := c.clone()
			ch := tc.mutate(&m)
			opt := tc.opt
			if opt.ServerKey == nil && opt.Keys == nil {
				opt = c.options()
			}
			err := ch.Verify(opt)
			if err == nil {
				t.Fatal("the chain verifies")
			}
			if !tc.want(err) {
				t.Errorf("Verify doesn't report the expected error; it reports:\n%v", err)
			}
			if n := len(flatten(err)); tc.alone && n != 1 {
				t.Errorf("Verify reports %d problems, want only the one:\n%v", n, err)
			}
		})
	}
}

// sealUnchecked seals entries the way Seal does, without refusing an
// rtype it doesn't know, the way a newer writer might.
func sealUnchecked(prev Hash, b uint64, entries []Entry) (BatchRow, error) {
	if len(entries) == 0 {
		return BatchRow{}, errors.New("no entries")
	}
	root := BatchRoot(entries)
	n := uint64(len(entries))
	return BatchRow{B: b, FirstSeq: entries[0].Header().Seq(), N: n, Root: root, Head: BatchHead(prev, root, n, b)}, nil
}

func TestSealRefuses(t *testing.T) {
	k := testKeys(t)
	rec := func(s uint64) Entry {
		r, err := UploadFailureRecord(k, s, testT, "dns")
		if err != nil {
			t.Fatal(err)
		}
		return r.Entry()
	}
	var unknown Header
	binary.BigEndian.PutUint64(unknown[:8], 1)
	unknown[offRType] = 0x13
	var someHead Hash
	someHead[0] = 1
	cases := []struct {
		name    string
		prev    Hash
		b       uint64
		entries []Entry
	}{
		{"batch 0 is refused; batch numbers start at 1", zeroHash, 0, []Entry{rec(1)}},
		{"an empty batch is never sealed", zeroHash, 1, nil},
		{"batch 1 starts at s = 1", zeroHash, 1, []Entry{rec(2)}},
		{"batch 1 starts after H_0 = zero(32)", someHead, 1, []Entry{rec(1)}},
		{"sequence numbers in a batch rise by 1", someHead, 2, []Entry{rec(4), rec(6)}},
		{"a repeated sequence number is refused", someHead, 2, []Entry{rec(4), rec(4)}},
		{"an rtype not in the table is refused", zeroHash, 1, []Entry{NewEntry(unknown, []byte(`{}`))}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Seal(tc.prev, tc.b, tc.entries); err == nil {
				t.Error("sealed")
			}
		})
	}
}

func TestReceipts(t *testing.T) {
	head := sum([]byte("a head"))
	rc, err := SignReceipt(testServerKey(), "d_x", 4, head, 1795089660250)
	if err != nil {
		t.Fatal(err)
	}
	pub := testServerPub()
	other := otherServerKey("another server").Public().(ed25519.PublicKey)
	later := rc
	later.ReceivedAt++
	cases := []struct {
		name     string
		pub      ed25519.PublicKey
		deviceID string
		b        uint64
		head     Hash
		r        Receipt
		ok       bool
	}{
		{"a receipt verifies for its device, batch, head and time", pub, "d_x", 4, head, rc, true},
		{"a receipt doesn't verify for another device", pub, "d_y", 4, head, rc, false},
		{"a receipt doesn't verify for another batch", pub, "d_x", 5, head, rc, false},
		{"a receipt doesn't verify for another head", pub, "d_x", 4, sum([]byte("another head")), rc, false},
		{"a receipt doesn't verify with its time moved by 1 ms", pub, "d_x", 4, head, later, false},
		{"a receipt doesn't verify under another server key", other, "d_x", 4, head, rc, false},
		{"a receipt doesn't verify under no key", nil, "d_x", 4, head, rc, false},
		{"a receipt for a device id that isn't an opaque key doesn't verify", pub, "d x", 4, head, rc, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifyReceipt(tc.pub, tc.deviceID, tc.b, tc.head, tc.r)
			if tc.ok != (err == nil) {
				t.Errorf("VerifyReceipt = %v", err)
			}
			if err != nil && !errors.Is(err, ErrReceipt) {
				t.Errorf("%v doesn't wrap ErrReceipt", err)
			}
		})
	}
}
