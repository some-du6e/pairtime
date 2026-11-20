// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"bytes"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/some-du6e/pairtime/pkg/ledger/features"
)

// KeySize is the size of K_dev and of each key derived from it.
const KeySize = 32

// The HKDF info strings of spec section 3. A new ledger key takes a new
// label, and no label is ever used for two keys.
const (
	labelLeaf  = "pairtime/v1/leaf"
	labelToken = "pairtime/v1/token"
	labelFeat  = "pairtime/v1/feat"
	labelVault = "pairtime/v1/vault"
)

// nonceTag is the first byte of a nonce's HMAC input (spec 2, 4.4).
const nonceTag = 0x01

// Keys holds the four keys a device derives from K_dev (spec section 3).
// Like K_dev they never leave the device unencrypted (DESIGN 4.3, 7.11),
// and nothing may log them. The fields are unexported, so encoding/json
// writes Keys as {}, and Keys formats as a placeholder under every fmt
// verb. Hold it by pointer, as DeriveKeys returns it: fmt can't call
// Format on a Keys value kept in another struct's unexported field, and
// would print its bytes.
type Keys struct {
	leaf, token, feat, vault [KeySize]byte
}

// DeriveKeys derives K_leaf, K_tok, K_feat and K_vault from kDev, the
// device's 32-byte K_dev: HKDF-SHA256 with a salt of 32 zero bytes, which
// is RFC 5869's default, and L = 32 (spec section 3).
func DeriveKeys(kDev []byte) (*Keys, error) {
	if len(kDev) != KeySize {
		return nil, fmt.Errorf("ledger: K_dev is %d bytes, got %d", KeySize, len(kDev))
	}
	prk, err := extract(kDev, make([]byte, sha256.Size))
	if err != nil {
		return nil, err
	}
	k := new(Keys)
	for _, d := range []struct {
		key   *[KeySize]byte
		label string
	}{
		{&k.leaf, labelLeaf},
		{&k.token, labelToken},
		{&k.feat, labelFeat},
		{&k.vault, labelVault},
	} {
		okm, err := expand(prk, d.label, KeySize)
		if err != nil {
			clear(prk)
			return nil, err
		}
		copy(d.key[:], okm)
		clear(okm)
	}
	clear(prk)
	return k, nil
}

// extract is RFC 5869's HKDF-Extract over SHA-256.
func extract(ikm, salt []byte) ([]byte, error) {
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, fmt.Errorf("ledger: HKDF-Extract: %w", err)
	}
	return prk, nil
}

// expand is RFC 5869's HKDF-Expand over SHA-256.
func expand(prk []byte, info string, length int) ([]byte, error) {
	okm, err := hkdf.Expand(sha256.New, prk, info, length)
	if err != nil {
		return nil, fmt.Errorf("ledger: HKDF-Expand: %w", err)
	}
	return okm, nil
}

// LeafKey returns K_leaf. It keys the record nonces and nothing else in
// version 1 (spec 4.4).
func (k *Keys) LeafKey() [KeySize]byte { return k.leaf }

// TokenKey returns K_tok, which keys the token salts of content
// commitments (spec part 2).
func (k *Keys) TokenKey() [KeySize]byte { return k.token }

// FeatKey returns K_feat, which keys scorer features and error signatures
// (spec part 2; DESIGN 3.6).
func (k *Keys) FeatKey() [KeySize]byte { return k.feat }

// Features returns K_feat as a features.Key, which computes every keyed
// feature value of spec 10.5 and 10.6: the sketches, the 16-byte values
// and the edit hunks a body's f holds, and the error_sig values of
// agent.tool and shell.command events.
func (k *Keys) Features() *features.Key { return features.NewKey(k.feat) }

// VaultKey returns K_vault, which encrypts the vault (DESIGN 3.5).
func (k *Keys) VaultKey() [KeySize]byte { return k.vault }

// Format writes a placeholder whatever the verb, so printing or logging
// Keys never shows key material.
func (Keys) Format(f fmt.State, _ rune) { io.WriteString(f, "ledger.Keys{redacted}") }

// Nonce returns record s's nonce, HMAC-SHA256(K_leaf, 0x01 || u64(s))
// (spec 4.4). It goes inside the body, so a reader who sees only a body's
// hash can't confirm a guess of a short body. Only the device can compute
// it.
func (k *Keys) Nonce(s uint64) Hash {
	var msg [9]byte
	msg[0] = nonceTag
	binary.BigEndian.PutUint64(msg[1:], s)
	m := hmac.New(sha256.New, k.leaf[:])
	m.Write(msg[:])
	var n Hash
	m.Sum(n[:0])
	return n
}

// CheckNonce reports whether body, the stored body of record s, carries
// record s's nonce in its "n" member, comparing in constant time. It
// returns ErrNonce when it doesn't.
func (k *Keys) CheckNonce(s uint64, body []byte) error {
	n := k.Nonce(s)
	var want [2 * HashSize]byte
	hex.Encode(want[:], n[:])
	end := len(bodyPrefix) + len(want)
	if len(body) < end || !bytes.HasPrefix(body, bodyPrefix) ||
		subtle.ConstantTimeCompare(body[len(bodyPrefix):end], want[:]) != 1 {
		return ErrNonce
	}
	return nil
}
