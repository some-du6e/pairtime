// SPDX-License-Identifier: AGPL-3.0-or-later

package features

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"unicode/utf8"
)

// ShingleEntry is one entry of a CC8 reveal (spec 10.8): a per-shingle
// value, with its shingle's text unless the shingle touches a removed
// span. A revealed entry comes from the client, so its value may be any
// length; VerifyShingleReveal checks it.
type ShingleEntry struct {
	// V is the per-shingle value, 32 bytes in a well-formed entry.
	V []byte
	// X is the shingle's UTF-8 bytes, or nil when the entry doesn't show
	// the shingle. An entry that shows an empty shingle has X non-nil and
	// empty, which is bad_entry.
	X []byte
}

type shingleEntryJSON struct {
	V string  `json:"v"`
	X *string `json:"x,omitempty"`
}

// MarshalJSON writes the entry as {"v": hex, "x": hex}, without "x" when it
// doesn't show the shingle.
func (e ShingleEntry) MarshalJSON() ([]byte, error) {
	j := shingleEntryJSON{V: hex.EncodeToString(e.V)}
	if e.X != nil {
		x := hex.EncodeToString(e.X)
		j.X = &x
	}
	return json.Marshal(j)
}

// UnmarshalJSON reads an entry, refusing a member other than "v" and "x"
// and a value that isn't lowercase hex.
func (e *ShingleEntry) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var j struct {
		V *string `json:"v"`
		X *string `json:"x"`
	}
	if err := dec.Decode(&j); err != nil {
		return fmt.Errorf("features: a shingle entry: %v", err)
	}
	if j.V == nil {
		return fmt.Errorf("features: a shingle entry has no v")
	}
	v, err := lowerHex(*j.V)
	if err != nil {
		return err
	}
	out := ShingleEntry{V: v}
	if j.X != nil {
		if out.X, err = lowerHex(*j.X); err != nil {
			return err
		}
		if out.X == nil {
			out.X = []byte{}
		}
	}
	*e = out
	return nil
}

// lowerHex decodes s, which must be lowercase hex of whole bytes.
func lowerHex(s string) ([]byte, error) {
	if len(s)%2 != 0 || !isLowerHex([]byte(s)) {
		return nil, fmt.Errorf("features: want lowercase hex, got %d characters that aren't", len(s))
	}
	return hex.DecodeString(s)
}

// ShingleVerdict is the outcome of checking a CC8 reveal against a
// record's committed sketch (spec 10.8).
type ShingleVerdict string

// The verdicts of spec 10.8, in the order they are checked.
const (
	// ShingleBadEntry is an entry whose value isn't 32 bytes or isn't
	// greater than the one before it, or that shows an empty shingle or one
	// that isn't valid UTF-8.
	ShingleBadEntry ShingleVerdict = "bad_entry"
	// ShingleSketch is a reveal whose values don't give the committed
	// sketch.
	ShingleSketch ShingleVerdict = "sketch"
	// ShingleOK is a reveal that passes both checks.
	ShingleOK ShingleVerdict = "ok"
)

// VerifyShingleReveal checks a CC8 reveal against mh, the committed sketch
// of a challenged prompt or track call (spec 10.8). It returns the first
// verdict that applies, with the index of the offending entry for
// ShingleBadEntry and -1 otherwise.
//
// An ok reveal proves less than DESIGN 7.3 assumes: the server can't check
// a value against K_feat, so a client that committed a sketch under a key
// it made up can reveal values under that key. CC8 (WP-TR2) adds the checks
// across a device's reveals that spec 10.8 describes.
func VerifyShingleReveal(mh Sketch, entries []ShingleEntry) (ShingleVerdict, int) {
	values := make([]ShingleValue, len(entries))
	for i, e := range entries {
		if len(e.V) != len(ShingleValue{}) || i > 0 && bytes.Compare(entries[i-1].V, e.V) >= 0 {
			return ShingleBadEntry, i
		}
		if e.X != nil && (len(e.X) == 0 || !utf8.Valid(e.X)) {
			return ShingleBadEntry, i
		}
		copy(values[i][:], e.V)
	}
	if SketchOf(values) != mh {
		return ShingleSketch, -1
	}
	return ShingleOK, -1
}

// ShingleReveal returns the CC8 reveal of text, a challenged prompt or
// track label: every distinct shingle's value in ascending order of the
// value compared byte by byte, each with its shingle's text unless hide
// says the shingle touches a removed span. hide may be nil, which shows
// every shingle.
func (k *Key) ShingleReveal(text string, hide func(shingle string) bool) []ShingleEntry {
	_, shingles := k.Shingles(text)
	slices.SortFunc(shingles, func(a, b Shingle) int { return bytes.Compare(a.V[:], b.V[:]) })
	out := make([]ShingleEntry, len(shingles))
	for i, s := range shingles {
		out[i] = ShingleEntry{V: bytes.Clone(s.V[:])}
		if hide == nil || !hide(s.X) {
			out[i].X = []byte(s.X)
		}
	}
	return out
}
