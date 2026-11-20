// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

package presence

// thisOS has no sources yet: macOS and Linux presence come with WP-OS3 and
// WP-OS4. Until then the sampler records nothing there, and every slot's
// presence is unknown (SM 2.1).
func thisOS() OS {
	return OS{}
}
