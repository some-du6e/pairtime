// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"github.com/godbus/dbus/v5"
	"github.com/zalando/go-keyring"
)

// heavyModules returns entry points of modules the Linux client will ship
// that nothing calls yet: godbus for idle time, logind and notifications
// (WP-OS4), and go-keyring for the device key in Secret Service (WP-OS7).
// Go's linker drops code that nothing refers to, so a bare import would
// leave these out of the binary. Run keeps the result in linked, so
// ci.yml's size gate counts them in the linux binaries, and
// heavy_darwin.go's modules in the darwin ones, from the first agent build
// on (JE must-fix 4). No gate measures their memory or CPU yet:
// footprint.yml measures the agent on Windows, where none of them is
// linked (heavy_other.go). The work package that starts calling one
// deletes it here.
func heavyModules() []any {
	return []any{
		dbus.ConnectSessionBus,
		dbus.ConnectSystemBus,
		keyring.Get,
		keyring.Set,
		keyring.Delete,
	}
}
