// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"encoding/binary"
	"errors"
	"fmt"
	"testing"
)

func TestInclusionProofs(t *testing.T) {
	leavesOf := func(n int) []Hash {
		hs := make([]Hash, n)
		for i := range hs {
			hs[i] = LeafHash(binary.BigEndian.AppendUint64(nil, uint64(i)))
		}
		return hs
	}
	flip := func(h Hash) Hash {
		h[0] ^= 1
		return h
	}
	type proofCase struct {
		m, n  uint64
		leaf  Hash
		proof []Hash
		root  Hash
	}
	cases := []struct {
		name   string
		mutate func(c proofCase) (proofCase, bool) // false: no such mutation for this proof
		ok     bool
	}{
		{"PATH(m, D_n) verifies for every leaf of trees of 1 to 70 leaves", func(c proofCase) (proofCase, bool) { return c, true }, true},
		{"a proof with the leaf hash changed fails", func(c proofCase) (proofCase, bool) { c.leaf = flip(c.leaf); return c, true }, false},
		{"a proof against another root fails", func(c proofCase) (proofCase, bool) { c.root = flip(c.root); return c, true }, false},
		{"a proof with one hash too many fails", func(c proofCase) (proofCase, bool) {
			c.proof = append(append([]Hash{}, c.proof...), c.root)
			return c, true
		}, false},
		{"a proof with its last hash dropped fails", func(c proofCase) (proofCase, bool) {
			if len(c.proof) == 0 {
				return c, false
			}
			c.proof = c.proof[:len(c.proof)-1]
			return c, true
		}, false},
		{"a proof with a hash changed fails", func(c proofCase) (proofCase, bool) {
			if len(c.proof) == 0 {
				return c, false
			}
			c.proof = append([]Hash{flip(c.proof[0])}, c.proof[1:]...)
			return c, true
		}, false},
		{"a proof checked for the neighbouring leaf fails", func(c proofCase) (proofCase, bool) {
			if c.n == 1 {
				return c, false
			}
			if c.m+1 < c.n {
				c.m++
			} else {
				c.m--
			}
			return c, true
		}, false},
		{"a proof for a leaf at or past n fails", func(c proofCase) (proofCase, bool) { c.m = c.n; return c, true }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tried := 0
			for n := 1; n <= 70; n++ {
				hs := leavesOf(n)
				root := Root(hs)
				for m := 0; m < n; m++ {
					proof, err := InclusionProof(m, hs)
					if err != nil {
						t.Fatal(err)
					}
					c, ok := tc.mutate(proofCase{uint64(m), uint64(n), hs[m], proof, root})
					if !ok {
						continue
					}
					tried++
					err = VerifyInclusion(c.m, c.n, c.leaf, c.proof, c.root)
					if tc.ok && err != nil {
						t.Fatalf("leaf %d of %d: %v", m, n, err)
					}
					if !tc.ok && !errors.Is(err, ErrProof) {
						t.Fatalf("leaf %d of %d: VerifyInclusion = %v, want ErrProof", m, n, err)
					}
				}
			}
			if tried == 0 {
				t.Fatal("no proofs tried")
			}
		})
	}
}

func TestTreeEdges(t *testing.T) {
	one := LeafHash([]byte("one"))
	cases := []struct {
		name string
		ok   bool
	}{
		{"ROOT of no leaves is the SHA-256 of no bytes", Root(nil).String() == "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{"ROOT of one leaf is that leaf", Root([]Hash{one}) == one},
		{"MTH applies the leaf prefix that ROOT doesn't", MTH([][]byte{[]byte("one")}) == one},
		{"a batch root is the MTH of the entries' bytes", BatchRoot([]Entry{{1}, {2}}) == MTH([][]byte{Entry{1}.bytes(), Entry{2}.bytes()})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Fail()
			}
		})
	}
	t.Run("InclusionProof refuses a leaf outside the tree", func(t *testing.T) {
		for _, m := range []int{-1, 1} {
			if _, err := InclusionProof(m, []Hash{one}); err == nil {
				t.Errorf("leaf %d of 1: no error", m)
			}
		}
	})
}

func (e Entry) bytes() []byte { return e[:] }

