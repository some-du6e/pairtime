// SPDX-License-Identifier: AGPL-3.0-or-later

package peer

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/platform"
)

// Variables that make this test binary a child process (TestMain).
const (
	// connectEnv holds an address. The child connects to it, writes its pid
	// on the connection and reads until the connection closes.
	connectEnv = "PAIRTIME_PEER_CONNECT"
	// spawnEnv holds the path of a program, a copy of this test binary. The
	// child runs it without spawnEnv, so the program connects, and waits for
	// it: that puts the child above the program in its parent chain, as an
	// editor's plugin is above the wakatime-cli it runs.
	spawnEnv = "PAIRTIME_PEER_SPAWN"
)

func TestMain(m *testing.M) {
	if exe := os.Getenv(spawnEnv); exe != "" {
		os.Exit(spawnChild(exe))
	}
	if addr := os.Getenv(connectEnv); addr != "" {
		os.Exit(connectChild(addr))
	}
	os.Exit(m.Run())
}

func spawnChild(exe string) int {
	cmd := exec.Command(exe)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, spawnEnv+"=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "peer spawn child:", err)
		return 1
	}
	return 0
}

func connectChild(addr string) int {
	c, err := net.Dial("tcp4", addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "peer connect child:", err)
		return 1
	}
	fmt.Fprintf(c, "%d\n", os.Getpid())
	io.Copy(io.Discard, c)
	return 0
}

// copyExe copies this test binary into dir as name, with .exe on Windows,
// so the process table names a process that runs it name.
func copyExe(t *testing.T, dir, name string) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == platform.Windows {
		name += ".exe"
	}
	dst := filepath.Join(dir, name)
	src, err := os.Open(self)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return dst
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

