// SPDX-License-Identifier: AGPL-3.0-or-later

package release

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestPAE(t *testing.T) {
	tests := []struct {
		name, payloadType, body, want string
	}{
		// The test vector in DSSE protocol.md v1.0.2.
		{name: "the DSSE spec's example", payloadType: "http://example.com/HelloWorld", body: "hello world", want: "DSSEv1 29 http://example.com/HelloWorld 11 hello world"},
		{name: "an empty body has length 0", payloadType: "t", body: "", want: "DSSEv1 1 t 0 "},
		{name: "pairtime's type", payloadType: PayloadType, body: "{}", want: "DSSEv1 48 application/vnd.pairtime.update-manifest.v1+json 2 {}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(pae(tt.payloadType, []byte(tt.body))); got != tt.want {
				t.Errorf("pae = %q, want %q", got, tt.want)
			}
		})
	}
}

func newKey(t *testing.T) SecretKey {
	t.Helper()
	k, err := GenerateSecretKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func testPayload(t *testing.T) []byte {
	t.Helper()
	p, err := EncodeManifest(testManifest())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSignAndOpen(t *testing.T) {
	k := newKey(t)
	payload := testPayload(t)
	data, err := Sign(payload, k)
	if err != nil {
		t.Fatal(err)
	}
	m, signer, err := Open(data, []PublicKey{newKey(t).Public(), k.Public()})
	if err != nil {
		t.Fatal(err)
	}
	if signer.ID() != k.Public().ID() {
		t.Errorf("signer = %s, want %s", signer.ID(), k.Public().ID())
	}
	got, _ := json.Marshal(m)
	want, _ := json.Marshal(testManifest())
	if !bytes.Equal(got, want) {
		t.Errorf("Open returned %s, want %s", got, want)
	}

	// The envelope is DSSE's, and each signature is Ed25519 over the PAE,
	// checked here with the envelope decoded by hand.
	var env struct {
		Payload, PayloadType string
		Signatures           []struct{ KeyID, Sig string }
	}
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	body, _ := b64.DecodeString(env.Payload)
	sig, _ := b64.DecodeString(env.Signatures[0].Sig)
	if !bytes.Equal(body, payload) || env.PayloadType != PayloadType || env.Signatures[0].KeyID != k.Public().ID() {
		t.Errorf("envelope fields are wrong: %s", data)
	}
	if !k.Public().verify([]byte("DSSEv1 48 "+PayloadType+" "+strconv.Itoa(len(payload))+" "+string(payload)), sig) {
		t.Errorf("the signature isn't Ed25519 over DSSE's PAE")
	}
}

// TestOneByteChange is WP-RL1's tamper check: change any one byte of a
// signed manifest.json, in any of three ways, and Open refuses it.
func TestOneByteChange(t *testing.T) {
	k := newKey(t)
	data, err := Sign(testPayload(t), k)
	if err != nil {
		t.Fatal(err)
	}
	trusted := []PublicKey{k.Public()}
	if _, _, err := Open(data, trusted); err != nil {
		t.Fatal(err)
	}
	for _, flip := range []byte{0x01, 0x20, 0x80} {
		for i := range data {
			bad := bytes.Clone(data)
			bad[i] ^= flip
			if _, _, err := Open(bad, trusted); err == nil {
				t.Fatalf("Open accepted manifest.json with byte %d changed from %q to %q", i, data[i], bad[i])
			}
		}
	}
	for _, edit := range []struct {
		name string
		data []byte
	}{
		{name: "a byte added at the end", data: append(bytes.Clone(data), '\n')},
		{name: "the final newline removed", data: data[:len(data)-1]},
		{name: "a byte removed from the middle", data: append(bytes.Clone(data[:100]), data[101:]...)},
	} {
		if _, _, err := Open(edit.data, trusted); err == nil {
			t.Errorf("Open accepted manifest.json with %s", edit.name)
		}
	}
}

func TestOpenRefuses(t *testing.T) {
	k := newKey(t)
	payload := testPayload(t)
	good, err := Sign(payload, k)
	if err != nil {
		t.Fatal(err)
	}
	trusted := []PublicKey{k.Public()}

	// sealed writes an envelope by hand, signed by k, for cases Sign won't
	// produce.
	sealed := func(payloadType string, body []byte, edit func(*envelope)) []byte {
		env := envelope{
			Payload:     b64.EncodeToString(body),
			PayloadType: payloadType,
			Signatures:  []signature{{KeyID: k.Public().ID(), Sig: b64.EncodeToString(k.sign(pae(payloadType, body)))}},
		}
		if edit != nil {
			edit(&env)
		}
		b, err := encodeEnvelope(env)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	invalid := bytes.Replace(payload, []byte(`"v0.0.0"`), []byte(`"v9.0.0"`), 1)

	// ASCII text encodes to base64 with no + or / unless it holds ?, > or
	// ~, so this payload carries "~~~" to put a + in its base64.
	tilde := testManifest()
	tilde.Artifacts[0].URL = "https://example.com/~~~/pairtime.zip"
	tildePayload, err := EncodeManifest(tilde)
	if err != nil {
		t.Fatal(err)
	}
	urlSafe := sealed(PayloadType, tildePayload, func(e *envelope) {
		e.Payload = strings.NewReplacer("+", "-", "/", "_").Replace(e.Payload)
	})
	if bytes.Equal(urlSafe, sealed(PayloadType, tildePayload, nil)) {
		t.Fatal("the URL-safe case changed nothing; its payload needs a + or / in base64")
	}

	tests := []struct {
		name    string
		data    []byte
		trusted []PublicKey // nil means k's public key
		noKeys  bool        // trust no key at all
		wantErr string
	}{
		{name: "a client with no release key accepts nothing", data: good, noKeys: true, wantErr: ErrNoTrustedKeys.Error()},
		{name: "a signature from an untrusted key", data: good, trusted: []PublicKey{newKey(t).Public()}, wantErr: "no signature verifies under a trusted key"},
		{name: "whitespace the writer doesn't use", data: bytes.Replace(good, []byte(`","payloadType"`), []byte(`", "payloadType"`), 1), wantErr: "exact form"},
		{name: "a field name in another case", data: bytes.Replace(good, []byte(`"payload"`), []byte(`"Payload"`), 1), wantErr: "exact form"},
		{name: "a field given twice", data: bytes.Replace(good, []byte(`{"payload"`), []byte(`{"payload":"","payload"`), 1), wantErr: "exact form"},
		{name: "a field DSSE doesn't define", data: bytes.Replace(good, []byte(`{"payload"`), []byte(`{"note":"x","payload"`), 1), wantErr: "unknown field"},
		{name: "a second JSON value after the envelope", data: append(bytes.Clone(good), []byte("{}\n")...), wantErr: "exact form"},
		{name: "a payload signed under another type", data: sealed("application/vnd.in-toto+json", payload, nil), wantErr: "payload type"},
		{name: "a payload in URL-safe base64", data: urlSafe, wantErr: "payload: illegal base64"},
		{name: "no signatures", data: sealed(PayloadType, payload, func(e *envelope) { e.Signatures = nil }), wantErr: "no signatures"},
		{name: "a signature that isn't base64", data: sealed(PayloadType, payload, func(e *envelope) { e.Signatures[0].Sig = "not base64!" }), wantErr: "signature 0"},
		{name: "a signature of the wrong length", data: sealed(PayloadType, payload, func(e *envelope) { e.Signatures[0].Sig = b64.EncodeToString(make([]byte, 63)) }), wantErr: "signature 0"},
		{name: "a good signature under the wrong key id", data: sealed(PayloadType, payload, func(e *envelope) { e.Signatures[0].KeyID = "0000000000000000" }), wantErr: "no signature verifies"},
		{name: "a signature over the bare payload instead of the PAE", data: sealed(PayloadType, payload, func(e *envelope) { e.Signatures[0].Sig = b64.EncodeToString(k.sign(payload)) }), wantErr: "no signature verifies"},
		{name: "a signed manifest that breaks a rule", data: sealed(PayloadType, invalid, nil), wantErr: "newer than version"},
		{name: "a signed payload that isn't JSON", data: sealed(PayloadType, []byte("hello"), nil), wantErr: "manifest payload"},
		{name: "an oversized file", data: append(bytes.Clone(good), bytes.Repeat([]byte(" "), MaxManifestSize)...), wantErr: "limit"},
		{name: "not JSON at all", data: []byte("pairtime\n"), wantErr: "manifest envelope"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := trusted
			if tt.trusted != nil {
				tr = tt.trusted
			}
			if tt.noKeys {
				tr = nil
			}
			_, _, err := Open(tt.data, tr)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
	if _, _, err := Open(good, nil); !errors.Is(err, ErrNoTrustedKeys) {
		t.Errorf("Open with no keys = %v, want ErrNoTrustedKeys", err)
	}
}

func TestKeyRotation(t *testing.T) {
	oldKey, newerKey, other := newKey(t), newKey(t), newKey(t)
	payload := testPayload(t)
	both, err := Sign(payload, oldKey, newerKey)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		data       []byte
		trusted    []PublicKey
		wantSigner string // "" means refused
	}{
		{name: "a client that trusts only the old key accepts a manifest signed by both", data: both, trusted: []PublicKey{oldKey.Public()}, wantSigner: oldKey.Public().ID()},
		{name: "a client that trusts only the new key accepts it too", data: both, trusted: []PublicKey{newerKey.Public()}, wantSigner: newerKey.Public().ID()},
		{name: "a client that trusts neither refuses it", data: both, trusted: []PublicKey{other.Public()}},
		{name: "a bad signature from one trusted key doesn't hide a good one from another", data: corruptFirstSig(t, both), trusted: []PublicKey{oldKey.Public(), newerKey.Public()}, wantSigner: newerKey.Public().ID()},
		{name: "a bad signature from the only trusted key is refused", data: corruptFirstSig(t, both), trusted: []PublicKey{oldKey.Public()}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, signer, err := Open(tt.data, tt.trusted)
			if tt.wantSigner == "" {
				if err == nil {
					t.Fatal("Open accepted it")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if signer.ID() != tt.wantSigner {
				t.Fatalf("signer = %s, want %s", signer.ID(), tt.wantSigner)
			}
		})
	}
	if _, err := Sign(payload, oldKey, oldKey); err == nil || !strings.Contains(err.Error(), "given twice") {
		t.Errorf("Sign with one key twice: err = %v", err)
	}
}

// corruptFirstSig flips one bit of the first signature and re-encodes the
// envelope, so only the signature check can catch it.
func corruptFirstSig(t *testing.T, data []byte) []byte {
	t.Helper()
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	sig, err := b64.DecodeString(env.Signatures[0].Sig)
	if err != nil {
		t.Fatal(err)
	}
	sig[10] ^= 0x01
	env.Signatures[0].Sig = b64.EncodeToString(sig)
	b, err := encodeEnvelope(env)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSignRefuses(t *testing.T) {
	payload := testPayload(t)
	tests := []struct {
		name    string
		payload []byte
		keys    []SecretKey
		wantErr string
	}{
		{name: "no key", payload: payload, wantErr: "no signing key"},
		{name: "the zero key", payload: payload, keys: []SecretKey{{}}, wantErr: "empty signing key"},
		{name: "an invalid manifest", payload: []byte(`{"version":"v1.0.0"}`), keys: []SecretKey{newKey(t)}, wantErr: "won't sign an invalid manifest"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Sign(tt.payload, tt.keys...)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestTrustedKeys(t *testing.T) {
	// release-keys.txt must always parse: a client that can't read its
	// own key list can't verify an update.
	keys, err := TrustedKeys()
	if err != nil {
		t.Fatalf("release-keys.txt: %v", err)
	}
	for _, k := range keys {
		if _, err := ParsePublicKey(k.String()); err != nil {
			t.Errorf("key %s: %v", k.ID(), err)
		}
	}
	if !strings.Contains(releaseKeys, "tools/release keygen") {
		t.Errorf("release-keys.txt lost the comment that says how to add a key")
	}
}
