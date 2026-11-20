// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"github.com/zalando/go-keyring"
)

// heavyModules returns entry points of modules the macOS client will ship
// that nothing calls yet: go-keyring for the device key in the Keychain
// (WP-OS7). purego left the list once the procs scan's macOS reader called
// it from the agent (WP-OS6). See heavy_linux.go for why the rest are
// kept here.
func heavyModules() []any {
	return []any{
		keyring.Get,
		keyring.Set,
		keyring.Delete,
	}
}
