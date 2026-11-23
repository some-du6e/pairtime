// SPDX-License-Identifier: AGPL-3.0-or-later

package windows

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	win "golang.org/x/sys/windows"

	"github.com/some-du6e/pairtime/internal/platform"
)

// connChildEnv, in this test binary's environment, makes it a child that
// connects to the address the variable holds (connChild). TestMain runs it.
const connChildEnv = "PAIRTIME_WINDOWS_CONN_CHILD"

// connChild connects to addr, writes its pid on the connection, and reads
// until the other end shuts for writing, when it writes "eof". Then it
// waits for its standard input to close.
func connChild(addr string) int {
	c, err := net.Dial("tcp4", addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "conn child:", err)
		return 1
	}
	fmt.Fprintf(c, "%d\n", os.Getpid())
	io.Copy(io.Discard, c)
	io.WriteString(c, "eof\n")
	io.Copy(io.Discard, os.Stdin)
	return 0
}

// connChildProc is a running connChild.
type connChildProc struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	// conn is the end of the child's connection that the test accepted.
	conn *net.TCPConn
	r    *bufio.Reader
}

// startConnChild runs this test binary as a connChild of ln's, and returns
// once ln has accepted its connection and the child has written its pid,
// which must be the pid the test started.
func startConnChild(t *testing.T, ln *net.TCPListener) *connChildProc {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), connChildEnv+"="+ln.Addr().String())
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := &connChildProc{cmd: cmd, stdin: stdin}
	t.Cleanup(func() {
		stdin.Close()
		cmd.Process.Kill()
		cmd.Wait()
	})
	c.conn = acceptConn(t, ln)
	c.r = bufio.NewReader(c.conn)
	line, err := c.r.ReadString('\n')
	if err != nil {
		t.Fatalf("reading the child's pid: %v", err)
	}
	if pid, err := strconv.Atoi(strings.TrimSpace(line)); err != nil || pid != cmd.Process.Pid {
		t.Fatalf("the child wrote pid %q, want %d", line, cmd.Process.Pid)
	}
	return c
}

// listenLoopback listens on a free port of 127.0.0.1, as the agent's
// server does (internal/agent/localhttp).
func listenLoopback(t *testing.T) *net.TCPListener {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln.(*net.TCPListener)
}

