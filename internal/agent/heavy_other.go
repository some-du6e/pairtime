// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !linux && !darwin

package agent

// heavyModules has nothing to keep on Windows: every Win32 call the client
// will make goes through golang.org/x/sys/windows, which the lock and the
// private file writes already use, and the device key uses DPAPI from the
// same module rather than go-keyring (WP-OS7). See heavy_linux.go.
func heavyModules() []any {
	return nil
}
