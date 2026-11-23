// SPDX-License-Identifier: AGPL-3.0-or-later

package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"fmt"
)

// The vault's cipher is AES-256-GCM with a 96-bit nonce and a 128-bit tag
// (DESIGN 3.5; docs/PLAN.md, WP-LG6), and Wycheproof's aes_gcm_test.json
// checks it (gcm_test.go).
const (
	keySize   = 32 // AES-256
	nonceSize = 12 // 96 bits, drawn at random for each entry
	tagSize   = 16 // 128 bits
)

// errSealed is an entry that doesn't open: its tag doesn't match its key,
// nonce, associated data and ciphertext, as when the entry was changed or
// another device key sealed it.
var errSealed = errors.New("vault: an entry doesn't open under this device key")

// gcm is AES-256-GCM with the vault's sizes. It refuses every other key
// and nonce size, so nothing can seal an entry under a shorter key or with
// a nonce GCM would have to hash first.
type gcm struct{ aead cipher.AEAD }

// newGCM returns the cipher of key, which must be 32 bytes.
func newGCM(key []byte) (*gcm, error) {
	if len(key) != keySize {
		return nil, fmt.Errorf("vault: an AES-256 key is %d bytes, not %d", keySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &gcm{aead: aead}, nil
}

// seal appends to dst the ciphertext of plaintext under nonce, with ad as
// its associated data, followed by its 16-byte tag. The nonce must be 12
// bytes, and no two calls under one key may share one.
func (g *gcm) seal(dst, nonce, plaintext, ad []byte) ([]byte, error) {
	if len(nonce) != nonceSize {
		return nil, fmt.Errorf("vault: a nonce is %d bytes, not %d", nonceSize, len(nonce))
	}
	return g.aead.Seal(dst, nonce, plaintext, ad), nil
}

// open appends to dst the plaintext of sealed, a ciphertext followed by its
// tag, under nonce and ad. It returns errSealed when the tag doesn't match,
// and an error for a nonce that isn't 12 bytes or a sealed text shorter
// than a tag.
func (g *gcm) open(dst, nonce, sealed, ad []byte) ([]byte, error) {
	if len(nonce) != nonceSize {
		return nil, fmt.Errorf("vault: a nonce is %d bytes, not %d", nonceSize, len(nonce))
	}
	if len(sealed) < tagSize {
		return nil, fmt.Errorf("%w: %d bytes is shorter than a tag", errSealed, len(sealed))
	}
	out, err := g.aead.Open(dst, nonce, sealed, ad)
	if err != nil {
		return nil, errSealed
	}
	return out, nil
}
