// SPDX-License-Identifier: AGPL-3.0-or-later

package home

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// mkdirAll makes dir and the parents it needs, as os.MkdirAll does, and
// creates each directory it makes with userOnlyDir's DACL: protected, with
// one entry that gives the current user full access and that every file
// and directory made inside inherits. That is what mode 0700 does on Unix.
// Windows ignores os.MkdirAll's mode, so a directory made with it takes
// its parent's ACL, and a folder at a drive root, such as C:\pairtime,
// would let every signed-in user read and change state.json and
// config.toml (#46).
// The DACL is there from the moment the directory exists, so no one else
// can open it or make a file in it first.
func mkdirAll(dir string) error {
	if fi, err := os.Stat(dir); err == nil {
		if fi.IsDir() {
			return nil
		}
		return &os.PathError{Op: "mkdir", Path: dir, Err: syscall.ENOTDIR}
	}

	// Make the parent first, unless it is the volume, as os.MkdirAll does.
	i := len(dir) - 1
	for i >= 0 && os.IsPathSeparator(dir[i]) {
		i--
	}
	for i >= 0 && !os.IsPathSeparator(dir[i]) {
		i--
	}
	if parent := dir[:max(i, 0)]; len(parent) > len(filepath.VolumeName(dir)) {
		if err := mkdirAll(parent); err != nil {
			return err
		}
	}

	sa, err := userOnlyDir()
	if err != nil {
		return err
	}
	p, err := windows.UTF16PtrFromString(extendedPath(dir))
	if err != nil {
		return &os.PathError{Op: "mkdir", Path: dir, Err: err}
	}
	if err := windows.CreateDirectory(p, sa); err != nil {
		// Another process may have made it since the Stat, which
		// os.MkdirAll accepts too.
		if fi, lerr := os.Lstat(dir); lerr == nil && fi.IsDir() {
			return nil
		}
		return &os.PathError{Op: "mkdir", Path: dir, Err: err}
	}
	return nil
}

// extendedPath gives an absolute path of 248 characters or more the \\?\
// prefix, without which CreateDirectory refuses it, as os.Mkdir does. Other
// paths come back as they are.
func extendedPath(p string) string {
	if len(p) < 248 || !filepath.IsAbs(p) || strings.HasPrefix(p, `\\?\`) || strings.HasPrefix(p, `\\.\`) {
		return p
	}
	// The prefix turns off Windows' own clean-up of the path, such as
	// turning / into \ and removing . and .., so Clean does it first.
	p = filepath.Clean(p)
	if strings.HasPrefix(p, `\\`) {
		return `\\?\UNC\` + p[2:]
	}
	return `\\?\` + p
}
