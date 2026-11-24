// SPDX-License-Identifier: AGPL-3.0-or-later

// Package seal seals tier-2 content to a P-256 public key, so a revealed
// prompt crosses Cloudflare, sits in the evidence store and reaches a
// reviewer's browser only as ciphertext (DESIGN 7.6; docs/spec/seal-v1.md).
// A client seals each reveal to the evidence key before it uploads it.
// pairtimed opens a stored blob with the evidence key its key id names and
// re-seals the plaintext to a viewer's ephemeral key on every read, and to
// a new evidence key after a rotation (WP-SB9).
//
// A blob is ECDH on P-256 between a new ephemeral key and the recipient's
// key, HKDF-SHA256 over the shared secret with an info string that names
// the submission and the part, and AES-256-GCM over the plaintext with the
// blob's header as additional data. All of it is the standard library:
// crypto/ecdh, crypto/hkdf, crypto/aes and crypto/cipher. WP-SB7's
// WebCrypto viewer implements the same spec a second time.
package seal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
)

// The blob's layout (seal-v1 3).
const (
	// Version is the first byte of every blob.
	Version = 1
	// KeyIDSize is the length of the key id in a blob's header: the
	// first 8 bytes of the SHA-256 of the recipient's point.
	KeyIDSize = 8
	// PointSize is the length of an uncompressed P-256 point, 0x04 and
	// two 32-byte coordinates.
	PointSize = 65
	// NonceSize is the length of the AES-GCM nonce.
	NonceSize = 12
	// TagSize is the length of the AES-GCM tag.
	TagSize = 16
	// HeaderSize is the length of a blob's header, which the tag covers
	// as additional data.
	HeaderSize = 1 + KeyIDSize + PointSize + NonceSize
	// Overhead is how much longer a blob is than its plaintext.
	Overhead = HeaderSize + TagSize
	// MaxNameSize is the most bytes a submission id or a part may have.
	MaxNameSize = 128
)

// label starts HKDF's info (seal-v1 2).
const label = "pairtime/v1/seal"

// keySize is the length of the AES-256 key HKDF derives.
const keySize = 32

// Errors that Open and the parsers wrap, for errors.Is.
var (
	// ErrMalformed: the bytes aren't a sealed blob. It is too short,
	// has another version, or its ephemeral key isn't a point on P-256.
	ErrMalformed = errors.New("seal: not a sealed blob")
	// ErrOtherKey: the blob is sealed to another key than the one Open
	// was given, so that key isn't tried.
	ErrOtherKey = errors.New("seal: the blob is sealed to another key")
	// ErrOpen: the tag doesn't verify. The blob was changed, or it isn't
	// sealed under the submission and part Open was given.
	ErrOpen = errors.New("seal: the blob doesn't open")
)

// Context is what a blob is sealed under: the submission it belongs to
// and which of its parts it holds (seal-v1 2). Both enter the key
// derivation, so a blob opens only under the Context it was sealed under.
type Context struct {
	// Submission is the submission's id.
	Submission string
	// Part names the part of the submission the blob holds, such as its
	// metadata reveal or its content reveal.
	Part string
}

// check refuses a Context outside seal-v1 2: each name is 1 to
// MaxNameSize bytes of printable ASCII without spaces.
func (c Context) check() error {
	for _, f := range []struct{ what, v string }{{"submission id", c.Submission}, {"part", c.Part}} {
		if len(f.v) < 1 || len(f.v) > MaxNameSize {
			return fmt.Errorf("seal: a %s is 1 to %d bytes, and this one is %d", f.what, MaxNameSize, len(f.v))
		}
		for i := 0; i < len(f.v); i++ {
			if f.v[i] < 0x21 || f.v[i] > 0x7e {
				return fmt.Errorf("seal: a %s is printable ASCII without spaces, and byte %d is %#02x", f.what, i, f.v[i])
			}
		}
	}
	return nil
}

// info is HKDF's info for c: label, then each name after its length in
// one byte, so no two Contexts give the same info (seal-v1 2).
func (c Context) info() string {
	b := make([]byte, 0, len(label)+2+len(c.Submission)+len(c.Part))
	b = append(b, label...)
	b = append(b, byte(len(c.Submission)))
	b = append(b, c.Submission...)
	b = append(b, byte(len(c.Part)))
	b = append(b, c.Part...)
	return string(b)
}

// KeyID is pub's id: the first 8 bytes of the SHA-256 of its uncompressed
// point, in lowercase hex. It is the evidence key's key_id in the keys
// document (keys-v1 2) and the id pairtimed keys show prints.
func KeyID(pub *ecdh.PublicKey) string {
	id := keyID(pub)
	return hex.EncodeToString(id[:])
}

