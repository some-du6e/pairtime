// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

package presence

// thisOS has no sources of its own here. On macOS and Linux presence shares
// its OS connection with the focus component: one darwin.Loop on the main
// thread, or one linux.Desktop, which the agent opens and passes to MacOS
// or LinuxOS (WP-OS9). Anywhere else the sampler records nothing, and
// every slot's presence is unknown (SM 2.1).
func thisOS() OS {
	return OS{}
}
