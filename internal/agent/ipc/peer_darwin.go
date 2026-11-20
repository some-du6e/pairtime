// SPDX-License-Identifier: AGPL-3.0-or-later

package ipc

import (
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// unixPeer returns who is at the other end of c, from LOCAL_PEERCRED, and
// its pid from LOCAL_PEERPID (CP 6.3). The kernel records both when the
// connection is made. Only the uid decides anything, so a pid the kernel
// won't give is reported as 0.
func unixPeer(c *net.UnixConn) (identity, int, error) {
	rc, err := c.SyscallConn()
	if err != nil {
		return identity{}, 0, err
	}
	var cred *unix.Xucred
	var credErr, pidErr error
	pid := 0
	if err := rc.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		pid, pidErr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	}); err != nil {
		return identity{}, 0, err
	}
	if credErr != nil {
		return identity{}, 0, os.NewSyscallError("getsockopt LOCAL_PEERCRED", credErr)
	}
	if pidErr != nil {
		pid = 0
	}
	return identity{uid: int(cred.Uid)}, pid, nil
}
