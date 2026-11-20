// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build darwin || linux

package keys

import (
	"testing"

	"github.com/zalando/go-keyring"
)

// overwriteRaw replaces the item name in the Keychain or the Secret
// Service with value as it is, not hex, through go-keyring, as another
// program could. It reports false for the other stores, which keep raw
// bytes.
func overwriteRaw(t *testing.T, k Kind, name, value string) bool {
	t.Helper()
	if k != KindKeychain && k != KindSecretService {
		return false
	}
	if err := keyring.Set(keyringService, name, value); err != nil {
		t.Fatal(err)
	}
	return true
}
