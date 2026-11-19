// SPDX-License-Identifier: AGPL-3.0-or-later

package release

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

// The text forms of release keys: a prefix naming the key's kind, then the
// key in base64. A public key and a secret key are both 32 bytes, so the
// prefix is what keeps a secret key from being committed as a public one.
const (
	publicKeyPrefix = "pairtime-release-ed25519-public:"
	secretKeyPrefix = "pairtime-release-ed25519-secret:"
)

// b64 is the one base64 form pairtime writes: standard alphabet, padded.
var b64 = base64.StdEncoding.Strict()

// decodeB64 decodes s and requires it to be the exact encoding b64 writes,
// so each byte string has one accepted text form. Strict mode alone still
// skips CR and LF.
func decodeB64(s string) ([]byte, error) {
	raw, err := b64.DecodeString(s)
	if err != nil {
		return nil, err
	}
	if b64.EncodeToString(raw) != s {
		return nil, errors.New("not in canonical base64")
	}
	return raw, nil
}

// PublicKey is a release public key. Clients hold them to verify update
// manifests.
type PublicKey struct {
	key ed25519.PublicKey
}

// ParsePublicKey reads a public key in the form String writes.
func ParsePublicKey(s string) (PublicKey, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, secretKeyPrefix) {
		// Never echo s: it's a secret key.
		return PublicKey{}, errors.New("this is a release secret key, not a public key: keep it out of the repository, and replace it if it was ever committed")
	}
	enc, ok := strings.CutPrefix(s, publicKeyPrefix)
	if !ok {
		return PublicKey{}, fmt.Errorf("a release public key starts with %q", publicKeyPrefix)
	}
	raw, err := decodeB64(enc)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return PublicKey{}, fmt.Errorf("a release public key is %q and %d bytes in padded standard base64", publicKeyPrefix, ed25519.PublicKeySize)
	}
	return PublicKey{key: ed25519.PublicKey(raw)}, nil
}

// ParsePublicKeys reads a key list such as release-keys.txt: one public key
// per line, with blank lines and lines starting with # ignored.
func ParsePublicKeys(text string) ([]PublicKey, error) {
	var keys []PublicKey
	seen := map[string]bool{}
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, err := ParsePublicKey(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		if seen[k.ID()] {
			return nil, fmt.Errorf("line %d: key %s is listed twice", i+1, k.ID())
		}
		seen[k.ID()] = true
		keys = append(keys, k)
	}
	return keys, nil
}

// String returns the key's text form, the way release-keys.txt lists it.
func (k PublicKey) String() string { return publicKeyPrefix + b64.EncodeToString(k.key) }

// ID is the key's id: the first 8 bytes of the SHA-256 of the raw key, in
// hex. Manifests name their signing key by it.
func (k PublicKey) ID() string {
	sum := sha256.Sum256(k.key)
	return hex.EncodeToString(sum[:8])
}

// verify checks an RFC 8032 Ed25519 signature of msg.
func (k PublicKey) verify(msg, sig []byte) bool {
	if len(k.key) != ed25519.PublicKeySize || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(k.key, msg, sig)
}

// SecretKey is a release signing key. Every fmt verb prints only its id,
// so a stray Printf can't leak it; Text is the one way to get the key out.
type SecretKey struct {
	key ed25519.PrivateKey
}

// GenerateSecretKey makes a new signing key from rand.
func GenerateSecretKey(rand io.Reader) (SecretKey, error) {
	_, priv, err := ed25519.GenerateKey(rand)
	if err != nil {
		return SecretKey{}, err
	}
	return SecretKey{key: priv}, nil
}

// ParseSecretKey reads a secret key in the form Text writes. Its errors
// never quote s.
func ParseSecretKey(s string) (SecretKey, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, publicKeyPrefix) {
		return SecretKey{}, errors.New("this is a release public key; the signing key is the line `tools/release keygen` printed to standard output")
	}
	enc, ok := strings.CutPrefix(s, secretKeyPrefix)
	if !ok {
		return SecretKey{}, fmt.Errorf("a release secret key starts with %q", secretKeyPrefix)
	}
	seed, err := decodeB64(enc)
	if err != nil || len(seed) != ed25519.SeedSize {
		return SecretKey{}, fmt.Errorf("a release secret key is %q and a %d-byte RFC 8032 private key in padded standard base64", secretKeyPrefix, ed25519.SeedSize)
	}
	return SecretKey{key: ed25519.NewKeyFromSeed(seed)}, nil
}

// Text returns the key's text form: what keygen prints once and the
// RELEASE_SIGNING_KEY secret holds.
func (k SecretKey) Text() string { return secretKeyPrefix + b64.EncodeToString(k.key.Seed()) }

// Public returns the key's public half.
func (k SecretKey) Public() PublicKey {
	return PublicKey{key: k.key.Public().(ed25519.PublicKey)}
}

// String names the key by its public id.
func (k SecretKey) String() string {
	if len(k.key) != ed25519.PrivateKeySize {
		return "release secret key (empty)"
	}
	return "release secret key " + k.Public().ID()
}

// Format makes every fmt verb, %d and %x included, print String.
func (k SecretKey) Format(f fmt.State, verb rune) { io.WriteString(f, k.String()) }

// sign returns the RFC 8032 Ed25519 signature of msg.
func (k SecretKey) sign(msg []byte) []byte { return ed25519.Sign(k.key, msg) }
