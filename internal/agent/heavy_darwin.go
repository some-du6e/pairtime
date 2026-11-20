// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"github.com/ebitengine/purego"
	"github.com/zalando/go-keyring"
)

// heavyModules returns entry points of modules the macOS client will ship
// that nothing calls yet: purego for CoreGraphics idle time, IOKit power
// events and NSWorkspace focus (WP-OS3), and go-keyring for the device key
// in the Keychain (WP-OS7). See heavy_linux.go for why they are kept here.
func heavyModules() []any {
	return []any{
		purego.Dlopen,
		purego.Dlsym,
		purego.RegisterLibFunc,
		keyring.Get,
		keyring.Set,
		keyring.Delete,
	}
}
