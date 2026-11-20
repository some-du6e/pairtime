// SPDX-License-Identifier: AGPL-3.0-or-later

package inbox

import (
	"io/fs"
	"os"
)

// spoolFlags open the spool for Append.
const spoolFlags = os.O_WRONLY | os.O_APPEND | os.O_CREATE

// readFlags open a taken spool file for a drain.
const readFlags = os.O_RDONLY

// privateDir checks that dir, the inbox directory, is a directory. On
// Windows, file modes and owners don't say who may open a file: the data
// directory's DACL, which the inbox inherits, does (internal/agent/home).
func privateDir(dir string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return notPrivate("%s isn't a directory", dir)
	}
	return nil
}

// privateFile checks that fi, a spool file as opened, is a regular file.
func privateFile(name string, fi fs.FileInfo) error {
	if !fi.Mode().IsRegular() {
		return notPrivate("%s isn't a regular file", name)
	}
	return nil
}