func keyID(pub *ecdh.PublicKey) [KeyIDSize]byte {
	sum := sha256.Sum256(pub.Bytes())
	var id [KeyIDSize]byte
	copy(id[:], sum[:])
	return id
}

// ParsePublicKey returns the P-256 public key whose uncompressed point is
// b, as a blob's header holds it and WebCrypto's exportKey("raw") gives
// it. It refuses a compressed point, the point at infinity and a point
// off the curve.
func ParsePublicKey(b []byte) (*ecdh.PublicKey, error) {
	pub, err := ecdh.P256().NewPublicKey(b)
	if err != nil {
		return nil, fmt.Errorf("seal: not an uncompressed P-256 point: %w", err)
	}
	return pub, nil
}

// ParseSPKI returns the P-256 public key in der, a DER
// SubjectPublicKeyInfo, as WebCrypto's exportKey("spki") and OpenSSL write
// one. It refuses a key of another kind or on another curve, explicit
// curve parameters, a compressed point and a point off the curve.
func ParseSPKI(der []byte) (*ecdh.PublicKey, error) {
	k, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("seal: %w", err)
	}
	ec, ok := k.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("seal: the SubjectPublicKeyInfo holds a %T, not a P-256 key", k)
	}
	pub, err := ec.ECDH()
	if err != nil || pub.Curve() != ecdh.P256() {
		return nil, errors.New("seal: the SubjectPublicKeyInfo doesn't hold a P-256 key")
	}
	return pub, nil
}

// Seal seals plaintext to the P-256 key to, under c (seal-v1 4), with a
// new ephemeral key and a random nonce.
func Seal(to *ecdh.PublicKey, c Context, plaintext []byte) ([]byte, error) {
	eph, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("seal: making the ephemeral key: %w", err)
	}
	var nonce [NonceSize]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("seal: making the nonce: %w", err)
	}
	return seal(eph, nonce, to, c, plaintext)
}

// seal is Seal with the ephemeral key and the nonce given, which the
// worked example in seal-v1 8 fixes.
func seal(eph *ecdh.PrivateKey, nonce [NonceSize]byte, to *ecdh.PublicKey, c Context, plaintext []byte) ([]byte, error) {
	if to == nil || to.Curve() != ecdh.P256() {
		return nil, errors.New("seal: the recipient isn't a P-256 key")
	}
	if err := c.check(); err != nil {
		return nil, err
	}
	key, err := blobKey(eph, to, c)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	var header [HeaderSize]byte
	header[0] = Version
	id := keyID(to)
	copy(header[1:], id[:])
	copy(header[1+KeyIDSize:], eph.PublicKey().Bytes())
	copy(header[1+KeyIDSize+PointSize:], nonce[:])
	blob := make([]byte, 0, Overhead+len(plaintext))
	blob = append(blob, header[:]...)
	return encrypt(blob, key, nonce[:], plaintext, header[:])
}

// Open opens blob with key, under c (seal-v1 5), and returns its
// plaintext. A blob that isn't one fails with ErrMalformed, one sealed to
// another key with ErrOtherKey, and one changed or sealed under another
// Context with ErrOpen.
func Open(key *ecdh.PrivateKey, c Context, blob []byte) ([]byte, error) {
	if key == nil || key.Curve() != ecdh.P256() {
		return nil, errors.New("seal: the key isn't a P-256 key")
	}
	if err := c.check(); err != nil {
		return nil, err
	}
	h, err := parseHeader(blob)
	if err != nil {
		return nil, err
	}
	if mine := keyID(key.PublicKey()); h.keyID != mine {
		return nil, fmt.Errorf("%w: it names key %x, and this is key %x", ErrOtherKey, h.keyID, mine)
	}
	k, err := blobKey(key, h.eph, c)
	if err != nil {
		return nil, err
	}
	defer clear(k)
	return decrypt(k, h.nonce, blob[HeaderSize:], blob[:HeaderSize])
}

// Reseal opens blob with key under c and seals its plaintext to to under
// the same c, with a new ephemeral key and nonce (seal-v1 6): to a
// viewer's ephemeral key on every read (DESIGN 7.6), and to a new
// evidence key after a rotation (WP-SB9). The plaintext is only ever in
// memory, and Reseal clears it before it returns.
func Reseal(key *ecdh.PrivateKey, to *ecdh.PublicKey, c Context, blob []byte) ([]byte, error) {
	pt, err := Open(key, c, blob)
	if err != nil {
		return nil, err
	}
	defer clear(pt)
	return Seal(to, c, pt)
}

