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
)

// nobody is the uid and gid of the second user: nobody and nogroup on
// Ubuntu.
const nobody = 65534

// sockEnv names the socket the nobody-sender part connects to.
const sockEnv = "PAIRTIME_TEST_SOCKET"

func init() {
	children["nobody-sender"] = nobodySender
	children["nobody-agent"] = nobodyAgent
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

// printSink prints each message it takes.
type printSink struct{ mu sync.Mutex }

func (p *printSink) Deliver(d inbox.Delivery) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Println("got", d.ID)
	return nil
}

// nobodyAgent is an agent's socket in a directory of its own. It prints
// "listening <path>" once it serves, then "got <id>" for each message it
// takes, until its stdin closes.
func nobodyAgent() int {
	dir, err := os.MkdirTemp("", "pt")
	if err != nil {
		fmt.Println("error:", err)
		return 1
	}
	defer os.RemoveAll(dir)
	ep, err := ipc.EndpointFor(home.Dirs{Data: dir, Config: dir, Runtime: dir})
	if err != nil {
		fmt.Println("error:", err)
		return 1
	}
	srv, err := ipc.Listen(ep, ipc.Options{})
	if err != nil {
		fmt.Println("error:", err)
		return 1
	}
	go srv.Serve(&printSink{})
	fmt.Println("listening", ep.Socket)
	io.Copy(io.Discard, os.Stdin)
	srv.Close()
	return 0
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
			name: "a sender refuses to hand a message to a socket another uid listens on",
			run: func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				cmd := nobodyCmd(ctx, exe, "nobody-agent")
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
					t.Fatalf("nobody's agent said %q (%v), want it to be listening", line, err)
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
				if via, err := ipc.Send(ep, inbox.New("test", nil), 10*time.Second); err != nil || via != inbox.ViaInbox {
					t.Errorf("Send = %q, %v; want the message spooled instead", via, err)
				}

				stdin.Close()
				rest, err := io.ReadAll(r)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(rest), "got ") {
					t.Errorf("nobody's agent got a message: %s", rest)
				}
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
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}
