// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"bytes"
	"cmp"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"unicode/utf8"

	"github.com/some-du6e/pairtime/pkg/ledger/features"
)

// SpanType says what removed a span from a reveal (spec 10.3). The types
// are a closed list, so a type can't carry free text.
type SpanType string

// The span types of spec 10.3. Adding one means changing the spec's table.
const (
	// SpanKnown is the teen's known values: account name, home directory,
	// hostname, git name and email, GitHub and HCA usernames, private repo
	// and folder names, the deny list.
	SpanKnown SpanType = "known"
	// SpanSecret is gitleaks' rules.
	SpanSecret SpanType = "secret"
	// SpanPII is the validated PII patterns ported from Presidio.
	SpanPII SpanType = "pii"
	// SpanCode is a fenced code block collapsed in an agent message.
	SpanCode SpanType = "code"
	// SpanTeen is the teen, by hand.
	SpanTeen SpanType = "teen"
)

// Valid reports whether t is in spec 10.3's table.
func (t SpanType) Valid() bool {
	switch t {
	case SpanKnown, SpanSecret, SpanPII, SpanCode, SpanTeen:
		return true
	}
	return false
}

// RevealItem is one item of a redacted reveal, in token order (spec 10.3):
// a kept token with its salt, or a removed span of consecutive tokens shown
// as their leaf hashes, with a type and a count. A reveal comes from the
// client, so its byte strings may be any length; VerifyReveal checks them.
type RevealItem struct {
	// Removed is set for a removed span and clear for a kept token.
	Removed bool

	// Salt and Token are a kept token's salt, 32 bytes when well formed,
	// and its UTF-8 bytes.
	Salt, Token []byte

	// Type, Count and Leaves are a removed span's type, the number of
	// tokens it covers, and their leaves, Count values of 32 bytes when
	// well formed.
	Type   SpanType
	Count  uint64
	Leaves [][]byte
	// Lines is a code span's number of lines, nil when the item gives
	// none. It is the client's claim, and nothing checks it.
	Lines *uint64
}

type keptJSON struct {
	Salt  string `json:"salt"`
	Token string `json:"token"`
}

type spanJSON struct {
	Type   SpanType `json:"type"`
	Count  uint64   `json:"count"`
	Leaves []string `json:"leaves"`
	Lines  *uint64  `json:"lines,omitempty"`
}

// MarshalJSON writes a kept token as {"salt": hex, "token": hex} and a
// removed span as {"type", "count", "leaves", "lines"}, without "lines"
// unless the item gives it. The token is hex of its UTF-8 bytes, so JSON
// string escaping can't change them.
func (it RevealItem) MarshalJSON() ([]byte, error) {
	if !it.Removed {
		return json.Marshal(keptJSON{Salt: hex.EncodeToString(it.Salt), Token: hex.EncodeToString(it.Token)})
	}
	leaves := make([]string, len(it.Leaves))
	for i, h := range it.Leaves {
		leaves[i] = hex.EncodeToString(h)
	}
	return json.Marshal(spanJSON{Type: it.Type, Count: it.Count, Leaves: leaves, Lines: it.Lines})
}

// UnmarshalJSON reads an item: one with a "salt" member is a kept token
// and must have exactly "salt" and "token"; any other is a removed span
// with "type", "count" and "leaves", and maybe "lines". Byte strings must
// be lowercase hex. A null "leaves" is an empty list.
func (it *RevealItem) UnmarshalJSON(data []byte) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil || obj == nil {
		return errors.New("ledger: a reveal item isn't a JSON object")
	}
	if _, ok := obj["salt"]; ok {
		if err := exactMembers(obj, []string{"salt", "token"}, nil); err != nil {
			return err
		}
		var j keptJSON
		if err := json.Unmarshal(data, &j); err != nil {
			return fmt.Errorf("ledger: a kept token: %v", err)
		}
		salt, err := hexField("salt", j.Salt)
		if err != nil {
			return err
		}
		token, err := hexField("token", j.Token)
		if err != nil {
			return err
		}
		*it = RevealItem{Salt: salt, Token: token}
		return nil
	}
	if err := exactMembers(obj, []string{"type", "count", "leaves"}, []string{"lines"}); err != nil {
		return err
	}
	var j spanJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return fmt.Errorf("ledger: a removed span: %v", err)
	}
	out := RevealItem{Removed: true, Type: j.Type, Count: j.Count, Lines: j.Lines, Leaves: make([][]byte, len(j.Leaves))}
	for i, h := range j.Leaves {
		var err error
		if out.Leaves[i], err = hexField("leaf", h); err != nil {
			return err
		}
	}
	*it = out
	return nil
}

