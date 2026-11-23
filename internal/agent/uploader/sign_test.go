// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/pkg/ledger"
)

// The worked example of docs/spec/ingest-v1.md 2.2, as of wp-sv4-ingest's
// d53500d: a valid batch 1 with n = 3 and R_b = 32 bytes of 0xaa.
const (
	exampleRaw  = "1f8b0800000000000203a5cc310e02310c44d1bb4c9d22b683b3c909a86911424e1c448510209ad5de9d882becaf669ab7a2d9a7dfc71bf5bca2a152c00355024ed7f9603b43c0f10fe5588cd2489ab51cbc99fb2d7369a24ea9b3ce69224ca49d4ae2be18932d839c49622196093d5fe33ba5b8336c97ed07d5b9d682f7000000"
	exampleHash = "c8e78a7463f88a78d206683e69e3f847fe3d83715ad7ec2845e997167ddf4e15"
	exampleM    = "7061697274696d652d696e676573742d76310a645f6578616d706c650a34320a313739353038393636303235300a63386537386137343633663838613738643230363638336536396533663834376665336438333731356164376563323834356539393731363764646634653135"
	exampleSig  = "ed25519=H/zgh89iWdWyhYT9m8CX5DM/uM35gYzHsGDd29ZvI2L1CPh2j1PGHr5y51DmoP70iHC4z+9t7wH3ijZAvPNVAA=="
	exampleJS   = `{"batches":[{"b":1,"n":3,"R_b":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","H_b":"709a14e467695dbaddf729b36d14c2629ba332116c1942c8a21a8e1d21309123","prev":"0000000000000000000000000000000000000000000000000000000000000000"}]}`
)

func exampleKey() ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("pairtime ingest-v1 example device key"))
	return ed25519.NewKeyFromSeed(seed[:])
}

func repeated(c byte) ledger.Hash {
	var h ledger.Hash
	for i := range h {
		h[i] = c
	}
	return h
}

