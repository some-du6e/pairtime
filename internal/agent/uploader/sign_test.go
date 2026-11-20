// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"testing"

	"github.com/some-du6e/pairtime/pkg/ledger"
)

// The worked example of docs/spec/ingest-v1.md 2.2.
const (
	exampleRaw = "1f8b0800000000000203ab564a4a2c49ce482d56b28aae564a52b232d451ca53b232d6510a8a07f2941229044a3a4a1e608392280440830a8a52cb8026195008946a636b0146d4783bf7000000"
	exampleM   = "7061697274696d652d696e676573742d76310a645f6578616d706c650a34320a313739353038393636303235300a37303633646337343864343032363332393761356135613934613731663331316562316161386366663135306665313338356537643539633538376433353933"
	exampleSig = "ed25519=VTusonN24ztn8ApkJahEbIOmr2W+2irhsCNohTx7rZoUAFFcKnq2PkfLnI7kMY/eZTk++qX7g7AWLzB8ItpICg=="
	exampleJS  = `{"batches":[{"b":1,"n":3,"R_b":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","H_b":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","prev":"0000000000000000000000000000000000000000000000000000000000000000"}]}`
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
			if got := bodyHash(raw); got != "7063dc748d40263297a5a5a94a71f311eb1aa8cff150fe1385e7d59c587d3593" {
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
		{"a batch root encodes as the spec's JSON, members in its order", func(t *testing.T) {
			js, err := encodeBody(Body{Batches: []Root{{B: 1, N: 3, Root: repeated(0xaa), Head: repeated(0xbb)}}})
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
