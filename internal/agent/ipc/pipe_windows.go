// SPDX-License-Identifier: AGPL-3.0-or-later

package ipc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The named pipe is the agent's fallback on Windows builds whose AF_UNIX
// fails the self-test (CP 6.3; DESIGN 3.3). It is built on
// golang.org/x/sys/windows alone. Every instance is opened for overlapped
// I/O: the agent waits for a client with an event it can cancel, and then
// hands the handle to os.NewFile, which since Go 1.25 puts such handles on
// the runtime's I/O completion port, so reads and writes have deadlines and
// hold no thread.

// pipeBuffer is the size of each instance's buffer, either way.
const pipeBuffer = 64 << 10

// maxConnectFailures is how many clients in a row may hang up before the
// agent finished connecting them before Accept gives up.
const maxConnectFailures = 10

// pipeName is the agent's named pipe: \\.\pipe\pairtime-<SID> (CP 6.3),
// then a hash of the runtime directory, like the name of the agent's lock
// (package home), so that two pairtime installations of one user
// (PAIRTIME_HOME) don't share a pipe.
func pipeName(runtimeDir string) (string, error) {
	me, err := self()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(runtimeDir))))
	return `\\.\pipe\pairtime-` + me.sid + "-" + hex.EncodeToString(sum[:8]), nil
}

// pipeSecurity is the pipe's security: owned by this user, whatever
// elevation the agent runs at, with a DACL that lets this user in and
// nobody else.
func pipeSecurity(me identity) (*windows.SecurityAttributes, error) {
	sd, err := windows.SecurityDescriptorFromString("O:" + me.sid + "D:P(A;;GA;;;" + me.sid + ")")
	if err != nil {
		return nil, err
	}
	sa := &windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(*sa))
	return sa, nil
}

// createPipe makes one instance of the pipe. The first one carries
// FILE_FLAG_FIRST_PIPE_INSTANCE, so it fails when another process made a
// pipe of that name first, perhaps to read what hooks send.
func createPipe(name string, sa *windows.SecurityAttributes, first bool) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return windows.InvalidHandle, err
	}
	flags := uint32(windows.PIPE_ACCESS_DUPLEX | windows.FILE_FLAG_OVERLAPPED)
	if first {
		flags |= windows.FILE_FLAG_FIRST_PIPE_INSTANCE
	}
	mode := uint32(windows.PIPE_TYPE_BYTE | windows.PIPE_READMODE_BYTE | windows.PIPE_WAIT | windows.PIPE_REJECT_REMOTE_CLIENTS)
	h, err := windows.CreateNamedPipe(p, flags, mode, windows.PIPE_UNLIMITED_INSTANCES, pipeBuffer, pipeBuffer, 0, sa)
	if err != nil {
		return windows.InvalidHandle, os.NewSyscallError("CreateNamedPipe", err)
	}
	return h, nil
}

func listenPipe(name string) (net.Listener, error) {
	me, err := self()
	if err != nil {
		return nil, err
	}
	sa, err := pipeSecurity(me)
	if err != nil {
		return nil, err
	}
	h, err := createPipe(name, sa, true)
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_PIPE_BUSY) {
			return nil, fmt.Errorf("ipc: another process has the pipe %s already, so the agent won't use it: %w", name, err)
		}
		return nil, err
	}
	stop, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		windows.CloseHandle(h)
		return nil, os.NewSyscallError("CreateEvent", err)
	}
	return &pipeListener{name: name, sa: sa, stop: stop, next: h}, nil
}

// pipeListener accepts clients of the agent's named pipe.
type pipeListener struct {
	name string
	sa   *windows.SecurityAttributes
	stop windows.Handle // a manual-reset event that Close sets

	mu      sync.Mutex
	next    windows.Handle // the instance the next client opens, or 0
	closed  bool
	accepts sync.WaitGroup // Accept calls under way
}

// Accept waits for a client and returns its connection, with the client's
// pid and user already asked of the OS.
func (l *pipeListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, net.ErrClosed
	}
	l.accepts.Add(1)
	h := l.next
	l.next = 0
	l.mu.Unlock()
	defer l.accepts.Done()

	for failures := 0; ; failures++ {
		if h == 0 {
			var err error
			if h, err = createPipe(l.name, l.sa, false); err != nil {
				return nil, err
			}
		}
		err := l.waitClient(h)
		if err == nil {
			break
		}
		windows.CloseHandle(h)
		h = 0
		if errors.Is(err, net.ErrClosed) || failures >= maxConnectFailures {
			return nil, err
		}
	}

	// Make the instance the next client will open before handing this one
	// over, so that clients find the pipe busy only for a moment.
	next, nextErr := createPipe(l.name, l.sa, false)
	l.mu.Lock()
	closed := l.closed
	if !closed && nextErr == nil {
		l.next = next
	}
	l.mu.Unlock()
	if closed {
		if nextErr == nil {
			windows.CloseHandle(next)
		}
		windows.CloseHandle(h)
		return nil, net.ErrClosed
	}

	c := &pipeConn{addr: pipeAddr(l.name)}
	var pid uint32
	if err := windows.GetNamedPipeClientProcessId(h, &pid); err != nil {
		c.peerErr = os.NewSyscallError("GetNamedPipeClientProcessId", err)
	} else {
		c.pid = int(pid)
		c.id, c.peerErr = pidIdentity(pid)
	}
	c.f = os.NewFile(uintptr(h), l.name)
	return c, nil
}

