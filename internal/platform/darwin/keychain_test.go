// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build darwin

package darwin

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

// These run on macos-latest in platform.yml, against the runner's login
// keychain. Each case uses its own random account and deletes its item.

func TestKeychain(t *testing.T) {
	k := Keychain{Service: "pairtime-test"}
	tests := []struct {
		name  string
		check func(t *testing.T, account string, secret []byte)
	}{
		{"a saved secret reads back byte for byte", func(t *testing.T, account string, secret []byte) {
			if err := k.Set(account, secret); err != nil {
				t.Fatal(err)
			}
			got, err := k.Get(account)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, secret) {
				t.Error("Get returned other bytes than Set saved")
			}
		}},
		{"a second Set replaces the item", func(t *testing.T, account string, secret []byte) {
			if err := k.Set(account, []byte("first")); err != nil {
				t.Fatal(err)
			}
			if err := k.Set(account, secret); err != nil {
				t.Fatal(err)
			}
			got, err := k.Get(account)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, secret) {
				t.Error("Get didn't return the second secret")
			}
		}},
		{"a missing item is ErrNotFound for Get and Delete", func(t *testing.T, account string, _ []byte) {
			if _, err := k.Get(account); !errors.Is(err, ErrNotFound) {
				t.Errorf("Get: got %v, want ErrNotFound", err)
			}
			if err := k.Delete(account); !errors.Is(err, ErrNotFound) {
				t.Errorf("Delete: got %v, want ErrNotFound", err)
			}
		}},
		{"an item that isn't hex is ErrBadItem, and the error doesn't quote it", func(t *testing.T, account string, _ []byte) {
			// Written through go-keyring as it is, not hex, as another
			// program could.
			if err := keyring.Set(k.Service, account, "QQQQ"); err != nil {
				t.Fatal(err)
			}
			got, err := k.Get(account)
			if !errors.Is(err, ErrBadItem) {
				t.Fatalf("got %v, want ErrBadItem", err)
			}
			if got != nil || strings.Contains(err.Error(), "Q") {
				t.Errorf("Get gave back bytes or quoted the item: %v", err)
			}
		}},
		{"Delete removes the item", func(t *testing.T, account string, secret []byte) {
			if err := k.Set(account, secret); err != nil {
				t.Fatal(err)
			}
			if err := k.Delete(account); err != nil {
				t.Fatal(err)
			}
			if _, err := k.Get(account); !errors.Is(err, ErrNotFound) {
				t.Errorf("Get after Delete: got %v, want ErrNotFound", err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := make([]byte, 8)
			rand.Read(id)
			account := "test-" + hex.EncodeToString(id)
			t.Cleanup(func() { k.Delete(account) })
			secret := make([]byte, 32)
			rand.Read(secret)
			tt.check(t, account, secret)
		})
	}
}
