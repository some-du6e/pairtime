// SPDX-License-Identifier: AGPL-3.0-or-later

package features

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// ShingleEntry is one entry of a CC8 reveal (spec 10.8): the per-shingle
// value of a shingle that touches no removed span, with the shingle's
// text. A shingle that touches a removed span gets no entry, because its
// value, the same in every text of the device, would undo the removal. A
// revealed entry comes from the client, so its value may be any length and
// its text any bytes; VerifyShingleReveal checks them.
type ShingleEntry struct {
	// V is the per-shingle value, 32 bytes in a well-formed entry.
	V []byte
	// X is the shingle's UTF-8 bytes. An empty X, or one that isn't valid
	// UTF-8, is bad_entry.
	X []byte
}

type shingleEntryJSON struct {
	V string `json:"v"`
	X string `json:"x"`
}

// MarshalJSON writes the entry as {"v": hex, "x": hex}.
func (e ShingleEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(shingleEntryJSON{V: hex.EncodeToString(e.V), X: hex.EncodeToString(e.X)})
}

// UnmarshalJSON reads an entry. It must have exactly the members "v" and
// "x", each lowercase hex. Names match exactly: encoding/json alone would
// also read "V" as v and "X" as x.
func (e *ShingleEntry) UnmarshalJSON(data []byte) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil || obj == nil {
		return fmt.Errorf("features: a shingle entry isn't an object")
	}
	var out ShingleEntry
	for _, m := range []struct {
		name string
		dst  *[]byte
	}{{"v", &out.V}, {"x", &out.X}} {
		raw, ok := obj[m.name]
		var s string
		if !ok || isNull(raw) || json.Unmarshal(raw, &s) != nil {
			return fmt.Errorf("features: a shingle entry has no %s string", m.name)
		}
		b, err := lowerHex(s)
		if err != nil {
			return err
		}
		*m.dst = b
	}
	if len(obj) != 2 {
		return fmt.Errorf("features: a shingle entry has members besides v and x")
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
	// sketch when nothing was removed, or, with spans removed, has a value
	// below the committed sketch somewhere.
	ShingleSketch ShingleVerdict = "sketch"
	// ShingleOK is a reveal that passes both checks.
	ShingleOK ShingleVerdict = "ok"
)

// VerifyShingleReveal checks a CC8 reveal against mh, the committed sketch
// of a challenged prompt or track call (spec 10.8). removed says whether
// the text's reveal removed any span. It returns the first verdict that
// applies, with the index of the offending entry for ShingleBadEntry and
// -1 otherwise. For ShingleOK it also returns unexplained, the number of
// places at which no shown value reaches mh: with spans removed, the
// places that shingles touching them set; with nothing removed, 0.
//
// With nothing removed, the values must give mh exactly. With spans
// removed, the shingles that touch one send nothing, so the check is that
// no shown value's MinHash value is below mh anywhere, which no shingle of
// the text could give, because mh holds the least of them.
//
// An ok reveal proves less than DESIGN 7.3 assumes: the server can't check
// a value against K_feat, so a client that committed a sketch under a key
// it made up can reveal values under that key. CC8 (WP-TR2) adds the checks
// against the revealed text and across a device's reveals that spec 10.8
// describes.
func VerifyShingleReveal(mh Sketch, entries []ShingleEntry, removed bool) (verdict ShingleVerdict, at, unexplained int) {
	values := make([]ShingleValue, len(entries))
	for i, e := range entries {
		if len(e.V) != len(ShingleValue{}) || i > 0 && bytes.Compare(entries[i-1].V, e.V) >= 0 {
			return ShingleBadEntry, i, 0
		}
		if len(e.X) == 0 || !utf8.Valid(e.X) {
			return ShingleBadEntry, i, 0
		}
		copy(values[i][:], e.V)
	}
	shown := SketchOf(values)
	for i := range shown {
		if removed && shown[i] < mh[i] || !removed && shown[i] != mh[i] {
			return ShingleSketch, -1, 0
		}
	}
	for i := range shown {
		if shown[i] != mh[i] {
			unexplained++
		}
	}
	return ShingleOK, -1, unexplained
}

// ShingleReveal returns the CC8 reveal the client sends for text, a
// challenged prompt or track label, when the text's redacted reveal
// removed the tokens removed, numbered from 0 in the order Tokens gives
// them (spec 10.8). It returns the kind of text's shingles, and the value
// and text of each distinct shingle that touches no removed span, in
// ascending order of the value compared byte by byte. A shingle that
// touches a removed span gets no entry. With removed empty, every shingle
// is shown.
func (k *Key) ShingleReveal(text string, removed []int) (ShingleKind, []ShingleEntry) {
	gone := make(map[int]bool, len(removed))
	for _, j := range removed {
		gone[j] = true
	}
	n, sources := normalizeSources(text)
	dirty := make([]bool, len(n.Words))
	for i, src := range sources {
		dirty[i] = slices.ContainsFunc(src, func(j int) bool { return gone[j] })
	}
	kind, shown := shownShingles(n.Words, dirty)
	out := make([]ShingleEntry, len(shown))
	for i, x := range shown {
		v := k.ShingleValue(kind, x)
		out[i] = ShingleEntry{V: bytes.Clone(v[:]), X: []byte(x)}
	}
	slices.SortFunc(out, func(a, b ShingleEntry) int { return bytes.Compare(a.V, b.V) })
	return kind, out
}

// shownShingles returns the kind of the shingles of words and the ones
// that touch no removed span, in ascending order of their UTF-8 bytes
// (spec 10.8). dirty[i] says whether word i comes from a removed token. A
// word shingle touches a removed span when every run of three words that
// makes it holds a dirty word. A character shingle touches one when every
// place it occurs holds a code point of a dirty word; the single spaces
// between words come from no token. So a shingle is shown exactly when it
// occurs somewhere built from kept tokens alone.
func shownShingles(words []string, dirty []bool) (ShingleKind, []string) {
	kind, all := Shingles(words)
	clean := map[string]bool{}
	if kind == Word3 {
		for i := 0; i+3 <= len(words); i++ {
			if !dirty[i] && !dirty[i+1] && !dirty[i+2] {
				clean[strings.Join(words[i:i+3], " ")] = true
			}
		}
	} else {
		// owner is the word each code point of text comes from, or -1 for
		// a space between words. A text of one to four code points is its
		// own single shingle, as Shingles has it.
		text := strings.Join(words, " ")
		rs := []rune(text)
		owner := make([]int, 0, len(rs))
		for i, w := range words {
			if i > 0 {
				owner = append(owner, -1)
			}
			for range []rune(w) {
				owner = append(owner, i)
			}
		}
		size := min(5, len(rs))
		for i := 0; size > 0 && i+size <= len(rs); i++ {
			if slices.ContainsFunc(owner[i:i+size], func(o int) bool { return o >= 0 && dirty[o] }) {
				continue
			}
			if size < 5 {
				clean[text] = true
			} else {
				clean[string(rs[i:i+size])] = true
			}
		}
	}
	var shown []string
	for _, x := range all {
		if clean[x] {
			shown = append(shown, x)
		}
	}
	return kind, shown
}
