// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The published vectors this package's primitives are checked against,
// beside vectors.json: RFC 5869 for the key derivation, RFC 8032 for the
// signer of receipts and day heads, and transparency-dev/merkle's
// Certificate Transparency vectors for the trees and proofs.

func TestRFC5869(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "ledger", "rfc5869.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Cases []struct {
			Name string   `json:"name"`
			IKM  hexBytes `json:"ikm"`
			Salt hexBytes `json:"salt"`
			Info hexBytes `json:"info"`
			L    int      `json:"l"`
			PRK  hexBytes `json:"prk"`
			OKM  hexBytes `json:"okm"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Cases) != 3 {
		t.Fatalf("rfc5869.json has %d cases, want test cases 1 to 3", len(f.Cases))
	}
	for _, c := range f.Cases {
		t.Run(c.Name+": the HKDF-Extract and HKDF-Expand that DeriveKeys uses give the RFC's PRK and OKM", func(t *testing.T) {
			prk, err := extract(c.IKM, c.Salt)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(prk, c.PRK) {
				t.Errorf("PRK is %x, want %x", prk, c.PRK)
			}
			okm, err := expand(prk, string(c.Info), c.L)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(okm, c.OKM) {
				t.Errorf("OKM is %x, want %x", okm, c.OKM)
			}
		})
	}
}

func TestRFC8032(t *testing.T) {
	// RFC 8032 section 7.1, TEST 1 to TEST 3, copied from the RFC text.
	cases := []struct {
		name            string
		secret, public  string
		message, sigHex string
	}{
		{
			name:    "RFC 8032 7.1 TEST 1, an empty message: the receipt signer computes the RFC's signature and verifies it",
			secret:  "9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60",
			public:  "d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a",
			message: "",
			sigHex:  "e5564300c360ac729086e2cc806e828a84877f1eb8e5d974d873e065224901555fb8821590a33bacc61e39701cf9b46bd25bf5f0595bbe24655141438e7a100b",
		},
		{
			name:    "RFC 8032 7.1 TEST 2, a one-byte message: the receipt signer computes the RFC's signature and verifies it",
			secret:  "4ccd089b28ff96da9db6c346ec114e0f5b8a319f35aba624da8cf6ed4fb8a6fb",
			public:  "3d4017c3e843895a92b70aa74d1b7ebc9c982ccf2ec4968cc0cd55f12af4660c",
			message: "72",
			sigHex:  "92a009a9f0d4cab8720e820b5f642540a2b27b5416503f8fb3762223ebdb69da085ac1e43e15996e458f3613d0f11d8c387b2eaeb4302aeeb00d291612bb0c00",
		},
		{
			name:    "RFC 8032 7.1 TEST 3, a two-byte message: the receipt signer computes the RFC's signature and verifies it",
			secret:  "c5aa8df43f9f837bedb7442f31dcb7b166d38535076f094b85ce3a2e0b4458f7",
			public:  "fc51cd8e6218a1a38da47ed00230f0580816ed13ba3303ac5deb911548908025",
			message: "af82",
			sigHex:  "6291d657deec24024827e69c3abe01a30ce548a284743a445e3680d7db5ac3ac18ff9b538d16f290ae67f760984dc6594a7c15e9716ed28dc027beceea1ec40a",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			key := ed25519.NewKeyFromSeed(mustHex(t, c.secret))
			pub := key.Public().(ed25519.PublicKey)
			if !bytes.Equal(pub, mustHex(t, c.public)) {
				t.Fatalf("public key is %x, want %s", []byte(pub), c.public)
			}
			msg := mustHex(t, c.message)
			sig, err := sign(key, msg)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(sig[:], mustHex(t, c.sigHex)) {
				t.Errorf("signature is %x, want %s", sig, c.sigHex)
			}
			if !verify(pub, msg, sig) {
				t.Error("the RFC's signature doesn't verify")
			}
			for i := range sig {
				bad := sig
				bad[i] ^= 0x01
				if verify(pub, msg, bad) {
					t.Fatalf("a signature with byte %d changed verifies", i)
				}
			}
			if verify(pub, append(msg, 0), sig) {
				t.Error("the signature verifies over a longer message")
			}
		})
	}
}

// ctConstants reads the leaf inputs, the subtree hashes and the roots of
// trees of 0 to 8 leaves from transparency-dev/merkle's constants.go,
// copied as testdata/ledger/ct/constants.go.txt.
func ctConstants(t *testing.T) (leaves [][]byte, levels [][]Hash, roots []Hash) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "ledger", "ct", "constants.go.txt"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	body := func(name string) string {
		m := regexp.MustCompile(`(?ms)^func ` + name + `\(\) [^\n]*\{\n(.*?)^\}\n`).FindStringSubmatch(text)
		if m == nil {
			t.Fatalf("constants.go.txt has no func %s", name)
		}
		return m[1]
	}
	hd := regexp.MustCompile(`hd\("([0-9a-f]*)"\)`)
	hexes := func(src string) [][]byte {
		var out [][]byte
		for _, m := range hd.FindAllStringSubmatch(src, -1) {
			out = append(out, mustHex(t, m[1]))
		}
		return out
	}
	hashes := func(src string) []Hash {
		var out []Hash
		for _, b := range hexes(src) {
			h, err := HashOf(b)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, h)
		}
		return out
	}
	leaves = hexes(body("LeafInputs"))
	empty := hashes(body("EmptyRootHash"))
	for _, m := range regexp.MustCompile(`EmptyRootHash\(\)|hd\("([0-9a-f]*)"\)`).FindAllStringSubmatch(body("RootHashes"), -1) {
		if m[0] == "EmptyRootHash()" {
			roots = append(roots, empty...)
		} else {
			roots = append(roots, hashes(m[0])...)
		}
	}
	for _, level := range strings.Split(body("NodeHashes"), "}, {") {
		levels = append(levels, hashes(level))
	}
	if len(leaves) != 8 || len(empty) != 1 || len(roots) != 9 || len(levels) != 4 {
		t.Fatalf("constants.go.txt has %d leaves, %d empty roots, %d roots and %d levels, want 8, 1, 9 and 4", len(leaves), len(empty), len(roots), len(levels))
	}
	return leaves, levels, roots
}

func TestCTTrees(t *testing.T) {
	leaves, levels, roots := ctConstants(t)
	for n, want := range roots {
		t.Run(fmt.Sprintf("CT RootHashes: MTH of the first %d leaf inputs is the published root", n), func(t *testing.T) {
			if got := MTH(leaves[:n]); got != want {
				t.Errorf("MTH is %s, want %s", got, want)
			}
		})
	}
	for level, hashes := range levels {
		for i, want := range hashes {
			t.Run(fmt.Sprintf("CT NodeHashes: complete subtree %d at level %d hashes as published", i, level), func(t *testing.T) {
				if got := MTH(leaves[i<<level : (i+1)<<level]); got != want {
					t.Errorf("MTH is %s, want %s", got, want)
				}
			})
		}
	}
}

// ctProbe is one of transparency-dev/merkle's inclusion-proof checks.
type ctProbe struct {
	LeafIdx  uint64   `json:"leafIdx"`
	TreeSize uint64   `json:"treeSize"`
	Root     string   `json:"root"`
	LeafHash string   `json:"leafHash"`
	Proof    []string `json:"proof"`
	Desc     string   `json:"desc"`
	WantErr  bool     `json:"wantErr"`
}

// verifyProbe runs p through VerifyInclusion. A value that isn't 32 bytes
// fails at HashOf, which is spec 5.2's length rule.
func verifyProbe(p ctProbe) error {
	b64 := func(s string) (Hash, error) {
		b, err := base64.StdEncoding.Strict().DecodeString(s)
		if err != nil {
			return Hash{}, err
		}
		return HashOf(b)
	}
	root, err := b64(p.Root)
	if err != nil {
		return err
	}
	leaf, err := b64(p.LeafHash)
	if err != nil {
		return err
	}
	proof := make([]Hash, len(p.Proof))
	for i, s := range p.Proof {
		if proof[i], err = b64(s); err != nil {
			return err
		}
	}
	return VerifyInclusion(p.LeafIdx, p.TreeSize, leaf, proof, root)
}

func TestCTInclusion(t *testing.T) {
	leaves, _, roots := ctConstants(t)
	base := filepath.Join("..", "..", "testdata", "ledger", "ct", "inclusion")
	var files []string
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".json") {
			files = append(files, path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 98 {
		t.Fatalf("found %d inclusion probes, want the 98 published ones", len(files))
	}
	for _, path := range files {
		rel, _ := filepath.Rel(base, path)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var p ctProbe
		if err := json.Unmarshal(data, &p); err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		rule := "verifies"
		if p.WantErr {
			rule = "fails"
		}
		t.Run("CT probe "+filepath.ToSlash(rel)+": "+p.Desc+" "+rule, func(t *testing.T) {
			err := verifyProbe(p)
			if p.WantErr && err == nil {
				t.Fatal("VerifyInclusion accepts it")
			}
			if !p.WantErr && err != nil {
				t.Fatalf("VerifyInclusion refuses it: %v", err)
			}
			// A proof that verifies in a tree built from the published leaf
			// inputs is also what InclusionProof generates.
			if p.WantErr || p.TreeSize >= uint64(len(roots)) {
				return
			}
			n := int(p.TreeSize)
			root, _ := base64.StdEncoding.DecodeString(p.Root)
			if !bytes.Equal(root, roots[n][:]) {
				return
			}
			hs := make([]Hash, n)
			for i := range hs {
				hs[i] = LeafHash(leaves[i])
			}
			path, err := InclusionProof(int(p.LeafIdx), hs)
			if err != nil {
				t.Fatal(err)
			}
			if len(path) != len(p.Proof) {
				t.Fatalf("InclusionProof gives %d hashes, the probe %d", len(path), len(p.Proof))
			}
			for i, s := range p.Proof {
				if base64.StdEncoding.EncodeToString(path[i][:]) != s {
					t.Errorf("hash %d of InclusionProof differs from the probe's", i)
				}
			}
		})
	}
}
