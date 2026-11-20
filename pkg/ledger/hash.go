// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// HashSize is the size of SHA-256 and HMAC-SHA256 output, and so of every
// hash, root, head and nonce in the ledger.
const HashSize = sha256.Size

// Hash is 32 bytes of SHA-256 or HMAC-SHA256 output: a leaf or node hash,
// a root, a head or a nonce. Its text form, which JSON uses, is 64
// lowercase hex digits (spec section 1).
type Hash [HashSize]byte

// zeroHash is zero(32): H_0 before batch 1, and the prev of the first day
// a server publishes (spec 6, 8).
var zeroHash Hash

// HashOf returns b as a Hash. It fails unless b is exactly HashSize bytes,
// which is how a value of any other length is refused where the spec wants
// a hash (spec 5.2).
func HashOf(b []byte) (Hash, error) {
	var h Hash
	if len(b) != HashSize {
		return h, fmt.Errorf("ledger: a hash is %d bytes, got %d", HashSize, len(b))
	}
	copy(h[:], b)
	return h, nil
}

// String returns h as 64 lowercase hex digits.
func (h Hash) String() string { return hex.EncodeToString(h[:]) }

// MarshalText returns h as 64 lowercase hex digits.
func (h Hash) MarshalText() ([]byte, error) { return hex.AppendEncode(nil, h[:]), nil }

// UnmarshalText reads h from exactly 64 lowercase hex digits, the one form
// MarshalText writes.
func (h *Hash) UnmarshalText(text []byte) error {
	if len(text) != 2*HashSize || !isLowerHex(text) {
		return fmt.Errorf("ledger: a hash is %d lowercase hex digits", 2*HashSize)
	}
	_, err := hex.Decode(h[:], text)
	return err
}

func isLowerHex(b []byte) bool {
	for _, c := range b {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}

// sum returns the SHA-256 of parts, concatenated.
func sum(parts ...[]byte) Hash {
	d := sha256.New()
	for _, p := range parts {
		d.Write(p)
	}
	var h Hash
	d.Sum(h[:0])
	return h
}
