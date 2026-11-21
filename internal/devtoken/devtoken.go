// SPDX-License-Identifier: AGPL-3.0-or-later

// Package devtoken is the format of pairtime's device tokens, which the
// server hands a linked app and the app sends with every upload (SP 8.3;
// docs/spec/device-auth-v1.md section 5):
//
//	ptd_ + 32 random base62 characters + a 6-character base62 checksum
//
// The checksum is the CRC-32 (IEEE) of everything before it, prefix
// included, so a token mistyped, cut short or corrupted in a copy fails
// Check without anyone looking it up. GitHub's tokens work the same way.
// Check takes 30 to 64 random characters, so the length can grow.
//
// The client imports this package too, so it does no work at init: every
// pairtime hook process pays for package init.
package devtoken

import (
	"crypto/rand"
	"errors"
	"hash/crc32"
)

// Prefix starts every device token.
const Prefix = "ptd_"

// RandomLen is how many random base62 characters New writes.
const RandomLen = 32

// The bounds Check puts on the random part.
const (
	MinRandomLen = 30
	MaxRandomLen = 64
)

// ChecksumLen is the length of the checksum at the end.
const ChecksumLen = 6

// Len is the length of a token New makes.
const Len = len(Prefix) + RandomLen + ChecksumLen

// base62 is the digits of base 62, in order.
const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// The errors Check returns.
var (
	// ErrMalformed means the token isn't ptd_ followed by 36 to 70 base62
	// characters.
	ErrMalformed = errors.New("devtoken: not a pairtime device token")
	// ErrChecksum means the token has the right shape, but its checksum
	// doesn't match the rest, as when a character was mistyped.
	ErrChecksum = errors.New("devtoken: the token's checksum doesn't match")
)

// New returns a new random device token.
func New() string {
	b := make([]byte, 0, Len)
	b = append(b, Prefix...)
	var buf [64]byte
	for len(b) < len(Prefix)+RandomLen {
		rand.Read(buf[:])
		for _, c := range buf {
			// 248 is the largest multiple of 62 a byte holds, so every
			// digit is equally likely.
			if c < 248 && len(b) < len(Prefix)+RandomLen {
				b = append(b, base62[c%62])
			}
		}
	}
	return string(appendChecksum(b, b))
}

// Check reports whether token is a well-formed device token whose checksum
// matches: ErrMalformed or ErrChecksum when it isn't. It doesn't say
// whether any server issued it.
func Check(token string) error {
	n := len(token) - len(Prefix) - ChecksumLen
	if n < MinRandomLen || n > MaxRandomLen || token[:len(Prefix)] != Prefix {
		return ErrMalformed
	}
	for i := len(Prefix); i < len(token); i++ {
		if !isBase62(token[i]) {
			return ErrMalformed
		}
	}
	body := token[:len(token)-ChecksumLen]
	var want [ChecksumLen]byte
	appendChecksum(want[:0], []byte(body))
	// The checksum guards against typos, not attackers, so a plain
	// comparison is enough: anyone can compute it.
	if string(want[:]) != token[len(body):] {
		return ErrChecksum
	}
	return nil
}

// appendChecksum appends the checksum of body to dst: its CRC-32 in base62,
// most significant digit first, padded with 0 to ChecksumLen digits.
// 62^6 is more than 2^32, so every CRC-32 fits.
func appendChecksum(dst, body []byte) []byte {
	sum := crc32.ChecksumIEEE(body)
	var digits [ChecksumLen]byte
	for i := ChecksumLen - 1; i >= 0; i-- {
		digits[i] = base62[sum%62]
		sum /= 62
	}
	return append(dst, digits[:]...)
}

func isBase62(c byte) bool {
	return '0' <= c && c <= '9' || 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z'
}
