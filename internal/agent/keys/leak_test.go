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
)

// TestNothingShowsKeyMaterial puts a known K_dev in a keystore and checks
// that nothing the keystore prints, returns as text or writes beside the
// key holds it or a key derived from it, in any of the ways Go writes
// bytes (renderings): not a Keystore under any fmt verb, not its Status or
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
		{"a Keystore printed with %q shows no key", fmt.Sprintf("%q", ks)},
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
				for how, r := range renderings(secret) {
					if shows(tt.text, r) {
						t.Errorf("it shows %s as %s", name, how)
					}
				}
			}
		})
	}
}

// shows reports whether text holds the rendering r, as it is or with both
// in lower case, which finds hex in upper case. Raw bytes need the first
// test: lowering turns bytes that aren't UTF-8 into U+FFFD, and how it
// splits them depends on the bytes around them.
func shows(text, r string) bool {
	return strings.Contains(text, r) || strings.Contains(strings.ToLower(text), strings.ToLower(r))
}

// renderings returns the ways Go code commonly turns key bytes into text,
// by name: fmt's default verbs, which print a byte slice or array in
// decimal, the hex verbs, encoding/hex, and encoding/json, which writes a
// slice in base64 and an array as decimal numbers. Each is the run of
// bytes alone, without the brackets or braces around it, so it is found
// inside a struct or a longer slice too. TestRenderings checks them
// against fmt and encoding/json.
func renderings(b []byte) map[string]string {
	dec := make([]string, len(b))
	goHex := make([]string, len(b))
	for i, c := range b {
		dec[i] = strconv.Itoa(int(c))
		goHex[i] = fmt.Sprintf("%#x", c)
	}
	return map[string]string{
		"raw bytes":                  string(b),
		"decimal (%v, %d)":           strings.Join(dec, " "),
		"decimal in JSON (an array)": strings.Join(dec, ","),
		"Go syntax (%#v)":            strings.Join(goHex, ", "),
		"hex (%x, %X, encoding/hex)": hex.EncodeToString(b),
		"spaced hex (% x)":           fmt.Sprintf("% x", b),
		"base64 (encoding/json)":     base64.StdEncoding.EncodeToString(b),
	}
}

func TestRenderings(t *testing.T) {
	key := make([]byte, keySize)
	rand.Read(key)
	var array [keySize]byte
	copy(array[:], key)
	type holder struct {
		Key []byte
		Arr [keySize]byte
	}
	h := holder{Key: key, Arr: array}
	asJSON, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	r := renderings(key)
	tests := []struct {
		name, text, rendering string
	}{
		{"%v of a slice is decimal", fmt.Sprintf("%v", key), "decimal (%v, %d)"},
		{"%d of a slice is decimal", fmt.Sprintf("%d", key), "decimal (%v, %d)"},
		{"%v of an array is decimal", fmt.Sprintf("%v", array), "decimal (%v, %d)"},
		{"%+v of a struct holding a slice is decimal", fmt.Sprintf("%+v", h), "decimal (%v, %d)"},
		{"%#v of a slice is Go syntax", fmt.Sprintf("%#v", key), "Go syntax (%#v)"},
		{"%#v of a struct holding an array is Go syntax", fmt.Sprintf("%#v", h), "Go syntax (%#v)"},
		{"%x of a slice is hex", fmt.Sprintf("%x", key), "hex (%x, %X, encoding/hex)"},
		{"%X of a slice is hex in upper case", fmt.Sprintf("%X", key), "hex (%x, %X, encoding/hex)"},
		{"% x of a slice is spaced hex", fmt.Sprintf("% x", key), "spaced hex (% x)"},
		{"encoding/json writes a slice as base64", string(asJSON), "base64 (encoding/json)"},
		{"encoding/json writes an array as decimal numbers", string(asJSON), "decimal in JSON (an array)"},
		{"%s of a slice is the raw bytes", fmt.Sprintf("%s", key), "raw bytes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want, ok := r[tt.rendering]
			if !ok {
				t.Fatalf("renderings has no %q", tt.rendering)
			}
			if !shows(tt.text, want) {
				t.Errorf("%q doesn't contain the %s rendering %q", tt.text, tt.rendering, want)
			}
		})
	}
}
