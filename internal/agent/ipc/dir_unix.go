// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package ipc

import (
	"fmt"
	"os"
	"syscall"
)

// secureDir makes dir, the socket's directory, when it is missing, and
// checks that only want can reach a socket in it: dir must be a directory
// that want owns, with no permissions for group or others. One of want's
// with looser permissions is tightened to 0700. One that belongs to
// someone else is refused, because its owner could swap the socket for
// their own (CP 6.3).
func secureDir(dir string, want identity) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("ipc: %s isn't a directory", dir)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("ipc: can't read who owns %s", dir)
	}
	if int(st.Uid) != want.uid {
		return fmt.Errorf("ipc: %s belongs to uid %d, not to this user (%s), so the agent won't put its socket there", dir, st.Uid, want)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return os.Chmod(dir, 0o700)
	}
	return nil
}

// secureSocket gives the socket file mode 0600. The directory keeps
// everyone else out already; this is a second lock.
func secureSocket(path string) error {
	return os.Chmod(path, 0o600)
}