func TestSigning(t *testing.T) {
	raw, err := hex.DecodeString(exampleRaw)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"the example's key is the one the spec names", func(t *testing.T) {
			pub := exampleKey().Public().(ed25519.PublicKey)
			if got := hex.EncodeToString(pub); got != "fadacf1c88bce8152ec27995a05aa8d9c53ac5678801cdd2e05b19060398a677" {
				t.Errorf("public key %s", got)
			}
		}},
		{"the body hash is hex SHA-256 of the raw gzip bytes", func(t *testing.T) {
			if got := bodyHash(raw); got != exampleHash {
				t.Errorf("bodyHash = %s", got)
			}
		}},
		{"M is the spec's 110 bytes", func(t *testing.T) {
			m := message("d_example", 42, 1795089660250, bodyHash(raw))
			if got := hex.EncodeToString(m); got != exampleM || len(m) != 110 {
				t.Errorf("M = %q (%d bytes)", m, len(m))
			}
		}},
		{"the signature header is the spec's", func(t *testing.T) {
			sg, err := sign(KeySigner(exampleKey()), "d_example", 42, 1795089660250, bodyHash(raw))
			if err != nil {
				t.Fatal(err)
			}
			if sg.signature != exampleSig {
				t.Errorf("signature %s", sg.signature)
			}
		}},
		{"a batch root encodes as the spec's JSON, members in its order, with its head from ledger-v1 6", func(t *testing.T) {
			root := repeated(0xaa)
			js, err := encodeBody(Body{Batches: []Root{{B: 1, N: 3, Root: root, Head: ledger.BatchHead(ledger.Hash{}, root, 3, 1)}}})
			if err != nil {
				t.Fatal(err)
			}
			if string(js) != exampleJS+"\n" {
				t.Errorf("body %s", js)
			}
		}},
		{"compress gives one gzip member that inflates back, and the same bytes each time", func(t *testing.T) {
			a, err := compress([]byte(exampleJS))
			if err != nil {
				t.Fatal(err)
			}
			b, _ := compress([]byte(exampleJS))
			if !bytes.Equal(a, b) {
				t.Error("two compressions of one body differ")
			}
			zr, err := gzip.NewReader(bytes.NewReader(a))
			if err != nil {
				t.Fatal(err)
			}
			zr.Multistream(false)
			out, err := io.ReadAll(zr)
			if err != nil || string(out) != exampleJS {
				t.Errorf("inflated %q, %v", out, err)
			}
			if rest, _ := io.ReadAll(zr); len(rest) != 0 {
				t.Error("bytes after the member")
			}
		}},
		{"the headers are ingest-v1 2.1's", func(t *testing.T) {
			h := http.Header{}
			sg := signed{req: 42, unixMS: 1795089660250, signature: exampleSig}
			setHeaders(h, Credentials{Token: "ptd_x", DeviceID: "d_example"}, "0190e2a4-7c3b-7d4e-8f00-000000000000", bodyHash(raw), sg, "pairtime-agent/v0.1.0")
			want := map[string]string{
				"Authorization":          "Bearer ptd_x",
				"Idempotency-Key":        "0190e2a4-7c3b-7d4e-8f00-000000000000",
				"Content-Type":           "application/json",
				"Content-Encoding":       "gzip",
				"X-Pairtime-Device":      "d_example",
				"X-Pairtime-Req":         "42",
				"X-Pairtime-Time":        "1795089660250",
				"X-Pairtime-Body-Sha256": bodyHash(raw),
				"X-Pairtime-Signature":   exampleSig,
				"User-Agent":             "pairtime-agent/v0.1.0",
			}
			for k, v := range want {
				if got := h.Get(k); got != v {
					t.Errorf("%s = %q, want %q", k, got, v)
				}
			}
		}},
		{"the state request signs M_state, which has its own context and no counter or body", func(t *testing.T) {
			m := stateMessage("d_example", 1795089660250)
			if string(m) != "pairtime-ingest-state-v1\nd_example\n1795089660250" {
				t.Errorf("M_state = %q", m)
			}
			sig, err := signState(KeySigner(exampleKey()), "d_example", 1795089660250)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(sig, "ed25519="))
			if err != nil || !ed25519.Verify(exampleKey().Public().(ed25519.PublicKey), m, raw) {
				t.Errorf("the state signature %q doesn't verify", sig)
			}
		}},
		{"a names request signs M_names, M with its own context string, byte for byte", func(t *testing.T) {
			m := namesMessage("d_example", 42, 1795089660250, exampleHash)
			want := "pairtime-project-names-v1\nd_example\n42\n1795089660250\n" + exampleHash
			if string(m) != want || len(m) != 117 {
				t.Errorf("M_names = %q (%d bytes), want %q", m, len(m), want)
			}
			if got := hex.EncodeToString(m[:26]); got != "7061697274696d652d70726f6a6563742d6e616d65732d76310a" {
				t.Errorf("M_names starts %s", got)
			}
		}},
		{"a names signature verifies over M_names and never passes for an upload's M, nor an upload's for M_names", func(t *testing.T) {
			pub := exampleKey().Public().(ed25519.PublicKey)
			names, err := signNames(KeySigner(exampleKey()), "d_example", 42, 1795089660250, exampleHash)
			if err != nil {
				t.Fatal(err)
			}
			upload, err := sign(KeySigner(exampleKey()), "d_example", 42, 1795089660250, exampleHash)
			if err != nil {
				t.Fatal(err)
			}
			decode := func(s string) []byte {
				b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(s, "ed25519="))
				if err != nil {
					t.Fatal(err)
				}
				return b
			}
			mNames := namesMessage("d_example", 42, 1795089660250, exampleHash)
			mUpload := message("d_example", 42, 1795089660250, exampleHash)
			if !ed25519.Verify(pub, mNames, decode(names.signature)) || names.req != 42 || names.unixMS != 1795089660250 {
				t.Errorf("the names signature %+v doesn't verify over M_names", names)
			}
			if ed25519.Verify(pub, mUpload, decode(names.signature)) || ed25519.Verify(pub, mNames, decode(upload.signature)) {
				t.Error("a names signature passes for an upload, or an upload's for a names request")
			}
		}},
		{"a names body is the spec's JSON: set items with project and label, delete keys, and {} for a read", func(t *testing.T) {
			for _, tc := range []struct {
				body namesBody
				want string
			}{
				{namesBody{}, `{}`},
				{namesBody{Set: []nameItem{{Project: "g7c41e9a2d05b8f63e1a94c7d2b6f0e85", Label: "chip8 <emu> & co"}}}, `{"set":[{"project":"g7c41e9a2d05b8f63e1a94c7d2b6f0e85","label":"chip8 <emu> & co"}]}`},
				{namesBody{Delete: []string{"g7c41e9a2d05b8f63e1a94c7d2b6f0e85"}}, `{"delete":["g7c41e9a2d05b8f63e1a94c7d2b6f0e85"]}`},
			} {
				js, err := encodeNames(tc.body)
				if err != nil || string(js) != tc.want {
					t.Errorf("encodeNames(%+v) = %s, %v; want %s", tc.body, js, err, tc.want)
				}
			}
		}},
		{"a signer that gives no 64-byte signature is refused", func(t *testing.T) {
			bad := Signer(func([]byte) ([]byte, error) { return []byte{1, 2, 3}, nil })
			if _, err := sign(bad, "d_example", 1, 1, "00"); err == nil {
				t.Error("a 3-byte signature was taken")
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}
