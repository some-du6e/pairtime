// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/ledger/features"
)

// MessageMax is the most bytes of a turn's final agent message that its
// record commits: the "first 16 KiB" the vault keeps (spec 10.1; DESIGN
// 3.5).
const MessageMax = 16384

// CommitsText reports whether records of kind k can commit a text (spec
// 10.1): prompt.submit, agent.output when it is the turn's final message,
// and cli.track. No other record has a "c" member.
func CommitsText(k event.Kind) bool {
	return k == event.KindPromptSubmit || k == event.KindAgentOutput || k == event.KindCLITrack
}

// CommittedMessage returns the text a turn's final agent message commits:
// raw itself when it is at most MessageMax bytes, and otherwise its longest
// prefix of at most MessageMax bytes that ends on a character boundary
// (spec 10.1). Prompts and labels commit in full.
func CommittedMessage(raw []byte) []byte {
	if len(raw) <= MessageMax {
		return raw
	}
	k := MessageMax
	for k > 0 && raw[k]&0xc0 == 0x80 {
		k--
	}
	return raw[:k]
}

// ErrText is a text a record can't commit: one that isn't valid UTF-8 as
// RFC 3629 defines it (spec 10.1). A writer makes such text valid before
// committing it.
var ErrText = errors.New("ledger: text isn't valid UTF-8, and a record commits only valid UTF-8")

// Commitment is a record's content commitment, the body member "c": the
// number of tokens n and the root C of the tree over their leaves (spec
// 10.2). Committing n with C fixes the tree's shape, so a reveal can't pass
// a subtree's hash off as one removed token.
type Commitment struct {
	N uint64 `json:"n"`
	R Hash   `json:"r"`
}

// TokenSalt returns salt_j = HMAC-SHA256(K_tok, u64(s) || u64(j)), the
// salt of token j, numbered from 1, of record s's committed text (spec
// 10.2). Each salt is its own HMAC, and a reveal shows only the salts of
// kept tokens, so a removed token's leaf can't be used to test a guess.
func (k *Keys) TokenSalt(s, j uint64) Hash {
	var msg [16]byte
	binary.BigEndian.PutUint64(msg[:8], s)
	binary.BigEndian.PutUint64(msg[8:], j)
	m := hmac.New(sha256.New, k.token[:])
	m.Write(msg[:])
	var salt Hash
	m.Sum(salt[:0])
	return salt
}

// TokenLeaf returns t = SHA-256(0x00 || salt || token), which is
// LEAF(salt || token), the leaf of one token in its text's tree (spec
// 10.2).
func TokenLeaf(salt Hash, token []byte) Hash { return sum([]byte{leafTag}, salt[:], token) }

// Opening is what opens one committed text: its tokens with their salts
// and leaves. The device builds it from the text in its vault when a
// record is drawn, and makes reveals from it. It holds the text and every
// salt, so it stays on the device: its fields are unexported, and it
// formats as a placeholder under every fmt verb. Hold it by pointer, as
// Keys.Open returns it.
type Opening struct {
	tokens [][]byte
	salts  []Hash
	leaves []Hash
	c      Commitment
}

// Open returns the opening of text as record s commits it (spec 10.2):
// the text's tokens, the salt and leaf of each, and the commitment. It
// refuses s = 0 and a text that isn't valid UTF-8.
func (k *Keys) Open(s uint64, text []byte) (*Opening, error) {
	if s == 0 {
		return nil, fmt.Errorf("ledger: sequence numbers start at 1")
	}
	if !utf8.Valid(text) {
		return nil, ErrText
	}
	toks := features.Tokens(string(text))
	o := &Opening{tokens: make([][]byte, len(toks)), salts: make([]Hash, len(toks)), leaves: make([]Hash, len(toks))}
	for i, t := range toks {
		o.tokens[i] = []byte(t)
		o.salts[i] = k.TokenSalt(s, uint64(i+1))
		o.leaves[i] = TokenLeaf(o.salts[i], o.tokens[i])
	}
	o.c = Commitment{N: uint64(len(toks)), R: Root(o.leaves)}
	return o, nil
}

// Commit returns record s's commitment to text (spec 10.2). With no
// tokens, C is the SHA-256 of no bytes, the same for every empty text.
func (k *Keys) Commit(s uint64, text []byte) (Commitment, error) {
	o, err := k.Open(s, text)
	if err != nil {
		return Commitment{}, err
	}
	return o.c, nil
}

// Commitment returns the commitment the opening opens.
func (o *Opening) Commitment() Commitment { return o.c }

// Len returns the number of tokens.
func (o *Opening) Len() int { return len(o.tokens) }

// Format writes a placeholder whatever the verb, so printing or logging an
// Opening never shows the text or a salt.
func (Opening) Format(f fmt.State, _ rune) { io.WriteString(f, "ledger.Opening{redacted}") }
