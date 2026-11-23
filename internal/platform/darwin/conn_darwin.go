// SPDX-License-Identifier: AGPL-3.0-or-later

package darwin

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sync"

	"golang.org/x/sys/unix"

	"github.com/some-du6e/pairtime/internal/platform"
)

// Flavors of proc_pidinfo and proc_pidfdinfo, from proc_info.h.
const (
	// procPIDListFDs is PROC_PIDLISTFDS: a process's descriptors, each a
	// proc_fdinfo of procFDInfoSize bytes (proc_fd, proc_fdtype).
	procPIDListFDs = 1
	procFDInfoSize = 8
	// procPIDFDSocketInfo is PROC_PIDFDSOCKETINFO: a socket descriptor's
	// socket_fdinfo.
	procPIDFDSocketInfo = 3
)

// libprocCalls are libproc's proc_pidinfo and proc_pidfdinfo, from
// libSystem as WP-OS6's proc_pidpath is (procs_darwin.go). Both return the
// bytes they filled, and 0 when they fail.
type libprocCalls struct {
	pidInfo   func(pid, flavor int32, arg uint64, buf *byte, size int32) int32
	pidFDInfo func(pid, fd, flavor int32, buf *byte, size int32) int32
}

// libproc binds libprocCalls the first time a lookup needs them.
var libproc = sync.OnceValues(func() (*libprocCalls, error) {
	var c libprocCalls
	sys := dlopen(pathLibSystem)
	sys.fn(&c.pidInfo, "proc_pidinfo")
	sys.fn(&c.pidFDInfo, "proc_pidfdinfo")
	if sys.err != nil {
		return nil, sys.err
	}
	return &c, nil
})

// libprocSource is the connSource of a running macOS.
type libprocSource struct {
	c  *libprocCalls
	me int // this process's effective uid
}

func osConnSource() (connSource, error) {
	c, err := libproc()
	if err != nil {
		return nil, err
	}
	return libprocSource{c: c, me: os.Geteuid()}, nil
}

// pids lists the processes whose effective uid is this process's, from
// sysctl(kern.proc.uid), leaving zombies out.
func (s libprocSource) pids() ([]int, error) {
	kps, err := unix.SysctlKinfoProcSlice("kern.proc.uid", s.me)
	if err != nil {
		return nil, err
	}
	pids := make([]int, 0, len(kps))
	for i := range kps {
		if kps[i].Proc.P_stat == sZomb || kps[i].Proc.P_pid <= 0 {
			continue
		}
		pids = append(pids, int(kps[i].Proc.P_pid))
	}
	return pids, nil
}

// socketFDs lists pid's socket descriptors. Called without a buffer,
// PROC_PIDLISTFDS gives the size of the process's descriptor table with
// room for 20 more; a list that fills the buffer may have been cut short,
// so it is read again into a bigger one.
func (s libprocSource) socketFDs(pid int) ([]int32, error) {
	size := s.c.pidInfo(int32(pid), procPIDListFDs, 0, nil, 0)
	if size <= 0 {
		return nil, fmt.Errorf("proc_pidinfo(%d, PROC_PIDLISTFDS) gave no size", pid)
	}
	for range 4 {
		buf := make([]byte, int(size)+16*procFDInfoSize)
		n := s.c.pidInfo(int32(pid), procPIDListFDs, 0, &buf[0], int32(len(buf)))
		if n <= 0 {
			return nil, fmt.Errorf("proc_pidinfo(%d, PROC_PIDLISTFDS) failed", pid)
		}
		if int(n) < len(buf) {
			return socketFDsIn(buf[:n]), nil
		}
		size = int32(len(buf)) * 2
	}
	return nil, errors.New("proc_pidinfo(PROC_PIDLISTFDS): the descriptor list kept outgrowing its buffer")
}

// socketFDsIn returns the socket descriptors among proc_fdinfo entries.
func socketFDsIn(b []byte) []int32 {
	var fds []int32
	for i := 0; i+procFDInfoSize <= len(b); i += procFDInfoSize {
		if binary.NativeEndian.Uint32(b[i+4:]) == proxFDTypeSocket {
			fds = append(fds, int32(binary.NativeEndian.Uint32(b[i:])))
		}
	}
	return fds
}

// socketInfo reads descriptor fd's socket_fdinfo. The kernel refuses a
// buffer smaller than the struct and fills exactly the struct, so a size
// other than socketFDInfoSize shows a layout parseSocketFDInfo doesn't
// know, and parseSocketFDInfo refuses it.
func (s libprocSource) socketInfo(pid int, fd int32) ([]byte, error) {
	buf := make([]byte, socketFDInfoSize)
	n := s.c.pidFDInfo(int32(pid), fd, procPIDFDSocketInfo, &buf[0], int32(len(buf)))
	if n <= 0 {
		return nil, fmt.Errorf("proc_pidfdinfo(%d, %d, PROC_PIDFDSOCKETINFO) failed", pid, fd)
	}
	return buf[:min(int(n), len(buf))], nil
}

func (libprocSource) procs() ([]platform.Proc, error) { return Processes() }
