// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !darwin && !(linux && desktop)

package keys

import "testing"

// plantText reports false: this build has no keyring to plant an item in.
// See plant_keyring_test.go.
func plantText(*testing.T, string, string) bool { return false }
