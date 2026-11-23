// SPDX-License-Identifier: AGPL-3.0-or-later

// Which process holds the client end of a loopback TCP connection, from
// libproc's socket descriptors (WP-OS13). The matching here builds
// everywhere, so ci.yml's Linux job tests it with fakes; conn_darwin.go
// makes the libproc calls.

package darwin

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"slices"

	"github.com/some-du6e/pairtime/internal/platform"
)

// Constants from xnu's bsd/sys/proc_info.h.
const (
	// proxFDTypeSocket is PROX_FDTYPE_SOCKET, a proc_fdinfo's proc_fdtype
	// for a socket.
	proxFDTypeSocket = 2
	// sockinfoTCP is SOCKINFO_TCP, socket_info's soi_kind for a TCP socket,
	// whose soi_proto is a tcp_sockinfo.
	sockinfoTCP = 2
	// iniIPv4 and iniIPv6 are INI_IPV4 and INI_IPV6 in in_sockinfo's
	// insi_vflag: which of the address unions holds the address.
	iniIPv4 = 0x1
	iniIPv6 = 0x2
	// tsiEstablished is TSI_S_ESTABLISHED, tcp_sockinfo's tcpsi_state for
	// an established connection.
	tsiEstablished = 4
	// socketFDInfoSize is PROC_PIDFDSOCKETINFO_SIZE, sizeof(struct
	// socket_fdinfo): a proc_fileinfo of 24 bytes, then a socket_info of
	// 768, whose soi_proto union, at 240, is 528 bytes long because of its
	// un_sockinfo.
	socketFDInfoSize = 792
)

// Offsets into a socket_fdinfo, from proc_info.h's layouts on both macOS
// architectures.
const (
	// offInfo is socket_fdinfo's psi, the socket_info after the
	// proc_fileinfo.
	offInfo = 24
	// offSo is socket_info's soi_so, the kernel's opaque handle of the
	// socket, after a vinfo_stat of 136 bytes.
	offSo = offInfo + 136
	// offKind is socket_info's soi_kind.
	offKind = offInfo + 232
	// offProto is socket_info's soi_proto. A tcp_sockinfo starts with an
	// in_sockinfo: insi_fport, insi_lport, insi_gencnt, insi_flags,
	// insi_flow, insi_vflag, insi_ip_ttl, rfu_1, then insi_faddr and
	// insi_laddr of 16 bytes each, and 80 bytes in all. tcpsi_state follows.
	offProto    = offInfo + 240
	offFPort    = offProto + 0
	offLPort    = offProto + 4
	offVFlag    = offProto + 24
	offFAddr    = offProto + 32
	offLAddr    = offProto + 48
	offTCPState = offProto + 80
	// offAddr4 is where an IPv4 address sits in an insi_faddr or
	// insi_laddr union: in4in6_addr's i46a_addr4, after three words of
	// padding.
	offAddr4 = 12
)

// tcpSock is what a TCP socket's socket_fdinfo says about it.
type tcpSock struct {
	// so is soi_so, the kernel's handle of the socket: two descriptors with
	// the same so are one socket.
	so            uint64
	local, remote netip.AddrPort
	state         int32
}

// parseSocketFDInfo reads a socket_fdinfo as PROC_PIDFDSOCKETINFO fills
// it. It reports false for a socket that isn't TCP over IPv4 or IPv6, and
// an error for a size the layout above doesn't describe.
func parseSocketFDInfo(b []byte) (tcpSock, bool, error) {
	if len(b) != socketFDInfoSize {
		return tcpSock{}, false, fmt.Errorf("a socket_fdinfo of %d bytes, not the %d this code reads", len(b), socketFDInfoSize)
	}
	ne := binary.NativeEndian
	if int32(ne.Uint32(b[offKind:])) != sockinfoTCP {
		return tcpSock{}, false, nil
	}
	var local, remote netip.Addr
	switch v := b[offVFlag]; {
	case v&iniIPv4 != 0:
		local = netip.AddrFrom4([4]byte(b[offLAddr+offAddr4 : offLAddr+offAddr4+4]))
		remote = netip.AddrFrom4([4]byte(b[offFAddr+offAddr4 : offFAddr+offAddr4+4]))
	case v&iniIPv6 != 0:
		local = netip.AddrFrom16([16]byte(b[offLAddr : offLAddr+16])).Unmap()
		remote = netip.AddrFrom16([16]byte(b[offFAddr : offFAddr+16])).Unmap()
	default:
		return tcpSock{}, false, nil
	}
	return tcpSock{
		so:     ne.Uint64(b[offSo:]),
		local:  netip.AddrPortFrom(local, inPort(ne.Uint32(b[offLPort:]))),
		remote: netip.AddrPortFrom(remote, inPort(ne.Uint32(b[offFPort:]))),
		state:  int32(ne.Uint32(b[offTCPState:])),
	}, true, nil
}

// inPort reads in_sockinfo's insi_lport or insi_fport, an int holding the
// pcb's port in network byte order, which lsof turns into the port with
// ntohs.
func inPort(v uint32) uint16 {
	var b [2]byte
	binary.NativeEndian.PutUint16(b[:], uint16(v))
	return binary.BigEndian.Uint16(b[:])
}

