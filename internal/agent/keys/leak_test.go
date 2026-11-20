// SPDX-License-Identifier: AGPL-3.0-or-later

package keys

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/pkg/ledger"
)

// renderings are the ways text can show a key's bytes: raw; in hex, as %x,
// %X, % x and % X print them; in decimal, as %v and %d print a byte slice
// or array, a struct's field included; in Go syntax, as %#v prints a byte
// array; quoted, as %q prints them; and in base64, as encoding/json writes
// a []byte.
func renderings(b []byte) []string {
	dec := make([]string, len(b))
	goHex := make([]string, len(b))
	for i, c := range b {
		dec[i] = strconv.Itoa(int(c))
		goHex[i] = fmt.Sprintf("%#x", c)
	}
	h := hex.EncodeToString(b)
	spaced := fmt.Sprintf("% x", b)
	q := strconv.Quote(string(b))
	return []string{
		string(b),
		h, strings.ToUpper(h),
		spaced, strings.ToUpper(spaced),
		strings.Join(dec, " "),
		strings.Join(goHex, ", "),
		q[1 : len(q)-1],
		base64.StdEncoding.EncodeToString(b),
		base64.RawStdEncoding.EncodeToString(b),
		base64.URLEncoding.EncodeToString(b),
		base64.RawURLEncoding.EncodeToString(b),
	}
}

// shows reports whether text shows secret in any of its renderings.
func shows(text string, secret []byte) bool {
	for _, r := range renderings(secret) {
		if strings.Contains(text, r) {
			return true
		}
	}
	return false
}

// TestLeakCheck checks the check: shows must see a key printed every way
// fmt and encoding/json print bytes, or TestNothingShowsKeyMaterial could
// pass while a key leaks.
func TestLeakCheck(t *testing.T) {
	kDev := make([]byte, keySize)
	rand.Read(kDev)
	var array [keySize]byte
	copy(array[:], kDev)
	keys, err := ledger.DeriveKeys(kDev)
	if err != nil {
		t.Fatal(err)
	}
	leaf := keys.LeafKey()
	asJSON, err := json.Marshal(struct{ K []byte }{kDev})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		text   string
		secret []byte
	}{
		{"raw, as %s prints a byte slice", fmt.Sprintf("%s", kDev), kDev},
		{"lowercase hex, as %x prints it", fmt.Sprintf("%x", kDev), kDev},
		{"uppercase hex, as %X prints it", fmt.Sprintf("%X", array), kDev},
		{"spaced hex, as % x prints it", fmt.Sprintf("% x", kDev), kDev},
		{"spaced uppercase hex, as % X prints it", fmt.Sprintf("% X", kDev), kDev},
		{"decimal, as %v prints a byte slice", fmt.Sprintf("%v", kDev), kDev},
		{"decimal, as %d prints a byte array", fmt.Sprintf("%d", array), kDev},
		{"Go syntax, as %#v prints a byte array", fmt.Sprintf("%#v", array), kDev},
		{"quoted, as %q prints a byte slice", fmt.Sprintf("%q", kDev), kDev},
		{"base64, as encoding/json writes a []byte", string(asJSON), kDev},
		// fmt can't call Format on a value in an unexported field, so it
		// prints the Keys' arrays in decimal (pkg/ledger, Keys).
		{"decimal, as %+v prints a ledger.Keys in another struct's unexported field", fmt.Sprintf("%+v", struct{ k ledger.Keys }{*keys}), leaf[:]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !shows(tt.text, tt.secret) {
				t.Error("shows doesn't see the key")
			}
		})
	}
}

// TestNothingShowsKeyMaterial puts a known K_dev in a keystore and checks
// that nothing the keystore prints, returns as text or writes beside the
// key shows it or a key derived from it, in any of the renderings shows
// knows: not a Keystore or its Keys under any fmt verb, not its Status or
// warning, not keystore.json, and not the errors of a failing Open.
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
		{"a Keystore printed with %d shows no key", fmt.Sprintf("%d", ks)},
		{"the Keys printed with %v show no key", fmt.Sprintf("%v", ks.Keys())},
		{"the Keys printed with %+v show no key", fmt.Sprintf("%+v", ks.Keys())},
		{"the Keys printed with %#v show no key", fmt.Sprintf("%#v", ks.Keys())},
		{"the Keys printed with %d show no key", fmt.Sprintf("%d", ks.Keys())},
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
				if shows(tt.text, secret) {
					t.Errorf("it shows %s", name)
				}
			}
		})
	}
}
