// SPDX-License-Identifier: AGPL-3.0-or-later

package linux

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/platform"
)

// connChildEnv, in this test binary's environment, makes it a child that
// connects to the address the variable holds (connChild).
const connChildEnv = "PAIRTIME_LINUX_CONN_CHILD"

func TestMain(m *testing.M) {
	if addr := os.Getenv(connChildEnv); addr != "" {
		os.Exit(connChild(addr))
	}
	os.Exit(m.Run())
}

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

// connAddrs returns the client and server addresses of c, a connection the
// test's listener accepted.
func connAddrs(c *net.TCPConn) (client, server netip.AddrPort) {
	return c.RemoteAddr().(*net.TCPAddr).AddrPort(), c.LocalAddr().(*net.TCPAddr).AddrPort()
}

// TestLoopbackOwner opens real loopback connections and checks the pid
// LoopbackOwner finds against the OS's own (PLAN, WP-OS13).
func TestLoopbackOwner(t *testing.T) {
	tests := []struct {
		name string
		// open makes the connection and returns the end the test accepted,
		// the pid that holds the client end, 0 when none should, and the
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
			client, server := connAddrs(conn)
			pid, procs, err := LoopbackOwner(client, server)
			if want == 0 {
				if err == nil {
					t.Fatalf("LoopbackOwner(%v, %v) = %d, want an error", client, server, pid)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoopbackOwner(%v, %v): %v", client, server, err)
			}
			if pid != want {
				t.Errorf("LoopbackOwner(%v, %v) = %d, want %d", client, server, pid, want)
			}
			chain := platform.NewTable(platform.Linux, procs).Chain(pid)
			if len(chain) == 0 {
				t.Fatalf("the table LoopbackOwner read lacks process %d", pid)
			}
			if parent != 0 && (len(chain) < 2 || chain[1].PID != parent) {
				t.Errorf("the table gives process %d the chain %v, want its parent %d", pid, chain, parent)
			}
		})
	}
}

// hexAddrPort writes ap as /proc/net/tcp does on this machine: each 32-bit
// word of the address, held in network byte order, printed as a number in
// native order, then the port.
func hexAddrPort(ap netip.AddrPort, v6 bool) string {
	var b []byte
	if v6 {
		a := ap.Addr().As16()
		b = a[:]
	} else {
		a := ap.Addr().As4()
		b = a[:]
	}
	var s strings.Builder
	for i := 0; i < len(b); i += 4 {
		fmt.Fprintf(&s, "%08X", binary.NativeEndian.Uint32(b[i:i+4]))
	}
	fmt.Fprintf(&s, ":%04X", ap.Port())
	return s.String()
}

// tcpLine is one socket line of /proc/net/tcp, with the fields the parser
// reads set and the rest as an idle socket has them.
func tcpLine(sl int, local, remote string, state uint8, uid uint32, inode uint64) string {
	return fmt.Sprintf("%4d: %s %s %02X 00000000:00000000 00:00000000 00000000 %5d        0 %d 1 0000000000000000 20 4 30 10 -1\n",
		sl, local, remote, state, uid, inode)
}

const tcpHeader = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"

var (
	agentAddr  = netip.MustParseAddrPort("127.0.0.1:7247")
	clientAddr = netip.MustParseAddrPort("127.0.0.1:54321")
)

