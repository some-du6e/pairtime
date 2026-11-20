// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !darwin && !linux

package keys

import "testing"

// overwriteRaw reports false: this OS's stores, DPAPI and the key file,
// keep raw bytes, so no item is in another form. keyring_test.go has the
// macOS and Linux version.
func overwriteRaw(*testing.T, Kind, string, string) bool { return false }