// BlobKeyID returns the id of the key blob is sealed to, from its header,
// without opening it. It is the key a ring opens the blob with, and the
// id WP-SB4's upload check compares with the current evidence key's. A
// blob that isn't one fails with ErrMalformed.
func BlobKeyID(blob []byte) (string, error) {
	h, err := parseHeader(blob)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.keyID[:]), nil
}

// blobHeader is a blob's parsed header.
type blobHeader struct {
	keyID [KeyIDSize]byte
	eph   *ecdh.PublicKey
	nonce []byte
}

// parseHeader reads blob's header and checks steps 1 and 3 of seal-v1 5.
func parseHeader(blob []byte) (blobHeader, error) {
	if len(blob) < Overhead {
		return blobHeader{}, fmt.Errorf("%w: %d bytes, and a blob has at least %d", ErrMalformed, len(blob), Overhead)
	}
	if blob[0] != Version {
		return blobHeader{}, fmt.Errorf("%w: version %d, and this package reads version %d", ErrMalformed, blob[0], Version)
	}
	var h blobHeader
	copy(h.keyID[:], blob[1:1+KeyIDSize])
	eph, err := ParsePublicKey(blob[1+KeyIDSize : 1+KeyIDSize+PointSize])
	if err != nil {
		return blobHeader{}, fmt.Errorf("%w: its ephemeral key isn't an uncompressed P-256 point", ErrMalformed)
	}
	h.eph = eph
	h.nonce = blob[1+KeyIDSize+PointSize : HeaderSize]
	return h, nil
}

// blobKey is K for the blob between priv and pub under c: HKDF-SHA256
// over their shared secret (seal-v1 4 steps 2 and 3).
func blobKey(priv *ecdh.PrivateKey, pub *ecdh.PublicKey, c Context) ([]byte, error) {
	z, err := shared(priv, pub)
	if err != nil {
		return nil, err
	}
	defer clear(z)
	return derive(z, c)
}

// shared is the ECDH shared secret of priv and pub: the X coordinate of
// their product, 32 bytes (seal-v1 4 step 2).
func shared(priv *ecdh.PrivateKey, pub *ecdh.PublicKey) ([]byte, error) {
	z, err := priv.ECDH(pub)
	if err != nil {
		return nil, fmt.Errorf("seal: ECDH: %w", err)
	}
	return z, nil
}

// derive is K for the shared secret z under c: HKDF-SHA256 with an empty
// salt (seal-v1 4 step 3).
func derive(z []byte, c Context) ([]byte, error) {
	return hkdfSHA256(z, nil, c.info(), keySize)
}

// hkdfSHA256 is RFC 5869's HKDF over SHA-256: n bytes from ikm, salt and
// info. A nil salt is the RFC's default of 32 zero bytes, which gives the
// same key as an empty one.
func hkdfSHA256(ikm, salt []byte, info string, n int) ([]byte, error) {
	k, err := hkdf.Key(sha256.New, ikm, salt, info, n)
	if err != nil {
		return nil, fmt.Errorf("seal: HKDF: %w", err)
	}
	return k, nil
}

// newGCM returns AES-256-GCM under key, with 12-byte nonces and 16-byte
// tags, the only parameters a blob uses.
func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != keySize {
		return nil, fmt.Errorf("seal: an AES-256 key is %d bytes, not %d", keySize, len(key))
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("seal: %w", err)
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		return nil, fmt.Errorf("seal: %w", err)
	}
	return g, nil
}

// encrypt appends to dst the AES-256-GCM encryption of plaintext under key
// and nonce, with aad as additional data, and its tag (seal-v1 4 step 6).
// aad must not overlap dst.
func encrypt(dst, key, nonce, plaintext, aad []byte) ([]byte, error) {
	g, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(nonce) != NonceSize {
		return nil, fmt.Errorf("seal: an AES-GCM nonce is %d bytes, not %d", NonceSize, len(nonce))
	}
	return g.Seal(dst, nonce, plaintext, aad), nil
}

// decrypt opens ct, AES-256-GCM ciphertext with its tag at the end, under
// key and nonce with aad as additional data (seal-v1 5 step 5). A tag that
// doesn't verify is ErrOpen.
func decrypt(key, nonce, ct, aad []byte) ([]byte, error) {
	g, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(nonce) != NonceSize {
		return nil, fmt.Errorf("seal: an AES-GCM nonce is %d bytes, not %d", NonceSize, len(nonce))
	}
	pt, err := g.Open(nil, nonce, ct, aad)
	if err != nil {
		return nil, ErrOpen
	}
	return pt, nil
}
