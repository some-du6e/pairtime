// SPDX-License-Identifier: AGPL-3.0-or-later

package seal

import (
	"bytes"
	"crypto/ecdh"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Wycheproof's vectors in testdata/wycheproof are this package's oracle
// for ECDH on P-256 and for AES-256-GCM (PROVENANCE.md there; PLAN
// WP-SB2). The tests read the files in place. When code and a file
// disagree, the code is wrong.

// hexBytes is a Wycheproof byte string, lowercase hex in the JSON.
type hexBytes []byte

func (h *hexBytes) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := hex.DecodeString(s)
	if err != nil {
		return err
	}
	*h = v
	return nil
}

// readVectors decodes the file name in testdata/wycheproof into v,
// refusing a member v doesn't declare, so a schema change upstream can't
// pass by being skipped.
func readVectors(t *testing.T, name string, v any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "wycheproof", name))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

// ecdhFile is an ECDH vector file, ecdh_test_schema_v1.json or
// ecdh_ecpoint_test_schema_v1.json.
type ecdhFile struct {
	Algorithm     string          `json:"algorithm"`
	Schema        string          `json:"schema"`
	NumberOfTests int             `json:"numberOfTests"`
	Header        json.RawMessage `json:"header"`
	Notes         json.RawMessage `json:"notes"`
	TestGroups    []struct {
		Type     string          `json:"type"`
		Curve    string          `json:"curve"`
		Encoding string          `json:"encoding"`
		Source   json.RawMessage `json:"source"`
		Tests    []ecdhTest      `json:"tests"`
	} `json:"testGroups"`
}

type ecdhTest struct {
	TCID    int      `json:"tcId"`
	Comment string   `json:"comment"`
	Flags   []string `json:"flags"`
	Public  hexBytes `json:"public"`
	Private hexBytes `json:"private"`
	Shared  hexBytes `json:"shared"`
	Result  string   `json:"result"`
}

// vectorCheck is one rule a test checks over a file's vectors.
type vectorCheck struct {
	name string
	run  func(t *testing.T)
}

func (tc ecdhTest) String() string {
	return fmt.Sprintf("tcId %d (%s, %s)", tc.TCID, tc.Result, strings.Join(tc.Flags, " "))
}

// privateKey reads a Wycheproof private key, a big-endian integer that
// has a leading zero byte when its top bit is set and fewer than 32 bytes
// when it is small, into the 32 bytes crypto/ecdh takes. Every key in
// both files is between 1 and the group order, so each test runs.
func privateKey(b []byte) (*ecdh.PrivateKey, error) {
	b = bytes.TrimLeft(b, "\x00")
	if len(b) > 32 {
		return nil, fmt.Errorf("a private key of %d bytes", len(b))
	}
	k := make([]byte, 32)
	copy(k[32-len(b):], b)
	return ecdh.P256().NewPrivateKey(k)
}

// exchange is what this package does with a test: parse its public key,
// then the ECDH shared secret with its private key, as Open does with a
// blob's ephemeral key.
func exchange(tc ecdhTest, parse func([]byte) (*ecdh.PublicKey, error)) ([]byte, error) {
	pub, err := parse(tc.Public)
	if err != nil {
		return nil, err
	}
	priv, err := privateKey(tc.Private)
	if err != nil {
		return nil, fmt.Errorf("the test's private key: %w", err)
	}
	return shared(priv, pub)
}

// The two ECDH files, the parser each one's public keys go through, and
// the counts PROVENANCE.md gives.
var ecdhFiles = []struct {
	name, schema, encoding  string
	parse                   func([]byte) (*ecdh.PublicKey, error)
	tests, valid, ok, wrong int
}{
	{name: "ecdh_secp256r1_ecpoint_test.json", schema: "ecdh_ecpoint_test_schema_v1.json", encoding: "ecpoint", parse: ParsePublicKey, tests: 355, valid: 330, ok: 1, wrong: 24},
	{name: "ecdh_secp256r1_test.json", schema: "ecdh_test_schema_v1.json", encoding: "asn", parse: ParseSPKI, tests: 612, valid: 330, ok: 230, wrong: 52},
}

