// SPDX-License-Identifier: AGPL-3.0-or-later

package install

// Registry reads and writes string values under keys of
// HKEY_CURRENT_USER, which needs no admin rights. The real one exists only
// on Windows; tests on every OS use a map.
type Registry interface {
	// Get returns the value name under key, its type, and whether it is
	// there. A missing key is a missing value.
	Get(key, name string) (value string, typ uint32, ok bool, err error)
	// Set writes the value, creating key if it isn't there.
	Set(key, name, value string, typ uint32) error
	// Delete removes the value. A value that isn't there is no error.
	Delete(key, name string) error
}

// The registry value types install reads and writes.
const (
	RegSZ       uint32 = 1 // REG_SZ
	RegExpandSZ uint32 = 2 // REG_EXPAND_SZ
)
