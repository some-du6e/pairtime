// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build darwin || (linux && desktop)

package keys

import (
	"testing"

	"github.com/zalando/go-keyring"
)

// plantText replaces the item name in this OS's keyring with text, through
// go-keyring, as any other program the user runs could. It reports
// whether it did: the keyring is the login Keychain on macOS, and in the
// Linux desktop build the Secret Service that fakesecret provides.
func plantText(t *testing.T, name, text string) bool {
	t.Helper()
	if err := keyring.Set(keyringService, name, text); err != nil {
		t.Fatal(err)
	}
	return true
}
