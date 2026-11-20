// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package windows

import (
	"bytes"
	"crypto/rand"
	"errors"
	"testing"
)

// These run on windows-latest in platform.yml, against the runner
// account's real DPAPI master key.

func TestDPAPI(t *testing.T) {
	secret := make([]byte, 32)
	rand.Read(secret)
	entropy := []byte("pairtime dpapi test")

	tests := []struct {
		name string
		// check gets a fresh blob of secret under entropy.
		check func(t *testing.T, blob []byte)
	}{
		{"a protected secret unprotects to the same bytes", func(t *testing.T, blob []byte) {
			got, err := Unprotect(blob, entropy)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, secret) {
				t.Error("Unprotect returned other bytes than Protect was given")
			}
		}},
		{"the blob doesn't contain the secret", func(t *testing.T, blob []byte) {
			if bytes.Contains(blob, secret) {
				t.Error("the DPAPI blob holds the secret in the clear")
			}
		}},
		{"other entropy can't unprotect the blob", func(t *testing.T, blob []byte) {
			if _, err := Unprotect(blob, []byte("other entropy")); err == nil {
				t.Error("Unprotect succeeded with the wrong entropy")
			}
		}},
		{"no entropy can't unprotect a blob made with entropy", func(t *testing.T, blob []byte) {
			if _, err := Unprotect(blob, nil); err == nil {
				t.Error("Unprotect succeeded without the entropy")
			}
		}},
		{"a changed byte fails DPAPI's integrity check", func(t *testing.T, blob []byte) {
			// The last bytes are the blob's HMAC over the rest.
			bad := bytes.Clone(blob)
			bad[len(bad)-1] ^= 0x01
			if got, err := Unprotect(bad, entropy); err == nil {
				t.Errorf("Unprotect accepted a changed blob and returned %d bytes", len(got))
			}
		}},
		{"two blobs of one secret differ, because each has its own salt", func(t *testing.T, blob []byte) {
			again, err := Protect(secret, entropy)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(again, blob) {
				t.Error("Protect returned the same blob twice")
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blob, err := Protect(secret, entropy)
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, blob)
		})
	}
}

func TestDPAPIRefusesEmptyInput(t *testing.T) {
	tests := []struct {
		name string
		call func() ([]byte, error)
	}{
		{"Protect refuses an empty secret", func() ([]byte, error) { return Protect(nil, []byte("e")) }},
		{"Unprotect refuses an empty blob", func() ([]byte, error) { return Unprotect([]byte{}, []byte("e")) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.call(); !errors.Is(err, ErrEmpty) {
				t.Errorf("got %v, want ErrEmpty", err)
			}
		})
	}
}
