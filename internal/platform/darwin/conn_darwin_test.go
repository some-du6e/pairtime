// SPDX-License-Identifier: AGPL-3.0-or-later

package darwin

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/platform"
)

// connChildEnv, in this test binary's environment, makes it a child that
// connects to the address the variable holds (connChild). TestMain runs it.
const connChildEnv = "PAIRTIME_DARWIN_CONN_CHILD"

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

// TestLoopbackOwnerReal opens real loopback connections and checks the pid
// LoopbackOwner finds against the OS's own (PLAN, WP-OS13).
func TestLoopbackOwnerReal(t *testing.T) {
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
				describeSockets(t, want)
				return
			}
			if pid != want {
				t.Errorf("LoopbackOwner(%v, %v) = %d, want %d", client, server, pid, want)
				describeSockets(t, want)
			}
			chain := platform.NewTable(platform.Darwin, procs).Chain(pid)
			if len(chain) == 0 {
				t.Fatalf("the table LoopbackOwner read lacks process %d", pid)
			}
			if parent != 0 && (len(chain) < 2 || chain[1].PID != parent) {
				t.Errorf("the table gives process %d the chain %v, want its parent %d", pid, chain, parent)
			}
		})
	}
}

// describeSockets logs what libproc says about each socket descriptor of
// process pid, so that a layout that parseSocketFDInfo reads wrongly shows
// in the log: the size, the parsed fields and the bytes around them.
func describeSockets(t *testing.T, pid int) {
	t.Helper()
	src, err := osConnSource()
	if err != nil {
		t.Logf("libproc: %v", err)
		return
	}
	fds, err := src.socketFDs(pid)
	if err != nil {
		t.Logf("socket descriptors of %d: %v", pid, err)
		return
	}
	for _, fd := range fds {
		b, err := src.socketInfo(pid, fd)
		if err != nil {
			t.Logf("fd %d: %v", fd, err)
			continue
		}
		s, ok, err := parseSocketFDInfo(b)
		t.Logf("fd %d: %d bytes, parsed %+v ok=%v err=%v", fd, len(b), s, ok, err)
		if len(b) > offTCPState+4 {
			t.Logf("fd %d: soi_kind and on: %s", fd, hex.EncodeToString(b[offKind:offTCPState+4]))
		}
	}
}
