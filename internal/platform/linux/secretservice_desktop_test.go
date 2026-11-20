// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux && desktop

package linux

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"maps"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/internal/platform/linux/fakesecret"
	"github.com/zalando/go-keyring"
)

// These run in platform.yml's Linux job, inside the private session bus
// that tools/ci/desktop_session.sh starts, against fakesecret: go-keyring's
// real D-Bus calls, answered by a Secret Service nobody's keyring backs.

func startFake(t *testing.T) *fakesecret.Service {
	t.Helper()
	fake, err := fakesecret.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fake.Close() })
	return fake
}

// itemsFor returns the fake's items for service and account.
func itemsFor(fake *fakesecret.Service, service, account string) []fakesecret.Item {
	var out []fakesecret.Item
	want := map[string]string{"service": service, "username": account}
	for _, it := range fake.Items() {
		if maps.Equal(it.Attributes, want) {
			out = append(out, it)
		}
	}
	return out
}

func TestSecretServiceAgainstFake(t *testing.T) {
	fake := startFake(t)
	ss := SecretService{Service: "pairtime-test"}
	tests := []struct {
		name  string
		check func(t *testing.T, account string, secret []byte)
	}{
		{"there is a session bus, so the store is available", func(t *testing.T, _ string, _ []byte) {
			if err := ss.Available(); err != nil {
				t.Fatal(err)
			}
		}},
		{"a saved secret reads back byte for byte", func(t *testing.T, account string, secret []byte) {
			if err := ss.Set(account, secret); err != nil {
				t.Fatal(err)
			}
			got, err := ss.Get(account)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, secret) {
				t.Error("Get returned other bytes than Set saved")
			}
		}},
		{"the item holds the secret as hex text under the service and username attributes", func(t *testing.T, account string, secret []byte) {
			if err := ss.Set(account, secret); err != nil {
				t.Fatal(err)
			}
			items := itemsFor(fake, ss.Service, account)
			if len(items) != 1 {
				t.Fatalf("found %d items for the account, want 1", len(items))
			}
			if got, want := string(items[0].Secret), hex.EncodeToString(secret); got != want {
				t.Error("the stored item isn't the secret's lowercase hex")
			}
			if items[0].ContentType != "text/plain; charset=utf8" {
				t.Errorf("content type %q, want go-keyring's text/plain", items[0].ContentType)
			}
		}},
		{"a second Set replaces the item instead of adding one", func(t *testing.T, account string, secret []byte) {
			if err := ss.Set(account, []byte("first")); err != nil {
				t.Fatal(err)
			}
			if err := ss.Set(account, secret); err != nil {
				t.Fatal(err)
			}
			if n := len(itemsFor(fake, ss.Service, account)); n != 1 {
				t.Errorf("found %d items for the account, want 1", n)
			}
			got, err := ss.Get(account)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, secret) {
				t.Error("Get didn't return the second secret")
			}
		}},
		{"a missing item is ErrNotFound for Get and Delete", func(t *testing.T, account string, _ []byte) {
			if _, err := ss.Get(account); !errors.Is(err, ErrNotFound) {
				t.Errorf("Get: got %v, want ErrNotFound", err)
			}
			if err := ss.Delete(account); !errors.Is(err, ErrNotFound) {
				t.Errorf("Delete: got %v, want ErrNotFound", err)
			}
		}},
		{"Delete removes the item", func(t *testing.T, account string, secret []byte) {
			if err := ss.Set(account, secret); err != nil {
				t.Fatal(err)
			}
			if err := ss.Delete(account); err != nil {
				t.Fatal(err)
			}
			if _, err := ss.Get(account); !errors.Is(err, ErrNotFound) {
				t.Errorf("Get after Delete: got %v, want ErrNotFound", err)
			}
			if n := len(itemsFor(fake, ss.Service, account)); n != 0 {
				t.Errorf("%d items left for the account", n)
			}
		}},
		{"an item that isn't hex is ErrBadItem, and the error doesn't quote it", func(t *testing.T, account string, _ []byte) {
			// Written through go-keyring as it is, not hex, as another
			// program could.
			if err := keyring.Set(ss.Service, account, "QQQQ"); err != nil {
				t.Fatal(err)
			}
			got, err := ss.Get(account)
			if !errors.Is(err, ErrBadItem) {
				t.Fatalf("got %v, want ErrBadItem", err)
			}
			if got != nil || strings.Contains(err.Error(), "Q") {
				t.Errorf("Get gave back bytes or quoted the item: %v", err)
			}
		}},
		{"go-keyring reports a delete whose prompt is dismissed as done, and the item stays", func(t *testing.T, account string, secret []byte) {
			// Why the keystore reads an item back after deleting it. If
			// go-keyring starts returning the dismissal, this fails, and
			// Delete's doc should say so.
			if err := ss.Set(account, secret); err != nil {
				t.Fatal(err)
			}
			fake.DismissDeletes(true)
			err := ss.Delete(account)
			fake.DismissDeletes(false)
			if err != nil {
				t.Fatalf("Delete with a dismissed prompt: %v", err)
			}
			got, err := ss.Get(account)
			if err != nil || !bytes.Equal(got, secret) {
				t.Errorf("after the dismissed delete, Get gives %v, want the secret", err)
			}
		}},
		{"a refusing service fails Get and Set, and neither is ErrNotFound", func(t *testing.T, account string, secret []byte) {
			if err := ss.Set(account, secret); err != nil {
				t.Fatal(err)
			}
			fake.Refuse(true)
			defer fake.Refuse(false)
			got, err := ss.Get(account)
			if err == nil || errors.Is(err, ErrNotFound) {
				t.Errorf("Get: got %v, want an error other than ErrNotFound", err)
			}
			if got != nil {
				t.Error("Get returned bytes along with its error")
			}
			if err := ss.Set(account, secret); err == nil {
				t.Error("Set succeeded on a refusing service")
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := make([]byte, 8)
			rand.Read(id)
			account := "test-" + hex.EncodeToString(id)
			t.Cleanup(func() { ss.Delete(account) })
			secret := make([]byte, 32)
			rand.Read(secret)
			tt.check(t, account, secret)
		})
	}
}