func TestWycheproofECDH(t *testing.T) {
	for _, f := range ecdhFiles {
		var file ecdhFile
		readVectors(t, f.name, &file)
		var all []ecdhTest
		for _, g := range file.TestGroups {
			all = append(all, g.Tests...)
		}
		byResult := func(r string) []ecdhTest {
			return slices.DeleteFunc(slices.Clone(all), func(tc ecdhTest) bool { return tc.Result != r })
		}
		tests := []vectorCheck{
			{name: "the file holds one P-256 group with the tests PROVENANCE.md counts", run: func(t *testing.T) {
				if file.Algorithm != "ECDH" || file.Schema != f.schema || len(file.TestGroups) != 1 ||
					file.TestGroups[0].Curve != "secp256r1" || file.TestGroups[0].Encoding != f.encoding {
					t.Fatalf("algorithm %q, schema %q, %d groups; want ECDH, %s and one secp256r1 group of %s keys", file.Algorithm, file.Schema, len(file.TestGroups), f.schema, f.encoding)
				}
				got := []int{file.NumberOfTests, len(all), len(byResult("valid")), len(byResult("acceptable")), len(byResult("invalid"))}
				if want := []int{f.tests, f.tests, f.valid, f.ok, f.wrong}; !slices.Equal(got, want) {
					t.Errorf("numberOfTests, tests, valid, acceptable and invalid are %v, want %v", got, want)
				}
			}},
			{name: "every valid test's public key parses, and its shared secret with the private key is Wycheproof's", run: func(t *testing.T) {
				for _, tc := range byResult("valid") {
					z, err := exchange(tc, f.parse)
					if err != nil || !bytes.Equal(z, tc.Shared) {
						t.Errorf("%v: shared secret %x, %v; want %x", tc, z, err, tc.Shared)
					}
				}
			}},
			{name: "every invalid test is refused", run: func(t *testing.T) {
				for _, tc := range byResult("invalid") {
					if z, err := exchange(tc, f.parse); err == nil {
						t.Errorf("%v: accepted, with shared secret %x", tc, z)
					}
				}
			}},
			{name: "an acceptable test may be refused, and one that is accepted gives Wycheproof's shared secret", run: func(t *testing.T) {
				accepted := 0
				for _, tc := range byResult("acceptable") {
					z, err := exchange(tc, f.parse)
					if err != nil {
						continue
					}
					accepted++
					if !bytes.Equal(z, tc.Shared) {
						t.Errorf("%v: accepted with shared secret %x, want %x", tc, z, tc.Shared)
					}
				}
				t.Logf("%d of %d acceptable tests accepted", accepted, f.ok)
			}},
		}
		if f.encoding == "ecpoint" {
			tests = append(tests,
				vectorCheck{name: "a blob whose ephemeral key is a valid test's point opens with the test's private key, under a key HKDF derives from Wycheproof's shared secret", run: func(t *testing.T) {
					for _, tc := range byResult("valid") {
						priv, err := privateKey(tc.Private)
						if err != nil {
							t.Fatal(err)
						}
						blob, err := blobAround(tc.Public, priv.PublicKey(), tc.Shared, example.c, []byte(example.m))
						if err != nil {
							t.Fatalf("%v: %v", tc, err)
						}
						if m, err := Open(priv, example.c, blob); err != nil || string(m) != example.m {
							t.Errorf("%v: Open = %q, %v", tc, m, err)
						}
					}
				}},
				vectorCheck{name: "a blob whose ephemeral key is an invalid test's 65-byte point fails with ErrMalformed", run: func(t *testing.T) {
					n := 0
					for _, tc := range byResult("invalid") {
						if len(tc.Public) != PointSize {
							continue
						}
						n++
						priv, err := privateKey(tc.Private)
						if err != nil {
							t.Fatal(err)
						}
						blob, err := blobAround(tc.Public, priv.PublicKey(), make([]byte, 32), example.c, []byte(example.m))
						if err != nil {
							t.Fatalf("%v: %v", tc, err)
						}
						if m, err := Open(priv, example.c, blob); !errors.Is(err, ErrMalformed) {
							t.Errorf("%v: Open = %q, %v; want ErrMalformed", tc, m, err)
						}
					}
					if n == 0 {
						t.Error("no invalid test has a 65-byte point, so this checked nothing")
					}
				}},
			)
		}
		for _, tt := range tests {
			t.Run(f.name+": "+tt.name, tt.run)
		}
	}
}

