// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package home

import "os"

// createTemp makes the temporary file. os.CreateTemp creates it with mode
// 0600, which already keeps a secret private, so private changes nothing.
func createTemp(dir, prefix string, _ bool) (*os.File, error) {
	return os.CreateTemp(dir, prefix+"*")
}

func rename(from, to string) error {
	return os.Rename(from, to)
}

// syncDir flushes the directory, so the rename itself survives a power
// cut. It is best effort: some file systems refuse to sync a directory, and
// the rename is atomic either way.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	d.Sync()
	d.Close()
}
