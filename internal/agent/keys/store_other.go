// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows && !darwin && !linux

package keys

// osStores is empty on an OS pairtime doesn't ship for (DESIGN 3.12 names
// Windows, macOS and Linux), so the key goes in the key file.
func osStores(string) []Store { return nil }
