// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

package presence

// thisOS has no sources yet. internal/platform/darwin (WP-OS3) and
// internal/platform/linux (WP-OS4) read presence on macOS and Linux, and
// WP-OS9 wires them in here. Until then the sampler records nothing there,
// and every slot's presence is unknown (SM 2.1).
func thisOS() OS {
	return OS{}
}
