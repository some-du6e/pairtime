// SPDX-License-Identifier: AGPL-3.0-or-later

package seal

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
	"testing"
)

// mustHex decodes s, which a test writes as lowercase hex.
func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// newKey makes a P-256 key pair.
func newKey(t *testing.T) *ecdh.PrivateKey {
	t.Helper()
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// scalarKey is the P-256 key whose scalar is the SHA-256 of label, as the
// worked example in seal-v1 8 makes its keys.
func scalarKey(t *testing.T, label string) *ecdh.PrivateKey {
	t.Helper()
	sum := sha256.Sum256([]byte(label))
	k, err := ecdh.P256().NewPrivateKey(sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// example is the worked example of seal-v1 8. Python's cryptography
// package, which runs OpenSSL, computed every value from the spec's
// steps, with none of this package's code.
var example = struct {
	r, e, nonceLabel                     string
	R, keyID, E, nonce, info, z, k, blob string
	c                                    Context
	m                                    string
}{
	r:          "pairtime seal-v1 example: evidence key",
	e:          "pairtime seal-v1 example: ephemeral key",
	nonceLabel: "pairtime seal-v1 example: nonce",
	R:          "0406293dfee843e0a8069a4cc8dce523770c58ed9ceb615ae828bf31eea6c59fd13f1632ccd717a62fc4398b7ecc2901f894cf11037e9468f956d86cc6a2e41be4",
	keyID:      "6edc953f4d170120",
	E:          "041551646e2190c666be618a781a018bfd0f33f90997ed144d031c0f74965dbd9519afaff1031a5d4c20940d04349cbe1289855ff680dec1787d953f69c6a49c87",
	nonce:      "743ba9269fc22e5171f02d6e",
	info:       "7061697274696d652f76312f7365616c146578616d706c652d7375626d697373696f6e2d31086d65746164617461",
	z:          "fb7b6a3df656e0c0166bf9e9596703293f7332c9c0fea6cd5bdf3bca2f1ad683",
	k:          "b6e3311074450e4273cf1298667d6b0c729e7c3c9cf3915bab24724a9f7af2d4",
	blob: "016edc953f4d170120041551646e2190c666be618a781a018bfd0f33f90997ed" +
		"144d031c0f74965dbd9519afaff1031a5d4c20940d04349cbe1289855ff680de" +
		"c1787d953f69c6a49c87743ba9269fc22e5171f02d6e08553f06669807e41082" +
		"1df43d32aa5577b23a836a9dc7b12dd6d5149763196e13b25df9f84cbaf4b012" +
		"b6dd",
	c: Context{Submission: "example-submission-1", Part: "metadata"},
	m: `{"note":"an example reveal"}`,
}

func TestExample(t *testing.T) {
	r, e := scalarKey(t, example.r), scalarKey(t, example.e)
	nonceSum := sha256.Sum256([]byte(example.nonceLabel))
	var nonce [NonceSize]byte
	copy(nonce[:], nonceSum[:])
	tests := []struct {
		name string
		got  func(t *testing.T) []byte
		want string
	}{
		{name: "the recipient's point is the example's R", got: func(t *testing.T) []byte { return r.PublicKey().Bytes() }, want: example.R},
		{name: "the ephemeral point is the example's E", got: func(t *testing.T) []byte { return e.PublicKey().Bytes() }, want: example.E},
		{name: "the nonce is the example's n", got: func(t *testing.T) []byte { return nonce[:] }, want: example.nonce},
		{name: "KeyID of R is the example's key id", got: func(t *testing.T) []byte { return []byte(KeyID(r.PublicKey())) }, want: hex.EncodeToString([]byte(example.keyID))},
		{name: "info for the example's submission and part is the example's info", got: func(t *testing.T) []byte { return []byte(example.c.info()) }, want: example.info},
		{name: "the shared secret of e and R is the example's Z", got: func(t *testing.T) []byte {
			z, err := shared(e, r.PublicKey())
			if err != nil {
				t.Fatal(err)
			}
			return z
		}, want: example.z},
		{name: "the shared secret of r and E is the same Z", got: func(t *testing.T) []byte {
			z, err := shared(r, e.PublicKey())
			if err != nil {
				t.Fatal(err)
			}
			return z
		}, want: example.z},
		{name: "derive gives the example's K from Z", got: func(t *testing.T) []byte {
			k, err := derive(mustHex(t, example.z), example.c)
			if err != nil {
				t.Fatal(err)
			}
			return k
		}, want: example.k},
		{name: "sealing m with the example's e and nonce gives the example's blob, byte for byte", got: func(t *testing.T) []byte {
			blob, err := seal(e, nonce, r.PublicKey(), example.c, []byte(example.m))
			if err != nil {
				t.Fatal(err)
			}
			return blob
		}, want: example.blob},
		{name: "the example's blob opens with r to m", got: func(t *testing.T) []byte {
			m, err := Open(r, example.c, mustHex(t, example.blob))
			if err != nil {
				t.Fatal(err)
			}
			return m
		}, want: hex.EncodeToString([]byte(example.m))},
		{name: "BlobKeyID of the example's blob is R's key id", got: func(t *testing.T) []byte {
			id, err := BlobKeyID(mustHex(t, example.blob))
			if err != nil {
				t.Fatal(err)
			}
			return []byte(id)
		}, want: hex.EncodeToString([]byte(example.keyID))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hex.EncodeToString(tt.got(t)); got != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	key := newKey(t)
	mega := make([]byte, 1<<20)
	rand.Read(mega)
	var printable strings.Builder
	for c := byte(0x21); c <= 0x7e; c++ {
		printable.WriteByte(c)
	}
	long := strings.Repeat(printable.String(), 3)[:MaxNameSize]
	tests := []struct {
		name string
		c    Context
		m    []byte
	}{
		{name: "an empty plaintext round-trips, in a blob of Overhead bytes", c: example.c, m: []byte{}},
		{name: "a one-byte plaintext round-trips", c: example.c, m: []byte{0}},
		{name: "a megabyte of random bytes round-trips", c: example.c, m: mega},
		{name: "names of one byte round-trip", c: Context{Submission: "s", Part: "p"}, m: []byte("x")},
		{name: "names of 255 bytes, every printable ASCII character among them, round-trip", c: Context{Submission: long, Part: long}, m: []byte("x")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blob, err := Seal(key.PublicKey(), tt.c, tt.m)
			if err != nil {
				t.Fatal(err)
			}
			if len(blob) != Overhead+len(tt.m) {
				t.Errorf("the blob is %d bytes, want %d", len(blob), Overhead+len(tt.m))
			}
			got, err := Open(key, tt.c, blob)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, tt.m) {
				t.Errorf("Open gave %d bytes that aren't the %d sealed", len(got), len(tt.m))
			}
			again, err := Seal(key.PublicKey(), tt.c, tt.m)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(again[1+KeyIDSize:HeaderSize], blob[1+KeyIDSize:HeaderSize]) {
				t.Error("two seals of the same plaintext share an ephemeral key and nonce")
			}
		})
	}
}

// negated is point with its Y coordinate negated: the other point with
// the same X coordinate, so the same ECDH shared secret.
func negated(point []byte) []byte {
	p, _ := new(big.Int).SetString("ffffffff00000001000000000000000000000000ffffffffffffffffffffffff", 16)
	y := new(big.Int).SetBytes(point[1+32:])
	out := append([]byte{}, point[:1+32]...)
	return append(out, new(big.Int).Sub(p, y).FillBytes(make([]byte, 32))...)
}

func TestOpenRefuses(t *testing.T) {
	key, other := newKey(t), newKey(t)
	m := []byte("a reveal that must not open")
	c := Context{Submission: "ab", Part: "c"}
	blob, err := Seal(key.PublicKey(), c, m)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := Seal(key.PublicKey(), c, nil)
	if err != nil {
		t.Fatal(err)
	}
	flip := func(i int) func([]byte) []byte {
		return func(b []byte) []byte { b[i] ^= 0x01; return b }
	}
	const ephAt = 1 + KeyIDSize
	tests := []struct {
		name    string
		blob    []byte
		change  func([]byte) []byte
		key     *ecdh.PrivateKey
		c       Context
		wantErr error
	}{
		{name: "a changed ciphertext byte fails with ErrOpen", change: flip(HeaderSize), wantErr: ErrOpen},
		{name: "a changed tag byte fails with ErrOpen", change: flip(len(blob) - 1), wantErr: ErrOpen},
		{name: "a changed nonce byte fails with ErrOpen", change: flip(HeaderSize - 1), wantErr: ErrOpen},
		{name: "a dropped tag byte fails with ErrOpen", change: func(b []byte) []byte { return b[:len(b)-1] }, wantErr: ErrOpen},
		{name: "an ephemeral key negated, which gives the same shared secret, fails with ErrOpen, since the header is the additional data",
			change: func(b []byte) []byte { copy(b[ephAt:], negated(b[ephAt:ephAt+PointSize])); return b }, wantErr: ErrOpen},
		{name: "an ephemeral key moved off the curve fails with ErrMalformed", change: flip(ephAt + PointSize - 1), wantErr: ErrMalformed},
		{name: "an ephemeral key marked compressed fails with ErrMalformed", change: func(b []byte) []byte { b[ephAt] = 0x02; return b }, wantErr: ErrMalformed},
		{name: "a changed key id fails with ErrOtherKey, and the key isn't tried", change: flip(1), wantErr: ErrOtherKey},
		{name: "another version fails with ErrMalformed", change: func(b []byte) []byte { b[0] = Version + 1; return b }, wantErr: ErrMalformed},
		{name: "a blob one byte shorter than a header and a tag fails with ErrMalformed", blob: empty,
			change: func(b []byte) []byte { return b[:Overhead-1] }, wantErr: ErrMalformed},
		{name: "another key fails with ErrOtherKey", key: other, wantErr: ErrOtherKey},
		{name: "another submission fails with ErrOpen", c: Context{Submission: "ad", Part: "c"}, wantErr: ErrOpen},
		{name: "another part fails with ErrOpen", c: Context{Submission: "ab", Part: "d"}, wantErr: ErrOpen},
		{name: "a byte moved from the submission id to the part fails with ErrOpen, since each name's length is in info",
			c: Context{Submission: "a", Part: "bc"}, wantErr: ErrOpen},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := bytes.Clone(blob)
			if tt.blob != nil {
				b = bytes.Clone(tt.blob)
			}
			if tt.change != nil {
				b = tt.change(b)
			}
			k, cc := key, c
			if tt.key != nil {
				k = tt.key
			}
			if tt.c != (Context{}) {
				cc = tt.c
			}
			got, err := Open(k, cc, b)
			if !errors.Is(err, tt.wantErr) || got != nil {
				t.Errorf("Open = %q, %v; want nothing and %v", got, err, tt.wantErr)
			}
		})
	}
}

func TestContext(t *testing.T) {
	key := newKey(t)
	tests := []struct {
		name string
		c    Context
		ok   bool
	}{
		{name: "the lowest and highest printable characters are names", c: Context{Submission: "!", Part: "~"}, ok: true},
		{name: "a name of 255 bytes, the most its length byte says, is one", c: Context{Submission: strings.Repeat("s", 255), Part: "p"}, ok: true},
		{name: "an empty submission id is refused", c: Context{Part: "p"}},
		{name: "an empty part is refused", c: Context{Submission: "s"}},
		{name: "a name of 256 bytes is refused, since its length doesn't fit a byte", c: Context{Submission: "s", Part: strings.Repeat("p", 256)}},
		{name: "a space is refused", c: Context{Submission: "a b", Part: "p"}},
		{name: "a tab is refused", c: Context{Submission: "s", Part: "a\tb"}},
		{name: "DEL is refused", c: Context{Submission: "s\x7f", Part: "p"}},
		{name: "a byte past ASCII is refused, so Go and JavaScript encode every name alike", c: Context{Submission: "caf\xc3\xa9", Part: "p"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blob, err := Seal(key.PublicKey(), tt.c, []byte("x"))
			if (err == nil) != tt.ok {
				t.Fatalf("Seal error = %v, want ok %v", err, tt.ok)
			}
			if !tt.ok {
				// A blob sealed under a good name, opened under this one.
				if blob, err = Seal(key.PublicKey(), example.c, []byte("x")); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Open(key, tt.c, blob); (err == nil) != tt.ok {
				t.Errorf("Open error = %v, want ok %v", err, tt.ok)
			}
		})
	}
}

func TestReseal(t *testing.T) {
	old, current, viewer := newKey(t), newKey(t), newKey(t)
	m := []byte("a revealed prompt")
	c := example.c
	stored, err := Seal(old.PublicKey(), c, m)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		// with is the key Reseal opens the stored blob with, to the key it
		// seals to, and c the Context it is given.
		with *ecdh.PrivateKey
		to   *ecdh.PrivateKey
		c    Context
		// open is the key the re-sealed blob is opened with, under c.
		open    *ecdh.PrivateKey
		wantErr error
		wantOK  bool
	}{
		{name: "a blob re-sealed to a new evidence key opens with that key to the same plaintext", with: old, to: current, c: c, open: current, wantOK: true},
		{name: "a blob re-sealed to a new evidence key no longer opens with the old key", with: old, to: current, c: c, open: old, wantErr: ErrOtherKey},
		{name: "a blob re-sealed to a viewer's key opens with the viewer's key", with: old, to: viewer, c: c, open: viewer, wantOK: true},
		{name: "a blob re-sealed to a viewer's key doesn't open with the evidence key", with: old, to: viewer, c: c, open: old, wantErr: ErrOtherKey},
		{name: "re-sealing under another part fails with ErrOpen and seals nothing", with: old, to: viewer, c: Context{Submission: c.Submission, Part: "content"}, wantErr: ErrOpen},
		{name: "re-sealing with a key the blob isn't sealed to fails with ErrOtherKey", with: current, to: viewer, c: c, wantErr: ErrOtherKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			again, err := Reseal(tt.with, tt.to.PublicKey(), tt.c, stored)
			if tt.open == nil {
				if !errors.Is(err, tt.wantErr) || again != nil {
					t.Fatalf("Reseal = %d bytes, %v; want nothing and %v", len(again), err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(again, stored) || bytes.Equal(again[1+KeyIDSize:HeaderSize], stored[1+KeyIDSize:HeaderSize]) {
				t.Error("the re-sealed blob reuses the stored blob's ephemeral key and nonce")
			}
			got, err := Open(tt.open, tt.c, again)
			if tt.wantOK != (err == nil) || !errors.Is(err, tt.wantErr) {
				t.Fatalf("Open = %v, want ok %v or %v", err, tt.wantOK, tt.wantErr)
			}
			if tt.wantOK && !bytes.Equal(got, m) {
				t.Errorf("the re-sealed blob opens to %q, want %q", got, m)
			}
		})
	}
}

func TestParsePublicKey(t *testing.T) {
	key := newKey(t).PublicKey()
	point := key.Bytes()
	// SEC 1 2.3.3: 0x02 or 0x03 by Y's parity, then X.
	compressed := append([]byte{0x02 | point[len(point)-1]&1}, point[1:1+32]...)
	offCurve := bytes.Clone(point)
	offCurve[len(offCurve)-1] ^= 0x01
	tests := []struct {
		name string
		b    []byte
		ok   bool
	}{
		{name: "an uncompressed point on the curve is a key", b: point, ok: true},
		{name: "a compressed point is refused", b: compressed},
		{name: "the point at infinity is refused", b: []byte{0}},
		{name: "a point off the curve is refused", b: offCurve},
		{name: "a point a byte short is refused", b: point[:len(point)-1]},
		{name: "a point with X = 5, on the curve, is a key", ok: true,
			b: mustHex(t, "040000000000000000000000000000000000000000000000000000000000000005459243b9aa581806fe913bce99817ade11ca503c64d9a3c533415c083248fbcc")},
		{name: "the same point with X written as 5 plus the field prime is refused, which a decoder that reduces would take",
			b: mustHex(t, "04ffffffff00000001000000000000000000000001000000000000000000000004459243b9aa581806fe913bce99817ade11ca503c64d9a3c533415c083248fbcc")},
		{name: "a hybrid point, prefix 0x06 or 0x07, is refused", b: append([]byte{0x06 | point[len(point)-1]&1}, point[1:]...)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePublicKey(tt.b)
			if (err == nil) != tt.ok {
				t.Fatalf("ParsePublicKey error = %v, want ok %v", err, tt.ok)
			}
			if tt.ok && !bytes.Equal(got.Bytes(), tt.b) {
				t.Error("ParsePublicKey gave another key")
			}
		})
	}
}

// mustSPKI is pub as a DER SubjectPublicKeyInfo.
func mustSPKI(t *testing.T, pub any) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func TestParseSPKI(t *testing.T) {
	key := newKey(t).PublicKey()
	p384, err := ecdh.P384().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	edPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		der  []byte
		ok   bool
	}{
		{name: "a P-256 key's SubjectPublicKeyInfo is a key", der: mustSPKI(t, key), ok: true},
		{name: "a P-384 key is refused", der: mustSPKI(t, p384.PublicKey())},
		{name: "an Ed25519 key is refused", der: mustSPKI(t, edPub)},
		{name: "a byte after the DER is refused", der: append(mustSPKI(t, key), 0)},
		{name: "two zero bytes inside the outer SEQUENCE, which encoding/asn1 would skip, are refused", der: func() []byte {
			der := mustSPKI(t, key)
			der[1] += 2
			return append(der, 0, 0)
		}()},
		{name: "a NULL parameter added inside the AlgorithmIdentifier is refused", der: func() []byte {
			der := mustSPKI(t, key)
			out := append([]byte{0x30, der[1] + 2, 0x30, der[3] + 2}, der[4:4+int(der[3])]...)
			out = append(out, 0x05, 0x00)
			return append(out, der[4+int(der[3]):]...)
		}()},
		{name: "the bare point is refused, since it isn't DER", der: key.Bytes()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSPKI(tt.der)
			if (err == nil) != tt.ok {
				t.Fatalf("ParseSPKI error = %v, want ok %v", err, tt.ok)
			}
			if tt.ok && !got.Equal(key) {
				t.Error("ParseSPKI gave another key")
			}
		})
	}
}

func TestKeyIDs(t *testing.T) {
	key := newKey(t).PublicKey()
	sum := sha256.Sum256(key.Bytes())
	blob, err := Seal(key, example.c, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		got     func() (string, error)
		want    string
		wantErr error
	}{
		{name: "KeyID is the first 8 bytes of the SHA-256 of the point, in hex", got: func() (string, error) { return KeyID(key), nil }, want: hex.EncodeToString(sum[:8])},
		{name: "BlobKeyID names the key the blob is sealed to", got: func() (string, error) { return BlobKeyID(blob) }, want: KeyID(key)},
		{name: "BlobKeyID of bytes that aren't a blob fails with ErrMalformed", got: func() (string, error) { return BlobKeyID(blob[:Overhead-1]) }, wantErr: ErrMalformed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.got()
			if got != tt.want || !errors.Is(err, tt.wantErr) {
				t.Errorf("got %q, %v; want %q, %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}