// exactMembers refuses an object that lacks a required member, holds one
// that is neither required nor optional, or holds a null other than a null
// "leaves", which is an empty list.
func exactMembers(obj map[string]json.RawMessage, required, optional []string) error {
	for _, name := range required {
		if _, ok := obj[name]; !ok {
			return fmt.Errorf("ledger: a reveal item has no %q", name)
		}
	}
	for name, raw := range obj {
		if !slices.Contains(required, name) && !slices.Contains(optional, name) {
			return fmt.Errorf("ledger: a reveal item has a member %q it doesn't define", name)
		}
		if name != "leaves" && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("ledger: a reveal item's %q is null", name)
		}
	}
	return nil
}

// hexField decodes a byte string written as lowercase hex.
func hexField(name, s string) ([]byte, error) {
	if len(s)%2 != 0 || !isLowerHex([]byte(s)) {
		return nil, fmt.Errorf("ledger: a reveal item's %s isn't lowercase hex", name)
	}
	return hex.DecodeString(s)
}

// RevealVerdict is the outcome of checking a reveal against a record's
// commitment (spec 10.3).
type RevealVerdict string

// The verdicts of spec 10.3, in the order they are checked.
const (
	// RevealBadItem is an item that isn't well formed: for a kept token, a
	// salt that isn't 32 bytes, or a token that is empty, isn't valid
	// UTF-8 or mixes whitespace with other code points; for a span, a type
	// not in the table, a count below 1, leaves that aren't count values
	// of 32 bytes, or lines on a span that isn't code, or below 1.
	RevealBadItem RevealVerdict = "bad_item"
	// RevealCount is a reveal whose kept tokens and span counts don't add
	// up to the committed n.
	RevealCount RevealVerdict = "count"
	// RevealAlternation is two kept tokens that break strict alternation:
	// at positions p < q, they must be of the same kind exactly when q - p
	// is even.
	RevealAlternation RevealVerdict = "alternation"
	// RevealRoot is a reveal whose leaves don't give the committed C.
	RevealRoot RevealVerdict = "root"
	// RevealOK is a reveal that passes every check. It proves that each
	// kept token is the committed token at its position, and that each span
	// covers exactly count committed tokens. It proves nothing about what a
	// span held or why it was removed.
	RevealOK RevealVerdict = "ok"
)