// blobAround builds the blob a sender would seal to recipient with the
// ephemeral point eph, given the shared secret z of the two, and a nonce
// of zeros. It follows seal-v1 4 steps 3 to 7 and never multiplies a
// point, so a Wycheproof point can stand as the ephemeral key.
func blobAround(eph []byte, recipient *ecdh.PublicKey, z []byte, c Context, m []byte) ([]byte, error) {
	key, err := derive(z, c)
	if err != nil {
		return nil, err
	}
	var header [HeaderSize]byte
	header[0] = Version
	id := keyID(recipient)
	copy(header[1:], id[:])
	copy(header[1+KeyIDSize:], eph)
	nonce := header[1+KeyIDSize+PointSize:]
	return encrypt(append([]byte{}, header[:]...), key, nonce, m, header[:])
}

// gcmFile is aes_gcm_test.json, aead_test_schema_v1.json.
type gcmFile struct {
	Algorithm     string          `json:"algorithm"`
	Schema        string          `json:"schema"`
	NumberOfTests int             `json:"numberOfTests"`
	Header        json.RawMessage `json:"header"`
	Notes         json.RawMessage `json:"notes"`
	TestGroups    []struct {
		Type    string          `json:"type"`
		Source  json.RawMessage `json:"source"`
		KeySize int             `json:"keySize"`
		IVSize  int             `json:"ivSize"`
		TagSize int             `json:"tagSize"`
		Tests   []gcmTest       `json:"tests"`
	} `json:"testGroups"`
}

type gcmTest struct {
	TCID    int      `json:"tcId"`
	Comment string   `json:"comment"`
	Flags   []string `json:"flags"`
	Key     hexBytes `json:"key"`
	IV      hexBytes `json:"iv"`
	AAD     hexBytes `json:"aad"`
	Msg     hexBytes `json:"msg"`
	CT      hexBytes `json:"ct"`
	Tag     hexBytes `json:"tag"`
	Result  string   `json:"result"`
}

