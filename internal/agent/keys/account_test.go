// SPDX-License-Identifier: AGPL-3.0-or-later

package keys

import (
	"bytes"
	"errors"
	"testing"
)

// TestAccount is the device's sign-in in the keystore: the token and key
// seed `pairtime login` keeps (WP-SV3).
func TestAccount(t *testing.T) {
	token := []byte("ptd_" + "0123456789ABCDEFGHIJKLMNOPQRSTUV" + "000000")
	seed := bytes.Repeat([]byte{0x5a}, 32)
	tests := []struct {
		name string
		run  func(t *testing.T, dir string)
	}{
		{"an installation that never signed in has no account", func(t *testing.T, dir string) {
			ks := mustOpen(t, Options{Dir: dir, Stores: []Store{newFake(KindKeychain)}})
			if _, _, err := ks.Account(); !errors.Is(err, ErrNotFound) {
				t.Errorf("Account = %v, want ErrNotFound", err)
			}
		}},
		{"the token and seed come back from the store that holds K_dev, and keystore.json names it", func(t *testing.T, dir string) {
			osStore := newFake(KindKeychain)
			ks := mustOpen(t, Options{Dir: dir, Stores: []Store{osStore}})
			if err := ks.SetAccount(token, seed); err != nil {
				t.Fatal(err)
			}
			gotToken, gotSeed, err := ks.Account()
			if err != nil || !bytes.Equal(gotToken, token) || !bytes.Equal(gotSeed, seed) {
				t.Fatalf("Account = %q, %x, %v", gotToken, gotSeed, err)
			}
			m := markerIn(t, dir)
			if m.Account != KindKeychain {
				t.Errorf("keystore.json says the sign-in is in %q, want %q", m.Account, KindKeychain)
			}
			for _, item := range m.accountItems() {
				if !osStore.has(item) {
					t.Errorf("the OS store has no %s", item)
				}
			}
		}},
		{"another process that opens the keystore sees the account", func(t *testing.T, dir string) {
			osStore := newFake(KindKeychain)
			opts := Options{Dir: dir, Stores: []Store{osStore}}
			agent := mustOpen(t, opts)
			login := mustOpen(t, opts)
			if err := login.SetAccount(token, seed); err != nil {
				t.Fatal(err)
			}
			if got, _, err := agent.Account(); err != nil || !bytes.Equal(got, token) {
				t.Errorf("the agent's keystore reads %q, %v", got, err)
			}
		}},
		{"SetAccount refuses a seed that isn't 32 bytes and a token that is empty", func(t *testing.T, dir string) {
			ks := mustOpen(t, Options{Dir: dir, Stores: []Store{newFake(KindKeychain)}})
			if err := ks.SetAccount(token, seed[:31]); err == nil {
				t.Error("a 31-byte seed was kept")
			}
			if err := ks.SetAccount(nil, seed); err == nil {
				t.Error("an empty token was kept")
			}
		}},
		{"ForgetAccount deletes both, and leaves K_dev", func(t *testing.T, dir string) {
			osStore := newFake(KindKeychain)
			opts := Options{Dir: dir, Stores: []Store{osStore}}
			ks := mustOpen(t, opts)
			if err := ks.SetAccount(token, seed); err != nil {
				t.Fatal(err)
			}
			if err := ks.ForgetAccount(); err != nil {
				t.Fatal(err)
			}
			if _, _, err := ks.Account(); !errors.Is(err, ErrNotFound) {
				t.Errorf("Account after ForgetAccount = %v, want ErrNotFound", err)
			}
			m := markerIn(t, dir)
			for _, item := range m.accountItems() {
				if osStore.has(item) {
					t.Errorf("the store still has %s", item)
				}
			}
			if m.Account != "" || !osStore.has(m.deviceItem()) {
				t.Errorf("after ForgetAccount keystore.json names %q, and K_dev is there: %v", m.Account, osStore.has(m.deviceItem()))
			}
			if _, err := Open(opts); err != nil {
				t.Errorf("the keystore doesn't open after ForgetAccount: %v", err)
			}
		}},
		{"a store that won't delete keeps the sign-in listed, so a later try can finish", func(t *testing.T, dir string) {
			osStore := newFake(KindKeychain)
			ks := mustOpen(t, Options{Dir: dir, Stores: []Store{osStore}})
			if err := ks.SetAccount(token, seed); err != nil {
				t.Fatal(err)
			}
			osStore.update(func(f *fakeStore) { f.keep = true })
			if err := ks.ForgetAccount(); err == nil {
				t.Fatal("ForgetAccount said it deleted secrets the store kept")
			}
			if markerIn(t, dir).Account != KindKeychain {
				t.Error("keystore.json stopped listing a sign-in the store still has")
			}
			osStore.update(func(f *fakeStore) { f.keep = false })
			if err := ks.ForgetAccount(); err != nil {
				t.Errorf("the second ForgetAccount: %v", err)
			}
		}},
		{"Destroy deletes the sign-in with the device key", func(t *testing.T, dir string) {
			osStore := newFake(KindKeychain)
			opts := Options{Dir: dir, Stores: []Store{osStore}}
			ks := mustOpen(t, opts)
			if err := ks.SetAccount(token, seed); err != nil {
				t.Fatal(err)
			}
			if err := Destroy(opts); err != nil {
				t.Fatal(err)
			}
			if osStore.size() != 0 {
				t.Errorf("the store still has %d items after Destroy", osStore.size())
			}
			checkCleared(t, dir)
		}},
		{"Destroy deletes a sign-in that keystore.json no longer lists, as a save that landed late leaves", func(t *testing.T, dir string) {
			osStore := newFake(KindKeychain)
			opts := Options{Dir: dir, Stores: []Store{osStore}}
			ks := mustOpen(t, opts)
			if err := ks.SetAccount(token, seed); err != nil {
				t.Fatal(err)
			}
			m := markerIn(t, dir)
			m.Account = ""
			if err := writeMarker(keysDir(dir), m); err != nil {
				t.Fatal(err)
			}
			if err := Destroy(opts); err != nil {
				t.Fatal(err)
			}
			if osStore.size() != 0 {
				t.Errorf("the store still has %d items after Destroy", osStore.size())
			}
		}},
		{"with K_dev in the key file, the sign-in is in files beside it, which Destroy deletes", func(t *testing.T, dir string) {
			opts := Options{Dir: dir, Stores: []Store{}}
			ks := mustOpen(t, opts)
			if err := ks.SetAccount(token, seed); err != nil {
				t.Fatal(err)
			}
			if got, _, err := ks.Account(); err != nil || !bytes.Equal(got, token) {
				t.Fatalf("Account = %q, %v", got, err)
			}
			if err := Destroy(opts); err != nil {
				t.Fatal(err)
			}
			checkCleared(t, dir)
		}},
		{"Chain reads the recorded ledger chain from keystore.json, and is empty before one", func(t *testing.T, dir string) {
			if c, err := Chain(dir); c != "" || err != nil {
				t.Errorf("Chain before a keystore = %q, %v", c, err)
			}
			ks := mustOpen(t, Options{Dir: dir, Stores: []Store{}})
			if c, err := Chain(dir); c != "" || err != nil {
				t.Errorf("Chain before the ledger started one = %q, %v", c, err)
			}
			if err := ks.RecordChain("d_7lq3k2m4nhv5xgjw6ycpbz2rsa"); err != nil {
				t.Fatal(err)
			}
			if c, err := Chain(dir); c != "d_7lq3k2m4nhv5xgjw6ycpbz2rsa" || err != nil {
				t.Errorf("Chain = %q, %v", c, err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { tt.run(t, t.TempDir()) })
	}
}
