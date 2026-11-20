// SPDX-License-Identifier: AGPL-3.0-or-later

package release

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// PayloadType is the DSSE payload type of a pairtime update manifest. A
// change to the manifest that old clients can't read gets a new type.
const PayloadType = "application/vnd.pairtime.update-manifest.v1+json"

// MaxManifestSize bounds the manifest.json Open accepts. A real one is a
// few kilobytes.
const MaxManifestSize = 1 << 20

// ErrNoTrustedKeys is Open's error when it is given no key to trust, as in
// a client built before release-keys.txt lists one.
var ErrNoTrustedKeys = errors.New("no release public key to verify against")

// envelope is a DSSE envelope (github.com/secure-systems-lab/dsse,
// envelope.md). Field order is the order json.Marshal writes.
type envelope struct {
	Payload     string      `json:"payload"`
	PayloadType string      `json:"payloadType"`
	Signatures  []signature `json:"signatures"`
}

type signature struct {
	KeyID string `json:"keyid"`
	Sig   string `json:"sig"`
}

// pae is DSSE's pre-authentication encoding, the bytes each signature
// covers (DSSE protocol.md, v1.0.2).
func pae(payloadType string, body []byte) []byte {
	b := fmt.Appendf(nil, "DSSEv1 %d %s %d ", len(payloadType), payloadType, len(body))
	return append(b, body...)
}

// encodeEnvelope writes the one form of an envelope that Open accepts:
// compact JSON and a final newline.
func encodeEnvelope(env envelope) ([]byte, error) {
	b, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Sign returns the bytes of manifest.json: payload, an encoded manifest,
// in a DSSE envelope with one signature per key. Signing with two keys
// lets clients that trust either one accept it, which is how a key is
// replaced.
func Sign(payload []byte, keys ...SecretKey) ([]byte, error) {
	if len(keys) == 0 {
		return nil, errors.New("no signing key")
	}
	if _, err := DecodeManifest(payload); err != nil {
		return nil, fmt.Errorf("won't sign an invalid manifest: %w", err)
	}
	msg := pae(PayloadType, payload)
	env := envelope{Payload: b64.EncodeToString(payload), PayloadType: PayloadType}
	seen := map[string]bool{}
	for _, k := range keys {
		if !k.valid() {
			return nil, errors.New("empty signing key")
		}
		id := k.Public().ID()
		if seen[id] {
			return nil, fmt.Errorf("key %s given twice", id)
		}
		seen[id] = true
		env.Signatures = append(env.Signatures, signature{KeyID: id, Sig: b64.EncodeToString(k.sign(msg))})
	}
	return encodeEnvelope(env)
}

// Open checks data, the bytes of a manifest.json, and returns the manifest
// in it with the trusted key whose signature verified. It follows DSSE's
// verification steps and is stricter in one way: data must be byte for
// byte what Sign writes, so changing any byte of a single-signature
// manifest makes Open reject it.
func Open(data []byte, trusted []PublicKey) (*Manifest, PublicKey, error) {
	if len(trusted) == 0 {
		return nil, PublicKey{}, ErrNoTrustedKeys
	}
	if len(data) > MaxManifestSize {
		return nil, PublicKey{}, fmt.Errorf("manifest is %d bytes, over the %d-byte limit", len(data), MaxManifestSize)
	}
	var env envelope
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&env); err != nil {
		return nil, PublicKey{}, fmt.Errorf("manifest envelope: %w", err)
	}
	// Re-encoding catches what the decoder forgives: whitespace, key case,
	// duplicate keys, escapes and anything after the object.
	if canon, err := encodeEnvelope(env); err != nil || !bytes.Equal(canon, data) {
		return nil, PublicKey{}, errors.New("manifest envelope isn't in the exact form tools/release writes")
	}
	if env.PayloadType != PayloadType {
		return nil, PublicKey{}, fmt.Errorf("payload type is %q, not %q", env.PayloadType, PayloadType)
	}
	payload, err := decodeB64(env.Payload)
	if err != nil {
		return nil, PublicKey{}, fmt.Errorf("payload: %w", err)
	}
	if len(env.Signatures) == 0 {
		return nil, PublicKey{}, errors.New("manifest has no signatures")
	}
	sigs := make([][]byte, len(env.Signatures))
	for i, s := range env.Signatures {
		sig, err := decodeB64(s.Sig)
		if err != nil || len(sig) != ed25519.SignatureSize {
			return nil, PublicKey{}, fmt.Errorf("signature %d isn't %d bytes in padded standard base64", i, ed25519.SignatureSize)
		}
		sigs[i] = sig
	}
	msg := pae(env.PayloadType, payload)
	// The key id only picks which trusted key to try (DSSE: it must not
	// decide anything on its own). A signature that doesn't verify is
	// skipped, so one good signature from a trusted key is enough.
	for i, s := range env.Signatures {
		for _, k := range trusted {
			if k.ID() != s.KeyID || !k.verify(msg, sigs[i]) {
				continue
			}
			m, err := DecodeManifest(payload)
			if err != nil {
				return nil, PublicKey{}, err
			}
			return m, k, nil
		}
	}
	signers := make([]string, len(env.Signatures))
	for i, s := range env.Signatures {
		signers[i] = fmt.Sprintf("%q", s.KeyID)
	}
	ids := make([]string, len(trusted))
	for i, k := range trusted {
		ids[i] = k.ID()
	}
	return nil, PublicKey{}, fmt.Errorf("no signature verifies under a trusted key (signed by %s; trusted: %s)",
		strings.Join(signers, ", "), strings.Join(ids, ", "))
}
