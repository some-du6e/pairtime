// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

package focus

// thisOS has no focus source yet. internal/platform/darwin (WP-OS3) and
// internal/platform/linux (WP-OS4) watch focus on macOS and Linux, and
// WP-OS9 wires them in here. Until then the component records nothing
// there, and every slot's focus is none (SM 2.5).
func thisOS() OS {
	return OS{}
}
