// SPDX-License-Identifier: AGPL-3.0-or-later

// Which process holds the client end of a loopback TCP connection, from
// /proc/net/tcp and /proc/<pid>/fd (WP-OS13).

package linux

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"strconv"

	"golang.org/x/sys/unix"

	"github.com/some-du6e/pairtime/internal/platform"
)

// tcpEstablished is TCP_ESTABLISHED, a socket's st in /proc/net/tcp.
const tcpEstablished = 0x01

// LoopbackOwner returns the pid of the process that holds the client end of
// the loopback TCP connection from client to server, and the process table
// read while it held it, for the agent's peer-process check (DESIGN 5.2,
// 5.7). client is the connection's remote address as the agent's server
// sees it, which is the client's own local address, and server is the
// server's local address.
//
// The client end is the socket that /proc/net/tcp, or tcp6 for a dual-stack
// socket, lists with client as its local address and server as its remote
// one. Only an established connection counts, and only one this user's
// process made. The row names the socket's inode, and the process that
// holds the socket has a descriptor under /proc/<pid>/fd linked to
// socket:[inode]. Every process this user can read is looked at, and
// exactly one may hold the socket: one that no process holds, as after its
// client closed it, or that several do, has no owner. Then the process
// table is read (Processes), and the owner's descriptors are looked at
// again, so the table's entry for that pid is the process that held the
// socket all along, not a later process given its pid.
//
// Of a descriptor only its link is read, and only to compare it with the
// socket's: a link that names a file is dropped, never kept.
func LoopbackOwner(client, server netip.AddrPort) (int, []platform.Proc, error) {
	return loopbackOwner("/proc", uint32(os.Geteuid()), client, server)
}

// loopbackOwner is LoopbackOwner over the /proc tree at root, for a
// process whose effective uid is me.
func loopbackOwner(root string, me uint32, client, server netip.AddrPort) (int, []platform.Proc, error) {
	client, server, err := loopbackPair(client, server)
	if err != nil {
		return 0, nil, err
	}
	s, err := findSocket(root, client, server)
	if err != nil {
		return 0, nil, err
	}
	if s.uid != me {
		return 0, nil, fmt.Errorf("the connection from %v to %v is another user's: uid %d made it", client, server, s.uid)
	}
	link := socketLink(s.inode)
	buf := make([]byte, len(link)+1)
	pid, err := holder(root, link, buf)
	if err != nil {
		return 0, nil, fmt.Errorf("the connection from %v to %v: %w", client, server, err)
	}
	procs, err := readProc(root)
	if err != nil {
		return 0, nil, err
	}
	if ok, _ := holds(root, pid, link, buf); !ok {
		return 0, nil, fmt.Errorf("process %d let go of the connection from %v to %v while the process table was read", pid, client, server)
	}
	return pid, procs, nil
}

// loopbackPair returns client and server with IPv4-mapped addresses
// unmapped and zones dropped, and an error unless both are loopback
// addresses with a port.
func loopbackPair(client, server netip.AddrPort) (netip.AddrPort, netip.AddrPort, error) {
	c := netip.AddrPortFrom(client.Addr().Unmap().WithZone(""), client.Port())
	s := netip.AddrPortFrom(server.Addr().Unmap().WithZone(""), server.Port())
	if !c.Addr().IsLoopback() || !s.Addr().IsLoopback() || c.Port() == 0 || s.Port() == 0 {
		return c, s, fmt.Errorf("%v to %v isn't a loopback TCP connection", client, server)
	}
	return c, s, nil
}

// tcpSocket is one row of /proc/net/tcp or tcp6.
type tcpSocket struct {
	local, remote netip.AddrPort
	state         uint8
	uid           uint32
	inode         uint64
}

// findSocket returns the socket of the established connection from client
// to server, which /proc/net/tcp lists for IPv4 sockets and tcp6 for IPv6
// ones, IPv4-mapped addresses included.
func findSocket(root string, client, server netip.AddrPort) (tcpSocket, error) {
	var match []tcpSocket
	for _, name := range []string{"/net/tcp", "/net/tcp6"} {
		data, err := os.ReadFile(root + name)
		if errors.Is(err, fs.ErrNotExist) && name == "/net/tcp6" {
			continue // a kernel without IPv6
		}
		if err != nil {
			return tcpSocket{}, err
		}
		for _, s := range parseTCP(data) {
			if s.local == client && s.remote == server {
				match = append(match, s)
			}
		}
	}
	var est []tcpSocket
	for _, s := range match {
		if s.state == tcpEstablished {
			est = append(est, s)
		}
	}
	switch {
	case len(match) == 0:
		return tcpSocket{}, fmt.Errorf("no TCP connection from %v to %v in /proc/net/tcp", client, server)
	case len(est) == 0:
		return tcpSocket{}, fmt.Errorf("the TCP connection from %v to %v isn't established: its state is %02X", client, server, match[0].state)
	case len(est) > 1:
		return tcpSocket{}, fmt.Errorf("/proc/net/tcp lists %d established connections from %v to %v", len(est), client, server)
	case est[0].inode == 0:
		return tcpSocket{}, fmt.Errorf("no process holds the connection from %v to %v", client, server)
	}
	return est[0], nil
}

