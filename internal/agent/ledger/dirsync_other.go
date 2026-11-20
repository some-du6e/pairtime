// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

package ledger

import "os"

// syncDir flushes dir, so a file just made in it, or renamed into it, is
// still there after a power cut. It is best effort: some file systems
// refuse to sync a directory, and the file's own data is flushed either
// way.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	d.Sync()
	d.Close()
}