// waitClient waits until a client opens the instance h, or Close is
// called.
func (l *pipeListener) waitClient(h windows.Handle) error {
	ev, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return os.NewSyscallError("CreateEvent", err)
	}
	defer windows.CloseHandle(ev)
	// On the heap, where it stays put while Windows holds its address.
	ov := &windows.Overlapped{HEvent: ev}
	switch err := windows.ConnectNamedPipe(h, ov); {
	case err == nil, errors.Is(err, windows.ERROR_PIPE_CONNECTED):
		return nil // the client opened the instance before the call
	case !errors.Is(err, windows.ERROR_IO_PENDING):
		return os.NewSyscallError("ConnectNamedPipe", err)
	}
	which, err := windows.WaitForMultipleObjects([]windows.Handle{ev, l.stop}, false, windows.INFINITE)
	if err != nil || which != windows.WAIT_OBJECT_0 {
		// Close was called, or the wait failed. Cancel the connect, and
		// wait for the cancel to land before ov may go.
		windows.CancelIoEx(h, ov)
		var n uint32
		windows.GetOverlappedResult(h, ov, &n, true)
		if err != nil {
			return os.NewSyscallError("WaitForMultipleObjects", err)
		}
		return net.ErrClosed
	}
	var n uint32
	if err := windows.GetOverlappedResult(h, ov, &n, false); err != nil {
		return os.NewSyscallError("ConnectNamedPipe", err)
	}
	return nil
}

// Close stops the listener and wakes a waiting Accept. Connections it
// returned stay open.
func (l *pipeListener) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	next := l.next
	l.next = 0
	l.mu.Unlock()
	windows.SetEvent(l.stop)
	if next != 0 {
		windows.CloseHandle(next)
	}
	l.accepts.Wait()
	windows.CloseHandle(l.stop)
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr(l.name) }

// pipeAddr is a pipe's name as a net.Addr.
type pipeAddr string

func (a pipeAddr) Network() string { return "pipe" }
func (a pipeAddr) String() string  { return string(a) }

// pipeConn is one end of a connected pipe instance.
type pipeConn struct {
	f       *os.File
	addr    pipeAddr
	id      identity // the other end's user
	pid     int      // the other end's process
	peerErr error    // why id is unknown
}

func (c *pipeConn) Read(b []byte) (int, error)         { return c.f.Read(b) }
func (c *pipeConn) Write(b []byte) (int, error)        { return c.f.Write(b) }
func (c *pipeConn) Close() error                       { return c.f.Close() }
func (c *pipeConn) LocalAddr() net.Addr                { return c.addr }
func (c *pipeConn) RemoteAddr() net.Addr               { return c.addr }
func (c *pipeConn) SetDeadline(t time.Time) error      { return c.f.SetDeadline(t) }
func (c *pipeConn) SetReadDeadline(t time.Time) error  { return c.f.SetReadDeadline(t) }
func (c *pipeConn) SetWriteDeadline(t time.Time) error { return c.f.SetWriteDeadline(t) }
func (c *pipeConn) peer() (identity, int, error)       { return c.id, c.pid, c.peerErr }

// dialPipe opens the agent's pipe and checks that the process serving it
// runs as want.
func dialPipe(ctx context.Context, name string, want identity) (net.Conn, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	var h windows.Handle
	for {
		// SECURITY_IDENTIFICATION: the server may learn who this is, but
		// may not act as this user.
		h, err = windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING,
			windows.FILE_FLAG_OVERLAPPED|windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION, 0)
		if err == nil {
			break
		}
		if !errors.Is(err, windows.ERROR_PIPE_BUSY) {
			return nil, &os.PathError{Op: "open", Path: name, Err: err}
		}
		// Every instance is taken for the moment, and the agent makes a
		// new one as it accepts.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
	var pid uint32
	if err := windows.GetNamedPipeServerProcessId(h, &pid); err != nil {
		windows.CloseHandle(h)
		return nil, fmt.Errorf("%w: %w", ErrNotAgent, os.NewSyscallError("GetNamedPipeServerProcessId", err))
	}
	id, err := pidIdentity(pid)
	if err == nil && id != want {
		err = fmt.Errorf("it runs as %s", id)
	}
	if err != nil {
		windows.CloseHandle(h)
		return nil, fmt.Errorf("%w: %w", ErrNotAgent, err)
	}
	return &pipeConn{f: os.NewFile(uintptr(h), name), addr: pipeAddr(name), id: id, pid: int(pid)}, nil
}