// parseTCP parses /proc/net/tcp or tcp6 (proc_net_tcp(5)): a header line,
// then one socket per line. A line that doesn't parse is skipped.
func parseTCP(data []byte) []tcpSocket {
	var out []tcpSocket
	first := true
	for line := range bytes.SplitSeq(data, []byte("\n")) {
		if first {
			first = false
			continue
		}
		if s, ok := parseTCPLine(line); ok {
			out = append(out, s)
		}
	}
	return out
}

// parseTCPLine parses one socket line: sl, local_address, rem_address, st,
// tx_queue:rx_queue, tr:tm->when, retrnsmt, uid, timeout, inode, then
// fields parseTCPLine doesn't read.
func parseTCPLine(line []byte) (tcpSocket, bool) {
	f := bytes.Fields(line)
	if len(f) < 10 {
		return tcpSocket{}, false
	}
	local, ok := parseHexAddrPort(f[1])
	if !ok {
		return tcpSocket{}, false
	}
	remote, ok := parseHexAddrPort(f[2])
	if !ok {
		return tcpSocket{}, false
	}
	st, err := strconv.ParseUint(string(f[3]), 16, 8)
	if err != nil {
		return tcpSocket{}, false
	}
	uid, err := strconv.ParseUint(string(f[7]), 10, 32)
	if err != nil {
		return tcpSocket{}, false
	}
	inode, err := strconv.ParseUint(string(f[9]), 10, 64)
	if err != nil {
		return tcpSocket{}, false
	}
	return tcpSocket{local: local, remote: remote, state: uint8(st), uid: uint32(uid), inode: inode}, true
}

// parseHexAddrPort parses an address of /proc/net/tcp, such as
// 0100007F:1C4F for 127.0.0.1:7247 on a little-endian machine. The kernel
// prints the address as the 32-bit words it stores in network byte order,
// each as a number in this machine's byte order, so each word's bytes are
// its value in native order. The port is a plain number. IPv4-mapped IPv6
// addresses come back as IPv4.
func parseHexAddrPort(b []byte) (netip.AddrPort, bool) {
	addr, port, ok := bytes.Cut(b, []byte(":"))
	if !ok {
		return netip.AddrPort{}, false
	}
	p, err := strconv.ParseUint(string(port), 16, 16)
	if err != nil {
		return netip.AddrPort{}, false
	}
	var ip netip.Addr
	switch len(addr) {
	case 8:
		var a [4]byte
		if !hexWord(addr, a[:]) {
			return netip.AddrPort{}, false
		}
		ip = netip.AddrFrom4(a)
	case 32:
		var a [16]byte
		for i := range 4 {
			if !hexWord(addr[8*i:8*i+8], a[4*i:4*i+4]) {
				return netip.AddrPort{}, false
			}
		}
		ip = netip.AddrFrom16(a).Unmap()
	default:
		return netip.AddrPort{}, false
	}
	return netip.AddrPortFrom(ip, uint16(p)), true
}

// hexWord parses 8 hex digits as a 32-bit number and puts it into out in
// this machine's byte order.
func hexWord(hex, out []byte) bool {
	v, err := strconv.ParseUint(string(hex), 16, 32)
	if err != nil {
		return false
	}
	binary.NativeEndian.PutUint32(out, uint32(v))
	return true
}

// socketLink is the link that a descriptor of the socket with this inode
// has under /proc/<pid>/fd.
func socketLink(inode uint64) []byte {
	return []byte("socket:[" + strconv.FormatUint(inode, 10) + "]")
}

// holder returns the one process under root with a descriptor linked to
// link. buf, which is longer than link, holds each link read. Processes
// whose descriptors can't be read, another user's or one that exited, are
// skipped.
func holder(root string, link, buf []byte) (int, error) {
	d, err := os.Open(root)
	if err != nil {
		return 0, err
	}
	names, err := d.Readdirnames(-1)
	d.Close()
	if err != nil {
		return 0, err
	}
	var pids []int
	for _, n := range names {
		pid, err := strconv.Atoi(n)
		if err != nil || pid <= 0 {
			continue
		}
		if ok, _ := holds(root, pid, link, buf); ok {
			pids = append(pids, pid)
		}
	}
	switch len(pids) {
	case 0:
		return 0, errors.New("no process this user can read holds it")
	case 1:
		return pids[0], nil
	}
	return 0, fmt.Errorf("processes %v all hold it, so it has no one owner", pids)
}

// holds reports whether a descriptor of process pid under root is linked to
// link. buf must be longer than link, so that a longer link, which readlink
// cuts to buf's length, never compares equal.
func holds(root string, pid int, link, buf []byte) (bool, error) {
	dir := root + "/" + strconv.Itoa(pid) + "/fd"
	d, err := os.Open(dir)
	if err != nil {
		return false, err
	}
	fds, err := d.Readdirnames(-1)
	d.Close()
	if err != nil {
		return false, err
	}
	for _, fd := range fds {
		n, err := unix.Readlink(dir+"/"+fd, buf)
		if err == nil && n == len(link) && bytes.Equal(buf[:n], link) {
			return true, nil
		}
	}
	return false, nil
}
