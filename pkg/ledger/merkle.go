// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"fmt"
	"slices"
)

// The first byte of a Merkle leaf's and a Merkle node's SHA-256 input
// (spec 2, 5.1).
const (
	leafTag = 0x00
	nodeTag = 0x01
)

// emptyRoot is ROOT of no leaves, the SHA-256 of no bytes (spec 5.1).
var emptyRoot = sum()

// LeafHash returns LEAF(d) = SHA-256(0x00 || d) (spec 5.1).
func LeafHash(d []byte) Hash { return sum([]byte{leafTag}, d) }

// NodeHash returns NODE(l, r) = SHA-256(0x01 || l || r) (spec 5.1).
func NodeHash(l, r Hash) Hash { return sum([]byte{nodeTag}, l[:], r[:]) }

// Root returns spec 5.1's ROOT, RFC 9162's tree hash over leaves that are
// already leaf hashes. It adds no leaf prefix: it suits leaves a caller
// hashed itself, such as part 2's token leaves, while MTH hashes data
// first.
func Root(leaves []Hash) Hash {
	switch n := len(leaves); n {
	case 0:
		return emptyRoot
	case 1:
		return leaves[0]
	default:
		k := split(n)
		return NodeHash(Root(leaves[:k]), Root(leaves[k:]))
	}
}

// MTH returns RFC 9162 section 2.1.1's Merkle Tree Hash of data, which
// applies the leaf prefix itself: ROOT(LEAF(d[0]), ..., LEAF(d[n-1]))
// (spec 5.1).
func MTH(data [][]byte) Hash {
	leaves := make([]Hash, len(data))
	for i, d := range data {
		leaves[i] = LeafHash(d)
	}
	return Root(leaves)
}

// EntryLeaves returns the leaf hashes of a batch tree, LEAF(entry) for
// each entry.
func EntryLeaves(entries []Entry) []Hash {
	leaves := make([]Hash, len(entries))
	for i := range entries {
		leaves[i] = LeafHash(entries[i][:])
	}
	return leaves
}

// BatchRoot returns R_b, the MTH of a batch's entries (spec 6).
func BatchRoot(entries []Entry) Hash { return Root(EntryLeaves(entries)) }

// split returns the largest power of two smaller than n, for n >= 2.
func split(n int) int {
	k := 1
	for k<<1 < n {
		k <<= 1
	}
	return k
}

// InclusionProof returns the inclusion proof of leaf m in the tree over
// leaves, RFC 9162 section 2.1.3.1's PATH(m, D_n): the node hashes from
// the leaf's sibling up to a child of the root, none when the tree has one
// leaf (spec 5.2). Leaves are numbered from 0.
func InclusionProof(m int, leaves []Hash) ([]Hash, error) {
	n := len(leaves)
	if m < 0 || m >= n {
		return nil, fmt.Errorf("ledger: leaf %d isn't in a tree of %d leaves", m, n)
	}
	path := []Hash{}
	for lo, hi := 0, n; hi-lo > 1; {
		k := split(hi - lo)
		if m-lo < k {
			path = append(path, Root(leaves[lo+k:hi]))
			hi = lo + k
		} else {
			path = append(path, Root(leaves[lo:lo+k]))
			lo += k
		}
	}
	slices.Reverse(path)
	return path, nil
}

// VerifyInclusion checks that proof shows leaf, the leaf hash of leaf m,
// in the tree of n leaves whose root is root, with RFC 9162 section
// 2.1.3.2's algorithm (spec 5.2). It fails an m that isn't below n and a
// proof with too many or too few hashes. Errors wrap ErrProof.
//
// A proof doesn't commit to n: some proofs also verify against the same
// root with a larger n. Take n from a head already verified, n_b for a
// batch tree and count_d for a day tree, never from whoever sent the
// proof. A value that isn't 32 bytes can't be passed in, which is spec
// 5.2's length rule; HashOf applies it to raw bytes.
func VerifyInclusion(m, n uint64, leaf Hash, proof []Hash, root Hash) error {
	if m >= n {
		return fmt.Errorf("%w: leaf %d isn't in a tree of %d leaves", ErrProof, m, n)
	}
	fn, sn, r := m, n-1, leaf
	for _, p := range proof {
		if sn == 0 {
			return fmt.Errorf("%w: too many hashes for leaf %d of %d", ErrProof, m, n)
		}
		if fn&1 == 1 || fn == sn {
			r = NodeHash(p, r)
			for fn&1 == 0 && fn != 0 {
				fn >>= 1
				sn >>= 1
			}
		} else {
			r = NodeHash(r, p)
		}
		fn >>= 1
		sn >>= 1
	}
	if sn != 0 {
		return fmt.Errorf("%w: too few hashes for leaf %d of %d", ErrProof, m, n)
	}
	if r != root {
		return fmt.Errorf("%w: it leads to %s, not to the root %s", ErrProof, r, root)
	}
	return nil
}
