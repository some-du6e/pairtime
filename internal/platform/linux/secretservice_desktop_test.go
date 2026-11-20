// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux && desktop

package linux

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"maps"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/some-du6e/pairtime/internal/platform/linux/fakesecret"
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
		{"a delete whose prompt is dismissed returns nil and leaves the item, because go-keyring drops the dismissed flag", func(t *testing.T, account string, secret []byte) {
			// internal/agent/keys reads an item back after a nil delete for
			// this reason. If go-keyring starts reporting the dismissal,
			// this case fails, and keys' Destroy doc can say so.
			if err := ss.Set(account, secret); err != nil {
				t.Fatal(err)
			}
			fake.DismissDeletes(true)
			defer fake.DismissDeletes(false)
			if err := ss.Delete(account); err != nil {
				t.Fatalf("Delete: %v, want nil", err)
			}
			got, err := ss.Get(account)
			if err != nil || !bytes.Equal(got, secret) {
				t.Errorf("Get after the dismissed delete: %v, want the secret", err)
			}
		}},
		{"an item that isn't hex is ErrBadItem itself, whose text quotes none of the item", func(t *testing.T, account string, _ []byte) {
			if err := keyring.Set(ss.Service, account, "zz-not-hex"); err != nil {
				t.Fatal(err)
			}
			got, err := ss.Get(account)
			if err != ErrBadItem || got != nil {
				t.Fatalf("got (%v, %v), want ErrBadItem alone and no bytes", got, err)
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