func acceptConn(t *testing.T, ln *net.TCPListener) *net.TCPConn {
	t.Helper()
	ln.SetDeadline(time.Now().Add(30 * time.Second))
	c, err := ln.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// TestLoopbackOwner opens real loopback connections and checks the pid
// LoopbackOwner finds against the OS's own (PLAN, WP-OS13).
func TestLoopbackOwner(t *testing.T) {
	tests := []struct {
		name string
		// open makes the connection and returns the end the test accepted,
		// the pid that owns the client end, 0 when none should, and the
		// pid of the owner's parent, 0 when the case doesn't check it.
		open func(t *testing.T, ln *net.TCPListener) (conn *net.TCPConn, owner, parent int)
	}{
		{
			name: "a child's connection is the child's, and the table read with it has this process as the child's parent",
			open: func(t *testing.T, ln *net.TCPListener) (*net.TCPConn, int, int) {
				c := startConnChild(t, ln)
				return c.conn, c.cmd.Process.Pid, os.Getpid()
			},
		},
		{
			name: "this process's own connection is its own",
			open: func(t *testing.T, ln *net.TCPListener) (*net.TCPConn, int, int) {
				d, err := net.Dial("tcp4", ln.Addr().String())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { d.Close() })
				return acceptConn(t, ln), os.Getpid(), 0
			},
		},
		{
			name: "a connection whose client has exited has no owner",
			open: func(t *testing.T, ln *net.TCPListener) (*net.TCPConn, int, int) {
				c := startConnChild(t, ln)
				c.cmd.Process.Kill()
				c.cmd.Wait()
				return c.conn, 0, 0
			},
		},
		{
			name: "a connection the server shut for writing isn't established, so it has no owner",
			open: func(t *testing.T, ln *net.TCPListener) (*net.TCPConn, int, int) {
				c := startConnChild(t, ln)
				if err := c.conn.CloseWrite(); err != nil {
					t.Fatal(err)
				}
				// The child writes "eof" once its read has seen the shutdown,
				// so its end is in CLOSE_WAIT by then.
				if line, err := c.r.ReadString('\n'); err != nil || line != "eof\n" {
					t.Fatalf("the child wrote %q, %v; want eof", line, err)
				}
				return c.conn, 0, 0
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ln := listenLoopback(t)
			conn, want, parent := tt.open(t, ln)
			client := conn.RemoteAddr().(*net.TCPAddr).AddrPort()
			server := conn.LocalAddr().(*net.TCPAddr).AddrPort()
			pid, procs, err := LoopbackOwner(client, server)
			if want == 0 {
				if err == nil {
					t.Fatalf("LoopbackOwner(%v, %v) = %d, want an error", client, server, pid)
				}
				return
			}
			if err != nil {
				t.Errorf("LoopbackOwner(%v, %v): %v", client, server, err)
				describeRows(t, server.Port())
				return
			}
			if pid != want {
				t.Errorf("LoopbackOwner(%v, %v) = %d, want %d", client, server, pid, want)
				describeRows(t, server.Port())
			}
			chain := platform.NewTable(platform.Windows, procs).Chain(pid)
			if len(chain) == 0 {
				t.Fatalf("the table LoopbackOwner read lacks process %d", pid)
			}
			if parent != 0 && (len(chain) < 2 || chain[1].PID != parent) {
				t.Errorf("the table gives process %d the chain %v, want its parent %d", pid, chain, parent)
			}
		})
	}
}

// describeRows logs every row of both tables with port at either end, so a
// layout that parseTCPTable reads wrongly shows in the log.
func describeRows(t *testing.T, port uint16) {
	t.Helper()
	for _, af := range []uint32{win.AF_INET, win.AF_INET6} {
		rows, err := tcpRows(af)
		if err != nil {
			t.Logf("table %d: %v", af, err)
			continue
		}
		for _, r := range rows {
			if r.local.Port() == port || r.remote.Port() == port {
				t.Logf("table %d: %+v", af, r)
			}
		}
	}
}

// tcpRowBytes lays out a MIB_TCPROW_OWNER_PID, or with v6 a
// MIB_TCP6ROW_OWNER_PID, as GetExtendedTcpTable fills it.
func tcpRowBytes(r tcpRow, v6 bool) []byte {
	le := binary.LittleEndian
	if v6 {
		b := make([]byte, tcp6RowSize)
		l, rm := r.local.Addr().As16(), r.remote.Addr().As16()
		copy(b[0:16], l[:])
		binary.BigEndian.PutUint16(b[20:22], r.local.Port())
		copy(b[24:40], rm[:])
		binary.BigEndian.PutUint16(b[44:46], r.remote.Port())
		le.PutUint32(b[48:52], r.state)
		le.PutUint32(b[52:56], r.pid)
		return b
	}
	b := make([]byte, tcpRowSize)
	le.PutUint32(b[0:4], r.state)
	l, rm := r.local.Addr().As4(), r.remote.Addr().As4()
	copy(b[4:8], l[:])
	binary.BigEndian.PutUint16(b[8:10], r.local.Port())
	// The upper 16 bits of a port's DWORD may hold anything.
	b[10], b[11] = 0xAB, 0xCD
	copy(b[12:16], rm[:])
	binary.BigEndian.PutUint16(b[16:18], r.remote.Port())
	le.PutUint32(b[20:24], r.pid)
	return b
}

// tcpTableBytes lays out a table of rows.
func tcpTableBytes(count uint32, rows [][]byte) []byte {
	b := binary.LittleEndian.AppendUint32(nil, count)
	for _, r := range rows {
		b = append(b, r...)
	}
	return b
}

