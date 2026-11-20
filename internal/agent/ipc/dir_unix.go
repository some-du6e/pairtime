// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package ipc

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// secureDir makes dir, the socket's directory, when it is missing, and
// checks that only want can reach a socket in it: dir must be a directory
// that want owns, with no permissions for group or others. One of want's
// with looser permissions is tightened to 0700. One that belongs to
// someone else is refused, because its owner could swap the socket for
// their own (CP 6.3).
//
// The checks and the chmod act on the directory as opened, not on its path
// a second time. A symbolic link at dir is followed only when want made
// it. In a shared parent such as /tmp, anyone else's link there could
// point the agent at some other directory of want's, which would pass the
// checks and be chmodded, and its owner could repoint it later.
func secureDir(dir string, want identity) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	li, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	flags := os.O_RDONLY | syscall.O_DIRECTORY
	if li.Mode()&fs.ModeSymlink != 0 {
		uid, ok := ownerOf(li)
		if !ok {
			return fmt.Errorf("ipc: can't read who made the symbolic link %s", dir)
		}
		if uid != want.uid {
			return fmt.Errorf("ipc: %s is a symbolic link that uid %d made, not this user (%s), so the agent won't put its socket behind it", dir, uid, want)
		}
	} else {
		// It was no link a moment ago; O_NOFOLLOW keeps it that way.
		flags |= syscall.O_NOFOLLOW
	}
	d, err := os.OpenFile(dir, flags, 0)
	if err != nil {
		return err
	}
	defer d.Close()
	fi, err := d.Stat()
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("ipc: %s isn't a directory", dir)
	}
	uid, ok := ownerOf(fi)
	if !ok {
		return fmt.Errorf("ipc: can't read who owns %s", dir)
	}
	if uid != want.uid {
		return fmt.Errorf("ipc: %s belongs to uid %d, not to this user (%s), so the agent won't put its socket there", dir, uid, want)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return d.Chmod(0o700)
	}
	return nil
}

// ownerOf returns the uid that owns the file fi describes.
func ownerOf(fi fs.FileInfo) (int, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Uid), true
}

// secureSocket gives the socket file mode 0600. The directory keeps
// everyone else out already; this is a second lock.
func secureSocket(path string) error {
	return os.Chmod(path, 0o600)
}
