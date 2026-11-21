// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package inbox

import (
	"io/fs"
	"os"
	"syscall"
)

// spoolFlags open the spool for Append. O_NOFOLLOW refuses a symbolic
// link in its place, and O_NONBLOCK keeps a FIFO there from holding the
// sender until something reads it; privateFile refuses whatever else the
// open lets through.
const spoolFlags = os.O_WRONLY | os.O_APPEND | os.O_CREATE | syscall.O_NOFOLLOW | syscall.O_NONBLOCK

// readFlags open a taken spool file for a drain, with the same guards.
const readFlags = os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK

// guardFlags are those guards alone, which OpenPrivate adds to its flags.
const guardFlags = syscall.O_NOFOLLOW | syscall.O_NONBLOCK

// privateDir checks that dir, the inbox directory, is a directory of this
// user's in which nobody else may make, rename or delete a file. Anyone
// who could would be able to put a file of their own in the spool's place
// and read what senders append, prompts included, or plant messages for
// the agent to drain. A symbolic link at dir is followed only when this
// user made it.
func privateDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		if err := mine(dir, fi); err != nil {
			return err
		}
		if fi, err = os.Stat(dir); err != nil {
			return err
		}
	}
	if !fi.IsDir() {
		return notPrivate("%s isn't a directory", dir)
	}
	if err := mine(dir, fi); err != nil {
		return err
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return notPrivate("others may write in %s (mode %v)", dir, fi.Mode().Perm())
	}
	return nil
}

// privateFile checks fi, a spool file as opened: it must be a regular file
// of this user's that nobody else may read or write, with no second name.
// A second name means someone linked one of this user's files into the
// inbox, to have senders append to it.
func privateFile(name string, fi fs.FileInfo) error {
	if !fi.Mode().IsRegular() {
		return notPrivate("%s isn't a regular file", name)
	}
	if err := mine(name, fi); err != nil {
		return err
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return notPrivate("others may open %s (mode %v)", name, fi.Mode().Perm())
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Nlink > 1 {
		return notPrivate("%s has %d names, and a spool file has one", name, st.Nlink)
	}
	return nil
}

// mine checks that this user owns the file that fi describes.
func mine(name string, fi fs.FileInfo) error {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return notPrivate("can't tell who owns %s", name)
	}
	if me := os.Geteuid(); int(st.Uid) != me {
		return notPrivate("%s belongs to uid %d, not to this user (uid %d)", name, st.Uid, me)
	}
	return nil
}
