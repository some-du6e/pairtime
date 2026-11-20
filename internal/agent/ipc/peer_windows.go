// SPDX-License-Identifier: AGPL-3.0-or-later

package ipc

import (
	"net"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// sioAFUnixGetPeerPID is SIO_AF_UNIX_GETPEERPID, _WSAIOR(IOC_VENDOR, 256)
// in afunix.h (CP 6.3).
const sioAFUnixGetPeerPID = windows.IOC_OUT | windows.IOC_VENDOR | 256

// unixPeer returns who is at the other end of c: the peer's pid from
// SIO_AF_UNIX_GETPEERPID, and the user that process runs as. Windows has no
// ancillary data on AF_UNIX, so this is the only way to ask. A Windows build
// that lacks the ioctl fails the agent's self-test, and the agent moves to
// the named pipe.
func unixPeer(c *net.UnixConn) (identity, int, error) {
	rc, err := c.SyscallConn()
	if err != nil {
		return identity{}, 0, err
	}
	var pid uint32
	var ioErr error
	if err := rc.Control(func(fd uintptr) {
		var n uint32
		ioErr = windows.WSAIoctl(windows.Handle(fd), sioAFUnixGetPeerPID, nil, 0,
			(*byte)(unsafe.Pointer(&pid)), uint32(unsafe.Sizeof(pid)), &n, nil, 0)
	}); err != nil {
		return identity{}, 0, err
	}
	if ioErr != nil {
		return identity{}, 0, os.NewSyscallError("WSAIoctl SIO_AF_UNIX_GETPEERPID", ioErr)
	}
	id, err := pidIdentity(pid)
	return id, int(pid), err
}
