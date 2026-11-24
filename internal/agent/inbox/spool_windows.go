// SPDX-License-Identifier: AGPL-3.0-or-later

package inbox

import (
	"io/fs"
	"os"

	"golang.org/x/sys/windows"
)

// spoolFlags open the spool for Append.
const spoolFlags = os.O_WRONLY | os.O_APPEND | os.O_CREATE

// readFlags open a taken spool file for a drain.
const readFlags = os.O_RDONLY

// openShared opens a spool file for Scan to read, sharing delete as well
// as read and write, which os.OpenFile doesn't: so while Scan reads it, a
// drain can still rename it (take) and delete it, and a sender append to
// it.
func openShared(name string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	return os.NewFile(uintptr(h), name), nil
}

// guardFlags are the flags OpenPrivate adds: none, since the data
// directory's DACL decides who may open what is in it.
const guardFlags = 0

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

// ownFile checks that fi, a spool file as Expire opened it, is a regular
// file, as privateFile does: the data directory's DACL decides who may
// open what is in it.
func ownFile(name string, fi fs.FileInfo) error { return privateFile(name, fi) }

// sweepable reports whether Expire may sweep dir, the inbox directory,
// that privateDir refused: a directory, as privateDir asks no more.
func sweepable(dir string) bool {
	fi, err := os.Stat(dir)
	return err == nil && fi.IsDir()
}

// ExposedPaths finds none on Windows, where the data directory's DACL, not
// file modes, decides who may open what is in it (internal/agent/home).
func ExposedPaths(paths ...string) []Exposed { return nil }
