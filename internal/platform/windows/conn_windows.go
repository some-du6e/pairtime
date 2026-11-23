// SPDX-License-Identifier: AGPL-3.0-or-later

// Which process owns the client end of a loopback TCP connection, from
// GetExtendedTcpTable (WP-OS13).

package windows

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"unsafe"

	win "golang.org/x/sys/windows"

	"github.com/some-du6e/pairtime/internal/platform"
)

var (
	modIphlpapi             = win.NewLazySystemDLL("iphlpapi.dll")
	procGetExtendedTcpTable = modIphlpapi.NewProc("GetExtendedTcpTable")
)

const (
	// tcpTableOwnerPIDConnections is TCP_TABLE_OWNER_PID_CONNECTIONS: every
	// TCP connection but the listeners, each with its owner's pid.
	tcpTableOwnerPIDConnections = 4
	// mibTCPStateEstab is MIB_TCP_STATE_ESTAB, a row's dwState for an
	// established connection.
	mibTCPStateEstab = 5
	// tcpRowSize is sizeof(MIB_TCPROW_OWNER_PID): dwState, dwLocalAddr,
	// dwLocalPort, dwRemoteAddr, dwRemotePort, dwOwningPid.
	tcpRowSize = 24
	// tcp6RowSize is sizeof(MIB_TCP6ROW_OWNER_PID): ucLocalAddr[16],
	// dwLocalScopeId, dwLocalPort, ucRemoteAddr[16], dwRemoteScopeId,
	// dwRemotePort, dwState, dwOwningPid.
	tcp6RowSize = 56
)

// errNoConn says the table has no row for the connection.
var errNoConn = errors.New("no TCP connection")

