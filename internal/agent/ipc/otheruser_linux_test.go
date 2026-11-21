// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package ipc_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/internal/agent/ipc"
	"github.com/some-du6e/pairtime/internal/platform/clock"
)

// nobody is the uid and gid of the second user: nobody and nogroup on
// Ubuntu.
const nobody = 65534

// sockEnv names the socket the nobody-sender part connects to.
const sockEnv = "PAIRTIME_TEST_SOCKET"

func init() {
	children["nobody-sender"] = nobodySender
	children["nobody-squatter"] = nobodySquatter
}

// nobodySender connects to the socket that sockEnv names and sends one
// message by hand, without asking who listens, as another user trying to
// slip a message in would. It prints "connected", then "acked" or
// "refused".
func nobodySender() int {
	c, err := net.DialTimeout("unix", os.Getenv(sockEnv), 10*time.Second)
	if err != nil {
		fmt.Println("no connection:", err)
		return 0
	}
	defer c.Close()
	fmt.Println("connected")
	c.SetDeadline(time.Now().Add(10 * time.Second))
	b, err := inbox.New("test", nil).Encode()
	if err != nil {
		fmt.Println("error:", err)
		return 1
	}
	if err := ipc.WriteFrame(c, b, inbox.MaxMessage); err != nil {
		fmt.Println("refused:", err)
		return 0
	}
	if r, err := ipc.ReadFrame(c, 4<<10); err != nil {
		fmt.Println("refused:", err)
	} else {
		fmt.Printf("acked: %s\n", r)
	}
	return 0
}

// nobodySquatter is a socket of another user's where an agent's would be,
// as someone waiting for hooks whose agent is down would make one. It is a
// bare listener in a directory of its own that anyone may enter, so a
// sender's own check is all that keeps a message from it. It prints
// "listening <path>", then "read <n> bytes" for each connection once its
// sender hangs up, until its stdin closes. It never answers.
func nobodySquatter() int {
	dir, err := os.MkdirTemp("", "pt")
	if err != nil {
		fmt.Println("error:", err)
		return 1
	}
	defer os.RemoveAll(dir)
	if err := os.Chmod(dir, 0o755); err != nil {
		fmt.Println("error:", err)
		return 1
	}
	path := filepath.Join(dir, ipc.SocketName)
	ln, err := net.Listen("unix", path)
	if err != nil {
		fmt.Println("error:", err)
		return 1
	}
	if err := os.Chmod(path, 0o777); err != nil {
		fmt.Println("error:", err)
		return 1
	}
	go func() {
		io.Copy(io.Discard, os.Stdin)
		ln.Close()
	}()
	fmt.Println("listening", path)
	for {
		c, err := ln.Accept()
		if err != nil {
			return 0
		}
		// Longer than a sender waits for an ack, so one that wrote and
		// then waited is counted once it gives up and hangs up.
		c.SetReadDeadline(time.Now().Add(30 * time.Second))
		n, _ := io.Copy(io.Discard, c)
		c.Close()
		fmt.Printf("read %d bytes\n", n)
	}
}

// nobodyCmd runs part of the test binary at exe as nobody.
func nobodyCmd(ctx context.Context, exe, part string, env ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, exe, "-test.run=^$")
	cmd.Env = append([]string{childEnv + "=" + part, "TMPDIR=/tmp", "PATH=/usr/bin:/bin"}, env...)
	cmd.Dir = "/"
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: nobody, Gid: nobody}}
	return cmd
}

// exeForNobody copies the test binary to where nobody can run it: go test
// builds it in a directory only its user can open.
func exeForNobody(t *testing.T) string {
	t.Helper()
	src, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := shortDir(t)
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "ipc.test")
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return dst
}

// countSink counts what it takes.
type countSink struct {
	mu sync.Mutex
	n  int
}

func (c *countSink) Deliver(inbox.Delivery) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	return nil
}

