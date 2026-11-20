// SPDX-License-Identifier: AGPL-3.0-or-later

package ipc

import (
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// unixPeer returns who is at the other end of c, and its pid, from
// SO_PEERCRED, which the kernel records when the connection is made.
func unixPeer(c *net.UnixConn) (identity, int, error) {
	rc, err := c.SyscallConn()
	if err != nil {
		return identity{}, 0, err
	}
	var cred *unix.Ucred
	var credErr error
	if err := rc.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return identity{}, 0, err
	}
	if credErr != nil {
		return identity{}, 0, os.NewSyscallError("getsockopt SO_PEERCRED", credErr)
	}
	return identity{uid: int(cred.Uid)}, int(cred.Pid), nil
}
