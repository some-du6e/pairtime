// SPDX-License-Identifier: AGPL-3.0-or-later

package accounts

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// EmailKeySize is the size of the secret PAIRTIME_EMAIL_KEY.
const EmailKeySize = 32

// sealedFormat is the first byte of a sealed email.
const sealedFormat = 1

// keyIDSize is the size of the key id a sealed email starts with after its
// format byte.
const keyIDSize = 4

// EmailCipher encrypts email addresses at rest (DESIGN 4.4) and computes the
// keyed index that finds an account by email (DESIGN 8.9).
//
// A sealed email is a format byte, a 4-byte id of the key, a 12-byte nonce
// and the AES-256-GCM ciphertext, whose additional data binds it to the
// account's HCA subject, so ciphertext copied to another row doesn't open.
// The encryption key, the index key and the key id are each derived from
// PAIRTIME_EMAIL_KEY with HKDF-SHA256 under their own label.
//
// Losing PAIRTIME_EMAIL_KEY loses data. HCA sends the email at every
// sign-in, so an account's address comes back, sealed under the new key,
// the next time its owner signs in. Until then, and for good when the
// owner never signs in again, the address can't be read, and its
// email_index no longer matches it, so a privacy-hub request that names
// the person by email (DESIGN 8.9) doesn't find the account. The key needs
// the same backup as the keys in /data/keys (DESIGN 10.2).
type EmailCipher struct {
	aead     cipher.AEAD
	indexKey []byte
	keyID    [keyIDSize]byte
}

// ParseEmailKey decodes PAIRTIME_EMAIL_KEY: 32 bytes written as 64 hex
// digits, or as base64, standard or URL-safe, with or without padding.
// "openssl rand -hex 32" makes one. Errors never quote the value.
func ParseEmailKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if len(s) == 2*EmailKeySize {
		if b, err := hex.DecodeString(s); err == nil {
			return b, nil
		}
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == EmailKeySize {
			return b, nil
		}
	}
	return nil, fmt.Errorf("accounts: the email key must be %d bytes written as 64 hex digits or as base64; make one with: openssl rand -hex 32", EmailKeySize)
}

// NewEmailCipher returns the cipher for key, which ParseEmailKey decoded.
func NewEmailCipher(key []byte) (*EmailCipher, error) {
	if len(key) != EmailKeySize {
		return nil, fmt.Errorf("accounts: the email key is %d bytes, want %d", len(key), EmailKeySize)
	}
	encKey, err := hkdf.Key(sha256.New, key, nil, "pairtime email encryption v1", 32)
	if err != nil {
		return nil, fmt.Errorf("accounts: %w", err)
	}
	indexKey, err := hkdf.Key(sha256.New, key, nil, "pairtime email index v1", 32)
	if err != nil {
		return nil, fmt.Errorf("accounts: %w", err)
	}
	id, err := hkdf.Key(sha256.New, key, nil, "pairtime email key id v1", keyIDSize)
	if err != nil {
		return nil, fmt.Errorf("accounts: %w", err)
	}
	block, err := aes.NewCipher(encKey)
	if err != nil {
		return nil, fmt.Errorf("accounts: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("accounts: %w", err)
	}
	c := &EmailCipher{aead: aead, indexKey: indexKey}
	copy(c.keyID[:], id)
	return c, nil
}

// ErrOtherEmailKey is what Open returns for an email sealed under another
// key.
var ErrOtherEmailKey = errors.New("accounts: the email was sealed under another PAIRTIME_EMAIL_KEY; it comes back at the account's next sign-in")

// emailAD is the additional data that binds a sealed email to its account.
func emailAD(sub string) []byte {
	return []byte("pairtime email v1\x00" + sub)
}

// Seal encrypts email for the account whose HCA subject is sub.
func (c *EmailCipher) Seal(sub, email string) []byte {
	nonce := make([]byte, c.aead.NonceSize())
	rand.Read(nonce)
	out := make([]byte, 0, 1+keyIDSize+len(nonce)+len(email)+c.aead.Overhead())
	out = append(out, sealedFormat)
	out = append(out, c.keyID[:]...)
	out = append(out, nonce...)
	return c.aead.Seal(out, nonce, []byte(email), emailAD(sub))
}

// Open decrypts an email Seal sealed for sub.
func (c *EmailCipher) Open(sub string, sealed []byte) (string, error) {
	head := 1 + keyIDSize + c.aead.NonceSize()
	if len(sealed) < head+c.aead.Overhead() || sealed[0] != sealedFormat {
		return "", errors.New("accounts: a sealed email is malformed")
	}
	if !hmac.Equal(sealed[1:1+keyIDSize], c.keyID[:]) {
		return "", ErrOtherEmailKey
	}
	plain, err := c.aead.Open(nil, sealed[1+keyIDSize:head], sealed[head:], emailAD(sub))
	if err != nil {
		return "", errors.New("accounts: a sealed email doesn't open: it was altered, or it belongs to another account")
	}
	return string(plain), nil
}

// Index returns the hex HMAC-SHA256 of the normalized address, under the
// index key. Equal addresses give equal indexes, so a row can be found by
// email without decrypting every row, and without the key the index
// reveals nothing.
func (c *EmailCipher) Index(email string) string {
	m := hmac.New(sha256.New, c.indexKey)
	m.Write([]byte(NormalizeEmail(email)))
	return hex.EncodeToString(m.Sum(nil))
}

// NormalizeEmail is the form Index hashes: trimmed and lowercased, as HCA
// stores addresses.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
