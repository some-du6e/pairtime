// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package home

import "os"

// mkdirAll makes dir and the parents it needs with mode 0700, so only
// this user can open what is inside.
func mkdirAll(dir string) error {
	return os.MkdirAll(dir, 0o700)
}