// connSource is what LoopbackOwner reads: libproc and sysctl on macOS
// (conn_darwin.go), fakes in tests.
type connSource interface {
	// pids lists the processes that run as this user.
	pids() ([]int, error)
	// socketFDs lists the descriptors of process pid that are sockets
	// (PROC_PIDLISTFDS).
	socketFDs(pid int) ([]int32, error)
	// socketInfo reads the socket_fdinfo of descriptor fd of process pid
	// (PROC_PIDFDSOCKETINFO).
	socketInfo(pid int, fd int32) ([]byte, error)
	// procs reads the process table (Processes).
	procs() ([]platform.Proc, error)
}

// LoopbackOwner returns the pid of the process that holds the client end of
// the loopback TCP connection from client to server, and the process table
// read while it held it, for the agent's peer-process check (DESIGN 5.2,
// 5.7). client is the connection's remote address as the agent's server
// sees it, which is the client's own local address, and server is the
// server's local address.
//
// macOS has no table of connections with their processes, so the lookup
// goes through the socket descriptors of every process that runs as this
// user, which libproc lists without any permission (PROC_PIDLISTFDS), and
// reads each one's socket_fdinfo (PROC_PIDFDSOCKETINFO). The client end is
// the TCP socket whose local address is client and whose foreign address
// is server, and only an established connection counts. Exactly one
// process may hold it: one that no process holds, as after its client
// closed it, or that several do, has no owner. Then the process table is
// read (Processes), and the owner's descriptor is read again, so the
// table's entry for that pid is the process that held the socket all
// along.
//
// Only socket descriptors are read, and of those only the socket's kind,
// addresses, ports and state. Another user's descriptors can't be read, so
// the owner is always this user's process.
func LoopbackOwner(client, server netip.AddrPort) (int, []platform.Proc, error) {
	src, err := osConnSource()
	if err != nil {
		return 0, nil, err
	}
	return loopbackOwner(src, client, server)
}

// held is a descriptor that holds the client end.
type held struct {
	pid int
	fd  int32
	so  uint64
}

func loopbackOwner(src connSource, client, server netip.AddrPort) (int, []platform.Proc, error) {
	client, server, err := loopbackPair(client, server)
	if err != nil {
		return 0, nil, err
	}
	pids, err := src.pids()
	if err != nil {
		return 0, nil, err
	}
	var found []held
	closing := false
	for _, pid := range pids {
		fds, err := src.socketFDs(pid)
		if err != nil {
			continue // exited, or not this user's after all
		}
		for _, fd := range fds {
			s, ok, err := readSocket(src, pid, fd)
			if err != nil {
				return 0, nil, err
			}
			if !ok || s.local != client || s.remote != server {
				continue
			}
			if s.state != tsiEstablished {
				closing = true
				continue
			}
			found = append(found, held{pid: pid, fd: fd, so: s.so})
		}
	}
	var owners []int
	var sockets []uint64
	for _, h := range found {
		if !slices.Contains(owners, h.pid) {
			owners = append(owners, h.pid)
		}
		if !slices.Contains(sockets, h.so) {
			sockets = append(sockets, h.so)
		}
	}
	switch {
	case len(owners) == 0 && closing:
		return 0, nil, fmt.Errorf("the TCP connection from %v to %v isn't established", client, server)
	case len(owners) == 0:
		return 0, nil, fmt.Errorf("no process this user can read holds the connection from %v to %v", client, server)
	case len(sockets) > 1:
		return 0, nil, fmt.Errorf("%d sockets hold the connection from %v to %v", len(sockets), client, server)
	case len(owners) > 1:
		return 0, nil, fmt.Errorf("processes %v all hold the connection from %v to %v, so it has no one owner", owners, client, server)
	}
	procs, err := src.procs()
	if err != nil {
		return 0, nil, err
	}
	h := found[0]
	if s, ok, err := readSocket(src, h.pid, h.fd); err != nil || !ok || s.so != h.so || s.local != client || s.remote != server || s.state != tsiEstablished {
		return 0, nil, fmt.Errorf("process %d let go of the connection from %v to %v while the process table was read", h.pid, client, server)
	}
	return h.pid, procs, nil
}

// readSocket reads and parses descriptor fd of process pid. A descriptor
// that closed since the list was read gives false; only a socket_fdinfo
// whose size this code doesn't know is an error, because nothing read
// through such a layout can be trusted.
func readSocket(src connSource, pid int, fd int32) (tcpSock, bool, error) {
	b, err := src.socketInfo(pid, fd)
	if err != nil {
		return tcpSock{}, false, nil
	}
	return parseSocketFDInfo(b)
}

// errNotLoopback says the addresses given aren't a loopback connection's.
var errNotLoopback = errors.New("isn't a loopback TCP connection")

// loopbackPair returns client and server with IPv4-mapped addresses
// unmapped and zones dropped, and an error unless both are loopback
// addresses with a port.
func loopbackPair(client, server netip.AddrPort) (netip.AddrPort, netip.AddrPort, error) {
	c := netip.AddrPortFrom(client.Addr().Unmap().WithZone(""), client.Port())
	s := netip.AddrPortFrom(server.Addr().Unmap().WithZone(""), server.Port())
	if !c.Addr().IsLoopback() || !s.Addr().IsLoopback() || c.Port() == 0 || s.Port() == 0 {
		return c, s, fmt.Errorf("%v to %v %w", client, server, errNotLoopback)
	}
	return c, s, nil
}
