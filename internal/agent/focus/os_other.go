// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

package focus

// thisOS has no focus source of its own here. On macOS and Linux focus
// shares its OS connection with presence: one darwin.Loop on the main
// thread, or one linux.Desktop, which the agent opens and passes to MacOS
// or LinuxOS (WP-OS9). Anywhere else the component records nothing.
func thisOS() OS {
	return OS{Unknown: "pairtime reads no focus on this OS"}
}
