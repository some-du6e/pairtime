// SPDX-License-Identifier: AGPL-3.0-or-later

package keys

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNothingShowsKeyMaterial puts a known K_dev in a keystore and checks
// that nothing the keystore prints, returns as text or writes beside the
// key holds it or a key derived from it, in hex or raw: not a Keystore
// under any fmt verb, not its Status or warning, not keystore.json, and not
// the errors of a failing Open.
func TestNothingShowsKeyMaterial(t *testing.T) {
	kDev := make([]byte, keySize)
	rand.Read(kDev)
	dir := t.TempDir()
	osStore := newFake(KindSecretService)
	osStore.failSet = errors.New("no keyring daemon")
	opts := Options{Dir: dir, Stores: []Store{osStore}}
	ks, err := open(opts, fixedKey(kDev))
	if err != nil {
		t.Fatal(err)
	}
	k := ks.Keys()
	leaf, tok, feat, vault := k.LeafKey(), k.TokenKey(), k.FeatKey(), k.VaultKey()
	secrets := map[string][]byte{
		"K_dev": kDev, "K_leaf": leaf[:], "K_tok": tok[:], "K_feat": feat[:], "K_vault": vault[:],
	}

	st := ks.Status()
	statusJSON, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	markerText := string(readFile(t, filepath.Join(keysDir(dir), markerName)))

	// Errors from a key file tampered with in two ways.
	if err := os.WriteFile(st.File, append(bytes.Clone(kDev), 0), 0o600); err != nil {
		t.Fatal(err)
	}
	_, lengthErr := Open(opts)
	flipped := bytes.Clone(kDev)
	flipped[0] ^= 0x01
	if err := os.WriteFile(st.File, flipped, 0o600); err != nil {
		t.Fatal(err)
	}
	_, changedErr := Open(opts)
	if lengthErr == nil || changedErr == nil {
		t.Fatal("Open accepted a tampered key file")
	}

	tests := []struct {
		name string
		text string
	}{
		{"a Keystore printed with %v shows no key", fmt.Sprintf("%v", ks)},
		{"a Keystore printed with %+v shows no key", fmt.Sprintf("%+v", ks)},
		{"a Keystore printed with %#v shows no key", fmt.Sprintf("%#v", ks)},
		{"a Keystore printed with %s shows no key", fmt.Sprintf("%s", ks)},
		{"a Keystore printed with %x shows no key", fmt.Sprintf("%x", ks)},
		{"the Keys printed with %+v show no key", fmt.Sprintf("%+v", ks.Keys())},
		{"the Status printed with %+v shows no key", fmt.Sprintf("%+v", st)},
		{"the Status as JSON shows no key", string(statusJSON)},
		{"the fallback warning shows no key", st.Warning()},
		{"keystore.json holds no key", markerText},
		{"the error for a key file of the wrong length shows no key", lengthErr.Error()},
		{"the error for a changed key file shows no key", changedErr.Error()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for name, secret := range secrets {
				h := hex.EncodeToString(secret)
				if strings.Contains(strings.ToLower(tt.text), h) || strings.Contains(tt.text, string(secret)) {
					t.Errorf("it shows %s", name)
				}
			}
		})
	}
}
