// SPDX-License-Identifier: AGPL-3.0-or-later

package keys

import (
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/some-du6e/pairtime/pkg/ledger"
)

// The oracle is testdata/ledger/vectors.json, which tools/ledger_ref.py
// wrote from fixed seeds (WP-LG1). The reference is standard-library Python
// and shares no code with pkg/ledger or this package. Each test below puts
// a vector's K_dev through a real store and the keystore's first-run and
// later-run paths, and checks the four keys it ends up with.

// keyVector is one entry of vectors.json's "keys" (ledger-v1.md 11.3).
type keyVector struct {
	KDev   string `json:"k_dev"`
	KLeaf  string `json:"k_leaf"`
	KTok   string `json:"k_tok"`
	KFeat  string `json:"k_feat"`
	KVault string `json:"k_vault"`
}

func readKeyVectors(t *testing.T) []keyVector {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "ledger", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Format string      `json:"format"`
		Keys   []keyVector `json:"keys"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("vectors.json: %v", err)
	}
	if f.Format != "pairtime-ledger-vectors-v1" {
		t.Fatalf("vectors.json has format %q", f.Format)
	}
	if len(f.Keys) < 4 {
		t.Fatalf("vectors.json has %d key vectors, want at least the 4 the spec lists", len(f.Keys))
	}
	return f.Keys
}

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != ledger.KeySize {
		t.Fatalf("vectors.json has a key that isn't %d bytes of hex", ledger.KeySize)
	}
	return b
}

// checkVector compares the keys k derived with the vector's, in constant
// time, and names the ones that differ.
func checkVector(t *testing.T, k *ledger.Keys, v keyVector) {
	t.Helper()
	for _, c := range []struct {
		name string
		got  [ledger.KeySize]byte
		want string
	}{
		{"K_leaf", k.LeafKey(), v.KLeaf},
		{"K_tok", k.TokenKey(), v.KTok},
		{"K_feat", k.FeatKey(), v.KFeat},
		{"K_vault", k.VaultKey(), v.KVault},
	} {
		if subtle.ConstantTimeCompare(c.got[:], unhex(t, c.want)) != 1 {
			t.Errorf("%s differs from vectors.json", c.name)
		}
	}
}

// checkVectors runs every key vector through the stores opts names: a first
// run that keeps the vector's K_dev, a later Open that reads it back from
// the store, and Archive followed by ArchivedKeys. want, when set, is the
// store each first run must pick. Every keystore is destroyed after, which
// matters for the real Keychain and Secret Service.
func checkVectors(t *testing.T, opts func(dir string) Options, want Kind) {
	for i, v := range readKeyVectors(t) {
		kDev := unhex(t, v.KDev)
		t.Run(fmt.Sprintf("keys[%d] read back from the store derive the vector's keys", i), func(t *testing.T) {
			o := opts(t.TempDir())
			t.Cleanup(func() { Destroy(o) })
			first, err := open(o, fixedKey(kDev))
			if err != nil {
				t.Fatal(err)
			}
			if want != "" && first.Status().Store != want {
				t.Fatalf("the first run kept the key in %s, want %s", first.Status().Store, want)
			}
			checkVector(t, first.Keys(), v)
			again, err := Open(o)
			if err != nil {
				t.Fatal(err)
			}
			if again.Created() {
				t.Fatal("the second Open made a key instead of reading the vector's")
			}
			checkVector(t, again.Keys(), v)
		})
		t.Run(fmt.Sprintf("keys[%d] archived and read back derive the vector's keys", i), func(t *testing.T) {
			o := opts(t.TempDir())
			t.Cleanup(func() { Destroy(o) })
			ks, err := Open(o)
			if err != nil {
				t.Fatal(err)
			}
			id, err := ks.Archive(kDev)
			if err != nil {
				t.Fatal(err)
			}
			got, err := ks.ArchivedKeys(id)
			if err != nil {
				t.Fatal(err)
			}
			checkVector(t, got, v)
		})
	}
}

func TestVectorsThroughTheKeyFile(t *testing.T) {
	checkVectors(t, func(dir string) Options {
		return Options{Dir: dir, Stores: []Store{}}
	}, KindFile)
}

// TestVectorsThroughThisOSStore uses DPAPI on Windows and the login
// Keychain on macOS, in platform.yml, and on Linux the Secret Service that
// fakesecret provides in the desktop build. In ci.yml's Linux job, with no
// Secret Service, the keys go through the key file.
func TestVectorsThroughThisOSStore(t *testing.T) {
	want := osStoreForTest(t)
	checkVectors(t, func(dir string) Options {
		return Options{Dir: dir, Timeout: osTimeout}
	}, want)
}