// VerifyReveal checks items, a redacted reveal, against c, the commitment
// in the body of the record the reveal names, which the metadata reveal
// has already tied to a witnessed batch (spec 10.3). It returns the first
// verdict that applies, with the index of the offending item for
// RevealBadItem and RevealAlternation, and -1 otherwise.
func VerifyReveal(c Commitment, items []RevealItem) (RevealVerdict, int) {
	type kept struct {
		pos   uint64
		space bool
		i     int
	}
	var ks []kept
	var leaves []Hash
	var pos uint64
	for i, it := range items {
		if !it.Removed {
			space, ok := tokenKind(it.Token)
			if len(it.Salt) != HashSize || !ok {
				return RevealBadItem, i
			}
			var salt Hash
			copy(salt[:], it.Salt)
			ks = append(ks, kept{pos: pos, space: space, i: i})
			leaves = append(leaves, TokenLeaf(salt, it.Token))
			pos++
			continue
		}
		if !it.Type.Valid() || it.Count < 1 || uint64(len(it.Leaves)) != it.Count {
			return RevealBadItem, i
		}
		if slices.ContainsFunc(it.Leaves, func(h []byte) bool { return len(h) != HashSize }) {
			return RevealBadItem, i
		}
		if it.Lines != nil && (it.Type != SpanCode || *it.Lines < 1) {
			return RevealBadItem, i
		}
		for _, h := range it.Leaves {
			var leaf Hash
			copy(leaf[:], h)
			leaves = append(leaves, leaf)
		}
		pos += it.Count
	}
	if pos != c.N {
		return RevealCount, -1
	}
	for j := 1; j < len(ks); j++ {
		a, b := ks[j-1], ks[j]
		if (a.space == b.space) != ((b.pos-a.pos)%2 == 0) {
			return RevealAlternation, b.i
		}
	}
	if Root(leaves) != c.R {
		return RevealRoot, -1
	}
	return RevealOK, -1
}

// tokenKind reports whether tok is a whitespace token or a word, and false
// for ok when tok isn't one token: empty, not valid UTF-8, or both kinds at
// once.
func tokenKind(tok []byte) (space, ok bool) {
	if len(tok) == 0 || !utf8.Valid(tok) {
		return false, false
	}
	r, _ := utf8.DecodeRune(tok)
	space = features.IsSpace(r)
	for _, r := range string(tok) {
		if features.IsSpace(r) != space {
			return false, false
		}
	}
	return space, true
}

// Span is a run of consecutive tokens to remove from a reveal: Count
// tokens from First, numbered from 0, with the type of what removed them
// (spec 10.3). pkg/redact finds spans as character ranges and rounds each
// out to whole tokens.
type Span struct {
	First, Count int
	Type         SpanType
	// Lines is a code span's number of lines, or 0 to give none. Only a
	// code span may give one.
	Lines int
}

// Reveal returns the reveal of the opened text that removes spans, in any
// order, and keeps every other token with its salt (spec 10.3). It refuses
// a span of an unknown type or of no tokens, lines on a span that isn't
// code, and spans that overlap or run past the text. Adjacent spans stay
// separate items.
func (o *Opening) Reveal(spans []Span) ([]RevealItem, error) {
	sorted := slices.Clone(spans)
	slices.SortFunc(sorted, func(a, b Span) int { return cmp.Compare(a.First, b.First) })
	var items []RevealItem
	j := 0
	keep := func(to int) {
		for ; j < to; j++ {
			items = append(items, RevealItem{Salt: bytes.Clone(o.salts[j][:]), Token: bytes.Clone(o.tokens[j])})
		}
	}
	for _, sp := range sorted {
		switch {
		case !sp.Type.Valid():
			return nil, fmt.Errorf("ledger: %q isn't a span type", sp.Type)
		case sp.Count < 1:
			return nil, errors.New("ledger: a span covers at least one token")
		case sp.Lines < 0 || sp.Lines > 0 && sp.Type != SpanCode:
			return nil, errors.New("ledger: only a code span gives a line count")
		case sp.First < j || sp.First > len(o.tokens)-sp.Count:
			return nil, errors.New("ledger: spans overlap or run past the text")
		}
		keep(sp.First)
		it := RevealItem{Removed: true, Type: sp.Type, Count: uint64(sp.Count), Leaves: make([][]byte, sp.Count)}
		for i := range it.Leaves {
			it.Leaves[i] = bytes.Clone(o.leaves[sp.First+i][:])
		}
		if sp.Lines > 0 {
			lines := uint64(sp.Lines)
			it.Lines = &lines
		}
		items = append(items, it)
		j = sp.First + sp.Count
	}
	keep(len(o.tokens))
	return items, nil
}