// LoopbackOwner returns the pid of the process that owns the client end of
// the loopback TCP connection from client to server, and the process table
// read while it owned it, for the agent's peer-process check (DESIGN 5.2,
// 5.7). client is the connection's remote address as the agent's server
// sees it, which is the client's own local address, and server is the
// server's local address.
//
// GetExtendedTcpTable lists every TCP connection with the pid of the
// process that owns it, which any user may read. The client end is the
// row whose local address is client and whose remote address is server,
// in the IPv4 table or, for a dual-stack socket, the IPv6 one, and only an
// established connection counts. Then the process table is read
// (Processes) and the row read again, and both reads must name the same
// owner. The row names a process of any user; the peer check compares its
// Owner with this user's (internal/agent/peer).
func LoopbackOwner(client, server netip.AddrPort) (int, []platform.Proc, error) {
	client, server, err := loopbackPair(client, server)
	if err != nil {
		return 0, nil, err
	}
	pid, err := tcpOwner(client, server)
	if err != nil {
		return 0, nil, err
	}
	procs, err := Processes()
	if err != nil {
		return 0, nil, err
	}
	again, err := tcpOwner(client, server)
	if err != nil {
		return 0, nil, fmt.Errorf("process %d let go of the connection while the process table was read: %w", pid, err)
	}
	if again != pid {
		return 0, nil, fmt.Errorf("the connection from %v to %v went from process %d to %d while the process table was read", client, server, pid, again)
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

// tcpOwner returns the owner of the established connection from client to
// server: from the IPv4 table, or, when it has no row for the connection,
// from the IPv6 one, where a dual-stack socket's connection to an IPv4
// address has IPv4-mapped addresses.
func tcpOwner(client, server netip.AddrPort) (int, error) {
	rows, err := tcpRows(win.AF_INET)
	if err != nil {
		return 0, err
	}
	pid, err := ownerOf(rows, client, server)
	if !errors.Is(err, errNoConn) {
		return pid, err
	}
	rows6, err6 := tcpRows(win.AF_INET6)
	if err6 != nil {
		return 0, err
	}
	return ownerOf(rows6, client, server)
}

// tcpRow is one row of GetExtendedTcpTable's table.
type tcpRow struct {
	local, remote netip.AddrPort
	state, pid    uint32
}

// ownerOf returns the owner of the established row from client to server.
func ownerOf(rows []tcpRow, client, server netip.AddrPort) (int, error) {
	var match, est []tcpRow
	for _, r := range rows {
		if r.local == client && r.remote == server {
			match = append(match, r)
			if r.state == mibTCPStateEstab {
				est = append(est, r)
			}
		}
	}
	switch {
	case len(match) == 0:
		return 0, fmt.Errorf("%w from %v to %v", errNoConn, client, server)
	case len(est) == 0:
		return 0, fmt.Errorf("the TCP connection from %v to %v isn't established: its state is %d", client, server, match[0].state)
	case len(est) > 1:
		return 0, fmt.Errorf("the TCP table lists %d established connections from %v to %v", len(est), client, server)
	case est[0].pid == 0:
		return 0, fmt.Errorf("no process owns the connection from %v to %v", client, server)
	}
	return int(est[0].pid), nil
}

// tcpRows reads GetExtendedTcpTable's table for the address family af.
func tcpRows(af uint32) ([]tcpRow, error) {
	buf, err := tcpTable(af)
	if err != nil {
		return nil, err
	}
	return parseTCPTable(buf, af == win.AF_INET6)
}

// tcpTable returns what GetExtendedTcpTable fills in for af. Connections
// made between asking for the size and reading the table can outgrow it,
// so it tries a few times with room to spare.
func tcpTable(af uint32) ([]byte, error) {
	if err := procGetExtendedTcpTable.Find(); err != nil {
		return nil, err
	}
	var size uint32
	var buf []byte
	for range 8 {
		var p unsafe.Pointer
		if len(buf) > 0 {
			p = unsafe.Pointer(&buf[0])
		}
		r, _, _ := procGetExtendedTcpTable.Call(uintptr(p), uintptr(unsafe.Pointer(&size)), 0, uintptr(af), tcpTableOwnerPIDConnections, 0)
		switch e := win.Errno(r); e {
		case 0:
			return buf, nil
		case win.ERROR_INSUFFICIENT_BUFFER:
			buf = make([]byte, size+size/8+4096)
			size = uint32(len(buf))
		default:
			return nil, fmt.Errorf("GetExtendedTcpTable: %w", e)
		}
	}
	return nil, errors.New("GetExtendedTcpTable: the table kept outgrowing the buffer")
}

// parseTCPTable reads a MIB_TCPTABLE_OWNER_PID, or with v6 a
// MIB_TCP6TABLE_OWNER_PID: dwNumEntries, then the rows. Addresses are in
// network byte order, and so is each port, in the low 16 bits of its
// DWORD; Windows is little-endian on every architecture pairtime ships
// for.
func parseTCPTable(buf []byte, v6 bool) ([]tcpRow, error) {
	if len(buf) == 0 {
		return nil, nil
	}
	if len(buf) < 4 {
		return nil, fmt.Errorf("GetExtendedTcpTable gave %d bytes, too few for a table", len(buf))
	}
	size := tcpRowSize
	if v6 {
		size = tcp6RowSize
	}
	n := binary.LittleEndian.Uint32(buf)
	if uint64(n) > uint64((len(buf)-4)/size) {
		return nil, fmt.Errorf("GetExtendedTcpTable's table counts %d rows in %d bytes", n, len(buf))
	}
	rows := make([]tcpRow, n)
	for i := range rows {
		b := buf[4+i*size : 4+(i+1)*size]
		if v6 {
			rows[i] = tcpRow{
				local:  netip.AddrPortFrom(netip.AddrFrom16([16]byte(b[0:16])).Unmap(), binary.BigEndian.Uint16(b[20:22])),
				remote: netip.AddrPortFrom(netip.AddrFrom16([16]byte(b[24:40])).Unmap(), binary.BigEndian.Uint16(b[44:46])),
				state:  binary.LittleEndian.Uint32(b[48:52]),
				pid:    binary.LittleEndian.Uint32(b[52:56]),
			}
			continue
		}
		rows[i] = tcpRow{
			state:  binary.LittleEndian.Uint32(b[0:4]),
			local:  netip.AddrPortFrom(netip.AddrFrom4([4]byte(b[4:8])), binary.BigEndian.Uint16(b[8:10])),
			remote: netip.AddrPortFrom(netip.AddrFrom4([4]byte(b[12:16])), binary.BigEndian.Uint16(b[16:18])),
			pid:    binary.LittleEndian.Uint32(b[20:24]),
		}
	}
	return rows, nil
}