func TestWycheproofGCM(t *testing.T) {
	var file gcmFile
	readVectors(t, "aes_gcm_test.json", &file)
	// mine are the tests with a blob's parameters, AES-256 with 96-bit
	// nonces and 128-bit tags, and others the rest.
	var mine, others []gcmTest
	for _, g := range file.TestGroups {
		if g.KeySize == 8*keySize && g.IVSize == 8*NonceSize && g.TagSize == 8*TagSize {
			mine = append(mine, g.Tests...)
		} else {
			others = append(others, g.Tests...)
		}
	}
	count := func(ts []gcmTest, r string) int {
		n := 0
		for _, tc := range ts {
			if tc.Result == r {
				n++
			}
		}
		return n
	}
	tests := []vectorCheck{
		{name: "the file holds the tests PROVENANCE.md counts, 66 of them with a blob's parameters", run: func(t *testing.T) {
			got := []int{file.NumberOfTests, len(mine) + len(others), len(file.TestGroups), len(mine), count(mine, "valid"), count(mine, "invalid")}
			if want := []int{316, 316, 45, 66, 39, 27}; file.Algorithm != "AES-GCM" || file.Schema != "aead_test_schema_v1.json" || !slices.Equal(got, want) {
				t.Errorf("algorithm %q, schema %q; tests, listed, groups, AES-256 with 96-bit nonces, valid and invalid are %v; want AES-GCM, aead_test_schema_v1.json and %v",
					file.Algorithm, file.Schema, got, want)
			}
		}},
		{name: "every valid test with a blob's parameters encrypts to Wycheproof's ciphertext and tag, and decrypts back", run: func(t *testing.T) {
			for _, tc := range mine {
				if tc.Result != "valid" {
					continue
				}
				want := append(slices.Clone(tc.CT), tc.Tag...)
				ct, err := encrypt(nil, tc.Key, tc.IV, tc.Msg, tc.AAD)
				if err != nil || !bytes.Equal(ct, want) {
					t.Errorf("tcId %d: encrypt = %x, %v; want %x", tc.TCID, ct, err, want)
				}
				m, err := decrypt(tc.Key, tc.IV, want, tc.AAD)
				if err != nil || !bytes.Equal(m, tc.Msg) {
					t.Errorf("tcId %d: decrypt = %x, %v; want %x", tc.TCID, m, err, tc.Msg)
				}
			}
		}},
		{name: "every invalid test with a blob's parameters fails to decrypt with ErrOpen", run: func(t *testing.T) {
			for _, tc := range mine {
				if tc.Result != "invalid" {
					continue
				}
				if m, err := decrypt(tc.Key, tc.IV, append(slices.Clone(tc.CT), tc.Tag...), tc.AAD); !errors.Is(err, ErrOpen) {
					t.Errorf("tcId %d (%s): decrypt = %x, %v; want ErrOpen", tc.TCID, strings.Join(tc.Flags, " "), m, err)
				}
			}
		}},
		{name: "every other test is refused, since a blob uses only AES-256 with 96-bit nonces", run: func(t *testing.T) {
			for _, tc := range others {
				if ct, err := encrypt(nil, tc.Key, tc.IV, tc.Msg, tc.AAD); err == nil {
					t.Errorf("tcId %d: a %d-byte key and a %d-byte nonce encrypted to %x", tc.TCID, len(tc.Key), len(tc.IV), ct)
				}
				if m, err := decrypt(tc.Key, tc.IV, append(slices.Clone(tc.CT), tc.Tag...), tc.AAD); err == nil || errors.Is(err, ErrOpen) {
					t.Errorf("tcId %d: a %d-byte key and a %d-byte nonce decrypted to %x, %v; want them refused before the tag is checked", tc.TCID, len(tc.Key), len(tc.IV), m, err)
				}
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

func TestRFC5869(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "ledger", "rfc5869.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Source string `json:"source"`
		Note   string `json:"note"`
		Cases  []struct {
			Name string   `json:"name"`
			IKM  hexBytes `json:"ikm"`
			Salt hexBytes `json:"salt"`
			Info hexBytes `json:"info"`
			L    int      `json:"l"`
			PRK  hexBytes `json:"prk"`
			OKM  hexBytes `json:"okm"`
		} `json:"cases"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		t.Fatal(err)
	}
	if len(f.Cases) != 3 {
		t.Fatalf("rfc5869.json has %d cases, want test cases 1 to 3", len(f.Cases))
	}
	for _, c := range f.Cases {
		t.Run(c.Name+": the HKDF that derives every blob's key gives the RFC's OKM", func(t *testing.T) {
			okm, err := hkdfSHA256(c.IKM, c.Salt, string(c.Info), c.L)
			if err != nil || !bytes.Equal(okm, c.OKM) {
				t.Errorf("OKM = %x, %v; want %x", okm, err, c.OKM)
			}
		})
	}
	c := f.Cases[2]
	t.Run(c.Name+": a nil salt and 32 zero bytes give the key the RFC's empty salt does, as seal-v1 4 says", func(t *testing.T) {
		if len(c.Salt) != 0 {
			t.Fatalf("%s has a %d-byte salt, and the RFC's test case 3 has none", c.Name, len(c.Salt))
		}
		for _, salt := range [][]byte{nil, make([]byte, 32)} {
			okm, err := hkdfSHA256(c.IKM, salt, string(c.Info), c.L)
			if err != nil || !bytes.Equal(okm, c.OKM) {
				t.Errorf("a salt of %d bytes: OKM = %x, %v; want %x", len(salt), okm, err, c.OKM)
			}
		}
	})
}