func TestDays(t *testing.T) {
	const day = 20776 // 2026-11-19
	start := uint64(day * DayMS)
	head := func(s string) Hash { return sum([]byte(s)) }
	rows := []DayRow{
		{DeviceID: "d_b", B: 2, Head: head("b2"), ReceivedAt: start + 5000},
		{DeviceID: "d_a", B: 9, Head: head("a9"), ReceivedAt: start + 5000},
		{DeviceID: "D_c", B: 1, Head: head("c1"), ReceivedAt: start + 5000},
		{DeviceID: "d_a", B: 8, Head: head("a8"), ReceivedAt: start + 5000},
		{DeviceID: "d_z", B: 4, Head: head("z4"), ReceivedAt: start},
		{DeviceID: "d_y", B: 3, Head: head("y3"), ReceivedAt: start + DayMS - 1},
	}
	// By received_at, then device_id byte by byte ('D' < 'd'), then b.
	wantOrder := []string{"d_z/4", "D_c/1", "d_a/8", "d_a/9", "d_b/2", "d_y/3"}
	built, err := BuildDay(day, zeroHash, rows)
	if err != nil {
		t.Fatal(err)
	}
	pub := testServerPub()
	published, err := built.Publish(testServerKey())
	if err != nil {
		t.Fatal(err)
	}
	next, err := BuildDay(day+1, built.Head, nil)
	if err != nil {
		t.Fatal(err)
	}
	nextPub, err := next.Publish(testServerKey())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"rows sort by received_at, then device_id compared byte by byte, then b", func(t *testing.T) {
			for i, r := range built.Rows {
				if got := fmt.Sprintf("%s/%d", r.DeviceID, r.B); got != wantOrder[i] {
					t.Errorf("row %d is %s, want %s", i, got, wantOrder[i])
				}
			}
		}},
		{"DayOf puts a day's first and last millisecond on it, and the next millisecond on the next day", func(t *testing.T) {
			if DayOf(start) != day || DayOf(start+DayMS-1) != day || DayOf(start+DayMS) != day+1 {
				t.Error("wrong day")
			}
		}},
		{"a row received on another day is refused", func(t *testing.T) {
			_, err := BuildDay(day, zeroHash, append(rows[:1:1], DayRow{DeviceID: "d_x", B: 1, ReceivedAt: start + DayMS}))
			if !errors.Is(err, ErrDayRows) {
				t.Errorf("BuildDay = %v", err)
			}
		}},
		{"a batch listed twice is refused, even at another time", func(t *testing.T) {
			_, err := BuildDay(day, zeroHash, append(rows[:1:1], DayRow{DeviceID: "d_b", B: 2, ReceivedAt: start + 9000}))
			if !errors.Is(err, ErrDayRows) {
				t.Errorf("BuildDay = %v", err)
			}
		}},
		{"a row whose device_id isn't an opaque key is refused", func(t *testing.T) {
			if _, err := BuildDay(day, zeroHash, []DayRow{{DeviceID: "d/x", B: 1, ReceivedAt: start}}); err == nil {
				t.Error("built")
			}
		}},
		{"a day with no rows has the empty root, count 0, and a head of its own", func(t *testing.T) {
			if next.Count() != 0 || next.Root != Root(nil) || next.Head != DayHead(built.Head, Root(nil), 0, day+1) {
				t.Errorf("empty day: count %d, root %s, head %s", next.Count(), next.Root, next.Head)
			}
		}},
		{"every row's proof verifies against T_d with count_d leaves", func(t *testing.T) {
			for i, r := range built.Rows {
				proof, err := built.Proof(i)
				if err != nil {
					t.Fatal(err)
				}
				leaf, err := r.Leaf()
				if err != nil {
					t.Fatal(err)
				}
				if err := VerifyInclusion(uint64(i), built.Count(), LeafHash(leaf), proof, built.Root); err != nil {
					t.Errorf("row %d: %v", i, err)
				}
			}
		}},
		{"the day's own rows verify against what it published", func(t *testing.T) {
			if err := published.VerifyRows(built.Rows); err != nil {
				t.Error(err)
			}
		}},
		{"rows out of order fail", func(t *testing.T) {
			swapped := append([]DayRow{}, built.Rows...)
			swapped[0], swapped[1] = swapped[1], swapped[0]
			err := published.VerifyRows(swapped)
			if !errors.Is(err, ErrDayRows) || !wants(err, func(e *RootMismatchError) bool { return e.Day && e.Number == day }) {
				t.Errorf("VerifyRows = %v", err)
			}
		}},
		{"a missing row fails", func(t *testing.T) {
			if err := published.VerifyRows(built.Rows[1:]); !errors.Is(err, ErrDayRows) {
				t.Errorf("VerifyRows = %v", err)
			}
		}},
		{"a row listed twice fails", func(t *testing.T) {
			twice := append(append([]DayRow{}, built.Rows...), built.Rows[len(built.Rows)-1])
			if err := published.VerifyRows(twice); !errors.Is(err, ErrDayRows) {
				t.Errorf("VerifyRows = %v", err)
			}
		}},
		{"two published days in a row verify", func(t *testing.T) {
			if err := VerifyDays(pub, []PublishedDay{published, nextPub}); err != nil {
				t.Error(err)
			}
		}},
		{"a day missing from the chain is a gap in d", func(t *testing.T) {
			later := nextPub
			later.D = day + 2
			err := VerifyDays(pub, []PublishedDay{published, later})
			if !wants(err, func(e *GapError) bool { return e.Field == "d" && e.Want == day+1 && e.Got == day+2 }) {
				t.Errorf("VerifyDays = %v", err)
			}
		}},
		{"a prev that isn't the head of the day before is a head mismatch in the link", func(t *testing.T) {
			other := nextPub
			other.Prev = head("another day")
			err := VerifyDays(pub, []PublishedDay{published, other})
			if !wants(err, func(e *HeadMismatchError) bool { return e.Day && e.Prev && e.Number == day+1 }) {
				t.Errorf("VerifyDays = %v", err)
			}
		}},
		{"a head that doesn't follow from its day is a head mismatch", func(t *testing.T) {
			other := published
			other.Count++
			err := VerifyDays(pub, []PublishedDay{other})
			if !wants(err, func(e *HeadMismatchError) bool { return e.Day && !e.Prev && e.Number == day }) {
				t.Errorf("VerifyDays = %v", err)
			}
		}},
		{"a day signed by another key fails its signature", func(t *testing.T) {
			sig, err := SignDay(otherServerKey("another server"), published.D, published.Head)
			if err != nil {
				t.Fatal(err)
			}
			other := published
			other.Signature = sig
			err = VerifyDays(pub, []PublishedDay{other})
			if !errors.Is(err, ErrDaySignature) || !wants(err, func(e *DayError) bool { return e.D == day }) {
				t.Errorf("VerifyDays = %v", err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, c.check)
	}
}
