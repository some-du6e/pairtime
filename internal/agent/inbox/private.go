// SPDX-License-Identifier: AGPL-3.0-or-later

package inbox

import (
	"io/fs"
	"os"
	"path/filepath"
)

// OpenPrivate opens name, a file in which the agent keeps what a spool
// does, hook calls with their prompts included, with the spool's guards
// (DESIGN 3.5; D-held-calls-inbox-class): its folder must be one nobody
// else may make, rename or delete a file in (privateDir), and the file, as
// opened, a regular file of this user's that nobody else may read or
// write, with one name (privateFile). Where the OS has them, O_NOFOLLOW
// refuses a symbolic link in the file's place and O_NONBLOCK keeps a FIFO
// there from holding the open. flag and perm are os.OpenFile's. On Windows
// the data directory's DACL decides who may open the file, as for the
// spool.
func OpenPrivate(name string, flag int, perm fs.FileMode) (*os.File, error) {
	if err := privateDir(filepath.Dir(name)); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(name, flag|guardFlags, perm)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err == nil {
		err = privateFile(name, fi)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// OpenOwned opens name, a file of hook calls the agent cuts or deletes on
// time, to read, as Expire reads a spool file: its folder one Expire would
// sweep (sweepable), and the file, as opened, a regular file of this
// user's with one name (ownFile), whatever its mode or its folder's. No
// other account can have put such a file there, and a cut only ever drops
// lines, so a folder or a file that lost its private mode, as after a
// chmod -R g+w or a copy under umask 002, keeps its calls no longer than
// heldKeep or SpoolKeep. Delivering a call keeps OpenPrivate's guards.
// Where the OS has them, O_NOFOLLOW and O_NONBLOCK are OpenPrivate's.
func OpenOwned(name string) (*os.File, error) {
	if err := privateDir(filepath.Dir(name)); err != nil && !sweepable(filepath.Dir(name)) {
		return nil, err
	}
	f, err := os.OpenFile(name, os.O_RDONLY|guardFlags, 0)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err == nil {
		err = ownFile(name, fi)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