func (c *countSink) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// TestOtherUser checks, with a real second user, that no other uid can
// hand the agent a message or take one meant for it (docs/PLAN.md, WP-AG2:
// "on Linux a socket owned by another uid is refused"). Acting as a second
// user takes root, so go test skips it, and ci.yml runs it with sudo.
func TestOtherUser(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root, to run a process as another user; ci.yml runs this test with sudo")
	}
	exe := exeForNobody(t)
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "the agent refuses a message from another uid that got as far as its socket",
			run: func(t *testing.T) {
				dir := shortDir(t)
				ep, err := ipc.EndpointFor(home.Dirs{Data: dir, Config: dir, Runtime: dir})
				if err != nil {
					t.Fatal(err)
				}
				srv, err := ipc.Listen(ep, ipc.Options{})
				if err != nil {
					t.Fatal(err)
				}
				sink := &countSink{}
				served := make(chan error, 1)
				go func() { served <- srv.Serve(sink) }()
				defer func() {
					srv.Close()
					<-served
				}()
				// Open the directory and the socket to everyone, so that the
				// agent's own check is all that stands in nobody's way.
				if err := os.Chmod(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(ep.Socket, 0o777); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				out, err := nobodyCmd(ctx, exe, "nobody-sender", sockEnv+"="+ep.Socket).CombinedOutput()
				if err != nil {
					t.Fatalf("the sender failed: %v\n%s", err, out)
				}
				if !strings.Contains(string(out), "connected\n") || !strings.Contains(string(out), "refused") {
					t.Errorf("the sender said %q, want it to connect and be refused", out)
				}
				if n := sink.count(); n != 0 {
					t.Errorf("the agent took %d messages from nobody", n)
				}
				if n := srv.Refused(); n != 1 {
					t.Errorf("Refused() = %d, want 1", n)
				}
			},
		},
		{
			name: "a sender or a question writes nothing to a socket another uid listens on, and the message is spooled instead",
			run: func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				cmd := nobodyCmd(ctx, exe, "nobody-squatter")
				cmd.Stderr = os.Stderr
				stdin, err := cmd.StdinPipe()
				if err != nil {
					t.Fatal(err)
				}
				stdout, err := cmd.StdoutPipe()
				if err != nil {
					t.Fatal(err)
				}
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				defer cmd.Wait()
				defer stdin.Close()
				r := bufio.NewReader(stdout)
				line, err := r.ReadString('\n')
				path, ok := strings.CutPrefix(strings.TrimSpace(line), "listening ")
				if err != nil || !ok {
					t.Fatalf("nobody's socket said %q (%v), want it to be listening", line, err)
				}
				// readNothing waits for nobody's report on the connection
				// that what made, and wants no byte of it read.
				readNothing := func(what string) {
					t.Helper()
					line, err := r.ReadString('\n')
					if err != nil {
						t.Fatalf("nobody's socket said nothing of the connection %s made: %v", what, err)
					}
					if got := strings.TrimSpace(line); got != "read 0 bytes" {
						t.Errorf("after %s, nobody's socket said %q, want it to have read nothing", what, got)
					}
				}

				ep := ipc.Endpoint{Socket: path, Inbox: filepath.Join(shortDir(t), "inbox")}
				dctx, dcancel := context.WithTimeout(ctx, 10*time.Second)
				defer dcancel()
				if c, err := ipc.Dial(dctx, ep); !errors.Is(err, ipc.ErrNotAgent) {
					if c != nil {
						c.Close()
					}
					t.Errorf("Dial = %v, want ErrNotAgent", err)
				}
				readNothing("Dial")
				if via, err := ipc.Send(ep, inbox.New("test", nil), 10*time.Second); err != nil || via != inbox.ViaInbox {
					t.Errorf("Send = %q, %v; want the message spooled instead", via, err)
				}
				readNothing("Send")
				if ans, err := ipc.Ask(dctx, ep, ipc.AskLastSession); !errors.Is(err, ipc.ErrNotAgent) {
					t.Errorf("Ask = %s, %v; want ErrNotAgent", ans, err)
				}
				readNothing("Ask")
			},
		},
		{
			name: "the agent won't put its socket in a directory another uid owns",
			run: func(t *testing.T) {
				dir := shortDir(t)
				run := filepath.Join(dir, "run")
				if err := os.Mkdir(run, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chown(run, nobody, nobody); err != nil {
					t.Fatal(err)
				}
				ep, err := ipc.EndpointFor(home.Dirs{Data: dir, Config: dir, Runtime: run})
				if err != nil {
					t.Fatal(err)
				}
				srv, err := ipc.Listen(ep, ipc.Options{})
				if err == nil {
					srv.Close()
					t.Fatal("Listen put its socket in nobody's directory")
				}
				if !strings.Contains(err.Error(), fmt.Sprint(nobody)) {
					t.Errorf("Listen = %v, want it to name the directory's owner", err)
				}
				if _, err := os.Lstat(ep.Socket); !os.IsNotExist(err) {
					t.Errorf("a socket was made anyway: %v", err)
				}
			},
		},
		{
			name: "the agent won't follow a symbolic link that another uid put where its socket's directory goes",
			run: func(t *testing.T) {
				// The link's target is root's own, so a check made through
				// the link would pass, and its chmod would land on the
				// target.
				dir := shortDir(t)
				target := filepath.Join(dir, "target")
				if err := os.Mkdir(target, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(target, 0o755); err != nil {
					t.Fatal(err)
				}
				link := filepath.Join(dir, "run")
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
				if err := os.Lchown(link, nobody, nobody); err != nil {
					t.Fatal(err)
				}
				ep, err := ipc.EndpointFor(home.Dirs{Data: dir, Config: dir, Runtime: link})
				if err != nil {
					t.Fatal(err)
				}
				srv, err := ipc.Listen(ep, ipc.Options{})
				if err == nil {
					srv.Close()
					t.Fatal("Listen put its socket behind nobody's link")
				}
				if !strings.Contains(err.Error(), "symbolic link") {
					t.Errorf("Listen = %v, want it to refuse the link", err)
				}
				fi, err := os.Stat(target)
				if err != nil {
					t.Fatal(err)
				}
				if perm := fi.Mode().Perm(); perm != 0o755 {
					t.Errorf("the link's target has mode %v now, want it left at 0755", perm)
				}
				if _, err := os.Lstat(filepath.Join(target, ipc.SocketName)); !os.IsNotExist(err) {
					t.Errorf("a socket was made behind the link: %v", err)
				}
			},
		},
		{
			name: "a sender won't spool into an inbox that another uid owns",
			run: func(t *testing.T) {
				in := filepath.Join(shortDir(t), "inbox")
				if err := os.Mkdir(in, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chown(in, nobody, nobody); err != nil {
					t.Fatal(err)
				}
				// No agent listens, so Send falls back to the spool.
				ep := ipc.Endpoint{Socket: filepath.Join(shortDir(t), ipc.SocketName), Inbox: in}
				if via, err := ipc.Send(ep, inbox.New("test", nil), time.Second); err == nil {
					t.Errorf("Send = %q, nil; want it to fail rather than spool into nobody's inbox", via)
				}
				entries, err := os.ReadDir(in)
				if err != nil {
					t.Fatal(err)
				}
				if len(entries) != 0 {
					t.Errorf("nobody's inbox has %d files now, want none", len(entries))
				}
			},
		},
		{
			name: "a sender won't append to a spool file that another uid owns",
			run: func(t *testing.T) {
				in := filepath.Join(shortDir(t), "inbox")
				if err := os.Mkdir(in, 0o700); err != nil {
					t.Fatal(err)
				}
				spool := filepath.Join(in, inbox.SpoolName)
				if err := os.WriteFile(spool, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chown(spool, nobody, nobody); err != nil {
					t.Fatal(err)
				}
				if err := inbox.Append(in, inbox.New("test", nil)); err == nil {
					t.Error("Append wrote to nobody's spool file")
				}
				fi, err := os.Stat(spool)
				if err != nil {
					t.Fatal(err)
				}
				if fi.Size() != 0 {
					t.Errorf("nobody's spool file has %d bytes, want 0", fi.Size())
				}
			},
		},
		{
			name: "a drain delivers nothing from a spool file that another uid wrote",
			run: func(t *testing.T) {
				in := filepath.Join(shortDir(t), "inbox")
				if err := os.Mkdir(in, 0o700); err != nil {
					t.Fatal(err)
				}
				b, err := inbox.New("test", nil).Encode()
				if err != nil {
					t.Fatal(err)
				}
				name := filepath.Join(in, "spool-00000000000000000001-0badc0de.drain")
				if err := os.WriteFile(name, []byte("\n"+string(b)+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chown(name, nobody, nobody); err != nil {
					t.Fatal(err)
				}
				sink := &countSink{}
				res, err := inbox.Drain(in, sink, clock.Now)
				if err == nil {
					t.Error("Drain passed over nobody's file without a word")
				}
				if n := sink.count(); n != 0 || res.Delivered != 0 {
					t.Errorf("Drain = %+v and the sink took %d messages, want none", res, n)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}