// dialSelf opens a connection from this process to ln, and returns the
// end ln accepted.
func dialSelf(t *testing.T, ln *net.TCPListener) *net.TCPConn {
	t.Helper()
	d, err := net.Dial("tcp4", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return acceptConn(t, ln)
}

// runChild runs exe, which connects to ln, or with under set runs under,
// which runs exe. It returns the command it started, the connection ln
// accepted and the pid the connecting process wrote.
func runChild(t *testing.T, ln *net.TCPListener, exe, under string) (*exec.Cmd, *net.TCPConn, int) {
	t.Helper()
	env := append(os.Environ(), connectEnv+"="+ln.Addr().String())
	cmd := exec.Command(exe)
	if under != "" {
		cmd = exec.Command(under)
		env = append(env, spawnEnv+"="+exe)
	}
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Registered after the copies' TempDir, so it runs first: on Windows a
	// running program's file can't be removed.
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
	conn := acceptConn(t, ln)
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("reading the child's pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("the child wrote %q, not a pid", line)
	}
	if under == "" && pid != cmd.Process.Pid {
		t.Fatalf("the child wrote pid %d, want %d", pid, cmd.Process.Pid)
	}
	return cmd, conn, pid
}

// TestRealConnections runs both checks over real loopback connections,
// from this process and from copies of this test binary named as the
// checks name processes (PLAN, WP-OS13). ci.yml runs it on Linux; the
// lookups' own tests run on Windows and macOS under internal/platform.
func TestRealConnections(t *testing.T) {
	dir := t.TempDir()
	waka := "wakatime-cli-" + runtime.GOOS + "-" + runtime.GOARCH
	exes := map[string]string{
		"code":   copyExe(t, dir, "code"),
		waka:     copyExe(t, dir, waka),
		"claude": copyExe(t, dir, "claude"),
	}
	c := New(Options{})

	// Whether an editor runs this test, as when it runs in an editor's
	// terminal: then a wakatime-cli this test starts has an editor above it.
	selfClient, selfServer, err := ConnAddrs(dialSelf(t, listenLoopback(t)))
	if err != nil {
		t.Fatal(err)
	}
	editorAbove := false
	for _, p := range c.Heartbeat(selfClient, selfServer).Chain {
		if e, ok := platform.Identify(runtime.GOOS, p); ok && e.Role == platform.RoleIDE {
			editorAbove = true
		}
	}

	tests := []struct {
		name string
		// client is the copy that connects, by name; "" means this process.
		client string
		// under is the copy that runs client, "" for none.
		under string
		// gone kills the client before the check.
		gone bool
		// otlp runs the OTLP check, with named answering names, instead of
		// Heartbeat.
		otlp  bool
		names bool
		// needsNoEditor skips the case when an editor runs this test.
		needsNoEditor bool
		want          Reason
	}{
		{
			name: "this process's own connection fails the heartbeat check, since the test binary isn't wakatime-cli",
			want: NotWakaTime,
		},
		{
			name: "this process's own connection fails the OTLP check, since the test binary isn't a harness, though named",
			otlp: true, names: true, want: NotHarness,
		},
		{
			name:   "a wakatime-cli that a process named code started passes, with that process as its editor",
			client: waka, under: "code", want: Passed,
		},
		{
			name:   "the same wakatime-cli fails the OTLP check, since it isn't a harness",
			client: waka, under: "code", otlp: true, names: true, want: NotHarness,
		},
		{
			name:   "a wakatime-cli this test started fails, since no editor started it",
			client: waka, needsNoEditor: true, want: NoEditor,
		},
		{
			name:   "a connection whose wakatime-cli has exited has no owner",
			client: waka, under: "code", gone: true, want: NoOwner,
		},
		{
			name:   "a process named claude that the agent named passes the OTLP check",
			client: "claude", otlp: true, names: true, want: Passed,
		},
		{
			name:   "a process named claude that nothing named fails the OTLP check",
			client: "claude", otlp: true, want: NotNamed,
		},
		{
			name:   "a process named claude fails the heartbeat check, since it isn't wakatime-cli",
			client: "claude", want: NotWakaTime,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.needsNoEditor && editorAbove {
				t.Skip("an editor runs this test, so a wakatime-cli it starts has an editor above it")
			}
			ln := listenLoopback(t)
			var conn *net.TCPConn
			want := os.Getpid()
			var cmd *exec.Cmd
			if tt.client == "" {
				conn = dialSelf(t, ln)
			} else {
				under := ""
				if tt.under != "" {
					under = exes[tt.under]
				}
				cmd, conn, want = runChild(t, ln, exes[tt.client], under)
			}
			if tt.gone {
				// The client is the grandchild; its parent waits for it, so
				// killing the client lets the parent exit too.
				p, err := os.FindProcess(want)
				if err != nil {
					t.Fatal(err)
				}
				p.Kill()
				cmd.Wait()
			}
			client, server, err := ConnAddrs(conn)
			if err != nil {
				t.Fatal(err)
			}
			var asked []platform.ID
			var r Result
			if tt.otlp {
				r = c.OTLP(client, server, func(id platform.ID) bool {
					asked = append(asked, id)
					return tt.names
				})
			} else {
				r = c.Heartbeat(client, server)
			}
			if r.Reason != tt.want {
				t.Fatalf("= %s (%v), want %s; chain %v", r.Reason, r.Err, tt.want, r.Chain)
			}
			if tt.want == NoOwner {
				return
			}
			if r.PID != want || r.Chain[0].PID != want {
				t.Errorf("PID %d, Chain[0] %v; want the client, %d", r.PID, r.Chain[0], want)
			}
			if tt.client != "" && !strings.EqualFold(strings.TrimSuffix(r.Chain[0].Name, ".exe"), tt.client) {
				t.Errorf("the client's name is %q, want %q", r.Chain[0].Name, tt.client)
			}
			if tt.under != "" && (len(r.Chain) < 2 || r.Chain[1].PID != cmd.Process.Pid) {
				t.Errorf("Chain %v, want the client's parent to be %d", r.Chain, cmd.Process.Pid)
			}
			if tt.want == Passed && !tt.otlp && r.Editor != 1 {
				t.Errorf("Editor = %d, want 1, the process named code", r.Editor)
			}
			if tt.otlp && tt.want != NotHarness && (len(asked) != 1 || asked[0].PID != want) {
				t.Errorf("named was asked about %v, want once about %d", asked, want)
			}
		})
	}
}

func TestConnAddrs(t *testing.T) {
	tests := []struct {
		name string
		conn func(t *testing.T) (net.Conn, netip.AddrPort, netip.AddrPort)
		err  bool
	}{
		{
			name: "an accepted TCP connection's remote address is the client's and its local address the server's",
			conn: func(t *testing.T) (net.Conn, netip.AddrPort, netip.AddrPort) {
				ln := listenLoopback(t)
				d, err := net.Dial("tcp4", ln.Addr().String())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { d.Close() })
				conn := acceptConn(t, ln)
				return conn, d.LocalAddr().(*net.TCPAddr).AddrPort(), ln.Addr().(*net.TCPAddr).AddrPort()
			},
		},
		{
			name: "a connection that isn't TCP has no addresses to check",
			conn: func(t *testing.T) (net.Conn, netip.AddrPort, netip.AddrPort) {
				a, b := net.Pipe()
				t.Cleanup(func() { a.Close(); b.Close() })
				return a, netip.AddrPort{}, netip.AddrPort{}
			},
			err: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, wantClient, wantServer := tt.conn(t)
			client, server, err := ConnAddrs(conn)
			if (err != nil) != tt.err {
				t.Fatalf("ConnAddrs error = %v, want an error: %v", err, tt.err)
			}
			if tt.err {
				return
			}
			if client != unmap(wantClient) || server != unmap(wantServer) {
				t.Errorf("ConnAddrs = %v, %v; want %v, %v", client, server, wantClient, wantServer)
			}
		})
	}
}