func TestParseTCPTable(t *testing.T) {
	client := netip.MustParseAddrPort("127.0.0.1:54321")
	agent := netip.MustParseAddrPort("127.0.0.1:7247")
	mapped := func(ap netip.AddrPort) netip.AddrPort {
		return netip.AddrPortFrom(netip.AddrFrom16(ap.Addr().As16()), ap.Port())
	}
	clientRow := tcpRow{local: client, remote: agent, state: mibTCPStateEstab, pid: 300}
	agentRow := tcpRow{local: agent, remote: client, state: mibTCPStateEstab, pid: 400}
	tests := []struct {
		name string
		buf  []byte
		v6   bool
		want []tcpRow
		err  bool
	}{
		{
			name: "IPv4 rows read their addresses, ports, state and owner, ignoring a port's upper bits",
			buf:  tcpTableBytes(2, [][]byte{tcpRowBytes(clientRow, false), tcpRowBytes(agentRow, false)}),
			want: []tcpRow{clientRow, agentRow},
		},
		{
			name: "IPv6 rows with IPv4-mapped addresses read as IPv4",
			buf:  tcpTableBytes(1, [][]byte{tcpRowBytes(tcpRow{local: mapped(client), remote: mapped(agent), state: 8, pid: 301}, true)}),
			v6:   true,
			want: []tcpRow{{local: client, remote: agent, state: 8, pid: 301}},
		},
		{
			name: "bytes past the counted rows are ignored",
			buf:  append(tcpTableBytes(1, [][]byte{tcpRowBytes(clientRow, false)}), make([]byte, 100)...),
			want: []tcpRow{clientRow},
		},
		{
			name: "an empty buffer is an empty table",
		},
		{
			name: "a count of more rows than the buffer holds is an error",
			buf:  tcpTableBytes(2, [][]byte{tcpRowBytes(clientRow, false)}),
			err:  true,
		},
		{
			name: "a buffer too short for its count is an error",
			buf:  []byte{1, 0},
			err:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTCPTable(tt.buf, tt.v6)
			if (err != nil) != tt.err {
				t.Fatalf("parseTCPTable error = %v, want an error: %v", err, tt.err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("parseTCPTable = %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("row %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestOwnerOf(t *testing.T) {
	client := netip.MustParseAddrPort("127.0.0.1:54321")
	agent := netip.MustParseAddrPort("127.0.0.1:7247")
	agentRow := tcpRow{local: agent, remote: client, state: mibTCPStateEstab, pid: 400}
	tests := []struct {
		name string
		rows []tcpRow
		want int // 0 for an error
	}{
		{
			name: "the established row from the client to the server names the owner",
			rows: []tcpRow{agentRow, {local: client, remote: agent, state: mibTCPStateEstab, pid: 300}},
			want: 300,
		},
		{
			name: "the server's own row isn't the client's",
			rows: []tcpRow{agentRow},
		},
		{
			name: "a row in CLOSE_WAIT isn't established",
			rows: []tcpRow{agentRow, {local: client, remote: agent, state: 8, pid: 300}},
		},
		{
			name: "a row whose owner is pid 0 has no owner",
			rows: []tcpRow{agentRow, {local: client, remote: agent, state: mibTCPStateEstab}},
		},
		{
			name: "two established rows for one connection have no one owner",
			rows: []tcpRow{{local: client, remote: agent, state: mibTCPStateEstab, pid: 300}, {local: client, remote: agent, state: mibTCPStateEstab, pid: 301}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pid, err := ownerOf(tt.rows, client, agent)
			if tt.want == 0 {
				if err == nil {
					t.Fatalf("ownerOf = %d, want an error", pid)
				}
				return
			}
			if err != nil || pid != tt.want {
				t.Errorf("ownerOf = %d, %v; want %d", pid, err, tt.want)
			}
		})
	}
}
