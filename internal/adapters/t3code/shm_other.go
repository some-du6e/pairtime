// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

package t3code

import "os"

// openShared opens the file name to read it. Outside Windows an open keeps
// no other process from writing the file or deleting it.
func openShared(name string) (*os.File, error) { return os.Open(name) }