func TestParseTCP(t *testing.T) {
	littleEndian := binary.NativeEndian.Uint16([]byte{1, 0}) == 1
	tests := []struct {
		name string
		// literal marks lines written as a little-endian machine's kernel
		// writes them; on another machine the case is skipped.
		literal bool
		data    string
		want    []tcpSocket
	}{
		{
			name:    "an IPv4 line reads as a little-endian machine prints it",
			literal: true,
			data:    tcpHeader + "   0: 0100007F:D431 0100007F:1C4F 01 00000000:00000000 00:00000000 00000000  1000        0 98765 1 0000000000000000 20 4 30 10 -1\n",
			want:    []tcpSocket{{local: clientAddr, remote: agentAddr, state: tcpEstablished, uid: 1000, inode: 98765}},
		},
		{
			name:    "an IPv4-mapped address in tcp6 reads as IPv4",
			literal: true,
			data:    tcpHeader + "   0: 0000000000000000FFFF00000100007F:D431 0000000000000000FFFF00000100007F:1C4F 01 00000000:00000000 00:00000000 00000000  1000        0 98766 1 0000000000000000 20 4 30 10 -1\n",
			want:    []tcpSocket{{local: clientAddr, remote: agentAddr, state: tcpEstablished, uid: 1000, inode: 98766}},
		},
		{
			name:    "::1 in tcp6 reads as ::1",
			literal: true,
			data:    tcpHeader + "   0: 00000000000000000000000001000000:D431 00000000000000000000000001000000:1C4F 08 00000000:00000000 00:00000000 00000000  1000        0 5 1 0000000000000000 20 4 30 10 -1\n",
			want: []tcpSocket{{
				local:  netip.MustParseAddrPort("[::1]:54321"),
				remote: netip.MustParseAddrPort("[::1]:7247"),
				state:  0x08, uid: 1000, inode: 5,
			}},
		},
		{
			name: "this machine's own encoding reads back",
			data: tcpHeader + tcpLine(0, hexAddrPort(clientAddr, false), hexAddrPort(agentAddr, false), tcpEstablished, 1000, 7) +
				tcpLine(1, hexAddrPort(agentAddr, false), hexAddrPort(clientAddr, false), tcpEstablished, 1000, 8),
			want: []tcpSocket{
				{local: clientAddr, remote: agentAddr, state: tcpEstablished, uid: 1000, inode: 7},
				{local: agentAddr, remote: clientAddr, state: tcpEstablished, uid: 1000, inode: 8},
			},
		},
		{
			name: "the header and lines that don't parse are skipped",
			data: tcpHeader + "   0: 0100007F 0100007F:1C4F 01\n" + "   1: zz00007F:D431 0100007F:1C4F 01 0:0 0:0 0 1000 0 9\n" +
				tcpLine(2, hexAddrPort(clientAddr, false), hexAddrPort(agentAddr, false), tcpEstablished, 1000, 10),
			want: []tcpSocket{{local: clientAddr, remote: agentAddr, state: tcpEstablished, uid: 1000, inode: 10}},
		},
		{
			name: "a file with only its header has no sockets",
			data: tcpHeader,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.literal && !littleEndian {
				t.Skip("the line is a little-endian machine's")
			}
			got := parseTCP([]byte(tt.data))
			if len(got) != len(tt.want) {
				t.Fatalf("parseTCP = %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("socket %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// fakeConnProc is a /proc tree for loopbackOwner: processes with stat lines
// and descriptor links, and the tcp and tcp6 tables.
type fakeConnProc struct {
	tcp, tcp6 string // "" leaves the file out
	// fds holds each process's descriptor links, by pid. A process listed
	// with nil links has a fd entry that isn't a directory, as when its
	// descriptors can't be read.
	fds map[string][]string
}

func (f fakeConnProc) write(t *testing.T) string {
	t.Helper()
	stats := map[string]string{"1": statLine("1", "systemd", "S", "0", "1", "0", "4194560", "100")}
	exes := map[string]string{"1": "/usr/lib/systemd/systemd"}
	for pid := range f.fds {
		stats[pid] = statLine(pid, "wakatime-cli-li", "S", "1", pid, "0", "4194560", "500")
		exes[pid] = "/home/u/.wakatime/wakatime-cli-linux-amd64"
	}
	root := fakeProc(t, "1763575080", stats, exes)
	if err := os.MkdirAll(filepath.Join(root, "net"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"tcp": f.tcp, "tcp6": f.tcp6} {
		if data == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(root, "net", name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for pid, links := range f.fds {
		fd := filepath.Join(root, pid, "fd")
		if links == nil {
			if err := os.WriteFile(fd, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(fd, 0o700); err != nil {
			t.Fatal(err)
		}
		for i, l := range links {
			if err := os.Symlink(l, filepath.Join(fd, strconv.Itoa(i))); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

func TestLoopbackOwnerFakeProc(t *testing.T) {
	const me = 1000
	client := hexAddrPort(clientAddr, false)
	agent := hexAddrPort(agentAddr, false)
	// The agent's own end of the connection, which every case lists too.
	agentEnd := tcpLine(1, agent, client, tcpEstablished, me, 4000)
	tests := []struct {
		name           string
		proc           fakeConnProc
		client, server netip.AddrPort
		want           int // 0 for an error
	}{
		{
			name: "the one process with a descriptor linked to the socket owns it",
			proc: fakeConnProc{
				tcp: tcpHeader + tcpLine(0, client, agent, tcpEstablished, me, 4001) + agentEnd,
				fds: map[string][]string{
					"300": {"/dev/null", "pipe:[77]", "socket:[4001]"},
					"301": {"socket:[4000]", "/home/u/notes.txt"},
				},
			},
			client: clientAddr, server: agentAddr, want: 300,
		},
		{
			name: "IPv4-mapped addresses given to the lookup are unmapped",
			proc: fakeConnProc{
				tcp: tcpHeader + tcpLine(0, client, agent, tcpEstablished, me, 4001) + agentEnd,
				fds: map[string][]string{"300": {"socket:[4001]"}},
			},
			client: netip.MustParseAddrPort("[::ffff:127.0.0.1]:54321"), server: netip.MustParseAddrPort("[::ffff:127.0.0.1]:7247"), want: 300,
		},
		{
			name: "a dual-stack socket's row in tcp6 counts",
			proc: fakeConnProc{
				tcp:  tcpHeader + agentEnd,
				tcp6: tcpHeader + tcpLine(0, hexAddrPort(netip.MustParseAddrPort("[::ffff:127.0.0.1]:54321"), true), hexAddrPort(netip.MustParseAddrPort("[::ffff:127.0.0.1]:7247"), true), tcpEstablished, me, 4002),
				fds:  map[string][]string{"302": {"socket:[4002]"}},
			},
			client: clientAddr, server: agentAddr, want: 302,
		},
		{
			name: "a kernel without tcp6 still finds IPv4 connections",
			proc: fakeConnProc{
				tcp: tcpHeader + tcpLine(0, client, agent, tcpEstablished, me, 4001) + agentEnd,
				fds: map[string][]string{"300": {"socket:[4001]"}},
			},
			client: clientAddr, server: agentAddr, want: 300,
		},
		{
			name: "a process whose descriptors can't be read is passed over",
			proc: fakeConnProc{
				tcp: tcpHeader + tcpLine(0, client, agent, tcpEstablished, me, 4001) + agentEnd,
				fds: map[string][]string{"300": {"socket:[4001]"}, "303": nil},
			},
			client: clientAddr, server: agentAddr, want: 300,
		},
		{
			name: "a socket two processes hold has no one owner",
			proc: fakeConnProc{
				tcp: tcpHeader + tcpLine(0, client, agent, tcpEstablished, me, 4001) + agentEnd,
				fds: map[string][]string{"300": {"socket:[4001]"}, "304": {"socket:[4001]"}},
			},
			client: clientAddr, server: agentAddr,
		},
		{
			name: "a socket no process holds has no owner",
			proc: fakeConnProc{
				tcp: tcpHeader + tcpLine(0, client, agent, tcpEstablished, me, 4001) + agentEnd,
				fds: map[string][]string{"300": {"socket:[400]"}},
			},
			client: clientAddr, server: agentAddr,
		},
		{
			// readlink cuts a link to its buffer's length, so with a buffer
			// no longer than the socket's link this one would read as it.
			name: "a link that is the socket's with a byte more doesn't match",
			proc: fakeConnProc{
				tcp: tcpHeader + tcpLine(0, client, agent, tcpEstablished, me, 400) + agentEnd,
				fds: map[string][]string{"300": {"socket:[400]0"}},
			},
			client: clientAddr, server: agentAddr,
		},
		{
			name: "an established row with inode 0, an orphaned socket, has no owner",
			proc: fakeConnProc{
				tcp: tcpHeader + tcpLine(0, client, agent, tcpEstablished, me, 0) + agentEnd,
				fds: map[string][]string{"300": {"socket:[0]"}},
			},
			client: clientAddr, server: agentAddr,
		},
		{
			name: "a connection in CLOSE_WAIT isn't established, so it has no owner",
			proc: fakeConnProc{
				tcp: tcpHeader + tcpLine(0, client, agent, 0x08, me, 4001) + agentEnd,
				fds: map[string][]string{"300": {"socket:[4001]"}},
			},
			client: clientAddr, server: agentAddr,
		},
		{
			name: "another user's connection has no owner here",
			proc: fakeConnProc{
				tcp: tcpHeader + tcpLine(0, client, agent, tcpEstablished, me+1, 4001) + agentEnd,
				fds: map[string][]string{"300": {"socket:[4001]"}},
			},
			client: clientAddr, server: agentAddr,
		},
		{
			name: "the server's own end isn't a client end",
			proc: fakeConnProc{
				tcp: tcpHeader + agentEnd,
				fds: map[string][]string{"301": {"socket:[4000]"}},
			},
			client: clientAddr, server: agentAddr,
		},
		{
			name: "an address that isn't loopback is refused",
			proc: fakeConnProc{
				tcp: tcpHeader + tcpLine(0, hexAddrPort(netip.MustParseAddrPort("10.0.0.5:54321"), false), agent, tcpEstablished, me, 4001),
				fds: map[string][]string{"300": {"socket:[4001]"}},
			},
			client: netip.MustParseAddrPort("10.0.0.5:54321"), server: agentAddr,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := tt.proc.write(t)
			pid, procs, err := loopbackOwner(root, me, tt.client, tt.server)
			if tt.want == 0 {
				if err == nil {
					t.Fatalf("loopbackOwner = %d, want an error", pid)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if pid != tt.want {
				t.Errorf("loopbackOwner = %d, want %d", pid, tt.want)
			}
			if _, ok := platform.NewTable(platform.Linux, procs).Get(pid); !ok {
				t.Errorf("the table loopbackOwner read lacks process %d", pid)
			}
		})
	}
}
