// SPDX-License-Identifier: AGPL-3.0-or-later

package vault

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// wycheproofFile is testdata/wycheproof/aes_gcm_test.json, Wycheproof's
// AES-GCM vectors at the commit testdata/wycheproof/PROVENANCE.md pins
// (aead_test_schema_v1.json). The test reads it in place and copies
// nothing.
type wycheproofFile struct {
	Algorithm     string            `json:"algorithm"`
	Schema        string            `json:"schema"`
	NumberOfTests int               `json:"numberOfTests"`
	Header        []string          `json:"header"`
	Notes         json.RawMessage   `json:"notes"`
	TestGroups    []wycheproofGroup `json:"testGroups"`
}

type wycheproofGroup struct {
	Type    string           `json:"type"`
	Source  json.RawMessage  `json:"source"`
	KeySize int              `json:"keySize"` // bits
	IVSize  int              `json:"ivSize"`  // bits
	TagSize int              `json:"tagSize"` // bits
	Tests   []wycheproofTest `json:"tests"`
}

type wycheproofTest struct {
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

// hexBytes is a byte string the vectors write in lowercase hex.
type hexBytes []byte

func (h *hexBytes) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return err
	}
	*h = b
	return nil
}

// readWycheproof reads the vectors, refusing a member the schema doesn't
// define, so a file in another shape fails rather than reads as empty.
func readWycheproof(t *testing.T) wycheproofFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "wycheproof", "aes_gcm_test.json"))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f wycheproofFile
	if err := dec.Decode(&f); err != nil {
		t.Fatalf("aes_gcm_test.json: %v", err)
	}
	if f.Algorithm != "AES-GCM" || f.Schema != "aead_test_schema_v1.json" {
		t.Fatalf("aes_gcm_test.json holds %s vectors of schema %s, want AES-GCM of aead_test_schema_v1.json", f.Algorithm, f.Schema)
	}
	return f
}

// vaultSizes reports whether a group has the vault's sizes: a 256-bit key,
// a 96-bit nonce and a 128-bit tag.
func vaultSizes(g wycheproofGroup) bool {
	return g.KeySize == 8*keySize && g.IVSize == 8*nonceSize && g.TagSize == 8*tagSize
}

func TestWycheproofCounts(t *testing.T) {
	f := readWycheproof(t)
	var all, ours, valid, invalid, other int
	for _, g := range f.TestGroups {
		for _, tc := range g.Tests {
			all++
			if !vaultSizes(g) {
				continue
			}
			ours++
			switch tc.Result {
			case "valid":
				valid++
			case "invalid":
				invalid++
			default:
				other++
			}
		}
	}
	// The counts come from testdata/wycheproof/PROVENANCE.md. If they stop
	// matching, a file with fewer vectors in it would pass the test below
	// without notice.
	cases := []struct {
		name      string
		got, want int
	}{
		{"the file holds the 316 tests its header counts", all, 316},
		{"the header's count is the file's", f.NumberOfTests, 316},
		{"66 tests have the vault's sizes, AES-256 with a 96-bit nonce", ours, 66},
		{"39 of them are valid", valid, 39},
		{"27 of them are invalid", invalid, 27},
		{"none of them is only acceptable", other, 0},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %d, want %d", tt.got, tt.want)
			}
		})
	}
}

func TestWycheproof(t *testing.T) {
	f := readWycheproof(t)
	cases := []struct {
		name string
		// in picks the groups the case runs over.
		in    func(wycheproofGroup) bool
		check func(wycheproofTest) error
	}{
		{
			name: "with the vault's sizes, a valid vector seals to its ciphertext and tag and opens to its message",
			in:   vaultSizes,
			check: func(tc wycheproofTest) error {
				if tc.Result != "valid" {
					return nil
				}
				g, err := newGCM(tc.Key)
				if err != nil {
					return err
				}
				sealed, err := g.seal(nil, tc.IV, tc.Msg, tc.AAD)
				if err != nil {
					return err
				}
				if want := append(append([]byte(nil), tc.CT...), tc.Tag...); !bytes.Equal(sealed, want) {
					return fmt.Errorf("sealed to %x, want %x", sealed, want)
				}
				msg, err := g.open(nil, tc.IV, sealed, tc.AAD)
				if err != nil {
					return fmt.Errorf("opening: %v", err)
				}
				if !bytes.Equal(msg, tc.Msg) {
					return fmt.Errorf("opened to %x, want %x", msg, tc.Msg)
				}
				return nil
			},
		},
		{
			name: "with the vault's sizes, an invalid vector doesn't open",
			in:   vaultSizes,
			check: func(tc wycheproofTest) error {
				if tc.Result != "invalid" {
					return nil
				}
				g, err := newGCM(tc.Key)
				if err != nil {
					return err
				}
				sealed := append(append([]byte(nil), tc.CT...), tc.Tag...)
				msg, err := g.open(nil, tc.IV, sealed, tc.AAD)
				if !errors.Is(err, errSealed) {
					return fmt.Errorf("opened to %x with error %v, want errSealed", msg, err)
				}
				return nil
			},
		},
		{
			name: "a vector with any other key or nonce size is refused, valid or not",
			in:   func(g wycheproofGroup) bool { return !vaultSizes(g) },
			check: func(tc wycheproofTest) error {
				g, err := newGCM(tc.Key)
				if err != nil {
					if len(tc.Key) == keySize {
						return fmt.Errorf("a %d-byte key was refused: %v", len(tc.Key), err)
					}
					return nil // the key's size is refused
				}
				if len(tc.IV) == nonceSize {
					return fmt.Errorf("the group has the vault's key and nonce sizes, so it isn't another size")
				}
				if sealed, err := g.seal(nil, tc.IV, tc.Msg, tc.AAD); err == nil {
					return fmt.Errorf("a %d-byte nonce sealed to %x", len(tc.IV), sealed)
				}
				sealed := append(append([]byte(nil), tc.CT...), tc.Tag...)
				if msg, err := g.open(nil, tc.IV, sealed, tc.AAD); err == nil {
					return fmt.Errorf("a %d-byte nonce opened to %x", len(tc.IV), msg)
				}
				return nil
			},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			ran := 0
			for _, g := range f.TestGroups {
				if g.Type != "AeadTest" {
					t.Fatalf("a group of type %q", g.Type)
				}
				if !tt.in(g) {
					continue
				}
				for _, tc := range g.Tests {
					ran++
					if err := tt.check(tc); err != nil {
						t.Errorf("tcId %d (%v, %s): %v", tc.TCID, tc.Flags, tc.Result, err)
					}
				}
			}
			if ran == 0 {
				t.Fatal("no vector ran")
			}
		})
	}
}
