// SPDX-License-Identifier: AGPL-3.0-or-later

package features

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"
)

// ShingleKind says how a prompt's shingles were cut, and is the tag byte of
// their per-shingle values (spec 2, 10.5).
type ShingleKind byte

const (
	// Word3 shingles are runs of three consecutive normalized words,
	// joined by single spaces, for prompts of six or more words.
	Word3 ShingleKind = 0x05
	// Char5 shingles are runs of five consecutive code points of the
	// normalized text, for prompts of fewer than six words.
	Char5 ShingleKind = 0x06
)

// String returns "word3" or "char5", the names the vectors use.
func (k ShingleKind) String() string {
	switch k {
	case Word3:
		return "word3"
	case Char5:
		return "char5"
	}
	return fmt.Sprintf("shingle kind 0x%02x", byte(k))
}

// Shingles returns the shingles of normalized words (spec 10.5, SM 8.5):
// the word 3-shingles when there are six or more words, and otherwise the
// character 5-shingles of the normalized text. A normalized text of one to
// four code points is its own single shingle, and the empty text has none.
// The shingles form a set, returned in ascending order of their UTF-8
// bytes.
func Shingles(words []string) (ShingleKind, []string) {
	set := map[string]bool{}
	if len(words) >= 6 {
		for i := 0; i+3 <= len(words); i++ {
			set[strings.Join(words[i:i+3], " ")] = true
		}
		return Word3, slices.Sorted(maps.Keys(set))
	}
	text := strings.Join(words, " ")
	if utf8.RuneCountInString(text) < 5 {
		if text == "" {
			return Char5, nil
		}
		return Char5, []string{text}
	}
	rs := []rune(text)
	for i := 0; i+5 <= len(rs); i++ {
		set[string(rs[i:i+5])] = true
	}
	return Char5, slices.Sorted(maps.Keys(set))
}

// ShingleValue is a shingle's per-shingle value, v(x) = HMAC-SHA256(K_feat,
// kind || x) (spec 10.5). Its text form is 64 lowercase hex digits.
type ShingleValue [32]byte

// String returns v as 64 lowercase hex digits.
func (v ShingleValue) String() string { return hex.EncodeToString(v[:]) }

// SketchSize is the number of MinHash values in a sketch.
const SketchSize = 64

// noMin is a sketch place no shingle has set: the empty set's value.
const noMin = 0xffffffff

// minhashTag starts each SHA-256 that expands a per-shingle value into
// MinHash values (spec 2, 10.5).
const minhashTag = 0x05

// Sketch is a prompt's keyed MinHash, mh[0] to mh[63] (spec 10.5). Its
// text form, which JSON uses, is the 256 bytes of the 64 values in order,
// each big-endian, as 512 lowercase hex digits.
type Sketch [SketchSize]uint32

// EmptySketch returns the sketch of the empty set: 0xffffffff in all 64
// places.
func EmptySketch() Sketch {
	var s Sketch
	for i := range s {
		s[i] = noMin
	}
	return s
}

// MinHashValues returns the 64 MinHash values of one shingle: value 8k + r
// is bytes 4r to 4r+3 of SHA-256(0x05 || v || u8(k)), big-endian, for k
// and r from 0 to 7 (spec 10.5). CC8 recomputes them from revealed values.
func MinHashValues(v ShingleValue) Sketch {
	var out Sketch
	var in [1 + 32 + 1]byte
	in[0] = minhashTag
	copy(in[1:], v[:])
	for k := 0; k < 8; k++ {
		in[33] = byte(k)
		b := sha256.Sum256(in[:])
		for r := 0; r < 8; r++ {
			out[8*k+r] = binary.BigEndian.Uint32(b[4*r:])
		}
	}
	return out
}

// SketchOf returns the sketch of a set of per-shingle values: in each of
// the 64 places, the smallest of the shingles' MinHash values there, or
// 0xffffffff for no shingles.
func SketchOf(values []ShingleValue) Sketch {
	s := EmptySketch()
	for _, v := range values {
		h := MinHashValues(v)
		for i := range s {
			s[i] = min(s[i], h[i])
		}
	}
	return s
}

// Bytes returns the sketch's 256 bytes: the 64 values in order, each
// big-endian.
func (s Sketch) Bytes() []byte {
	out := make([]byte, 4*SketchSize)
	for i, x := range s {
		binary.BigEndian.PutUint32(out[4*i:], x)
	}
	return out
}

// SketchFrom reads a sketch from its 256 bytes.
func SketchFrom(b []byte) (Sketch, error) {
	var s Sketch
	if len(b) != 4*SketchSize {
		return s, fmt.Errorf("features: a sketch is %d bytes, got %d", 4*SketchSize, len(b))
	}
	for i := range s {
		s[i] = binary.BigEndian.Uint32(b[4*i:])
	}
	return s, nil
}

// String returns the sketch as 512 lowercase hex digits.
func (s Sketch) String() string { return hex.EncodeToString(s.Bytes()) }

// MarshalText returns the sketch as 512 lowercase hex digits.
func (s Sketch) MarshalText() ([]byte, error) { return hex.AppendEncode(nil, s.Bytes()), nil }

// UnmarshalText reads a sketch from exactly 512 lowercase hex digits, the
// one form MarshalText writes.
func (s *Sketch) UnmarshalText(text []byte) error {
	b, err := decodeHex(text, 4*SketchSize)
	if err != nil {
		return fmt.Errorf("features: a sketch is %d lowercase hex digits", 8*SketchSize)
	}
	*s, err = SketchFrom(b)
	return err
}

// Matches returns m, the number of places at which s and o are equal. The
// estimate of the Jaccard similarity of their prompts is m / 64 (spec
// 10.5). Two empty sketches are equal everywhere, as SM 4.2 has two empty
// normalized texts identical.
func (s Sketch) Matches(o Sketch) int {
	m := 0
	for i := range s {
		if s[i] == o[i] {
			m++
		}
	}
	return m
}

// Similar reports whether s and o pass a Jaccard threshold of num / den,
// such as SIM_JACCARD, in exact rationals: 64 × num ≤ m × den (spec 10.5).
// At 0.8 that is m ≥ 52. den must be positive.
func Similar(s, o Sketch, num, den int64) bool {
	return SketchSize*num <= int64(s.Matches(o))*den
}

// decodeHex decodes exactly n bytes from lowercase hex.
func decodeHex(text []byte, n int) ([]byte, error) {
	if len(text) != 2*n || !isLowerHex(text) {
		return nil, fmt.Errorf("features: want %d lowercase hex digits", 2*n)
	}
	return hex.AppendDecode(nil, text)
}

func isLowerHex(b []byte) bool {
	for _, c := range b {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}
